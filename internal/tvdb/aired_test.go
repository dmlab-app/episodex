package tvdb

import (
	"testing"
	"time"
)

func TestIsSeasonAired(t *testing.T) {
	// Fix nowFunc to a known date for deterministic tests
	origNow := nowFunc
	nowFunc = func() time.Time {
		return time.Date(2026, 2, 14, 0, 0, 0, 0, time.UTC)
	}
	t.Cleanup(func() { nowFunc = origNow })

	tests := []struct {
		name     string
		year     string
		expected bool
	}{
		{"past year", "2020", true},
		{"current year", "2026", true},
		{"future year", "2027", false},
		{"empty year", "", false},
		{"invalid year", "abc", false},
		{"zero year", "0", false},
		{"negative year", "-1", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isSeasonAired(tt.year)
			if result != tt.expected {
				t.Errorf("isSeasonAired(%q) = %v, want %v", tt.year, result, tt.expected)
			}
		})
	}
}

func TestCountAiredSeasons(t *testing.T) {
	seasons := []SeasonInfo{
		{Number: 1, Year: "2020", Aired: true},
		{Number: 2, Year: "2021", Aired: true},
		{Number: 3, Year: "2025", Aired: true},
		{Number: 4, Year: "2028", Aired: false}, // future
		{Number: 5, Year: "", Aired: false},     // no year
	}

	count := CountAiredSeasons(seasons)
	if count != 3 {
		t.Errorf("CountAiredSeasons() = %d, want 3", count)
	}
}

func TestCountAiredSeasons_Empty(t *testing.T) {
	count := CountAiredSeasons(nil)
	if count != 0 {
		t.Errorf("CountAiredSeasons(nil) = %d, want 0", count)
	}

	count = CountAiredSeasons([]SeasonInfo{})
	if count != 0 {
		t.Errorf("CountAiredSeasons([]) = %d, want 0", count)
	}
}

func TestCountAiredSeasons_AllAired(t *testing.T) {
	seasons := []SeasonInfo{
		{Number: 1, Aired: true},
		{Number: 2, Aired: true},
	}
	count := CountAiredSeasons(seasons)
	if count != 2 {
		t.Errorf("CountAiredSeasons() = %d, want 2", count)
	}
}

func TestCountAiredSeasons_NoneAired(t *testing.T) {
	seasons := []SeasonInfo{
		{Number: 1, Aired: false},
		{Number: 2, Aired: false},
	}
	count := CountAiredSeasons(seasons)
	if count != 0 {
		t.Errorf("CountAiredSeasons() = %d, want 0", count)
	}
}
