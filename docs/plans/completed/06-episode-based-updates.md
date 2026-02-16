# Episode-based Updates — track new episodes instead of seasons

## Overview
Refactor the Updates tab logic: determine season aired status by checking aired episodes from the TVDB API, instead of the `year` field on seasons (which TVDB does not return in `/series/{id}/extended`). Store `aired_episodes` per season. Track both new seasons and mid-season breaks (return from hiatus).

**Problem:** `isSeasonAired()` → `MaxAiredSeasonNumber()` → `SyncSeriesMetadata()` → `aired_seasons = 0` for all series → Updates tab is always empty.

**Solution:** a single request to `/series/{id}/episodes/official` returns all episodes with an `aired` date field. Count aired episodes per season; a season is aired if `aired_episodes > 0`.

## Context (from discovery)
- **Broken chain**: `filterSeasons()` → `isSeasonAired(year="")` → false → `MaxAiredSeasonNumber()` = 0 → `aired_seasons = 0`
- **Files:**
  - `internal/tvdb/client.go` — `filterSeasons`, `isSeasonAired`, `MaxAiredSeasonNumber`, `AiredSeasonNumbers`, `SeasonInfo.Aired`
  - `internal/api/sync.go` — `SyncSeriesMetadata` (line 218: `AiredSeasons: tvdb.MaxAiredSeasonNumber`), `CheckForTVDBUpdates` (line 83)
  - `internal/api/router.go` — `handleGetUpdates` (line 954), `queryNewSeasonNumbers` (line 1047)
  - `internal/database/db.go` — schema: `seasons` table, `series.aired_seasons`
  - `internal/database/series.go` — `Season` struct, `Series` struct, `SyncSeriesAndChildren`, `GetSeasonBySeriesAndNumber`
  - `web/static/app.js` — `loadUpdates()` (line 745): uses `u.new_seasons`, `u.aired_seasons`
- **Tests:** mock TVDB server (`newTestTVDBServer`), `TestHandleGetUpdates_*`, `TestIsSeasonAired`, `TestMaxAiredSeasonNumber`
- **TVDB API:** `GET /series/{id}/episodes/official?page=0` — all episodes, pagination support, each episode has `aired` (date string), `seasonNumber`, `number`

## Development Approach
- **Testing approach**: Regular (code first, then tests)
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**
- **CRITICAL: update this plan file when scope changes during implementation**

## Testing Strategy
- **Unit tests**: required for every task
- Mock TVDB server for testing new endpoints (existing pattern from `newTestTVDBServer`)

## Progress Tracking
- Mark completed items with `[x]` immediately when done
- Add newly discovered tasks with ➕ prefix
- Document issues/blockers with ⚠️ prefix

## Implementation Steps

### Task 1: Add `aired_episodes` to seasons table and struct
- [x] In `internal/database/db.go` add `aired_episodes INTEGER DEFAULT 0` to CREATE TABLE seasons (after `is_owned`)
- [x] In `internal/database/series.go` add `AiredEpisodes int` to the `Season` struct
- [x] Update `GetSeasonBySeriesAndNumber` — add `aired_episodes` to SELECT and Scan
- [x] Update `upsertSeasonTx` — add `aired_episodes` to INSERT/UPDATE (use COALESCE to preserve on re-sync)
- [x] Update all other SELECTs from seasons where columns are listed — add `aired_episodes`
- [x] Write tests: season with `aired_episodes > 0`, season with `aired_episodes = 0`
- [x] Run tests — must pass before task 2

### Task 2: Add `GetSeriesEpisodes` to TVDB client
- [x] In `internal/tvdb/client.go` add struct `EpisodeBase` with fields: `ID int`, `SeasonNumber int`, `Number int`, `Aired string`, `Name string`
- [x] Add method `GetSeriesEpisodes(tvdbID int) ([]EpisodeBase, error)` — GET `/series/{id}/episodes/official`, handle pagination (`links.next` field in response)
- [x] Add function `CountAiredEpisodesBySeason(episodes []EpisodeBase) map[int]int` — group by `SeasonNumber`, count only episodes with `aired != "" && aired <= today`
- [x] Write tests for `CountAiredEpisodesBySeason`: empty list, all aired, all future, mix, season 0 (specials) included
- [x] Write tests for `GetSeriesEpisodes` with mock TVDB server (single page, multiple pages)
- [x] Run tests — must pass before task 3

### Task 3: Update `SyncSeriesMetadata` — store aired_episodes
- [x] In `SyncSeriesMetadata()` after `GetSeriesExtendedFull`, call `tvdbClient.GetSeriesEpisodes(tvdbID)`
- [x] Call `CountAiredEpisodesBySeason(episodes)` to get aired count per season
- [x] When building `[]database.Season`, populate `AiredEpisodes` from that map
- [x] Calculate `AiredSeasons` as count of seasons with `aired_episodes > 0` (replacing `MaxAiredSeasonNumber`)
- [x] If `GetSeriesEpisodes` returns an error — log warning, continue sync with `aired_episodes = 0` (do not break main sync)
- [x] Update existing tests `TestSyncSeriesMetadata_Success` — add mock for `/episodes/official`
- [x] Write test: sync sets correct aired_episodes per season
- [x] Run tests — must pass before task 4

### Task 4: Update `CheckForTVDBUpdates` — episode-based detection
- [x] Replace `tvdb.MaxAiredSeasonNumber(details.Seasons)` with episode fetch + `CountAiredEpisodesBySeason`
- [x] Load current `aired_episodes` from DB for comparison (single query per series)
- [x] Detect changes: new season with aired episodes OR increase in aired_episodes for existing season
- [x] Create alerts with specific info: "Series X — S03: 4 new episodes" or "Series X — new season S04"
- [x] Update `aired_episodes` in seasons and `aired_seasons` in series after check
- [x] Write tests: new aired season → alert, mid-season return (aired_episodes increased) → alert, unchanged episodes → no alert
- [x] Run tests — must pass before task 5

### Task 5: Update `handleGetUpdates` — episode-based logic
- [x] Rewrite SQL: series appears in Updates if there are seasons with `season_number > max_watched AND aired_episodes > 0`
- [x] Add `aired_episodes` per new season to the response (instead of just season numbers)
- [x] Remove `queryNewSeasonNumbers()` — replace with direct query of aired seasons
- [x] Update frontend `loadUpdates()` in `web/static/app.js` — show episode count ("S03: 8 episodes", "S04: 4 episodes")
- [x] Update existing tests `TestHandleGetUpdates_*` for new logic
- [x] Write test: unaired season (aired_episodes = 0) does not appear in Updates
- [x] Run tests — must pass before task 6

### Task 6: Remove dead code
- [x] Remove `isSeasonAired()` from `internal/tvdb/client.go`
- [x] Remove `MaxAiredSeasonNumber()` from `internal/tvdb/client.go`
- [x] Remove `AiredSeasonNumbers()` from `internal/tvdb/client.go` (if unused)
- [x] Remove `Aired bool` field from `SeasonInfo` struct
- [x] Remove `isSeasonAired` call from `filterSeasons` (field `Aired` no longer needed)
- [x] Remove tests from `internal/tvdb/aired_test.go` for deleted functions
- [x] Run tests — must pass before task 7

### Task 7: Final verification
- [x] Verify all requirements from Overview are implemented
- [x] Verify edge cases: series without tvdb_id, series without episodes, season 0 (specials)
- [x] Run full test suite
- [x] Run linter — all issues must be fixed
- [x] `go vet ./...` passes
- [x] Build project `go build ./...`

*Note: ralphex automatically moves completed plans to `docs/plans/completed/`*

## Technical Details
- **TVDB endpoint:** `GET /series/{id}/episodes/official?page=0` → `{data: {episodes: [...]}, links: {next: "url_or_null"}}`
- **EpisodeBase fields:** `id`, `seasonNumber`, `number`, `aired` (string "YYYY-MM-DD" or ""), `name`
- **Aired logic:** episode is aired if `aired != ""` and `aired <= today` (parse as date)
- **aired_episodes** in seasons — integer, count of aired episodes, updated on every sync/check
- **aired_seasons** in series — `COUNT(seasons WHERE aired_episodes > 0 AND season_number > 0)`, computed on write
- **Pagination:** TVDB returns ~500 episodes per page, `links.next` contains next page URL or null
- **Mid-season break:** aired_episodes in a watched season increased since last check → notification in system_alerts

## Post-Completion
**Manual verification:**
- `ALTER TABLE seasons ADD COLUMN aired_episodes INTEGER DEFAULT 0` on production DB
- Restart container, wait for TVDB check or trigger POST /api/updates/check
- Verify Updates shows correct series (Squid Game S3, Reacher S1/S2 etc)
- Verify unaired seasons (Fallout S2 id=2201569 with 0 episodes) do not appear
