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
func tvdbMux(extendedResp, translationResp, seasonEpisodesResp interface{}) http.Handler {
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

	// Season episodes endpoint
	mux.HandleFunc("/v4/seasons/", func(w http.ResponseWriter, _ *http.Request) {
		if seasonEpisodesResp != nil {
			_ = json.NewEncoder(w).Encode(seasonEpisodesResp)
		} else {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"status": "success",
				"data":   map[string]interface{}{"episodes": []interface{}{}},
			})
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

	seasonEpisodesResp := map[string]interface{}{
		"status": "success",
		"data": map[string]interface{}{
			"episodes": []map[string]interface{}{
				{"id": 1001, "number": 1, "name": "Pilot", "overview": "First episode", "aired": "2008-01-20", "runtime": 58},
				{"id": 1002, "number": 2, "name": "Cat's in the Bag...", "overview": "Second episode", "aired": "2008-01-27", "runtime": 48},
			},
		},
	}

	tvdbClient := newTestTVDBServer(t, tvdbMux(extendedResp, translationResp, seasonEpisodesResp))
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

	// SyncSeriesMetadata syncs series, seasons, characters, and artworks — not episodes.
	// Verify no episodes were created (episode sync was removed).
	var episodeCount int
	err = db.QueryRow(`
		SELECT COUNT(*) FROM episodes e
		INNER JOIN seasons s ON e.season_id = s.id
		WHERE s.series_id = ?
	`, seriesID).Scan(&episodeCount)
	if err != nil {
		t.Fatalf("failed to count episodes: %v", err)
	}
	if episodeCount != 0 {
		t.Errorf("expected 0 episodes (sync does not create episodes), got %d", episodeCount)
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
