package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/episodex/episodex/internal/database"
	"github.com/episodex/episodex/internal/tvdb"
)

// newTestTVDBServer creates a mock TVDB API server and a client connected to it.
func newTestTVDBServer(t *testing.T, handler http.Handler) *tvdb.Client {
	t.Helper()
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	client := tvdb.NewClientWithBaseURL("test-key", ts.URL+"/v4")
	return client
}

// tvdbMux creates a handler that serves mock TVDB API responses.
func tvdbMux(extendedResp, translationResp interface{}) http.Handler {
	mux := http.NewServeMux()

	// Login endpoint
	mux.HandleFunc("/v4/login", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "success",
			"data":   map[string]string{"token": "test-token"},
		})
	})

	// Catch-all for /v4/series/ paths: distinguish extended vs translations by path content
	mux.HandleFunc("/v4/series/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case strings.Contains(path, "/translations/"):
			if translationResp != nil {
				_ = json.NewEncoder(w).Encode(translationResp)
			} else {
				w.WriteHeader(http.StatusNotFound)
			}
		default:
			// /v4/series/{id}/extended
			_ = json.NewEncoder(w).Encode(extendedResp)
		}
	})

	return mux
}

func TestSyncSeriesMetadata_Success(t *testing.T) {
	// Setup test database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := database.New(dbPath)
	if err != nil {
		t.Fatalf("failed to create test database: %v", err)
	}
	t.Cleanup(func() {
		db.Close()
		os.Remove(dbPath)
	})

	// Seed a series with TVDB ID
	tvdbID := 81189
	result, err := db.Exec(`
		INSERT INTO series (tvdb_id, title, status, total_seasons, created_at, updated_at)
		VALUES (?, 'Breaking Bad', 'Continuing', 3, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, tvdbID)
	if err != nil {
		t.Fatalf("failed to seed series: %v", err)
	}
	seriesID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("failed to get last insert ID: %v", err)
	}

	// Create mock TVDB server
	extendedResp := map[string]interface{}{
		"status": "success",
		"data": map[string]interface{}{
			"id":               tvdbID,
			"name":             "Breaking Bad",
			"originalName":     "Breaking Bad",
			"slug":             "breaking-bad",
			"overview":         "A chemistry teacher becomes a drug dealer.",
			"image":            "https://tvdb.com/poster.jpg",
			"status":           map[string]interface{}{"name": "Ended"},
			"firstAired":       "2008-01-20",
			"lastAired":        "2013-09-29",
			"year":             "2008",
			"averageRuntime":   47,
			"score":            9.5,
			"originalCountry":  "usa",
			"originalLanguage": "eng",
			"seasons": []map[string]interface{}{
				{"id": 101, "number": 1, "type": map[string]interface{}{"name": "Official", "type": "official"}, "name": "Season 1", "year": "2008", "image": ""},
				{"id": 102, "number": 2, "type": map[string]interface{}{"name": "Official", "type": "official"}, "name": "Season 2", "year": "2009", "image": ""},
				{"id": 103, "number": 3, "type": map[string]interface{}{"name": "Official", "type": "official"}, "name": "Season 3", "year": "2010", "image": ""},
				{"id": 104, "number": 4, "type": map[string]interface{}{"name": "Official", "type": "official"}, "name": "Season 4", "year": "2011", "image": ""},
				{"id": 105, "number": 5, "type": map[string]interface{}{"name": "Official", "type": "official"}, "name": "Season 5", "year": "2012", "image": ""},
			},
			"genres":         []interface{}{},
			"artworks":       []interface{}{},
			"characters":     []interface{}{},
			"contentRatings": []interface{}{},
			"companies":      []interface{}{},
		},
	}

	translationResp := map[string]interface{}{
		"status": "success",
		"data": map[string]interface{}{
			"name":     "Во все тяжкие",
			"overview": "Учитель химии начинает варить мет.",
			"language": "rus",
		},
	}

	tvdbClient := newTestTVDBServer(t, tvdbMux(extendedResp, translationResp))
	if err := tvdbClient.Login(); err != nil {
		t.Fatalf("failed to login to mock TVDB: %v", err)
	}

	// Run sync
	err = SyncSeriesMetadata(db, tvdbClient, seriesID, tvdbID)
	if err != nil {
		t.Fatalf("SyncSeriesMetadata failed: %v", err)
	}

	// Verify series was updated
	var title, overview string
	var totalSeasons, airedSeasons int
	err = db.QueryRow(`SELECT title, overview, total_seasons, aired_seasons FROM series WHERE id = ?`, seriesID).
		Scan(&title, &overview, &totalSeasons, &airedSeasons)
	if err != nil {
		t.Fatalf("failed to query series: %v", err)
	}

	if title != "Во все тяжкие" {
		t.Errorf("expected Russian title, got %q", title)
	}
	if overview != "Учитель химии начинает варить мет." {
		t.Errorf("expected Russian overview, got %q", overview)
	}
	if totalSeasons != 5 {
		t.Errorf("expected 5 total seasons, got %d", totalSeasons)
	}
	// All 5 seasons have years in the past (2008-2012), so all should be aired
	if airedSeasons != 5 {
		t.Errorf("expected 5 aired seasons, got %d", airedSeasons)
	}

	// Verify seasons were created
	var seasonCount int
	err = db.QueryRow(`SELECT COUNT(*) FROM seasons WHERE series_id = ?`, seriesID).Scan(&seasonCount)
	if err != nil {
		t.Fatalf("failed to count seasons: %v", err)
	}
	if seasonCount != 5 {
		t.Errorf("expected 5 seasons, got %d", seasonCount)
	}

}

func TestSyncSeriesMetadata_TVDBIDChanged(t *testing.T) {
	// Verify that SyncSeriesMetadata aborts when the series was rematched
	// to a different TVDB ID (simulates concurrent rematch during sync).
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := database.New(dbPath)
	if err != nil {
		t.Fatalf("failed to create test database: %v", err)
	}
	t.Cleanup(func() {
		db.Close()
		os.Remove(dbPath)
	})

	// Seed a series with TVDB ID 100
	oldTVDBID := 100
	result, err := db.Exec(`
		INSERT INTO series (tvdb_id, title, status, total_seasons, created_at, updated_at)
		VALUES (?, 'Old Show', 'Continuing', 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, oldTVDBID)
	if err != nil {
		t.Fatalf("failed to seed series: %v", err)
	}
	seriesID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("failed to get last insert ID: %v", err)
	}

	// Simulate a rematch: change tvdb_id to 200 (as handleMatchSeries would)
	newTVDBID := 200
	_, err = db.Exec(`UPDATE series SET tvdb_id = ? WHERE id = ?`, newTVDBID, seriesID)
	if err != nil {
		t.Fatalf("failed to update tvdb_id: %v", err)
	}

	// Create mock TVDB server with data for OLD tvdb_id
	extendedResp := map[string]interface{}{
		"status": "success",
		"data": map[string]interface{}{
			"id":           oldTVDBID,
			"name":         "Old Show Data",
			"originalName": "Old Show",
			"slug":         "old-show",
			"overview":     "This is stale data from old TVDB match",
			"image":        "https://tvdb.com/old.jpg",
			"status":       map[string]interface{}{"name": "Ended"},
			"seasons": []map[string]interface{}{
				{"id": 501, "number": 1, "type": map[string]interface{}{"name": "Official", "type": "official"}, "name": "Stale Season 1"},
			},
			"genres":         []interface{}{},
			"artworks":       []interface{}{},
			"characters":     []interface{}{},
			"contentRatings": []interface{}{},
			"companies":      []interface{}{},
		},
	}

	tvdbClient := newTestTVDBServer(t, tvdbMux(extendedResp, nil))
	if err := tvdbClient.Login(); err != nil {
		t.Fatalf("failed to login to mock TVDB: %v", err)
	}

	// Call SyncSeriesMetadata with the OLD tvdb_id — should fail because
	// SyncSeriesAndChildren's WHERE clause requires tvdb_id to match.
	err = SyncSeriesMetadata(db, tvdbClient, seriesID, oldTVDBID)
	if err == nil {
		t.Fatal("expected SyncSeriesMetadata to fail when tvdb_id changed, but it succeeded")
	}

	// Verify no stale seasons were created
	var seasonCount int
	err = db.QueryRow(`SELECT COUNT(*) FROM seasons WHERE series_id = ?`, seriesID).Scan(&seasonCount)
	if err != nil {
		t.Fatalf("failed to count seasons: %v", err)
	}
	if seasonCount != 0 {
		t.Errorf("expected 0 seasons (sync should have aborted), got %d", seasonCount)
	}

	// Verify the series title was NOT overwritten with stale data
	var title string
	err = db.QueryRow(`SELECT title FROM series WHERE id = ?`, seriesID).Scan(&title)
	if err != nil {
		t.Fatalf("failed to query series: %v", err)
	}
	if title != "Old Show" {
		t.Errorf("expected title unchanged as 'Old Show', got %q", title)
	}
}

func TestSyncSeriesAndChildren_TVDBIDMismatch(t *testing.T) {
	// Verify that SyncSeriesAndChildren rejects both parent and child writes
	// when tvdb_id doesn't match, ensuring atomicity of the entire sync.
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := database.New(dbPath)
	if err != nil {
		t.Fatalf("failed to create test database: %v", err)
	}
	t.Cleanup(func() {
		db.Close()
		os.Remove(dbPath)
	})

	// Seed a series with TVDB ID 200 (simulating it was already rematched)
	result, err := db.Exec(`
		INSERT INTO series (tvdb_id, title, status, total_seasons, created_at, updated_at)
		VALUES (200, 'Rematched Show', 'Continuing', 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`)
	if err != nil {
		t.Fatalf("failed to seed series: %v", err)
	}
	seriesID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("failed to get last insert ID: %v", err)
	}

	// Try to write parent + child records with OLD tvdb_id (100) — should fail
	staleOverview := "Stale overview from old TVDB match"
	staleSeries := &database.Series{
		Title:        "Stale Title",
		Overview:     &staleOverview,
		TotalSeasons: 5,
		AiredSeasons: 5,
	}
	staleSeasons := []database.Season{
		{SeriesID: seriesID, SeasonNumber: 1, Name: strPtr("Stale Season")},
	}
	charName := "Stale Character"
	staleCharacters := []database.Character{
		{SeriesID: seriesID, CharacterName: &charName},
	}

	err = db.SyncSeriesAndChildren(seriesID, 100, staleSeries, staleSeasons, staleCharacters)
	if err == nil {
		t.Fatal("expected SyncSeriesAndChildren to fail when tvdb_id mismatches, but it succeeded")
	}

	// Verify parent was NOT updated with stale data (transaction rolled back)
	var title string
	var totalSeasons int
	if err := db.QueryRow(`SELECT title, total_seasons FROM series WHERE id = ?`, seriesID).Scan(&title, &totalSeasons); err != nil {
		t.Fatalf("failed to query series: %v", err)
	}
	if title != "Rematched Show" {
		t.Errorf("expected title unchanged as 'Rematched Show', got %q", title)
	}
	if totalSeasons != 1 {
		t.Errorf("expected total_seasons unchanged at 1, got %d", totalSeasons)
	}

	// Verify no stale seasons were created (transaction should have rolled back)
	var seasonCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM seasons WHERE series_id = ?`, seriesID).Scan(&seasonCount); err != nil {
		t.Fatalf("failed to count seasons: %v", err)
	}
	if seasonCount != 0 {
		t.Errorf("expected 0 seasons after rolled-back sync, got %d", seasonCount)
	}

	// Verify no stale characters were created
	var charCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM series_characters WHERE series_id = ?`, seriesID).Scan(&charCount); err != nil {
		t.Fatalf("failed to count characters: %v", err)
	}
	if charCount != 0 {
		t.Errorf("expected 0 characters after rolled-back sync, got %d", charCount)
	}
}

func strPtr(s string) *string { return &s }

func TestSyncUnsyncedSeries_NoUnsynced(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := database.New(dbPath)
	if err != nil {
		t.Fatalf("failed to create test database: %v", err)
	}
	t.Cleanup(func() {
		db.Close()
		os.Remove(dbPath)
	})

	// Insert a series that already has overview (should NOT be picked up)
	_, err = db.Exec(`
		INSERT INTO series (tvdb_id, title, overview, status, total_seasons, created_at, updated_at)
		VALUES (12345, 'Already Synced', 'Has overview', 'Continuing', 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`)
	if err != nil {
		t.Fatalf("failed to seed series: %v", err)
	}

	tvdbClient := newTestTVDBServer(t, http.NewServeMux())

	// Should return without errors - nothing to sync
	SyncUnsyncedSeries(db, tvdbClient)
}

func TestSyncUnsyncedSeries_SyncsSuccessfully(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := database.New(dbPath)
	if err != nil {
		t.Fatalf("failed to create test database: %v", err)
	}
	t.Cleanup(func() {
		db.Close()
		os.Remove(dbPath)
	})

	// Insert an unsynced series (tvdb_id set, overview NULL)
	tvdbID := 81189
	_, err = db.Exec(`
		INSERT INTO series (tvdb_id, title, status, total_seasons, created_at, updated_at)
		VALUES (?, 'Unsynced Show', 'Continuing', 0, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, tvdbID)
	if err != nil {
		t.Fatalf("failed to seed series: %v", err)
	}

	// Create mock TVDB server
	extendedResp := map[string]interface{}{
		"status": "success",
		"data": map[string]interface{}{
			"id":               tvdbID,
			"name":             "Unsynced Show",
			"originalName":     "Unsynced Show",
			"slug":             "unsynced-show",
			"overview":         "Now it has an overview.",
			"image":            "https://tvdb.com/poster.jpg",
			"status":           map[string]interface{}{"name": "Continuing"},
			"seasons":          []map[string]interface{}{},
			"genres":           []interface{}{},
			"artworks":         []interface{}{},
			"characters":       []interface{}{},
			"contentRatings":   []interface{}{},
			"companies":        []interface{}{},
		},
	}

	tvdbClient := newTestTVDBServer(t, tvdbMux(extendedResp, nil))
	if err := tvdbClient.Login(); err != nil {
		t.Fatalf("failed to login to mock TVDB: %v", err)
	}

	SyncUnsyncedSeries(db, tvdbClient)

	// Verify the series was synced (overview should now be set)
	var overview string
	err = db.QueryRow(`SELECT overview FROM series WHERE tvdb_id = ?`, tvdbID).Scan(&overview)
	if err != nil {
		t.Fatalf("failed to query series: %v", err)
	}
	if overview != "Now it has an overview." {
		t.Errorf("expected overview to be set, got %q", overview)
	}
}

func TestSyncUnsyncedSeries_ErrorDoesNotStopOthers(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := database.New(dbPath)
	if err != nil {
		t.Fatalf("failed to create test database: %v", err)
	}
	t.Cleanup(func() {
		db.Close()
		os.Remove(dbPath)
	})

	// Insert two unsynced series
	badTVDBID := 99999
	goodTVDBID := 81189
	_, err = db.Exec(`
		INSERT INTO series (tvdb_id, title, status, total_seasons, created_at, updated_at)
		VALUES (?, 'Bad Show', 'Continuing', 0, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, badTVDBID)
	if err != nil {
		t.Fatalf("failed to seed series: %v", err)
	}
	_, err = db.Exec(`
		INSERT INTO series (tvdb_id, title, status, total_seasons, created_at, updated_at)
		VALUES (?, 'Good Show', 'Continuing', 0, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, goodTVDBID)
	if err != nil {
		t.Fatalf("failed to seed series: %v", err)
	}

	// Create mock TVDB server that returns error for badTVDBID, success for goodTVDBID
	extendedResp := map[string]interface{}{
		"status": "success",
		"data": map[string]interface{}{
			"id":             goodTVDBID,
			"name":           "Good Show",
			"originalName":   "Good Show",
			"slug":           "good-show",
			"overview":       "Successfully synced.",
			"image":          "https://tvdb.com/poster.jpg",
			"status":         map[string]interface{}{"name": "Continuing"},
			"seasons":        []map[string]interface{}{},
			"genres":         []interface{}{},
			"artworks":       []interface{}{},
			"characters":     []interface{}{},
			"contentRatings": []interface{}{},
			"companies":      []interface{}{},
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v4/login", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "success",
			"data":   map[string]string{"token": "test-token"},
		})
	})
	mux.HandleFunc("/v4/series/99999/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("TVDB error"))
	})
	mux.HandleFunc("/v4/series/81189/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case strings.Contains(path, "/translations/"):
			w.WriteHeader(http.StatusNotFound)
		default:
			_ = json.NewEncoder(w).Encode(extendedResp)
		}
	})

	tvdbClient := newTestTVDBServer(t, mux)
	if err := tvdbClient.Login(); err != nil {
		t.Fatalf("failed to login to mock TVDB: %v", err)
	}

	// Should not panic or stop - first series fails, second should still sync
	SyncUnsyncedSeries(db, tvdbClient)

	// Verify the good series was synced despite the bad one failing
	var overview string
	err = db.QueryRow(`SELECT overview FROM series WHERE tvdb_id = ?`, goodTVDBID).Scan(&overview)
	if err != nil {
		t.Fatalf("failed to query good series: %v", err)
	}
	if overview != "Successfully synced." {
		t.Errorf("expected good series overview to be set, got %q", overview)
	}

	// Verify the bad series was NOT synced (overview still NULL)
	var badOverview *string
	err = db.QueryRow(`SELECT overview FROM series WHERE tvdb_id = ?`, badTVDBID).Scan(&badOverview)
	if err != nil {
		t.Fatalf("failed to query bad series: %v", err)
	}
	if badOverview != nil {
		t.Errorf("expected bad series overview to remain NULL, got %q", *badOverview)
	}
}

func TestSyncSeriesMetadata_TVDBError(t *testing.T) {
	// Setup test database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := database.New(dbPath)
	if err != nil {
		t.Fatalf("failed to create test database: %v", err)
	}
	t.Cleanup(func() {
		db.Close()
		os.Remove(dbPath)
	})

	// Seed a series
	tvdbID := 99999
	result, err := db.Exec(`
		INSERT INTO series (tvdb_id, title, status, total_seasons, created_at, updated_at)
		VALUES (?, 'Test Show', 'Continuing', 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, tvdbID)
	if err != nil {
		t.Fatalf("failed to seed series: %v", err)
	}
	seriesID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("failed to get last insert ID: %v", err)
	}

	// Create mock TVDB server that returns errors
	mux := http.NewServeMux()
	mux.HandleFunc("/v4/login", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "success",
			"data":   map[string]string{"token": "test-token"},
		})
	})
	mux.HandleFunc("/v4/series/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("TVDB error"))
	})

	tvdbClient := newTestTVDBServer(t, mux)
	if err := tvdbClient.Login(); err != nil {
		t.Fatalf("failed to login: %v", err)
	}

	// Run sync - should return error
	err = SyncSeriesMetadata(db, tvdbClient, seriesID, tvdbID)
	if err == nil {
		t.Fatal("expected SyncSeriesMetadata to return an error for TVDB failure")
	}

	// Verify series was NOT modified
	var title string
	var totalSeasons int
	err = db.QueryRow(`SELECT title, total_seasons FROM series WHERE id = ?`, seriesID).
		Scan(&title, &totalSeasons)
	if err != nil {
		t.Fatalf("failed to query series: %v", err)
	}
	if title != "Test Show" {
		t.Errorf("expected title unchanged as 'Test Show', got %q", title)
	}
	if totalSeasons != 1 {
		t.Errorf("expected total_seasons unchanged at 1, got %d", totalSeasons)
	}
}
