package database

import (
	"database/sql"
	"fmt"
	"time"
)

// Recommendation represents a TMDB-sourced show suggestion filtered by Kinozal availability.
type Recommendation struct {
	TVDBID        int
	TMDBID        int
	Title         string
	OriginalTitle string
	Overview      string
	PosterURL     string
	Year          int
	Rating        float64
	Genres        string
	Score         float64
	TrackerURL    string
	TorrentTitle  string
	TorrentSize   string
	CreatedAt     time.Time
}

// BlacklistEntry represents a user-blacklisted show, excluded from future recommendations.
type BlacklistEntry struct {
	TVDBID        int
	Title         string
	BlacklistedAt time.Time
}

// GetRecommendations returns all recommendations ordered by score DESC.
func (db *DB) GetRecommendations() ([]Recommendation, error) {
	rows, err := db.Query(`
		SELECT tvdb_id, tmdb_id, title, original_title, overview, poster_url,
		       year, rating, genres, score, tracker_url, torrent_title, torrent_size, created_at
		FROM recommendations
		ORDER BY score DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to query recommendations: %w", err)
	}
	defer rows.Close()

	var recs []Recommendation
	for rows.Next() {
		var r Recommendation
		var tmdbID, year sql.NullInt64
		var originalTitle, overview, posterURL, genres, torrentTitle, torrentSize sql.NullString
		var rating sql.NullFloat64
		if err := rows.Scan(
			&r.TVDBID, &tmdbID, &r.Title, &originalTitle, &overview, &posterURL,
			&year, &rating, &genres, &r.Score, &r.TrackerURL, &torrentTitle, &torrentSize, &r.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan recommendation: %w", err)
		}
		r.TMDBID = int(tmdbID.Int64)
		r.OriginalTitle = originalTitle.String
		r.Overview = overview.String
		r.PosterURL = posterURL.String
		r.Year = int(year.Int64)
		r.Rating = rating.Float64
		r.Genres = genres.String
		r.TorrentTitle = torrentTitle.String
		r.TorrentSize = torrentSize.String
		recs = append(recs, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}
	return recs, nil
}

// ReplaceRecommendations atomically deletes all existing recommendations and inserts the new batch.
func (db *DB) ReplaceRecommendations(recs []Recommendation) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after commit

	if _, err := tx.Exec(`DELETE FROM recommendations`); err != nil {
		return fmt.Errorf("failed to delete recommendations: %w", err)
	}

	stmt, err := tx.Prepare(`
		INSERT INTO recommendations (
			tvdb_id, tmdb_id, title, original_title, overview, poster_url,
			year, rating, genres, score, tracker_url, torrent_title, torrent_size
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare insert: %w", err)
	}
	defer stmt.Close()

	for _, r := range recs {
		if _, err := stmt.Exec(
			r.TVDBID, r.TMDBID, r.Title, r.OriginalTitle, r.Overview, r.PosterURL,
			r.Year, r.Rating, r.Genres, r.Score, r.TrackerURL, r.TorrentTitle, r.TorrentSize,
		); err != nil {
			return fmt.Errorf("failed to insert recommendation %d: %w", r.TVDBID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit recommendations: %w", err)
	}
	return nil
}

// AddToBlacklist inserts the show into the blacklist and removes any matching recommendation.
func (db *DB) AddToBlacklist(tvdbID int, title string) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.Exec(`
		INSERT INTO recommendation_blacklist (tvdb_id, title)
		VALUES (?, ?)
		ON CONFLICT(tvdb_id) DO UPDATE SET title = excluded.title
	`, tvdbID, title); err != nil {
		return fmt.Errorf("failed to insert blacklist entry: %w", err)
	}

	if _, err := tx.Exec(`DELETE FROM recommendations WHERE tvdb_id = ?`, tvdbID); err != nil {
		return fmt.Errorf("failed to delete recommendation: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit blacklist add: %w", err)
	}
	return nil
}

// RemoveFromBlacklist deletes the given show from the blacklist.
func (db *DB) RemoveFromBlacklist(tvdbID int) error {
	if _, err := db.Exec(`DELETE FROM recommendation_blacklist WHERE tvdb_id = ?`, tvdbID); err != nil {
		return fmt.Errorf("failed to remove blacklist entry: %w", err)
	}
	return nil
}

// GetBlacklist returns all blacklist entries ordered by blacklisted_at DESC.
func (db *DB) GetBlacklist() ([]BlacklistEntry, error) {
	rows, err := db.Query(`
		SELECT tvdb_id, title, blacklisted_at
		FROM recommendation_blacklist
		ORDER BY blacklisted_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to query blacklist: %w", err)
	}
	defer rows.Close()

	var entries []BlacklistEntry
	for rows.Next() {
		var e BlacklistEntry
		var title sql.NullString
		if err := rows.Scan(&e.TVDBID, &title, &e.BlacklistedAt); err != nil {
			return nil, fmt.Errorf("failed to scan blacklist entry: %w", err)
		}
		e.Title = title.String
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}
	return entries, nil
}

// GetBlacklistedIDs returns a set of all blacklisted TVDB IDs for fast lookup.
func (db *DB) GetBlacklistedIDs() (map[int]bool, error) {
	rows, err := db.Query(`SELECT tvdb_id FROM recommendation_blacklist`)
	if err != nil {
		return nil, fmt.Errorf("failed to query blacklisted IDs: %w", err)
	}
	defer rows.Close()

	ids := make(map[int]bool)
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("failed to scan blacklist id: %w", err)
		}
		ids[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}
	return ids, nil
}
