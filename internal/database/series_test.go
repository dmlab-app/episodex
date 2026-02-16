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
