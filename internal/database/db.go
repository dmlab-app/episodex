// Package database provides SQLite database operations and backup management.
package database

import (
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

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

	// Pre-migrations: add new columns to existing tables BEFORE creating indexes
	// This is needed because the schema has indexes on new columns
	db.preMigrations()

	// Initialize tables (creates tables and indexes)
	if err := db.initTables(); err != nil {
		sqlDB.Close() //nolint:errcheck // best-effort cleanup on init failure
		return nil, fmt.Errorf("failed to initialize tables: %w", err)
	}

	// Run migrations (data migrations)
	if err := db.runMigrations(); err != nil {
		sqlDB.Close() //nolint:errcheck // best-effort cleanup on init failure
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	slog.Info("Database initialized", "path", dbPath)
	return db, nil
}

// initTables creates all required tables
func (db *DB) initTables() error {
	schema := `
	-- Сериалы (полные метаданные из TVDB)
	CREATE TABLE IF NOT EXISTS series (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		tvdb_id INTEGER UNIQUE,
		title TEXT NOT NULL,
		original_title TEXT,
		slug TEXT,
		overview TEXT,
		poster_url TEXT,
		backdrop_url TEXT,
		status TEXT,
		first_aired DATE,
		last_aired DATE,
		year INTEGER,
		runtime INTEGER,
		rating REAL,
		content_rating TEXT,
		original_country TEXT,
		original_language TEXT,
		genres TEXT,
		networks TEXT,
		studios TEXT,
		total_seasons INTEGER DEFAULT 0,
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
		overview TEXT,
		poster_url TEXT,
		first_aired DATE,
		episode_count INTEGER,
		folder_path TEXT,
		voice_actor_id INTEGER,
		is_owned BOOLEAN DEFAULT 0,
		discovered_at TIMESTAMP,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(series_id, season_number),
		FOREIGN KEY (series_id) REFERENCES series(id) ON DELETE CASCADE,
		FOREIGN KEY (voice_actor_id) REFERENCES voice_actors(id) ON DELETE SET NULL
	);

	-- Эпизоды
	CREATE TABLE IF NOT EXISTS episodes (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		season_id INTEGER NOT NULL,
		tvdb_episode_id INTEGER,
		episode_number INTEGER NOT NULL,
		title TEXT,
		overview TEXT,
		image_url TEXT,
		air_date DATE,
		runtime INTEGER,
		rating REAL,
		file_path TEXT,
		file_hash TEXT,
		file_size INTEGER,
		is_owned BOOLEAN DEFAULT 0,
		watched_at TIMESTAMP,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(season_id, episode_number),
		FOREIGN KEY (season_id) REFERENCES seasons(id) ON DELETE CASCADE
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

	-- Artwork (постеры, фоны, баннеры)
	CREATE TABLE IF NOT EXISTS artworks (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		series_id INTEGER,
		season_id INTEGER,
		tvdb_artwork_id INTEGER,
		type TEXT,
		url TEXT NOT NULL,
		thumbnail_url TEXT,
		language TEXT,
		score REAL,
		width INTEGER,
		height INTEGER,
		is_primary BOOLEAN DEFAULT 0,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (series_id) REFERENCES series(id) ON DELETE CASCADE,
		FOREIGN KEY (season_id) REFERENCES seasons(id) ON DELETE CASCADE
	);

	-- Watched seasons (DEPRECATED - kept for migration)
	CREATE TABLE IF NOT EXISTS watched_seasons (
		series_id INTEGER NOT NULL,
		season_number INTEGER NOT NULL,
		voice_actor_id INTEGER,
		folder_path TEXT,
		source TEXT DEFAULT 'scan',
		discovered_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (series_id, season_number),
		FOREIGN KEY (series_id) REFERENCES series(id) ON DELETE CASCADE,
		FOREIGN KEY (voice_actor_id) REFERENCES voice_actors(id) ON DELETE SET NULL
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

	-- История сканирований
	CREATE TABLE IF NOT EXISTS scan_history (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		started_at TIMESTAMP,
		finished_at TIMESTAMP,
		folders_scanned INTEGER DEFAULT 0,
		new_series INTEGER DEFAULT 0,
		new_seasons INTEGER DEFAULT 0
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

	-- Индексы для производительности
	CREATE INDEX IF NOT EXISTS idx_series_tvdb_id ON series(tvdb_id);
	CREATE INDEX IF NOT EXISTS idx_series_status ON series(status);
	CREATE INDEX IF NOT EXISTS idx_series_year ON series(year);
	CREATE INDEX IF NOT EXISTS idx_seasons_series ON seasons(series_id);
	CREATE INDEX IF NOT EXISTS idx_seasons_tvdb_id ON seasons(tvdb_season_id);
	CREATE INDEX IF NOT EXISTS idx_episodes_season ON episodes(season_id);
	CREATE INDEX IF NOT EXISTS idx_episodes_tvdb_id ON episodes(tvdb_episode_id);
	CREATE INDEX IF NOT EXISTS idx_episodes_file_path ON episodes(file_path);
	CREATE INDEX IF NOT EXISTS idx_characters_series ON series_characters(series_id);
	CREATE INDEX IF NOT EXISTS idx_artworks_series ON artworks(series_id);
	CREATE INDEX IF NOT EXISTS idx_artworks_season ON artworks(season_id);
	CREATE INDEX IF NOT EXISTS idx_watched_seasons_series ON watched_seasons(series_id);
	CREATE INDEX IF NOT EXISTS idx_system_alerts_dismissed ON system_alerts(dismissed);
	CREATE INDEX IF NOT EXISTS idx_scan_history_started ON scan_history(started_at DESC);
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

// preMigrations adds new columns to existing tables BEFORE initTables runs.
// This is necessary because initTables creates indexes on these columns.
// Errors are logged but not returned because pre-migrations are best-effort:
// columns may already exist, or the table may not exist yet (first run).
func (db *DB) preMigrations() {
	// Check if series table exists
	var tableExists int
	err := db.QueryRow(`
		SELECT COUNT(*) FROM sqlite_master
		WHERE type='table' AND name='series'
	`).Scan(&tableExists)
	if err != nil || tableExists == 0 {
		// Table doesn't exist yet, nothing to migrate
		return
	}

	// Add new columns to series table if they don't exist
	newColumns := []struct {
		name       string
		definition string
	}{
		{"slug", "ALTER TABLE series ADD COLUMN slug TEXT"},
		{"overview", "ALTER TABLE series ADD COLUMN overview TEXT"},
		{"backdrop_url", "ALTER TABLE series ADD COLUMN backdrop_url TEXT"},
		{"first_aired", "ALTER TABLE series ADD COLUMN first_aired DATE"},
		{"last_aired", "ALTER TABLE series ADD COLUMN last_aired DATE"},
		{"year", "ALTER TABLE series ADD COLUMN year INTEGER"},
		{"runtime", "ALTER TABLE series ADD COLUMN runtime INTEGER"},
		{"rating", "ALTER TABLE series ADD COLUMN rating REAL"},
		{"content_rating", "ALTER TABLE series ADD COLUMN content_rating TEXT"},
		{"original_country", "ALTER TABLE series ADD COLUMN original_country TEXT"},
		{"original_language", "ALTER TABLE series ADD COLUMN original_language TEXT"},
		{"genres", "ALTER TABLE series ADD COLUMN genres TEXT"},
		{"networks", "ALTER TABLE series ADD COLUMN networks TEXT"},
		{"studios", "ALTER TABLE series ADD COLUMN studios TEXT"},
	}

	for _, col := range newColumns {
		var exists int
		err := db.QueryRow(`
			SELECT COUNT(*)
			FROM pragma_table_info('series')
			WHERE name = ?
		`, col.name).Scan(&exists)
		if err != nil {
			continue // Table might not exist yet
		}

		if exists == 0 {
			slog.Info("Pre-migration: Adding column to series table", "column", col.name)
			_, err := db.Exec(col.definition)
			if err != nil {
				slog.Warn("Failed to add column (may already exist)", "column", col.name, "error", err)
			}
		}
	}
}

// runMigrations applies database schema migrations
func (db *DB) runMigrations() error {
	// Legacy migrations for watched_seasons (still needed for backward compatibility)
	if err := db.migrateLegacyWatchedSeasons(); err != nil {
		return err
	}

	// New migrations for schema v2
	if err := db.migrateToSchemaV2(); err != nil {
		return err
	}

	return nil
}

// migrateLegacyWatchedSeasons handles old migrations for watched_seasons table
func (db *DB) migrateLegacyWatchedSeasons() error {
	var columnExists int

	// Check if folder_path column exists in watched_seasons
	err := db.QueryRow(`
		SELECT COUNT(*)
		FROM pragma_table_info('watched_seasons')
		WHERE name='folder_path'
	`).Scan(&columnExists)
	if err != nil {
		return fmt.Errorf("failed to check schema: %w", err)
	}
	if columnExists == 0 {
		slog.Info("Running migration: adding folder_path column to watched_seasons")
		_, err := db.Exec(`ALTER TABLE watched_seasons ADD COLUMN folder_path TEXT`)
		if err != nil {
			return fmt.Errorf("failed to add folder_path column: %w", err)
		}
		slog.Info("Migration completed: folder_path column added")
	}

	// Check if source column exists in watched_seasons
	err = db.QueryRow(`
		SELECT COUNT(*)
		FROM pragma_table_info('watched_seasons')
		WHERE name='source'
	`).Scan(&columnExists)
	if err != nil {
		return fmt.Errorf("failed to check schema: %w", err)
	}
	if columnExists == 0 {
		slog.Info("Running migration: adding source column to watched_seasons")
		_, err := db.Exec(`ALTER TABLE watched_seasons ADD COLUMN source TEXT DEFAULT 'scan'`)
		if err != nil {
			return fmt.Errorf("failed to add source column: %w", err)
		}
		slog.Info("Migration completed: source column added")
	}

	// Check if discovered_at column exists in watched_seasons
	err = db.QueryRow(`
		SELECT COUNT(*)
		FROM pragma_table_info('watched_seasons')
		WHERE name='discovered_at'
	`).Scan(&columnExists)
	if err != nil {
		return fmt.Errorf("failed to check schema: %w", err)
	}
	if columnExists == 0 {
		slog.Info("Running migration: adding discovered_at column to watched_seasons")
		_, err := db.Exec(`ALTER TABLE watched_seasons ADD COLUMN discovered_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP`)
		if err != nil {
			return fmt.Errorf("failed to add discovered_at column: %w", err)
		}
		slog.Info("Migration completed: discovered_at column added")
	}

	return nil
}

// migrateToSchemaV2 migrates data from watched_seasons to new seasons table
func (db *DB) migrateToSchemaV2() error {
	// Check if migration has already been run by checking if seasons table has any data
	var seasonCount int
	err := db.QueryRow(`SELECT COUNT(*) FROM seasons`).Scan(&seasonCount)
	if err != nil {
		return fmt.Errorf("failed to check seasons table: %w", err)
	}

	// Check if there's data in watched_seasons to migrate
	var watchedCount int
	err = db.QueryRow(`SELECT COUNT(*) FROM watched_seasons`).Scan(&watchedCount)
	if err != nil {
		return fmt.Errorf("failed to check watched_seasons table: %w", err)
	}

	// Migrate data from watched_seasons to seasons (handles both initial and incremental cases)
	if watchedCount > 0 {
		// Check if voice_actor_id column exists in watched_seasons
		var hasVoiceActorID int
		err = db.QueryRow(`
			SELECT COUNT(*) FROM pragma_table_info('watched_seasons')
			WHERE name = 'voice_actor_id'
		`).Scan(&hasVoiceActorID)
		if err != nil {
			hasVoiceActorID = 0
		}

		// Migrate rows from watched_seasons that don't already exist in seasons
		var migrateQuery string
		if hasVoiceActorID > 0 {
			migrateQuery = `
				INSERT INTO seasons (series_id, season_number, folder_path, voice_actor_id, is_owned, discovered_at)
				SELECT ws.series_id, ws.season_number, ws.folder_path, ws.voice_actor_id, 1, ws.discovered_at
				FROM watched_seasons ws
				WHERE NOT EXISTS (
					SELECT 1 FROM seasons sn
					WHERE sn.series_id = ws.series_id AND sn.season_number = ws.season_number
				)
			`
		} else {
			migrateQuery = `
				INSERT INTO seasons (series_id, season_number, folder_path, is_owned, discovered_at)
				SELECT ws.series_id, ws.season_number, ws.folder_path, 1, ws.discovered_at
				FROM watched_seasons ws
				WHERE NOT EXISTS (
					SELECT 1 FROM seasons sn
					WHERE sn.series_id = ws.series_id AND sn.season_number = ws.season_number
				)
			`
		}

		result, err := db.Exec(migrateQuery)
		if err != nil {
			return fmt.Errorf("failed to migrate watched_seasons data: %w", err)
		}

		migrated, _ := result.RowsAffected() // SQLite always supports RowsAffected
		if migrated > 0 {
			slog.Info("Migration completed: migrated watched_seasons to seasons", "migrated", migrated)
		}
	}

	// Ensure all (series_id, season_number) pairs referenced by media_files exist in seasons.
	// Without this, FK enforcement would reject orphaned media_files rows from before migration.
	_, err = db.Exec(`
		INSERT OR IGNORE INTO seasons (series_id, season_number, is_owned)
		SELECT DISTINCT mf.series_id, mf.season_number, 1
		FROM media_files mf
		WHERE NOT EXISTS (
			SELECT 1 FROM seasons sn
			WHERE sn.series_id = mf.series_id AND sn.season_number = mf.season_number
		)
	`)
	if err != nil {
		slog.Warn("Failed to backfill seasons from media_files", "error", err)
	}

	// Note: New columns are added in preMigrations() which runs before initTables()
	// This is necessary because initTables creates indexes on these columns

	return nil
}
