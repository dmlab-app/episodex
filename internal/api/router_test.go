package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/episodex/episodex/internal/database"
	"github.com/episodex/episodex/internal/qbittorrent"
	"github.com/episodex/episodex/internal/recommender"
	"github.com/episodex/episodex/internal/tmdb"
	"github.com/episodex/episodex/internal/tracker"
	"github.com/episodex/episodex/internal/tvdb"
)

// setupTestServer creates a Server with a temporary SQLite database for testing.
// An optional tvdb.Client can be passed for tests that need TVDB integration.
func setupTestServer(t *testing.T, tvdbClient ...*tvdb.Client) (*Server, *database.DB) {
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

	var tc *tvdb.Client
	if len(tvdbClient) > 0 {
		tc = tvdbClient[0]
	}
	srv := NewServer(db, nil, tc, nil, "")
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
func seedSeason(t *testing.T, db *database.DB, seriesID int64, seasonNum int, folderPath string, downloaded bool, trackName *string) {
	t.Helper()
	_, err := db.Exec(`
		INSERT INTO seasons (series_id, season_number, folder_path, downloaded, track_name, discovered_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, seriesID, seasonNum, folderPath, downloaded, trackName)
	if err != nil {
		t.Fatalf("failed to seed season: %v", err)
	}
}

// seedSeasonWithEpisodes inserts a season with aired_episodes and max_episode_on_disk set.
// When downloaded=true, max_episode_on_disk = airedEpisodes (user had all episodes).
func seedSeasonWithEpisodes(t *testing.T, db *database.DB, seriesID int64, seasonNum int, folderPath string, downloaded bool, airedEpisodes int) {
	t.Helper()
	maxEpOnDisk := 0
	if downloaded {
		maxEpOnDisk = airedEpisodes
	}
	_, err := db.Exec(`
		INSERT INTO seasons (series_id, season_number, folder_path, downloaded, aired_episodes, max_episode_on_disk, discovered_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, seriesID, seasonNum, folderPath, downloaded, airedEpisodes, maxEpOnDisk)
	if err != nil {
		t.Fatalf("failed to seed season with episodes: %v", err)
	}
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
		downloadedSeasons := int(s["downloaded_seasons"].(float64))

		switch title {
		case "Breaking Bad":
			if downloadedSeasons != 3 {
				t.Errorf("Breaking Bad: expected 3 owned seasons, got %d", downloadedSeasons)
			}
		case "Better Call Saul":
			// Only 1 is owned (downloaded=1)
			if downloadedSeasons != 1 {
				t.Errorf("Better Call Saul: expected 1 owned season, got %d", downloadedSeasons)
			}
		default:
			t.Errorf("unexpected series: %s", title)
		}
	}
}

func TestHandleGetSeries_ReturnsSeasonsWithTrackNames(t *testing.T) {
	srv, db := setupTestServer(t)

	seriesID := seedSeries(t, db, "Breaking Bad", 5)

	lostfilm := "LostFilm"
	seedSeason(t, db, seriesID, 1, "/media/bb/s01", true, &lostfilm)
	seedSeason(t, db, seriesID, 2, "/media/bb/s02", true, nil)

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

	s1 := seasons[0].(map[string]interface{})
	if s1["track_name"] != "LostFilm" {
		t.Errorf("season 1: expected track_name 'LostFilm', got %v", s1["track_name"])
	}

	s2 := seasons[1].(map[string]interface{})
	if _, exists := s2["track_name"]; exists {
		t.Errorf("season 2: expected no track_name, got %v", s2["track_name"])
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

	amedia := "Amedia"

	// Only seasons 1 and 3 are owned
	seedSeason(t, db, seriesID, 1, "/media/bb/s01", true, &amedia)
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
	if s1["downloaded"] != true {
		t.Errorf("season 1: expected downloaded=true, got %v", s1["downloaded"])
	}
	if s1["downloaded"] != true {
		t.Errorf("season 1: expected downloaded=true, got %v", s1["downloaded"])
	}
	if s1["track_name"] != "Amedia" {
		t.Errorf("season 1: expected track_name 'Amedia', got %v", s1["track_name"])
	}

	// Season 2: locked (not owned)
	s2 := seasons[1]
	if s2["downloaded"] != false {
		t.Errorf("season 2: expected downloaded=false, got %v", s2["downloaded"])
	}
	if s2["downloaded"] != false {
		t.Errorf("season 2: expected downloaded=false, got %v", s2["downloaded"])
	}

	// Season 3: owned without voice actor
	s3 := seasons[2]
	if s3["downloaded"] != true {
		t.Errorf("season 3: expected downloaded=true, got %v", s3["downloaded"])
	}
	if s3["downloaded"] != true {
		t.Errorf("season 3: expected downloaded=true, got %v", s3["downloaded"])
	}

	// Season 4: locked
	s4 := seasons[3]
	if s4["downloaded"] != false {
		t.Errorf("season 4: expected downloaded=false, got %v", s4["downloaded"])
	}
	if s4["downloaded"] != false {
		t.Errorf("season 4: expected downloaded=false, got %v", s4["downloaded"])
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
			tvdb_id, title, original_title, overview,
			poster_url, backdrop_url, status,
			year, runtime, rating, content_rating,
			genres, networks,
			total_seasons, created_at, updated_at
		) VALUES (
			81189, 'Во все тяжкие', 'Breaking Bad', 'A chemistry teacher diagnosed with cancer teams up with a former student to manufacture meth.',
			'https://artworks.thetvdb.com/poster.jpg', 'https://artworks.thetvdb.com/backdrop.jpg',
			'Ended',
			2008, 47, 9.5, 'TV-MA',
			'["Drama","Thriller"]', '["AMC"]',
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
		"title":          "Во все тяжкие",
		"original_title": "Breaking Bad",
		"status":         "Ended",
		"content_rating": "TV-MA",
	}

	for key, expected := range checks {
		if result[key] != expected {
			t.Errorf("%s: expected %v, got %v", key, expected, result[key])
		}
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

func TestHandleUpdateSeason_Success(t *testing.T) {
	srv, db := setupTestServer(t)

	seriesID := seedSeries(t, db, "Breaking Bad", 5)
	seedSeason(t, db, seriesID, 1, "/media/bb/s01", true, nil)

	body := `{"track_name": "LostFilm"}`
	req := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/series/%d/seasons/1", seriesID), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var savedTrackName *string
	err := db.QueryRow(`SELECT track_name FROM seasons WHERE series_id = ? AND season_number = 1`, seriesID).Scan(&savedTrackName)
	if err != nil {
		t.Fatalf("failed to query saved track: %v", err)
	}
	if savedTrackName == nil || *savedTrackName != "LostFilm" {
		t.Errorf("expected track_name=LostFilm, got %v", savedTrackName)
	}
}

func TestHandleUpdateSeason_ClearTrack(t *testing.T) {
	srv, db := setupTestServer(t)

	seriesID := seedSeries(t, db, "Breaking Bad", 5)
	lostfilm := "LostFilm"
	seedSeason(t, db, seriesID, 1, "/media/bb/s01", true, &lostfilm)

	body := `{"track_name": null}`
	req := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/series/%d/seasons/1", seriesID), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var savedTrackName *string
	err := db.QueryRow(`SELECT track_name FROM seasons WHERE series_id = ? AND season_number = 1`, seriesID).Scan(&savedTrackName)
	if err != nil {
		t.Fatalf("failed to query saved track: %v", err)
	}
	if savedTrackName != nil {
		t.Errorf("expected track_name=nil, got %v", *savedTrackName)
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
	// Non-owned seasons 4-5 exist in DB from TVDB sync with aired episodes.
	id := seedSeriesWithAired(t, db, "Breaking Bad", 5, 5)
	seedSeasonWithEpisodes(t, db, id, 1, "/media/bb/s01", true, 10)
	seedSeasonWithEpisodes(t, db, id, 2, "/media/bb/s02", true, 13)
	seedSeasonWithEpisodes(t, db, id, 3, "/media/bb/s03", true, 13)
	seedSeasonWithEpisodes(t, db, id, 4, "", false, 13)
	seedSeasonWithEpisodes(t, db, id, 5, "", false, 16)

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
	// new_seasons now contains objects with season_number and aired_episodes
	s4 := newSeasons[0].(map[string]interface{})
	s5 := newSeasons[1].(map[string]interface{})
	if int(s4["season_number"].(float64)) != 4 || int(s4["aired_episodes"].(float64)) != 13 {
		t.Errorf("expected season 4 with 13 episodes, got %v", s4)
	}
	if int(s5["season_number"].(float64)) != 5 || int(s5["aired_episodes"].(float64)) != 16 {
		t.Errorf("expected season 5 with 16 episodes, got %v", s5)
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

	// Series with total_seasons=5, user owns up to season 3.
	// Seasons 4-5 exist but have aired_episodes=0 (unaired) — should not trigger update.
	id := seedSeriesWithAired(t, db, "Future Show", 5, 3)
	seedSeasonWithEpisodes(t, db, id, 1, "/media/fs/s01", true, 10)
	seedSeasonWithEpisodes(t, db, id, 2, "/media/fs/s02", true, 10)
	seedSeasonWithEpisodes(t, db, id, 3, "/media/fs/s03", true, 10)
	seedSeasonWithEpisodes(t, db, id, 4, "", false, 0)
	seedSeasonWithEpisodes(t, db, id, 5, "", false, 0)

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
		t.Errorf("expected 0 updates when future seasons have no aired episodes, got %d", len(updates))
	}
}

func TestHandleGetUpdates_DeletedOldSeasons_NotShownAsNew(t *testing.T) {
	srv, db := setupTestServer(t)

	// Series with 5 aired seasons. User has only season 3 and 5 (deleted 1,2,4).
	// Max owned = 5, no unowned seasons beyond 5. No update should show.
	id := seedSeriesWithAired(t, db, "Gaps Show", 5, 5)
	seedSeasonWithEpisodes(t, db, id, 3, "/media/gs/s03", true, 10)
	seedSeasonWithEpisodes(t, db, id, 5, "/media/gs/s05", true, 10)

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
		t.Errorf("expected 0 updates when max owned season covers aired, got %d", len(updates))
	}
}

func TestHandleGetUpdates_GapsInSeasons_OnlyShowsNewerThanMax(t *testing.T) {
	srv, db := setupTestServer(t)

	// Series with 7 aired seasons. User owns season 1 and 3 (max=3). Seasons 4-7 are new.
	// Non-owned seasons 2, 4-7 exist in DB from TVDB sync with aired episodes.
	id := seedSeriesWithAired(t, db, "Gap Series", 7, 7)
	seedSeasonWithEpisodes(t, db, id, 1, "/media/gap/s01", true, 10)
	seedSeasonWithEpisodes(t, db, id, 2, "", false, 10)
	seedSeasonWithEpisodes(t, db, id, 3, "/media/gap/s03", true, 10)
	seedSeasonWithEpisodes(t, db, id, 4, "", false, 8)
	seedSeasonWithEpisodes(t, db, id, 5, "", false, 12)
	seedSeasonWithEpisodes(t, db, id, 6, "", false, 10)
	seedSeasonWithEpisodes(t, db, id, 7, "", false, 6)

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
	// Seasons 4, 5, 6, 7 are newer than max owned (3) and have aired episodes
	if len(newSeasons) != 4 {
		t.Fatalf("expected 4 new seasons, got %d: %v", len(newSeasons), newSeasons)
	}
	expectedSeasons := []struct{ num, eps int }{{4, 8}, {5, 12}, {6, 10}, {7, 6}}
	for i, exp := range expectedSeasons {
		s := newSeasons[i].(map[string]interface{})
		if int(s["season_number"].(float64)) != exp.num {
			t.Errorf("new_seasons[%d]: expected season %d, got %v", i, exp.num, s["season_number"])
		}
		if int(s["aired_episodes"].(float64)) != exp.eps {
			t.Errorf("new_seasons[%d]: expected %d episodes, got %v", i, exp.eps, s["aired_episodes"])
		}
	}
}

func TestHandleGetUpdates_AllSeasonsOwned_NoUpdates(t *testing.T) {
	srv, db := setupTestServer(t)

	// Series with 3 aired seasons, user owns all 3
	id := seedSeriesWithAired(t, db, "Complete Show", 3, 3)
	seedSeasonWithEpisodes(t, db, id, 1, "/media/cs/s01", true, 10)
	seedSeasonWithEpisodes(t, db, id, 2, "/media/cs/s02", true, 10)
	seedSeasonWithEpisodes(t, db, id, 3, "/media/cs/s03", true, 10)

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
	// season rows exist (no non-owned rows from TVDB sync). Since there are no
	// unowned seasons with aired_episodes > 0 beyond max owned, no update shows.
	id := seedSeriesWithAired(t, db, "Scanner Show", 5, 5)
	seedSeasonWithEpisodes(t, db, id, 1, "/media/ss/s01", true, 10)
	seedSeasonWithEpisodes(t, db, id, 2, "/media/ss/s02", true, 10)
	seedSeasonWithEpisodes(t, db, id, 3, "/media/ss/s03", true, 10)
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

	// With episode-based logic, no unowned seasons with aired_episodes > 0 exist,
	// so the series should NOT appear in updates.
	if len(updates) != 0 {
		t.Errorf("expected 0 updates (no unowned aired seasons), got %d", len(updates))
	}
}

func TestHandleGetUpdates_UnairedSeason_NotInUpdates(t *testing.T) {
	srv, db := setupTestServer(t)

	// Series where user owns S1, S2 exists with aired_episodes=0 (unaired).
	// Should NOT appear in updates because the new season has no aired episodes.
	id := seedSeriesWithAired(t, db, "Unaired Show", 2, 1)
	seedSeasonWithEpisodes(t, db, id, 1, "/media/us/s01", true, 8)
	seedSeasonWithEpisodes(t, db, id, 2, "", false, 0)

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
		t.Errorf("expected 0 updates for season with aired_episodes=0, got %d", len(updates))
	}
}

func TestHandleGetSeries_DownloadedField(t *testing.T) {
	srv, db := setupTestServer(t)

	seriesID := seedSeries(t, db, "Breaking Bad", 5)

	// Season 1: owned (files present)
	seedSeason(t, db, seriesID, 1, "/media/bb/s01", true, nil)
	// Season 2: not owned (files removed)
	seedSeason(t, db, seriesID, 2, "", false, nil)

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

	// Season 1: owned=true
	s1 := seasons[0].(map[string]interface{})
	if s1["downloaded"] != true {
		t.Errorf("season 1: expected downloaded=true, got %v", s1["downloaded"])
	}

	// Season 2: owned=false
	s2 := seasons[1].(map[string]interface{})
	if s2["downloaded"] != false {
		t.Errorf("season 2: expected downloaded=false, got %v", s2["downloaded"])
	}
}

func TestHandleListSeasons_DownloadedField(t *testing.T) {
	srv, db := setupTestServer(t)

	seriesID := seedSeries(t, db, "Breaking Bad", 4)

	// Season 1: owned
	seedSeason(t, db, seriesID, 1, "/media/bb/s01", true, nil)
	// Season 2: not owned (files removed)
	seedSeason(t, db, seriesID, 2, "", false, nil)
	// Season 3: not owned (from TVDB sync)
	seedSeason(t, db, seriesID, 3, "", false, nil)

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

	// Season 1: owned=true
	if seasons[0]["downloaded"] != true {
		t.Errorf("season 1: expected downloaded=true, got %v", seasons[0]["downloaded"])
	}

	// Season 2: owned=false
	if seasons[1]["downloaded"] != false {
		t.Errorf("season 2: expected downloaded=false, got %v", seasons[1]["downloaded"])
	}

	// Season 3: owned=false
	if seasons[2]["downloaded"] != false {
		t.Errorf("season 3: expected downloaded=false, got %v", seasons[2]["downloaded"])
	}

	// Season 4: locked placeholder, owned=false
	if seasons[3]["downloaded"] != false {
		t.Errorf("season 4 (locked): expected downloaded=false, got %v", seasons[3]["downloaded"])
	}
}

func TestHandleGetSeason_DownloadedField(t *testing.T) {
	srv, db := setupTestServer(t)

	seriesID := seedSeries(t, db, "Breaking Bad", 5)
	seedSeason(t, db, seriesID, 1, "/media/bb/s01", true, nil)
	seedSeason(t, db, seriesID, 2, "", false, nil)

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
	if s1["downloaded"] != true {
		t.Errorf("season 1: expected downloaded=true, got %v", s1["downloaded"])
	}

	// Test season 2: not owned
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
	if s2["downloaded"] != false {
		t.Errorf("season 2: expected downloaded=false, got %v", s2["downloaded"])
	}
}

func TestSyncEndpoint_Removed(t *testing.T) {
	srv, db := setupTestServer(t)
	seriesID := seedSeries(t, db, "Breaking Bad", 5)

	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/series/%d/sync", seriesID), http.NoBody)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	// The route no longer exists; chi returns 404 for unmatched path segments
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for removed sync endpoint, got %d", w.Code)
	}
}

func TestRescanEndpoint_Removed(t *testing.T) {
	srv, db := setupTestServer(t)
	seriesID := seedSeries(t, db, "Breaking Bad", 5)
	seedSeason(t, db, seriesID, 1, "/media/bb/s01", true, nil)

	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/series/%d/seasons/1/rescan", seriesID), http.NoBody)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	// The route no longer exists; chi returns 404 for unmatched path segments
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for removed rescan endpoint, got %d", w.Code)
	}
}

// matchTVDBResponses builds the extended and translation response maps for match tests.
func matchTVDBResponses(tvdbID int, name, rusName, overview, rusOverview, image, status string) (extendedResp, translationResp interface{}) {
	extendedResp = map[string]interface{}{
		"status": "success",
		"data": map[string]interface{}{
			"id":           tvdbID,
			"name":         name,
			"originalName": name,
			"slug":         "test-show",
			"overview":     overview,
			"image":        image,
			"status":       map[string]interface{}{"name": status},
			"firstAired":   "2020-01-01",
			"year":         "2020",
			"seasons": []map[string]interface{}{
				{"id": 1001, "number": 1, "type": map[string]interface{}{"name": "Official", "type": "official"}, "name": "Season 1", "year": "2020"},
				{"id": 1002, "number": 2, "type": map[string]interface{}{"name": "Official", "type": "official"}, "name": "Season 2", "year": "2021"},
			},
			"genres":         []interface{}{},
			"artworks":       []interface{}{},
			"characters":     []interface{}{},
			"contentRatings": []interface{}{},
			"companies":      []interface{}{},
		},
	}
	translationResp = map[string]interface{}{
		"status": "success",
		"data": map[string]interface{}{
			"name":     rusName,
			"overview": rusOverview,
			"language": "rus",
		},
	}
	return
}

func TestHandleMatchSeries_Rematch(t *testing.T) {
	tvdbID := 54321
	ext, trans := matchTVDBResponses(tvdbID, "New Show EN", "Новый Сериал", "English overview", "Русское описание", "https://tvdb.com/new.jpg", "Continuing")
	tvdbClient := newTestTVDBServer(t, tvdbMux(ext, trans))
	if err := tvdbClient.Login(); err != nil {
		t.Fatalf("failed to login to mock TVDB: %v", err)
	}

	srv, db := setupTestServer(t, tvdbClient)

	// Seed series with an existing tvdb_id (simulates rematch)
	result, err := db.Exec(`
		INSERT INTO series (tvdb_id, title, original_title, poster_url, status, total_seasons, created_at, updated_at)
		VALUES (11111, 'Old Title', 'Old Original', 'https://tvdb.com/old.jpg', 'Ended', 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`)
	if err != nil {
		t.Fatalf("failed to seed series: %v", err)
	}
	seriesID, _ := result.LastInsertId()

	// Match to new TVDB ID
	body := fmt.Sprintf(`{"tvdb_id": %d}`, tvdbID)
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/series/%d/match", seriesID), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// Verify response has the new tvdb_id
	if int(resp["tvdb_id"].(float64)) != tvdbID {
		t.Errorf("expected tvdb_id=%d, got %v", tvdbID, resp["tvdb_id"])
	}

	// Verify DB was updated with the new tvdb_id
	var dbTVDBId int
	err = db.QueryRow(`SELECT tvdb_id FROM series WHERE id = ?`, seriesID).Scan(&dbTVDBId)
	if err != nil {
		t.Fatalf("failed to query series: %v", err)
	}
	if dbTVDBId != tvdbID {
		t.Errorf("expected DB tvdb_id=%d, got %d", tvdbID, dbTVDBId)
	}

	// Verify SyncSeriesMetadata ran: overview should be set
	var overview *string
	err = db.QueryRow(`SELECT overview FROM series WHERE id = ?`, seriesID).Scan(&overview)
	if err != nil {
		t.Fatalf("failed to query overview: %v", err)
	}
	if overview == nil || *overview == "" {
		t.Error("expected overview to be set after sync")
	}

	// Verify seasons were created by SyncSeriesMetadata
	var seasonCount int
	err = db.QueryRow(`SELECT COUNT(*) FROM seasons WHERE series_id = ?`, seriesID).Scan(&seasonCount)
	if err != nil {
		t.Fatalf("failed to count seasons: %v", err)
	}
	if seasonCount != 2 {
		t.Errorf("expected 2 seasons from sync, got %d", seasonCount)
	}
}

func TestHandleMatchSeries_FirstMatch(t *testing.T) {
	tvdbID := 99887
	ext, trans := matchTVDBResponses(tvdbID, "Brand New Show", "Новое Шоу", "A brand new show", "Новое описание", "https://tvdb.com/brandnew.jpg", "Continuing")
	tvdbClient := newTestTVDBServer(t, tvdbMux(ext, trans))
	if err := tvdbClient.Login(); err != nil {
		t.Fatalf("failed to login to mock TVDB: %v", err)
	}

	srv, db := setupTestServer(t, tvdbClient)

	// Seed series WITHOUT tvdb_id (first match scenario)
	seriesID := seedSeries(t, db, "Unknown Show", 0)

	// Match to TVDB ID
	body := fmt.Sprintf(`{"tvdb_id": %d}`, tvdbID)
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/series/%d/match", seriesID), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// Verify tvdb_id in response
	if int(resp["tvdb_id"].(float64)) != tvdbID {
		t.Errorf("expected tvdb_id=%d, got %v", tvdbID, resp["tvdb_id"])
	}

	// Verify DB was updated
	var dbTVDBId int
	err := db.QueryRow(`SELECT tvdb_id FROM series WHERE id = ?`, seriesID).Scan(&dbTVDBId)
	if err != nil {
		t.Fatalf("failed to query series: %v", err)
	}
	if dbTVDBId != tvdbID {
		t.Errorf("expected DB tvdb_id=%d, got %d", tvdbID, dbTVDBId)
	}

	// Verify title was updated from TVDB
	var title string
	err = db.QueryRow(`SELECT title FROM series WHERE id = ?`, seriesID).Scan(&title)
	if err != nil {
		t.Fatalf("failed to query title: %v", err)
	}
	// GetSeriesDetailsWithRussian uses Russian name as primary
	if title != "Новое Шоу" {
		t.Errorf("expected Russian title 'Новое Шоу', got %q", title)
	}
}

// seedMediaFile inserts a media file record for testing.
func seedMediaFile(t *testing.T, db *database.DB, seriesID int64, seasonNum int, filePath string) {
	t.Helper()
	_, err := db.Exec(`
		INSERT INTO media_files (series_id, season_number, file_path, file_name, file_size, file_hash, mod_time, first_seen, last_checked)
		VALUES (?, ?, ?, ?, 1000, 'abc123', 0, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, seriesID, seasonNum, filePath, filepath.Base(filePath))
	if err != nil {
		t.Fatalf("failed to seed media file: %v", err)
	}
}

func TestHandleDeleteSeries_RemovesFilesAndFolders(t *testing.T) {
	srv, db := setupTestServer(t)

	// Create temp directory structure simulating media library
	mediaDir := t.TempDir()
	srv.mediaPath = mediaDir
	s01Dir := filepath.Join(mediaDir, "Show.S01")
	s02Dir := filepath.Join(mediaDir, "Show.S02")
	if err := os.Mkdir(s01Dir, 0o755); err != nil {
		t.Fatalf("failed to create test dir: %v", err)
	}
	if err := os.Mkdir(s02Dir, 0o755); err != nil {
		t.Fatalf("failed to create test dir: %v", err)
	}

	// Create temp media files
	ep1Path := filepath.Join(s01Dir, "ep1.mkv")
	ep2Path := filepath.Join(s01Dir, "ep2.mkv")
	ep3Path := filepath.Join(s02Dir, "ep3.mkv")
	if err := os.WriteFile(ep1Path, []byte("video1"), 0o644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}
	if err := os.WriteFile(ep2Path, []byte("video2"), 0o644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}
	if err := os.WriteFile(ep3Path, []byte("video3"), 0o644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// Seed DB records
	seriesID := seedSeries(t, db, "Test Show", 2)
	seedSeason(t, db, seriesID, 1, s01Dir, true, nil)
	seedSeason(t, db, seriesID, 2, s02Dir, true, nil)
	seedMediaFile(t, db, seriesID, 1, ep1Path)
	seedMediaFile(t, db, seriesID, 1, ep2Path)
	seedMediaFile(t, db, seriesID, 2, ep3Path)

	// Delete the series
	req := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/series/%d", seriesID), http.NoBody)
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

	// Verify media files were removed from disk
	for _, fp := range []string{ep1Path, ep2Path, ep3Path} {
		if _, err := os.Stat(fp); !os.IsNotExist(err) {
			t.Errorf("expected file %s to be removed", fp)
		}
	}

	// Verify season folders were removed (they should be empty now)
	for _, fp := range []string{s01Dir, s02Dir} {
		if _, err := os.Stat(fp); !os.IsNotExist(err) {
			t.Errorf("expected folder %s to be removed", fp)
		}
	}

	// Verify DB records are gone
	var count int
	db.QueryRow(`SELECT COUNT(*) FROM series WHERE id = ?`, seriesID).Scan(&count)
	if count != 0 {
		t.Errorf("expected series to be deleted from DB, got count=%d", count)
	}
}

func TestHandleDeleteSeries_SucceedsWhenFilesAlreadyMissing(t *testing.T) {
	srv, db := setupTestServer(t)

	// Seed DB records pointing to non-existent files
	seriesID := seedSeries(t, db, "Ghost Show", 1)
	seedSeason(t, db, seriesID, 1, "/nonexistent/path/Show.S01", true, nil)
	seedMediaFile(t, db, seriesID, 1, "/nonexistent/path/Show.S01/ep1.mkv")

	// Delete should still succeed
	req := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/series/%d", seriesID), http.NoBody)
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

	// Verify DB records are gone
	var count int
	db.QueryRow(`SELECT COUNT(*) FROM series WHERE id = ?`, seriesID).Scan(&count)
	if count != 0 {
		t.Errorf("expected series to be deleted from DB, got count=%d", count)
	}
}

func TestHandleDeleteSeries_NotFound(t *testing.T) {
	srv, _ := setupTestServer(t)

	req := httptest.NewRequest(http.MethodDelete, "/api/series/999", http.NoBody)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestIsWithinMediaPath(t *testing.T) {
	srv := &Server{}

	// When mediaPath is empty, everything is allowed
	srv.mediaPath = ""
	if !srv.isWithinMediaPath("/any/path") {
		t.Error("empty mediaPath: expected true for any path")
	}

	// Normal media path
	srv.mediaPath = "/media"
	if !srv.isWithinMediaPath("/media/show/ep.mkv") {
		t.Error("expected /media/show/ep.mkv to be within /media")
	}
	if srv.isWithinMediaPath("/other/file.mkv") {
		t.Error("expected /other/file.mkv to be outside /media")
	}

	// Root media path "/" — should allow all absolute paths
	srv.mediaPath = "/"
	if !srv.isWithinMediaPath("/media/file.mkv") {
		t.Error("root mediaPath: expected /media/file.mkv to be within /")
	}
	if !srv.isWithinMediaPath("/any/deep/path.mkv") {
		t.Error("root mediaPath: expected /any/deep/path.mkv to be within /")
	}
}

func TestHandleDeleteSeries_SkipsFilesOutsideMediaPath(t *testing.T) {
	srv, db := setupTestServer(t)

	// Create two separate directories: one is the "media library", the other is "outside"
	mediaDir := t.TempDir()
	outsideDir := t.TempDir()
	srv.mediaPath = mediaDir

	// Create a file outside the media path
	outsideFile := filepath.Join(outsideDir, "secret.mkv")
	if err := os.WriteFile(outsideFile, []byte("important"), 0o644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// Create a file inside the media path
	insideDir := filepath.Join(mediaDir, "Show.S01")
	if err := os.Mkdir(insideDir, 0o755); err != nil {
		t.Fatalf("failed to create test dir: %v", err)
	}
	insideFile := filepath.Join(insideDir, "ep1.mkv")
	if err := os.WriteFile(insideFile, []byte("video"), 0o644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// Seed DB with both paths
	seriesID := seedSeries(t, db, "Boundary Test", 1)
	seedSeason(t, db, seriesID, 1, insideDir, true, nil)
	seedMediaFile(t, db, seriesID, 1, insideFile)
	seedMediaFile(t, db, seriesID, 1, outsideFile)

	req := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/series/%d", seriesID), http.NoBody)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	// File inside media path should be deleted
	if _, err := os.Stat(insideFile); !os.IsNotExist(err) {
		t.Errorf("expected file inside media path to be removed: %s", insideFile)
	}

	// File outside media path should NOT be deleted
	if _, err := os.Stat(outsideFile); err != nil {
		t.Errorf("expected file outside media path to be preserved: %s", outsideFile)
	}
}

// setupQbitMock creates a mock qBittorrent API server that returns the given
// torrents list and properties by hash. Returns the mock server and a logged-in client.
func setupQbitMock(t *testing.T, torrents []qbittorrent.Torrent, properties map[string]qbittorrent.Properties) *qbittorrent.Client {
	t.Helper()

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "test-session"})
			w.WriteHeader(http.StatusOK)
		case "/api/v2/torrents/info":
			json.NewEncoder(w).Encode(torrents)
		case "/api/v2/torrents/properties":
			hash := r.URL.Query().Get("hash")
			props, ok := properties[hash]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			json.NewEncoder(w).Encode(props)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(mock.Close)

	client := qbittorrent.NewClient(mock.URL, "admin", "pass")
	if err := client.Login(); err != nil {
		t.Fatalf("failed to login to mock qbit: %v", err)
	}
	return client
}

// setupTestServerWithQbit creates a test server with a qBittorrent client attached.
func setupTestServerWithQbit(t *testing.T, qbitClient *qbittorrent.Client) (*Server, *database.DB) {
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

	srv := NewServer(db, nil, nil, qbitClient, "")
	return srv, db
}

func TestHandleGetSeasonTracker_MatchFound(t *testing.T) {
	torrents := []qbittorrent.Torrent{
		{Name: "Breaking.Bad.S01.1080p", SavePath: "/downloads/", Hash: "abc123"},
	}
	properties := map[string]qbittorrent.Properties{
		"abc123": {Comment: "https://tracker.example.com/torrent/12345"},
	}
	qbitClient := setupQbitMock(t, torrents, properties)
	srv, db := setupTestServerWithQbit(t, qbitClient)

	seriesID := seedSeries(t, db, "Breaking Bad", 5)
	seedSeason(t, db, seriesID, 1, "/media/Breaking.Bad.S01.1080p", true, nil)

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/series/%d/seasons/1/tracker", seriesID), http.NoBody)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	trackerURL, ok := resp["tracker_url"].(string)
	if !ok || trackerURL != "https://tracker.example.com/torrent/12345" {
		t.Fatalf("expected tracker_url to be 'https://tracker.example.com/torrent/12345', got %v", resp["tracker_url"])
	}
}

func TestHandleGetSeasonTracker_NoMatch(t *testing.T) {
	torrents := []qbittorrent.Torrent{
		{Name: "Other.Show.S01", SavePath: "/downloads/", Hash: "xyz789"},
	}
	qbitClient := setupQbitMock(t, torrents, nil)
	srv, db := setupTestServerWithQbit(t, qbitClient)

	seriesID := seedSeries(t, db, "Breaking Bad", 5)
	seedSeason(t, db, seriesID, 1, "/media/Breaking.Bad.S01.1080p", true, nil)

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/series/%d/seasons/1/tracker", seriesID), http.NoBody)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["tracker_url"] != nil {
		t.Fatalf("expected tracker_url to be null, got %v", resp["tracker_url"])
	}
}

func TestHandleGetSeasonTracker_QbitNotConfigured(t *testing.T) {
	srv, db := setupTestServer(t)

	seriesID := seedSeries(t, db, "Breaking Bad", 5)
	seedSeason(t, db, seriesID, 1, "/media/Breaking.Bad.S01.1080p", true, nil)

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/series/%d/seasons/1/tracker", seriesID), http.NoBody)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["tracker_url"] != nil {
		t.Fatalf("expected tracker_url to be null when qbit not configured, got %v", resp["tracker_url"])
	}
}

func TestHandleGetSeasonTracker_EmptyComment(t *testing.T) {
	torrents := []qbittorrent.Torrent{
		{Name: "Breaking.Bad.S01.1080p", SavePath: "/downloads/", Hash: "abc123"},
	}
	properties := map[string]qbittorrent.Properties{
		"abc123": {Comment: ""},
	}
	qbitClient := setupQbitMock(t, torrents, properties)
	srv, db := setupTestServerWithQbit(t, qbitClient)

	seriesID := seedSeries(t, db, "Breaking Bad", 5)
	seedSeason(t, db, seriesID, 1, "/media/Breaking.Bad.S01.1080p", true, nil)

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/series/%d/seasons/1/tracker", seriesID), http.NoBody)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["tracker_url"] != nil {
		t.Fatalf("expected tracker_url to be null when comment is empty, got %v", resp["tracker_url"])
	}
}

func TestHandleGetSeasonTracker_SeasonNotFound(t *testing.T) {
	torrents := []qbittorrent.Torrent{}
	qbitClient := setupQbitMock(t, torrents, nil)
	srv, db := setupTestServerWithQbit(t, qbitClient)

	seriesID := seedSeries(t, db, "Breaking Bad", 5)

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/series/%d/seasons/99/tracker", seriesID), http.NoBody)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", w.Code)
	}
}

func TestHandleGetSeasonTracker_NoFolderPath(t *testing.T) {
	torrents := []qbittorrent.Torrent{
		{Name: "Breaking.Bad.S01.1080p", SavePath: "/downloads/", Hash: "abc123"},
	}
	qbitClient := setupQbitMock(t, torrents, nil)
	srv, db := setupTestServerWithQbit(t, qbitClient)

	seriesID := seedSeries(t, db, "Breaking Bad", 5)
	seedSeason(t, db, seriesID, 1, "", false, nil)

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/series/%d/seasons/1/tracker", seriesID), http.NoBody)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["tracker_url"] != nil {
		t.Fatalf("expected tracker_url to be null when folder_path is empty, got %v", resp["tracker_url"])
	}
}

// mockSeasonSearcher implements tracker.SeasonSearcher for testing.
type mockSeasonSearcher struct {
	results map[string]*tracker.SeasonSearchResult
}

func (m *mockSeasonSearcher) FindSeasonTorrent(query string, seasonNumber int) (*tracker.SeasonSearchResult, error) {
	key := fmt.Sprintf("%s_%d", query, seasonNumber)
	if r, ok := m.results[key]; ok {
		return r, nil
	}
	return nil, nil
}

// errorSeasonSearcher always returns an error, simulating transient tracker failures.
type errorSeasonSearcher struct{}

func (m *errorSeasonSearcher) FindSeasonTorrent(_ string, _ int) (*tracker.SeasonSearchResult, error) {
	return nil, fmt.Errorf("kinozal connection refused")
}

func setupTestServerWithSearcher(t *testing.T, searcher tracker.SeasonSearcher) (*Server, *database.DB) {
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

	srv := NewServer(db, nil, nil, nil, "", WithSeasonSearcher(searcher))
	return srv, db
}

func TestHandleGetNextSeasons_WithDownloadedSeasons(t *testing.T) {
	mock := &mockSeasonSearcher{
		results: map[string]*tracker.SeasonSearchResult{
			"Breaking Bad_4": {
				Title:      "Breaking Bad (4 сезон) / 1080p",
				Size:       "45.3 ГБ",
				DetailsURL: "https://kinozal.tv/details.php?id=111",
			},
		},
	}
	srv, db := setupTestServerWithSearcher(t, mock)

	// Series with 5 aired seasons, user has S01-S03 downloaded
	id := seedSeriesWithAired(t, db, "Breaking Bad", 5, 5)
	seedSeasonWithEpisodes(t, db, id, 1, "/media/bb/s01", true, 10)
	seedSeasonWithEpisodes(t, db, id, 2, "/media/bb/s02", true, 10)
	seedSeasonWithEpisodes(t, db, id, 3, "/media/bb/s03", true, 10)

	req := httptest.NewRequest(http.MethodGet, "/api/next-seasons", http.NoBody)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var results []map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&results); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	r := results[0]
	if int(r["next_season"].(float64)) != 4 {
		t.Errorf("expected next_season=4, got %v", r["next_season"])
	}
	if r["tracker_url"] != "https://kinozal.tv/details.php?id=111" {
		t.Errorf("expected tracker_url, got %v", r["tracker_url"])
	}
	if r["torrent_title"] != "Breaking Bad (4 сезон) / 1080p" {
		t.Errorf("expected torrent_title, got %v", r["torrent_title"])
	}
	if r["torrent_size"] != "45.3 ГБ" {
		t.Errorf("expected torrent_size, got %v", r["torrent_size"])
	}
}

func TestHandleGetNextSeasons_NoDownloadedSeasons(t *testing.T) {
	mock := &mockSeasonSearcher{
		results: map[string]*tracker.SeasonSearchResult{
			"New Show_1": {
				Title:      "New Show (1 сезон) / 1080p",
				Size:       "20.0 ГБ",
				DetailsURL: "https://kinozal.tv/details.php?id=222",
			},
		},
	}
	srv, db := setupTestServerWithSearcher(t, mock)

	// Series in library with aired seasons but nothing downloaded
	id := seedSeriesWithAired(t, db, "New Show", 3, 3)
	// No downloaded seasons — but series exists
	_ = id

	req := httptest.NewRequest(http.MethodGet, "/api/next-seasons", http.NoBody)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var results []map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&results); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if int(results[0]["next_season"].(float64)) != 1 {
		t.Errorf("expected next_season=1, got %v", results[0]["next_season"])
	}
}

func TestHandleGetNextSeasons_SkipsUnairedSeasons(t *testing.T) {
	srv, db := setupTestServer(t)

	// Series with 2 aired seasons, user has both — next would be S03 but only 2 aired
	id := seedSeriesWithAired(t, db, "Complete Show", 3, 2)
	seedSeasonWithEpisodes(t, db, id, 1, "/media/cs/s01", true, 10)
	seedSeasonWithEpisodes(t, db, id, 2, "/media/cs/s02", true, 10)

	req := httptest.NewRequest(http.MethodGet, "/api/next-seasons", http.NoBody)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	var results []map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&results); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}

	if len(results) != 0 {
		t.Errorf("expected 0 results (next season not aired), got %d", len(results))
	}
}

func TestHandleGetNextSeasons_FiltersEndedSeries(t *testing.T) {
	srv, db := setupTestServer(t)

	// Ended series — should be excluded
	_, err := db.Exec(`
		INSERT INTO series (title, status, total_seasons, aired_seasons, created_at, updated_at)
		VALUES ('Ended Show', 'Ended', 3, 3, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/next-seasons", http.NoBody)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	var results []map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&results); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}

	if len(results) != 0 {
		t.Errorf("expected 0 results for ended series, got %d", len(results))
	}
}

func TestHandleGetNextSeasons_UsesCache(t *testing.T) {
	// No searcher configured — should still return cached results
	srv, db := setupTestServer(t)

	id := seedSeriesWithAired(t, db, "Cached Show", 5, 5)
	seedSeasonWithEpisodes(t, db, id, 1, "/media/cs/s01", true, 10)
	seedSeasonWithEpisodes(t, db, id, 2, "/media/cs/s02", true, 10)

	// Pre-populate cache
	err := db.SaveCachedNextSeason(&database.NextSeasonCache{
		SeriesID:     id,
		SeasonNumber: 3,
		TrackerURL:   "https://kinozal.tv/details.php?id=333",
		Title:        "Cached Show (3 сезон)",
		Size:         "30.0 ГБ",
	})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/next-seasons", http.NoBody)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	var results []map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&results); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0]["tracker_url"] != "https://kinozal.tv/details.php?id=333" {
		t.Errorf("expected cached tracker_url, got %v", results[0]["tracker_url"])
	}
}

func TestHandleGetNextSeasons_NoSearcherConfigured(t *testing.T) {
	srv, db := setupTestServer(t)

	// Series with next season to find, but no searcher
	id := seedSeriesWithAired(t, db, "No Searcher Show", 5, 5)
	seedSeasonWithEpisodes(t, db, id, 1, "/media/ns/s01", true, 10)

	req := httptest.NewRequest(http.MethodGet, "/api/next-seasons", http.NoBody)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var results []map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&results); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}

	// Should still return the series, just without tracker info
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0]["tracker_url"] != nil {
		t.Errorf("expected no tracker_url without searcher, got %v", results[0]["tracker_url"])
	}
}

func TestHandleGetNextSeasons_FallbackToOriginalTitle(t *testing.T) {
	mock := &mockSeasonSearcher{
		results: map[string]*tracker.SeasonSearchResult{
			// No result for Russian title, but result for original title
			"Stargate SG-1_5": {
				Title:      "Stargate SG-1 Season 5 Complete",
				Size:       "50.0 ГБ",
				DetailsURL: "https://kinozal.tv/details.php?id=444",
			},
		},
	}
	srv, db := setupTestServerWithSearcher(t, mock)

	// Series with Russian title + original title
	result, err := db.Exec(`
		INSERT INTO series (title, original_title, status, total_seasons, aired_seasons, created_at, updated_at)
		VALUES ('Звёздные врата: ЗВ-1', 'Stargate SG-1', 'Continuing', 10, 10, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := result.LastInsertId()
	seedSeasonWithEpisodes(t, db, id, 1, "/media/sg/s01", true, 22)
	seedSeasonWithEpisodes(t, db, id, 2, "/media/sg/s02", true, 22)
	seedSeasonWithEpisodes(t, db, id, 3, "/media/sg/s03", true, 22)
	seedSeasonWithEpisodes(t, db, id, 4, "/media/sg/s04", true, 22)

	req := httptest.NewRequest(http.MethodGet, "/api/next-seasons", http.NoBody)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	var results []map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&results); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0]["tracker_url"] != "https://kinozal.tv/details.php?id=444" {
		t.Errorf("expected fallback tracker_url, got %v", results[0]["tracker_url"])
	}
}

func TestHandleGetNextSeasons_CachesSearchResult(t *testing.T) {
	mock := &mockSeasonSearcher{
		results: map[string]*tracker.SeasonSearchResult{
			"Test Show_2": {
				Title:      "Test Show (2 сезон)",
				Size:       "25.0 ГБ",
				DetailsURL: "https://kinozal.tv/details.php?id=555",
			},
		},
	}
	srv, db := setupTestServerWithSearcher(t, mock)

	id := seedSeriesWithAired(t, db, "Test Show", 5, 5)
	seedSeasonWithEpisodes(t, db, id, 1, "/media/ts/s01", true, 10)

	req := httptest.NewRequest(http.MethodGet, "/api/next-seasons", http.NoBody)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	// Verify result was cached in DB
	cached, err := db.GetCachedNextSeason(id, 2)
	if err != nil {
		t.Fatal(err)
	}
	if cached == nil {
		t.Fatal("expected search result to be cached")
	}
	if cached.TrackerURL != "https://kinozal.tv/details.php?id=555" {
		t.Errorf("expected cached tracker_url, got %s", cached.TrackerURL)
	}
}

func TestHandleGetNextSeasons_NegativeCacheExpiresAfter24h(t *testing.T) {
	mock := &mockSeasonSearcher{
		results: map[string]*tracker.SeasonSearchResult{
			"Recheck Show_2": {
				Title:      "Recheck Show (2 сезон)",
				Size:       "30.0 ГБ",
				DetailsURL: "https://kinozal.tv/details.php?id=777",
			},
		},
	}
	srv, db := setupTestServerWithSearcher(t, mock)

	id := seedSeriesWithAired(t, db, "Recheck Show", 5, 5)
	seedSeasonWithEpisodes(t, db, id, 1, "/media/rc/s01", true, 10)

	// Insert a negative cache entry (empty tracker_url) older than 24 hours
	_, err := db.Exec(`
		INSERT INTO next_season_cache (series_id, season_number, tracker_url, title, size, cached_at)
		VALUES (?, 2, '', '', '', ?)
	`, id, time.Now().Add(-25*time.Hour))
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/next-seasons", http.NoBody)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var results []map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&results); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	// After expired negative cache, the search should have run and found the torrent
	if results[0]["tracker_url"] != "https://kinozal.tv/details.php?id=777" {
		t.Errorf("expected tracker_url from fresh search after expired negative cache, got %v", results[0]["tracker_url"])
	}

	// Verify the cache was updated with the positive result
	cached, err := db.GetCachedNextSeason(id, 2)
	if err != nil {
		t.Fatal(err)
	}
	if cached == nil {
		t.Fatal("expected cache entry after re-search")
	}
	if cached.TrackerURL != "https://kinozal.tv/details.php?id=777" {
		t.Errorf("expected updated cache with tracker_url, got %s", cached.TrackerURL)
	}
}

func TestHandleGetNextSeasons_DoesNotCacheOnError(t *testing.T) {
	srv, db := setupTestServerWithSearcher(t, &errorSeasonSearcher{})

	id := seedSeriesWithAired(t, db, "Error Show", 5, 5)
	seedSeasonWithEpisodes(t, db, id, 1, "/media/es/s01", true, 10)

	req := httptest.NewRequest(http.MethodGet, "/api/next-seasons", http.NoBody)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	// Verify that no cache entry was created for the failed search
	cached, err := db.GetCachedNextSeason(id, 2)
	if err != nil {
		t.Fatal(err)
	}
	if cached != nil {
		t.Errorf("expected no cache entry on search error, but found one: %+v", cached)
	}
}

// Recommendations handlers tests

// setupTestServerWithRecommender returns a server wired with a Recommender
// backed by a no-op TMDB client + searcher. Real Refresh won't call TMDB
// because no series with tvdb_id are seeded unless the test does so itself.
func setupTestServerWithRecommender(t *testing.T) (*Server, *database.DB) {
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

	rec := recommender.New(db, tmdb.NewClient(""), &mockSeasonSearcher{})
	srv := NewServer(db, nil, nil, nil, "", WithRecommender(rec))
	return srv, db
}

func TestHandleGetRecommendations_Empty(t *testing.T) {
	srv, _ := setupTestServerWithRecommender(t)

	req := httptest.NewRequest(http.MethodGet, "/api/recommendations", http.NoBody)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var result []map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected empty array, got %d entries", len(result))
	}
}

// When the feature is disabled (no recommender configured) the handler must
// return an empty array even if the recommendations table has stale rows, so
// the UI degrades gracefully and does not display results for a feature that
// can no longer be refreshed.
func TestHandleGetRecommendations_FeatureDisabled_IgnoresStaleRows(t *testing.T) {
	srv, db := setupTestServer(t)

	if err := db.ReplaceRecommendations([]database.Recommendation{
		{TVDBID: 1001, Title: "Stale", Score: 10, TrackerURL: "https://kinozal.tv/stale"},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/recommendations", http.NoBody)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var result []map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected empty array when feature disabled, got %d entries", len(result))
	}
}

func TestHandleGetRecommendations_ReturnsSeededEntries(t *testing.T) {
	srv, db := setupTestServerWithRecommender(t)

	if err := db.ReplaceRecommendations([]database.Recommendation{
		{
			TVDBID: 1001, TMDBID: 2001, Title: "Top Show", OriginalTitle: "Top Show Orig",
			Overview: "A great show", PosterURL: "https://img/1.jpg", Year: 2020, Rating: 8.5,
			Genres: "[18,10765]", Score: 42, TrackerURL: "https://kinozal.tv/details.php?id=1",
			TorrentTitle: "Top Show S01", TorrentSize: "10 GB",
		},
		{
			TVDBID: 1002, TMDBID: 2002, Title: "Lower Show",
			Score: 10, TrackerURL: "https://kinozal.tv/details.php?id=2",
			Rating: 7.5,
		},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/recommendations", http.NoBody)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var result []map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(result))
	}
	// Ordered by score DESC
	first := result[0]
	if int(first["tvdb_id"].(float64)) != 1001 {
		t.Errorf("expected tvdb_id=1001 first, got %v", first["tvdb_id"])
	}
	if first["title"] != "Top Show" {
		t.Errorf("expected title Top Show, got %v", first["title"])
	}
	if first["original_title"] != "Top Show Orig" {
		t.Errorf("expected original_title, got %v", first["original_title"])
	}
	if first["tracker_url"] != "https://kinozal.tv/details.php?id=1" {
		t.Errorf("expected tracker_url, got %v", first["tracker_url"])
	}
	if first["poster_url"] != "https://img/1.jpg" {
		t.Errorf("expected poster_url, got %v", first["poster_url"])
	}
	if int(first["year"].(float64)) != 2020 {
		t.Errorf("expected year=2020, got %v", first["year"])
	}
	genres, ok := first["genres"].([]interface{})
	if !ok {
		t.Fatalf("expected genres array, got %T", first["genres"])
	}
	if len(genres) != 2 || int(genres[0].(float64)) != 18 {
		t.Errorf("expected genres [18,10765], got %v", genres)
	}
}

func TestHandleRefreshRecommendations_NotConfigured(t *testing.T) {
	srv, _ := setupTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/recommendations/refresh", http.NoBody)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleRefreshRecommendations_Accepted(t *testing.T) {
	srv, _ := setupTestServerWithRecommender(t)

	req := httptest.NewRequest(http.MethodPost, "/api/recommendations/refresh", http.NoBody)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Errorf("expected 202, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleGetBlacklist_Empty(t *testing.T) {
	srv, _ := setupTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/recommendations/blacklist", http.NoBody)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var result []map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected empty array, got %d entries", len(result))
	}
}

func TestHandleGetBlacklist_WithEntries(t *testing.T) {
	srv, db := setupTestServer(t)

	if err := db.AddToBlacklist(555, "Unwanted Show"); err != nil {
		t.Fatalf("seed blacklist: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/recommendations/blacklist", http.NoBody)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var result []map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(result))
	}
	if int(result[0]["tvdb_id"].(float64)) != 555 {
		t.Errorf("expected tvdb_id=555, got %v", result[0]["tvdb_id"])
	}
	if result[0]["title"] != "Unwanted Show" {
		t.Errorf("expected title=Unwanted Show, got %v", result[0]["title"])
	}
}

func TestHandleAddBlacklist_Success(t *testing.T) {
	srv, db := setupTestServer(t)

	body, _ := json.Marshal(map[string]interface{}{"tvdb_id": 777, "title": "Drop This"})
	req := httptest.NewRequest(http.MethodPost, "/api/recommendations/blacklist", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	ids, err := db.GetBlacklistedIDs()
	if err != nil {
		t.Fatalf("get ids: %v", err)
	}
	if !ids[777] {
		t.Errorf("expected 777 blacklisted, got %v", ids)
	}
}

func TestHandleAddBlacklist_AlsoRemovesRecommendation(t *testing.T) {
	srv, db := setupTestServer(t)
	if err := db.ReplaceRecommendations([]database.Recommendation{
		{TVDBID: 888, Title: "Pending", Score: 5, TrackerURL: "x"},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	body, _ := json.Marshal(map[string]interface{}{"tvdb_id": 888, "title": "Pending"})
	req := httptest.NewRequest(http.MethodPost, "/api/recommendations/blacklist", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	recs, err := db.GetRecommendations()
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 0 {
		t.Errorf("expected recommendation removed, got %d entries", len(recs))
	}
}

func TestHandleAddBlacklist_InvalidBody(t *testing.T) {
	srv, _ := setupTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/recommendations/blacklist", strings.NewReader("not json"))
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleAddBlacklist_MissingTVDBID(t *testing.T) {
	srv, _ := setupTestServer(t)

	body, _ := json.Marshal(map[string]interface{}{"title": "No ID"})
	req := httptest.NewRequest(http.MethodPost, "/api/recommendations/blacklist", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleRemoveBlacklist_Success(t *testing.T) {
	srv, db := setupTestServer(t)
	if err := db.AddToBlacklist(444, "Will Unban"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/recommendations/blacklist/444", http.NoBody)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	ids, err := db.GetBlacklistedIDs()
	if err != nil {
		t.Fatal(err)
	}
	if ids[444] {
		t.Errorf("expected 444 removed, still present")
	}
}

func TestHandleRemoveBlacklist_InvalidID(t *testing.T) {
	srv, _ := setupTestServer(t)

	req := httptest.NewRequest(http.MethodDelete, "/api/recommendations/blacklist/notanumber", http.NoBody)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}
