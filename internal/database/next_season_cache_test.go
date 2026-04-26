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

func TestDeleteNextSeasonCacheBySeries(t *testing.T) {
	tests := []struct {
		name      string
		seed      []NextSeasonCache
		seriesID  int64
		wantAfter map[int64]map[int]bool // remaining (series,season) entries
	}{
		{
			name:      "no rows",
			seed:      nil,
			seriesID:  1,
			wantAfter: map[int64]map[int]bool{},
		},
		{
			name: "one row deleted",
			seed: []NextSeasonCache{
				{SeriesID: 1, SeasonNumber: 5, TrackerURL: "u", Title: "t", Size: "s"},
			},
			seriesID:  1,
			wantAfter: map[int64]map[int]bool{},
		},
		{
			name: "multiple seasons of same series deleted",
			seed: []NextSeasonCache{
				{SeriesID: 1, SeasonNumber: 1, TrackerURL: "u", Title: "t", Size: "s"},
				{SeriesID: 1, SeasonNumber: 2, TrackerURL: "u", Title: "t", Size: "s"},
				{SeriesID: 1, SeasonNumber: 3, TrackerURL: "u", Title: "t", Size: "s"},
			},
			seriesID:  1,
			wantAfter: map[int64]map[int]bool{},
		},
		{
			name: "other series untouched",
			seed: []NextSeasonCache{
				{SeriesID: 1, SeasonNumber: 1, TrackerURL: "u", Title: "t", Size: "s"},
				{SeriesID: 2, SeasonNumber: 1, TrackerURL: "u", Title: "t", Size: "s"},
				{SeriesID: 2, SeasonNumber: 2, TrackerURL: "u", Title: "t", Size: "s"},
			},
			seriesID: 1,
			wantAfter: map[int64]map[int]bool{
				2: {1: true, 2: true},
			},
		},
		{
			name:      "missing series no error",
			seed:      nil,
			seriesID:  99999,
			wantAfter: map[int64]map[int]bool{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := newTestDB(t)
			for _, c := range tt.seed {
				c := c
				if err := db.SaveCachedNextSeason(&c); err != nil {
					t.Fatalf("seed: %v", err)
				}
			}

			if err := db.DeleteNextSeasonCacheBySeries(tt.seriesID); err != nil {
				t.Fatalf("delete: %v", err)
			}

			rows, err := db.Query(`SELECT series_id, season_number FROM next_season_cache`)
			if err != nil {
				t.Fatalf("query: %v", err)
			}
			defer rows.Close()
			got := map[int64]map[int]bool{}
			for rows.Next() {
				var sid int64
				var n int
				if err := rows.Scan(&sid, &n); err != nil {
					t.Fatalf("scan: %v", err)
				}
				if got[sid] == nil {
					got[sid] = map[int]bool{}
				}
				got[sid][n] = true
			}

			if len(got) != len(tt.wantAfter) {
				t.Fatalf("series count: got %d, want %d", len(got), len(tt.wantAfter))
			}
			for sid, seasons := range tt.wantAfter {
				if len(got[sid]) != len(seasons) {
					t.Errorf("series %d: got %d seasons, want %d", sid, len(got[sid]), len(seasons))
				}
				for n := range seasons {
					if !got[sid][n] {
						t.Errorf("series %d season %d missing", sid, n)
					}
				}
			}
		})
	}
}

func TestDeleteNextSeasonCacheBySeason(t *testing.T) {
	t.Run("deletes target row, leaves siblings", func(t *testing.T) {
		db := newTestDB(t)
		seed := []NextSeasonCache{
			{SeriesID: 1, SeasonNumber: 1, TrackerURL: "u", Title: "t", Size: "s"},
			{SeriesID: 1, SeasonNumber: 2, TrackerURL: "u", Title: "t", Size: "s"},
			{SeriesID: 2, SeasonNumber: 1, TrackerURL: "u", Title: "t", Size: "s"},
		}
		for _, c := range seed {
			c := c
			if err := db.SaveCachedNextSeason(&c); err != nil {
				t.Fatalf("seed: %v", err)
			}
		}

		if err := db.DeleteNextSeasonCacheBySeason(1, 2); err != nil {
			t.Fatalf("delete: %v", err)
		}

		s1, err := db.GetCachedNextSeason(1, 1)
		if err != nil {
			t.Fatalf("get s1: %v", err)
		}
		if s1 == nil {
			t.Error("series 1 season 1 should remain")
		}

		s2, err := db.GetCachedNextSeason(1, 2)
		if err != nil {
			t.Fatalf("get s2: %v", err)
		}
		if s2 != nil {
			t.Error("series 1 season 2 should be gone")
		}

		other, err := db.GetCachedNextSeason(2, 1)
		if err != nil {
			t.Fatalf("get other: %v", err)
		}
		if other == nil {
			t.Error("series 2 season 1 should remain")
		}
	})

	t.Run("missing row no error", func(t *testing.T) {
		db := newTestDB(t)
		if err := db.DeleteNextSeasonCacheBySeason(999, 7); err != nil {
			t.Fatalf("delete missing: %v", err)
		}
	})
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
