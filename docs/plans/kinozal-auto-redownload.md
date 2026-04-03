# Kinozal Integration — Auto-Redownload Updated Torrents

## Overview
When a downloaded season has new aired episodes (detected via TVDB), periodically check the Kinozal torrent page (URL already stored in `tracker_url`) to see if the torrent has been updated with new episodes. If so, download the new .torrent file and add it to qBittorrent, replacing the old torrent. This automates the manual process of checking tracker pages and re-downloading updated releases.

Scope: only mid-season updates (new episodes added to existing torrent), NOT new seasons.

## Architecture — Modular Tracker Interface
Kinozal is the first tracker, but the system must support adding other trackers (rutracker, etc.) without changing the check/download logic.

```go
// internal/tracker/tracker.go
type TrackerClient interface {
    // CanHandle returns true if this client handles the given tracker URL
    CanHandle(trackerURL string) bool
    // GetEpisodeCount fetches the torrent page and returns the number of episodes available
    GetEpisodeCount(trackerURL string) (int, error)
    // DownloadTorrent downloads the .torrent file by tracker URL, returns raw bytes
    DownloadTorrent(trackerURL string) ([]byte, error)
}
```

- `internal/tracker/tracker.go` — interface definition + registry that routes URLs to the right client
- `internal/tracker/kinozal/` — Kinozal implementation of TrackerClient
- Future trackers: `internal/tracker/rutracker/`, etc. — just implement the interface and register
- The scheduled check logic works with `TrackerClient` interface, doesn't know about Kinozal specifics

## Context
- `tracker_url` and `torrent_hash` are already stored in the `seasons` table
- Kinozal torrent page title contains episode count: `"1-17 серии из 18"` or `"1-8 серии из 8"`
- Kinozal requires login (username/password) → cookie-based auth
- qBittorrent integration exists: can delete and add torrents
- Torrent categories in qBittorrent determine save path
- Config: new env vars `KINOZAL_USER`, `KINOZAL_PASSWORD`

## Development Approach
- **Testing approach**: TDD (tests first)
- Complete each task fully before moving to the next
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**
- **CRITICAL: update this plan file when scope changes during implementation**
- Maintain backward compatibility — Kinozal integration is optional

## Implementation Steps

### Task 1: TrackerClient interface and registry
- [x] create `internal/tracker/tracker.go` with `TrackerClient` interface (CanHandle, GetEpisodeCount, DownloadTorrent)
- [x] create `Registry` struct that holds multiple `TrackerClient` implementations
- [x] implement `Registry.GetClient(trackerURL string)` — returns the client that can handle the URL
- [x] write tests for registry routing
- [x] run tests — must pass before next task

### Task 2: Config — add Kinozal credentials
- [x] write tests for config loading with KINOZAL_USER, KINOZAL_PASSWORD env vars
- [x] write tests for validation: both required when either is set
- [x] add `KinozalUser`, `KinozalPassword` fields to Config struct
- [x] implement loading and validation
- [x] run tests — must pass before next task

### Task 3: Kinozal client — authentication
- [ ] create `internal/tracker/kinozal/client.go` implementing `TrackerClient`
- [ ] write tests for `Login()` — success returns session cookie, failure returns error
- [ ] write tests for automatic re-login on auth failure
- [ ] implement `Login()` — POST to `/takelogin.php` with `username` + `password` form data
- [ ] implement `CanHandle()` — returns true for URLs containing `kinozal.tv`
- [ ] run tests — must pass before next task

### Task 4: Kinozal client — parse episode count from torrent page
- [ ] write tests for `GetEpisodeCount(trackerURL string)` — parses title, extracts episode count
- [ ] write tests for various title formats:
  - `"Сериал (1 сезон: 1-8 серии из 8)"` → 8
  - `"Сериал (2 сезон: 1-17 серии из 18)"` → 17
  - `"Сериал (1 сезон: 1-6 серий из 10)"` → 6
  - title without episode info → 0
- [ ] implement: fetch page, parse title tag, extract max episode number with regexp
- [ ] run tests — must pass before next task

### Task 5: Kinozal client — download .torrent file
- [ ] write tests for `DownloadTorrent(trackerURL string)` — returns torrent file bytes
- [ ] write tests for auth failure and retry
- [ ] implement: parse ID from URL, GET `/download.php?id=XXXXX` with session cookie
- [ ] run tests — must pass before next task

### Task 6: qBittorrent client — add torrent and set file priorities
- [ ] write tests for `AddTorrent(torrentData []byte, category string, savePath string)` — returns torrent hash
- [ ] write tests for `GetTorrentFiles(hash string)` — returns list of files with index and name
- [ ] write tests for `SetFilePriority(hash string, fileIndexes []int, priority int)` — priority 0 = don't download
- [ ] implement: POST to `/api/v2/torrents/add` with multipart form data
- [ ] implement: GET `/api/v2/torrents/files?hash=...`
- [ ] implement: POST `/api/v2/torrents/filePrio` with hash, file indexes, priority
- [ ] run tests — must pass before next task

### Task 7: Scheduled check — compare episodes and trigger redownload
- [ ] write check logic using `TrackerClient` interface (not Kinozal-specific):
  - for each season with `tracker_url`:
    - get client from registry via `CanHandle`
    - call `GetEpisodeCount` to get available episodes on tracker
    - compare with max episode number on disk (ptn lib)
    - if tracker has more → trigger redownload
- [ ] write redownload logic:
  - download .torrent from tracker via `DownloadTorrent`
  - delete old torrent from qBit (by `torrent_hash`)
  - add new torrent to qBit with same category
  - get file list from new torrent
  - parse episode numbers from file names (ptn lib)
  - files already in `processed_files` → set priority 0 (skip download)
  - only new episodes download
  - update `torrent_hash` in seasons table
- [ ] write tests for the comparison and trigger logic (mock tracker + qbit)
- [ ] wire into scheduler as periodic task
- [ ] add config: `TRACKER_CHECK_INTERVAL_HOURS` (default: 6)
- [ ] run tests — must pass before next task

### Task 8: Post-download audio processing
- [ ] write logic: after torrent completes, process only new files
  - periodically check if torrent is completed (qBit API: torrent state = "uploading" or "stalledUP")
  - get `voice_actor_id` from season → determine which track to keep
  - run AudioCutter only on files NOT in `processed_files`
  - mark new files in `processed_files`
- [ ] write tests for post-download processing logic
- [ ] add to scheduler or as part of tracker check cycle
- [ ] run tests — must pass before next task

### Task 9: Wire into main.go
- [ ] create tracker registry
- [ ] initialize Kinozal client when credentials configured, register in registry
- [ ] add scheduled task for tracker check
- [ ] run tests — must pass before next task

### Task 10: Verify acceptance criteria
- [ ] verify Kinozal check runs on schedule
- [ ] verify no errors when Kinozal not configured
- [ ] verify torrent is re-added with correct category
- [ ] verify already-processed files are skipped (priority 0)
- [ ] verify only new episodes are downloaded and processed
- [ ] run full test suite
- [ ] run linter — all issues must be fixed

### Task 11: [Final] Update documentation
- [ ] add KINOZAL_USER, KINOZAL_PASSWORD, TRACKER_CHECK_INTERVAL_HOURS to .env.example
- [ ] update README.md

## Technical Details

### Kinozal Auth
```
POST https://kinozal.tv/takelogin.php
Content-Type: application/x-www-form-urlencoded
Body: username=XXX&password=YYY
Response: Set-Cookie with session
```

### Kinozal Torrent Download
```
GET https://kinozal.tv/download.php?id=2107649
Cookie: <session cookie>
Response: application/x-bittorrent
```

### Episode Count Parsing
Torrent title format: `"Название (N сезон: 1-X серии из Y)"`
- X = episodes currently in torrent
- Y = total expected episodes in season
- Extract X with regexp: `(\d+)-(\d+)\s+сери[ийя]`

### qBittorrent Add Torrent
```
POST /api/v2/torrents/add
Content-Type: multipart/form-data
Fields: torrents (file), category, savepath
```

### Check Flow
```
For each season WHERE tracker_url IS NOT NULL:
  1. Find TrackerClient via registry.GetClient(tracker_url)
  2. Call client.GetEpisodeCount(tracker_url) → episodes on tracker (X)
  3. Get max episode on disk (ptn parse file names from media_files)
  4. If X > max_on_disk:
     a. Download .torrent via client.DownloadTorrent(tracker_url)
     b. Get category from existing qBit torrent (or default)
     c. Delete old torrent from qBit (by torrent_hash)
     d. Add new .torrent to qBit with same category → get new hash
     e. Get file list from new torrent
     f. For each file: parse episode number, check processed_files
        - if in processed_files → set priority 0 (skip)
        - else → leave priority normal (download)
     g. Update torrent_hash in seasons table
     h. Mark season as "pending_processing" for post-download step

Post-download (periodic check):
  1. For seasons with pending_processing:
     a. Check torrent state in qBit — if completed:
     b. Get voice_actor_id from season → find track_id
     c. Run AudioCutter on files NOT in processed_files
     d. Add to processed_files
     e. Clear pending_processing flag
```

## Post-Completion
- Monitor first few auto-redownloads to verify correct behavior
- Consider adding UI indicator showing Kinozal check status
- Future: extend to other trackers (rutracker, etc.) via provider interface
