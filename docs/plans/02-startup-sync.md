# Startup sync for never-synced series

## Overview
On server startup, pull show metadata from TVDB for series that were added by the scanner but never had their metadata synced.

## Context
- Scanner adds series with basic info (title, poster, status) from `GetSeriesDetailsWithRussian`
- Full metadata (overview, genres, networks, studios, backdrop, etc.) is only pulled by `SyncSeriesMetadata`
- Without startup sync, new series wait up to 24h for the scheduled `tvdb_check` to populate metadata

## Key Decisions
- **Show metadata only** — updates only the `series` table via `UpsertSeries`
- **Does NOT touch seasons, episodes, characters, or artworks**
- **Non-blocking** — runs in background goroutine, does not delay server start
- **Criteria**: `tvdb_id IS NOT NULL AND overview IS NULL`

## Development Approach
- **Reasonable sufficiency** — do not add features that were not requested. Do not invent extra work.
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**

## Implementation Steps

### Task 1: Add `SyncUnsyncedShowMetadata` function
- [ ] Add function `SyncUnsyncedShowMetadata(db, tvdbClient)` in `internal/api/sync.go`
  - Query series with `tvdb_id IS NOT NULL AND overview IS NULL`
  - For each, call TVDB API (`GetSeriesExtendedFull` + `GetSeriesTranslation`) and update **only the `series` table** via `db.UpsertSeries`
  - Log progress
- [ ] Write test (series with tvdb_id but no overview -> metadata populated)
- [ ] Run project tests

### Task 2: Call on startup
- [ ] In `cmd/server/main.go`, after TVDB client init and before HTTP server start, launch a goroutine calling `api.SyncUnsyncedShowMetadata(db, tvdbClient)`
- [ ] Add log message "Syncing show metadata for never-synced series in background"
- [ ] Run project tests

## Technical Details
- Startup sync criteria: `tvdb_id IS NOT NULL AND overview IS NULL`
- Uses `UpsertSeries` which preserves existing fields via COALESCE
