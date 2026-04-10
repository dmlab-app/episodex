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
		voice_actor_id INTEGER,
		downloaded BOOLEAN DEFAULT 0,
		aired_episodes INTEGER DEFAULT 0,
		tracker_url TEXT,
		torrent_hash TEXT,
		tracker_updated_at TEXT,
		discovered_at TIMESTAMP,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(series_id, season_number),
		FOREIGN KEY (series_id) REFERENCES series(id) ON DELETE CASCADE,
		FOREIGN KEY (voice_actor_id) REFERENCES voice_actors(id) ON DELETE SET NULL
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

	-- Справочник озвучек
	CREATE TABLE IF NOT EXISTS voice_actors (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT UNIQUE NOT NULL
	);

	-- Обработанные файлы (AudioCutter)
	CREATE TABLE IF NOT EXISTS processed_files (
		file_path TEXT PRIMARY KEY,
		series_id INTEGER,
		season_number INTEGER,
		track_kept INTEGER,
		track_language TEXT,
		track_name TEXT,
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

	-- Метаданные бекапов
	CREATE TABLE IF NOT EXISTS backups (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		filename TEXT NOT NULL,
		size_bytes INTEGER,
		integrity_ok INTEGER,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
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

	// Seed default voice actors if table is empty
	if err := db.seedVoiceActors(); err != nil {
		return fmt.Errorf("failed to seed voice actors: %w", err)
	}

	return nil
}

// seedVoiceActors inserts popular voice actors if the table is empty
func (db *DB) seedVoiceActors() error {
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM voice_actors").Scan(&count)
	if err != nil {
		return err
	}

	if count > 0 {
		return nil // Already seeded
	}

	defaultVoices := []string{
		"LostFilm",
		"Кубик в Кубе",
		"Amedia",
		"NewStudio",
		"ColdFilm",
		"Jaskier",
		"AlexFilm",
		"SDI Media",
		"Original",
	}

	stmt, err := db.Prepare("INSERT INTO voice_actors (name) VALUES (?)")
	if err != nil {
		return err
	}
	defer stmt.Close() //nolint:errcheck // closing prepared statement

	for _, name := range defaultVoices {
		if _, err := stmt.Exec(name); err != nil {
			return err
		}
	}

	slog.Info("Seeded default voice actors", "count", len(defaultVoices))
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
