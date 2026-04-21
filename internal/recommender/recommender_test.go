package recommender

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/episodex/episodex/internal/database"
	"github.com/episodex/episodex/internal/tmdb"
	"github.com/episodex/episodex/internal/tracker"
)

// fakeTMDB implements tmdbClient for tests.
type fakeTMDB struct {
	findByTVDB      map[int]*tmdb.TMDBShow
	findErr         map[int]error
	recommendations map[int][]tmdb.TMDBShow
	recommendErr    map[int]error
	externalIDs     map[int]*tmdb.ExternalIDs
	externalErr     map[int]error

	findCalls    int
	recCalls     int
	extCalls     int
	extCallOrder []int
}

func (f *fakeTMDB) FindByTVDBID(tvdbID int) (*tmdb.TMDBShow, error) {
	f.findCalls++
	if err, ok := f.findErr[tvdbID]; ok {
		return nil, err
	}
	return f.findByTVDB[tvdbID], nil
}

func (f *fakeTMDB) GetRecommendations(tmdbID int) ([]tmdb.TMDBShow, error) {
	f.recCalls++
	if err, ok := f.recommendErr[tmdbID]; ok {
		return nil, err
	}
	return f.recommendations[tmdbID], nil
}

func (f *fakeTMDB) GetExternalIDs(tmdbID int) (*tmdb.ExternalIDs, error) {
	f.extCalls++
	f.extCallOrder = append(f.extCallOrder, tmdbID)
	if err, ok := f.externalErr[tmdbID]; ok {
		return nil, err
	}
	return f.externalIDs[tmdbID], nil
}

// fakeSearcher implements tracker.SeasonSearcher for tests.
type fakeSearcher struct {
	byQuery map[string]*tracker.SeasonSearchResult
	errFor  map[string]error
	calls   []string
	seasons []int
}

func (s *fakeSearcher) FindSeasonTorrent(query string, season int) (*tracker.SeasonSearchResult, error) {
	s.calls = append(s.calls, query)
	s.seasons = append(s.seasons, season)
	if err, ok := s.errFor[query]; ok {
		return nil, err
	}
	return s.byQuery[query], nil
}

func newTestDB(t *testing.T) *database.DB {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := database.New(dbPath)
	if err != nil {
		t.Fatalf("create test db: %v", err)
	}
	t.Cleanup(func() {
		db.Close()
		os.Remove(dbPath)
	})
	return db
}

func seedSeries(t *testing.T, db *database.DB, entries map[int]string) {
	t.Helper()
	for tvdbID, title := range entries {
		_, err := db.Exec(`INSERT INTO series (tvdb_id, title) VALUES (?, ?)`, tvdbID, title)
		if err != nil {
			t.Fatalf("seed series: %v", err)
		}
	}
}

func newRecommender(db *database.DB, t *fakeTMDB, s *fakeSearcher) *Recommender {
	r := New(db, t, s)
	r.sleep = func(time.Duration) {} // no-op in tests
	return r
}

func TestRefresh_NoOwnedSeries_ClearsRecommendations(t *testing.T) {
	db := newTestDB(t)
	// Pre-seed an existing recommendation to ensure it gets cleared.
	if err := db.ReplaceRecommendations([]database.Recommendation{
		{TVDBID: 999, Title: "Stale", Score: 1, TrackerURL: "x"},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	r := newRecommender(db, &fakeTMDB{}, &fakeSearcher{})
	if err := r.Refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	got, err := db.GetRecommendations()
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty recommendations, got %d", len(got))
	}
}

func TestRefresh_AggregatesFiltersAndWritesTop(t *testing.T) {
	db := newTestDB(t)
	seedSeries(t, db, map[int]string{
		1001: "Owned A",
		1002: "Owned B",
	})

	// TMDB returns tmdb shows for each owned tvdb id
	ft := &fakeTMDB{
		findByTVDB: map[int]*tmdb.TMDBShow{
			1001: {ID: 501, Name: "Owned A"},
			1002: {ID: 502, Name: "Owned B"},
		},
		recommendations: map[int][]tmdb.TMDBShow{
			// Owned A recommends X (rating 8.0), Y (rating 6.0 - filtered out), Z (rating 9.0)
			501: {
				{ID: 600, Name: "X Show", VoteAverage: 8.0, FirstAirDate: "2021-05-01", PosterPath: "/x.jpg", GenreIDs: []int{18}},
				{ID: 601, Name: "Y Show", VoteAverage: 6.0},
				{ID: 602, Name: "Z Show", VoteAverage: 9.0, FirstAirDate: "2020-01-02", PosterPath: "/z.jpg"},
			},
			// Owned B also recommends X — aggregated frequency=2
			502: {
				{ID: 600, Name: "X Show", VoteAverage: 8.0, FirstAirDate: "2021-05-01", PosterPath: "/x.jpg", GenreIDs: []int{18}},
				{ID: 603, Name: "W Show", VoteAverage: 7.5, FirstAirDate: "2019-03-01"},
			},
		},
		externalIDs: map[int]*tmdb.ExternalIDs{
			600: {TVDBId: 6000, ID: 600},
			602: {TVDBId: 6002, ID: 602},
			603: {TVDBId: 6003, ID: 603},
		},
	}

	fs := &fakeSearcher{
		byQuery: map[string]*tracker.SeasonSearchResult{
			"X Show": {DetailsURL: "https://kinozal.tv/x", Title: "X.S01", Size: "10 GB"},
			"Z Show": {DetailsURL: "https://kinozal.tv/z", Title: "Z.S01", Size: "5 GB"},
			"W Show": {DetailsURL: "https://kinozal.tv/w", Title: "W.S01", Size: "3 GB"},
		},
	}

	r := newRecommender(db, ft, fs)
	if err := r.Refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	got, err := db.GetRecommendations()
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	// Expected scores:
	//   X Show: freq=2, rating=8.0 → score 16.0
	//   Z Show: freq=1, rating=9.0 → score 9.0
	//   W Show: freq=1, rating=7.5 → score 7.5
	//   Y Show: filtered (rating<=7.0)
	if len(got) != 3 {
		t.Fatalf("expected 3 recommendations, got %d: %+v", len(got), got)
	}
	wantOrder := []int{6000, 6002, 6003}
	for i, tvdbID := range wantOrder {
		if got[i].TVDBID != tvdbID {
			t.Errorf("position %d: TVDBID=%d want %d", i, got[i].TVDBID, tvdbID)
		}
	}
	if got[0].Score != 16.0 {
		t.Errorf("top score = %v, want 16.0", got[0].Score)
	}
	if got[0].PosterURL != tmdb.PosterBaseURL+"/x.jpg" {
		t.Errorf("PosterURL = %q", got[0].PosterURL)
	}
	if got[0].Year != 2021 {
		t.Errorf("Year = %d, want 2021", got[0].Year)
	}
	if got[0].TrackerURL != "https://kinozal.tv/x" {
		t.Errorf("TrackerURL = %q", got[0].TrackerURL)
	}
	if got[0].TMDBID != 600 {
		t.Errorf("TMDBID = %d, want 600", got[0].TMDBID)
	}
}

func TestRefresh_ExcludesOwnedAndBlacklisted(t *testing.T) {
	db := newTestDB(t)
	seedSeries(t, db, map[int]string{
		1001: "Owned A",
	})
	if err := db.AddToBlacklist(7777, "Banned Show"); err != nil {
		t.Fatalf("blacklist: %v", err)
	}

	ft := &fakeTMDB{
		findByTVDB: map[int]*tmdb.TMDBShow{
			1001: {ID: 501, Name: "Owned A"},
		},
		recommendations: map[int][]tmdb.TMDBShow{
			501: {
				{ID: 600, Name: "Already Owned", VoteAverage: 9.0}, // resolves to owned tvdb
				{ID: 601, Name: "Blacklisted", VoteAverage: 8.5},   // resolves to blacklisted tvdb
				{ID: 602, Name: "Good Show", VoteAverage: 8.0},     // survives
			},
		},
		externalIDs: map[int]*tmdb.ExternalIDs{
			600: {TVDBId: 1001}, // owned
			601: {TVDBId: 7777}, // blacklisted
			602: {TVDBId: 6002},
		},
	}
	fs := &fakeSearcher{
		byQuery: map[string]*tracker.SeasonSearchResult{
			"Good Show": {DetailsURL: "https://kinozal.tv/g", Title: "G.S01", Size: "4 GB"},
		},
	}

	r := newRecommender(db, ft, fs)
	if err := r.Refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	got, err := db.GetRecommendations()
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got) != 1 || got[0].TVDBID != 6002 {
		t.Errorf("expected only Good Show (tvdb 6002), got %+v", got)
	}
}

func TestRefresh_SkipsKinozalMisses(t *testing.T) {
	db := newTestDB(t)
	seedSeries(t, db, map[int]string{1001: "Owned A"})

	ft := &fakeTMDB{
		findByTVDB: map[int]*tmdb.TMDBShow{
			1001: {ID: 501, Name: "Owned A"},
		},
		recommendations: map[int][]tmdb.TMDBShow{
			501: {
				{ID: 600, Name: "Not On Kinozal", VoteAverage: 9.0},
				{ID: 601, Name: "On Kinozal", VoteAverage: 8.0},
			},
		},
		externalIDs: map[int]*tmdb.ExternalIDs{
			600: {TVDBId: 6000},
			601: {TVDBId: 6001},
		},
	}
	fs := &fakeSearcher{
		byQuery: map[string]*tracker.SeasonSearchResult{
			"On Kinozal": {DetailsURL: "https://kinozal.tv/ok", Title: "OK.S01", Size: "6 GB"},
		},
	}

	r := newRecommender(db, ft, fs)
	if err := r.Refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	got, _ := db.GetRecommendations()
	if len(got) != 1 || got[0].TVDBID != 6001 {
		t.Errorf("expected only On Kinozal (6001), got %+v", got)
	}
}

func TestRefresh_KinozalOriginalNameFallback(t *testing.T) {
	db := newTestDB(t)
	seedSeries(t, db, map[int]string{1001: "Owned A"})

	ft := &fakeTMDB{
		findByTVDB: map[int]*tmdb.TMDBShow{
			1001: {ID: 501, Name: "Owned A"},
		},
		recommendations: map[int][]tmdb.TMDBShow{
			501: {
				{ID: 600, Name: "Localized", OriginalName: "OriginalTitle", VoteAverage: 8.0},
			},
		},
		externalIDs: map[int]*tmdb.ExternalIDs{
			600: {TVDBId: 6000},
		},
	}
	// Only the original name has a torrent.
	fs := &fakeSearcher{
		byQuery: map[string]*tracker.SeasonSearchResult{
			"OriginalTitle": {DetailsURL: "https://kinozal.tv/o", Title: "O.S01", Size: "8 GB"},
		},
	}

	r := newRecommender(db, ft, fs)
	if err := r.Refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	got, _ := db.GetRecommendations()
	if len(got) != 1 {
		t.Fatalf("expected 1 result, got %d", len(got))
	}
	if got[0].TrackerURL != "https://kinozal.tv/o" {
		t.Errorf("tracker url = %q", got[0].TrackerURL)
	}
	// Both queries must have been attempted.
	if len(fs.calls) != 2 {
		t.Errorf("expected 2 searcher calls (primary + fallback), got %d: %v", len(fs.calls), fs.calls)
	}
	// Season 1 must be requested for both the primary and the fallback call.
	for i, s := range fs.seasons {
		if s != 1 {
			t.Errorf("call %d asked for season %d, want 1", i, s)
		}
	}
}

func TestRefresh_KinozalPrimaryErrorFallsBackToOriginalName(t *testing.T) {
	db := newTestDB(t)
	seedSeries(t, db, map[int]string{1001: "Owned A"})

	ft := &fakeTMDB{
		findByTVDB: map[int]*tmdb.TMDBShow{
			1001: {ID: 501, Name: "Owned A"},
		},
		recommendations: map[int][]tmdb.TMDBShow{
			501: {
				{ID: 600, Name: "Localized", OriginalName: "OriginalTitle", VoteAverage: 8.0},
			},
		},
		externalIDs: map[int]*tmdb.ExternalIDs{
			600: {TVDBId: 6000},
		},
	}
	// Primary name errors transiently; original name succeeds.
	fs := &fakeSearcher{
		byQuery: map[string]*tracker.SeasonSearchResult{
			"OriginalTitle": {DetailsURL: "https://kinozal.tv/o", Title: "O.S01", Size: "8 GB"},
		},
		errFor: map[string]error{
			"Localized": errors.New("kinozal transient error"),
		},
	}

	r := newRecommender(db, ft, fs)
	if err := r.Refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	got, _ := db.GetRecommendations()
	if len(got) != 1 {
		t.Fatalf("expected 1 result (fallback after primary error), got %d: %+v", len(got), got)
	}
	if got[0].TrackerURL != "https://kinozal.tv/o" {
		t.Errorf("tracker url = %q; want fallback URL", got[0].TrackerURL)
	}
	if len(fs.calls) != 2 {
		t.Errorf("expected primary+fallback calls, got %d: %v", len(fs.calls), fs.calls)
	}
}

func TestRefresh_TopCutoffAt20(t *testing.T) {
	db := newTestDB(t)
	seedSeries(t, db, map[int]string{1001: "Owned A"})

	// Generate 25 candidates, all above rating threshold, all with kinozal hits.
	recs := make([]tmdb.TMDBShow, 0, 25)
	externals := make(map[int]*tmdb.ExternalIDs)
	searchResults := make(map[string]*tracker.SeasonSearchResult)
	for i := 0; i < 25; i++ {
		id := 600 + i
		name := "Show" + itoa(i)
		// Descending rating so ordering is deterministic.
		recs = append(recs, tmdb.TMDBShow{ID: id, Name: name, VoteAverage: 9.0 - 0.01*float64(i)})
		externals[id] = &tmdb.ExternalIDs{TVDBId: 7000 + i}
		searchResults[name] = &tracker.SeasonSearchResult{DetailsURL: "https://kinozal.tv/" + name, Title: name, Size: "1 GB"}
	}

	ft := &fakeTMDB{
		findByTVDB:      map[int]*tmdb.TMDBShow{1001: {ID: 501, Name: "Owned A"}},
		recommendations: map[int][]tmdb.TMDBShow{501: recs},
		externalIDs:     externals,
	}
	fs := &fakeSearcher{byQuery: searchResults}

	r := newRecommender(db, ft, fs)
	if err := r.Refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	got, _ := db.GetRecommendations()
	if len(got) != maxFinal {
		t.Errorf("expected %d recommendations, got %d", maxFinal, len(got))
	}
}

func TestRefresh_ConcurrentCallsSkipSecond(t *testing.T) {
	db := newTestDB(t)
	r := newRecommender(db, &fakeTMDB{}, &fakeSearcher{})

	// Hold the lock to simulate an in-progress refresh.
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := r.Refresh(); err != nil {
		t.Errorf("expected nil error when refresh is in progress, got %v", err)
	}
}

func TestRefresh_FindErrorSkipsSeries(t *testing.T) {
	db := newTestDB(t)
	seedSeries(t, db, map[int]string{
		1001: "Bad Lookup",
		1002: "Good Lookup",
	})

	ft := &fakeTMDB{
		findByTVDB: map[int]*tmdb.TMDBShow{
			1002: {ID: 502, Name: "Good Lookup"},
		},
		findErr: map[int]error{
			1001: errors.New("tmdb down"),
		},
		recommendations: map[int][]tmdb.TMDBShow{
			502: {{ID: 600, Name: "Kept", VoteAverage: 8.0}},
		},
		externalIDs: map[int]*tmdb.ExternalIDs{
			600: {TVDBId: 6000},
		},
	}
	fs := &fakeSearcher{
		byQuery: map[string]*tracker.SeasonSearchResult{
			"Kept": {DetailsURL: "https://kinozal.tv/k", Title: "K.S01", Size: "2 GB"},
		},
	}

	r := newRecommender(db, ft, fs)
	if err := r.Refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	got, _ := db.GetRecommendations()
	if len(got) != 1 || got[0].TVDBID != 6000 {
		t.Errorf("expected one result for surviving series, got %+v", got)
	}
}

func TestRefresh_AllTMDBCallsFailedPreservesExisting(t *testing.T) {
	db := newTestDB(t)
	seedSeries(t, db, map[int]string{
		1001: "Owned A",
		1002: "Owned B",
	})
	// Pre-seed existing recommendations that must survive a total TMDB outage.
	if err := db.ReplaceRecommendations([]database.Recommendation{
		{TVDBID: 9001, Title: "Existing 1", Score: 10, TrackerURL: "u1"},
		{TVDBID: 9002, Title: "Existing 2", Score: 5, TrackerURL: "u2"},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	ft := &fakeTMDB{
		findErr: map[int]error{
			1001: errors.New("tmdb down"),
			1002: errors.New("tmdb down"),
		},
	}
	fs := &fakeSearcher{}

	r := newRecommender(db, ft, fs)
	if err := r.Refresh(); err == nil {
		t.Fatal("expected refresh to return error when all TMDB lookups failed")
	}

	// Existing recommendations must still be present — outage must not wipe.
	got, err := db.GetRecommendations()
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("expected 2 preserved recommendations, got %d: %+v", len(got), got)
	}
}

func TestRefresh_UnresolvedTVDBIDSkipped(t *testing.T) {
	db := newTestDB(t)
	seedSeries(t, db, map[int]string{1001: "Owned A"})

	ft := &fakeTMDB{
		findByTVDB: map[int]*tmdb.TMDBShow{
			1001: {ID: 501, Name: "Owned A"},
		},
		recommendations: map[int][]tmdb.TMDBShow{
			501: {
				{ID: 600, Name: "NoTVDB", VoteAverage: 9.0},
				{ID: 601, Name: "HasTVDB", VoteAverage: 8.0},
			},
		},
		externalIDs: map[int]*tmdb.ExternalIDs{
			600: {TVDBId: 0}, // TMDB returned no tvdb mapping
			601: {TVDBId: 6001},
		},
	}
	fs := &fakeSearcher{
		byQuery: map[string]*tracker.SeasonSearchResult{
			"HasTVDB": {DetailsURL: "https://kinozal.tv/h", Title: "H.S01", Size: "1 GB"},
		},
	}

	r := newRecommender(db, ft, fs)
	if err := r.Refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	got, _ := db.GetRecommendations()
	if len(got) != 1 || got[0].TVDBID != 6001 {
		t.Errorf("expected only HasTVDB, got %+v", got)
	}
}

func TestRefresh_DedupsDuplicateTVDBIDs(t *testing.T) {
	db := newTestDB(t)
	seedSeries(t, db, map[int]string{1001: "Owned A"})

	// Two different TMDB IDs resolve to the same TVDB ID (TMDB can have
	// duplicate show entries pointing at the same TVDB series). Without
	// dedup, the second INSERT fails with UNIQUE constraint violation and
	// the whole refresh transaction rolls back.
	ft := &fakeTMDB{
		findByTVDB: map[int]*tmdb.TMDBShow{
			1001: {ID: 501, Name: "Owned A"},
		},
		recommendations: map[int][]tmdb.TMDBShow{
			501: {
				{ID: 600, Name: "Primary Entry", VoteAverage: 9.0},
				{ID: 601, Name: "Duplicate Entry", VoteAverage: 8.0},
				{ID: 602, Name: "Distinct Show", VoteAverage: 7.5},
			},
		},
		externalIDs: map[int]*tmdb.ExternalIDs{
			600: {TVDBId: 6000},
			601: {TVDBId: 6000}, // same TVDB ID as 600
			602: {TVDBId: 6002},
		},
	}
	fs := &fakeSearcher{
		byQuery: map[string]*tracker.SeasonSearchResult{
			"Primary Entry":   {DetailsURL: "https://kinozal.tv/p", Title: "P.S01", Size: "4 GB"},
			"Duplicate Entry": {DetailsURL: "https://kinozal.tv/d", Title: "D.S01", Size: "5 GB"},
			"Distinct Show":   {DetailsURL: "https://kinozal.tv/x", Title: "X.S01", Size: "2 GB"},
		},
	}

	r := newRecommender(db, ft, fs)
	if err := r.Refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	got, err := db.GetRecommendations()
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 recommendations (dup collapsed), got %d: %+v", len(got), got)
	}
	// First entry wins for TVDB 6000 — it came from the higher-scored candidate.
	if got[0].TVDBID != 6000 || got[0].TMDBID != 600 {
		t.Errorf("top result = TVDB %d/TMDB %d, want 6000/600", got[0].TVDBID, got[0].TMDBID)
	}
	if got[1].TVDBID != 6002 {
		t.Errorf("second result TVDB = %d, want 6002", got[1].TVDBID)
	}
}

// Partial TMDB outage: /find returns no match for some shows (legitimate)
// while /recommendations fails for the shows that DO match. With the old
// guard this would have bypassed preservation (processedOK incremented from
// the no-match path) and silently wiped the table.
func TestRefresh_PartialOutage_RecommendationsEndpointFailing_PreservesExisting(t *testing.T) {
	db := newTestDB(t)
	seedSeries(t, db, map[int]string{
		1001: "Unknown to TMDB",
		1002: "Known but Rec Endpoint Fails",
	})
	if err := db.ReplaceRecommendations([]database.Recommendation{
		{TVDBID: 9001, Title: "Existing", Score: 10, TrackerURL: "u"},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	ft := &fakeTMDB{
		// 1001 -> no TMDB match. 1002 -> match, but /recommendations fails.
		findByTVDB: map[int]*tmdb.TMDBShow{
			1002: {ID: 502, Name: "Known"},
		},
		recommendErr: map[int]error{
			502: errors.New("tmdb /recommendations down"),
		},
	}
	fs := &fakeSearcher{}

	r := newRecommender(db, ft, fs)
	if err := r.Refresh(); err == nil {
		t.Fatal("expected error when all recommendation fetches fail during partial outage")
	}

	got, err := db.GetRecommendations()
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got) != 1 || got[0].TVDBID != 9001 {
		t.Errorf("expected existing recommendation preserved, got %+v", got)
	}
}

// Partial TMDB outage on /external_ids: aggregation succeeds but every
// external_ids probe errors. The existing table must be preserved rather
// than overwritten with an empty slice.
func TestRefresh_ExternalIDsAllFail_PreservesExisting(t *testing.T) {
	db := newTestDB(t)
	seedSeries(t, db, map[int]string{1001: "Owned A"})
	if err := db.ReplaceRecommendations([]database.Recommendation{
		{TVDBID: 9001, Title: "Existing", Score: 10, TrackerURL: "u"},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	ft := &fakeTMDB{
		findByTVDB: map[int]*tmdb.TMDBShow{
			1001: {ID: 501, Name: "Owned A"},
		},
		recommendations: map[int][]tmdb.TMDBShow{
			501: {
				{ID: 600, Name: "Candidate 1", VoteAverage: 8.0},
				{ID: 601, Name: "Candidate 2", VoteAverage: 7.5},
			},
		},
		externalErr: map[int]error{
			600: errors.New("external_ids down"),
			601: errors.New("external_ids down"),
		},
	}
	fs := &fakeSearcher{}

	r := newRecommender(db, ft, fs)
	if err := r.Refresh(); err == nil {
		t.Fatal("expected error when all external_ids probes fail")
	}

	got, err := db.GetRecommendations()
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got) != 1 || got[0].TVDBID != 9001 {
		t.Errorf("expected existing recommendation preserved, got %+v", got)
	}
}

// Partial Kinozal outage: every Kinozal search errors. Existing table must
// be preserved rather than overwritten with an empty slice.
func TestRefresh_KinozalAllFail_PreservesExisting(t *testing.T) {
	db := newTestDB(t)
	seedSeries(t, db, map[int]string{1001: "Owned A"})
	if err := db.ReplaceRecommendations([]database.Recommendation{
		{TVDBID: 9001, Title: "Existing", Score: 10, TrackerURL: "u"},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	ft := &fakeTMDB{
		findByTVDB: map[int]*tmdb.TMDBShow{
			1001: {ID: 501, Name: "Owned A"},
		},
		recommendations: map[int][]tmdb.TMDBShow{
			501: {
				{ID: 600, Name: "Candidate 1", VoteAverage: 8.0},
				{ID: 601, Name: "Candidate 2", OriginalName: "Original 2", VoteAverage: 7.5},
			},
		},
		externalIDs: map[int]*tmdb.ExternalIDs{
			600: {TVDBId: 6000},
			601: {TVDBId: 6001},
		},
	}
	fs := &fakeSearcher{
		errFor: map[string]error{
			"Candidate 1": errors.New("kinozal down"),
			"Candidate 2": errors.New("kinozal down"),
			"Original 2":  errors.New("kinozal down"),
		},
	}

	r := newRecommender(db, ft, fs)
	if err := r.Refresh(); err == nil {
		t.Fatal("expected error when all Kinozal searches fail")
	}

	got, err := db.GetRecommendations()
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got) != 1 || got[0].TVDBID != 9001 {
		t.Errorf("expected existing recommendation preserved, got %+v", got)
	}
}

// One /recommendations call succeeds with an empty list while the other errors.
// Candidates is empty but the outage was real — existing rows must survive
// rather than being wiped as if the refresh legitimately produced no results.
func TestRefresh_PartialOutage_OneEmptyOneErrored_PreservesExisting(t *testing.T) {
	db := newTestDB(t)
	seedSeries(t, db, map[int]string{
		1001: "Owned A",
		1002: "Owned B",
	})
	if err := db.ReplaceRecommendations([]database.Recommendation{
		{TVDBID: 9001, Title: "Existing", Score: 10, TrackerURL: "u"},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	ft := &fakeTMDB{
		findByTVDB: map[int]*tmdb.TMDBShow{
			1001: {ID: 501, Name: "Owned A"},
			1002: {ID: 502, Name: "Owned B"},
		},
		// 501 succeeds with empty recs; 502 errors. recsSucceeded=1, tmdbErrors=1.
		recommendations: map[int][]tmdb.TMDBShow{
			501: {},
		},
		recommendErr: map[int]error{
			502: errors.New("tmdb down"),
		},
	}
	fs := &fakeSearcher{}

	r := newRecommender(db, ft, fs)
	if err := r.Refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	got, err := db.GetRecommendations()
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got) != 1 || got[0].TVDBID != 9001 {
		t.Errorf("expected existing recommendation preserved, got %+v", got)
	}
}

// One external_ids probe succeeds but yields no usable candidate (tvdb_id=0);
// a subsequent probe errors. recs ends up empty but the outage was real —
// existing rows must be preserved rather than wiped.
func TestRefresh_PartialOutage_ExternalIDsMixedOutcome_PreservesExisting(t *testing.T) {
	db := newTestDB(t)
	seedSeries(t, db, map[int]string{1001: "Owned A"})
	if err := db.ReplaceRecommendations([]database.Recommendation{
		{TVDBID: 9001, Title: "Existing", Score: 10, TrackerURL: "u"},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	ft := &fakeTMDB{
		findByTVDB: map[int]*tmdb.TMDBShow{
			1001: {ID: 501, Name: "Owned A"},
		},
		recommendations: map[int][]tmdb.TMDBShow{
			501: {
				{ID: 600, Name: "Unresolved", VoteAverage: 9.0},
				{ID: 601, Name: "Errored", VoteAverage: 8.0},
			},
		},
		externalIDs: map[int]*tmdb.ExternalIDs{
			// 600 succeeds with no tvdb mapping — extSucceeded=1 but unusable.
			600: {TVDBId: 0},
		},
		externalErr: map[int]error{
			601: errors.New("external_ids down"),
		},
	}
	fs := &fakeSearcher{}

	r := newRecommender(db, ft, fs)
	if err := r.Refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	got, err := db.GetRecommendations()
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got) != 1 || got[0].TVDBID != 9001 {
		t.Errorf("expected existing recommendation preserved, got %+v", got)
	}
}

// Primary Kinozal search returns (nil, nil) — a clean miss, not an error —
// and the original-name fallback errors. The overall result is empty but the
// fallback error means upstream Kinozal is partially degraded; existing
// recommendations must survive rather than being wiped.
func TestRefresh_KinozalPrimaryMissAndFallbackError_PreservesExisting(t *testing.T) {
	db := newTestDB(t)
	seedSeries(t, db, map[int]string{1001: "Owned A"})
	if err := db.ReplaceRecommendations([]database.Recommendation{
		{TVDBID: 9001, Title: "Existing", Score: 10, TrackerURL: "u"},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	ft := &fakeTMDB{
		findByTVDB: map[int]*tmdb.TMDBShow{
			1001: {ID: 501, Name: "Owned A"},
		},
		recommendations: map[int][]tmdb.TMDBShow{
			501: {
				{ID: 600, Name: "Localized", OriginalName: "OriginalTitle", VoteAverage: 8.0},
			},
		},
		externalIDs: map[int]*tmdb.ExternalIDs{
			600: {TVDBId: 6000},
		},
	}
	// Primary returns (nil, nil) — not in byQuery, no error registered.
	// Fallback errors. No torrent produced; upstream had a real error.
	fs := &fakeSearcher{
		errFor: map[string]error{
			"OriginalTitle": errors.New("kinozal fallback down"),
		},
	}

	r := newRecommender(db, ft, fs)
	if err := r.Refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	got, err := db.GetRecommendations()
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got) != 1 || got[0].TVDBID != 9001 {
		t.Errorf("expected existing recommendation preserved, got %+v", got)
	}
}

// All owned shows legitimately unknown to TMDB (no errors, just no match).
// This is a valid zero-state — recommendations should be cleared, not
// preserved as an "outage".
func TestRefresh_AllOwnedUnknownToTMDB_ClearsRecommendations(t *testing.T) {
	db := newTestDB(t)
	seedSeries(t, db, map[int]string{
		1001: "Obscure A",
		1002: "Obscure B",
	})
	if err := db.ReplaceRecommendations([]database.Recommendation{
		{TVDBID: 9001, Title: "Stale", Score: 10, TrackerURL: "u"},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// No errors, no matches — legitimate "none of your shows are on TMDB".
	ft := &fakeTMDB{}
	fs := &fakeSearcher{}

	r := newRecommender(db, ft, fs)
	if err := r.Refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	got, err := db.GetRecommendations()
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected recommendations cleared, got %d: %+v", len(got), got)
	}
}

func TestParseYear(t *testing.T) {
	cases := map[string]int{
		"2021-05-01": 2021,
		"1999":       1999,
		"":           0,
		"abc":        0,
		"20":         0,
	}
	for in, want := range cases {
		if got := parseYear(in); got != want {
			t.Errorf("parseYear(%q) = %d, want %d", in, got, want)
		}
	}
}

// itoa avoids pulling strconv into the main test fixture setup repeatedly.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
