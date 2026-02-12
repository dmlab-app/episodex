# EpisodeX — Complete Overhaul

## Overview
Fix critical data layer bugs, redesign UI in Plex style, enhance series detail page with full metadata, add voice selection on season detail, and polish frontend UX.

The project has a split-brain problem: the scanner writes to the `seasons` table but all API handlers read from the deprecated `watched_seasons` table, causing 404 errors and missing data. The UI also needs a full visual overhaul to match Plex aesthetics and several missing features need to be wired up.

## Context (from discovery)
- **Critical bug**: `internal/api/router.go` — all handlers read `watched_seasons`, scanner writes to `seasons`
- **Database schema**: `internal/database/db.go` — has both `watched_seasons` (legacy) and `seasons` (current)
- **Scanner**: `internal/scanner/scanner.go` — uses `database.UpsertSeason()` writing to `seasons`
- **ORM layer**: `internal/database/series.go` — has proper Season struct and methods for `seasons` table
- **Frontend**: `web/static/app.js`, `web/static/style.css`, `web/templates/index.html`
- **Audio**: `internal/audio/audio.go` — fully implemented, UI has white theme mismatch
- **Voice actors**: Pre-seeded in DB (LostFilm, Amedia, etc.), `voice_actor_id` column exists in `seasons`, no UI
- **Extended data**: `internal/api/handlers_series.go` — `handleGetSeriesExtended()` and `handleSyncSeriesFromTVDB()` exist but aren't used by frontend

## Development Approach
- **Testing approach**: Regular (code first, then tests)
- Complete each task fully before moving to the next
- Make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
- **CRITICAL: all tests must pass before starting next task**
- **CRITICAL: update this plan file when scope changes during implementation**
- Run tests after each change
- Maintain backward compatibility during migration

## Testing Strategy
- **Unit tests**: required for every task with code changes
- **Integration tests**: test API handlers with real SQLite DB
- **Manual verification**: check UI in browser after frontend changes

## Progress Tracking
- Mark completed items with `[x]` immediately when done
- Add newly discovered tasks with + prefix
- Document issues/blockers with ! prefix
- Update plan if implementation deviates from original scope

## Implementation Steps

### Task 1: Migrate API handlers from watched_seasons to seasons table
- [x] In `internal/api/router.go` — update `handleListSeries`: replace `watched_seasons` subquery with `seasons` table count
- [x] Update `handleGetSeries`: read seasons from `seasons` table instead of `watched_seasons`, include `voice_actor_id` and voice actor name via JOIN
- [x] Update `handleListSeasons`: read owned seasons from `seasons` table, JOIN with `voice_actors` for studio name
- [x] Update `handleGetSeason`: query `seasons` table for folder_path and season data
- [x] Update `handleGetAudioTracks`: get folder_path from `seasons` table
- [x] Update `handleRescanSeason`: get folder_path from `seasons` table
- [x] Update `handleProcessAudioStream`: get folder_path from `seasons` table
- [x] Update `handleGetUpdates`: count watched seasons from `seasons` table
- [x] Update `handleMatchSeries`: merge operations via `seasons` table instead of `watched_seasons`
- [x] Add data migration in `internal/database/db.go`: on startup, copy any data from `watched_seasons` that doesn't exist in `seasons`
- [x] Write tests for handleListSeries handler (returns correct season counts from seasons table)
- [x] Write tests for handleGetSeries handler (returns seasons data correctly)
- [x] Write tests for handleListSeasons handler (owned vs locked seasons)
- [x] Run `make test` — must pass before next task

### Task 2: Plex-inspired CSS redesign — variables, base, header
- [x] In `web/static/style.css` — replace all CSS variables with Plex palette: bg-deep `#1a1c22`, bg-primary `#1f2326`, bg-surface `#282c37`, bg-elevated `#323640`, bg-hover `#3d4250`, accent `#e5a00d`, accent-dim `#cc7b19`
- [x] Replace fonts: use `Inter` for body and display, keep `JetBrains Mono` for monospace. Update Google Fonts import and `--font-display`, `--font-body` variables
- [x] Update `web/templates/index.html`: change Google Fonts link to load Inter instead of Outfit/Unbounded/DM Sans
- [x] Update body base styles: new background, remove noise texture overlay (`body::before`), simplify ambient gradient
- [x] Redesign header `.header`: Plex-style navbar — solid dark bg, cleaner look, accent on active nav
- [x] Update button styles `.btn-*`: Plex-like buttons — rounded, clean, accent orange for primary
- [x] Verify in browser: header renders correctly, colors are Plex-like
- [x] No tests needed (CSS-only changes)

### Task 3: Plex-inspired CSS redesign — cards, grids, components
- [x] Redesign series cards `.series-card`: Plex poster style — clean rounded corners, subtle shadow, hover overlay with gradient, remove neon glow border effect
- [x] Update stats bar `.stats-bar`: Plex-style stat blocks
- [x] Update filters bar `.filters-bar`, `.filter-btn`, `.search-box`: Plex-style search and filters
- [x] Update season cards `.season-card`: Plex style with clean hover, no exaggerated glow
- [x] Redesign audio tracks panel `.audio-tracks-panel`: replace white theme with dark Plex theme — dark bg, light text, accent highlights, remove all hardcoded light colors (#ffffff, #333, #f8f9fa, #666, #f0f4ff, #667eea, #f093fb, #f5576c, #fff3e0, #ffa726, #38ef7d)
- [x] Update modals `.modal-*`: dark Plex style
- [x] Update toasts `.toast-*`: Plex-consistent notifications
- [x] Update search results `.search-result`, match results `.match-result-item`: Plex style
- [x] Update progress container `.progress-*`: Plex-style progress bar
- [x] Verify in browser: full app looks consistent Plex-style
- [x] No tests needed (CSS-only changes)

### Task 4: Series detail page — API enhancement
- [x] In `internal/api/router.go` — update `handleGetSeries` to return full metadata: overview, year, runtime, rating, genres, networks, studios, backdrop_url, slug (fields already exist in series table)
- [x] Add characters endpoint or include top characters in series response — query `series_characters` table, limit 10, ordered by sort_order
- [x] Add artwork endpoint or include poster/backdrop from `artworks` table as fallback
- [x] Write tests for enhanced handleGetSeries (verify all metadata fields returned)
- [x] Write tests for characters data inclusion
- [x] Run `make test` — must pass before next task

### Task 5: Series detail page — frontend redesign
- [x] In `web/templates/index.html` — redesign `#page-series-detail` section: add hero backdrop area, metadata section (year, rating, genres, networks), overview text area, characters row
- [x] In `web/static/app.js` — update `loadSeriesDetail()`: call enhanced GET endpoint, populate backdrop, overview, year, rating, genres, networks, characters
- [x] Update `renderSeasons()`: show voice actor badge on owned season cards (small label like "LostFilm" at bottom of card)
- [x] In `web/static/style.css` — add styles for hero backdrop (full-width image with gradient overlay), metadata tags, character avatars row, voice badge on season cards
- [x] Add "Sync with TVDB" button on series detail that calls `POST /api/series/{id}/sync`
- [x] Verify in browser: series detail page shows full metadata, backdrop, characters, voice badges
- [x] No code tests needed (frontend-only), manual verification

### Task 6: Voice selection on season detail page
- [x] In `internal/api/router.go` — add `PUT /api/series/{id}/seasons/{num}` handler to update voice_actor_id on season
- [x] Add `GET /api/voices` handler to return list of voice actor studios from `voice_actors` table
- [x] In `web/static/app.js` — update `loadSeasonDetail()`: load voices list, load current season voice, render dropdown selector
- [x] In `web/static/app.js` — add `updateSeasonVoice(seriesId, seasonNum, voiceActorId)` function to save selection via PUT
- [x] In `web/static/style.css` — style voice selector dropdown in Plex dark theme, integrate naturally into season detail layout
- [x] In `web/templates/index.html` or `createSeasonDetailPage()` — add voice selector UI element above audio tracks panel
- [x] Write tests for PUT season voice endpoint (success, invalid voice_id, season not found)
- [x] Write tests for GET voices endpoint
- [x] Run `make test` — must pass before next task

### Task 7: Frontend polish and bug fixes
- [x] In `web/static/app.js` — fix season detail page DOM: create once in index.html, show/hide like other pages instead of dynamic createElement/remove
- [x] Fix back button navigation: wire `#back-to-series` click in DOMContentLoaded, ensure back from season goes to series detail
- [x] Fix sort select CSS: `.sort-select` is a class on the `<select>` element directly, but CSS targets `.sort-select select` — fix selector to match actual HTML
- [x] Add graceful handling on season detail: if season is not owned (no folder_path), show message "Season files not available" instead of error toast
- [x] Improve updates page: show which specific season numbers are new, not just count
- [x] Add a proper SVG placeholder for series without posters (inline SVG in app.js or a static file)
- [x] Fix nav link active state: clicking nav links should use hash navigation, not `onclick="navigateTo()"` which doesn't exist (index.html line 17)
- [x] Verify in browser: all navigation works, no console errors, graceful fallbacks
- [x] No code tests needed (frontend-only), manual verification

### Task 8: Verify acceptance criteria
- [x] Verify: clicking on any owned season opens season detail without errors
- [x] Verify: audio tracks load correctly for seasons with MKV files
- [x] Verify: voice selection saves and displays correctly
- [x] Verify: series detail shows full metadata (overview, genres, year, rating, characters)
- [x] Verify: entire UI is consistent Plex dark theme (no white panels, no style mismatches)
- [x] Verify: updates page correctly shows new seasons
- [x] Run full test suite `make test`
- [x] Run linter `make lint` — all issues must be fixed

### Task 9: [Final] Update documentation
- [ ] Update CLAUDE.md: remove "Not Yet Implemented" items that are now done, update API endpoints list
- [ ] Clean up any TODO comments left in code

## Technical Details

### Database migration (watched_seasons → seasons)
The `seasons` table has: `id, series_id, season_number, folder_path, voice_actor_id, owned, image, created_at, updated_at`
The `watched_seasons` table has: `id, series_id, season_number, folder_path, discovered_at`

Migration: INSERT INTO seasons (series_id, season_number, folder_path, owned, created_at) SELECT series_id, season_number, folder_path, 1, discovered_at FROM watched_seasons WHERE NOT EXISTS matching row in seasons.

### Plex color palette
- Deep background: `#1a1c22`
- Primary background: `#1f2326`
- Surface: `#282c37`
- Elevated: `#323640`
- Hover: `#3d4250`
- Accent (Plex orange): `#e5a00d`
- Accent hover: `#cc7b19`
- Text primary: `#eaeaea`
- Text secondary: `#999999`
- Text muted: `#666666`
- Border: `rgba(255,255,255,0.08)`

### Voice selection flow
1. Season detail page loads → GET /api/voices for dropdown options
2. Current voice shown from season data (voice_actor_id JOIN voice_actors)
3. User selects → PUT /api/series/{id}/seasons/{num} with {voice_actor_id: N}
4. Series detail page shows voice badge on season cards

## Post-Completion

**Manual verification:**
- Test full flow: browse series list → click series → view seasons → open season → select voice → process audio
- Test on different screen sizes (responsive)
- Test with series that have no TVDB match (unmatched cards)
- Test scan trigger and verify new data appears correctly

**Future enhancements (out of scope):**
- Telegram notifications for new seasons
- Plex/Jellyfin integration
- Episode-level tracking
