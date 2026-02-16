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

func TestParseSeriesFolder(t *testing.T) {
	sc := &Scanner{}

	tests := []struct {
		name       string
		folder     string
		wantSeason int
		wantTitle  string
		wantNil    bool
	}{
		{
			name:       "NxRus suffix misparsed as season",
			folder:     "Ginny.and.Georgia.S03.1080p.NF.WEB-DL.2xRus.Ukr.Eng.Subs-alekartem",
			wantSeason: 3,
			wantTitle:  "Ginny and Georgia",
		},
		{
			name:       "3xRus suffix",
			folder:     "Show.S02.1080p.3xRus.Eng",
			wantSeason: 2,
			wantTitle:  "Show",
		},
		{
			name:       "2xUkr suffix",
			folder:     "Show.S05.720p.2xUkr",
			wantSeason: 5,
			wantTitle:  "Show",
		},
		{
			name:       "normal case no NxLang",
			folder:     "Breaking.Bad.S01.1080p.BluRay",
			wantSeason: 1,
			wantTitle:  "Breaking Bad",
		},
		{
			name:       "Season word pattern with spaces",
			folder:     "Some Show Season 2 1080p",
			wantSeason: 2,
			wantTitle:  "Some Show",
		},
		{
			name:    "no season at all",
			folder:  "Some.Show.1080p",
			wantNil: true,
		},
		{
			name:       "NxLang only no S## marker",
			folder:     "Show.2xRus.720p",
			wantSeason: 2,
			wantTitle:  "Show",
		},
		{
			name:       "DS9 with NxLang does not false-positive on S9",
			folder:     "DS9.S03.1080p.2xRus",
			wantSeason: 3,
			wantTitle:  "DS9",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sc.parseSeriesFolder(tt.folder, "/fake/path")

			if tt.wantNil {
				if result != nil {
					t.Errorf("expected nil, got season=%d title=%q", result.Season, result.Title)
				}
				return
			}

			if result == nil {
				t.Fatal("expected non-nil result, got nil")
			}
			if result.Season != tt.wantSeason {
				t.Errorf("season: got %d, want %d", result.Season, tt.wantSeason)
			}
			if result.Title != tt.wantTitle {
				t.Errorf("title: got %q, want %q", result.Title, tt.wantTitle)
			}
		})
	}
}

func TestExtractSeasonNumber(t *testing.T) {
	tests := []struct {
		name string
		input string
		want int
	}{
		{"S03 pattern", "Ginny.and.Georgia.S03.1080p", 3},
		{"S01 pattern", "Show.S01.720p", 1},
		{"Season word", "Show Season 5", 5},
		{"no season", "Just.A.Show.1080p", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractSeasonNumber(tt.input)
			if got != tt.want {
				t.Errorf("extractSeasonNumber(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestScanSeasonFolders(t *testing.T) {
	tests := []struct {
		name        string
		seriesName  string
		subfolders  []string // season subfolders to create
		wantTitle   string
		wantSeasons []int
	}{
		{
			name:        "NxLang series with season subfolders",
			seriesName:  "Ginny.and.Georgia.S03.1080p.NF.WEB-DL.2xRus",
			subfolders:  []string{"Season 1", "Season 2"},
			wantTitle:   "Ginny and Georgia",
			wantSeasons: []int{1, 2},
		},
		{
			name:        "normal series with season subfolders",
			seriesName:  "Breaking.Bad.1080p",
			subfolders:  []string{"Season 1"},
			wantTitle:   "Breaking Bad",
			wantSeasons: []int{1},
		},
		{
			name:        "title with embedded S+digits not false-positive",
			seriesName:  "PS5.Review.S01.720p",
			subfolders:  []string{"Season 1"},
			wantTitle:   "PS5 Review",
			wantSeasons: []int{1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sc := &Scanner{}
			seriesDir := filepath.Join(t.TempDir(), tt.seriesName)
			if err := os.MkdirAll(seriesDir, 0o755); err != nil {
				t.Fatalf("failed to create series dir: %v", err)
			}

			for _, sub := range tt.subfolders {
				subDir := filepath.Join(seriesDir, sub)
				createVideoFile(t, subDir, "episode.mkv")
			}

			results := sc.scanSeasonFolders(tt.seriesName, seriesDir)
			if len(results) != len(tt.wantSeasons) {
				t.Fatalf("result count: got %d, want %d", len(results), len(tt.wantSeasons))
			}

			for i, r := range results {
				if r.Title != tt.wantTitle {
					t.Errorf("result[%d] title: got %q, want %q", i, r.Title, tt.wantTitle)
				}
				if r.Season != tt.wantSeasons[i] {
					t.Errorf("result[%d] season: got %d, want %d", i, r.Season, tt.wantSeasons[i])
				}
			}
		})
	}
}

func TestCleanSeriesTitle(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"remove S## at end", "Ginny and Georgia S03", "Ginny and Georgia"},
		{"remove quality tags", "Breaking Bad 1080p BluRay", "Breaking Bad"},
		{"dots to spaces", "Some.Show.S01", "Some Show"},
		{"already clean", "Breaking Bad", "Breaking Bad"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cleanSeriesTitle(tt.input)
			if got != tt.want {
				t.Errorf("cleanSeriesTitle(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
