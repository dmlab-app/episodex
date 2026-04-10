package database

import (
	"testing"
	"time"
)

func TestGetCachedNextSeason_NotFound(t *testing.T) {
	db := newTestDB(t)

	result, err := db.GetCachedNextSeason(999, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil, got %+v", result)
	}
}

func TestSaveAndGetCachedNextSeason(t *testing.T) {
	db := newTestDB(t)

	cache := &NextSeasonCache{
		SeriesID:     1,
		SeasonNumber: 5,
		TrackerURL:   "https://kinozal.tv/details.php?id=12345",
		Title:        "Звёздные врата (5 сезон) / Stargate SG-1",
		Size:         "45.2 GB",
	}

	if err := db.SaveCachedNextSeason(cache); err != nil {
		t.Fatalf("failed to save: %v", err)
	}

	got, err := db.GetCachedNextSeason(1, 5)
	if err != nil {
		t.Fatalf("failed to get: %v", err)
	}
	if got == nil {
		t.Fatal("expected cached entry, got nil")
	}
	if got.TrackerURL != cache.TrackerURL {
		t.Errorf("tracker_url = %q, want %q", got.TrackerURL, cache.TrackerURL)
	}
	if got.Title != cache.Title {
		t.Errorf("title = %q, want %q", got.Title, cache.Title)
	}
	if got.Size != cache.Size {
		t.Errorf("size = %q, want %q", got.Size, cache.Size)
	}
	if got.CachedAt.IsZero() {
		t.Error("cached_at should not be zero")
	}
}

func TestSaveCachedNextSeason_Upsert(t *testing.T) {
	db := newTestDB(t)

	original := &NextSeasonCache{
		SeriesID:     1,
		SeasonNumber: 3,
		TrackerURL:   "https://kinozal.tv/details.php?id=111",
		Title:        "Original Title",
		Size:         "10 GB",
	}
	if err := db.SaveCachedNextSeason(original); err != nil {
		t.Fatalf("failed to save original: %v", err)
	}

	updated := &NextSeasonCache{
		SeriesID:     1,
		SeasonNumber: 3,
		TrackerURL:   "https://kinozal.tv/details.php?id=222",
		Title:        "Updated Title",
		Size:         "20 GB",
	}
	if err := db.SaveCachedNextSeason(updated); err != nil {
		t.Fatalf("failed to save updated: %v", err)
	}

	got, err := db.GetCachedNextSeason(1, 3)
	if err != nil {
		t.Fatalf("failed to get: %v", err)
	}
	if got.TrackerURL != updated.TrackerURL {
		t.Errorf("tracker_url = %q, want %q", got.TrackerURL, updated.TrackerURL)
	}
	if got.Title != updated.Title {
		t.Errorf("title = %q, want %q", got.Title, updated.Title)
	}
	if got.Size != updated.Size {
		t.Errorf("size = %q, want %q", got.Size, updated.Size)
	}
}

func TestGetCachedNextSeason_DifferentSeasons(t *testing.T) {
	db := newTestDB(t)

	s5 := &NextSeasonCache{SeriesID: 1, SeasonNumber: 5, TrackerURL: "url5", Title: "S5", Size: "10 GB"}
	s6 := &NextSeasonCache{SeriesID: 1, SeasonNumber: 6, TrackerURL: "url6", Title: "S6", Size: "12 GB"}

	if err := db.SaveCachedNextSeason(s5); err != nil {
		t.Fatalf("save s5: %v", err)
	}
	if err := db.SaveCachedNextSeason(s6); err != nil {
		t.Fatalf("save s6: %v", err)
	}

	got5, err := db.GetCachedNextSeason(1, 5)
	if err != nil {
		t.Fatalf("get season 5: %v", err)
	}
	got6, err := db.GetCachedNextSeason(1, 6)
	if err != nil {
		t.Fatalf("get season 6: %v", err)
	}

	if got5.Title != "S5" {
		t.Errorf("season 5 title = %q, want S5", got5.Title)
	}
	if got6.Title != "S6" {
		t.Errorf("season 6 title = %q, want S6", got6.Title)
	}
}

func TestClearExpiredCache_RemovesOld(t *testing.T) {
	db := newTestDB(t)

	// Insert entry with old cached_at
	_, err := db.Exec(`
		INSERT INTO next_season_cache (series_id, season_number, tracker_url, title, size, cached_at)
		VALUES (1, 1, 'old_url', 'Old Entry', '5 GB', ?)
	`, time.Now().Add(-8*24*time.Hour))
	if err != nil {
		t.Fatalf("failed to insert old entry: %v", err)
	}

	// Insert fresh entry
	fresh := &NextSeasonCache{SeriesID: 2, SeasonNumber: 1, TrackerURL: "fresh_url", Title: "Fresh", Size: "10 GB"}
	if err := db.SaveCachedNextSeason(fresh); err != nil {
		t.Fatalf("save fresh: %v", err)
	}

	deleted, err := db.ClearExpiredCache(7 * 24 * time.Hour)
	if err != nil {
		t.Fatalf("clear expired: %v", err)
	}
	if deleted != 1 {
		t.Errorf("deleted = %d, want 1", deleted)
	}

	// Old entry gone
	old, err := db.GetCachedNextSeason(1, 1)
	if err != nil {
		t.Fatalf("get old entry: %v", err)
	}
	if old != nil {
		t.Error("expected old entry to be deleted")
	}

	// Fresh entry still there
	got, err := db.GetCachedNextSeason(2, 1)
	if err != nil {
		t.Fatalf("get fresh entry: %v", err)
	}
	if got == nil {
		t.Error("expected fresh entry to remain")
	}
}

func TestClearExpiredCache_NothingToDelete(t *testing.T) {
	db := newTestDB(t)

	deleted, err := db.ClearExpiredCache(7 * 24 * time.Hour)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if deleted != 0 {
		t.Errorf("deleted = %d, want 0", deleted)
	}
}
