# Season and Series Deletion (Files + qBittorrent + DB)

## Overview

Extend deletion functionality so that:

1. **Series deletion** (already partially implemented at `DELETE /api/series/{id}`): also removes all qBittorrent torrents linked via any season's `torrent_hash`, and clears orphaned `next_season_cache` rows.
2. **Season deletion** (new): adds `DELETE /api/series/{id}/seasons/{num}` that physically removes the season's files and folder, removes the qBittorrent torrent (if any), deletes the season's DB rows (cascading to `media_files`), clears `next_season_cache` for that season, and exposes a Delete button on the season detail page.

Why now: users want to free up disk space and remove finished/abandoned content end-to-end without leaving orphan torrents seeding in qBittorrent or stale DB rows that break re-import. Today series-delete leaves torrents seeding; per-season delete does not exist at all.

## Context (from discovery)

**Files/components involved:**
- Backend handler: `internal/api/router.go` (existing `handleDeleteSeries` at line 625; new `handleDeleteSeason` to add; route registration in router setup).
- DB layer: `internal/database/series.go` (`GetSeasonFolderPaths`, `UpdateTorrentHash`, season queries), `internal/database/media_files.go` (`GetMediaFilePathsBySeriesID`, `DeleteMediaFilesBySeason`, `DeleteProcessedFilesBySeason`).
- DB schema: `internal/database/db.go` — `seasons` (CASCADE from series), `media_files` (CASCADE from `(series_id, season_number)`), `next_season_cache` (NO FK — manual cleanup needed), `processed_files` (SET NULL).
- qBittorrent: `internal/qbittorrent/client.go:174` — `DeleteTorrent(hash)` already exists with `deleteFiles=false` (decision: we delete files ourselves; safer and respects `MEDIA_PATH` boundary).
- Frontend: `web/templates/index.html` (series delete button at line 170; season detail page lines 214-305 — no delete button yet); `web/static/app.js` (existing `deleteSeries()` at line 372; need new `deleteSeason()`).
- Path safety: `Server.isWithinMediaPath()` at `router.go:701` — every disk delete must pass this check.

**Related patterns found:**
- chi/v5 router with method+path tuples.
- Series-level deletion already collects file/folder paths, deletes from disk inside `MEDIA_PATH`, then `DELETE FROM series` relying on FK CASCADE.
- qBit category recovery for redownload (commit `a19dabf`) is unrelated to delete but confirms torrent_hash is the link to qBit.
- Vanilla JS frontend with `api.delete()` helper and native `confirm()` dialog.
- Tests: standard `testing` package, table-driven, `httptest.NewServer` for qBit mocks, temp-file SQLite for DB tests.

**Dependencies identified:**
- `seasons.torrent_hash` is the only link between a season and a qBit torrent. If `NULL`, no torrent action needed for that season.
- `next_season_cache` rows are keyed by `(series_id, season_number)` and have NO foreign key — must be deleted manually in both flows.
- `processed_files.series_id` becomes `NULL` on series delete (acceptable; rows become orphans).

## Development Approach

- **Testing approach**: Regular (code first, then tests). Project convention: table-driven tests, temp SQLite for DB, `httptest.NewServer` for qBit.
- Complete each task fully before moving to the next.
- Make small, focused changes.
- Tests required for each task — both happy path and error/edge cases.
- All tests must pass before starting next task.
- Update this plan if scope changes mid-flight.
- Run `docker-compose up -d --build` after backend or frontend changes for end-to-end verification (project runs in Docker; do **not** use `go build`).
- No DB migration code — schema stays as-is; deletion uses existing CASCADE plus manual `next_season_cache` cleanup.

## Testing Strategy

- **Unit tests**: required for every task.
  - DB layer: new helpers (`DeleteSeason`, `GetSeasonByNumber`, `DeleteNextSeasonCacheBySeries`, `DeleteNextSeasonCacheBySeason`, `GetTorrentHashesBySeries`) — temp-DB tests with table-driven cases.
  - qBit cleanup: test continues even if `DeleteTorrent` returns `ErrTorrentNotFound` (treat as already-gone).
  - HTTP handlers: use existing test scaffolding in `internal/api/*_test.go`; mock qBit via `httptest.NewServer`.
- **E2E tests**: project has no Playwright/Cypress suite — manual UI verification covered in Post-Completion.

## Progress Tracking

- Mark completed items with `[x]` immediately when done.
- Add newly discovered tasks with ➕ prefix.
- Document blockers with ⚠️ prefix.
- Keep this file in sync with actual work done.

## What Goes Where

- **Implementation Steps** (`[ ]`): code, tests, route wiring, frontend changes — everything achievable in this repo.
- **Post-Completion** (no checkboxes): manual verification in browser against real qBittorrent + NFS mount.

## Implementation Steps

### Task 1: DB helpers for deletion

Add small, focused DB functions used by both handlers. Keep them in the existing files alongside related queries.

- [x] add `GetTorrentHashesBySeries(seriesID int64) ([]string, error)` to `internal/database/series.go` — selects non-null `torrent_hash` from `seasons WHERE series_id = ?`.
- [x] add `GetSeasonByNumber(seriesID int64, seasonNumber int) (*Season, error)` to `internal/database/series.go` if not already covered — already covered by existing `GetSeasonBySeriesAndNumber` (returns torrent_hash + folder_path; returns nil for sql.ErrNoRows).
- [x] add `DeleteSeason(seriesID int64, seasonNumber int) (int64, error)` to `internal/database/series.go` — `DELETE FROM seasons WHERE series_id = ? AND season_number = ?`; returns rows affected (CASCADE removes `media_files` via composite FK).
- [x] add `DeleteNextSeasonCacheBySeries(seriesID int64) error` to `internal/database/next_season_cache.go` — `DELETE FROM next_season_cache WHERE series_id = ?`.
- [x] add `DeleteNextSeasonCacheBySeason(seriesID int64, seasonNumber int) error` to `internal/database/next_season_cache.go` — `DELETE FROM next_season_cache WHERE series_id = ? AND season_number = ?`.
- [x] write table-driven unit tests in `internal/database/series_test.go` and `internal/database/next_season_cache_test.go` covering: zero rows, one row, multiple rows, missing series, NULL torrent_hash filtered out.
- [x] run `go test ./internal/database/...` — must pass before next task.

### Task 2: Per-season deletion API endpoint

Implement `DELETE /api/series/{id}/seasons/{num}` in `internal/api/router.go`.

- [x] register route inside the existing chi setup (next to other season routes; e.g., near `handleUpdateSeason`).
- [x] implement `handleDeleteSeason(w, r)`:
  - parse `id` (int64) and `num` (int) from URL params; 400 on parse error.
  - load season via `GetSeasonByNumber`; 404 if not found.
  - if `season.TorrentHash != nil && *season.TorrentHash != ""`, call `s.qbitClient.DeleteTorrent(*season.TorrentHash)`; ignore `ErrTorrentNotFound`; on any other error log and continue (do not block disk/DB cleanup — torrent stays in qBit but we surface a warning in the response).
  - get media file paths for that season: add `GetMediaFilePathsBySeason(seriesID, seasonNumber)` to `media_files.go` (mirrors existing `GetMediaFilePathsBySeriesID` but scoped to one season).
  - `os.Remove` each file inside `MEDIA_PATH`; warn-and-continue on individual failures.
  - if `season.FolderPath != nil` and inside `MEDIA_PATH`, `os.RemoveAll(folder)` (use `RemoveAll` — folder may still contain leftover non-tracked files like `.nfo`, audio backups, partial downloads).
  - call `DeleteSeason(seriesID, seasonNumber)`; 404 if rows == 0, 500 on error.
  - call `DeleteNextSeasonCacheBySeason(seriesID, seasonNumber)`; log-and-continue on error (non-fatal).
  - log summary `slog.Info("Deleted season", "series_id", id, "season", num, "files_removed", N, "torrent_removed", bool)`.
  - respond `{ "success": true, "files_removed": N, "folder_removed": bool, "torrent_removed": bool }`.
- [x] write unit tests `handleDeleteSeason` in `internal/api/*_test.go`:
  - happy path with torrent_hash set (verify qBit DELETE called).
  - happy path with `torrent_hash = NULL` (no qBit call).
  - season not found (404).
  - bad path params (400).
  - qBit returns `ErrTorrentNotFound` — request still succeeds.
  - file outside `MEDIA_PATH` — skipped, no error.
- [x] run `go test ./internal/api/...` — must pass before next task.

### Task 3: Extend handleDeleteSeries with qBit cleanup and cache cleanup

Update existing `handleDeleteSeries` at `internal/api/router.go:625` so series deletion also removes torrents and orphaned cache rows.

- [x] before the `DELETE FROM series` query, call `s.db.GetTorrentHashesBySeries(id)`; for each hash call `s.qbitClient.DeleteTorrent(hash)`; ignore `ErrTorrentNotFound`; warn-and-continue on others; track `torrentsRemoved` count.
- [x] replace `os.Remove(folderPath)` with `os.RemoveAll(folderPath)` for season folders (folders may have leftover files; series-delete should not silently leave them).
- [x] after successful `DELETE FROM series`, call `DeleteNextSeasonCacheBySeries(id)`; log-and-continue on error.
- [x] update final `slog.Info` and JSON response to include `torrents_removed` count.
- [x] update existing tests for `handleDeleteSeries` (if any) and add cases for: series with multiple seasons each with torrent_hash (verify each is deleted), series with no torrents, qBit error on one torrent doesn't abort the rest.
- [x] run `go test ./internal/api/...` — must pass before next task.

### Task 4: Frontend — Delete Season button on season detail page

- [ ] add a Delete button in `web/templates/index.html` season detail page (lines 214-305), styled with `btn btn-danger` matching the existing series delete button. Place it in a sensible location — likely next to the season title / tracker link block, or in a footer action row.
- [ ] add `id="delete-season-btn"` and a click handler in `web/static/app.js`.
- [ ] implement `deleteSeason()` in `web/static/app.js` mirroring `deleteSeries()`:
  - read current `state.currentSeriesId` and the season number from the rendered view (or store on state when opening a season).
  - native `confirm()` with Russian text: `Удалить сезон ${num} сериала "${title}"? Файлы, папка и раздача в qBittorrent будут удалены. Это действие необратимо.`
  - `await api.delete(`/api/series/${seriesId}/seasons/${num}`)`.
  - on success: `showToast('Season deleted')` and `navigate('/series/${seriesId}')` (back to series detail).
  - on error: `showToast(...)`.
- [ ] update the series-level delete confirmation message (optional but consistent) to mention qBit removal: `Удалить "${title}" со всеми файлами и раздачами? Это действие необратимо.`
- [ ] no JS unit tests in this project — verification is manual.
- [ ] run `docker-compose up -d --build` and smoke-test in browser before moving on.

### Task 5: Verify acceptance criteria

- [ ] verify all requirements from Overview are implemented end-to-end.
- [ ] verify edge cases:
  - season with `torrent_hash = NULL` deletes cleanly.
  - season whose torrent was already removed in qBit (returns `ErrTorrentNotFound`) deletes cleanly.
  - file path outside `MEDIA_PATH` is skipped with a warning, not an error.
  - series delete still works if qBit is unreachable (warns, continues with disk + DB).
  - 404 for non-existent series id / season number.
- [ ] run full test suite: `go test ./...` — must pass.
- [ ] run linter (`make lint` if defined; otherwise `go vet ./...`) — fix any issues.
- [ ] verify CASCADE: after season delete, no rows in `seasons` or `media_files` for that key; `processed_files.series_id` may remain (SET NULL is fine).
- [ ] verify `next_season_cache` is empty for the deleted scope.

### Task 6: Update documentation

- [ ] update `README.md` if it documents the API surface — add the new `DELETE /api/series/{id}/seasons/{num}` route and clarify that series delete now also removes torrents.
- [ ] update CLAUDE.md only if a new pattern is introduced (none expected — handlers and DB helpers follow existing conventions).

*Note: ralphex automatically moves completed plans to `docs/plans/completed/`*

## Technical Details

**Deletion order (per season — both in `handleDeleteSeason` and inside `handleDeleteSeries`'s loop):**
1. qBit `DeleteTorrent(hash)` — earliest, so a failure doesn't leave the DB in a half-deleted state with no torrent reference. Errors except `ErrTorrentNotFound` are warn-and-continue.
2. Disk: `os.Remove` each tracked media file path, then `os.RemoveAll` on the season folder (both gated by `isWithinMediaPath`).
3. DB: `DELETE FROM seasons WHERE ...` (CASCADE removes `media_files`).
4. Cache: `DELETE FROM next_season_cache WHERE ...`.

**Why `RemoveAll` instead of `Remove` for folders:**
- Audio processing creates extra files (originals, processed mp4s) that may not be tracked in `media_files`.
- Partial qBittorrent downloads (`.!qB` extension) can linger.
- `Remove` only succeeds on empty dirs and silently leaves filled folders behind.
- The boundary check (`isWithinMediaPath`) guards against escape; inside a confirmed season folder, recursive removal is the intended behavior.

**Why `deleteFiles=false` in qBit (chosen over `deleteFiles=true`):**
- We control the path validation via `isWithinMediaPath` before any `os.Remove`. qBit would not run that check.
- Some files in the season folder were created by audio processing and are not part of the torrent — we want them gone too.
- qBit may still have a stale save_path that no longer matches reality on NFS.

**Response shape:**

```json
// Season delete
{ "success": true, "files_removed": 12, "folder_removed": true, "torrent_removed": true }

// Series delete (extended)
{ "success": true, "files_removed": 24, "folders_removed": 2, "torrents_removed": 2 }
```

**Error handling philosophy:**
- DB lookup failures → 500 (don't proceed and orphan data).
- File/folder failures → warn-and-continue (best-effort; logged for ops).
- qBit failures (other than not-found) → warn-and-continue; still report `torrent_removed: false`.
- Final DB delete failure → 500; the disk cleanup may have happened, but the user can retry and `os.Remove` is idempotent (file-already-missing is fine).

## Post-Completion

**Manual verification (must run in real environment with NFS mount + live qBittorrent):**

- Pick a real series with at least one season that has `torrent_hash` set:
  - Open season detail page → click Delete → confirm dialog appears in Russian → confirm.
  - Verify in qBittorrent UI: torrent gone.
  - Verify on `/Volumes/Plex/TV Show/`: season folder gone.
  - Verify in DB (`sqlite3 data/episodex.db "SELECT * FROM seasons WHERE series_id = ? AND season_number = ?"`): row gone, `media_files` rows for that season gone.
- Repeat for series-level delete: every torrent linked to any season in that series is removed from qBit.
- Try deleting a season whose torrent was manually removed from qBit beforehand — endpoint should still succeed (404 from qBit handled as not-found).
- Try deleting with qBit stopped — endpoint should still complete disk + DB delete, with a warning in logs.

**External system updates:** none — this is a self-contained feature.
