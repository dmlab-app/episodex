package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/episodex/episodex/internal/database"
)

// handleSyncSeriesFromTVDB syncs series metadata from TVDB
func (s *Server) handleSyncSeriesFromTVDB(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		s.respondError(w, http.StatusBadRequest, "invalid series ID")
		return
	}

	if s.tvdbClient == nil {
		s.respondError(w, http.StatusServiceUnavailable, "TVDB client not configured")
		return
	}

	// Get series tvdb_id
	var tvdbID *int
	err = s.db.QueryRow(`SELECT tvdb_id FROM series WHERE id = ?`, id).Scan(&tvdbID)
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

	// Convert arrays to JSON (store as string arrays for consistency)
	genreNames := make([]string, len(extended.Genres))
	for i, g := range extended.Genres {
		genreNames[i] = g.Name
	}
	networkNames := make([]string, len(extended.Networks))
	for i, n := range extended.Networks {
		networkNames[i] = n.Name
	}
	studioNames := make([]string, len(extended.Studios))
	for i, st := range extended.Studios {
		studioNames[i] = st.Name
	}

	genresJSON, err := json.Marshal(genreNames)
	if err != nil {
		slog.Error("Failed to marshal genres", "error", err)
		genresJSON = []byte("[]")
	}
	networksJSON, err := json.Marshal(networkNames)
	if err != nil {
		slog.Error("Failed to marshal networks", "error", err)
		networksJSON = []byte("[]")
	}
	studiosJSON, err := json.Marshal(studioNames)
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
		TVDBId:       tvdbID,
		Title:        title,
		TotalSeasons: len(extended.Seasons),
	}
	// Only set non-empty string fields to avoid overwriting existing values with ""
	if originalTitle != "" {
		seriesData.OriginalTitle = &originalTitle
	}
	if extended.Slug != "" {
		seriesData.Slug = &extended.Slug
	}
	if overview != "" {
		seriesData.Overview = &overview
	}
	if extended.Image != "" {
		seriesData.PosterURL = &extended.Image
	}
	if extended.Backdrop != "" {
		seriesData.BackdropURL = &extended.Backdrop
	}
	if extended.Status != "" {
		seriesData.Status = &extended.Status
	}
	if extended.FirstAired != "" {
		seriesData.FirstAired = &extended.FirstAired
	}
	if extended.LastAired != "" {
		seriesData.LastAired = &extended.LastAired
	}
	if contentRating != "" {
		seriesData.ContentRating = &contentRating
	}
	if extended.OriginalCountry != "" {
		seriesData.OriginalCountry = &extended.OriginalCountry
	}
	if extended.OriginalLanguage != "" {
		seriesData.OriginalLanguage = &extended.OriginalLanguage
	}
	if genres != "" {
		seriesData.Genres = &genres
	}
	if networks != "" {
		seriesData.Networks = &networks
	}
	if studios != "" {
		seriesData.Studios = &studios
	}
	// Only store non-zero values so we don't overwrite NULL with meaningless 0
	if extended.Year > 0 {
		seriesData.Year = &extended.Year
	}
	if extended.Runtime > 0 {
		seriesData.Runtime = &extended.Runtime
	}
	if extended.Score > 0 {
		seriesData.Rating = &extended.Score
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
		}
		// Only set name/image if non-empty to avoid overwriting existing values with ""
		if seasonInfo.Name != "" {
			seasonData.Name = &seasonInfo.Name
		}
		if seasonInfo.Image != "" {
			seasonData.PosterURL = &seasonInfo.Image
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
					Title:         strPtrOrNil(ep.Name),
					Overview:      strPtrOrNil(ep.Overview),
					ImageURL:      strPtrOrNil(ep.Image),
					AirDate:       strPtrOrNil(ep.AirDate),
					Runtime:       intPtrOrNil(ep.Runtime),
					Rating:        floatPtrOrNil(ep.Rating),
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

// strPtrOrNil returns nil for empty strings, otherwise a pointer to the value.
func strPtrOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// intPtrOrNil returns nil for zero values, otherwise a pointer to the value.
func intPtrOrNil(v int) *int {
	if v == 0 {
		return nil
	}
	return &v
}

// floatPtrOrNil returns nil for zero values, otherwise a pointer to the value.
func floatPtrOrNil(v float64) *float64 {
	if v == 0 {
		return nil
	}
	return &v
}
