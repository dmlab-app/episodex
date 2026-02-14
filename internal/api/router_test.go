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
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("failed to get last insert ID: %v", err)
	}
	return id
}

// seedSeason inserts a season into the seasons table.
func seedSeason(t *testing.T, db *database.DB, seriesID int64, seasonNum int, folderPath string, isWatched bool, voiceActorID *int) {
	t.Helper()
	// Default: is_owned follows is_watched for backward-compatible tests
	isOwned := isWatched
	_, err := db.Exec(`
		INSERT INTO seasons (series_id, season_number, folder_path, is_watched, is_owned, voice_actor_id, discovered_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, seriesID, seasonNum, folderPath, isWatched, isOwned, voiceActorID)
	if err != nil {
		t.Fatalf("failed to seed season: %v", err)
	}
}

// seedSeasonWithOwned inserts a season with explicit is_watched and is_owned values.
func seedSeasonWithOwned(t *testing.T, db *database.DB, seriesID int64, seasonNum int, folderPath string, isWatched, isOwned bool) {
	t.Helper()
	_, err := db.Exec(`
		INSERT INTO seasons (series_id, season_number, folder_path, is_watched, is_owned, discovered_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, seriesID, seasonNum, folderPath, isWatched, isOwned)
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
	req := httptest.NewRequest(http.MethodGet, "/api/series", http.NoBody)
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
			// Only 1 is owned (is_watched=1)
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
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/series/%d", seriesID), http.NoBody)
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
	if s1["watched"] != true {
		t.Errorf("season 1: expected owned=true, got %v", s1["watched"])
	}

	// Check season 2 has no voice actor
	s2 := seasons[1].(map[string]interface{})
	if _, exists := s2["voice_actor_name"]; exists {
		t.Errorf("season 2: expected no voice_actor_name, got %v", s2["voice_actor_name"])
	}
}

func TestHandleGetSeries_NotFound(t *testing.T) {
	srv, _ := setupTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/series/999", http.NoBody)
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

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/series/%d/seasons", seriesID), http.NoBody)
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
	if s1["watched"] != true {
		t.Errorf("season 1: expected owned=true, got %v", s1["watched"])
	}
	if s1["voice_actor_name"] != "Amedia" {
		t.Errorf("season 1: expected voice_actor_name 'Amedia', got %v", s1["voice_actor_name"])
	}

	// Season 2: locked (not owned)
	s2 := seasons[1]
	if s2["watched"] != false {
		t.Errorf("season 2: expected owned=false, got %v", s2["watched"])
	}

	// Season 3: owned without voice actor
	s3 := seasons[2]
	if s3["watched"] != true {
		t.Errorf("season 3: expected owned=true, got %v", s3["watched"])
	}

	// Season 4: locked
	s4 := seasons[3]
	if s4["watched"] != false {
		t.Errorf("season 4: expected owned=false, got %v", s4["watched"])
	}
}

func TestHandleListSeasons_SeriesNotFound(t *testing.T) {
	srv, _ := setupTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/series/999/seasons", http.NoBody)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", w.Code)
	}
}

// seedSeriesWithMetadata inserts a series with full metadata and returns its ID.
func seedSeriesWithMetadata(t *testing.T, db *database.DB) int64 {
	t.Helper()
	result, err := db.Exec(`
		INSERT INTO series (
			tvdb_id, title, original_title, slug, overview,
			poster_url, backdrop_url, status, first_aired, last_aired,
			year, runtime, rating, content_rating,
			original_country, original_language,
			genres, networks, studios,
			total_seasons, created_at, updated_at
		) VALUES (
			81189, 'Во все тяжкие', 'Breaking Bad', 'breaking-bad', 'A chemistry teacher diagnosed with cancer teams up with a former student to manufacture meth.',
			'https://artworks.thetvdb.com/poster.jpg', 'https://artworks.thetvdb.com/backdrop.jpg',
			'Ended', '2008-01-20', '2013-09-29',
			2008, 47, 9.5, 'TV-MA',
			'usa', 'eng',
			'["Drama","Thriller"]', '["AMC"]', '["Sony Pictures"]',
			5, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		t.Fatalf("failed to seed series with metadata: %v", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("failed to get last insert ID: %v", err)
	}
	return id
}

// seedCharacters inserts characters for a series.
func seedCharacters(t *testing.T, db *database.DB, seriesID int64) {
	t.Helper()
	chars := []struct {
		name, actor, image string
		sort               int
	}{
		{"Walter White", "Bryan Cranston", "https://img/walter.jpg", 1},
		{"Jesse Pinkman", "Aaron Paul", "https://img/jesse.jpg", 2},
		{"Skyler White", "Anna Gunn", "https://img/skyler.jpg", 3},
	}
	for _, c := range chars {
		_, err := db.Exec(`
			INSERT INTO series_characters (series_id, character_name, actor_name, image_url, sort_order)
			VALUES (?, ?, ?, ?, ?)
		`, seriesID, c.name, c.actor, c.image, c.sort)
		if err != nil {
			t.Fatalf("failed to seed character %s: %v", c.name, err)
		}
	}
}

// seedArtwork inserts an artwork row for a series.
func seedArtwork(t *testing.T, db *database.DB, seriesID int64, artType, url string, score float64) {
	t.Helper()
	_, err := db.Exec(`
		INSERT INTO artworks (series_id, type, url, score, is_primary)
		VALUES (?, ?, ?, ?, 0)
	`, seriesID, artType, url, score)
	if err != nil {
		t.Fatalf("failed to seed artwork: %v", err)
	}
}

func TestHandleGetSeries_ReturnsFullMetadata(t *testing.T) {
	srv, db := setupTestServer(t)

	seriesID := seedSeriesWithMetadata(t, db)
	seedSeason(t, db, seriesID, 1, "/media/bb/s01", true, nil)

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/series/%d", seriesID), http.NoBody)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var result map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// Check all metadata fields
	checks := map[string]interface{}{
		"title":             "Во все тяжкие",
		"original_title":    "Breaking Bad",
		"slug":              "breaking-bad",
		"status":            "Ended",
		"content_rating":    "TV-MA",
		"original_country":  "usa",
		"original_language": "eng",
	}

	for key, expected := range checks {
		if result[key] != expected {
			t.Errorf("%s: expected %v, got %v", key, expected, result[key])
		}
	}

	// Date fields: SQLite may return with time suffix
	firstAired, _ := result["first_aired"].(string)
	if firstAired == "" || firstAired[:10] != "2008-01-20" {
		t.Errorf("first_aired: expected starts with 2008-01-20, got %v", result["first_aired"])
	}
	lastAired, _ := result["last_aired"].(string)
	if lastAired == "" || lastAired[:10] != "2013-09-29" {
		t.Errorf("last_aired: expected starts with 2013-09-29, got %v", result["last_aired"])
	}

	// Numeric checks (JSON numbers are float64)
	if result["year"] != float64(2008) {
		t.Errorf("year: expected 2008, got %v", result["year"])
	}
	if result["runtime"] != float64(47) {
		t.Errorf("runtime: expected 47, got %v", result["runtime"])
	}
	if result["rating"] != float64(9.5) {
		t.Errorf("rating: expected 9.5, got %v", result["rating"])
	}
	if result["tvdb_id"] != float64(81189) {
		t.Errorf("tvdb_id: expected 81189, got %v", result["tvdb_id"])
	}

	// Check overview contains expected text
	overview, ok := result["overview"].(string)
	if !ok || overview == "" {
		t.Error("expected non-empty overview")
	}

	// Check poster and backdrop URLs
	if result["poster_url"] != "https://artworks.thetvdb.com/poster.jpg" {
		t.Errorf("poster_url: expected poster URL, got %v", result["poster_url"])
	}
	if result["backdrop_url"] != "https://artworks.thetvdb.com/backdrop.jpg" {
		t.Errorf("backdrop_url: expected backdrop URL, got %v", result["backdrop_url"])
	}

	// Check JSON array fields are parsed
	genres, ok := result["genres"].([]interface{})
	if !ok || len(genres) != 2 {
		t.Errorf("genres: expected array of 2, got %v", result["genres"])
	} else if genres[0] != "Drama" || genres[1] != "Thriller" {
		t.Errorf("genres: expected [Drama, Thriller], got %v", genres)
	}

	networks, ok := result["networks"].([]interface{})
	if !ok || len(networks) != 1 || networks[0] != "AMC" {
		t.Errorf("networks: expected [AMC], got %v", result["networks"])
	}

	studios, ok := result["studios"].([]interface{})
	if !ok || len(studios) != 1 || studios[0] != "Sony Pictures" {
		t.Errorf("studios: expected [Sony Pictures], got %v", result["studios"])
	}

	// Characters should be empty array (none seeded)
	chars, ok := result["characters"].([]interface{})
	if !ok {
		t.Fatalf("expected characters to be an array, got %T", result["characters"])
	}
	if len(chars) != 0 {
		t.Errorf("expected 0 characters, got %d", len(chars))
	}
}

func TestHandleGetSeries_IncludesCharacters(t *testing.T) {
	srv, db := setupTestServer(t)

	seriesID := seedSeriesWithMetadata(t, db)
	seedCharacters(t, db, seriesID)

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/series/%d", seriesID), http.NoBody)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var result map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	chars, ok := result["characters"].([]interface{})
	if !ok {
		t.Fatalf("expected characters to be an array, got %T", result["characters"])
	}

	if len(chars) != 3 {
		t.Fatalf("expected 3 characters, got %d", len(chars))
	}

	// Characters should be ordered by sort_order
	c1 := chars[0].(map[string]interface{})
	if c1["character_name"] != "Walter White" {
		t.Errorf("first character: expected Walter White, got %v", c1["character_name"])
	}
	if c1["actor_name"] != "Bryan Cranston" {
		t.Errorf("first character actor: expected Bryan Cranston, got %v", c1["actor_name"])
	}
	if c1["image_url"] != "https://img/walter.jpg" {
		t.Errorf("first character image: expected URL, got %v", c1["image_url"])
	}

	c2 := chars[1].(map[string]interface{})
	if c2["character_name"] != "Jesse Pinkman" {
		t.Errorf("second character: expected Jesse Pinkman, got %v", c2["character_name"])
	}
}

func TestHandleGetSeries_ArtworkFallback(t *testing.T) {
	srv, db := setupTestServer(t)

	// Create series WITHOUT poster_url and backdrop_url
	result, err := db.Exec(`
		INSERT INTO series (title, status, total_seasons, created_at, updated_at)
		VALUES ('No Poster Show', 'Continuing', 2, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`)
	if err != nil {
		t.Fatalf("failed to seed series: %v", err)
	}
	seriesID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("failed to get last insert ID: %v", err)
	}

	// Seed artwork as fallback
	seedArtwork(t, db, seriesID, "poster", "https://art/fallback-poster.jpg", 8.0)
	seedArtwork(t, db, seriesID, "background", "https://art/fallback-backdrop.jpg", 7.5)

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/series/%d", seriesID), http.NoBody)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// Should fall back to artwork table
	if resp["poster_url"] != "https://art/fallback-poster.jpg" {
		t.Errorf("poster_url: expected artwork fallback, got %v", resp["poster_url"])
	}
	if resp["backdrop_url"] != "https://art/fallback-backdrop.jpg" {
		t.Errorf("backdrop_url: expected artwork fallback, got %v", resp["backdrop_url"])
	}
}

func TestHandleUpdateSeason_Success(t *testing.T) {
	srv, db := setupTestServer(t)

	seriesID := seedSeries(t, db, "Breaking Bad", 5)
	seedSeason(t, db, seriesID, 1, "/media/bb/s01", true, nil)

	lostfilmID := getVoiceActorID(t, db, "LostFilm")

	body := fmt.Sprintf(`{"voice_actor_id": %d}`, lostfilmID)
	req := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/series/%d/seasons/1", seriesID), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var result map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if result["success"] != true {
		t.Errorf("expected success=true, got %v", result["success"])
	}

	// Verify the voice was actually saved
	var savedVoiceID int
	err := db.QueryRow(`SELECT voice_actor_id FROM seasons WHERE series_id = ? AND season_number = 1`, seriesID).Scan(&savedVoiceID)
	if err != nil {
		t.Fatalf("failed to query saved voice: %v", err)
	}
	if savedVoiceID != lostfilmID {
		t.Errorf("expected voice_actor_id=%d, got %d", lostfilmID, savedVoiceID)
	}
}

func TestHandleUpdateSeason_ClearVoice(t *testing.T) {
	srv, db := setupTestServer(t)

	seriesID := seedSeries(t, db, "Breaking Bad", 5)
	lostfilmID := getVoiceActorID(t, db, "LostFilm")
	seedSeason(t, db, seriesID, 1, "/media/bb/s01", true, &lostfilmID)

	// Clear voice by sending null
	body := `{"voice_actor_id": null}`
	req := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/series/%d/seasons/1", seriesID), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	// Verify voice was cleared
	var savedVoiceID *int
	err := db.QueryRow(`SELECT voice_actor_id FROM seasons WHERE series_id = ? AND season_number = 1`, seriesID).Scan(&savedVoiceID)
	if err != nil {
		t.Fatalf("failed to query saved voice: %v", err)
	}
	if savedVoiceID != nil {
		t.Errorf("expected voice_actor_id=nil, got %d", *savedVoiceID)
	}
}

func TestHandleUpdateSeason_InvalidVoiceID(t *testing.T) {
	srv, db := setupTestServer(t)

	seriesID := seedSeries(t, db, "Breaking Bad", 5)
	seedSeason(t, db, seriesID, 1, "/media/bb/s01", true, nil)

	body := `{"voice_actor_id": 99999}`
	req := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/series/%d/seasons/1", seriesID), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleUpdateSeason_SeasonNotFound(t *testing.T) {
	srv, db := setupTestServer(t)

	seriesID := seedSeries(t, db, "Breaking Bad", 5)

	body := `{"voice_actor_id": 1}`
	req := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/series/%d/seasons/99", seriesID), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleListVoices(t *testing.T) {
	srv, _ := setupTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/voices", http.NoBody)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var voices []map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&voices); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// Should have the 9 seeded voice actors
	if len(voices) != 9 {
		t.Fatalf("expected 9 voices, got %d", len(voices))
	}

	// Verify structure: each voice has id and name
	for _, v := range voices {
		if _, ok := v["id"]; !ok {
			t.Error("voice missing 'id' field")
		}
		if _, ok := v["name"]; !ok {
			t.Error("voice missing 'name' field")
		}
	}

	// Verify alphabetical ordering - check a few known entries
	names := make([]string, len(voices))
	for i, v := range voices {
		names[i] = v["name"].(string)
	}
	// "AlexFilm" should come before "LostFilm" alphabetically
	alexIdx, lostIdx := -1, -1
	for i, n := range names {
		if n == "AlexFilm" {
			alexIdx = i
		}
		if n == "LostFilm" {
			lostIdx = i
		}
	}
	if alexIdx == -1 || lostIdx == -1 {
		t.Error("expected AlexFilm and LostFilm in voice list")
	} else if alexIdx > lostIdx {
		t.Errorf("expected AlexFilm before LostFilm, but AlexFilm at %d, LostFilm at %d", alexIdx, lostIdx)
	}
}

// seedSeriesWithAired inserts a series with total_seasons and aired_seasons and returns its ID.
func seedSeriesWithAired(t *testing.T, db *database.DB, title string, totalSeasons, airedSeasons int) int64 {
	t.Helper()
	result, err := db.Exec(`
		INSERT INTO series (title, status, total_seasons, aired_seasons, created_at, updated_at)
		VALUES (?, 'Continuing', ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, title, totalSeasons, airedSeasons)
	if err != nil {
		t.Fatalf("failed to seed series with aired: %v", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("failed to get last insert ID: %v", err)
	}
	return id
}

func TestHandleGetUpdates_NewAiredSeasonsOnly(t *testing.T) {
	srv, db := setupTestServer(t)

	// Series with 5 aired seasons, user owns up to season 3.
	// Non-owned seasons 4-5 exist in DB from TVDB sync.
	id := seedSeriesWithAired(t, db, "Breaking Bad", 5, 5)
	seedSeason(t, db, id, 1, "/media/bb/s01", true, nil)
	seedSeason(t, db, id, 2, "/media/bb/s02", true, nil)
	seedSeason(t, db, id, 3, "/media/bb/s03", true, nil)
	seedSeason(t, db, id, 4, "", false, nil)
	seedSeason(t, db, id, 5, "", false, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/updates", http.NoBody)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var updates []map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&updates); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(updates) != 1 {
		t.Fatalf("expected 1 update, got %d", len(updates))
	}

	u := updates[0]
	if u["title"] != "Breaking Bad" {
		t.Errorf("expected title 'Breaking Bad', got %v", u["title"])
	}

	newSeasons, ok := u["new_seasons"].([]interface{})
	if !ok {
		t.Fatalf("expected new_seasons to be an array, got %T", u["new_seasons"])
	}
	if len(newSeasons) != 2 {
		t.Fatalf("expected 2 new seasons, got %d: %v", len(newSeasons), newSeasons)
	}
	if int(newSeasons[0].(float64)) != 4 || int(newSeasons[1].(float64)) != 5 {
		t.Errorf("expected new seasons [4, 5], got %v", newSeasons)
	}
}

func TestHandleGetUpdates_NoOwnedSeasons_Excluded(t *testing.T) {
	srv, db := setupTestServer(t)

	// Series with aired seasons but user has no owned seasons at all
	seedSeriesWithAired(t, db, "New Show", 3, 3)

	req := httptest.NewRequest(http.MethodGet, "/api/updates", http.NoBody)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var updates []map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&updates); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(updates) != 0 {
		t.Errorf("expected 0 updates for series with no owned seasons, got %d", len(updates))
	}
}

func TestHandleGetUpdates_UnairedFutureSeasons_Excluded(t *testing.T) {
	srv, db := setupTestServer(t)

	// Series with total_seasons=5 but only aired_seasons=3, user owns up to season 3
	// Even though TVDB lists 5 seasons, only 3 have aired - no update should show
	id := seedSeriesWithAired(t, db, "Future Show", 5, 3)
	seedSeason(t, db, id, 1, "/media/fs/s01", true, nil)
	seedSeason(t, db, id, 2, "/media/fs/s02", true, nil)
	seedSeason(t, db, id, 3, "/media/fs/s03", true, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/updates", http.NoBody)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var updates []map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&updates); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(updates) != 0 {
		t.Errorf("expected 0 updates when aired_seasons <= max_watched, got %d", len(updates))
	}
}

func TestHandleGetUpdates_DeletedOldSeasons_NotShownAsNew(t *testing.T) {
	srv, db := setupTestServer(t)

	// Series with 5 aired seasons. User has only season 3 and 5 (deleted 1,2,4).
	// Max owned = 5, aired = 5, so no update should show.
	id := seedSeriesWithAired(t, db, "Gaps Show", 5, 5)
	seedSeason(t, db, id, 3, "/media/gs/s03", true, nil)
	seedSeason(t, db, id, 5, "/media/gs/s05", true, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/updates", http.NoBody)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var updates []map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&updates); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// Max owned is 5, aired is 5, so no updates
	if len(updates) != 0 {
		t.Errorf("expected 0 updates when max owned season covers aired, got %d", len(updates))
	}
}

func TestHandleGetUpdates_GapsInSeasons_OnlyShowsNewerThanMax(t *testing.T) {
	srv, db := setupTestServer(t)

	// Series with 7 aired seasons. User owns season 1 and 3 (max=3). Seasons 4-7 are new.
	// Non-owned seasons 2, 4-7 exist in DB from TVDB sync.
	id := seedSeriesWithAired(t, db, "Gap Series", 7, 7)
	seedSeason(t, db, id, 1, "/media/gap/s01", true, nil)
	seedSeason(t, db, id, 2, "", false, nil)
	seedSeason(t, db, id, 3, "/media/gap/s03", true, nil)
	seedSeason(t, db, id, 4, "", false, nil)
	seedSeason(t, db, id, 5, "", false, nil)
	seedSeason(t, db, id, 6, "", false, nil)
	seedSeason(t, db, id, 7, "", false, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/updates", http.NoBody)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var updates []map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&updates); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(updates) != 1 {
		t.Fatalf("expected 1 update, got %d", len(updates))
	}

	newSeasons, ok := updates[0]["new_seasons"].([]interface{})
	if !ok {
		t.Fatalf("expected new_seasons to be an array, got %T", updates[0]["new_seasons"])
	}
	// Seasons 4, 5, 6, 7 are newer than max owned (3)
	if len(newSeasons) != 4 {
		t.Fatalf("expected 4 new seasons, got %d: %v", len(newSeasons), newSeasons)
	}
	expected := []int{4, 5, 6, 7}
	for i, exp := range expected {
		if int(newSeasons[i].(float64)) != exp {
			t.Errorf("new_seasons[%d]: expected %d, got %v", i, exp, newSeasons[i])
		}
	}
}

func TestHandleGetUpdates_AllSeasonsOwned_NoUpdates(t *testing.T) {
	srv, db := setupTestServer(t)

	// Series with 3 aired seasons, user owns all 3
	id := seedSeriesWithAired(t, db, "Complete Show", 3, 3)
	seedSeason(t, db, id, 1, "/media/cs/s01", true, nil)
	seedSeason(t, db, id, 2, "/media/cs/s02", true, nil)
	seedSeason(t, db, id, 3, "/media/cs/s03", true, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/updates", http.NoBody)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var updates []map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&updates); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(updates) != 0 {
		t.Errorf("expected 0 updates when all seasons owned, got %d", len(updates))
	}
}

func TestHandleGetUpdates_ScannerCreatedNoNonOwnedRows(t *testing.T) {
	srv, db := setupTestServer(t)

	// Simulates scanner-created series: aired_seasons is set high but only owned
	// season rows exist (no non-owned rows from TVDB sync). The series should still
	// appear in updates but with empty new_seasons (no phantom numbers).
	id := seedSeriesWithAired(t, db, "Scanner Show", 5, 5)
	seedSeason(t, db, id, 1, "/media/ss/s01", true, nil)
	seedSeason(t, db, id, 2, "/media/ss/s02", true, nil)
	seedSeason(t, db, id, 3, "/media/ss/s03", true, nil)
	// No non-owned rows for seasons 4-5 (scanner doesn't create them)

	req := httptest.NewRequest(http.MethodGet, "/api/updates", http.NoBody)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var updates []map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&updates); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(updates) != 1 {
		t.Fatalf("expected 1 update, got %d", len(updates))
	}

	// new_seasons should be empty since no non-owned rows exist
	newSeasons, ok := updates[0]["new_seasons"].([]interface{})
	if !ok {
		t.Fatalf("expected new_seasons to be an array, got %T", updates[0]["new_seasons"])
	}
	if len(newSeasons) != 0 {
		t.Errorf("expected 0 new seasons (no non-owned rows), got %d: %v", len(newSeasons), newSeasons)
	}
}

func TestHandleGetSeries_IsOwnedField(t *testing.T) {
	srv, db := setupTestServer(t)

	seriesID := seedSeries(t, db, "Breaking Bad", 5)

	// Season 1: was watched, currently owned (files present)
	seedSeasonWithOwned(t, db, seriesID, 1, "/media/bb/s01", true, true)
	// Season 2: was watched, no longer owned (files removed)
	seedSeasonWithOwned(t, db, seriesID, 2, "", true, false)

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/series/%d", seriesID), http.NoBody)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var result map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	seasons, ok := result["seasons"].([]interface{})
	if !ok || len(seasons) != 2 {
		t.Fatalf("expected 2 seasons, got %v", result["seasons"])
	}

	// Season 1: watched=true, owned=true
	s1 := seasons[0].(map[string]interface{})
	if s1["watched"] != true {
		t.Errorf("season 1: expected watched=true, got %v", s1["watched"])
	}
	if s1["owned"] != true {
		t.Errorf("season 1: expected owned=true, got %v", s1["owned"])
	}

	// Season 2: watched=true, owned=false
	s2 := seasons[1].(map[string]interface{})
	if s2["watched"] != true {
		t.Errorf("season 2: expected watched=true, got %v", s2["watched"])
	}
	if s2["owned"] != false {
		t.Errorf("season 2: expected owned=false, got %v", s2["owned"])
	}
}

func TestHandleListSeasons_IsOwnedField(t *testing.T) {
	srv, db := setupTestServer(t)

	seriesID := seedSeries(t, db, "Breaking Bad", 4)

	// Season 1: watched + owned
	seedSeasonWithOwned(t, db, seriesID, 1, "/media/bb/s01", true, true)
	// Season 2: watched but not owned (files removed)
	seedSeasonWithOwned(t, db, seriesID, 2, "", true, false)
	// Season 3: not watched, not owned (from TVDB sync)
	seedSeasonWithOwned(t, db, seriesID, 3, "", false, false)

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/series/%d/seasons", seriesID), http.NoBody)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var seasons []map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&seasons); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// Should have 4 seasons (3 from DB + 1 locked placeholder for season 4)
	if len(seasons) != 4 {
		t.Fatalf("expected 4 seasons, got %d", len(seasons))
	}

	// Season 1: watched=true, owned=true
	if seasons[0]["watched"] != true || seasons[0]["owned"] != true {
		t.Errorf("season 1: expected watched=true owned=true, got watched=%v owned=%v", seasons[0]["watched"], seasons[0]["owned"])
	}

	// Season 2: watched=true, owned=false
	if seasons[1]["watched"] != true || seasons[1]["owned"] != false {
		t.Errorf("season 2: expected watched=true owned=false, got watched=%v owned=%v", seasons[1]["watched"], seasons[1]["owned"])
	}

	// Season 3: watched=false, owned=false
	if seasons[2]["watched"] != false || seasons[2]["owned"] != false {
		t.Errorf("season 3: expected watched=false owned=false, got watched=%v owned=%v", seasons[2]["watched"], seasons[2]["owned"])
	}

	// Season 4: locked placeholder, watched=false, owned=false
	if seasons[3]["watched"] != false || seasons[3]["owned"] != false {
		t.Errorf("season 4 (locked): expected watched=false owned=false, got watched=%v owned=%v", seasons[3]["watched"], seasons[3]["owned"])
	}
}

func TestHandleGetSeason_IsOwnedField(t *testing.T) {
	srv, db := setupTestServer(t)

	seriesID := seedSeries(t, db, "Breaking Bad", 5)
	seedSeasonWithOwned(t, db, seriesID, 1, "/media/bb/s01", true, true)
	seedSeasonWithOwned(t, db, seriesID, 2, "", true, false)

	// Test season 1: owned
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/series/%d/seasons/1", seriesID), http.NoBody)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var s1 map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&s1); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if s1["owned"] != true {
		t.Errorf("season 1: expected owned=true, got %v", s1["owned"])
	}
	if s1["watched"] != true {
		t.Errorf("season 1: expected watched=true, got %v", s1["watched"])
	}

	// Test season 2: not owned but watched
	req2 := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/series/%d/seasons/2", seriesID), http.NoBody)
	w2 := httptest.NewRecorder()
	srv.router.ServeHTTP(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w2.Code, w2.Body.String())
	}

	var s2 map[string]interface{}
	if err := json.NewDecoder(w2.Body).Decode(&s2); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if s2["owned"] != false {
		t.Errorf("season 2: expected owned=false, got %v", s2["owned"])
	}
	if s2["watched"] != true {
		t.Errorf("season 2: expected watched=true, got %v", s2["watched"])
	}
}
