# EpisodeX

Local TV series tracking service with automatic folder scanning.

## Stack

- **Backend**: Go 1.25, chi router, SQLite (modernc.org/sqlite - pure Go)
- **Frontend**: Vanilla JS, HTML, CSS
- **Deploy**: Docker Compose with reproxy (network: `gateway_gateway`)

## Project Structure

```
cmd/server/main.go          - Entry point
internal/
  config/config.go          - Environment config
  database/db.go            - SQLite setup, tables
  database/backup.go        - Nightly backup with integrity check
  api/router.go             - HTTP handlers
  scheduler/scheduler.go    - Background tasks
web/
  templates/index.html      - SPA
  static/style.css          - Cinematic Noir theme
  static/app.js             - Frontend app
```

## Key Features

- Auto-scan media folder every hour
- TVDB integration for metadata and new season detection
- Per-season voice dubbing tracking
- Built-in AudioCutter (mkvmerge/ffmpeg)
- SQLite with nightly backups (keep 10)

## Not Yet Implemented

- `internal/tvdb/client.go` - TVDB API client
- `internal/scanner/scanner.go` - Folder scanner with go-parse-torrent-name
- `internal/audio/` - AudioCutter (analyzer, processor)
- Full series CRUD handlers
- Search endpoint (proxy to TVDB)

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
BACKUP_RETENTION=10
SCAN_INTERVAL_HOURS=1
```

## API Endpoints

Working:
- `GET /api/health`
- `GET /api/series` (empty)
- `GET /api/alerts`
- `GET/POST/DELETE /api/voices`

Pending:
- Series CRUD with TVDB
- Seasons management
- Audio processing with SSE progress

## Rules

- **Git workflow**: feature branch → changes → PR to main. Never commit directly to main.
- **No destructive commands** (rename, delete, move, script execution) without explicit user approval. Show the plan first.
- **Media files**: ALWAYS use `mv`, never `cp`. Never copy media files unless explicitly asked.
- **Stay within scope.** Don't make changes beyond what was asked. Mention improvements, don't act on them.
- **Credentials**: if you need API keys, container names, or server addresses — ASK, don't guess. Check docker-compose.yml for container names.

## Notes

- Uses `gateway_gateway` Docker network for reproxy
- Voice studios pre-seeded on first run (LostFilm, Amedia, etc.)
- Frontend uses Outfit + JetBrains Mono fonts
