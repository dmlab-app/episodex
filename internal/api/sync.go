package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/episodex/episodex/internal/database"
	"github.com/episodex/episodex/internal/tvdb"
)

// tvdbCheckMu prevents concurrent TVDB update checks (manual + scheduler overlap).
var tvdbCheckMu sync.Mutex

// TVDBCheckResult holds the outcome of a TVDB update check run.
type TVDBCheckResult struct {
	Checked int
	Updated int
	Synced  int
	Errors  int
	Skipped bool
}

// CheckForTVDBUpdates checks all series with TVDB IDs for season count changes,
// creates alerts for new aired seasons, and optionally auto-syncs stale metadata.
// This function is used by both the scheduled tvdb_check task and the manual API trigger.
func CheckForTVDBUpdates(db *database.DB, tvdbClient *tvdb.Client, autoSync bool) TVDBCheckResult {
	if !tvdbCheckMu.TryLock() {
		slog.Info("TVDB check already in progress, skipping")
		return TVDBCheckResult{Skipped: true}
	}
	defer tvdbCheckMu.Unlock()

	var result TVDBCheckResult

	type seriesRow struct {
		id, tvdbID, totalSeasons, airedSeasons, maxWatched int
		title                                              string
		updatedAt                                          time.Time
	}

	rows, err := db.Query(`
		SELECT s.id, s.tvdb_id, s.title, s.total_seasons, s.aired_seasons,
			COALESCE((SELECT MAX(sn.season_number) FROM seasons sn WHERE sn.series_id = s.id AND sn.is_watched = 1 AND sn.season_number > 0), 0),
			s.updated_at
		FROM series s
		WHERE s.tvdb_id IS NOT NULL
	`)
	if err != nil {
		slog.Error("Failed to fetch series for TVDB check", "error", err)
		return result
	}

	var seriesList []seriesRow
	for rows.Next() {
		var s seriesRow
		if err := rows.Scan(&s.id, &s.tvdbID, &s.title, &s.totalSeasons, &s.airedSeasons, &s.maxWatched, &s.updatedAt); err != nil {
			slog.Error("Failed to scan series check row", "error", err)
			continue
		}
		seriesList = append(seriesList, s)
	}
	if err := rows.Err(); err != nil {
		rows.Close() //nolint:errcheck
		slog.Error("Error iterating series for TVDB check", "error", err)
		return result
	}
	rows.Close() //nolint:errcheck

	for _, s := range seriesList {
		result.Checked++

		details, err := tvdbClient.GetSeriesDetails(s.tvdbID)
		if err != nil {
			slog.Error("Failed to get TVDB seasons", "series_id", s.id, "tvdb_id", s.tvdbID, "error", err)
			result.Errors++
			continue
		}

		newTotalSeasons := len(details.Seasons)
		newAiredSeasons := tvdb.MaxAiredSeasonNumber(details.Seasons)

		seasonCountChanged := newTotalSeasons != s.totalSeasons || newAiredSeasons != s.airedSeasons
		if seasonCountChanged {
			if newAiredSeasons > s.maxWatched && s.maxWatched > 0 {
				message := "New seasons available for " + s.title

				if _, err = db.Exec(`
					INSERT INTO system_alerts (type, message, created_at, dismissed)
					SELECT ?, ?, CURRENT_TIMESTAMP, 0
					WHERE NOT EXISTS (
						SELECT 1 FROM system_alerts WHERE type = ? AND message = ? AND dismissed = 0
					)
				`, "new_seasons", message, "new_seasons", message); err != nil {
					slog.Error("Failed to create alert", "series_id", s.id, "error", err)
				}
			}

			slog.Info("Detected season changes", "series", s.title,
				"old_total", s.totalSeasons, "new_total", newTotalSeasons,
				"old_aired", s.airedSeasons, "new_aired", newAiredSeasons)
			result.Updated++
		}

		// Auto-sync full metadata when season counts changed (so new season rows are
		// created immediately, not after a 7-day delay) or for stale series.
		needsSync := autoSync && (seasonCountChanged || time.Since(s.updatedAt) > 7*24*time.Hour)
		if needsSync {
			// Verify series still exists (may have been deleted during this check run)
			var exists int
			if err := db.QueryRow(`SELECT COUNT(*) FROM series WHERE id = ?`, s.id).Scan(&exists); err != nil {
				slog.Error("Failed to check series existence", "series_id", s.id, "error", err)
				result.Errors++
				continue
			}
			if exists == 0 {
				slog.Info("Series was deleted during TVDB check, skipping sync", "series_id", s.id, "title", s.title)
				continue
			}

			if seasonCountChanged {
				slog.Info("Syncing series metadata after season count change", "series", s.title)
			} else {
				slog.Info("Auto-syncing stale series metadata", "series", s.title, "last_updated", s.updatedAt)
			}
			if err := SyncSeriesMetadata(db, tvdbClient, int64(s.id), s.tvdbID); err != nil {
				slog.Error("Failed to auto-sync series", "series_id", s.id, "error", err)
				result.Errors++
			} else {
				result.Synced++
			}
		} else if seasonCountChanged {
			// autoSync disabled — update season counts directly.
			if _, err = db.Exec(`
				UPDATE series
				SET total_seasons = ?, aired_seasons = ?, updated_at = CURRENT_TIMESTAMP
				WHERE id = ?
			`, newTotalSeasons, newAiredSeasons, s.id); err != nil {
				slog.Error("Failed to update series seasons", "series_id", s.id, "error", err)
				result.Errors++
				continue
			}
		}
	}

	slog.Info("TVDB check completed", "checked", result.Checked, "updated", result.Updated, "synced", result.Synced, "errors", result.Errors)
	return result
}

// SyncSeriesMetadata fetches full metadata from TVDB and updates the database.
// It syncs series info, seasons, and characters.
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

	genres := string(genresJSON)
	networks := string(networksJSON)

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
		AiredSeasons: tvdb.MaxAiredSeasonNumber(extended.Seasons),
	}
	if originalTitle != "" {
		seriesData.OriginalTitle = &originalTitle
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
	if contentRating != "" {
		seriesData.ContentRating = &contentRating
	}
	if genres != "" && genres != "[]" {
		seriesData.Genres = &genres
	}
	if networks != "" && networks != "[]" {
		seriesData.Networks = &networks
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

	// Build child records
	seasons := make([]database.Season, 0, len(extended.Seasons))
	for _, seasonInfo := range extended.Seasons {
		seasonData := database.Season{
			SeriesID:     seriesID,
			TVDBSeasonID: &seasonInfo.ID,
			SeasonNumber: seasonInfo.Number,
		}
		if seasonInfo.Name != "" {
			seasonData.Name = &seasonInfo.Name
		}
		if seasonInfo.Image != "" {
			seasonData.PosterURL = &seasonInfo.Image
		}
		seasons = append(seasons, seasonData)
	}

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

	// Update series row and write all child records (seasons, characters)
	// in a single transaction with a tvdb_id guard.
	if err := db.SyncSeriesAndChildren(seriesID, tvdbID, seriesData, seasons, characters); err != nil {
		return fmt.Errorf("failed to sync series: %w", err)
	}

	slog.Info("Synced series from TVDB", "series_id", seriesID, "tvdb_id", tvdbID, "title", title)
	return nil
}

// SyncUnsyncedSeries syncs metadata for all series that have a TVDB ID but
// no overview (added by scanner but not yet fully synced).
func SyncUnsyncedSeries(db *database.DB, tvdbClient *tvdb.Client) {
	unsyncedSeries, err := db.GetUnsyncedSeries()
	if err != nil {
		slog.Error("Failed to get unsynced series", "error", err)
		return
	}

	if len(unsyncedSeries) == 0 {
		slog.Info("No unsynced series found")
		return
	}

	slog.Info("Starting startup sync for unsynced series", "count", len(unsyncedSeries))

	if !tvdbCheckMu.TryLock() {
		slog.Info("TVDB check already in progress, skipping startup sync")
		return
	}
	defer tvdbCheckMu.Unlock()

	var synced, errors int
	for i := range unsyncedSeries {
		s := &unsyncedSeries[i]
		slog.Info("Syncing unsynced series", "progress", fmt.Sprintf("%d/%d", i+1, len(unsyncedSeries)), "title", s.Title, "tvdb_id", *s.TVDBId)
		if err := SyncSeriesMetadata(db, tvdbClient, s.ID, *s.TVDBId); err != nil {
			slog.Error("Failed to sync unsynced series", "series_id", s.ID, "title", s.Title, "error", err)
			errors++
			continue
		}
		// Mark series as synced even if TVDB returned no overview, to prevent
		// re-syncing on every restart (overview IS NULL is the "unsynced" marker).
		// The tvdb_id guard prevents stamping a rematched series with stale data.
		if _, err := db.Exec(`UPDATE series SET overview = COALESCE(overview, '') WHERE id = ? AND tvdb_id = ?`, s.ID, *s.TVDBId); err != nil {
			slog.Error("Failed to mark series as synced", "series_id", s.ID, "error", err)
		}
		synced++
	}

	slog.Info("Startup sync completed", "total", len(unsyncedSeries), "synced", synced, "errors", errors)
}
