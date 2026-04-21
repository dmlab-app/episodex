package database

import (
	"testing"
)

func TestGetRecommendations_Empty(t *testing.T) {
	db := newTestDB(t)

	recs, err := db.GetRecommendations()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(recs) != 0 {
		t.Errorf("expected 0 recommendations, got %d", len(recs))
	}
}

func TestReplaceRecommendations_InsertsAndOrdersByScoreDesc(t *testing.T) {
	db := newTestDB(t)

	recs := []Recommendation{
		{TVDBID: 101, TMDBID: 1001, Title: "Low Score Show", Score: 3.5, Rating: 7.2, Year: 2020, TrackerURL: "https://kinozal.tv/a", TorrentTitle: "a.torrent", TorrentSize: "5 GB", Genres: `["Drama"]`},
		{TVDBID: 102, TMDBID: 1002, Title: "High Score Show", OriginalTitle: "HSS", Overview: "ovv", PosterURL: "https://img/p.jpg", Score: 20.0, Rating: 8.5, Year: 2021, TrackerURL: "https://kinozal.tv/b", TorrentTitle: "b.torrent", TorrentSize: "10 GB", Genres: `["Drama","Sci-Fi"]`},
		{TVDBID: 103, TMDBID: 1003, Title: "Mid Score Show", Score: 10.0, Rating: 7.8, Year: 2019, TrackerURL: "https://kinozal.tv/c", TorrentTitle: "c.torrent", TorrentSize: "7 GB"},
	}
	if err := db.ReplaceRecommendations(recs); err != nil {
		t.Fatalf("failed to insert recommendations: %v", err)
	}

	got, err := db.GetRecommendations()
	if err != nil {
		t.Fatalf("failed to get recommendations: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 recommendations, got %d", len(got))
	}
	wantOrder := []int{102, 103, 101}
	for i, tvdbID := range wantOrder {
		if got[i].TVDBID != tvdbID {
			t.Errorf("position %d: tvdb_id = %d, want %d", i, got[i].TVDBID, tvdbID)
		}
	}

	// Verify all fields survive round-trip for the high-score entry
	hs := got[0]
	if hs.Title != "High Score Show" || hs.OriginalTitle != "HSS" || hs.Overview != "ovv" ||
		hs.PosterURL != "https://img/p.jpg" || hs.Rating != 8.5 || hs.Year != 2021 ||
		hs.TMDBID != 1002 || hs.TrackerURL != "https://kinozal.tv/b" ||
		hs.TorrentTitle != "b.torrent" || hs.TorrentSize != "10 GB" ||
		hs.Genres != `["Drama","Sci-Fi"]` || hs.Score != 20.0 {
		t.Errorf("field mismatch for high-score entry: %+v", hs)
	}
	if hs.CreatedAt.IsZero() {
		t.Error("created_at should not be zero")
	}
}

func TestReplaceRecommendations_ReplacesExisting(t *testing.T) {
	db := newTestDB(t)

	first := []Recommendation{
		{TVDBID: 1, Title: "A", Score: 5, TrackerURL: "https://kinozal.tv/1"},
		{TVDBID: 2, Title: "B", Score: 4, TrackerURL: "https://kinozal.tv/2"},
	}
	if err := db.ReplaceRecommendations(first); err != nil {
		t.Fatalf("first insert: %v", err)
	}

	second := []Recommendation{
		{TVDBID: 3, Title: "C", Score: 10, TrackerURL: "https://kinozal.tv/3"},
	}
	if err := db.ReplaceRecommendations(second); err != nil {
		t.Fatalf("second insert: %v", err)
	}

	got, err := db.GetRecommendations()
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got) != 1 || got[0].TVDBID != 3 {
		t.Errorf("expected only tvdb 3, got %+v", got)
	}
}

func TestReplaceRecommendations_EmptyClears(t *testing.T) {
	db := newTestDB(t)

	if err := db.ReplaceRecommendations([]Recommendation{
		{TVDBID: 1, Title: "A", Score: 5, TrackerURL: "https://kinozal.tv/1"},
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	if err := db.ReplaceRecommendations(nil); err != nil {
		t.Fatalf("replace with nil: %v", err)
	}

	got, err := db.GetRecommendations()
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty, got %d entries", len(got))
	}
}

func TestAddToBlacklist_InsertsAndRemovesRecommendation(t *testing.T) {
	db := newTestDB(t)

	// Seed recommendations with two entries
	recs := []Recommendation{
		{TVDBID: 10, Title: "Keep Me", Score: 5, TrackerURL: "https://kinozal.tv/10"},
		{TVDBID: 20, Title: "Blacklist Me", Score: 8, TrackerURL: "https://kinozal.tv/20"},
	}
	if err := db.ReplaceRecommendations(recs); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := db.AddToBlacklist(20, "Blacklist Me"); err != nil {
		t.Fatalf("add blacklist: %v", err)
	}

	// recommendation 20 should be gone
	got, err := db.GetRecommendations()
	if err != nil {
		t.Fatalf("get recs: %v", err)
	}
	if len(got) != 1 || got[0].TVDBID != 10 {
		t.Errorf("expected only tvdb 10 remaining, got %+v", got)
	}

	// blacklist should contain 20
	bl, err := db.GetBlacklist()
	if err != nil {
		t.Fatalf("get blacklist: %v", err)
	}
	if len(bl) != 1 || bl[0].TVDBID != 20 || bl[0].Title != "Blacklist Me" {
		t.Errorf("unexpected blacklist state: %+v", bl)
	}
	if bl[0].BlacklistedAt.IsZero() {
		t.Error("blacklisted_at should not be zero")
	}
}

func TestAddToBlacklist_IdempotentUpdate(t *testing.T) {
	db := newTestDB(t)

	if err := db.AddToBlacklist(42, "Original Title"); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := db.AddToBlacklist(42, "Updated Title"); err != nil {
		t.Fatalf("re-add: %v", err)
	}

	bl, err := db.GetBlacklist()
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(bl) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(bl))
	}
	if bl[0].Title != "Updated Title" {
		t.Errorf("title = %q, want Updated Title", bl[0].Title)
	}
}

func TestRemoveFromBlacklist(t *testing.T) {
	db := newTestDB(t)

	if err := db.AddToBlacklist(100, "Show"); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := db.RemoveFromBlacklist(100); err != nil {
		t.Fatalf("remove: %v", err)
	}

	bl, err := db.GetBlacklist()
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(bl) != 0 {
		t.Errorf("expected empty blacklist, got %d entries", len(bl))
	}
}

func TestRemoveFromBlacklist_NonExistentNoError(t *testing.T) {
	db := newTestDB(t)

	if err := db.RemoveFromBlacklist(999); err != nil {
		t.Errorf("expected no error removing non-existent, got %v", err)
	}
}

func TestGetBlacklistedIDs(t *testing.T) {
	db := newTestDB(t)

	if err := db.AddToBlacklist(1, "A"); err != nil {
		t.Fatalf("add 1: %v", err)
	}
	if err := db.AddToBlacklist(2, "B"); err != nil {
		t.Fatalf("add 2: %v", err)
	}
	if err := db.AddToBlacklist(3, "C"); err != nil {
		t.Fatalf("add 3: %v", err)
	}

	ids, err := db.GetBlacklistedIDs()
	if err != nil {
		t.Fatalf("get ids: %v", err)
	}
	if len(ids) != 3 {
		t.Errorf("expected 3 ids, got %d", len(ids))
	}
	for _, want := range []int{1, 2, 3} {
		if !ids[want] {
			t.Errorf("id %d missing from blacklisted set", want)
		}
	}
	if ids[999] {
		t.Error("unexpected id 999 in blacklisted set")
	}
}

func TestGetBlacklistedIDs_Empty(t *testing.T) {
	db := newTestDB(t)

	ids, err := db.GetBlacklistedIDs()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("expected empty map, got %d entries", len(ids))
	}
}

// If a show gets blacklisted between a refresh's blacklist snapshot and the
// final ReplaceRecommendations call, the insert must still skip it so the
// blacklisted entry does not race back into the table.
func TestReplaceRecommendations_SkipsBlacklistedAtCommit(t *testing.T) {
	db := newTestDB(t)

	if err := db.AddToBlacklist(42, "Banned"); err != nil {
		t.Fatalf("blacklist: %v", err)
	}

	recs := []Recommendation{
		{TVDBID: 42, Title: "Banned", Score: 10, TrackerURL: "https://kinozal.tv/42"},
		{TVDBID: 43, Title: "Allowed", Score: 8, TrackerURL: "https://kinozal.tv/43"},
	}
	if err := db.ReplaceRecommendations(recs); err != nil {
		t.Fatalf("replace: %v", err)
	}

	got, err := db.GetRecommendations()
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got) != 1 || got[0].TVDBID != 43 {
		t.Errorf("expected only tvdb 43, got %+v", got)
	}
}
