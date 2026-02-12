package database

import (
	"database/sql"
	"fmt"
	"log/slog"
	"time"
)

// MediaFile represents a media file with hash tracking
type MediaFile struct {
	ID           int64
	SeriesID     int64
	SeasonNumber int
	FilePath     string
	FileName     string
	FileSize     int64
	FileHash     string
	ModTime      int64
	FirstSeen    time.Time
	LastChecked  time.Time
}

// GetMediaFileByPath retrieves a media file by its path
func (db *DB) GetMediaFileByPath(filePath string) (*MediaFile, error) {
	var mf MediaFile
	err := db.QueryRow(`
		SELECT id, series_id, season_number, file_path, file_name, file_size,
		       file_hash, mod_time, first_seen, last_checked
		FROM media_files
		WHERE file_path = ?
	`, filePath).Scan(
		&mf.ID, &mf.SeriesID, &mf.SeasonNumber, &mf.FilePath, &mf.FileName,
		&mf.FileSize, &mf.FileHash, &mf.ModTime, &mf.FirstSeen, &mf.LastChecked,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get media file: %w", err)
	}

	return &mf, nil
}

// GetMediaFilesBySeason retrieves all media files for a season
func (db *DB) GetMediaFilesBySeason(seriesID int64, seasonNumber int) ([]MediaFile, error) {
	rows, err := db.Query(`
		SELECT id, series_id, season_number, file_path, file_name, file_size,
		       file_hash, mod_time, first_seen, last_checked
		FROM media_files
		WHERE series_id = ? AND season_number = ?
		ORDER BY file_name
	`, seriesID, seasonNumber)

	if err != nil {
		return nil, fmt.Errorf("failed to query media files: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	var files []MediaFile
	for rows.Next() {
		var mf MediaFile
		err := rows.Scan(
			&mf.ID, &mf.SeriesID, &mf.SeasonNumber, &mf.FilePath, &mf.FileName,
			&mf.FileSize, &mf.FileHash, &mf.ModTime, &mf.FirstSeen, &mf.LastChecked,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan media file: %w", err)
		}
		files = append(files, mf)
	}

	return files, nil
}

// UpsertMediaFile inserts or updates a media file record
func (db *DB) UpsertMediaFile(mf *MediaFile) error {
	// Check if file already exists
	existing, err := db.GetMediaFileByPath(mf.FilePath)
	if err != nil {
		return err
	}

	now := time.Now()

	if existing == nil {
		// Insert new file
		result, err := db.Exec(`
			INSERT INTO media_files (
				series_id, season_number, file_path, file_name, file_size,
				file_hash, mod_time, first_seen, last_checked
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, mf.SeriesID, mf.SeasonNumber, mf.FilePath, mf.FileName,
			mf.FileSize, mf.FileHash, mf.ModTime, now, now)

		if err != nil {
			return fmt.Errorf("failed to insert media file: %w", err)
		}

		mf.ID, _ = result.LastInsertId()
		mf.FirstSeen = now
		mf.LastChecked = now

		slog.Debug("Inserted new media file", "path", mf.FilePath, "hash", mf.FileHash)
		return nil
	}

	// Update existing file
	_, err = db.Exec(`
		UPDATE media_files
		SET file_size = ?, file_hash = ?, mod_time = ?, last_checked = ?
		WHERE id = ?
	`, mf.FileSize, mf.FileHash, mf.ModTime, now, existing.ID)

	if err != nil {
		return fmt.Errorf("failed to update media file: %w", err)
	}

	mf.ID = existing.ID
	mf.FirstSeen = existing.FirstSeen
	mf.LastChecked = now

	slog.Debug("Updated media file", "path", mf.FilePath, "hash", mf.FileHash)
	return nil
}

// DeleteMediaFile deletes a media file record
func (db *DB) DeleteMediaFile(filePath string) error {
	_, err := db.Exec(`DELETE FROM media_files WHERE file_path = ?`, filePath)
	if err != nil {
		return fmt.Errorf("failed to delete media file: %w", err)
	}
	return nil
}

// DeleteMediaFilesBySeason deletes all media files for a season
func (db *DB) DeleteMediaFilesBySeason(seriesID int64, seasonNumber int) error {
	result, err := db.Exec(`
		DELETE FROM media_files
		WHERE series_id = ? AND season_number = ?
	`, seriesID, seasonNumber)

	if err != nil {
		return fmt.Errorf("failed to delete media files: %w", err)
	}

	affected, _ := result.RowsAffected()
	slog.Info("Deleted media files", "series_id", seriesID, "season", seasonNumber, "count", affected)
	return nil
}

// InvalidateCachedData invalidates all cached data related to changed media files
// This includes audio track information and processed file records
func (db *DB) InvalidateCachedData(filePath string) error {
	// Delete from processed_files table
	_, err := db.Exec(`DELETE FROM processed_files WHERE file_path = ?`, filePath)
	if err != nil {
		return fmt.Errorf("failed to invalidate processed files: %w", err)
	}

	slog.Info("Invalidated cached data for file", "path", filePath)
	return nil
}

// InvalidateCachedDataForSeason invalidates all cached data for a season
func (db *DB) InvalidateCachedDataForSeason(seriesID int64, seasonNumber int) error {
	// Get all media files for this season
	files, err := db.GetMediaFilesBySeason(seriesID, seasonNumber)
	if err != nil {
		return err
	}

	// Invalidate each file
	for i := range files {
		if err := db.InvalidateCachedData(files[i].FilePath); err != nil {
			slog.Error("Failed to invalidate file", "path", files[i].FilePath, "error", err)
		}
	}

	slog.Info("Invalidated cached data for season",
		"series_id", seriesID, "season", seasonNumber, "files", len(files))
	return nil
}

// CheckFileChanged checks if a file's hash has changed and invalidates cache if needed
func (db *DB) CheckFileChanged(filePath, currentHash string) (bool, error) {
	existing, err := db.GetMediaFileByPath(filePath)
	if err != nil {
		return false, err
	}

	if existing == nil {
		// File is new, not changed
		return false, nil
	}

	changed := existing.FileHash != currentHash

	if changed {
		slog.Info("File hash changed, invalidating cache",
			"path", filePath,
			"old_hash", existing.FileHash,
			"new_hash", currentHash)

		// Invalidate cached data
		if err := db.InvalidateCachedData(filePath); err != nil {
			return true, err
		}
	}

	return changed, nil
}

// GetStaleMediaFiles returns media files that haven't been checked in the specified duration
func (db *DB) GetStaleMediaFiles(staleDuration time.Duration) ([]MediaFile, error) {
	staleTime := time.Now().Add(-staleDuration)

	rows, err := db.Query(`
		SELECT id, series_id, season_number, file_path, file_name, file_size,
		       file_hash, mod_time, first_seen, last_checked
		FROM media_files
		WHERE last_checked < ?
		ORDER BY last_checked ASC
	`, staleTime)

	if err != nil {
		return nil, fmt.Errorf("failed to query stale media files: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	var files []MediaFile
	for rows.Next() {
		var mf MediaFile
		err := rows.Scan(
			&mf.ID, &mf.SeriesID, &mf.SeasonNumber, &mf.FilePath, &mf.FileName,
			&mf.FileSize, &mf.FileHash, &mf.ModTime, &mf.FirstSeen, &mf.LastChecked,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan media file: %w", err)
		}
		files = append(files, mf)
	}

	return files, nil
}

// CleanupOrphanedMediaFiles removes media file records for files that no longer exist
func (db *DB) CleanupOrphanedMediaFiles() (int, error) {
	// This should be called periodically by a background task
	// For now, we'll just mark this as a placeholder
	// Real implementation would check filesystem and delete records for missing files
	return 0, nil
}
