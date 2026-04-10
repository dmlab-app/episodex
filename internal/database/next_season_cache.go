package database

import (
	"database/sql"
	"fmt"
	"time"
)

// NextSeasonCache represents a cached Kinozal search result for a series' next season.
type NextSeasonCache struct {
	SeriesID     int64
	SeasonNumber int
	TrackerURL   string
	Title        string
	Size         string
	CachedAt     time.Time
}

// GetCachedNextSeason returns a cached torrent result for the given series and season,
// or nil if no cache entry exists.
func (db *DB) GetCachedNextSeason(seriesID int64, seasonNumber int) (*NextSeasonCache, error) {
	var c NextSeasonCache
	err := db.QueryRow(`
		SELECT series_id, season_number, tracker_url, title, size, cached_at
		FROM next_season_cache
		WHERE series_id = ? AND season_number = ?
	`, seriesID, seasonNumber).Scan(
		&c.SeriesID, &c.SeasonNumber, &c.TrackerURL, &c.Title, &c.Size, &c.CachedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get cached next season: %w", err)
	}

	return &c, nil
}

// SaveCachedNextSeason inserts or replaces a cache entry for the given series and season.
func (db *DB) SaveCachedNextSeason(c *NextSeasonCache) error {
	_, err := db.Exec(`
		INSERT INTO next_season_cache (series_id, season_number, tracker_url, title, size, cached_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(series_id, season_number) DO UPDATE SET
			tracker_url = excluded.tracker_url,
			title = excluded.title,
			size = excluded.size,
			cached_at = excluded.cached_at
	`, c.SeriesID, c.SeasonNumber, c.TrackerURL, c.Title, c.Size, time.Now())

	if err != nil {
		return fmt.Errorf("failed to save cached next season: %w", err)
	}
	return nil
}

// ClearExpiredCache deletes cache entries older than the given duration.
func (db *DB) ClearExpiredCache(maxAge time.Duration) (int64, error) {
	cutoff := time.Now().Add(-maxAge)
	result, err := db.Exec(`
		DELETE FROM next_season_cache WHERE cached_at < ?
	`, cutoff)

	if err != nil {
		return 0, fmt.Errorf("failed to clear expired cache: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to get rows affected: %w", err)
	}
	return affected, nil
}
