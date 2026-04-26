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

// seedTestSeason inserts a season with explicit downloaded.
func seedTestSeason(t *testing.T, db *database.DB, seriesID int64, seasonNum int, folderPath string, downloaded bool) int64 {
	t.Helper()
	result, err := db.Exec(`
		INSERT INTO seasons (series_id, season_number, folder_path, downloaded, discovered_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, seriesID, seasonNum, folderPath, downloaded)
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
	sc := New(db, nil, t.TempDir(), nil)

	seriesID := seedTestSeries(t, db, "Test Show")

	// Create a real folder with a video file
	seasonDir := filepath.Join(t.TempDir(), "Season 1")
	createVideoFile(t, seasonDir, "episode.mkv")

	_ = seedTestSeason(t, db, seriesID, 1, seasonDir, true)
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
	if !season.Downloaded {
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
	sc := New(db, nil, t.TempDir(), nil)

	seriesID := seedTestSeries(t, db, "Test Show")

	// Use a folder path that doesn't exist
	missingDir := filepath.Join(t.TempDir(), "nonexistent", "Season 1")
	_ = seedTestSeason(t, db, seriesID, 1, missingDir, true)
	seedTestMediaFile(t, db, seriesID, 1, filepath.Join(missingDir, "episode.mkv"))

	// Run cleanup
	if err := sc.cleanupRemovedSeasons(); err != nil {
		t.Fatalf("cleanupRemovedSeasons failed: %v", err)
	}

	// Season should still be marked as downloaded, but folder_path cleared
	season, err := db.GetSeasonBySeriesAndNumber(seriesID, 1)
	if err != nil {
		t.Fatalf("failed to get season: %v", err)
	}
	if !season.Downloaded {
		t.Error("expected downloaded to remain true")
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

func TestCleanupRemovedSeasons_FolderEmpty_ClearOwned(t *testing.T) {
	db := setupTestDB(t)
	sc := New(db, nil, t.TempDir(), nil)

	seriesID := seedTestSeries(t, db, "Test Show")

	// Create an empty folder (no video files)
	emptyDir := filepath.Join(t.TempDir(), "Season 1")
	if err := os.MkdirAll(emptyDir, 0o755); err != nil {
		t.Fatalf("failed to create empty dir: %v", err)
	}

	_ = seedTestSeason(t, db, seriesID, 1, emptyDir, true)
	seedTestMediaFile(t, db, seriesID, 1, filepath.Join(emptyDir, "episode.mkv"))

	// Run cleanup
	if err := sc.cleanupRemovedSeasons(); err != nil {
		t.Fatalf("cleanupRemovedSeasons failed: %v", err)
	}

	// Season should still be downloaded, but folder_path cleared
	season, err := db.GetSeasonBySeriesAndNumber(seriesID, 1)
	if err != nil {
		t.Fatalf("failed to get season: %v", err)
	}
	if !season.Downloaded {
		t.Error("expected downloaded to remain true")
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
	sc := New(db, nil, t.TempDir(), nil)

	seriesID := seedTestSeries(t, db, "Another Show")

	// A non-owned season (from TVDB sync) should not be affected
	seedTestSeason(t, db, seriesID, 2, "", false)

	if err := sc.cleanupRemovedSeasons(); err != nil {
		t.Fatalf("cleanupRemovedSeasons failed: %v", err)
	}

	season, err := db.GetSeasonBySeriesAndNumber(seriesID, 2)
	if err != nil {
		t.Fatalf("failed to get season: %v", err)
	}
	if season.Downloaded {
		t.Error("non-owned season should remain not owned")
	}
}

func TestCleanupRemovedSeasons_PreservesTrackName(t *testing.T) {
	db := setupTestDB(t)
	sc := New(db, nil, t.TempDir(), nil)

	seriesID := seedTestSeries(t, db, "Voice Show")

	// Create owned season with track_name, folder gone
	missingDir := filepath.Join(t.TempDir(), "nonexistent")
	_, err := db.Exec(`
		INSERT INTO seasons (series_id, season_number, folder_path, track_name, downloaded, discovered_at, created_at, updated_at)
		VALUES (?, 3, ?, 'LostFilm', 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, seriesID, missingDir)
	if err != nil {
		t.Fatalf("failed to seed season: %v", err)
	}

	if err := sc.cleanupRemovedSeasons(); err != nil {
		t.Fatalf("cleanupRemovedSeasons failed: %v", err)
	}

	var savedTrackName *string
	err = db.QueryRow(`
		SELECT track_name FROM seasons WHERE series_id = ? AND season_number = 3
	`, seriesID).Scan(&savedTrackName)
	if err != nil {
		t.Fatalf("failed to query track_name: %v", err)
	}
	if savedTrackName == nil || *savedTrackName != "LostFilm" {
		t.Errorf("expected track_name=LostFilm to be preserved, got %v", savedTrackName)
	}
}

// TestProcessSeriesInfo_LockedSeasonSkipped verifies that the scanner does not
// upsert a season whose procLock is held by another op (e.g. an in-flight
// delete handler). Without this, the scanner would resurrect a row that's
// about to be deleted.
func TestProcessSeriesInfo_LockedSeasonSkipped(t *testing.T) {
	db := setupTestDB(t)
	procLock := database.NewProcessingLock()
	sc := New(db, nil, t.TempDir(), procLock)

	seriesID := seedTestSeries(t, db, "Locked Show")

	seasonDir := filepath.Join(t.TempDir(), "Locked Show", "Season 1")
	createVideoFile(t, seasonDir, "ep01.mkv")

	// Hold the lock externally.
	if !procLock.TryLock(seriesID, 1) {
		t.Fatal("failed to acquire test lock")
	}
	defer procLock.Unlock(seriesID, 1)

	info := SeriesInfo{Title: "Locked Show", Path: seasonDir, Season: 1}
	if err := sc.processSeriesInfo(info); err != nil {
		t.Fatalf("processSeriesInfo error: %v", err)
	}

	// No season row should have been upserted.
	season, err := db.GetSeasonBySeriesAndNumber(seriesID, 1)
	if err != nil {
		t.Fatalf("get season: %v", err)
	}
	if season != nil {
		t.Errorf("expected no season row when locked, got %+v", season)
	}
}

// TestProcessSeriesInfo_FolderDeletedBeforeUpsert_NotResurrected verifies that
// the scanner does not upsert a season whose folder has been removed between
// the filesystem walk and the upsert (e.g. delete completed in the meantime).
func TestProcessSeriesInfo_FolderDeletedBeforeUpsert_NotResurrected(t *testing.T) {
	db := setupTestDB(t)
	procLock := database.NewProcessingLock()
	sc := New(db, nil, t.TempDir(), procLock)

	seriesID := seedTestSeries(t, db, "Deleted Show")

	// Path that does not exist on disk (delete already removed it).
	missingDir := filepath.Join(t.TempDir(), "Deleted Show", "Season 1")

	info := SeriesInfo{Title: "Deleted Show", Path: missingDir, Season: 1}
	if err := sc.processSeriesInfo(info); err != nil {
		t.Fatalf("processSeriesInfo error: %v", err)
	}

	season, err := db.GetSeasonBySeriesAndNumber(seriesID, 1)
	if err != nil {
		t.Fatalf("get season: %v", err)
	}
	if season != nil {
		t.Errorf("expected no resurrected season row when folder gone, got %+v", season)
	}
}

// TestProcessSeriesInfo_FolderGoneAtStart_NoSeriesCreated verifies that when
// the folder is already gone before processSeriesInfo runs (delete handler
// finished while the entry sat in the scanner's in-memory queue), no series
// row is created. Without the early stat, the fallback path would INSERT a
// series row before the post-lock stat returned, leaving an empty resurrected
// metadata-only series.
func TestProcessSeriesInfo_FolderGoneAtStart_NoSeriesCreated(t *testing.T) {
	db := setupTestDB(t)
	procLock := database.NewProcessingLock()
	sc := New(db, nil, t.TempDir(), procLock)

	missingDir := filepath.Join(t.TempDir(), "Ghost Show", "Season 1")

	info := SeriesInfo{Title: "Ghost Show", Path: missingDir, Season: 1}
	if err := sc.processSeriesInfo(info); err != nil {
		t.Fatalf("processSeriesInfo error: %v", err)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM series WHERE title = ?`, "Ghost Show").Scan(&count); err != nil {
		t.Fatalf("count series: %v", err)
	}
	if count != 0 {
		t.Errorf("expected no series row created when folder gone at start, got %d", count)
	}
}

// TestProcessSeriesInfo_ClearsTombstoneOnRediscovery verifies that the scanner
// clears the deleted_seasons tombstone when files for a previously-deleted
// season reappear on disk, so subsequent TVDB syncs are allowed to populate
// metadata again.
func TestProcessSeriesInfo_ClearsTombstoneOnRediscovery(t *testing.T) {
	db := setupTestDB(t)
	procLock := database.NewProcessingLock()
	sc := New(db, nil, t.TempDir(), procLock)

	seriesID := seedTestSeries(t, db, "Rediscovered Show")

	// Tombstone exists from a prior season delete.
	if err := db.MarkSeasonDeleted(seriesID, 1); err != nil {
		t.Fatalf("mark tombstone: %v", err)
	}

	// User puts files back on disk; scanner picks them up.
	seasonDir := filepath.Join(t.TempDir(), "Rediscovered Show", "Season 1")
	createVideoFile(t, seasonDir, "ep01.mkv")

	info := SeriesInfo{Title: "Rediscovered Show", Path: seasonDir, Season: 1}
	if err := sc.processSeriesInfo(info); err != nil {
		t.Fatalf("processSeriesInfo error: %v", err)
	}

	// Tombstone must be gone.
	tombstoned, err := db.IsSeasonDeleted(seriesID, 1)
	if err != nil {
		t.Fatalf("IsSeasonDeleted: %v", err)
	}
	if tombstoned {
		t.Error("expected tombstone cleared after scanner re-discovery")
	}

	// Season row must exist.
	season, err := db.GetSeasonBySeriesAndNumber(seriesID, 1)
	if err != nil {
		t.Fatalf("get season: %v", err)
	}
	if season == nil {
		t.Error("expected season row after scanner upsert")
	}
}

// TestProcessSeriesInfo_SeriesLocked_NoUpsert verifies that processSeriesInfo
// skips when the series is series-locked (i.e. handleDeleteSeries is in
// progress), even for a season that wasn't part of the original DB snapshot.
func TestProcessSeriesInfo_SeriesLocked_NoUpsert(t *testing.T) {
	db := setupTestDB(t)
	procLock := database.NewProcessingLock()
	sc := New(db, nil, t.TempDir(), procLock)

	seriesID := seedTestSeries(t, db, "Series Locked Show")

	seasonDir := filepath.Join(t.TempDir(), "Series Locked Show", "Season 2")
	createVideoFile(t, seasonDir, "ep01.mkv")

	if !procLock.TryLockSeries(seriesID) {
		t.Fatal("failed to acquire series lock")
	}
	defer procLock.UnlockSeries(seriesID)

	info := SeriesInfo{Title: "Series Locked Show", Path: seasonDir, Season: 2}
	if err := sc.processSeriesInfo(info); err != nil {
		t.Fatalf("processSeriesInfo error: %v", err)
	}

	season, err := db.GetSeasonBySeriesAndNumber(seriesID, 2)
	if err != nil {
		t.Fatalf("get season: %v", err)
	}
	if season != nil {
		t.Errorf("expected no upsert when series-locked, got %+v", season)
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
		{
			name:       "Russian Сезон N pattern",
			folder:     "Друзья и соседи (Your Friends and Neighbors) Сезон 2",
			wantSeason: 2,
			wantTitle:  "Друзья и соседи",
		},
		{
			name:       "Russian N сезон pattern",
			folder:     "Некий сериал 3 сезон",
			wantSeason: 3,
			wantTitle:  "Некий сериал",
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
		name  string
		input string
		want  int
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
