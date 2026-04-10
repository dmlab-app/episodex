# Next Seasons Tab — Discover Next Season to Download

## Overview
New "Seasons" tab in the UI that shows the **next season to download** for each series in the library. For a series where the user has S04 on disk, show S05 with a link to the best Kinozal torrent (largest file size for that specific season). For new series with no seasons on disk, show S01.

This helps the user discover what to download next without manually searching the tracker.

## Context
- Series have `total_seasons` and `aired_seasons` from TVDB
- `downloaded` flag and `folder_path` in seasons table track what's on disk
- Kinozal search requires authentication (already implemented)
- Kinozal search URL: `https://kinozal.tv/browse.php?s=QUERY&g=0&c=0&v=0&d=0&w=0&t=0&f=0`
- Search results contain title, size, link to details page
- Results need to be filtered to match the correct season number
- Cache found links in DB to avoid searching every time

## Development Approach
- **Testing approach**: TDD (tests first)
- Complete each task fully before moving to the next
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**
- Maintain backward compatibility

## Implementation Steps

### Task 1: Kinozal client — search torrents
- [ ] write tests for `Search(query string)` — returns list of results with Title, Size, DetailsURL
- [ ] write tests for parsing search results HTML (title, size in GB, link)
- [ ] write tests for empty results
- [ ] implement `Search()` — GET `/browse.php?s=QUERY`, parse results table
- [ ] run tests — must pass before next task

### Task 2: Kinozal client — find best torrent for season
- [ ] write tests for `FindSeasonTorrent(query string, seasonNumber int)` — filters results by season, returns largest
- [ ] write tests for season matching in title (e.g. "S05", "5 сезон", "Season 5")
- [ ] write tests for no match found
- [ ] implement: call Search(), filter by season number in title, sort by size desc, return first
- [ ] run tests — must pass before next task

### Task 3: Database — next_season_cache table
- [ ] add `next_season_cache` table: series_id, season_number, tracker_url, title, size, cached_at
- [ ] add DB methods: GetCachedNextSeason, SaveCachedNextSeason, ClearExpiredCache
- [ ] write tests for cache CRUD
- [ ] run tests — must pass before next task

### Task 4: API — next seasons endpoint
- [ ] write tests for `GET /api/next-seasons` — returns list of series with next season info
- [ ] implement handler logic:
  - for each series with at least one downloaded season:
    - next_season = max_downloaded_season + 1
  - for series with no downloaded seasons but in library:
    - next_season = 1
  - skip if next_season > aired_seasons (not aired yet)
  - check cache first, if miss → search Kinozal (russian title, fallback to original)
  - cache result
  - return: series info + next season number + tracker link + torrent title + size
- [ ] filter out Ended series (same as updates tab)
- [ ] run tests — must pass before next task

### Task 5: Frontend — Seasons tab
- [ ] add "Seasons" nav item in index.html
- [ ] add seasons page container in index.html
- [ ] implement `loadNextSeasons()` in app.js — fetch /api/next-seasons, render cards
- [ ] each card shows: series poster, title, "Season N", torrent size, link to Kinozal
- [ ] style the cards in style.css (consistent with updates tab)
- [ ] add badge with count (like updates tab)

### Task 6: Verify acceptance criteria
- [ ] verify next season shows correctly for series with downloaded seasons
- [ ] verify S01 shows for series with no downloaded seasons
- [ ] verify Kinozal search finds correct season
- [ ] verify cache works (second load is instant)
- [ ] verify no errors when Kinozal not configured
- [ ] run full test suite
- [ ] run linter — all issues must be fixed

### Task 7: [Final] Update documentation
- [ ] update README.md with Seasons tab description

## Technical Details

### Kinozal Search
```
GET https://kinozal.tv/browse.php?s=Звёздные+врата&g=0&c=0&v=0&d=0&w=0&t=0&f=0
Cookie: <session>
Response: HTML with results table
```

### Season Matching in Search Results
Search result title examples:
- `"Звёздные врата: ЗВ-1 (5 сезон: 1-22 серии из 22) / Stargate SG-1 / WEB-DL (1080p)"`
- `"Stargate SG-1 Season 5 Complete BluRay 1080p"`

Match patterns:
- `(\d+)\s*сезон` — "5 сезон"
- `S(\d+)` — "S05"
- `Season\s*(\d+)` — "Season 5"

### Next Season Logic
```
For each series in library:
  max_downloaded = MAX(season_number) WHERE downloaded = 1
  next_season = max_downloaded + 1 (or 1 if no downloads)
  if next_season > aired_seasons → skip (not aired)
  search Kinozal for next_season
  return result
```

### Cache Table
```sql
CREATE TABLE IF NOT EXISTS next_season_cache (
    series_id INTEGER NOT NULL,
    season_number INTEGER NOT NULL,
    tracker_url TEXT,
    title TEXT,
    size TEXT,
    cached_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(series_id, season_number)
);
```
Cache expires after 7 days.

## Post-Completion
- Consider adding "Download" button that sends torrent to qBittorrent
- Consider showing multiple quality options instead of just the largest
