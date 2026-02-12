package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/episodex/episodex/internal/database"
)

// setupTestServer creates a Server with a temporary SQLite database for testing.
func setupTestServer(t *testing.T) (*Server, *database.DB) {
	t.Helper()

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

	srv := NewServer(db, nil, nil)
	return srv, db
}

// seedSeries inserts a series and returns its ID.
func seedSeries(t *testing.T, db *database.DB, title string, totalSeasons int) int64 {
	t.Helper()
	result, err := db.Exec(`
		INSERT INTO series (title, status, total_seasons, created_at, updated_at)
		VALUES (?, 'Continuing', ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, title, totalSeasons)
	if err != nil {
		t.Fatalf("failed to seed series: %v", err)
	}
	id, _ := result.LastInsertId()
	return id
}

// seedSeason inserts a season into the seasons table.
func seedSeason(t *testing.T, db *database.DB, seriesID int64, seasonNum int, folderPath string, isOwned bool, voiceActorID *int) {
	t.Helper()
	_, err := db.Exec(`
		INSERT INTO seasons (series_id, season_number, folder_path, is_owned, voice_actor_id, discovered_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, seriesID, seasonNum, folderPath, isOwned, voiceActorID)
	if err != nil {
		t.Fatalf("failed to seed season: %v", err)
	}
}

// getVoiceActorID retrieves a voice actor ID by name.
func getVoiceActorID(t *testing.T, db *database.DB, name string) int {
	t.Helper()
	var id int
	err := db.QueryRow(`SELECT id FROM voice_actors WHERE name = ?`, name).Scan(&id)
	if err != nil {
		t.Fatalf("failed to get voice actor %q: %v", name, err)
	}
	return id
}

func TestHandleListSeries_ReturnsCorrectSeasonCounts(t *testing.T) {
	srv, db := setupTestServer(t)

	// Seed two series
	id1 := seedSeries(t, db, "Breaking Bad", 5)
	id2 := seedSeries(t, db, "Better Call Saul", 6)

	// Add 3 owned seasons for Breaking Bad
	seedSeason(t, db, id1, 1, "/media/bb/s01", true, nil)
	seedSeason(t, db, id1, 2, "/media/bb/s02", true, nil)
	seedSeason(t, db, id1, 3, "/media/bb/s03", true, nil)

	// Add 1 owned season for Better Call Saul, plus 1 non-owned
	seedSeason(t, db, id2, 1, "/media/bcs/s01", true, nil)
	seedSeason(t, db, id2, 2, "", false, nil)

	// Make request
	req := httptest.NewRequest(http.MethodGet, "/api/series", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var result []map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(result) != 2 {
		t.Fatalf("expected 2 series, got %d", len(result))
	}

	// Results are ordered by created_at DESC, so Better Call Saul comes first
	for _, s := range result {
		title := s["title"].(string)
		watchedSeasons := int(s["watched_seasons"].(float64))

		switch title {
		case "Breaking Bad":
			if watchedSeasons != 3 {
				t.Errorf("Breaking Bad: expected 3 watched seasons, got %d", watchedSeasons)
			}
		case "Better Call Saul":
			// Only 1 is owned (is_owned=1)
			if watchedSeasons != 1 {
				t.Errorf("Better Call Saul: expected 1 watched season, got %d", watchedSeasons)
			}
		default:
			t.Errorf("unexpected series: %s", title)
		}
	}
}

func TestHandleGetSeries_ReturnsSeasonsWithVoiceActors(t *testing.T) {
	srv, db := setupTestServer(t)

	seriesID := seedSeries(t, db, "Breaking Bad", 5)

	// Get a voice actor ID from seeded data
	lostfilmID := getVoiceActorID(t, db, "LostFilm")

	// Seed seasons - one with voice actor, one without
	seedSeason(t, db, seriesID, 1, "/media/bb/s01", true, &lostfilmID)
	seedSeason(t, db, seriesID, 2, "/media/bb/s02", true, nil)

	// Make request
	req := httptest.NewRequest(http.MethodGet, "/api/series/1", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var result map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// Check series fields
	if result["title"] != "Breaking Bad" {
		t.Errorf("expected title 'Breaking Bad', got %v", result["title"])
	}

	seasons, ok := result["seasons"].([]interface{})
	if !ok {
		t.Fatalf("expected seasons to be an array, got %T", result["seasons"])
	}

	if len(seasons) != 2 {
		t.Fatalf("expected 2 seasons, got %d", len(seasons))
	}

	// Check season 1 has voice actor
	s1 := seasons[0].(map[string]interface{})
	if s1["voice_actor_name"] != "LostFilm" {
		t.Errorf("season 1: expected voice_actor_name 'LostFilm', got %v", s1["voice_actor_name"])
	}
	if s1["owned"] != true {
		t.Errorf("season 1: expected owned=true, got %v", s1["owned"])
	}

	// Check season 2 has no voice actor
	s2 := seasons[1].(map[string]interface{})
	if _, exists := s2["voice_actor_name"]; exists {
		t.Errorf("season 2: expected no voice_actor_name, got %v", s2["voice_actor_name"])
	}
}

func TestHandleGetSeries_NotFound(t *testing.T) {
	srv, _ := setupTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/series/999", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", w.Code)
	}
}

func TestHandleListSeasons_OwnedVsLocked(t *testing.T) {
	srv, db := setupTestServer(t)

	// Series with 4 total seasons
	seriesID := seedSeries(t, db, "Breaking Bad", 4)

	amediaID := getVoiceActorID(t, db, "Amedia")

	// Only seasons 1 and 3 are owned
	seedSeason(t, db, seriesID, 1, "/media/bb/s01", true, &amediaID)
	seedSeason(t, db, seriesID, 3, "/media/bb/s03", true, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/series/1/seasons", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var seasons []map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&seasons); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// Should have 4 seasons (2 owned + 2 locked)
	if len(seasons) != 4 {
		t.Fatalf("expected 4 seasons, got %d", len(seasons))
	}

	// Season 1: owned with voice actor
	s1 := seasons[0]
	if s1["owned"] != true {
		t.Errorf("season 1: expected owned=true, got %v", s1["owned"])
	}
	if s1["voice_actor_name"] != "Amedia" {
		t.Errorf("season 1: expected voice_actor_name 'Amedia', got %v", s1["voice_actor_name"])
	}

	// Season 2: locked (not owned)
	s2 := seasons[1]
	if s2["owned"] != false {
		t.Errorf("season 2: expected owned=false, got %v", s2["owned"])
	}

	// Season 3: owned without voice actor
	s3 := seasons[2]
	if s3["owned"] != true {
		t.Errorf("season 3: expected owned=true, got %v", s3["owned"])
	}

	// Season 4: locked
	s4 := seasons[3]
	if s4["owned"] != false {
		t.Errorf("season 4: expected owned=false, got %v", s4["owned"])
	}
}

func TestHandleListSeasons_SeriesNotFound(t *testing.T) {
	srv, _ := setupTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/series/999/seasons", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", w.Code)
	}
}
