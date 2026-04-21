package tracker

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/episodex/episodex/internal/database"
	"github.com/episodex/episodex/internal/qbittorrent"
)

// mockCheckerClient implements Client for testing.
type mockCheckerClient struct {
	canHandle    bool
	episodeCount int
	episodeErr   error
	torrentData  []byte
	downloadErr  error
}

func (m *mockCheckerClient) CanHandle(_ string) bool { return m.canHandle }
func (m *mockCheckerClient) GetEpisodeCount(_ string) (int, error) {
	return m.episodeCount, m.episodeErr
}
func (m *mockCheckerClient) DownloadTorrent(_ string) ([]byte, error) {
	return m.torrentData, m.downloadErr
}

// mockQbitClient implements QbitClient for testing.
type mockQbitClient struct {
	torrents       []qbittorrent.Torrent
	listErr        error
	deleteErr      error
	addHash        string
	addErr         error
	files          []qbittorrent.TorrentFile
	filesErr       error
	setPriorityErr error

	deletedHashes []string
	addedTorrents []addTorrentCall
	priorityCalls []priorityCall
}

type addTorrentCall struct {
	data     []byte
	category string
	savePath string
}

type priorityCall struct {
	hash     string
	indexes  []int
	priority int
}

func (m *mockQbitClient) ListTorrents() ([]qbittorrent.Torrent, error) {
	return m.torrents, m.listErr
}

func (m *mockQbitClient) DeleteTorrent(hash string) error {
	m.deletedHashes = append(m.deletedHashes, hash)
	return m.deleteErr
}

func (m *mockQbitClient) AddTorrent(data []byte, category, savePath string) (string, error) {
	m.addedTorrents = append(m.addedTorrents, addTorrentCall{data, category, savePath})
	return m.addHash, m.addErr
}

func (m *mockQbitClient) GetTorrentFiles(_ string) ([]qbittorrent.TorrentFile, error) {
	return m.files, m.filesErr
}

func (m *mockQbitClient) SetFilePriority(hash string, indexes []int, priority int) error {
	m.priorityCalls = append(m.priorityCalls, priorityCall{hash, indexes, priority})
	return m.setPriorityErr
}

// fakeTorrent builds minimal valid bencode torrent data with a unique info dictionary.
// The name parameter makes each torrent produce a different info_hash.
func fakeTorrent(name string) []byte {
	// d8:announce0:4:infod4:name<len>:<name>12:piece lengthi1e6:pieces20:xxxxxxxxxxxxxxxxxxxxxee
	info := fmt.Sprintf("d4:name%d:%s12:piece lengthi1e6:pieces20:xxxxxxxxxxxxxxxxxxxxe", len(name), name)
	return []byte(fmt.Sprintf("d8:announce0:4:info%se", info))
}

func setupTestDB(t *testing.T) *database.DB {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := database.New(dbPath)
	if err != nil {
		t.Fatalf("failed to create test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func insertTestSeries(t *testing.T, db *database.DB, title string) int64 {
	t.Helper()
	result, err := db.Exec(`INSERT INTO series (title) VALUES (?)`, title)
	if err != nil {
		t.Fatalf("insert series: %v", err)
	}
	id, _ := result.LastInsertId()
	return id
}

func insertTestSeason(t *testing.T, db *database.DB, seriesID int64, seasonNum int, trackerURL, torrentHash, folderPath *string) int64 {
	t.Helper()
	result, err := db.Exec(`
		INSERT INTO seasons (series_id, season_number, tracker_url, torrent_hash, folder_path)
		VALUES (?, ?, ?, ?, ?)
	`, seriesID, seasonNum, trackerURL, torrentHash, folderPath)
	if err != nil {
		t.Fatalf("insert season: %v", err)
	}
	id, _ := result.LastInsertId()
	return id
}

func insertTestMediaFile(t *testing.T, db *database.DB, seriesID int64, seasonNum int, fileName string) {
	t.Helper()
	filePath := "/media/" + fileName
	_, err := db.Exec(`
		INSERT INTO media_files (series_id, season_number, file_path, file_name, file_size, file_hash, mod_time)
		VALUES (?, ?, ?, ?, 100, 'abc', 0)
	`, seriesID, seasonNum, filePath, fileName)
	if err != nil {
		t.Fatalf("insert media file: %v", err)
	}
}

func insertTestProcessedFile(t *testing.T, db *database.DB, seriesID int64, seasonNum int, filePath string) { //nolint:unparam // test helper, seasonNum varies by test scenario
	t.Helper()
	_, err := db.Exec(`
		INSERT INTO processed_files (file_path, series_id, season_number, track_kept)
		VALUES (?, ?, ?, 'TestTrack')
	`, filePath, seriesID, seasonNum)
	if err != nil {
		t.Fatalf("insert processed file: %v", err)
	}
}

func strPtr(s string) *string { return &s }

func TestChecker_NoSeasonsWithTrackerURL(t *testing.T) {
	db := setupTestDB(t)
	registry := NewRegistry()
	qbit := &mockQbitClient{}
	checker := NewChecker(db, registry, qbit)

	results := checker.Check()
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestChecker_NoNewEpisodes(t *testing.T) {
	db := setupTestDB(t)
	seriesID := insertTestSeries(t, db, "Test Show")
	insertTestSeason(t, db, seriesID, 1, strPtr("https://kinozal.tv/details.php?id=123"), strPtr("abc123"), strPtr("/media/TestShow/S01"))
	// 5 episodes on disk
	for i := 1; i <= 5; i++ {
		insertTestMediaFile(t, db, seriesID, 1, fmt.Sprintf("Test.Show.S01E%02d.mkv", i))
	}

	mock := &mockCheckerClient{canHandle: true, episodeCount: 5}
	registry := NewRegistry()
	registry.Register(mock)

	qbit := &mockQbitClient{}
	checker := NewChecker(db, registry, qbit)

	results := checker.Check()
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Redownloaded {
		t.Error("should not redownload when tracker eps <= disk eps")
	}
	if results[0].TrackerEps != 5 {
		t.Errorf("expected TrackerEps=5, got %d", results[0].TrackerEps)
	}
	if results[0].DiskEps != 5 {
		t.Errorf("expected DiskEps=5, got %d", results[0].DiskEps)
	}
}

func TestChecker_NewEpisodesTriggersRedownload(t *testing.T) {
	db := setupTestDB(t)
	seriesID := insertTestSeries(t, db, "Test Show")
	seasonID := insertTestSeason(t, db, seriesID, 1,
		strPtr("https://kinozal.tv/details.php?id=123"),
		strPtr("oldhash"),
		strPtr("/media/TestShow/S01"))
	// 5 episodes on disk
	for i := 1; i <= 5; i++ {
		insertTestMediaFile(t, db, seriesID, 1, fmt.Sprintf("Test.Show.S01E%02d.mkv", i))
	}

	mock := &mockCheckerClient{
		canHandle:    true,
		episodeCount: 8, // 8 on tracker vs 5 on disk
		torrentData:  fakeTorrent("new"),
	}
	registry := NewRegistry()
	registry.Register(mock)

	qbit := &mockQbitClient{
		torrents: []qbittorrent.Torrent{
			{Hash: "oldhash", Category: "tv-shows", SavePath: "/downloads/shows"},
		},
		addHash: "newhash",
		files: []qbittorrent.TorrentFile{
			{Index: 0, Name: "Test.Show.S01E01.mkv"},
			{Index: 1, Name: "Test.Show.S01E02.mkv"},
			{Index: 2, Name: "Test.Show.S01E06.mkv"},
		},
	}
	checker := NewChecker(db, registry, qbit)

	results := checker.Check()
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if !r.Redownloaded {
		t.Fatal("expected redownload")
	}
	if r.Error != nil {
		t.Fatalf("unexpected error: %v", r.Error)
	}

	// Check old torrent was deleted
	if len(qbit.deletedHashes) != 1 || qbit.deletedHashes[0] != "oldhash" {
		t.Errorf("expected old hash deleted, got %v", qbit.deletedHashes)
	}

	// Check new torrent was added with correct category and save path
	if len(qbit.addedTorrents) != 1 {
		t.Fatalf("expected 1 add call, got %d", len(qbit.addedTorrents))
	}
	if qbit.addedTorrents[0].category != "tv-shows" {
		t.Errorf("expected category=tv-shows, got %s", qbit.addedTorrents[0].category)
	}
	if qbit.addedTorrents[0].savePath != "/downloads/shows" {
		t.Errorf("expected savePath=/downloads/shows, got %s", qbit.addedTorrents[0].savePath)
	}

	// Check torrent hash updated in DB
	season, err := db.GetSeasonBySeriesAndNumber(seriesID, 1)
	if err != nil {
		t.Fatalf("get season: %v", err)
	}
	expectedHash, _ := qbittorrent.ComputeInfoHash(fakeTorrent("new"))
	if season.TorrentHash == nil || *season.TorrentHash != expectedHash {
		t.Errorf("expected torrent hash=%s, got %v", expectedHash, season.TorrentHash)
	}
	_ = seasonID
}

func TestChecker_SkipsProcessedFiles(t *testing.T) {
	db := setupTestDB(t)
	seriesID := insertTestSeries(t, db, "Test Show")
	insertTestSeason(t, db, seriesID, 1,
		strPtr("https://kinozal.tv/details.php?id=123"),
		strPtr("oldhash"),
		strPtr("/media/TestShow/S01"))

	// 3 episodes on disk, 2 processed
	for i := 1; i <= 3; i++ {
		insertTestMediaFile(t, db, seriesID, 1, fmt.Sprintf("Test.Show.S01E%02d.mkv", i))
	}
	insertTestProcessedFile(t, db, seriesID, 1, "/media/TestShow/S01/Test.Show.S01E01.mkv")
	insertTestProcessedFile(t, db, seriesID, 1, "/media/TestShow/S01/Test.Show.S01E02.mkv")

	mock := &mockCheckerClient{
		canHandle:    true,
		episodeCount: 5, // more than 3 on disk
		torrentData:  fakeTorrent("new2"),
	}
	registry := NewRegistry()
	registry.Register(mock)

	qbit := &mockQbitClient{
		torrents: []qbittorrent.Torrent{{Hash: "oldhash", Category: "tv"}},
		addHash:  "newhash",
		files: []qbittorrent.TorrentFile{
			{Index: 0, Name: "Test.Show.S01E01.mkv"},
			{Index: 1, Name: "Test.Show.S01E02.mkv"},
			{Index: 2, Name: "Test.Show.S01E03.mkv"},
			{Index: 3, Name: "Test.Show.S01E04.mkv"},
			{Index: 4, Name: "Test.Show.S01E05.mkv"},
		},
	}
	checker := NewChecker(db, registry, qbit)

	results := checker.Check()
	if len(results) != 1 || !results[0].Redownloaded {
		t.Fatal("expected successful redownload")
	}

	// Should have set priority 0 for processed files (E01 and E02)
	if len(qbit.priorityCalls) != 1 {
		t.Fatalf("expected 1 priority call, got %d", len(qbit.priorityCalls))
	}
	call := qbit.priorityCalls[0]
	expectedHash, _ := qbittorrent.ComputeInfoHash(fakeTorrent("new2"))
	if call.hash != expectedHash {
		t.Errorf("expected hash=%s, got %s", expectedHash, call.hash)
	}
	if call.priority != 0 {
		t.Errorf("expected priority=0, got %d", call.priority)
	}
	if len(call.indexes) != 2 || call.indexes[0] != 0 || call.indexes[1] != 1 {
		t.Errorf("expected indexes [0,1], got %v", call.indexes)
	}
}

func TestChecker_ClientError(t *testing.T) {
	db := setupTestDB(t)
	seriesID := insertTestSeries(t, db, "Test Show")
	insertTestSeason(t, db, seriesID, 1, strPtr("https://kinozal.tv/details.php?id=123"), nil, nil)

	mock := &mockCheckerClient{
		canHandle:  true,
		episodeErr: fmt.Errorf("network error"),
	}
	registry := NewRegistry()
	registry.Register(mock)

	qbit := &mockQbitClient{}
	checker := NewChecker(db, registry, qbit)

	results := checker.Check()
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Error == nil {
		t.Error("expected error")
	}
	if results[0].Redownloaded {
		t.Error("should not redownload on error")
	}
}

func TestChecker_NoClient(t *testing.T) {
	db := setupTestDB(t)
	seriesID := insertTestSeries(t, db, "Test Show")
	insertTestSeason(t, db, seriesID, 1, strPtr("https://unknown-tracker.com/123"), nil, nil)

	registry := NewRegistry() // empty registry
	qbit := &mockQbitClient{}
	checker := NewChecker(db, registry, qbit)

	results := checker.Check()
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Error == nil {
		t.Error("expected error for unknown tracker")
	}
}

func TestChecker_ZeroEpisodesOnTracker(t *testing.T) {
	db := setupTestDB(t)
	seriesID := insertTestSeries(t, db, "Test Show")
	insertTestSeason(t, db, seriesID, 1, strPtr("https://kinozal.tv/details.php?id=123"), nil, nil)

	mock := &mockCheckerClient{canHandle: true, episodeCount: 0}
	registry := NewRegistry()
	registry.Register(mock)

	qbit := &mockQbitClient{}
	checker := NewChecker(db, registry, qbit)

	results := checker.Check()
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Redownloaded {
		t.Error("should not redownload when tracker eps is 0")
	}
	if results[0].Error != nil {
		t.Errorf("expected no error, got %v", results[0].Error)
	}
}

func TestChecker_NoTorrentHashInSeason(t *testing.T) {
	db := setupTestDB(t)
	seriesID := insertTestSeries(t, db, "Test Show")
	insertTestSeason(t, db, seriesID, 1,
		strPtr("https://kinozal.tv/details.php?id=123"),
		nil, // no torrent hash
		strPtr("/media/TestShow/S01"))
	insertTestMediaFile(t, db, seriesID, 1, "Test.Show.S01E01.mkv")

	mock := &mockCheckerClient{
		canHandle:    true,
		episodeCount: 3,
		torrentData:  fakeTorrent("new3"),
	}
	registry := NewRegistry()
	registry.Register(mock)

	qbit := &mockQbitClient{
		addHash: "newhash",
		files:   []qbittorrent.TorrentFile{},
	}
	checker := NewChecker(db, registry, qbit)

	results := checker.Check()
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if !results[0].Redownloaded {
		t.Fatal("expected redownload")
	}
	// Should not have tried to delete anything
	if len(qbit.deletedHashes) != 0 {
		t.Errorf("should not delete when no old hash, got %v", qbit.deletedHashes)
	}
	// Should add with empty category/path
	if len(qbit.addedTorrents) != 1 {
		t.Fatalf("expected 1 add, got %d", len(qbit.addedTorrents))
	}
	if qbit.addedTorrents[0].category != "" {
		t.Errorf("expected empty category, got %s", qbit.addedTorrents[0].category)
	}
}

func TestChecker_MultipleSeasons(t *testing.T) {
	db := setupTestDB(t)
	seriesID := insertTestSeries(t, db, "Show A")

	insertTestSeason(t, db, seriesID, 1,
		strPtr("https://kinozal.tv/details.php?id=100"), strPtr("hash1"), strPtr("/media/A/S01"))
	insertTestSeason(t, db, seriesID, 2,
		strPtr("https://kinozal.tv/details.php?id=200"), strPtr("hash2"), strPtr("/media/A/S02"))

	// S01: 5 on disk, S02: 3 on disk
	for i := 1; i <= 5; i++ {
		insertTestMediaFile(t, db, seriesID, 1, fmt.Sprintf("A.S01E%02d.mkv", i))
	}
	for i := 1; i <= 3; i++ {
		insertTestMediaFile(t, db, seriesID, 2, fmt.Sprintf("A.S02E%02d.mkv", i))
	}

	dynamicMock := &dynamicClient{
		canHandle: true,
		episodeCounts: map[string]int{
			"https://kinozal.tv/details.php?id=100": 5, // no new eps
			"https://kinozal.tv/details.php?id=200": 6, // 3 new eps
		},
		torrentData: fakeTorrent("new4"),
	}

	registry := NewRegistry()
	registry.Register(dynamicMock)

	qbit := &mockQbitClient{
		torrents: []qbittorrent.Torrent{
			{Hash: "hash1", Category: "tv"},
			{Hash: "hash2", Category: "tv"},
		},
		addHash: "newhash2",
		files:   []qbittorrent.TorrentFile{},
	}
	checker := NewChecker(db, registry, qbit)

	results := checker.Check()
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// S01 should not be redownloaded
	if results[0].Redownloaded {
		t.Error("S01 should not be redownloaded")
	}
	// S02 should be redownloaded
	if !results[1].Redownloaded {
		t.Error("S02 should be redownloaded")
	}
}

// dynamicClient returns different episode counts per URL.
type dynamicClient struct {
	canHandle     bool
	episodeCounts map[string]int
	torrentData   []byte
}

func (d *dynamicClient) CanHandle(_ string) bool { return d.canHandle }
func (d *dynamicClient) GetEpisodeCount(url string) (int, error) {
	count, ok := d.episodeCounts[url]
	if !ok {
		return 0, fmt.Errorf("unknown URL: %s", url)
	}
	return count, nil
}
func (d *dynamicClient) DownloadTorrent(_ string) ([]byte, error) {
	return d.torrentData, nil
}

func TestChecker_DownloadTorrentError(t *testing.T) {
	db := setupTestDB(t)
	seriesID := insertTestSeries(t, db, "Test Show")
	insertTestSeason(t, db, seriesID, 1,
		strPtr("https://kinozal.tv/details.php?id=123"), strPtr("oldhash"), nil)
	insertTestMediaFile(t, db, seriesID, 1, "Test.Show.S01E01.mkv")

	mock := &mockCheckerClient{
		canHandle:    true,
		episodeCount: 5,
		downloadErr:  fmt.Errorf("download failed"),
	}
	registry := NewRegistry()
	registry.Register(mock)

	qbit := &mockQbitClient{}
	checker := NewChecker(db, registry, qbit)

	results := checker.Check()
	if results[0].Error == nil {
		t.Error("expected error from download failure")
	}
	if results[0].Redownloaded {
		t.Error("should not be redownloaded on error")
	}
}

func TestChecker_AddTorrentError(t *testing.T) {
	db := setupTestDB(t)
	seriesID := insertTestSeries(t, db, "Test Show")
	insertTestSeason(t, db, seriesID, 1,
		strPtr("https://kinozal.tv/details.php?id=123"), nil, nil)
	insertTestMediaFile(t, db, seriesID, 1, "Test.Show.S01E01.mkv")

	mock := &mockCheckerClient{
		canHandle:    true,
		episodeCount: 5,
		torrentData:  fakeTorrent("new5"),
	}
	registry := NewRegistry()
	registry.Register(mock)

	qbit := &mockQbitClient{addErr: fmt.Errorf("qbit error")}
	checker := NewChecker(db, registry, qbit)

	results := checker.Check()
	if results[0].Error == nil {
		t.Error("expected error from add torrent failure")
	}
}

func TestChecker_SkipsSeasonWithNoEpisodesOnDisk(t *testing.T) {
	db := setupTestDB(t)
	seriesID := insertTestSeries(t, db, "Test Show")
	insertTestSeason(t, db, seriesID, 1,
		strPtr("https://kinozal.tv/details.php?id=123"),
		strPtr("oldhash"),
		strPtr("/media/TestShow/S01"))
	// No media files on disk — diskEps = 0

	mock := &mockCheckerClient{
		canHandle:    true,
		episodeCount: 5, // tracker has 5 episodes
		torrentData:  fakeTorrent("new6"),
	}
	registry := NewRegistry()
	registry.Register(mock)

	qbit := &mockQbitClient{}
	checker := NewChecker(db, registry, qbit)

	results := checker.Check()
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Redownloaded {
		t.Error("should not redownload when no episodes on disk (not a mid-season update)")
	}
	if results[0].Error != nil {
		t.Errorf("unexpected error: %v", results[0].Error)
	}
	if len(qbit.addedTorrents) != 0 {
		t.Error("should not add torrent when no episodes on disk")
	}
}

func TestChecker_SkipsProcessedFilesByEpisodeNumber(t *testing.T) {
	db := setupTestDB(t)
	seriesID := insertTestSeries(t, db, "Test Show")
	insertTestSeason(t, db, seriesID, 1,
		strPtr("https://kinozal.tv/details.php?id=123"),
		strPtr("oldhash"),
		strPtr("/media/TestShow/S01"))

	for i := 1; i <= 3; i++ {
		insertTestMediaFile(t, db, seriesID, 1, fmt.Sprintf("Test.Show.S01E%02d.mkv", i))
	}
	// Processed files have DIFFERENT names than torrent files (e.g. different quality tag)
	insertTestProcessedFile(t, db, seriesID, 1, "/media/TestShow/S01/Test.Show.S01E01.720p.mkv")
	insertTestProcessedFile(t, db, seriesID, 1, "/media/TestShow/S01/Test.Show.S01E02.720p.mkv")

	mock := &mockCheckerClient{
		canHandle:    true,
		episodeCount: 5,
		torrentData:  fakeTorrent("new7"),
	}
	registry := NewRegistry()
	registry.Register(mock)

	qbit := &mockQbitClient{
		torrents: []qbittorrent.Torrent{{Hash: "oldhash", Category: "tv"}},
		addHash:  "newhash",
		files: []qbittorrent.TorrentFile{
			{Index: 0, Name: "Test.Show.S01E01.1080p.mkv"},
			{Index: 1, Name: "Test.Show.S01E02.1080p.mkv"},
			{Index: 2, Name: "Test.Show.S01E03.1080p.mkv"},
			{Index: 3, Name: "Test.Show.S01E04.1080p.mkv"},
			{Index: 4, Name: "Test.Show.S01E05.1080p.mkv"},
		},
	}
	checker := NewChecker(db, registry, qbit)

	results := checker.Check()
	if len(results) != 1 || !results[0].Redownloaded {
		t.Fatal("expected successful redownload")
	}

	// Should skip E01 and E02 by episode number even though filenames differ
	if len(qbit.priorityCalls) != 1 {
		t.Fatalf("expected 1 priority call, got %d", len(qbit.priorityCalls))
	}
	call := qbit.priorityCalls[0]
	if len(call.indexes) != 2 || call.indexes[0] != 0 || call.indexes[1] != 1 {
		t.Errorf("expected indexes [0,1], got %v", call.indexes)
	}
}

func TestChecker_PriorityFailureCleansUp(t *testing.T) {
	db := setupTestDB(t)
	seriesID := insertTestSeries(t, db, "Test Show")
	insertTestSeason(t, db, seriesID, 1,
		strPtr("https://kinozal.tv/details.php?id=123"),
		strPtr("oldhash"),
		strPtr("/media/TestShow/S01"))

	for i := 1; i <= 3; i++ {
		insertTestMediaFile(t, db, seriesID, 1, fmt.Sprintf("Test.Show.S01E%02d.mkv", i))
	}
	insertTestProcessedFile(t, db, seriesID, 1, "/media/TestShow/S01/Test.Show.S01E01.mkv")

	mock := &mockCheckerClient{
		canHandle:    true,
		episodeCount: 5,
		torrentData:  fakeTorrent("new8"),
	}
	registry := NewRegistry()
	registry.Register(mock)

	newHash, _ := qbittorrent.ComputeInfoHash(fakeTorrent("new8"))
	qbit := &mockQbitClient{
		torrents:       []qbittorrent.Torrent{{Hash: "oldhash", Category: "tv"}},
		addHash:        "newhash",
		files:          []qbittorrent.TorrentFile{{Index: 0, Name: "Test.Show.S01E01.mkv"}},
		setPriorityErr: fmt.Errorf("qbit priority error"),
	}
	checker := NewChecker(db, registry, qbit)

	results := checker.Check()
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Error == nil {
		t.Fatal("expected error when priority setting fails")
	}
	if results[0].Redownloaded {
		t.Error("should not mark as redownloaded when priorities failed")
	}

	// Should have cleaned up new torrent only (old torrent preserved for retry)
	if len(qbit.deletedHashes) != 1 {
		t.Fatalf("expected exactly 1 delete call (new torrent cleanup), got %d: %v", len(qbit.deletedHashes), qbit.deletedHashes)
	}
	if qbit.deletedHashes[0] != newHash {
		t.Errorf("expected new torrent %s to be deleted for cleanup, got %s", newHash, qbit.deletedHashes[0])
	}

	// DB hash should NOT be updated (still old)
	season, err := db.GetSeasonBySeriesAndNumber(seriesID, 1)
	if err != nil {
		t.Fatalf("get season: %v", err)
	}
	if season.TorrentHash == nil || *season.TorrentHash != "oldhash" {
		t.Errorf("expected torrent hash=oldhash (unchanged), got %v", season.TorrentHash)
	}
}
