# Recommendations Tab (TMDB-powered)

## Overview
Add a "Recommendations" tab that suggests new TV shows based on the user's existing library. Recommendations come from TMDB's collaborative-filtering endpoint (`/tv/{id}/recommendations`), seeded from each show the user already has. Candidates are filtered by TMDB rating (>7), exclusion of already-owned / blacklisted shows, and Kinozal availability (S01 torrent). Top 20 are displayed; clicking a card opens the Kinozal torrent page. Each card has a × button to permanently blacklist the show. A separate view lets the user manage (remove) blacklist entries.

The refresh runs on container startup and once every 24 hours via the existing scheduler.

## Context (from discovery)
- **Config loader**: `internal/config/config.go` (`Load()`, `getEnv()` helper) — add `TMDBApiKey` field
- **Scheduler**: `internal/scheduler/scheduler.go` + `cmd/server/main.go` lines ~150-190 — follow `tracker_check` task pattern with `IntervalSchedule` (runs on startup too)
- **TVDB client pattern**: `internal/tvdb/client.go` — mutex + http client + typed response structs; model new TMDB client on this
- **Kinozal searcher**: `SeasonSearcher` interface in `internal/tracker/tracker.go`; `FindSeasonTorrent(query, seasonNum)` returns single largest result. Pass via `api.ServerOption` (existing `WithSeasonSearcher`)
- **DB CRUD pattern**: `internal/database/next_season_cache.go` — `Get / Save / Clear` methods + table in `initTables()`
- **API handler pattern**: `internal/api/router.go` — chi route registration near line ~133, handlers follow `handleGetNextSeasons` style
- **Frontend tabs**: `web/templates/index.html` (nav at line ~29), `web/static/app.js` (`router()` hash dispatch)
- **Reusable card styling**: existing `.series-card` / `.season-card` in `web/static/style.css`
- **Genres JSON storage**: `series.genres` stored as TEXT (JSON array of strings) via `json.Marshal` in `internal/api/sync.go`

## Development Approach
- **Testing approach**: Regular (code first, then tests) — matches existing project pattern
- Complete each task fully before moving to the next
- Make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
- **CRITICAL: all tests must pass before starting next task** — no exceptions
- **CRITICAL: update this plan file when scope changes during implementation**
- Run tests after each change: `go build ./... && go test ./...`
- Deploy: `docker-compose up -d --build` (never `go build` directly)
- No DB migrations — schema changes only in `initTables()`; user drops DB if needed

## Testing Strategy
- **Unit tests**: required for every task. New packages (`internal/tmdb`, `internal/recommender`) get full coverage via mock HTTP servers (`httptest`) for external APIs and in-memory SQLite for DB.
- **No e2e framework** in project — manual UI verification documented in Post-Completion section.

## Progress Tracking
- Mark completed items with `[x]` immediately when done
- Add newly discovered tasks with ➕ prefix
- Document issues/blockers with ⚠️ prefix
- Update plan if implementation deviates from original scope

## What Goes Where
- **Implementation Steps** (`[ ]` checkboxes): all code, tests, schema, frontend — everything done in repo
- **Post-Completion** (no checkboxes): manual UI verification, TMDB API key provisioning, deploy check

## Implementation Steps

### Task 1: Prep — commit untracked processing_lock.go and create feature branch
- [x] verify `git status` is clean except for `internal/database/processing_lock.go` (already committed in 8d18bb8)
- [x] commit `processing_lock.go` on `main` with message `feat: add processing lock for concurrent audio operations` (done in 8d18bb8)
- [x] create and switch to branch `feature/recommendations` from `main` (already on branch)
- [x] run `go build ./... && go test ./...` — must pass before Task 2

### Task 2: Add TMDB config var
- [ ] add `TMDBApiKey string` field to `Config` struct in `internal/config/config.go`
- [ ] load via `getEnv("TMDB_API_KEY", "")` in `Load()`
- [ ] no validation required (optional — feature disables if empty)
- [ ] write test case in `internal/config/config_test.go` verifying TMDB_API_KEY env var is loaded
- [ ] run tests — must pass before Task 3

### Task 3: Create TMDB client package
- [ ] create `internal/tmdb/client.go` with `Client` struct (mutex, httpClient, apiKey, baseURL) mirroring `internal/tvdb/client.go`
- [ ] implement `NewClient(apiKey string) *Client` and `NewClientWithBaseURL(apiKey, base string) *Client` for test injection
- [ ] implement `makeRequest(method, path string, params url.Values)` helper — TMDB v3 uses `api_key` as query param OR `Authorization: Bearer <token>` header (use Bearer — cleaner)
- [ ] add typed response structs: `FindResult` (with `TVResults []TMDBShow`), `TMDBShow` (id, name, original_name, overview, poster_path, first_air_date, vote_average, genre_ids), `RecommendationsResponse` (with `Results []TMDBShow`)
- [ ] implement `FindByTVDBID(tvdbID int) (*TMDBShow, error)` — GET `/find/{tvdb_id}?external_source=tvdb_id`, returns first `tv_results` entry or nil
- [ ] implement `GetRecommendations(tmdbID int) ([]TMDBShow, error)` — GET `/tv/{tmdb_id}/recommendations`
- [ ] handle 429 rate limit: sleep 1s and retry once
- [ ] write tests using `httptest.Server` for all methods (success, 404, 429 retry, malformed JSON)
- [ ] run tests — must pass before Task 4

### Task 4: Add recommendations DB schema and CRUD
- [ ] add `recommendations` and `recommendation_blacklist` tables in `initTables()` in `internal/database/db.go`:
  ```sql
  CREATE TABLE IF NOT EXISTS recommendations (
      tvdb_id INTEGER PRIMARY KEY,
      tmdb_id INTEGER,
      title TEXT NOT NULL,
      original_title TEXT,
      overview TEXT,
      poster_url TEXT,
      year INTEGER,
      rating REAL,
      genres TEXT,
      score REAL NOT NULL,
      tracker_url TEXT NOT NULL,
      torrent_title TEXT,
      torrent_size TEXT,
      created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
  );
  CREATE TABLE IF NOT EXISTS recommendation_blacklist (
      tvdb_id INTEGER PRIMARY KEY,
      title TEXT,
      blacklisted_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
  );
  ```
- [ ] create `internal/database/recommendations.go` with struct `Recommendation` (all DB fields) and `BlacklistEntry` (tvdb_id, title, blacklisted_at)
- [ ] implement `GetRecommendations() ([]Recommendation, error)` — ordered by score DESC
- [ ] implement `ReplaceRecommendations(recs []Recommendation) error` — wrap in transaction: DELETE all, INSERT new batch
- [ ] implement `AddToBlacklist(tvdbID int, title string) error` — also DELETE from recommendations
- [ ] implement `RemoveFromBlacklist(tvdbID int) error`
- [ ] implement `GetBlacklist() ([]BlacklistEntry, error)` — ordered by blacklisted_at DESC
- [ ] implement `GetBlacklistedIDs() (map[int]bool, error)` — fast lookup set
- [ ] write tests in `internal/database/recommendations_test.go` using `setupTestDB` helper: CRUD happy paths, blacklist interaction (adding to blacklist removes from recommendations), GetBlacklistedIDs correctness
- [ ] run tests — must pass before Task 5

### Task 5: Create recommender package
- [ ] create `internal/recommender/recommender.go` with `Recommender` struct (fields: db, tmdb, kinozal `tracker.SeasonSearcher`)
- [ ] implement `New(db, tmdb, kinozal) *Recommender` constructor
- [ ] implement `Refresh() error` algorithm:
  1. Get all owned series with non-null tvdb_id via `SELECT id, tvdb_id, title FROM series WHERE tvdb_id IS NOT NULL`
  2. Load owned set: `map[int]bool` of all owned tvdb_ids
  3. Load blacklist via `db.GetBlacklistedIDs()`
  4. For each owned series: `tmdb.FindByTVDBID(tvdbID)` → `tmdb.GetRecommendations(tmdbID)` → for each recommended show, accumulate into `map[tmdbID]*candidate{frequency, sumRating, name, posterPath, voteAverage, ...}`
  5. Convert aggregates to list; compute `score = frequency * vote_average`; sort DESC
  6. Filter by `vote_average > 7.0`
  7. For each top candidate (up to 40, buffered): call `tmdb.FindByTVDBID` reverse? — no. Store tmdb_id only; when checking Kinozal use `title` for search. For owned/blacklist filter we need tvdb_id — call `tmdb` endpoint `/tv/{id}/external_ids` to resolve (add method to TMDB client)
  8. Skip if resolved tvdb_id is in owned set or blacklist
  9. `kinozal.FindSeasonTorrent(title, 1)` — skip if nil
  10. Collect until 20 found; call `db.ReplaceRecommendations(...)`
- [ ] ➕ add TMDB `GetExternalIDs(tmdbID int) (*ExternalIDs, error)` method to `internal/tmdb/client.go` — GET `/tv/{id}/external_ids`, returns struct with `tvdb_id`
- [ ] write tests in `internal/recommender/recommender_test.go`: mock TMDB + mock kinozal (implement `tracker.SeasonSearcher` as test fake), verify aggregation, filtering, blacklist exclusion, Kinozal filter, top-20 cutoff
- [ ] run tests — must pass before Task 6

### Task 6: Register scheduler task in main.go
- [ ] in `cmd/server/main.go`, after existing tracker/processor tasks: if `cfg.TMDBApiKey != ""`, initialize `tmdbClient := tmdb.NewClient(cfg.TMDBApiKey)`, get kinozal searcher from `trackerRegistry.Clients()` (same pattern as `WithSeasonSearcher`), create `rec := recommender.New(db, tmdbClient, kinozalSearcher)`
- [ ] register scheduler task:
  ```go
  sch.AddTask(scheduler.Task{
      Name:     "recommendation_refresh",
      Schedule: &scheduler.IntervalSchedule{Interval: 24 * time.Hour},
      Handler:  func(_ context.Context) error { return rec.Refresh() },
  })
  ```
- [ ] log info if feature is disabled due to missing TMDB key
- [ ] no unit test for main.go; covered by manual verification
- [ ] run `go build ./...` — must pass before Task 7

### Task 7: Add API handlers and routes
- [ ] add `*recommender.Recommender` field to `Server` struct in `internal/api/router.go`
- [ ] add `WithRecommender(*recommender.Recommender) ServerOption`
- [ ] pass recommender via option in `cmd/server/main.go`
- [ ] add routes inside existing `/api` group:
  ```go
  r.Get("/recommendations", s.handleGetRecommendations)
  r.Post("/recommendations/refresh", s.handleRefreshRecommendations)
  r.Get("/recommendations/blacklist", s.handleGetBlacklist)
  r.Post("/recommendations/blacklist", s.handleAddBlacklist)
  r.Delete("/recommendations/blacklist/{tvdb_id}", s.handleRemoveBlacklist)
  ```
- [ ] implement `handleGetRecommendations` — return `db.GetRecommendations()` as JSON array; return empty array if feature disabled
- [ ] implement `handleRefreshRecommendations` — 202 Accepted, run `rec.Refresh()` in goroutine; return 503 if recommender is nil
- [ ] implement `handleAddBlacklist` — body `{"tvdb_id": int, "title": string}`, call `db.AddToBlacklist`
- [ ] implement `handleRemoveBlacklist` — parse URL param tvdb_id, call `db.RemoveFromBlacklist`
- [ ] implement `handleGetBlacklist` — return `db.GetBlacklist()`
- [ ] write tests in `internal/api/router_test.go` for each new handler (happy path + validation errors + feature-disabled case)
- [ ] run tests — must pass before Task 8

### Task 8: Frontend — Recommendations tab
- [ ] add nav link in `web/templates/index.html` next to Seasons: `<a href="#/recommendations" data-page="recommendations">` with icon + label
- [ ] add `<section id="page-recommendations" class="page">` block with header (title, Refresh button, "Manage Blacklist" link), container `<div id="recommendations-list" class="recommendations-grid"></div>`, empty-state message
- [ ] in `web/static/app.js`:
  - [ ] add `/recommendations` and `/recommendations/blacklist` handlers in `router()`
  - [ ] implement `showRecommendationsPage()` and `loadRecommendations()` — fetch `/api/recommendations`, render cards
  - [ ] each card: poster, title, year, rating badge, genres list, anchor wrapping the card that opens `tracker_url` in new tab (`target="_blank" rel="noopener"`), × button on corner calling `blacklistRecommendation(tvdbID, title)`
  - [ ] `blacklistRecommendation`: POST `/api/recommendations/blacklist`, optimistic DOM removal, toast
  - [ ] `refreshRecommendations`: POST `/api/recommendations/refresh`, show spinner on button, toast after
- [ ] add CSS in `web/static/style.css`: reuse `.series-card` where possible, add `.recommendation-card` with `position: relative` for × button, `.btn-blacklist` icon button in top-right
- [ ] write small JS test? No JS test framework in project — rely on manual verification
- [ ] run all Go tests — must pass before Task 9

### Task 9: Frontend — Blacklist management page
- [ ] add `<section id="page-blacklist" class="page">` block in index.html with heading "Blacklisted Shows" and `<div id="blacklist-list"></div>`, back link to recommendations
- [ ] in `app.js` add `showBlacklistPage()` + `loadBlacklist()` fetching `/api/recommendations/blacklist`
- [ ] render rows: title + date + "Unblacklist" button
- [ ] `unblacklistShow(tvdbID)`: DELETE `/api/recommendations/blacklist/{tvdb_id}`, reload list
- [ ] style rows using existing list/table patterns
- [ ] run Go tests — must pass before Task 10

### Task 10: Verify acceptance criteria
- [ ] verify all requirements from Overview are implemented
- [ ] run full test suite: `go test ./...`
- [ ] run linter: `go vet ./...`
- [ ] manual: `docker-compose up -d --build`, check container logs for `Running scheduled task name=recommendation_refresh` on startup
- [ ] manual: open UI → Recommendations tab shows up to 20 cards
- [ ] manual: click card → Kinozal torrent page opens in new tab
- [ ] manual: click × → card disappears, show appears on Blacklist page
- [ ] manual: Blacklist page → Unblacklist button works, entry is removed
- [ ] manual: click Refresh button → logs show refresh task running

### Task 11: [Final] Update documentation
- [ ] update README.md env vars section with `TMDB_API_KEY`
- [ ] add short "Recommendations" feature description to README

*Note: ralphex automatically moves completed plans to `docs/plans/completed/`*

## Technical Details

### TMDB API endpoints used (v3, Bearer auth)
- `GET /find/{external_id}?external_source=tvdb_id` — map TVDB ID → TMDB ID
- `GET /tv/{tv_id}/recommendations?page=1` — get recommendations
- `GET /tv/{tv_id}/external_ids` — get tvdb_id for a TMDB show (needed for owned/blacklist filtering)

### Scoring
`score = frequency_in_recs × vote_average` — weights shows that appear across multiple seeds and have high rating.

### Poster URL
TMDB returns `poster_path` like `/abc.jpg`. Prepend `https://image.tmdb.org/t/p/w342` to build full URL. Store full URL in DB to avoid coupling frontend to this constant.

### Concurrency
Refresh must not run twice simultaneously. Use a simple `sync.Mutex` on `Recommender` — if TryLock fails, return immediately with info log.

### Rate limiting
TMDB: 40 req/10s. With ~50 seeds in library and 3 calls each (find, recommendations, external_ids for top candidates) we need roughly 150-200 calls. Simple 100ms delay between TMDB calls keeps us well below the limit.

### Kinozal filter
Reuse existing `tracker.SeasonSearcher.FindSeasonTorrent(title, 1)`. If the recommendation has an `original_name` that differs from `name`, try `name` first then `original_name` as fallback (same pattern as `handleGetNextSeasons`).

## Post-Completion
*Items requiring manual intervention or external systems — no checkboxes, informational only*

**Environment setup:**
- User must add `TMDB_API_KEY=<key>` to `.env` file before container rebuild
- Rebuild: `docker-compose up -d --build`

**Manual verification:**
- Refresh task produces 0–20 recommendations depending on library size and TMDB data
- For a fresh library with <3 shows, recommendations may be sparse — this is expected
- If TMDB is unreachable, scheduler logs error and retains previous cache
- Blacklisted shows should stay blacklisted across container restarts (permanence check)

**Future enhancements (out of scope):**
- Click "Add to library" instead of opening Kinozal page (auto-adds series + starts download)
- Recommendation strength indicator (how many of your shows led to this one)
- Per-genre filter on recommendations page
