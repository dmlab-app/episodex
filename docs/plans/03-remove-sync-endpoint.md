# Remove dead API endpoints

## Overview
Two endpoints exist in the router but are never called by the frontend or any other code:
- `POST /api/series/{id}/sync` — frontend button was removed
- `POST /api/series/{id}/seasons/{num}/rescan` — frontend never calls it; scanning is fully automatic

## Development Approach
- **Reasonable sufficiency** — do not add features that were not requested. Do not invent extra work.
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**

## Implementation Steps

### Task 1: Remove sync endpoint
- [ ] Remove route `r.Post("/{id}/sync", s.handleSyncSeriesFromTVDB)` from `internal/api/router.go`
- [ ] Delete file `internal/api/handlers_series.go` (contains only this handler)
- [ ] Verify `SyncSeriesMetadata` in `internal/api/sync.go` is NOT removed (used by scheduler)
- [ ] Run project tests and linter

### Task 2: Remove rescan endpoint
- [ ] Remove route `r.Post("/{num}/rescan", s.handleRescanSeason)` from `internal/api/router.go`
- [ ] Remove `handleRescanSeason` handler from `internal/api/router.go`
- [ ] Remove `RescanSeason` method from `internal/scanner/scanner.go`
- [ ] Remove `InvalidateCachedDataForSeason` from `internal/database/media_files.go` (only caller was `RescanSeason`)
- [ ] Run project tests and linter
