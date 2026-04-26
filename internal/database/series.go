package database

import (
	"database/sql"
	"fmt"
)

// Series represents a TV series with full metadata
type Series struct {
	TVDBId        *int
	OriginalTitle *string
	Overview      *string
	PosterURL     *string
	BackdropURL   *string
	Status        *string
	Year          *int
	Runtime       *int
	Rating        *float64
	ContentRating *string
	Genres        *string // JSON array
	Networks      *string // JSON array
	Title         string
	ID            int64
	TotalSeasons  int
	AiredSeasons  int
}

// Season represents a season of a series
type Season struct {
	TVDBSeasonID     *int
	Name             *string
	PosterURL        *string
	FolderPath       *string
	TrackName        *string
	TrackerURL       *string
	TorrentHash      *string
	TrackerUpdatedAt *string
	DiscoveredAt     *string
	ID               int64
	SeriesID         int64
	SeasonNumber     int
	AiredEpisodes    int
	Downloaded       bool
	AutoProcess      bool
}

// Character represents a character in a series
type Character struct {
	TVDBCharacterID *int
	TVDBPersonID    *int
	CharacterName   *string
	ActorName       *string
	ImageURL        *string
	SortOrder       *int
	ID              int64
	SeriesID        int64
}

// GetUnsyncedSeries returns series that have a TVDB ID but no overview,
// indicating they were added by the scanner but not yet fully synced.
func (db *DB) GetUnsyncedSeries() ([]Series, error) {
	rows, err := db.Query(`
		SELECT id, tvdb_id, title
		FROM series
		WHERE tvdb_id IS NOT NULL AND overview IS NULL
		ORDER BY id
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to query unsynced series: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	var series []Series
	for rows.Next() {
		var s Series
		if err := rows.Scan(&s.ID, &s.TVDBId, &s.Title); err != nil {
			return nil, fmt.Errorf("failed to scan unsynced series: %w", err)
		}
		series = append(series, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate unsynced series: %w", err)
	}
	return series, nil
}

// UpsertSeason creates or updates a season
func (db *DB) UpsertSeason(season *Season) (int64, error) {
	// Check if season exists
	var existingID int64
	err := db.QueryRow(`
		SELECT id FROM seasons
		WHERE series_id = ? AND season_number = ?
	`, season.SeriesID, season.SeasonNumber).Scan(&existingID)

	if err != nil && err != sql.ErrNoRows {
		return 0, fmt.Errorf("failed to check existing season: %w", err)
	}

	if err == nil {
		// Update existing season — use COALESCE so that callers with partial data
		// (e.g. scanner with no TVDB fields) don't overwrite previously synced metadata.
		_, err = db.Exec(`
			UPDATE seasons SET
				tvdb_season_id = COALESCE(?, tvdb_season_id),
				name = COALESCE(?, name),
				poster_url = COALESCE(?, poster_url),
				folder_path = COALESCE(?, folder_path),
				track_name = COALESCE(?, track_name),
				downloaded = MAX(downloaded, ?),
				aired_episodes = ?,
				discovered_at = COALESCE(?, discovered_at),
				updated_at = CURRENT_TIMESTAMP
			WHERE id = ?
		`, season.TVDBSeasonID, season.Name, season.PosterURL,
			season.FolderPath, season.TrackName, season.Downloaded,
			season.AiredEpisodes,
			season.DiscoveredAt, existingID)
		if err != nil {
			return 0, fmt.Errorf("failed to update season: %w", err)
		}
		return existingID, nil
	}

	// Insert new season
	result, err := db.Exec(`
		INSERT INTO seasons (
			series_id, tvdb_season_id, season_number, name,
			poster_url, folder_path,
			track_name, downloaded, aired_episodes, discovered_at,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, season.SeriesID, season.TVDBSeasonID, season.SeasonNumber, season.Name,
		season.PosterURL, season.FolderPath,
		season.TrackName, season.Downloaded, season.AiredEpisodes, season.DiscoveredAt)

	if err != nil {
		return 0, fmt.Errorf("failed to insert season: %w", err)
	}

	return result.LastInsertId()
}

// GetSeasonFolderPaths returns all non-empty folder paths for seasons of a series
func (db *DB) GetSeasonFolderPaths(seriesID int64) ([]string, error) {
	rows, err := db.Query(`
		SELECT folder_path FROM seasons
		WHERE series_id = ? AND folder_path IS NOT NULL AND folder_path != ''
		ORDER BY folder_path
	`, seriesID)
	if err != nil {
		return nil, fmt.Errorf("failed to query season folder paths: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	var paths []string
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return nil, fmt.Errorf("failed to scan season folder path: %w", err)
		}
		paths = append(paths, path)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate season folder paths: %w", err)
	}
	return paths, nil
}

// GetSeasonBySeriesAndNumber retrieves a season by series ID and season number
func (db *DB) GetSeasonBySeriesAndNumber(seriesID int64, seasonNumber int) (*Season, error) {
	var season Season
	err := db.QueryRow(`
		SELECT id, series_id, tvdb_season_id, season_number, name,
			poster_url, folder_path,
			track_name, downloaded, aired_episodes, tracker_url, torrent_hash, tracker_updated_at, discovered_at
		FROM seasons WHERE series_id = ? AND season_number = ?
	`, seriesID, seasonNumber).Scan(
		&season.ID, &season.SeriesID, &season.TVDBSeasonID, &season.SeasonNumber,
		&season.Name, &season.PosterURL, &season.FolderPath,
		&season.TrackName, &season.Downloaded, &season.AiredEpisodes, &season.TrackerURL, &season.TorrentHash, &season.TrackerUpdatedAt, &season.DiscoveredAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get season: %w", err)
	}
	return &season, nil
}

// GetSeasonsWithTrackerURL returns all seasons that have a tracker_url set.
func (db *DB) GetSeasonsWithTrackerURL() ([]Season, error) {
	rows, err := db.Query(`
		SELECT id, series_id, tvdb_season_id, season_number, name,
			poster_url, folder_path,
			track_name, downloaded, aired_episodes, tracker_url, torrent_hash, tracker_updated_at, discovered_at, auto_process
		FROM seasons
		WHERE tracker_url IS NOT NULL AND tracker_url != ''
		ORDER BY series_id, season_number
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to query seasons with tracker URL: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	var seasons []Season
	for rows.Next() {
		var s Season
		if err := rows.Scan(
			&s.ID, &s.SeriesID, &s.TVDBSeasonID, &s.SeasonNumber,
			&s.Name, &s.PosterURL, &s.FolderPath,
			&s.TrackName, &s.Downloaded, &s.AiredEpisodes, &s.TrackerURL, &s.TorrentHash, &s.TrackerUpdatedAt, &s.DiscoveredAt, &s.AutoProcess,
		); err != nil {
			return nil, fmt.Errorf("failed to scan season with tracker URL: %w", err)
		}
		seasons = append(seasons, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate seasons with tracker URL: %w", err)
	}
	return seasons, nil
}

// GetSeasonNumbersBySeries returns all season_number values for a series.
func (db *DB) GetSeasonNumbersBySeries(seriesID int64) ([]int, error) {
	rows, err := db.Query(`
		SELECT season_number FROM seasons
		WHERE series_id = ?
		ORDER BY season_number
	`, seriesID)
	if err != nil {
		return nil, fmt.Errorf("failed to query season numbers: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	var nums []int
	for rows.Next() {
		var n int
		if err := rows.Scan(&n); err != nil {
			return nil, fmt.Errorf("failed to scan season number: %w", err)
		}
		nums = append(nums, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate season numbers: %w", err)
	}
	return nums, nil
}

// GetTorrentHashesBySeries returns all non-empty torrent_hash values
// for the seasons of a series.
func (db *DB) GetTorrentHashesBySeries(seriesID int64) ([]string, error) {
	rows, err := db.Query(`
		SELECT torrent_hash FROM seasons
		WHERE series_id = ? AND torrent_hash IS NOT NULL AND torrent_hash != ''
		ORDER BY season_number
	`, seriesID)
	if err != nil {
		return nil, fmt.Errorf("failed to query torrent hashes: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	var hashes []string
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, fmt.Errorf("failed to scan torrent hash: %w", err)
		}
		hashes = append(hashes, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate torrent hashes: %w", err)
	}
	return hashes, nil
}

// DeleteSeason removes a season row by (series_id, season_number).
// Returns rows affected. CASCADE deletes related media_files via the composite FK.
func (db *DB) DeleteSeason(seriesID int64, seasonNumber int) (int64, error) {
	result, err := db.Exec(`
		DELETE FROM seasons WHERE series_id = ? AND season_number = ?
	`, seriesID, seasonNumber)
	if err != nil {
		return 0, fmt.Errorf("failed to delete season: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to get rows affected: %w", err)
	}
	return affected, nil
}

// DeleteSeasonAndTombstone atomically removes the season row and records a
// tombstone in deleted_seasons. Both writes share a single transaction so the
// season cannot be left without a tombstone — otherwise TVDB sync would
// resurrect it as metadata-only and the user could not retry the delete.
// Returns rows affected by the season delete. CASCADE removes media_files.
func (db *DB) DeleteSeasonAndTombstone(seriesID int64, seasonNumber int) (int64, error) {
	tx, err := db.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	result, err := tx.Exec(`
		DELETE FROM seasons WHERE series_id = ? AND season_number = ?
	`, seriesID, seasonNumber)
	if err != nil {
		return 0, fmt.Errorf("failed to delete season: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to get rows affected: %w", err)
	}
	if affected == 0 {
		return 0, nil
	}

	if _, err := tx.Exec(`
		INSERT OR REPLACE INTO deleted_seasons (series_id, season_number, deleted_at)
		VALUES (?, ?, CURRENT_TIMESTAMP)
	`, seriesID, seasonNumber); err != nil {
		return 0, fmt.Errorf("failed to mark season deleted: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit transaction: %w", err)
	}
	return affected, nil
}

// MarkSeasonDeleted records a tombstone so TVDB sync will not resurrect the
// season. Idempotent (INSERT OR REPLACE refreshes deleted_at).
func (db *DB) MarkSeasonDeleted(seriesID int64, seasonNumber int) error {
	_, err := db.Exec(`
		INSERT OR REPLACE INTO deleted_seasons (series_id, season_number, deleted_at)
		VALUES (?, ?, CURRENT_TIMESTAMP)
	`, seriesID, seasonNumber)
	if err != nil {
		return fmt.Errorf("failed to mark season deleted: %w", err)
	}
	return nil
}

// ClearSeasonTombstone removes the tombstone for a (series_id, season_number).
// Called when files for the season reappear on disk (scanner) so future TVDB
// syncs are allowed to populate metadata again.
func (db *DB) ClearSeasonTombstone(seriesID int64, seasonNumber int) error {
	_, err := db.Exec(`
		DELETE FROM deleted_seasons WHERE series_id = ? AND season_number = ?
	`, seriesID, seasonNumber)
	if err != nil {
		return fmt.Errorf("failed to clear season tombstone: %w", err)
	}
	return nil
}

// IsSeasonDeleted reports whether the season has an active tombstone.
func (db *DB) IsSeasonDeleted(seriesID int64, seasonNumber int) (bool, error) {
	var exists int
	err := db.QueryRow(`
		SELECT 1 FROM deleted_seasons WHERE series_id = ? AND season_number = ?
	`, seriesID, seasonNumber).Scan(&exists)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to check season tombstone: %w", err)
	}
	return true, nil
}

// UpdateTorrentHash updates the torrent_hash for a season.
func (db *DB) UpdateTorrentHash(seasonID int64, hash string) error {
	_, err := db.Exec(`UPDATE seasons SET torrent_hash = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, hash, seasonID)
	if err != nil {
		return fmt.Errorf("failed to update torrent hash: %w", err)
	}
	return nil
}

// UpdateTrackerUpdatedAt stores the last update timestamp from the tracker page.
func (db *DB) UpdateTrackerUpdatedAt(seasonID int64, updatedAt string) error {
	_, err := db.Exec(`UPDATE seasons SET tracker_updated_at = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, updatedAt, seasonID)
	if err != nil {
		return fmt.Errorf("failed to update tracker_updated_at: %w", err)
	}
	return nil
}

// GetTrackNamesForSeries returns all distinct track_name values for a series (history).
func (db *DB) GetTrackNamesForSeries(seriesID int64) ([]string, error) {
	rows, err := db.Query(`
		SELECT DISTINCT track_name FROM seasons
		WHERE series_id = ? AND track_name IS NOT NULL AND track_name != ''
		ORDER BY season_number
	`, seriesID)
	if err != nil {
		return nil, fmt.Errorf("failed to query track names: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("failed to scan track name: %w", err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate track names: %w", err)
	}
	return names, nil
}

// SyncSeriesAndChildren updates the series row and writes all child records
// (seasons, characters) within a single transaction. The tvdb_id
// guard prevents stale metadata from being written if the series was rematched
// to a different TVDB ID during a long sync operation.
func (db *DB) SyncSeriesAndChildren(seriesID int64, expectedTVDBID int, series *Series, seasons []Season, characters []Character) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	// Update parent series row with tvdb_id guard (concurrent rematch protection)
	result, err := tx.Exec(`
		UPDATE series SET
			title = COALESCE(NULLIF(?, ''), title),
			original_title = COALESCE(?, original_title),
			overview = COALESCE(?, overview),
			poster_url = COALESCE(?, poster_url),
			backdrop_url = COALESCE(?, backdrop_url),
			status = COALESCE(?, status),
			year = COALESCE(?, year),
			runtime = COALESCE(?, runtime),
			rating = COALESCE(?, rating),
			content_rating = COALESCE(?, content_rating),
			genres = COALESCE(?, genres),
			networks = COALESCE(?, networks),
			total_seasons = ?,
			aired_seasons = ?,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = ? AND tvdb_id = ?
	`, series.Title, series.OriginalTitle, series.Overview,
		series.PosterURL, series.BackdropURL, series.Status,
		series.Year, series.Runtime,
		series.Rating, series.ContentRating,
		series.Genres, series.Networks,
		series.TotalSeasons, series.AiredSeasons, seriesID, expectedTVDBID)
	if err != nil {
		return fmt.Errorf("failed to update series: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("series %d no longer exists or tvdb_id changed", seriesID)
	}

	// Upsert seasons
	for _, season := range seasons {
		if err := upsertSeasonTx(tx, &season); err != nil {
			return fmt.Errorf("failed to upsert season %d: %w", season.SeasonNumber, err)
		}
	}

	// Replace characters (always delete old ones, even if new list is empty)
	if err := upsertCharactersTx(tx, seriesID, characters); err != nil {
		return err
	}

	return tx.Commit()
}

// upsertSeasonTx creates or updates a season within an existing transaction.
// Skips both insert and update when the season has a tombstone in
// deleted_seasons — the user explicitly deleted it and TVDB sync must not
// resurrect or refresh metadata for it.
func upsertSeasonTx(tx *sql.Tx, season *Season) error {
	var tombstoned int
	err := tx.QueryRow(`
		SELECT 1 FROM deleted_seasons
		WHERE series_id = ? AND season_number = ?
	`, season.SeriesID, season.SeasonNumber).Scan(&tombstoned)
	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("failed to check season tombstone: %w", err)
	}
	if err == nil {
		return nil
	}

	var existingID int64
	err = tx.QueryRow(`
		SELECT id FROM seasons
		WHERE series_id = ? AND season_number = ?
	`, season.SeriesID, season.SeasonNumber).Scan(&existingID)

	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("failed to check existing season: %w", err)
	}

	if err == nil {
		_, err = tx.Exec(`
			UPDATE seasons SET
				tvdb_season_id = COALESCE(?, tvdb_season_id),
				name = COALESCE(?, name),
				poster_url = COALESCE(?, poster_url),
				folder_path = COALESCE(?, folder_path),
				track_name = COALESCE(?, track_name),
				downloaded = MAX(downloaded, ?),
				aired_episodes = ?,
				discovered_at = COALESCE(?, discovered_at),
				updated_at = CURRENT_TIMESTAMP
			WHERE id = ?
		`, season.TVDBSeasonID, season.Name, season.PosterURL,
			season.FolderPath, season.TrackName, season.Downloaded,
			season.AiredEpisodes,
			season.DiscoveredAt, existingID)
		if err != nil {
			return fmt.Errorf("failed to update season: %w", err)
		}
		return nil
	}

	_, err = tx.Exec(`
		INSERT INTO seasons (
			series_id, tvdb_season_id, season_number, name,
			poster_url, folder_path,
			track_name, downloaded, aired_episodes, discovered_at,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, season.SeriesID, season.TVDBSeasonID, season.SeasonNumber, season.Name,
		season.PosterURL, season.FolderPath,
		season.TrackName, season.Downloaded, season.AiredEpisodes, season.DiscoveredAt)
	if err != nil {
		return fmt.Errorf("failed to insert season: %w", err)
	}
	return nil
}

// upsertCharactersTx replaces characters for a series within an existing transaction.
func upsertCharactersTx(tx *sql.Tx, seriesID int64, characters []Character) error {
	_, err := tx.Exec(`DELETE FROM series_characters WHERE series_id = ?`, seriesID)
	if err != nil {
		return fmt.Errorf("failed to delete old characters: %w", err)
	}

	stmt, err := tx.Prepare(`
		INSERT INTO series_characters (
			series_id, tvdb_character_id, tvdb_person_id,
			character_name, actor_name, image_url, sort_order
		) VALUES (?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare character insert: %w", err)
	}
	defer stmt.Close() //nolint:errcheck // closing prepared statement

	for _, char := range characters {
		_, err := stmt.Exec(
			seriesID, char.TVDBCharacterID, char.TVDBPersonID,
			char.CharacterName, char.ActorName, char.ImageURL, char.SortOrder,
		)
		if err != nil {
			return fmt.Errorf("failed to insert character %v: %w", char.CharacterName, err)
		}
	}
	return nil
}
