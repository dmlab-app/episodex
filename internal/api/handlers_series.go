package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/episodex/episodex/internal/database"
)

// handleSyncSeriesFromTVDB syncs series metadata from TVDB
func (s *Server) handleSyncSeriesFromTVDB(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	if s.tvdbClient == nil {
		s.respondError(w, http.StatusServiceUnavailable, "TVDB client not configured")
		return
	}

	// Get series tvdb_id
	var tvdbID *int
	err := s.db.QueryRow(`SELECT tvdb_id FROM series WHERE id = ?`, id).Scan(&tvdbID)
	if err != nil || tvdbID == nil {
		s.respondError(w, http.StatusNotFound, "series not found or no TVDB ID")
		return
	}

	// Fetch extended data from TVDB
	extended, err := s.tvdbClient.GetSeriesExtendedFull(*tvdbID)
	if err != nil {
		slog.Error("Failed to fetch series from TVDB", "tvdb_id", *tvdbID, "error", err)
		s.respondError(w, http.StatusInternalServerError, "failed to fetch series metadata")
		return
	}

	// Get Russian translation
	rusTrans, _ := s.tvdbClient.GetSeriesTranslation(*tvdbID, "rus")

	// Use Russian name if available
	title := extended.Name
	originalTitle := extended.OriginalName
	overview := extended.Overview

	if rusTrans != nil && rusTrans.Name != "" {
		title = rusTrans.Name
		originalTitle = extended.Name
		if rusTrans.Overview != "" {
			overview = rusTrans.Overview
		}
	}

	// Convert arrays to JSON
	genresJSON, err := json.Marshal(extended.Genres)
	if err != nil {
		slog.Error("Failed to marshal genres", "error", err)
		genresJSON = []byte("[]")
	}
	networksJSON, err := json.Marshal(extended.Networks)
	if err != nil {
		slog.Error("Failed to marshal networks", "error", err)
		networksJSON = []byte("[]")
	}
	studiosJSON, err := json.Marshal(extended.Studios)
	if err != nil {
		slog.Error("Failed to marshal studios", "error", err)
		studiosJSON = []byte("[]")
	}

	genres := string(genresJSON)
	networks := string(networksJSON)
	studios := string(studiosJSON)

	// Get content rating (prefer USA rating)
	var contentRating string
	for _, cr := range extended.ContentRatings {
		if cr.Country == "usa" || cr.Country == "US" {
			contentRating = cr.Name
			break
		}
	}
	if contentRating == "" && len(extended.ContentRatings) > 0 {
		contentRating = extended.ContentRatings[0].Name
	}

	// Update series in database
	seriesData := &database.Series{
		TVDBId:           tvdbID,
		Title:            title,
		OriginalTitle:    &originalTitle,
		Slug:             &extended.Slug,
		Overview:         &overview,
		PosterURL:        &extended.Image,
		BackdropURL:      &extended.Backdrop,
		Status:           &extended.Status,
		FirstAired:       &extended.FirstAired,
		LastAired:        &extended.LastAired,
		Year:             &extended.Year,
		Runtime:          &extended.Runtime,
		Rating:           &extended.Score,
		ContentRating:    &contentRating,
		OriginalCountry:  &extended.OriginalCountry,
		OriginalLanguage: &extended.OriginalLanguage,
		Genres:           &genres,
		Networks:         &networks,
		Studios:          &studios,
		TotalSeasons:     len(extended.Seasons),
	}

	seriesID, err := s.db.UpsertSeries(seriesData)
	if err != nil {
		slog.Error("Failed to update series", "error", err)
		s.respondError(w, http.StatusInternalServerError, "failed to update series")
		return
	}

	// Sync seasons with TVDB data
	for _, seasonInfo := range extended.Seasons {
		seasonData := &database.Season{
			SeriesID:     seriesID,
			TVDBSeasonID: &seasonInfo.ID,
			SeasonNumber: seasonInfo.Number,
			Name:         &seasonInfo.Name,
			PosterURL:    &seasonInfo.Image,
		}

		// Check if season is already owned locally
		existing, _ := s.db.GetSeasonBySeriesAndNumber(seriesID, seasonInfo.Number)
		if existing != nil {
			seasonData.FolderPath = existing.FolderPath
			seasonData.VoiceActorID = existing.VoiceActorID
			seasonData.IsOwned = existing.IsOwned
			seasonData.DiscoveredAt = existing.DiscoveredAt
		}

		_, err := s.db.UpsertSeason(seasonData)
		if err != nil {
			slog.Error("Failed to upsert season", "season", seasonInfo.Number, "error", err)
		}

		// Fetch episodes for this season if we have TVDB season ID
		if seasonInfo.ID > 0 {
			episodes, err := s.tvdbClient.GetSeasonEpisodes(seasonInfo.ID)
			if err != nil {
				slog.Warn("Failed to fetch episodes", "season_id", seasonInfo.ID, "error", err)
				continue
			}

			// Get season DB ID
			season, _ := s.db.GetSeasonBySeriesAndNumber(seriesID, seasonInfo.Number)
			if season == nil {
				continue
			}

			// Sync episodes
			for _, ep := range episodes {
				episodeData := &database.Episode{
					SeasonID:      season.ID,
					TVDBEpisodeID: &ep.ID,
					EpisodeNumber: ep.Number,
					Title:         &ep.Name,
					Overview:      &ep.Overview,
					ImageURL:      &ep.Image,
					AirDate:       &ep.AirDate,
					Runtime:       &ep.Runtime,
					Rating:        &ep.Rating,
				}

				_, err := s.db.UpsertEpisode(episodeData)
				if err != nil {
					slog.Error("Failed to upsert episode", "episode", ep.Number, "error", err)
				}
			}
		}
	}

	// Sync characters
	if len(extended.Characters) > 0 {
		characters := make([]database.Character, 0, len(extended.Characters))
		for _, char := range extended.Characters {
			characters = append(characters, database.Character{
				SeriesID:        seriesID,
				TVDBCharacterID: &char.ID,
				CharacterName:   &char.Name,
				ActorName:       &char.PersonName,
				ImageURL:        &char.Image,
				SortOrder:       &char.Sort,
			})
		}

		if err := s.db.UpsertCharacters(seriesID, characters); err != nil {
			slog.Error("Failed to sync characters", "error", err)
		}
	}

	// Sync artworks
	if len(extended.Artworks) > 0 {
		artworks := make([]database.Artwork, 0, len(extended.Artworks))
		for _, art := range extended.Artworks {
			seriesIDInt := seriesID
			artworks = append(artworks, database.Artwork{
				SeriesID:      &seriesIDInt,
				TVDBArtworkID: &art.ID,
				Type:          &art.TypeName,
				URL:           art.URL,
				ThumbnailURL:  &art.Thumbnail,
				Language:      &art.Language,
				Score:         &art.Score,
				Width:         &art.Width,
				Height:        &art.Height,
			})
		}

		if err := s.db.UpsertArtworks(artworks); err != nil {
			slog.Error("Failed to sync artworks", "error", err)
		}
	}

	slog.Info("Synced series from TVDB", "series_id", seriesID, "tvdb_id", *tvdbID, "title", title)

	s.respondJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("Synced series: %s", title),
	})
}
