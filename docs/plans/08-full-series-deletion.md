# Full Series Deletion (with filesystem cleanup)

## Overview
- Enhance the existing `DELETE /api/series/{id}` endpoint to also delete video files and season folders from the media library on disk
- Currently the endpoint only removes database records (via CASCADE), leaving video files orphaned on disk
- The UI Delete button and confirmation dialog already exist on the series detail page
- After this change, deleting a series will be a complete cleanup: database + filesystem

## Context (from discovery)
- Files/components involved:
  - `internal/api/router.go` — `handleDeleteSeries()` (line 572) — existing DELETE handler, currently only deletes from DB
  - `internal/database/db.go` — schema with CASCADE rules: series → seasons, series_characters, media_files (via seasons), processed_files (SET NULL)
  - `internal/database/media_files.go` — `MediaFile` struct with `FilePath` field, existing helper `DeleteMediaFilesBySeason()`
  - `internal/database/series.go` — series/season DB operations
  - `web/static/app.js` — `deleteSeries()` function (line 345), Delete button already wired up
  - `web/templates/index.html` — Delete button already rendered (line 155)
- Related patterns found:
  - Scanner cleanup in `scanner.go` (line 208) already deletes media_files and processed_files per season — similar pattern
  - `media_files.file_path` contains the full path to each video file on disk
  - `seasons.folder_path` contains the season folder path
- Dependencies: none — this is an enhancement to an existing feature

## Development Approach
- **Testing approach**: Regular (code first, then tests)
- Complete each task fully before moving to the next
- Make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
- **CRITICAL: all tests must pass before starting next task** — no exceptions
- **CRITICAL: update this plan file when scope changes during implementation**
- Run tests after each change
- Maintain backward compatibility

## Testing Strategy
- **Unit tests**: required for every task (see Development Approach above)
- No E2E tests in this project

## Progress Tracking
- Mark completed items with `[x]` immediately when done
- Add newly discovered tasks with ➕ prefix
- Document issues/blockers with ⚠️ prefix
- Update plan if implementation deviates from original scope
- Keep plan in sync with actual work done

## Implementation Steps

### Task 1: Add database method to get media file paths and season folders for a series
- [ ] In `internal/database/media_files.go`, add method `GetMediaFilePathsBySeriesID(seriesID int64) ([]string, error)` that returns all `file_path` values from `media_files` where `series_id = ?`
- [ ] In `internal/database/series.go`, add method `GetSeasonFolderPaths(seriesID int64) ([]string, error)` that returns all `folder_path` values from `seasons` where `series_id = ?` and `folder_path IS NOT NULL AND folder_path != ''`
- [ ] Write table-driven tests for `GetMediaFilePathsBySeriesID` (series with files, series with no files, non-existent series)
- [ ] Write table-driven tests for `GetSeasonFolderPaths` (series with folders, seasons without folder_path, non-existent series)
- [ ] Run `go test ./internal/database/...` — must pass before next task

### Task 2: Enhance DELETE handler to remove files from disk before DB deletion
- [ ] In `internal/api/router.go` `handleDeleteSeries()` (line 572), before the existing `DELETE FROM series` query:
  1. Call `s.db.GetMediaFilePathsBySeriesID(id)` to get all video file paths
  2. Call `s.db.GetSeasonFolderPaths(id)` to get all season folder paths
  3. Delete each media file from disk using `os.Remove()` — log errors but don't fail the request (files may already be gone)
  4. Delete each season folder using `os.Remove()` (not `RemoveAll` — only removes empty folders after files are deleted) — log errors but don't fail
  5. Proceed with existing DB deletion as before
- [ ] Add `slog.Info` logging for each deleted file and folder
- [ ] Write tests for enhanced `handleDeleteSeries`:
  - test that handler calls file deletion before DB deletion (use temp files)
  - test that handler succeeds even if files don't exist on disk (already removed)
  - test that handler still returns 404 for non-existent series
- [ ] Run `go test ./internal/api/...` — must pass before next task

### Task 3: Update frontend confirmation dialog with more details
- [ ] In `web/static/app.js` `deleteSeries()` (line 345), update the `confirm()` message to warn that files will also be deleted from disk. Example: `Delete "${series?.title}" and all its files from disk? This cannot be undone.`
- [ ] Update error toast to include error details (consistent with recent fix): `Failed to delete series: ${e.message || e}`
- [ ] No automated tests for frontend — manual verification

### Task 4: Verify acceptance criteria
- [ ] Verify all requirements from Overview are implemented
- [ ] Verify edge cases are handled (missing files, empty folder_path, non-existent series)
- [ ] Run full test suite: `go test ./...`
- [ ] Run linter: `go vet ./...`
- [ ] Verify test coverage meets project standard

### Task 5: [Final] Update documentation
- [ ] Update README.md if needed
- [ ] Update project knowledge docs if new patterns discovered

*Note: ralphex automatically moves completed plans to `docs/plans/completed/`*

## Technical Details

### Deletion flow (after fix)
```
DELETE /api/series/{id}
  ↓ GetMediaFilePathsBySeriesID(id)
  → ["/mnt/media/Show.S01/ep1.mkv", "/mnt/media/Show.S01/ep2.mkv", ...]
  ↓ GetSeasonFolderPaths(id)
  → ["/mnt/media/Show.S01", "/mnt/media/Show.S02"]
  ↓ os.Remove() each media file (log errors, don't fail)
  ↓ os.Remove() each season folder (only works if empty, log errors)
  ↓ DELETE FROM series WHERE id = ? (CASCADE handles all DB records)
  → 200 OK {"success": true}
```

### Safety considerations
- Use `os.Remove()` not `os.RemoveAll()` for folders — only removes empty directories, prevents accidental data loss if folder contains unexpected files
- Log all filesystem operations for audit trail
- Don't fail the API request if files are already missing from disk — proceed with DB cleanup
- Frontend shows clear warning that files will be permanently deleted

### Database CASCADE chain (already working)
```
DELETE series (id=X)
  → DELETE seasons WHERE series_id=X (CASCADE)
    → DELETE media_files WHERE (series_id, season_number) matches (CASCADE)
  → DELETE series_characters WHERE series_id=X (CASCADE)
  → UPDATE processed_files SET series_id=NULL WHERE series_id=X (SET NULL)
```

## Post-Completion

**Manual verification:**
- Delete a test series and verify:
  - All DB records removed (series, seasons, media_files, characters)
  - Video files removed from `/mnt/media/`
  - Season folders removed (if empty after file deletion)
  - No errors in application logs
  - Series disappears from UI list
- Verify scanner doesn't re-add deleted series on next scan (files are gone)
