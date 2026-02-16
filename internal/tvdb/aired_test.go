package tvdb

import (
	"testing"
	"time"
)

func TestCountAiredEpisodesBySeason_Empty(t *testing.T) {
	origNow := nowFunc
	nowFunc = func() time.Time { return time.Date(2026, 2, 14, 0, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { nowFunc = origNow })

	got := CountAiredEpisodesBySeason(nil)
	if len(got) != 0 {
		t.Errorf("expected empty map for nil input, got %v", got)
	}

	got = CountAiredEpisodesBySeason([]EpisodeBase{})
	if len(got) != 0 {
		t.Errorf("expected empty map for empty input, got %v", got)
	}
}

func TestCountAiredEpisodesBySeason_AllAired(t *testing.T) {
	origNow := nowFunc
	nowFunc = func() time.Time { return time.Date(2026, 2, 14, 0, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { nowFunc = origNow })

	episodes := []EpisodeBase{
		{SeasonNumber: 1, Number: 1, Aired: "2024-01-15"},
		{SeasonNumber: 1, Number: 2, Aired: "2024-01-22"},
		{SeasonNumber: 2, Number: 1, Aired: "2025-06-01"},
		{SeasonNumber: 2, Number: 2, Aired: "2025-06-08"},
		{SeasonNumber: 2, Number: 3, Aired: "2025-06-15"},
	}

	got := CountAiredEpisodesBySeason(episodes)
	if got[1] != 2 {
		t.Errorf("season 1: expected 2, got %d", got[1])
	}
	if got[2] != 3 {
		t.Errorf("season 2: expected 3, got %d", got[2])
	}
}

func TestCountAiredEpisodesBySeason_AllFuture(t *testing.T) {
	origNow := nowFunc
	nowFunc = func() time.Time { return time.Date(2026, 2, 14, 0, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { nowFunc = origNow })

	episodes := []EpisodeBase{
		{SeasonNumber: 1, Number: 1, Aired: "2027-01-15"},
		{SeasonNumber: 1, Number: 2, Aired: "2027-01-22"},
	}

	got := CountAiredEpisodesBySeason(episodes)
	if len(got) != 0 {
		t.Errorf("expected empty map for all-future episodes, got %v", got)
	}
}

func TestCountAiredEpisodesBySeason_Mix(t *testing.T) {
	origNow := nowFunc
	nowFunc = func() time.Time { return time.Date(2026, 2, 14, 0, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { nowFunc = origNow })

	episodes := []EpisodeBase{
		{SeasonNumber: 1, Number: 1, Aired: "2024-01-15"},
		{SeasonNumber: 1, Number: 2, Aired: "2024-01-22"},
		{SeasonNumber: 2, Number: 1, Aired: "2026-02-01"},
		{SeasonNumber: 2, Number: 2, Aired: "2026-02-14"}, // today — should count
		{SeasonNumber: 2, Number: 3, Aired: "2026-03-01"}, // future
		{SeasonNumber: 2, Number: 4, Aired: ""},           // no date
		{SeasonNumber: 3, Number: 1, Aired: "2027-01-01"}, // all future
	}

	got := CountAiredEpisodesBySeason(episodes)
	if got[1] != 2 {
		t.Errorf("season 1: expected 2, got %d", got[1])
	}
	if got[2] != 2 {
		t.Errorf("season 2: expected 2, got %d", got[2])
	}
	if got[3] != 0 {
		t.Errorf("season 3: expected 0, got %d", got[3])
	}
}

func TestCountAiredEpisodesBySeason_Specials(t *testing.T) {
	origNow := nowFunc
	nowFunc = func() time.Time { return time.Date(2026, 2, 14, 0, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { nowFunc = origNow })

	episodes := []EpisodeBase{
		{SeasonNumber: 0, Number: 1, Aired: "2024-01-15"},
		{SeasonNumber: 0, Number: 2, Aired: "2024-06-01"},
		{SeasonNumber: 1, Number: 1, Aired: "2024-01-15"},
	}

	got := CountAiredEpisodesBySeason(episodes)
	if got[0] != 2 {
		t.Errorf("season 0 (specials): expected 2, got %d", got[0])
	}
	if got[1] != 1 {
		t.Errorf("season 1: expected 1, got %d", got[1])
	}
}
