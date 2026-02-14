# Remove manual sync endpoint

## Overview
`POST /api/series/{id}/sync` endpoint exists but the frontend button was removed — dead code. Remove the endpoint and handler.

## Development Approach
- **Reasonable sufficiency** — do not add features that were not requested. Do not invent extra work.
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**

## Implementation Steps

### Task 1: Remove endpoint and handler
- [ ] Remove route `r.Post("/{id}/sync", s.handleSyncSeriesFromTVDB)` from `internal/api/router.go`
- [ ] Remove the `handleSyncSeriesFromTVDB` handler function from `internal/api/handlers_series.go`
- [ ] Remove the test for the sync endpoint in `internal/api/router_test.go`
- [ ] Verify `SyncSeriesMetadata` in `internal/api/sync.go` is NOT removed (used by scheduler)
- [ ] Run project tests
- [ ] Run linter — fix any issues
