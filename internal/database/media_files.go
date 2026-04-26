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
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate media files: %w", err)
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

// GetMediaFilePathsBySeriesID returns all file paths for media files belonging to a series
func (db *DB) GetMediaFilePathsBySeriesID(seriesID int64) ([]string, error) {
	rows, err := db.Query(`
		SELECT file_path FROM media_files WHERE series_id = ? ORDER BY file_path
	`, seriesID)
	if err != nil {
		return nil, fmt.Errorf("failed to query media file paths: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	var paths []string
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return nil, fmt.Errorf("failed to scan media file path: %w", err)
		}
		paths = append(paths, path)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate media file paths: %w", err)
	}
	return paths, nil
}

// GetMediaFilePathsBySeason returns all file paths for media files belonging to a single season.
func (db *DB) GetMediaFilePathsBySeason(seriesID int64, seasonNumber int) ([]string, error) {
	rows, err := db.Query(`
		SELECT file_path FROM media_files
		WHERE series_id = ? AND season_number = ?
		ORDER BY file_path
	`, seriesID, seasonNumber)
	if err != nil {
		return nil, fmt.Errorf("failed to query media file paths: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	var paths []string
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return nil, fmt.Errorf("failed to scan media file path: %w", err)
		}
		paths = append(paths, path)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate media file paths: %w", err)
	}
	return paths, nil
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

// DeleteProcessedFilesBySeason deletes all processed file records for a season
func (db *DB) DeleteProcessedFilesBySeason(seriesID int64, seasonNumber int) error {
	result, err := db.Exec(`
		DELETE FROM processed_files
		WHERE series_id = ? AND season_number = ?
	`, seriesID, seasonNumber)

	if err != nil {
		return fmt.Errorf("failed to delete processed files: %w", err)
	}

	affected, _ := result.RowsAffected()
	if affected > 0 {
		slog.Info("Deleted processed files", "series_id", seriesID, "season", seasonNumber, "count", affected)
	}
	return nil
}

// IsFileProcessed returns true if the file path exists in processed_files.
func (db *DB) IsFileProcessed(filePath string) (bool, error) {
	var exists bool
	err := db.QueryRow(`SELECT COUNT(*) > 0 FROM processed_files WHERE file_path = ?`, filePath).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("failed to check processed file: %w", err)
	}
	return exists, nil
}

// GetProcessedFilePathsBySeason returns all processed file paths for a season.
func (db *DB) GetProcessedFilePathsBySeason(seriesID int64, seasonNumber int) ([]string, error) {
	rows, err := db.Query(`
		SELECT file_path FROM processed_files
		WHERE series_id = ? AND season_number = ?
		ORDER BY file_path
	`, seriesID, seasonNumber)
	if err != nil {
		return nil, fmt.Errorf("failed to query processed file paths: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	var paths []string
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return nil, fmt.Errorf("failed to scan processed file path: %w", err)
		}
		paths = append(paths, path)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate processed file paths: %w", err)
	}
	return paths, nil
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

// InsertProcessedFile adds a file to the processed_files table.
func (db *DB) InsertProcessedFile(filePath string, seriesID int64, seasonNumber int, trackKept string) error {
	_, err := db.Exec(`
		INSERT OR IGNORE INTO processed_files (file_path, series_id, season_number, track_kept, processed_at)
		VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)
	`, filePath, seriesID, seasonNumber, trackKept)
	if err != nil {
		return fmt.Errorf("failed to insert processed file: %w", err)
	}
	return nil
}

