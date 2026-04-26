# EpisodeX

Local web service for tracking TV series with automatic media folder scanning.

## Features

- Automatic scanning of TV shows folder
- TheTVDB integration for metadata (series info, characters, artwork)
- New season and episode update notifications
- Per-season voice dubbing tracking
- AudioCutter for audio track management (mkvmerge/ffmpeg)
- Automatic database backups
- qBittorrent integration (tracker links on season pages)
- Kinozal tracker integration — auto-redownload updated torrents with new episodes
- Seasons tab — discover next season to download via Kinozal search
- Recommendations tab — TMDB-powered TV show suggestions based on your library, with Kinozal availability filter and blacklist management
- Plex-inspired dark theme web interface

## Quick Start

### Requirements

- Go 1.25 or higher
- Docker (optional)
- mkvmerge (MKVToolNix) — for audio track management
- ffmpeg — for audio preview generation

### Local Development

1. Clone the repository
2. Copy `.env.example` to `.env` and configure:

```bash
cp .env.example .env
```

3. Install dependencies:

```bash
make deps
```

4. Run the server:

```bash
make run
```

Server will be available at: http://localhost:8080

### Docker

1. Create `.env` file with settings
2. Run via Docker Compose:

```bash
docker-compose up -d
```

## Project Structure

```
episodex/
├── cmd/server/          # Application entry point
├── internal/
│   ├── api/            # HTTP API handlers
│   ├── config/         # Configuration
│   ├── database/       # Database and backups
│   ├── scheduler/      # Task scheduler
│   ├── scanner/        # Media folder scanning
│   ├── tvdb/           # TVDB API client
│   ├── tmdb/           # TMDB API client
│   ├── recommender/    # Recommendations aggregation engine
│   ├── hash/           # File hashing utilities
│   ├── audio/          # AudioCutter (mkvmerge/ffmpeg)
│   ├── qbittorrent/    # qBittorrent Web API client
│   └── tracker/        # Tracker integration (Kinozal, etc.)
├── web/
│   ├── static/         # CSS, JS
│   └── templates/      # HTML templates
└── data/               # Database and backups
```

## Makefile Commands

```bash
make build          # Build binary
make run            # Run server
make dev            # Run with auto-reload (requires air)
make test           # Run tests
make lint           # Check code with linter
make clean          # Clean build artifacts
make docker-build   # Build Docker image
make docker-up      # Start in Docker
make docker-down    # Stop Docker container
```

## Configuration

All settings are configured via environment variables (`.env` file):

- `MEDIA_PATH` - path to TV shows folder
- `TVDB_API_KEY` - TheTVDB API key
- `PORT` - web server port (default 8080)
- `HOST` - server bind address (default 0.0.0.0)
- `DB_PATH` - path to SQLite database
- `BACKUP_PATH` - backup folder
- `BACKUP_RETENTION` - number of backups to keep (default 10)
- `BACKUP_HOUR` - hour of day for backup (default 3, range 0-23)
- `SCAN_INTERVAL_HOURS` - scanning interval in hours
- `TVDB_CHECK_HOUR` - hour of day for TVDB update check (default 5, range 0-23)
- `QBIT_URL` - qBittorrent Web UI URL (optional, enables qBittorrent integration)
- `QBIT_USER` - qBittorrent username (required when QBIT_URL is set)
- `QBIT_PASSWORD` - qBittorrent password (required when QBIT_URL is set)
- `KINOZAL_USER` - Kinozal username (optional, enables tracker auto-redownload)
- `KINOZAL_PASSWORD` - Kinozal password (required when KINOZAL_USER is set)
- `TRACKER_CHECK_INTERVAL_HOURS` - how often to check trackers for new episodes (default 6)
- `TMDB_API_KEY` - TMDB v3 API key (optional, enables Recommendations tab)

## API Endpoints

### System

- `GET /api/health` - health check
- `GET /api/alerts` - system alerts
- `POST /api/alerts/:id/dismiss` - dismiss alert
- `POST /api/scan/trigger` - trigger folder scan

### Series

- `GET /api/series` - list series with season counts
- `POST /api/series` - create series
- `GET /api/series/:id` - series detail with metadata, characters, artwork
- `DELETE /api/series/:id` - delete series with all media files from disk and remove linked qBittorrent torrents
- `POST /api/series/:id/match` - match series to TVDB
- `GET /api/search` - search TVDB for series

### Seasons

- `GET /api/series/:id/seasons` - list seasons (owned vs locked)
- `GET /api/series/:id/seasons/:num` - season detail
- `PUT /api/series/:id/seasons/:num` - update season (voice actor)
- `DELETE /api/series/:id/seasons/:num` - delete a single season: removes media files, season folder, qBittorrent torrent, and DB rows

### Audio

- `GET /api/series/:id/seasons/:num/audio` - list audio tracks
- `POST /api/series/:id/seasons/:num/audio/preview` - generate audio preview
- `POST /api/series/:id/seasons/:num/audio/process` - process audio (SSE); body: `{track_id, keep_original}`
- `GET /api/audio/preview/:hash` - serve audio preview file

### Tracker

- `GET /api/series/:id/seasons/:num/tracker` - get tracker URL for season (from qBittorrent)

### Voice Actors

- `GET /api/voices` - list voice actor studios

### Updates

- `GET /api/updates` - get new season updates (with aired episode counts per season)
- `POST /api/updates/check` - check for TVDB episode updates

### Next Seasons

- `GET /api/next-seasons` - get next season to download for each series (with Kinozal torrent link)

### Recommendations

- `GET /api/recommendations` - list current TMDB-powered recommendations
- `POST /api/recommendations/refresh` - trigger refresh of recommendations
- `GET /api/recommendations/blacklist` - list blacklisted shows
- `POST /api/recommendations/blacklist` - add show to blacklist; body: `{tvdb_id, title}`
- `DELETE /api/recommendations/blacklist/{tvdb_id}` - remove show from blacklist

## Technologies

- **Backend**: Go 1.25
- **Database**: SQLite (modernc.org/sqlite - pure Go, no CGO)
- **Router**: chi v5
- **Frontend**: Vanilla JS + HTML + CSS
- **Linter**: golangci-lint

## Background Tasks

On startup:

- Automatic TVDB metadata sync for series added by scanner but not yet fully synced

Scheduler automatically runs:

- Media folder scanning (every hour)
- TVDB updates check (daily)
- Tracker check for new episodes (every 6 hours, configurable)
- Post-download audio processing for completed torrents
- Recommendations refresh (every 24 hours, when TMDB_API_KEY is set)
- Database backup (daily at 3:00)
- Old backup rotation

## Development

### Install Tools

```bash
make install-tools
```

Installs:
- golangci-lint
- goimports
- air (auto-reload)

### Run with auto-reload

```bash
make dev
```

### Check Code

```bash
make lint
make test
```

## Project Status

All core features implemented:

- [x] Project structure and configuration
- [x] Database (SQLite) with backups
- [x] HTTP server with middleware
- [x] Task scheduler
- [x] Docker support with reproxy
- [x] TVDB integration (metadata, characters, artwork)
- [x] Media folder scanning with torrent name parsing
- [x] Series API (CRUD, match, sync)
- [x] Voice dubbing management
- [x] AudioCutter (mkvmerge/ffmpeg)
- [x] Plex-inspired dark theme UI
- [x] qBittorrent integration (tracker links)
- [x] Kinozal tracker auto-redownload
- [x] Seasons tab (next season discovery via Kinozal)
- [x] Recommendations tab (TMDB-powered)
- [x] Linting and tests

## License

MIT
