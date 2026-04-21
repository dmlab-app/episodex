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
}

func (s *fakeSearcher) FindSeasonTorrent(query string, season int) (*tracker.SeasonSearchResult, error) {
	s.calls = append(s.calls, query)
	if err, ok := s.errFor[query]; ok {
		return nil, err
	}
	_ = season
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
				{ID: 600, Name: "Already Owned", VoteAverage: 9.0},  // resolves to owned tvdb
				{ID: 601, Name: "Blacklisted", VoteAverage: 8.5},    // resolves to blacklisted tvdb
				{ID: 602, Name: "Good Show", VoteAverage: 8.0},      // survives
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
