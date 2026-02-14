# Season `is_owned` — clickability based on file presence

## Overview
Add `is_owned` column to seasons. Scanner manages it exclusively based on file presence. Frontend uses it to determine if a season is clickable.

## Context
- `is_watched` = "season was ever in library", never resets, used for Updates page
- `is_owned` = "files currently present on disk", scanner sets/clears
- Without `is_owned`, seasons show as available but error when clicked (no files)

## Key Decisions
- **No migration needed** — scanner will set `is_owned` correctly on next run
- **`is_owned` excluded from `UpsertSeason`** — managed only by scanner via direct SQL to avoid TVDB sync resetting it
- **If no files — season is grey/locked**, regardless of whether it was previously owned
- **When clearing `is_owned`**: delete `media_files`, clear episode file fields, but preserve `voice_actor_id`

## Development Approach
- **Reasonable sufficiency** — do not add features that were not requested. Do not invent extra work.
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**

## Implementation Steps

### Task 1: Add `is_owned` column to seasons
- [x] Add `is_owned BOOLEAN DEFAULT 0` column to `seasons` table schema in `internal/database/db.go`
- [x] Add `IsOwned bool` field to `Season` struct in `internal/database/series.go`
- [x] **DO NOT** add `is_owned` to `UpsertSeason` — it is managed exclusively by the scanner
- [x] Update `GetSeasonBySeriesAndNumber` to scan `is_owned`
- [x] Update API handlers in `internal/api/router.go` that query seasons to include `is_owned` and return it as `"owned"` in JSON responses (`handleGetSeries`, `handleListSeasons`, `handleGetSeason`)
- [x] Write tests for season with `is_watched=1, is_owned=0` and `is_watched=1, is_owned=1`
- [x] Run project tests — must pass before task 2

### Task 2: Scanner manages `is_owned`
- [x] In `internal/scanner/scanner.go` `processSeriesInfo()`, update the raw SQL `INSERT ... ON CONFLICT DO UPDATE` (line ~472) to set `is_owned = 1` when files are found
- [x] In `Scan()` method, after processing all found series, query all seasons with `is_owned = 1` and check if their `folder_path` still exists and contains video files
- [x] For seasons where folder is gone or empty:
  - Set `is_owned = 0` and `folder_path = NULL`
  - Delete `media_files` rows for that `(series_id, season_number)`
  - Clear episode file fields: `UPDATE episodes SET file_path = NULL, file_hash = NULL, file_size = NULL WHERE season_id = ?` (preserve voice_actor_id, TVDB metadata, is_watched)
- [x] Write tests for the clearing logic (folder exists -> keep; folder gone -> clear; folder empty -> clear)
- [x] Run project tests — must pass before task 3

### Task 3: Frontend uses `is_owned` for clickability
- [ ] In `web/static/app.js` `renderSeasons()`, use `season.owned` to determine if season card is clickable
- [ ] Both `owned=false` states (previously had files, never had files) render identically as grey/locked
- [ ] In `loadSeasonDetail()`, check `seasonInfo.owned` — if false, show "files not available" and hide audio/voice panels
- [ ] Run project tests

## Technical Details
- `seasons` table gets new column: `is_owned BOOLEAN DEFAULT 0`
- `is_owned`: set by scanner when files exist, cleared when files disappear — **not part of `UpsertSeason`**
- `is_watched`: set once by scanner, never cleared — for Updates logic
- Season clickability: `is_owned = 1`
- JSON response: `"owned"` = is_owned, `"watched"` = is_watched
- Scanner raw SQL in `processSeriesInfo` uses `ON CONFLICT DO UPDATE SET is_owned = 1`
