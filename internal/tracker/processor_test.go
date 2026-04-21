package tracker

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/episodex/episodex/internal/database"
	"github.com/episodex/episodex/internal/qbittorrent"
)

// mockAudioProcessor implements AudioProcessor for testing.
type mockAudioProcessor struct {
	removeErr   error
	removeCalls []removeCall
}

type removeCall struct {
	filePath     string
	trackName    string
	keepOriginal bool
}

func (m *mockAudioProcessor) RemoveAudioTracks(filePath string, trackName string, keepOriginal bool) error {
	m.removeCalls = append(m.removeCalls, removeCall{filePath, trackName, keepOriginal})
	return m.removeErr
}

func createTempMKVFiles(t *testing.T, dir string, names ...string) {
	t.Helper()
	for _, name := range names {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("fake mkv"), 0o644); err != nil {
			t.Fatalf("create temp file %s: %v", name, err)
		}
	}
}

func TestProcessCompleted_ProcessesNewFiles(t *testing.T) {
	db := setupTestDB(t)
	dir := t.TempDir()

	seriesID := insertTestSeries(t, db, "Test Show")
	insertTestSeasonWithTrackName(t, db, seriesID, 1,
		strPtr("https://kinozal.tv/details.php?id=123"),
		strPtr("hash1"),
		strPtr(dir),
		strPtr("TestTrack"))

	// Create MKV files on disk
	createTempMKVFiles(t, dir, "Test.Show.S01E01.mkv", "Test.Show.S01E02.mkv", "Test.Show.S01E03.mkv")

	// E01 already processed, E02 and E03 are new
	insertTestProcessedFile(t, db, seriesID, 1, filepath.Join(dir, "Test.Show.S01E01.mkv"))

	audio := &mockAudioProcessor{}
	qbit := &mockQbitClient{
		torrents: []qbittorrent.Torrent{
			{Hash: "hash1", State: "stalledUP"},
		},
	}

	processor := NewPostDownloadProcessor(db, qbit, audio, database.NewProcessingLock())
	results := processor.ProcessCompleted()

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if r.Processed != 2 {
		t.Errorf("expected 2 processed, got %d", r.Processed)
	}
	if r.Failed != 0 {
		t.Errorf("expected 0 failed, got %d", r.Failed)
	}
	if r.Error != nil {
		t.Errorf("unexpected error: %v", r.Error)
	}

	// Verify audio was called for E02 and E03 with correct track
	if len(audio.removeCalls) != 2 {
		t.Fatalf("expected 2 audio calls, got %d", len(audio.removeCalls))
	}
	for _, call := range audio.removeCalls {
		if call.trackName != "TestTrack" {
			t.Errorf("expected trackName=TestTrack, got %s", call.trackName)
		}
		if call.keepOriginal {
			t.Error("expected keepOriginal=false")
		}
	}

	// Verify new files are now marked as processed in DB
	processed, err := db.GetProcessedFilePathsBySeason(seriesID, 1)
	if err != nil {
		t.Fatalf("get processed: %v", err)
	}
	if len(processed) != 3 {
		t.Errorf("expected 3 processed files in DB, got %d", len(processed))
	}
}

func TestProcessCompleted_SkipsIncompleteDownload(t *testing.T) {
	db := setupTestDB(t)
	dir := t.TempDir()

	seriesID := insertTestSeries(t, db, "Test Show")
	insertTestSeasonWithTrackName(t, db, seriesID, 1,
		strPtr("https://kinozal.tv/details.php?id=123"),
		strPtr("hash1"),
		strPtr(dir),
		strPtr("TestTrack"))

	createTempMKVFiles(t, dir, "Test.Show.S01E01.mkv")
	insertTestProcessedFile(t, db, seriesID, 1, filepath.Join(dir, "Test.Show.S01E01.mkv"))

	audio := &mockAudioProcessor{}
	qbit := &mockQbitClient{
		torrents: []qbittorrent.Torrent{
			{Hash: "hash1", State: "downloading"}, // not completed
		},
	}

	processor := NewPostDownloadProcessor(db, qbit, audio, database.NewProcessingLock())
	results := processor.ProcessCompleted()

	// Should return no results because torrent is still downloading
	if len(results) != 0 {
		t.Errorf("expected 0 results for incomplete download, got %d", len(results))
	}
	if len(audio.removeCalls) != 0 {
		t.Error("should not process audio for incomplete download")
	}
}

func TestProcessCompleted_SkipsNoTrackName(t *testing.T) {
	db := setupTestDB(t)
	dir := t.TempDir()

	seriesID := insertTestSeries(t, db, "Test Show")
	insertTestSeason(t, db, seriesID, 1,
		strPtr("https://kinozal.tv/details.php?id=123"),
		strPtr("hash1"),
		strPtr(dir))

	createTempMKVFiles(t, dir, "Test.Show.S01E01.mkv")

	audio := &mockAudioProcessor{}
	qbit := &mockQbitClient{
		torrents: []qbittorrent.Torrent{
			{Hash: "hash1", State: "stalledUP"},
		},
	}

	processor := NewPostDownloadProcessor(db, qbit, audio, database.NewProcessingLock())
	results := processor.ProcessCompleted()

	// No track name → season skipped entirely (nil result)
	if len(results) != 0 {
		t.Errorf("expected 0 results when no track name, got %d", len(results))
	}
}

func TestProcessCompleted_ProcessesWithTrackNameNoPriorFiles(t *testing.T) {
	db := setupTestDB(t)
	dir := t.TempDir()

	seriesID := insertTestSeries(t, db, "Test Show")
	insertTestSeasonWithTrackName(t, db, seriesID, 1,
		strPtr("https://kinozal.tv/details.php?id=123"),
		strPtr("hash1"),
		strPtr(dir),
		strPtr("TestTrack"))

	createTempMKVFiles(t, dir, "Test.Show.S01E01.mkv")

	audio := &mockAudioProcessor{}
	qbit := &mockQbitClient{
		torrents: []qbittorrent.Torrent{
			{Hash: "hash1", State: "stalledUP"},
		},
	}

	processor := NewPostDownloadProcessor(db, qbit, audio, database.NewProcessingLock())
	results := processor.ProcessCompleted()

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Processed != 1 {
		t.Errorf("expected 1 processed, got %d", results[0].Processed)
	}
	if len(audio.removeCalls) != 1 {
		t.Fatalf("expected 1 audio call, got %d", len(audio.removeCalls))
	}
	if audio.removeCalls[0].trackName != "TestTrack" {
		t.Errorf("expected trackName=TestTrack, got %s", audio.removeCalls[0].trackName)
	}
}

func TestProcessCompleted_AllFilesAlreadyProcessed(t *testing.T) {
	db := setupTestDB(t)
	dir := t.TempDir()

	seriesID := insertTestSeries(t, db, "Test Show")
	insertTestSeasonWithTrackName(t, db, seriesID, 1,
		strPtr("https://kinozal.tv/details.php?id=123"),
		strPtr("hash1"),
		strPtr(dir),
		strPtr("TestTrack"))

	createTempMKVFiles(t, dir, "Test.Show.S01E01.mkv", "Test.Show.S01E02.mkv")
	insertTestProcessedFile(t, db, seriesID, 1, filepath.Join(dir, "Test.Show.S01E01.mkv"))
	insertTestProcessedFile(t, db, seriesID, 1, filepath.Join(dir, "Test.Show.S01E02.mkv"))

	audio := &mockAudioProcessor{}
	qbit := &mockQbitClient{
		torrents: []qbittorrent.Torrent{
			{Hash: "hash1", State: "uploading"},
		},
	}

	processor := NewPostDownloadProcessor(db, qbit, audio, database.NewProcessingLock())
	results := processor.ProcessCompleted()

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Processed != 0 {
		t.Errorf("expected 0 processed, got %d", results[0].Processed)
	}
	if len(audio.removeCalls) != 0 {
		t.Error("should not call audio when all files already processed")
	}
}

func TestProcessCompleted_AudioProcessingError(t *testing.T) {
	db := setupTestDB(t)
	dir := t.TempDir()

	seriesID := insertTestSeries(t, db, "Test Show")
	insertTestSeasonWithTrackName(t, db, seriesID, 1,
		strPtr("https://kinozal.tv/details.php?id=123"),
		strPtr("hash1"),
		strPtr(dir),
		strPtr("TestTrack"))

	createTempMKVFiles(t, dir, "Test.Show.S01E01.mkv", "Test.Show.S01E02.mkv")
	// E01 already processed (provides track reference), E02 is new
	insertTestProcessedFile(t, db, seriesID, 1, filepath.Join(dir, "Test.Show.S01E01.mkv"))

	audio := &mockAudioProcessor{removeErr: fmt.Errorf("mkvmerge failed")}
	qbit := &mockQbitClient{
		torrents: []qbittorrent.Torrent{
			{Hash: "hash1", State: "stalledUP"},
		},
	}

	processor := NewPostDownloadProcessor(db, qbit, audio, database.NewProcessingLock())
	results := processor.ProcessCompleted()

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if r.Failed != 1 {
		t.Errorf("expected 1 failed, got %d", r.Failed)
	}
	if r.Processed != 0 {
		t.Errorf("expected 0 processed, got %d", r.Processed)
	}
}

func TestProcessCompleted_MultipleCompletedStates(t *testing.T) {
	states := []string{"uploading", "stalledUP", "pausedUP", "forcedUP", "queuedUP", "checkingUP"}

	for _, state := range states {
		t.Run(state, func(t *testing.T) {
			db := setupTestDB(t)
			dir := t.TempDir()

			seriesID := insertTestSeries(t, db, "Test Show")
			insertTestSeasonWithTrackName(t, db, seriesID, 1,
				strPtr("https://kinozal.tv/details.php?id=123"),
				strPtr("hash1"),
				strPtr(dir),
				strPtr("TestTrack"))

			createTempMKVFiles(t, dir, "Test.Show.S01E01.mkv", "Test.Show.S01E02.mkv")
			insertTestProcessedFile(t, db, seriesID, 1, filepath.Join(dir, "Test.Show.S01E01.mkv"))

			audio := &mockAudioProcessor{}
			qbit := &mockQbitClient{
				torrents: []qbittorrent.Torrent{
					{Hash: "hash1", State: state},
				},
			}

			processor := NewPostDownloadProcessor(db, qbit, audio, database.NewProcessingLock())
			results := processor.ProcessCompleted()

			if len(results) != 1 {
				t.Fatalf("expected 1 result for state %s, got %d", state, len(results))
			}
			if results[0].Processed != 1 {
				t.Errorf("expected 1 processed for state %s, got %d", state, results[0].Processed)
			}
		})
	}
}

func TestProcessCompleted_NonCompletedStatesSkipped(t *testing.T) {
	states := []string{"downloading", "stalledDL", "pausedDL", "queuedDL", "checkingDL", "metaDL", "allocating", "moving", "error", "missingFiles"}

	for _, state := range states {
		t.Run(state, func(t *testing.T) {
			db := setupTestDB(t)
			dir := t.TempDir()

			seriesID := insertTestSeries(t, db, "Test Show")
			insertTestSeasonWithTrackName(t, db, seriesID, 1,
				strPtr("https://kinozal.tv/details.php?id=123"),
				strPtr("hash1"),
				strPtr(dir),
				strPtr("TestTrack"))

			createTempMKVFiles(t, dir, "Test.Show.S01E01.mkv")
			insertTestProcessedFile(t, db, seriesID, 1, filepath.Join(dir, "Test.Show.S01E01.mkv"))

			audio := &mockAudioProcessor{}
			qbit := &mockQbitClient{
				torrents: []qbittorrent.Torrent{
					{Hash: "hash1", State: state},
				},
			}

			processor := NewPostDownloadProcessor(db, qbit, audio, database.NewProcessingLock())
			results := processor.ProcessCompleted()

			if len(results) != 0 {
				t.Errorf("expected 0 results for state %s, got %d", state, len(results))
			}
		})
	}
}

func TestProcessCompleted_TorrentNotInQbit(t *testing.T) {
	db := setupTestDB(t)
	dir := t.TempDir()

	seriesID := insertTestSeries(t, db, "Test Show")
	insertTestSeasonWithTrackName(t, db, seriesID, 1,
		strPtr("https://kinozal.tv/details.php?id=123"),
		strPtr("hash1"),
		strPtr(dir),
		strPtr("TestTrack"))

	createTempMKVFiles(t, dir, "Test.Show.S01E01.mkv")

	audio := &mockAudioProcessor{}
	qbit := &mockQbitClient{
		torrents: []qbittorrent.Torrent{}, // empty — torrent not found
	}

	processor := NewPostDownloadProcessor(db, qbit, audio, database.NewProcessingLock())
	results := processor.ProcessCompleted()

	if len(results) != 0 {
		t.Errorf("expected 0 results when torrent not in qBit, got %d", len(results))
	}
}

// insertTestSeasonWithTrackName is a test helper that creates a season with track_name.
func insertTestSeasonWithTrackName(t *testing.T, db *database.DB, seriesID int64, seasonNum int, trackerURL, torrentHash, folderPath, trackName *string) int64 { //nolint:unparam // test helper, seasonNum varies by test scenario
	t.Helper()
	result, err := db.Exec(`
		INSERT INTO seasons (series_id, season_number, tracker_url, torrent_hash, folder_path, track_name, auto_process)
		VALUES (?, ?, ?, ?, ?, ?, 1)
	`, seriesID, seasonNum, trackerURL, torrentHash, folderPath, trackName)
	if err != nil {
		t.Fatalf("insert season: %v", err)
	}
	id, _ := result.LastInsertId()
	return id
}
