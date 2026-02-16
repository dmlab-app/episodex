package api

import (
	"encoding/json"
	"fmt"
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
	return tvdbMuxWithEpisodes(extendedResp, translationResp, nil)
}

// tvdbMuxWithEpisodes creates a handler that serves mock TVDB API responses including episodes.
func tvdbMuxWithEpisodes(extendedResp, translationResp, episodesResp interface{}) http.Handler {
	mux := http.NewServeMux()

	// Login endpoint
	mux.HandleFunc("/v4/login", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "success",
			"data":   map[string]string{"token": "test-token"},
		})
	})

	// Catch-all for /v4/series/ paths: distinguish extended vs translations vs episodes
	mux.HandleFunc("/v4/series/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case strings.Contains(path, "/translations/"):
			if translationResp != nil {
				_ = json.NewEncoder(w).Encode(translationResp)
			} else {
				w.WriteHeader(http.StatusNotFound)
			}
		case strings.Contains(path, "/episodes/"):
			if episodesResp != nil {
				_ = json.NewEncoder(w).Encode(episodesResp)
			} else {
				// Default: empty episodes response
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"status": "success",
					"data":   map[string]interface{}{"episodes": []interface{}{}},
					"links":  map[string]interface{}{"next": nil},
				})
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

	// Mock episodes: S1 has 7 aired, S2 has 13 aired, S3 has 13 aired,
	// S4 has 13 aired, S5 has 16 aired
	episodesResp := map[string]interface{}{
		"status": "success",
		"data": map[string]interface{}{
			"episodes": []map[string]interface{}{
				{"id": 1, "seasonNumber": 1, "number": 1, "aired": "2008-01-20", "name": "Pilot"},
				{"id": 2, "seasonNumber": 1, "number": 2, "aired": "2008-01-27", "name": "Cat's in the Bag..."},
				{"id": 3, "seasonNumber": 1, "number": 3, "aired": "2008-02-10", "name": "...And the Bag's in the River"},
				{"id": 4, "seasonNumber": 1, "number": 4, "aired": "2008-02-17", "name": "Cancer Man"},
				{"id": 5, "seasonNumber": 1, "number": 5, "aired": "2008-02-24", "name": "Gray Matter"},
				{"id": 6, "seasonNumber": 1, "number": 6, "aired": "2008-03-02", "name": "Crazy Handful of Nothin'"},
				{"id": 7, "seasonNumber": 1, "number": 7, "aired": "2008-03-09", "name": "A No-Rough-Stuff-Type Deal"},
				{"id": 8, "seasonNumber": 2, "number": 1, "aired": "2009-03-08", "name": "Seven Thirty-Seven"},
				{"id": 9, "seasonNumber": 3, "number": 1, "aired": "2010-03-21", "name": "No Más"},
				{"id": 10, "seasonNumber": 4, "number": 1, "aired": "2011-07-17", "name": "Box Cutter"},
				{"id": 11, "seasonNumber": 5, "number": 1, "aired": "2012-07-15", "name": "Live Free or Die"},
			},
		},
		"links": map[string]interface{}{"next": nil},
	}

	tvdbClient := newTestTVDBServer(t, tvdbMuxWithEpisodes(extendedResp, translationResp, episodesResp))
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
	// All 5 seasons have episodes with aired dates in the past
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

	// Verify aired_episodes were stored correctly
	var airedEps int
	err = db.QueryRow(`SELECT aired_episodes FROM seasons WHERE series_id = ? AND season_number = 1`, seriesID).Scan(&airedEps)
	if err != nil {
		t.Fatalf("failed to query season 1: %v", err)
	}
	if airedEps != 7 {
		t.Errorf("expected 7 aired episodes for S1, got %d", airedEps)
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

	// Verify the already-synced series was not modified
	var overview string
	err = db.QueryRow(`SELECT overview FROM series WHERE tvdb_id = 12345`).Scan(&overview)
	if err != nil {
		t.Fatalf("failed to query series: %v", err)
	}
	if overview != "Has overview" {
		t.Errorf("expected overview unchanged as 'Has overview', got %q", overview)
	}
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
			"id":             tvdbID,
			"name":           "Unsynced Show",
			"originalName":   "Unsynced Show",
			"slug":           "unsynced-show",
			"overview":       "Now it has an overview.",
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

func TestSyncSeriesMetadata_AiredEpisodesPerSeason(t *testing.T) {
	// Verify that SyncSeriesMetadata correctly stores aired_episodes per season
	// and computes aired_seasons from episode data (not from year).
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

	tvdbID := 42000
	result, err := db.Exec(`
		INSERT INTO series (tvdb_id, title, status, total_seasons, created_at, updated_at)
		VALUES (?, 'Test Series', 'Continuing', 0, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, tvdbID)
	if err != nil {
		t.Fatalf("failed to seed series: %v", err)
	}
	seriesID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("failed to get last insert ID: %v", err)
	}

	extendedResp := map[string]interface{}{
		"status": "success",
		"data": map[string]interface{}{
			"id":           tvdbID,
			"name":         "Test Series",
			"originalName": "Test Series",
			"slug":         "test-series",
			"overview":     "A test series.",
			"image":        "",
			"status":       map[string]interface{}{"name": "Continuing"},
			"seasons": []map[string]interface{}{
				{"id": 201, "number": 1, "type": map[string]interface{}{"name": "Official", "type": "official"}, "name": "Season 1"},
				{"id": 202, "number": 2, "type": map[string]interface{}{"name": "Official", "type": "official"}, "name": "Season 2"},
				{"id": 203, "number": 3, "type": map[string]interface{}{"name": "Official", "type": "official"}, "name": "Season 3"},
			},
			"genres":         []interface{}{},
			"artworks":       []interface{}{},
			"characters":     []interface{}{},
			"contentRatings": []interface{}{},
			"companies":      []interface{}{},
		},
	}

	// S1: 10 aired episodes, S2: 5 aired episodes, S3: 0 aired episodes (all future)
	episodesResp := map[string]interface{}{
		"status": "success",
		"data": map[string]interface{}{
			"episodes": []map[string]interface{}{
				{"id": 1, "seasonNumber": 1, "number": 1, "aired": "2023-01-10", "name": "E1"},
				{"id": 2, "seasonNumber": 1, "number": 2, "aired": "2023-01-17", "name": "E2"},
				{"id": 3, "seasonNumber": 1, "number": 3, "aired": "2023-01-24", "name": "E3"},
				{"id": 4, "seasonNumber": 1, "number": 4, "aired": "2023-01-31", "name": "E4"},
				{"id": 5, "seasonNumber": 1, "number": 5, "aired": "2023-02-07", "name": "E5"},
				{"id": 6, "seasonNumber": 1, "number": 6, "aired": "2023-02-14", "name": "E6"},
				{"id": 7, "seasonNumber": 1, "number": 7, "aired": "2023-02-21", "name": "E7"},
				{"id": 8, "seasonNumber": 1, "number": 8, "aired": "2023-02-28", "name": "E8"},
				{"id": 9, "seasonNumber": 1, "number": 9, "aired": "2023-03-07", "name": "E9"},
				{"id": 10, "seasonNumber": 1, "number": 10, "aired": "2023-03-14", "name": "E10"},
				{"id": 11, "seasonNumber": 2, "number": 1, "aired": "2024-06-01", "name": "S2E1"},
				{"id": 12, "seasonNumber": 2, "number": 2, "aired": "2024-06-08", "name": "S2E2"},
				{"id": 13, "seasonNumber": 2, "number": 3, "aired": "2024-06-15", "name": "S2E3"},
				{"id": 14, "seasonNumber": 2, "number": 4, "aired": "2024-06-22", "name": "S2E4"},
				{"id": 15, "seasonNumber": 2, "number": 5, "aired": "2024-06-29", "name": "S2E5"},
				{"id": 16, "seasonNumber": 3, "number": 1, "aired": "2099-01-01", "name": "S3E1 Future"},
				{"id": 17, "seasonNumber": 3, "number": 2, "aired": "2099-01-08", "name": "S3E2 Future"},
			},
		},
		"links": map[string]interface{}{"next": nil},
	}

	tvdbClient := newTestTVDBServer(t, tvdbMuxWithEpisodes(extendedResp, nil, episodesResp))
	if err := tvdbClient.Login(); err != nil {
		t.Fatalf("failed to login to mock TVDB: %v", err)
	}

	err = SyncSeriesMetadata(db, tvdbClient, seriesID, tvdbID)
	if err != nil {
		t.Fatalf("SyncSeriesMetadata failed: %v", err)
	}

	// Verify aired_seasons: only S1 and S2 have aired episodes, S3 is future
	var airedSeasons int
	err = db.QueryRow(`SELECT aired_seasons FROM series WHERE id = ?`, seriesID).Scan(&airedSeasons)
	if err != nil {
		t.Fatalf("failed to query series: %v", err)
	}
	if airedSeasons != 2 {
		t.Errorf("expected 2 aired seasons, got %d", airedSeasons)
	}

	// Verify aired_episodes per season
	tests := []struct {
		seasonNum     int
		expectedAired int
	}{
		{1, 10},
		{2, 5},
		{3, 0},
	}
	for _, tc := range tests {
		var airedEps int
		err = db.QueryRow(`SELECT aired_episodes FROM seasons WHERE series_id = ? AND season_number = ?`,
			seriesID, tc.seasonNum).Scan(&airedEps)
		if err != nil {
			t.Fatalf("failed to query season %d: %v", tc.seasonNum, err)
		}
		if airedEps != tc.expectedAired {
			t.Errorf("season %d: expected %d aired episodes, got %d", tc.seasonNum, tc.expectedAired, airedEps)
		}
	}
}

// tvdbEpisodesMux creates a handler that only serves login and episodes endpoints.
// Used for CheckForTVDBUpdates tests which only call GetSeriesEpisodes.
func tvdbEpisodesMux(episodesResp interface{}) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v4/login", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "success",
			"data":   map[string]string{"token": "test-token"},
		})
	})
	mux.HandleFunc("/v4/series/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if strings.Contains(path, "/episodes/") {
			if episodesResp != nil {
				_ = json.NewEncoder(w).Encode(episodesResp)
			} else {
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"status": "success",
					"data":   map[string]interface{}{"episodes": []interface{}{}},
					"links":  map[string]interface{}{"next": nil},
				})
			}
		} else {
			w.WriteHeader(http.StatusInternalServerError)
		}
	})
	return mux
}

func TestCheckForTVDBUpdates_NewAiredSeason(t *testing.T) {
	// Series has S1 with 10 aired episodes in DB.
	// TVDB now returns S1 with 10 and S2 with 5 aired episodes.
	// User watches S1 (max_watched=1). S2 is new -> alert.
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

	tvdbID := 50001
	result, err := db.Exec(`
		INSERT INTO series (tvdb_id, title, status, total_seasons, aired_seasons, created_at, updated_at)
		VALUES (?, 'Test Show', 'Continuing', 1, 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, tvdbID)
	if err != nil {
		t.Fatalf("failed to seed series: %v", err)
	}
	seriesID, _ := result.LastInsertId()

	// S1: watched, 10 aired episodes in DB
	_, err = db.Exec(`
		INSERT INTO seasons (series_id, season_number, is_watched, is_owned, aired_episodes, created_at, updated_at)
		VALUES (?, 1, 1, 1, 10, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, seriesID)
	if err != nil {
		t.Fatalf("failed to seed season: %v", err)
	}

	// TVDB returns S1 (10 eps) + S2 (5 eps), all aired
	episodesResp := map[string]interface{}{
		"status": "success",
		"data": map[string]interface{}{
			"episodes": []map[string]interface{}{
				{"id": 1, "seasonNumber": 1, "number": 1, "aired": "2024-01-01"},
				{"id": 2, "seasonNumber": 1, "number": 2, "aired": "2024-01-08"},
				{"id": 3, "seasonNumber": 1, "number": 3, "aired": "2024-01-15"},
				{"id": 4, "seasonNumber": 1, "number": 4, "aired": "2024-01-22"},
				{"id": 5, "seasonNumber": 1, "number": 5, "aired": "2024-01-29"},
				{"id": 6, "seasonNumber": 1, "number": 6, "aired": "2024-02-05"},
				{"id": 7, "seasonNumber": 1, "number": 7, "aired": "2024-02-12"},
				{"id": 8, "seasonNumber": 1, "number": 8, "aired": "2024-02-19"},
				{"id": 9, "seasonNumber": 1, "number": 9, "aired": "2024-02-26"},
				{"id": 10, "seasonNumber": 1, "number": 10, "aired": "2024-03-04"},
				{"id": 11, "seasonNumber": 2, "number": 1, "aired": "2025-06-01"},
				{"id": 12, "seasonNumber": 2, "number": 2, "aired": "2025-06-08"},
				{"id": 13, "seasonNumber": 2, "number": 3, "aired": "2025-06-15"},
				{"id": 14, "seasonNumber": 2, "number": 4, "aired": "2025-06-22"},
				{"id": 15, "seasonNumber": 2, "number": 5, "aired": "2025-06-29"},
			},
		},
		"links": map[string]interface{}{"next": nil},
	}

	tvdbClient := newTestTVDBServer(t, tvdbEpisodesMux(episodesResp))
	if err := tvdbClient.Login(); err != nil {
		t.Fatalf("failed to login: %v", err)
	}

	checkResult := CheckForTVDBUpdates(db, tvdbClient, false)

	if checkResult.Checked != 1 {
		t.Errorf("expected 1 checked, got %d", checkResult.Checked)
	}
	if checkResult.Updated != 1 {
		t.Errorf("expected 1 updated, got %d", checkResult.Updated)
	}
	if checkResult.Errors != 0 {
		t.Errorf("expected 0 errors, got %d", checkResult.Errors)
	}

	// Verify alert was created for new season S02
	var alertMsg string
	err = db.QueryRow(`SELECT message FROM system_alerts WHERE type = 'new_seasons' AND dismissed = 0`).Scan(&alertMsg)
	if err != nil {
		t.Fatalf("expected alert to be created, got error: %v", err)
	}
	if alertMsg != "Test Show — new season S02" {
		t.Errorf("expected alert 'Test Show — new season S02', got %q", alertMsg)
	}

	// Verify aired_seasons was updated to 2
	var airedSeasons int
	err = db.QueryRow(`SELECT aired_seasons FROM series WHERE id = ?`, seriesID).Scan(&airedSeasons)
	if err != nil {
		t.Fatalf("failed to query series: %v", err)
	}
	if airedSeasons != 2 {
		t.Errorf("expected aired_seasons=2, got %d", airedSeasons)
	}
}

func TestCheckForTVDBUpdates_MidSeasonReturn(t *testing.T) {
	// Series has S1 with 10 aired, S2 with 5 aired in DB.
	// TVDB now shows S2 with 8 aired (3 new episodes = mid-season return).
	// User watches S1 (max_watched=1). Alert for S2 increase.
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

	tvdbID := 50002
	result, err := db.Exec(`
		INSERT INTO series (tvdb_id, title, status, total_seasons, aired_seasons, created_at, updated_at)
		VALUES (?, 'Hiatus Show', 'Continuing', 2, 2, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, tvdbID)
	if err != nil {
		t.Fatalf("failed to seed series: %v", err)
	}
	seriesID, _ := result.LastInsertId()

	// S1: watched, 10 aired
	_, err = db.Exec(`
		INSERT INTO seasons (series_id, season_number, is_watched, is_owned, aired_episodes, created_at, updated_at)
		VALUES (?, 1, 1, 1, 10, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, seriesID)
	if err != nil {
		t.Fatalf("failed to seed S1: %v", err)
	}
	// S2: not watched, 5 aired
	_, err = db.Exec(`
		INSERT INTO seasons (series_id, season_number, is_watched, is_owned, aired_episodes, created_at, updated_at)
		VALUES (?, 2, 0, 0, 5, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, seriesID)
	if err != nil {
		t.Fatalf("failed to seed S2: %v", err)
	}

	// TVDB returns S1 (10 eps) + S2 (8 eps)
	eps := make([]map[string]interface{}, 0, 18)
	for i := 1; i <= 10; i++ {
		eps = append(eps, map[string]interface{}{
			"id": i, "seasonNumber": 1, "number": i,
			"aired": fmt.Sprintf("2024-01-%02d", i),
		})
	}
	for i := 1; i <= 8; i++ {
		eps = append(eps, map[string]interface{}{
			"id": 10 + i, "seasonNumber": 2, "number": i,
			"aired": fmt.Sprintf("2025-01-%02d", i),
		})
	}
	episodesResp := map[string]interface{}{
		"status": "success",
		"data":   map[string]interface{}{"episodes": eps},
		"links":  map[string]interface{}{"next": nil},
	}

	tvdbClient := newTestTVDBServer(t, tvdbEpisodesMux(episodesResp))
	if err := tvdbClient.Login(); err != nil {
		t.Fatalf("failed to login: %v", err)
	}

	checkResult := CheckForTVDBUpdates(db, tvdbClient, false)

	if checkResult.Updated != 1 {
		t.Errorf("expected 1 updated, got %d", checkResult.Updated)
	}

	// Verify alert for mid-season return: 3 new episodes in S02
	var alertMsg string
	err = db.QueryRow(`SELECT message FROM system_alerts WHERE type = 'new_seasons' AND dismissed = 0`).Scan(&alertMsg)
	if err != nil {
		t.Fatalf("expected alert to be created, got error: %v", err)
	}
	if alertMsg != "Hiatus Show — S02: 3 new episodes" {
		t.Errorf("expected alert 'Hiatus Show — S02: 3 new episodes', got %q", alertMsg)
	}

	// Verify aired_episodes was updated in DB
	var airedEps int
	err = db.QueryRow(`SELECT aired_episodes FROM seasons WHERE series_id = ? AND season_number = 2`, seriesID).Scan(&airedEps)
	if err != nil {
		t.Fatalf("failed to query S2: %v", err)
	}
	if airedEps != 8 {
		t.Errorf("expected 8 aired_episodes for S2, got %d", airedEps)
	}
}

func TestCheckForTVDBUpdates_UnchangedEpisodes(t *testing.T) {
	// Series has S1 with 10 aired and S2 with 5 aired in DB.
	// TVDB returns same counts. No alert, no update.
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

	tvdbID := 50003
	result, err := db.Exec(`
		INSERT INTO series (tvdb_id, title, status, total_seasons, aired_seasons, created_at, updated_at)
		VALUES (?, 'Stable Show', 'Ended', 2, 2, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, tvdbID)
	if err != nil {
		t.Fatalf("failed to seed series: %v", err)
	}
	seriesID, _ := result.LastInsertId()

	_, err = db.Exec(`
		INSERT INTO seasons (series_id, season_number, is_watched, is_owned, aired_episodes, created_at, updated_at)
		VALUES (?, 1, 1, 1, 10, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, seriesID)
	if err != nil {
		t.Fatalf("failed to seed S1: %v", err)
	}
	_, err = db.Exec(`
		INSERT INTO seasons (series_id, season_number, is_watched, is_owned, aired_episodes, created_at, updated_at)
		VALUES (?, 2, 1, 1, 5, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, seriesID)
	if err != nil {
		t.Fatalf("failed to seed S2: %v", err)
	}

	// TVDB returns same counts
	eps := make([]map[string]interface{}, 0, 15)
	for i := 1; i <= 10; i++ {
		eps = append(eps, map[string]interface{}{
			"id": i, "seasonNumber": 1, "number": i,
			"aired": fmt.Sprintf("2023-01-%02d", i),
		})
	}
	for i := 1; i <= 5; i++ {
		eps = append(eps, map[string]interface{}{
			"id": 10 + i, "seasonNumber": 2, "number": i,
			"aired": fmt.Sprintf("2024-01-%02d", i),
		})
	}
	episodesResp := map[string]interface{}{
		"status": "success",
		"data":   map[string]interface{}{"episodes": eps},
		"links":  map[string]interface{}{"next": nil},
	}

	tvdbClient := newTestTVDBServer(t, tvdbEpisodesMux(episodesResp))
	if err := tvdbClient.Login(); err != nil {
		t.Fatalf("failed to login: %v", err)
	}

	checkResult := CheckForTVDBUpdates(db, tvdbClient, false)

	if checkResult.Checked != 1 {
		t.Errorf("expected 1 checked, got %d", checkResult.Checked)
	}
	if checkResult.Updated != 0 {
		t.Errorf("expected 0 updated, got %d", checkResult.Updated)
	}

	// Verify no alerts were created
	var alertCount int
	err = db.QueryRow(`SELECT COUNT(*) FROM system_alerts WHERE type = 'new_seasons'`).Scan(&alertCount)
	if err != nil {
		t.Fatalf("failed to count alerts: %v", err)
	}
	if alertCount != 0 {
		t.Errorf("expected 0 alerts, got %d", alertCount)
	}
}

func TestCheckForTVDBUpdates_ResetStaleAiredEpisodes(t *testing.T) {
	// Series has S1 with 10 aired and S2 with 5 aired in DB.
	// TVDB now returns S1 with 10 aired but NO episodes for S2.
	// S2 aired_episodes should be reset to 0.
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

	tvdbID := 50004
	result, err := db.Exec(`
		INSERT INTO series (tvdb_id, title, status, total_seasons, aired_seasons, created_at, updated_at)
		VALUES (?, 'Reset Show', 'Continuing', 2, 2, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, tvdbID)
	if err != nil {
		t.Fatalf("failed to seed series: %v", err)
	}
	seriesID, _ := result.LastInsertId()

	_, err = db.Exec(`
		INSERT INTO seasons (series_id, season_number, is_watched, is_owned, aired_episodes, created_at, updated_at)
		VALUES (?, 1, 1, 1, 10, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, seriesID)
	if err != nil {
		t.Fatalf("failed to seed S1: %v", err)
	}
	_, err = db.Exec(`
		INSERT INTO seasons (series_id, season_number, is_watched, is_owned, aired_episodes, created_at, updated_at)
		VALUES (?, 2, 0, 0, 5, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, seriesID)
	if err != nil {
		t.Fatalf("failed to seed S2: %v", err)
	}

	// TVDB returns only S1 episodes, nothing for S2
	eps := make([]map[string]interface{}, 0, 10)
	for i := 1; i <= 10; i++ {
		eps = append(eps, map[string]interface{}{
			"id": i, "seasonNumber": 1, "number": i,
			"aired": fmt.Sprintf("2023-01-%02d", i),
		})
	}
	episodesResp := map[string]interface{}{
		"status": "success",
		"data":   map[string]interface{}{"episodes": eps},
		"links":  map[string]interface{}{"next": nil},
	}

	tvdbClient := newTestTVDBServer(t, tvdbEpisodesMux(episodesResp))
	if err := tvdbClient.Login(); err != nil {
		t.Fatalf("failed to login: %v", err)
	}

	checkResult := CheckForTVDBUpdates(db, tvdbClient, false)

	if checkResult.Checked != 1 {
		t.Errorf("expected 1 checked, got %d", checkResult.Checked)
	}
	if checkResult.Errors != 0 {
		t.Errorf("expected 0 errors, got %d", checkResult.Errors)
	}

	// Verify S2 aired_episodes was reset to 0
	var s2Aired int
	err = db.QueryRow(`SELECT aired_episodes FROM seasons WHERE series_id = ? AND season_number = 2`, seriesID).Scan(&s2Aired)
	if err != nil {
		t.Fatalf("failed to query S2 aired_episodes: %v", err)
	}
	if s2Aired != 0 {
		t.Errorf("expected S2 aired_episodes = 0 after reset, got %d", s2Aired)
	}

	// Verify S1 aired_episodes unchanged
	var s1Aired int
	err = db.QueryRow(`SELECT aired_episodes FROM seasons WHERE series_id = ? AND season_number = 1`, seriesID).Scan(&s1Aired)
	if err != nil {
		t.Fatalf("failed to query S1 aired_episodes: %v", err)
	}
	if s1Aired != 10 {
		t.Errorf("expected S1 aired_episodes = 10, got %d", s1Aired)
	}

	// Verify aired_seasons was updated from 2 to 1
	var airedSeasons int
	err = db.QueryRow(`SELECT aired_seasons FROM series WHERE id = ?`, seriesID).Scan(&airedSeasons)
	if err != nil {
		t.Fatalf("failed to query aired_seasons: %v", err)
	}
	if airedSeasons != 1 {
		t.Errorf("expected aired_seasons = 1 after S2 reset, got %d", airedSeasons)
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
