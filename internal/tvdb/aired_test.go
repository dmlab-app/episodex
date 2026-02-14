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

func TestMaxAiredSeasonNumber(t *testing.T) {
	seasons := []SeasonInfo{
		{Number: 1, Year: "2020", Aired: true},
		{Number: 2, Year: "2021", Aired: true},
		{Number: 3, Year: "2025", Aired: true},
		{Number: 4, Year: "2028", Aired: false}, // future
		{Number: 5, Year: "", Aired: false},     // no year
	}

	got := MaxAiredSeasonNumber(seasons)
	if got != 3 {
		t.Errorf("MaxAiredSeasonNumber() = %d, want 3", got)
	}
}

func TestMaxAiredSeasonNumber_Empty(t *testing.T) {
	got := MaxAiredSeasonNumber(nil)
	if got != 0 {
		t.Errorf("MaxAiredSeasonNumber(nil) = %d, want 0", got)
	}

	got = MaxAiredSeasonNumber([]SeasonInfo{})
	if got != 0 {
		t.Errorf("MaxAiredSeasonNumber([]) = %d, want 0", got)
	}
}

func TestMaxAiredSeasonNumber_AllAired(t *testing.T) {
	seasons := []SeasonInfo{
		{Number: 1, Aired: true},
		{Number: 2, Aired: true},
	}
	got := MaxAiredSeasonNumber(seasons)
	if got != 2 {
		t.Errorf("MaxAiredSeasonNumber() = %d, want 2", got)
	}
}

func TestMaxAiredSeasonNumber_NoneAired(t *testing.T) {
	seasons := []SeasonInfo{
		{Number: 1, Aired: false},
		{Number: 2, Aired: false},
	}
	got := MaxAiredSeasonNumber(seasons)
	if got != 0 {
		t.Errorf("MaxAiredSeasonNumber() = %d, want 0", got)
	}
}

func TestMaxAiredSeasonNumber_NonContiguous(t *testing.T) {
	// Seasons numbered 1, 2, 5 (gap at 3, 4) — max should be 5, not count of 3
	seasons := []SeasonInfo{
		{Number: 1, Aired: true},
		{Number: 2, Aired: true},
		{Number: 5, Aired: true},
	}
	got := MaxAiredSeasonNumber(seasons)
	if got != 5 {
		t.Errorf("MaxAiredSeasonNumber() = %d, want 5", got)
	}
}
