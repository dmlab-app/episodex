package database

import (
	"testing"
)

func TestGetMediaFilePathsBySeriesID(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(t *testing.T, db *DB) int64
		wantPaths []string
	}{
		{
			name: "series with files",
			setup: func(t *testing.T, db *DB) int64 {
				t.Helper()
				seriesID := createTestSeries(t, db)
				createTestSeason(t, db, seriesID, 1, "")
				insertMediaFile(t, db, seriesID, 1, "/mnt/media/Show.S01/ep1.mkv")
				insertMediaFile(t, db, seriesID, 1, "/mnt/media/Show.S01/ep2.mkv")
				return seriesID
			},
			wantPaths: []string{"/mnt/media/Show.S01/ep1.mkv", "/mnt/media/Show.S01/ep2.mkv"},
		},
		{
			name: "series with no files",
			setup: func(t *testing.T, db *DB) int64 {
				t.Helper()
				return createTestSeries(t, db)
			},
			wantPaths: nil,
		},
		{
			name: "non-existent series",
			setup: func(t *testing.T, _ *DB) int64 {
				t.Helper()
				return 99999
			},
			wantPaths: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := newTestDB(t)
			seriesID := tt.setup(t, db)

			paths, err := db.GetMediaFilePathsBySeriesID(seriesID)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(paths) != len(tt.wantPaths) {
				t.Fatalf("expected %d paths, got %d", len(tt.wantPaths), len(paths))
			}
			for i, want := range tt.wantPaths {
				if paths[i] != want {
					t.Errorf("path[%d]: expected %q, got %q", i, want, paths[i])
				}
			}
		})
	}
}

func createTestSeason(t *testing.T, db *DB, seriesID int64, seasonNumber int, folderPath string) {
	t.Helper()
	var fp *string
	if folderPath != "" {
		fp = &folderPath
	}
	_, err := db.UpsertSeason(&Season{
		SeriesID:     seriesID,
		SeasonNumber: seasonNumber,
		FolderPath:   fp,
	})
	if err != nil {
		t.Fatalf("failed to create test season: %v", err)
	}
}

func insertMediaFile(t *testing.T, db *DB, seriesID int64, seasonNumber int, filePath string) {
	t.Helper()
	err := db.UpsertMediaFile(&MediaFile{
		SeriesID:     seriesID,
		SeasonNumber: seasonNumber,
		FilePath:     filePath,
		FileName:     filePath,
		FileSize:     1000,
		FileHash:     "abc123",
	})
	if err != nil {
		t.Fatalf("failed to insert media file: %v", err)
	}
}
