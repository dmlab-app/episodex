package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/episodex/episodex/internal/database"
	"github.com/go-chi/chi/v5"
)

// handleGetSeriesExtended returns extended series information with all metadata
func (s *Server) handleGetSeriesExtended(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	// Get series basic info
	query := `
		SELECT id, tvdb_id, title, original_title, slug, overview,
			poster_url, backdrop_url, status, first_aired, last_aired,
			year, runtime, rating, content_rating,
			original_country, original_language,
			genres, networks, studios, total_seasons, created_at
		FROM series
		WHERE id = ?
	`

	var series struct {
		ID               int
		TVDBId           *int
		Title            string
		OriginalTitle    *string
		Slug             *string
		Overview         *string
		PosterURL        *string
		BackdropURL      *string
		Status           *string
		FirstAired       *string
		LastAired        *string
		Year             *int
		Runtime          *int
		Rating           *float64
		ContentRating    *string
		OriginalCountry  *string
		OriginalLanguage *string
		Genres           *string
		Networks         *string
		Studios          *string
		TotalSeasons     int
		CreatedAt        time.Time
	}

	err := s.db.QueryRow(query, id).Scan(
		&series.ID, &series.TVDBId, &series.Title, &series.OriginalTitle,
		&series.Slug, &series.Overview, &series.PosterURL, &series.BackdropURL,
		&series.Status, &series.FirstAired, &series.LastAired,
		&series.Year, &series.Runtime, &series.Rating, &series.ContentRating,
		&series.OriginalCountry, &series.OriginalLanguage,
		&series.Genres, &series.Networks, &series.Studios,
		&series.TotalSeasons, &series.CreatedAt,
	)

	if err != nil {
		s.respondError(w, http.StatusNotFound, "series not found")
		return
	}

	// Get seasons
	seasonsQuery := `
		SELECT id, season_number, name, overview, poster_url,
			first_aired, episode_count, folder_path, is_owned, discovered_at
		FROM seasons
		WHERE series_id = ?
		ORDER BY season_number
	`

	seasonsRows, err := s.db.Query(seasonsQuery, id)
	if err != nil {
		s.respondError(w, http.StatusInternalServerError, "failed to fetch seasons")
		return
	}
	defer seasonsRows.Close()

	seasons := []map[string]interface{}{}
	for seasonsRows.Next() {
		var seasonID int64
		var seasonNum int
		var name, overview, posterURL, firstAired, folderPath *string
		var episodeCount *int
		var isOwned bool
		var discoveredAt *time.Time

		if err := seasonsRows.Scan(&seasonID, &seasonNum, &name, &overview, &posterURL,
			&firstAired, &episodeCount, &folderPath, &isOwned, &discoveredAt); err != nil {
			continue
		}

		season := map[string]interface{}{
			"id":            seasonID,
			"season_number": seasonNum,
			"is_owned":      isOwned,
		}

		if name != nil {
			season["name"] = *name
		}
		if overview != nil {
			season["overview"] = *overview
		}
		if posterURL != nil {
			season["poster_url"] = *posterURL
		}
		if firstAired != nil {
			season["first_aired"] = *firstAired
		}
		if episodeCount != nil {
			season["episode_count"] = *episodeCount
		}
		if folderPath != nil {
			season["folder_path"] = *folderPath
		}
		if discoveredAt != nil {
			season["discovered_at"] = *discoveredAt
		}

		seasons = append(seasons, season)
	}

	// Get characters
	charactersQuery := `
		SELECT character_name, actor_name, image_url, sort_order
		FROM series_characters
		WHERE series_id = ?
		ORDER BY sort_order
		LIMIT 10
	`

	charactersRows, err := s.db.Query(charactersQuery, id)
	if err == nil {
		defer charactersRows.Close()

		// Characters will be added to response separately
	}

	// Build response
	response := map[string]interface{}{
		"id":            series.ID,
		"title":         series.Title,
		"total_seasons": series.TotalSeasons,
		"seasons":       seasons,
		"created_at":    series.CreatedAt,
	}

	if series.TVDBId != nil {
		response["tvdb_id"] = *series.TVDBId
	}
	if series.OriginalTitle != nil {
		response["original_title"] = *series.OriginalTitle
	}
	if series.Slug != nil {
		response["slug"] = *series.Slug
	}
	if series.Overview != nil {
		response["overview"] = *series.Overview
	}
	if series.PosterURL != nil {
		response["poster_url"] = *series.PosterURL
	}
	if series.BackdropURL != nil {
		response["backdrop_url"] = *series.BackdropURL
	}
	if series.Status != nil {
		response["status"] = *series.Status
	}
	if series.FirstAired != nil {
		response["first_aired"] = *series.FirstAired
	}
	if series.LastAired != nil {
		response["last_aired"] = *series.LastAired
	}
	if series.Year != nil {
		response["year"] = *series.Year
	}
	if series.Runtime != nil {
		response["runtime"] = *series.Runtime
	}
	if series.Rating != nil {
		response["rating"] = *series.Rating
	}
	if series.ContentRating != nil {
		response["content_rating"] = *series.ContentRating
	}
	if series.OriginalCountry != nil {
		response["original_country"] = *series.OriginalCountry
	}
	if series.OriginalLanguage != nil {
		response["original_language"] = *series.OriginalLanguage
	}

	// Parse JSON fields
	if series.Genres != nil {
		var genres []interface{}
		if err := json.Unmarshal([]byte(*series.Genres), &genres); err == nil {
			response["genres"] = genres
		}
	}
	if series.Networks != nil {
		var networks []interface{}
		if err := json.Unmarshal([]byte(*series.Networks), &networks); err == nil {
			response["networks"] = networks
		}
	}
	if series.Studios != nil {
		var studios []interface{}
		if err := json.Unmarshal([]byte(*series.Studios), &studios); err == nil {
			response["studios"] = studios
		}
	}

	s.respondJSON(w, http.StatusOK, response)
}

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
	genresJSON, _ := json.Marshal(extended.Genres)
	networksJSON, _ := json.Marshal(extended.Networks)
	studiosJSON, _ := json.Marshal(extended.Studios)

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
