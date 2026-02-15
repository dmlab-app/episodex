package scanner

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/episodex/episodex/internal/database"
)

// setupTestDB creates a temporary SQLite database for testing.
func setupTestDB(t *testing.T) *database.DB {
	t.Helper()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := database.New(dbPath)
	if err != nil {
		t.Fatalf("failed to create test database: %v", err)
	}

	t.Cleanup(func() {
		db.Close()
	})

	return db
}

// seedTestSeries inserts a series and returns its ID.
func seedTestSeries(t *testing.T, db *database.DB, title string) int64 {
	t.Helper()
	result, err := db.Exec(`
		INSERT INTO series (title, status, total_seasons, created_at, updated_at)
		VALUES (?, 'Continuing', 5, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, title)
	if err != nil {
		t.Fatalf("failed to seed series: %v", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("failed to get last insert ID: %v", err)
	}
	return id
}

// seedTestSeason inserts a season with explicit is_owned.
func seedTestSeason(t *testing.T, db *database.DB, seriesID int64, seasonNum int, folderPath string, isWatched, isOwned bool) int64 {
	t.Helper()
	result, err := db.Exec(`
		INSERT INTO seasons (series_id, season_number, folder_path, is_watched, is_owned, discovered_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, seriesID, seasonNum, folderPath, isWatched, isOwned)
	if err != nil {
		t.Fatalf("failed to seed season: %v", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("failed to get last insert ID: %v", err)
	}
	return id
}

// seedTestMediaFile inserts a media file row.
func seedTestMediaFile(t *testing.T, db *database.DB, seriesID int64, seasonNum int, filePath string) {
	t.Helper()
	_, err := db.Exec(`
		INSERT INTO media_files (series_id, season_number, file_path, file_name, file_size, file_hash, mod_time)
		VALUES (?, ?, ?, ?, 1000, 'abc123', 0)
	`, seriesID, seasonNum, filePath, filepath.Base(filePath))
	if err != nil {
		t.Fatalf("failed to seed media file: %v", err)
	}
}

// createVideoFile creates a dummy .mkv file in the given directory.
func createVideoFile(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("failed to create directory %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("fake video"), 0o644); err != nil {
		t.Fatalf("failed to create video file: %v", err)
	}
}

func TestCleanupRemovedSeasons_FolderExists_KeepOwned(t *testing.T) {
	db := setupTestDB(t)
	sc := New(db, nil, t.TempDir())

	seriesID := seedTestSeries(t, db, "Test Show")

	// Create a real folder with a video file
	seasonDir := filepath.Join(t.TempDir(), "Season 1")
	createVideoFile(t, seasonDir, "episode.mkv")

	_ = seedTestSeason(t, db, seriesID, 1, seasonDir, true, true)
	seedTestMediaFile(t, db, seriesID, 1, filepath.Join(seasonDir, "episode.mkv"))

	// Run cleanup
	if err := sc.cleanupRemovedSeasons(); err != nil {
		t.Fatalf("cleanupRemovedSeasons failed: %v", err)
	}

	// Season should still be owned
	season, err := db.GetSeasonBySeriesAndNumber(seriesID, 1)
	if err != nil {
		t.Fatalf("failed to get season: %v", err)
	}
	if !season.IsOwned {
		t.Error("expected season to remain owned when folder exists with video files")
	}
	if season.FolderPath == nil || *season.FolderPath != seasonDir {
		t.Error("expected folder_path to remain unchanged")
	}

	// Media files should still exist
	files, err := db.GetMediaFilesBySeason(seriesID, 1)
	if err != nil {
		t.Fatalf("failed to get media files: %v", err)
	}
	if len(files) != 1 {
		t.Errorf("expected 1 media file, got %d", len(files))
	}
}

func TestCleanupRemovedSeasons_FolderGone_ClearOwned(t *testing.T) {
	db := setupTestDB(t)
	sc := New(db, nil, t.TempDir())

	seriesID := seedTestSeries(t, db, "Test Show")

	// Use a folder path that doesn't exist
	missingDir := filepath.Join(t.TempDir(), "nonexistent", "Season 1")
	_ = seedTestSeason(t, db, seriesID, 1, missingDir, true, true)
	seedTestMediaFile(t, db, seriesID, 1, filepath.Join(missingDir, "episode.mkv"))

	// Run cleanup
	if err := sc.cleanupRemovedSeasons(); err != nil {
		t.Fatalf("cleanupRemovedSeasons failed: %v", err)
	}

	// Season should no longer be owned
	season, err := db.GetSeasonBySeriesAndNumber(seriesID, 1)
	if err != nil {
		t.Fatalf("failed to get season: %v", err)
	}
	if season.IsOwned {
		t.Error("expected season to be not owned when folder is gone")
	}
	if season.FolderPath != nil {
		t.Errorf("expected folder_path to be NULL, got %v", *season.FolderPath)
	}
	// is_watched should be preserved
	if !season.IsWatched {
		t.Error("expected is_watched to remain true")
	}

	// Media files should be deleted
	files, err := db.GetMediaFilesBySeason(seriesID, 1)
	if err != nil {
		t.Fatalf("failed to get media files: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("expected 0 media files, got %d", len(files))
	}
}

func TestCleanupRemovedSeasons_FolderEmpty_ClearOwned(t *testing.T) {
	db := setupTestDB(t)
	sc := New(db, nil, t.TempDir())

	seriesID := seedTestSeries(t, db, "Test Show")

	// Create an empty folder (no video files)
	emptyDir := filepath.Join(t.TempDir(), "Season 1")
	if err := os.MkdirAll(emptyDir, 0o755); err != nil {
		t.Fatalf("failed to create empty dir: %v", err)
	}

	_ = seedTestSeason(t, db, seriesID, 1, emptyDir, true, true)
	seedTestMediaFile(t, db, seriesID, 1, filepath.Join(emptyDir, "episode.mkv"))

	// Run cleanup
	if err := sc.cleanupRemovedSeasons(); err != nil {
		t.Fatalf("cleanupRemovedSeasons failed: %v", err)
	}

	// Season should no longer be owned
	season, err := db.GetSeasonBySeriesAndNumber(seriesID, 1)
	if err != nil {
		t.Fatalf("failed to get season: %v", err)
	}
	if season.IsOwned {
		t.Error("expected season to be not owned when folder is empty")
	}
	if season.FolderPath != nil {
		t.Errorf("expected folder_path to be NULL, got %v", *season.FolderPath)
	}

	// Media files should be deleted
	files, err := db.GetMediaFilesBySeason(seriesID, 1)
	if err != nil {
		t.Fatalf("failed to get media files: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("expected 0 media files, got %d", len(files))
	}
}

func TestCleanupRemovedSeasons_NonOwnedSeason_NotTouched(t *testing.T) {
	db := setupTestDB(t)
	sc := New(db, nil, t.TempDir())

	seriesID := seedTestSeries(t, db, "Another Show")

	// A non-owned season (from TVDB sync) should not be affected
	seedTestSeason(t, db, seriesID, 2, "", false, false)

	if err := sc.cleanupRemovedSeasons(); err != nil {
		t.Fatalf("cleanupRemovedSeasons failed: %v", err)
	}

	season, err := db.GetSeasonBySeriesAndNumber(seriesID, 2)
	if err != nil {
		t.Fatalf("failed to get season: %v", err)
	}
	if season.IsOwned {
		t.Error("non-owned season should remain not owned")
	}
}

func TestCleanupRemovedSeasons_PreservesVoiceActorID(t *testing.T) {
	db := setupTestDB(t)
	sc := New(db, nil, t.TempDir())

	seriesID := seedTestSeries(t, db, "Voice Show")

	// Get a voice actor ID
	var voiceID int
	err := db.QueryRow(`SELECT id FROM voice_actors WHERE name = 'LostFilm'`).Scan(&voiceID)
	if err != nil {
		t.Fatalf("failed to get voice actor: %v", err)
	}

	// Create owned season with voice actor, folder gone
	missingDir := filepath.Join(t.TempDir(), "nonexistent")
	_, err = db.Exec(`
		INSERT INTO seasons (series_id, season_number, folder_path, voice_actor_id, is_watched, is_owned, discovered_at, created_at, updated_at)
		VALUES (?, 3, ?, ?, 1, 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, seriesID, missingDir, voiceID)
	if err != nil {
		t.Fatalf("failed to seed season with voice: %v", err)
	}

	if err := sc.cleanupRemovedSeasons(); err != nil {
		t.Fatalf("cleanupRemovedSeasons failed: %v", err)
	}

	// voice_actor_id should be preserved
	var savedVoiceID *int
	err = db.QueryRow(`
		SELECT voice_actor_id FROM seasons WHERE series_id = ? AND season_number = 3
	`, seriesID).Scan(&savedVoiceID)
	if err != nil {
		t.Fatalf("failed to query voice_actor_id: %v", err)
	}
	if savedVoiceID == nil || *savedVoiceID != voiceID {
		t.Errorf("expected voice_actor_id=%d to be preserved, got %v", voiceID, savedVoiceID)
	}
}
