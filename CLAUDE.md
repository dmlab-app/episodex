# EpisodeX

Local TV series tracking service with automatic folder scanning.

## Stack

- **Backend**: Go 1.25, chi router, SQLite (modernc.org/sqlite - pure Go)
- **Frontend**: Vanilla JS, HTML, CSS
- **Deploy**: Docker Compose with reproxy (network: `gateway_gateway`)

## Project Structure

```
cmd/server/main.go              - Entry point
internal/
  config/config.go              - Environment config
  database/db.go                - SQLite setup, tables, migrations
  database/backup.go            - Nightly backup with integrity check
  database/series.go            - Series/season ORM methods
  database/media_files.go       - Media file tracking
  api/router.go                 - HTTP handlers, routing
  api/handlers_series.go        - Extended series handlers (sync, search)
  api/sync.go                   - Shared TVDB metadata sync logic
  tvdb/client.go                - TVDB API client
  scanner/scanner.go            - Folder scanner with torrent name parsing
  audio/audio.go                - AudioCutter (mkvmerge/ffmpeg)
  hash/hash.go                  - File hashing utilities
  scheduler/scheduler.go        - Background tasks (scan, updates)
web/
  templates/index.html          - SPA
  static/style.css              - Plex-inspired dark theme
  static/app.js                 - Frontend app
```

## Key Features

- Auto-scan media folder every hour
- TVDB integration for metadata and new season detection
- Per-season voice dubbing tracking
- Built-in AudioCutter (mkvmerge/ffmpeg)
- SQLite with nightly backups (keep 10)

## Commands

```bash
make run          # Run locally
make lint         # golangci-lint
make test         # Run tests
docker-compose up -d   # Deploy with reproxy
```

## Config (.env)

```
MEDIA_PATH_HOST=/Volumes/Plex/TV Show
TVDB_API_KEY=xxx
PORT=8080
HOST=0.0.0.0
BACKUP_RETENTION=10
BACKUP_HOUR=3
SCAN_INTERVAL_HOURS=1
TVDB_CHECK_HOUR=5
```

## API Endpoints

- `GET /api/health` — health check
- `GET /api/series` — list all series with season counts
- `POST /api/series` — create series
- `GET /api/series/{id}` — series detail with metadata, characters, artwork
- `DELETE /api/series/{id}` — delete series
- `POST /api/series/{id}/match` — match series to TVDB
- `POST /api/series/{id}/sync` — sync metadata from TVDB
- `GET /api/series/{id}/seasons` — list seasons (owned vs locked)
- `GET /api/series/{id}/seasons/{num}` — season detail
- `PUT /api/series/{id}/seasons/{num}` — update season (voice actor)
- `POST /api/series/{id}/seasons/{num}/rescan` — rescan season folder
- `GET /api/series/{id}/seasons/{num}/audio` — list audio tracks
- `POST /api/series/{id}/seasons/{num}/audio/preview` — generate audio preview
- `POST /api/series/{id}/seasons/{num}/audio/process` — process audio (SSE); body: `{track_id, keep_original}`
- `GET /api/voices` — list voice actor studios
- `GET /api/audio/preview/{hash}` — serve audio preview file
- `GET /api/alerts` — list alerts
- `POST /api/alerts/{id}/dismiss` — dismiss alert
- `POST /api/scan/trigger` — trigger folder scan
- `GET /api/updates` — get new season updates
- `POST /api/updates/check` — check for TVDB updates
- `GET /api/search` — search TVDB for series

## Rules

- **Git workflow**: feature branch → changes → PR to main. Never commit directly to main.
- **No destructive commands** (rename, delete, move, script execution) without explicit user approval. Show the plan first.
- **Media files**: ALWAYS use `mv`, never `cp`. Never copy media files unless explicitly asked.
- **Stay within scope.** Don't make changes beyond what was asked. Mention improvements, don't act on them.
- **Credentials**: if you need API keys, container names, or server addresses — ASK, don't guess. Check docker-compose.yml for container names.

## Notes

- Uses `gateway_gateway` Docker network for reproxy
- Voice studios pre-seeded on first run (LostFilm, Amedia, etc.)
- Frontend uses Inter + JetBrains Mono fonts
- SSE endpoints (audio process) registered outside `/api` group to bypass 60s timeout middleware; server WriteTimeout is 120s, SSE handler uses `http.ResponseController.SetWriteDeadline` to disable per-request
- Requires mkvmerge (MKVToolNix) and ffmpeg in PATH for audio features
- SQLite uses `MaxOpenConns(1)`: always close rows before Exec in the same loop to avoid deadlocks
- `UpsertSeason`/`UpsertEpisode` use `COALESCE(?, column)` so partial updates don't overwrite existing metadata with NULL
- Database backup uses `VACUUM INTO` (atomic, includes WAL contents)
- TVDB client is thread-safe: token refresh protected by `sync.Mutex`
- golangci-lint uses v2 config format (`version: "2"`) — requires golangci-lint v2.x
