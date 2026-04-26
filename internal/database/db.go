// Package database provides SQLite database operations and backup management.
package database

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"database/sql"

	_ "modernc.org/sqlite" // SQLite driver registration
)

// DB wraps the database connection
type DB struct {
	*sql.DB
}

// New creates a new database connection and initializes tables
func New(dbPath string) (*DB, error) {
	// Ensure the directory exists
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create database directory: %w", err)
	}

	// Open database connection
	sqlDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Configure connection pool
	sqlDB.SetMaxOpenConns(1) // SQLite works best with single connection
	sqlDB.SetMaxIdleConns(1)

	db := &DB{DB: sqlDB}

	// Enable foreign key enforcement (required per-connection in SQLite)
	if _, err := sqlDB.Exec("PRAGMA foreign_keys = ON"); err != nil {
		sqlDB.Close() //nolint:errcheck
		return nil, fmt.Errorf("failed to enable foreign keys: %w", err)
	}

	// Initialize tables (creates tables and indexes)
	if err := db.initTables(); err != nil {
		sqlDB.Close() //nolint:errcheck // best-effort cleanup on init failure
		return nil, fmt.Errorf("failed to initialize tables: %w", err)
	}

	slog.Info("Database initialized", "path", dbPath)
	return db, nil
}

// initTables creates all required tables
func (db *DB) initTables() error {
	schema := `
	-- Сериалы (метаданные из TVDB)
	CREATE TABLE IF NOT EXISTS series (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		tvdb_id INTEGER UNIQUE,
		title TEXT NOT NULL,
		original_title TEXT,
		overview TEXT,
		poster_url TEXT,
		backdrop_url TEXT,
		status TEXT,
		year INTEGER,
		runtime INTEGER,
		rating REAL,
		content_rating TEXT,
		genres TEXT,
		networks TEXT,
		total_seasons INTEGER DEFAULT 0,
		aired_seasons INTEGER DEFAULT 0,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);

	-- Сезоны
	CREATE TABLE IF NOT EXISTS seasons (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		series_id INTEGER NOT NULL,
		tvdb_season_id INTEGER,
		season_number INTEGER NOT NULL,
		name TEXT,
		poster_url TEXT,
		folder_path TEXT,
		track_name TEXT,
		downloaded BOOLEAN DEFAULT 0,
		aired_episodes INTEGER DEFAULT 0,
		max_episode_on_disk INTEGER DEFAULT 0,
		tracker_url TEXT,
		torrent_hash TEXT,
		auto_process BOOLEAN DEFAULT 0,
		tracker_updated_at TEXT,
		discovered_at TIMESTAMP,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(series_id, season_number),
		FOREIGN KEY (series_id) REFERENCES series(id) ON DELETE CASCADE
	);

	-- Актёры/персонажи сериала
	CREATE TABLE IF NOT EXISTS series_characters (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		series_id INTEGER NOT NULL,
		tvdb_character_id INTEGER,
		tvdb_person_id INTEGER,
		character_name TEXT,
		actor_name TEXT,
		image_url TEXT,
		sort_order INTEGER,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (series_id) REFERENCES series(id) ON DELETE CASCADE
	);

	-- Обработанные файлы (AudioCutter)
	CREATE TABLE IF NOT EXISTS processed_files (
		file_path TEXT PRIMARY KEY,
		series_id INTEGER,
		season_number INTEGER,
		track_kept TEXT,
		processed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (series_id) REFERENCES series(id) ON DELETE SET NULL
	);

	-- Медиафайлы с хешами для отслеживания изменений
	CREATE TABLE IF NOT EXISTS media_files (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		series_id INTEGER NOT NULL,
		season_number INTEGER NOT NULL,
		file_path TEXT NOT NULL UNIQUE,
		file_name TEXT NOT NULL,
		file_size INTEGER NOT NULL,
		file_hash TEXT NOT NULL,
		mod_time INTEGER,
		first_seen TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		last_checked TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (series_id, season_number) REFERENCES seasons(series_id, season_number) ON DELETE CASCADE
	);

	-- Системные алерты (для UI)
	CREATE TABLE IF NOT EXISTS system_alerts (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		type TEXT NOT NULL,
		message TEXT,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		dismissed INTEGER DEFAULT 0
	);

	-- Tombstones for user-deleted seasons.
	-- Prevents TVDB sync (upsertSeasonTx) from resurrecting a season the user
	-- explicitly removed. Cleared when the scanner re-discovers files for the
	-- same (series_id, season_number), since presence of files signals the user
	-- wants the season back. CASCADE on series delete keeps the table clean.
	CREATE TABLE IF NOT EXISTS deleted_seasons (
		series_id INTEGER NOT NULL,
		season_number INTEGER NOT NULL,
		deleted_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (series_id, season_number),
		FOREIGN KEY (series_id) REFERENCES series(id) ON DELETE CASCADE
	);

	-- Кеш найденных торрентов для следующего сезона
	CREATE TABLE IF NOT EXISTS next_season_cache (
		series_id INTEGER NOT NULL,
		season_number INTEGER NOT NULL,
		tracker_url TEXT,
		title TEXT,
		size TEXT,
		cached_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(series_id, season_number)
	);

	-- Рекомендации (из TMDB, отфильтрованные по Kinozal)
	CREATE TABLE IF NOT EXISTS recommendations (
		tvdb_id INTEGER PRIMARY KEY,
		tmdb_id INTEGER,
		title TEXT NOT NULL,
		original_title TEXT,
		overview TEXT,
		poster_url TEXT,
		year INTEGER,
		rating REAL,
		genres TEXT,
		score REAL NOT NULL,
		tracker_url TEXT NOT NULL,
		torrent_title TEXT,
		torrent_size TEXT,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);

	-- Чёрный список рекомендаций
	CREATE TABLE IF NOT EXISTS recommendation_blacklist (
		tvdb_id INTEGER PRIMARY KEY,
		title TEXT,
		blacklisted_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);

	-- Индексы для производительности
	CREATE INDEX IF NOT EXISTS idx_series_tvdb_id ON series(tvdb_id);
	CREATE INDEX IF NOT EXISTS idx_series_status ON series(status);
	CREATE INDEX IF NOT EXISTS idx_series_year ON series(year);
	CREATE INDEX IF NOT EXISTS idx_seasons_series ON seasons(series_id);
	CREATE INDEX IF NOT EXISTS idx_seasons_tvdb_id ON seasons(tvdb_season_id);
	CREATE INDEX IF NOT EXISTS idx_characters_series ON series_characters(series_id);
	CREATE INDEX IF NOT EXISTS idx_system_alerts_dismissed ON system_alerts(dismissed);
	CREATE INDEX IF NOT EXISTS idx_media_files_series ON media_files(series_id, season_number);
	CREATE INDEX IF NOT EXISTS idx_media_files_hash ON media_files(file_hash);
	`

	_, err := db.Exec(schema)
	if err != nil {
		return fmt.Errorf("failed to create tables: %w", err)
	}

	return nil
}

// Close closes the database connection
func (db *DB) Close() error {
	return db.DB.Close()
}

// Ping checks if database is accessible
func (db *DB) Ping() error {
	return db.DB.Ping()
}
