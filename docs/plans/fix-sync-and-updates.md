# Fix Auto-Sync and Updates Logic

## Overview
Three issues found during review:
1. **"Sync with TVDB" button is manual** — syncing metadata should happen automatically, not require user action
2. **Updates page shows wrong data** — currently shows ANY missing season (including old deleted ones) as "new". Should only show genuinely new seasons that are newer than what the user has ever had.
3. **Updates must only show AIRED seasons** — a season counts as an update only when episodes have actually aired/released, not just announced on TVDB. If TVDB lists season 5 but it hasn't aired yet, it's NOT an update.

## Context
- Scheduler already runs daily TVDB check (`tvdb_check` task in `cmd/server/main.go`) but it only updates `total_seasons` count, doesn't sync full metadata
- `handleSyncSeriesFromTVDB` in `internal/api/handlers_series.go` does full metadata sync but is only triggered by manual button click
- Updates logic in `handleGetUpdates` (`internal/api/router.go`) uses `total_seasons > count(owned seasons)` which is wrong — if user watched season 1 and deleted files, it shows season 1 as "new update"
- Frontend has "Sync with TVDB" button in `web/templates/index.html` and `web/static/app.js`
- TVDB season data includes `firstAired` date on episodes — can be used to check if a season has actually started airing

## Development Approach
- **Testing approach**: Regular (code first, then tests)
- Complete each task fully before moving to the next
- **CRITICAL: every task MUST include new/updated tests** for code changes
- **CRITICAL: all tests must pass before starting next task**
- **CRITICAL: update this plan file when scope changes during implementation**

## Testing Strategy
- **Unit tests**: required for every task with backend changes
- **Manual verification**: check UI in browser after frontend changes

## Progress Tracking
- Mark completed items with `[x]` immediately when done
- Add newly discovered tasks with + prefix
- Document issues/blockers with ! prefix

## Implementation Steps

### Task 1: Make TVDB metadata sync automatic
- [x] In `cmd/server/main.go` — extend the daily `tvdb_check` task: after checking for new seasons, also call full metadata sync (`GetSeriesExtendedFull` + `GetSeriesTranslation`) for each series that hasn't been updated in 7+ days (check `updated_at` field)
- [x] Reuse the sync logic from `handleSyncSeriesFromTVDB` in `internal/api/handlers_series.go` — extract into a shared function that both the scheduler and the handler can call (e.g. `SyncSeriesMetadata(db, tvdbClient, seriesID, tvdbID)` or a method on Server)
- [x] Keep the `POST /api/series/{id}/sync` endpoint (useful for API, just remove from UI)
- [x] Write tests for the extracted sync function (success case, TVDB error case)
- [x] Run `make test` — must pass before next task

### Task 2: Remove Sync button from frontend
- [x] In `web/templates/index.html` — remove the "Sync with TVDB" button element (`#sync-tvdb-btn`)
- [x] In `web/static/app.js` — remove `syncWithTVDB()` function and its event listener binding
- [x] In `web/static/app.js` — remove any references to `sync-tvdb-btn` element
- [x] Verify in browser: series detail page renders without sync button, no console errors
- [x] No tests needed (frontend-only)

### Task 3: Track aired seasons from TVDB
- [ ] In `internal/tvdb/client.go` — ensure `GetSeriesDetailsWithRussian` (or a new method) returns season air dates. TVDB season objects have `firstAired` or episodes have `aired` dates. Add an `Aired` (bool or date) field to the Season struct returned by the client
- [ ] A season is considered "aired" if: it has at least one episode with an `aired` date in the past (or the season's own `firstAired` is in the past)
- [ ] In `cmd/server/main.go` `tvdb_check` task — when checking for new seasons, count only AIRED seasons (not just total listed on TVDB). Store `aired_seasons` count on the `series` table (add column if needed) or filter in the query
- [ ] Write tests for aired season detection logic
- [ ] Run `make test` — must pass before next task

### Task 4: Fix Updates logic — only show genuinely new aired seasons
- [ ] In `internal/api/router.go` — rewrite `handleGetUpdates`: an update is shown only when there are aired seasons with number HIGHER than `MAX(season_number)` from user's owned seasons for that series
- [ ] Handle edge case: series with no owned seasons at all (user added manually via TVDB, never had files) — do NOT show in updates
- [ ] Handle edge case: series where TVDB lists future unaired season — do NOT show as update
- [ ] In `cmd/server/main.go` — update the `tvdb_check` alert creation: only create "new seasons" alert when new aired seasons exist beyond user's max owned season
- [ ] Update the frontend updates page response to include which specific season numbers are new (not just a count)
- [ ] Update tests for handleGetUpdates (aired vs unaired, gaps in seasons, deleted old seasons, no owned seasons)
- [ ] Run `make test` — must pass before next task

### Task 5: Verify acceptance criteria
- [ ] Verify: no "Sync with TVDB" button on series detail page
- [ ] Verify: Updates page does NOT show old deleted seasons as new
- [ ] Verify: Updates page does NOT show unaired/announced seasons
- [ ] Verify: Updates page correctly shows only aired seasons newer than user's latest owned
- [ ] Verify: series with no owned seasons don't appear in updates
- [ ] Verify: daily TVDB check syncs full metadata for stale series
- [ ] Run full test suite `make test`
- [ ] Run linter `make lint` — all issues must be fixed

## Technical Details

### Updates query logic (Task 4)
Current (wrong):
```sql
WHERE s.total_seasons > (SELECT COUNT(*) FROM seasons WHERE series_id = s.id)
```

Correct approach:
```sql
WHERE s.aired_seasons > COALESCE(
    (SELECT MAX(season_number) FROM seasons WHERE series_id = s.id AND owned = 1),
    0
)
AND (SELECT COUNT(*) FROM seasons WHERE series_id = s.id AND owned = 1) > 0
```

The second condition ensures series with zero owned seasons don't show up (user hasn't started watching).

### Aired season detection (Task 3)
TVDB API returns season data with episode air dates. A season is "aired" when:
- The season has at least 1 episode with `aired` date <= today
- OR the season itself has a `firstAired` date <= today

Store as `aired_seasons` integer on `series` table, updated during `tvdb_check`.

### Auto-sync schedule (Task 1)
- Runs as part of existing daily `tvdb_check`
- Only syncs series where `updated_at < NOW() - 7 days`
- Syncs: title, overview, poster, backdrop, status, genres, networks, characters, artworks
- Rate-limited: already sequential in the loop

## Post-Completion
**Manual verification:**
- Wait for daily TVDB check or trigger manually via `POST /api/updates/check`
- Check that series metadata refreshes automatically over time
- Delete a season's files, rescan, verify it doesn't appear as "new update"
- Find a series with an announced but unaired future season, verify it does NOT show in updates
