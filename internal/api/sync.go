package api

import (
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/episodex/episodex/internal/database"
	"github.com/episodex/episodex/internal/tvdb"
)

// SyncSeriesMetadata fetches full metadata from TVDB and updates the database.
// It syncs series info, seasons, episodes, characters, and artworks.
// This function is used by both the manual sync handler and the scheduled auto-sync.
func SyncSeriesMetadata(db *database.DB, tvdbClient *tvdb.Client, seriesID int64, tvdbID int) error {
	// Fetch extended data from TVDB
	extended, err := tvdbClient.GetSeriesExtendedFull(tvdbID)
	if err != nil {
		return fmt.Errorf("failed to fetch series from TVDB: %w", err)
	}

	// Get Russian translation
	rusTrans, _ := tvdbClient.GetSeriesTranslation(tvdbID, "rus")

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
		TVDBId:       &tvdbID,
		Title:        title,
		TotalSeasons: len(extended.Seasons),
	}
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
	if genres != "" && genres != "[]" {
		seriesData.Genres = &genres
	}
	if networks != "" && networks != "[]" {
		seriesData.Networks = &networks
	}
	if studios != "" && studios != "[]" {
		seriesData.Studios = &studios
	}
	if extended.Year > 0 {
		seriesData.Year = &extended.Year
	}
	if extended.Runtime > 0 {
		seriesData.Runtime = &extended.Runtime
	}
	if extended.Score > 0 {
		seriesData.Rating = &extended.Score
	}

	upsertedID, err := db.UpsertSeries(seriesData)
	if err != nil {
		return fmt.Errorf("failed to update series: %w", err)
	}

	// Sync seasons with TVDB data
	for _, seasonInfo := range extended.Seasons {
		seasonData := &database.Season{
			SeriesID:     upsertedID,
			TVDBSeasonID: &seasonInfo.ID,
			SeasonNumber: seasonInfo.Number,
		}
		if seasonInfo.Name != "" {
			seasonData.Name = &seasonInfo.Name
		}
		if seasonInfo.Image != "" {
			seasonData.PosterURL = &seasonInfo.Image
		}

		_, err := db.UpsertSeason(seasonData)
		if err != nil {
			slog.Error("Failed to upsert season", "season", seasonInfo.Number, "error", err)
		}

		// Fetch episodes for this season if we have TVDB season ID
		if seasonInfo.ID > 0 {
			episodes, err := tvdbClient.GetSeasonEpisodes(seasonInfo.ID)
			if err != nil {
				slog.Warn("Failed to fetch episodes", "season_id", seasonInfo.ID, "error", err)
				continue
			}

			season, _ := db.GetSeasonBySeriesAndNumber(upsertedID, seasonInfo.Number)
			if season == nil {
				continue
			}

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

				_, err := db.UpsertEpisode(episodeData)
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
				SeriesID:        upsertedID,
				TVDBCharacterID: &char.ID,
				CharacterName:   &char.Name,
				ActorName:       &char.PersonName,
				ImageURL:        &char.Image,
				SortOrder:       &char.Sort,
			})
		}

		if err := db.UpsertCharacters(upsertedID, characters); err != nil {
			slog.Error("Failed to sync characters", "error", err)
		}
	}

	// Sync artworks
	if len(extended.Artworks) > 0 {
		artworks := make([]database.Artwork, 0, len(extended.Artworks))
		for _, art := range extended.Artworks {
			id := upsertedID
			artworks = append(artworks, database.Artwork{
				SeriesID:      &id,
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

		if err := db.UpsertArtworks(artworks); err != nil {
			slog.Error("Failed to sync artworks", "error", err)
		}
	}

	slog.Info("Synced series from TVDB", "series_id", upsertedID, "tvdb_id", tvdbID, "title", title)
	return nil
}
