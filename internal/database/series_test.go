package database

import (
	"os"
	"path/filepath"
	"testing"
)

func newTestDB(t *testing.T) *DB {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := New(dbPath)
	if err != nil {
		t.Fatalf("failed to create test database: %v", err)
	}
	t.Cleanup(func() {
		db.Close()
		os.Remove(dbPath)
	})
	return db
}

func TestGetUnsyncedSeries_EmptyDB(t *testing.T) {
	db := newTestDB(t)

	series, err := db.GetUnsyncedSeries()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(series) != 0 {
		t.Errorf("expected 0 series, got %d", len(series))
	}
}

func TestGetUnsyncedSeries_NoTVDBID(t *testing.T) {
	db := newTestDB(t)

	// Series without tvdb_id should not be returned
	_, err := db.Exec(`
		INSERT INTO series (title, created_at, updated_at)
		VALUES ('No TVDB', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`)
	if err != nil {
		t.Fatalf("failed to insert series: %v", err)
	}

	series, err := db.GetUnsyncedSeries()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(series) != 0 {
		t.Errorf("expected 0 series (no tvdb_id), got %d", len(series))
	}
}

func TestGetUnsyncedSeries_WithOverview(t *testing.T) {
	db := newTestDB(t)

	// Series with tvdb_id AND overview should not be returned (already synced)
	_, err := db.Exec(`
		INSERT INTO series (tvdb_id, title, overview, created_at, updated_at)
		VALUES (12345, 'Synced Show', 'Some overview', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`)
	if err != nil {
		t.Fatalf("failed to insert series: %v", err)
	}

	series, err := db.GetUnsyncedSeries()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(series) != 0 {
		t.Errorf("expected 0 series (has overview), got %d", len(series))
	}
}

func TestGetUnsyncedSeries_Unsynced(t *testing.T) {
	db := newTestDB(t)

	// Series with tvdb_id but no overview should be returned
	_, err := db.Exec(`
		INSERT INTO series (tvdb_id, title, created_at, updated_at)
		VALUES (67890, 'Unsynced Show', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`)
	if err != nil {
		t.Fatalf("failed to insert series: %v", err)
	}

	// Also add a fully synced series to make sure it's excluded
	_, err = db.Exec(`
		INSERT INTO series (tvdb_id, title, overview, created_at, updated_at)
		VALUES (11111, 'Synced Show', 'Has overview', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`)
	if err != nil {
		t.Fatalf("failed to insert synced series: %v", err)
	}

	// And a series without tvdb_id
	_, err = db.Exec(`
		INSERT INTO series (title, created_at, updated_at)
		VALUES ('No TVDB', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`)
	if err != nil {
		t.Fatalf("failed to insert no-tvdb series: %v", err)
	}

	series, err := db.GetUnsyncedSeries()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(series) != 1 {
		t.Fatalf("expected 1 unsynced series, got %d", len(series))
	}

	s := series[0]
	if s.ID == 0 {
		t.Errorf("expected non-zero ID")
	}
	if s.Title != "Unsynced Show" {
		t.Errorf("expected title 'Unsynced Show', got %q", s.Title)
	}
	if s.TVDBId == nil || *s.TVDBId != 67890 {
		t.Errorf("expected tvdb_id 67890, got %v", s.TVDBId)
	}
}

func createTestSeries(t *testing.T, db *DB) int64 {
	t.Helper()
	result, err := db.Exec(`
		INSERT INTO series (tvdb_id, title, created_at, updated_at)
		VALUES (99999, 'Test Show', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`)
	if err != nil {
		t.Fatalf("failed to insert series: %v", err)
	}
	id, _ := result.LastInsertId()
	return id
}

func TestGetSeasonFolderPaths(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(t *testing.T, db *DB) int64
		wantPaths []string
	}{
		{
			name: "series with folder paths",
			setup: func(t *testing.T, db *DB) int64 {
				t.Helper()
				seriesID := createTestSeries(t, db)
				fp1 := "/mnt/media/Show.S01"
				fp2 := "/mnt/media/Show.S02"
				if _, err := db.UpsertSeason(&Season{SeriesID: seriesID, SeasonNumber: 1, FolderPath: &fp1}); err != nil {
					t.Fatalf("failed to upsert season: %v", err)
				}
				if _, err := db.UpsertSeason(&Season{SeriesID: seriesID, SeasonNumber: 2, FolderPath: &fp2}); err != nil {
					t.Fatalf("failed to upsert season: %v", err)
				}
				return seriesID
			},
			wantPaths: []string{"/mnt/media/Show.S01", "/mnt/media/Show.S02"},
		},
		{
			name: "seasons without folder_path",
			setup: func(t *testing.T, db *DB) int64 {
				t.Helper()
				seriesID := createTestSeries(t, db)
				// Season with NULL folder_path
				if _, err := db.UpsertSeason(&Season{SeriesID: seriesID, SeasonNumber: 1}); err != nil {
					t.Fatalf("failed to upsert season: %v", err)
				}
				// Season with empty folder_path
				empty := ""
				if _, err := db.UpsertSeason(&Season{SeriesID: seriesID, SeasonNumber: 2, FolderPath: &empty}); err != nil {
					t.Fatalf("failed to upsert season: %v", err)
				}
				return seriesID
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

			paths, err := db.GetSeasonFolderPaths(seriesID)
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

func TestGetTorrentHashesBySeries(t *testing.T) {
	tests := []struct {
		name       string
		setup      func(t *testing.T, db *DB) int64
		wantHashes []string
	}{
		{
			name: "no seasons",
			setup: func(t *testing.T, db *DB) int64 {
				t.Helper()
				return createTestSeries(t, db)
			},
			wantHashes: nil,
		},
		{
			name: "one season with hash",
			setup: func(t *testing.T, db *DB) int64 {
				t.Helper()
				seriesID := createTestSeries(t, db)
				if _, err := db.UpsertSeason(&Season{SeriesID: seriesID, SeasonNumber: 1}); err != nil {
					t.Fatalf("upsert: %v", err)
				}
				if _, err := db.Exec(`UPDATE seasons SET torrent_hash = ? WHERE series_id = ? AND season_number = ?`, "hashA", seriesID, 1); err != nil {
					t.Fatalf("set hash: %v", err)
				}
				return seriesID
			},
			wantHashes: []string{"hashA"},
		},
		{
			name: "multiple seasons with hashes ordered by season_number",
			setup: func(t *testing.T, db *DB) int64 {
				t.Helper()
				seriesID := createTestSeries(t, db)
				for _, n := range []int{1, 2, 3} {
					if _, err := db.UpsertSeason(&Season{SeriesID: seriesID, SeasonNumber: n}); err != nil {
						t.Fatalf("upsert: %v", err)
					}
				}
				if _, err := db.Exec(`UPDATE seasons SET torrent_hash = ? WHERE series_id = ? AND season_number = ?`, "hashC", seriesID, 3); err != nil {
					t.Fatalf("set hash: %v", err)
				}
				if _, err := db.Exec(`UPDATE seasons SET torrent_hash = ? WHERE series_id = ? AND season_number = ?`, "hashA", seriesID, 1); err != nil {
					t.Fatalf("set hash: %v", err)
				}
				if _, err := db.Exec(`UPDATE seasons SET torrent_hash = ? WHERE series_id = ? AND season_number = ?`, "hashB", seriesID, 2); err != nil {
					t.Fatalf("set hash: %v", err)
				}
				return seriesID
			},
			wantHashes: []string{"hashA", "hashB", "hashC"},
		},
		{
			name: "null and empty hashes filtered out",
			setup: func(t *testing.T, db *DB) int64 {
				t.Helper()
				seriesID := createTestSeries(t, db)
				for _, n := range []int{1, 2, 3} {
					if _, err := db.UpsertSeason(&Season{SeriesID: seriesID, SeasonNumber: n}); err != nil {
						t.Fatalf("upsert: %v", err)
					}
				}
				if _, err := db.Exec(`UPDATE seasons SET torrent_hash = ? WHERE series_id = ? AND season_number = ?`, "", seriesID, 2); err != nil {
					t.Fatalf("set hash: %v", err)
				}
				if _, err := db.Exec(`UPDATE seasons SET torrent_hash = ? WHERE series_id = ? AND season_number = ?`, "hashA", seriesID, 3); err != nil {
					t.Fatalf("set hash: %v", err)
				}
				return seriesID
			},
			wantHashes: []string{"hashA"},
		},
		{
			name: "non-existent series",
			setup: func(t *testing.T, _ *DB) int64 {
				t.Helper()
				return 99999
			},
			wantHashes: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := newTestDB(t)
			seriesID := tt.setup(t, db)

			hashes, err := db.GetTorrentHashesBySeries(seriesID)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(hashes) != len(tt.wantHashes) {
				t.Fatalf("expected %d hashes, got %d (%v)", len(tt.wantHashes), len(hashes), hashes)
			}
			for i, want := range tt.wantHashes {
				if hashes[i] != want {
					t.Errorf("hash[%d]: expected %q, got %q", i, want, hashes[i])
				}
			}
		})
	}
}

func TestDeleteSeason(t *testing.T) {
	t.Run("deletes season and cascades media_files", func(t *testing.T) {
		db := newTestDB(t)
		seriesID := createTestSeries(t, db)

		if _, err := db.UpsertSeason(&Season{SeriesID: seriesID, SeasonNumber: 1}); err != nil {
			t.Fatalf("upsert: %v", err)
		}
		if _, err := db.UpsertSeason(&Season{SeriesID: seriesID, SeasonNumber: 2}); err != nil {
			t.Fatalf("upsert: %v", err)
		}
		if _, err := db.Exec(`
			INSERT INTO media_files (series_id, season_number, file_path, file_name, file_size, file_hash)
			VALUES (?, 1, '/tmp/s1e1.mkv', 's1e1.mkv', 100, 'h1'),
			       (?, 1, '/tmp/s1e2.mkv', 's1e2.mkv', 200, 'h2'),
			       (?, 2, '/tmp/s2e1.mkv', 's2e1.mkv', 300, 'h3')
		`, seriesID, seriesID, seriesID); err != nil {
			t.Fatalf("insert media files: %v", err)
		}

		affected, err := db.DeleteSeason(seriesID, 1)
		if err != nil {
			t.Fatalf("delete season: %v", err)
		}
		if affected != 1 {
			t.Errorf("expected 1 row affected, got %d", affected)
		}

		got, err := db.GetSeasonBySeriesAndNumber(seriesID, 1)
		if err != nil {
			t.Fatalf("get deleted season: %v", err)
		}
		if got != nil {
			t.Error("expected season 1 to be deleted")
		}

		var s2Count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM seasons WHERE series_id = ? AND season_number = ?`, seriesID, 2).Scan(&s2Count); err != nil {
			t.Fatalf("count season 2: %v", err)
		}
		if s2Count != 1 {
			t.Errorf("expected season 2 to remain, got count %d", s2Count)
		}

		var mfCount int
		if err := db.QueryRow(`SELECT COUNT(*) FROM media_files WHERE series_id = ? AND season_number = ?`, seriesID, 1).Scan(&mfCount); err != nil {
			t.Fatalf("count media files season 1: %v", err)
		}
		if mfCount != 0 {
			t.Errorf("expected media_files for season 1 to cascade-delete, got %d", mfCount)
		}

		var mfCount2 int
		if err := db.QueryRow(`SELECT COUNT(*) FROM media_files WHERE series_id = ? AND season_number = ?`, seriesID, 2).Scan(&mfCount2); err != nil {
			t.Fatalf("count media files season 2: %v", err)
		}
		if mfCount2 != 1 {
			t.Errorf("expected season 2 media_files to remain, got %d", mfCount2)
		}
	})

	t.Run("missing season returns 0 rows", func(t *testing.T) {
		db := newTestDB(t)
		seriesID := createTestSeries(t, db)

		affected, err := db.DeleteSeason(seriesID, 99)
		if err != nil {
			t.Fatalf("delete missing season: %v", err)
		}
		if affected != 0 {
			t.Errorf("expected 0 rows affected, got %d", affected)
		}
	})

	t.Run("missing series returns 0 rows", func(t *testing.T) {
		db := newTestDB(t)

		affected, err := db.DeleteSeason(99999, 1)
		if err != nil {
			t.Fatalf("delete from missing series: %v", err)
		}
		if affected != 0 {
			t.Errorf("expected 0 rows affected, got %d", affected)
		}
	})
}

func TestSeasonAiredEpisodes_WithEpisodes(t *testing.T) {
	db := newTestDB(t)
	seriesID := createTestSeries(t, db)

	season := &Season{
		SeriesID:      seriesID,
		SeasonNumber:  1,
		AiredEpisodes: 10,
	}
	_, err := db.UpsertSeason(season)
	if err != nil {
		t.Fatalf("failed to upsert season: %v", err)
	}

	got, err := db.GetSeasonBySeriesAndNumber(seriesID, 1)
	if err != nil {
		t.Fatalf("failed to get season: %v", err)
	}
	if got == nil {
		t.Fatal("expected season, got nil")
	}
	if got.AiredEpisodes != 10 {
		t.Errorf("expected aired_episodes=10, got %d", got.AiredEpisodes)
	}
}

func TestSeasonAiredEpisodes_Zero(t *testing.T) {
	db := newTestDB(t)
	seriesID := createTestSeries(t, db)

	season := &Season{
		SeriesID:      seriesID,
		SeasonNumber:  1,
		AiredEpisodes: 0,
	}
	_, err := db.UpsertSeason(season)
	if err != nil {
		t.Fatalf("failed to upsert season: %v", err)
	}

	got, err := db.GetSeasonBySeriesAndNumber(seriesID, 1)
	if err != nil {
		t.Fatalf("failed to get season: %v", err)
	}
	if got == nil {
		t.Fatal("expected season, got nil")
	}
	if got.AiredEpisodes != 0 {
		t.Errorf("expected aired_episodes=0, got %d", got.AiredEpisodes)
	}
}

func TestSeasonAiredEpisodes_PreservedOnUpsert(t *testing.T) {
	db := newTestDB(t)
	seriesID := createTestSeries(t, db)

	// Insert season with aired_episodes=8
	season := &Season{
		SeriesID:      seriesID,
		SeasonNumber:  1,
		AiredEpisodes: 8,
	}
	_, err := db.UpsertSeason(season)
	if err != nil {
		t.Fatalf("failed to upsert season: %v", err)
	}

	// Upsert same season with aired_episodes=0 (e.g. scanner re-sync)
	// Direct assignment overwrites the old value.
	season2 := &Season{
		SeriesID:      seriesID,
		SeasonNumber:  1,
		AiredEpisodes: 0,
	}
	_, err = db.UpsertSeason(season2)
	if err != nil {
		t.Fatalf("failed to re-upsert season: %v", err)
	}

	got, err := db.GetSeasonBySeriesAndNumber(seriesID, 1)
	if err != nil {
		t.Fatalf("failed to get season: %v", err)
	}
	if got.AiredEpisodes != 0 {
		t.Errorf("expected aired_episodes=0 (overwritten), got %d", got.AiredEpisodes)
	}
}

func TestSeasonAiredEpisodes_SyncTransaction(t *testing.T) {
	db := newTestDB(t)
	seriesID := createTestSeries(t, db)

	tvdbID := 99999
	series := &Series{
		Title:        "Test Show",
		TotalSeasons: 2,
		AiredSeasons: 1,
	}
	seasons := []Season{
		{SeriesID: seriesID, SeasonNumber: 1, AiredEpisodes: 10},
		{SeriesID: seriesID, SeasonNumber: 2, AiredEpisodes: 0},
	}

	err := db.SyncSeriesAndChildren(seriesID, tvdbID, series, seasons, nil)
	if err != nil {
		t.Fatalf("failed to sync: %v", err)
	}

	s1, err := db.GetSeasonBySeriesAndNumber(seriesID, 1)
	if err != nil {
		t.Fatalf("failed to get season 1: %v", err)
	}
	if s1.AiredEpisodes != 10 {
		t.Errorf("season 1: expected aired_episodes=10, got %d", s1.AiredEpisodes)
	}

	s2, err := db.GetSeasonBySeriesAndNumber(seriesID, 2)
	if err != nil {
		t.Fatalf("failed to get season 2: %v", err)
	}
	if s2.AiredEpisodes != 0 {
		t.Errorf("season 2: expected aired_episodes=0, got %d", s2.AiredEpisodes)
	}
}
