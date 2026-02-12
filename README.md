# EpisodeX

Local web service for tracking watched TV series with automatic media folder scanning.

## Features

- Automatic scanning of TV shows folder
- TheTVDB integration for metadata
- New season notifications
- Per-season voice dubbing tracking
- AudioCutter for audio track management
- Automatic database backups
- Modern web interface

## Quick Start

### Requirements

- Go 1.22 or higher
- Docker (optional)

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
│   ├── scanner/        # Media scanning (TODO)
│   ├── tvdb/           # TVDB API client (TODO)
│   └── audio/          # AudioCutter (TODO)
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
- `DB_PATH` - path to SQLite database
- `BACKUP_PATH` - backup folder
- `BACKUP_RETENTION` - number of backups to keep (default 10)
- `SCAN_INTERVAL_HOURS` - scanning interval in hours

## API Endpoints

### System

- `GET /api/health` - health check
- `GET /api/alerts` - system alerts
- `POST /api/alerts/:id/dismiss` - dismiss alert

### Series (in development)

- `GET /api/series` - list series
- `POST /api/series` - add series
- `GET /api/series/:id` - series details
- `DELETE /api/series/:id` - delete series

## Technologies

- **Backend**: Go 1.22
- **Database**: SQLite (modernc.org/sqlite - pure Go, no CGO)
- **Router**: chi v5
- **Frontend**: Vanilla JS + HTML + CSS
- **Linter**: golangci-lint

## Background Tasks

Scheduler automatically runs:

- Media folder scanning (every hour)
- TVDB updates check (daily)
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

**Phase 1 (Infrastructure)**: Completed

- [x] Project structure
- [x] Configuration
- [x] Database (SQLite)
- [x] Backup system
- [x] HTTP server with middleware
- [x] Task scheduler
- [x] Docker support
- [x] Linting

**Phases 2-6**: Planned

- [ ] TVDB integration
- [ ] Media scanning
- [ ] Series API
- [ ] Voice dubbing management
- [ ] AudioCutter
- [ ] Full UI

## License

MIT
