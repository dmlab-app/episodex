# Fix PTN Season Misparsing (NxRus/NxUkr patterns)

## Overview
- The `go-parse-torrent-name` (ptn) library misparses multi-language suffixes like `2xRus`, `3xRus`, `2xUkr` as season numbers
- Example: `Ginny.and.Georgia.S03.1080p.NF.WEB-DL.2xRus` → ptn returns `Season=2, Title="Ginny and Georgia S03"` instead of `Season=3, Title="Ginny and Georgia"`
- This causes series to be filed under the wrong season in the database
- The fix must handle both `parseSeriesFolder()` and `scanSeasonFolders()` which both call `ptn.Parse()`

## Context (from discovery)
- Files/components involved:
  - `internal/scanner/scanner.go` — `parseSeriesFolder()` (line 230), `scanSeasonFolders()` (line 266), `extractSeasonNumber()` (line 331), `cleanSeriesTitle()` (line 348), regex patterns (lines 23-29)
  - `internal/scanner/scanner_test.go` — currently has NO tests for `parseSeriesFolder`, `extractSeasonNumber`, or `scanSeasonFolders`
- Related patterns found: `extractSeasonNumber()` uses `reSeasonS` regex (`(?i)[Ss](\d{1,2})`) to extract season from raw folder name — already works correctly but only called as fallback when `ptn.Season == 0`
- Dependencies: `github.com/middelink/go-parse-torrent-name v0.0.0-20190301154245-3ff4efacd4c4` (2019 pinned version)

## Development Approach
- **Testing approach**: Regular (code first, then tests)
- Complete each task fully before moving to the next
- Make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
  - tests are not optional - they are a required part of the checklist
  - write unit tests for new functions/methods
  - write unit tests for modified functions/methods
  - add new test cases for new code paths
  - update existing test cases if behavior changes
  - tests cover both success and error scenarios
- **CRITICAL: all tests must pass before starting next task** - no exceptions
- **CRITICAL: update this plan file when scope changes during implementation**
- Run tests after each change
- Maintain backward compatibility

## Testing Strategy
- **Unit tests**: required for every task (see Development Approach above)
- No E2E tests in this project

## Progress Tracking
- Mark completed items with `[x]` immediately when done
- Add newly discovered tasks with ➕ prefix
- Document issues/blockers with ⚠️ prefix
- Update plan if implementation deviates from original scope
- Keep plan in sync with actual work done

## What Goes Where
- **Implementation Steps** (`[ ]` checkboxes): tasks achievable within this codebase - code changes, tests, documentation updates
- **Post-Completion** (no checkboxes): items requiring external action - manual testing, changes in consuming projects, deployment configs, third-party verifications

## Implementation Steps

### Task 1: Add validation logic to `parseSeriesFolder` to detect ptn misparsing
- [x] Add a regex pattern `reMultiLang` to match `NxLang` suffixes (e.g. `2xRus`, `3xUkr`, `2xEng`) at the top of `scanner.go` alongside existing regex patterns (line ~23)
- [x] In `parseSeriesFolder()` (line 230), after `ptn.Parse()` returns, add a check: if `info.Season > 0` AND the title still contains an `S##` pattern (via `extractSeasonNumber(info.Title) > 0`), override `season` with `extractSeasonNumber(name)` (extract from original raw folder name)
- [x] Also strip the leftover `S##` from the title when this override happens — the existing `cleanSeriesTitle()` already handles `SXX` removal via `reSeasonEnd`/`reSeasonMid` patterns, so ensure the title goes through `cleanSeriesTitle()` after override
- [x] Write table-driven tests for `parseSeriesFolder` covering:
  - `Ginny.and.Georgia.S03.1080p.NF.WEB-DL.2xRus.Ukr.Eng.Subs-alekartem` → season=3, title="Ginny and Georgia"
  - `Show.S02.1080p.3xRus.Eng` → season=2, title="Show"
  - `Show.S05.720p.2xUkr` → season=5, title="Show"
  - `Breaking.Bad.S01.1080p.BluRay` → season=1, title="Breaking Bad" (no NxLang, normal case still works)
  - `Some.Show.Season.2.1080p` → season=2, title="Some Show" (Season word pattern still works)
- [x] Write tests for error/edge cases:
  - folder name with no season at all (e.g. `Some.Show.1080p`) → returns nil
  - folder name with only NxLang and no S## (e.g. `Show.2xRus.720p`) → ptn season used if no S## in title
- [x] Run `go test ./internal/scanner/...` - must pass before next task

### Task 2: Fix title extraction in `scanSeasonFolders` for NxLang series names
- [x] In `scanSeasonFolders()` (line 266), after `ptn.Parse(seriesName)` at line 276, the result is used only for `info.Title` (not season). If ptn misparses `2xRus` as season, it may also corrupt the title (e.g. "Ginny and Georgia S03" instead of "Ginny and Georgia"). Add a fallback: if `info.Title` still contains an `S##` pattern, re-extract the title using `extractTitleFromName(seriesName)` instead
- [x] Write table-driven tests for `scanSeasonFolders` covering:
  - series folder named `Ginny.and.Georgia.S03.1080p.NF.WEB-DL.2xRus` with season subfolders → title should be "Ginny and Georgia" (not "Ginny and Georgia S03")
  - series folder named `Breaking.Bad.1080p` with `Season 1` subfolder → title="Breaking Bad"
- [x] Run `go test ./internal/scanner/...` - must pass before next task

### Task 3: Verify acceptance criteria
- [x] Verify all requirements from Overview are implemented
- [x] Verify edge cases are handled (NxRus, NxUkr, NxEng, no season, normal names)
- [x] Run full test suite: `go test ./...`
- [x] Run linter: `go vet ./...`
- [x] Verify test coverage meets project standard

### Task 4: [Final] Update documentation
- [x] Update README.md if needed
- [x] Update project knowledge docs if new patterns discovered

*Note: ralphex automatically moves completed plans to `docs/plans/completed/`*

## Technical Details

### Root cause
The `go-parse-torrent-name` library uses regex patterns to extract metadata from torrent names. The `NxLang` pattern (e.g. `2xRus`, `3xUkr`) matches the library's internal season detection regex (number followed by character), causing it to interpret the language count as a season number.

### Fix approach
**Post-validation of ptn results**: After `ptn.Parse()` returns, check if the parsed title still contains an explicit season marker (`S##`). If it does, this indicates ptn picked up the wrong value as season — override with manual regex extraction from the original folder name.

### Data flow (after fix)
```
Input: "Ginny.and.Georgia.S03.1080p.NF.WEB-DL.2xRus"
  ↓ ptn.Parse()
  → Season=2, Title="Ginny and Georgia S03"
  ↓ check: extractSeasonNumber(title) > 0? → yes (S03)
  ↓ override: season = extractSeasonNumber(originalName) → 3
  ↓ title = cleanSeriesTitle(title) → "Ginny and Georgia"
  → Final: Season=3, Title="Ginny and Georgia"
```

### Regex patterns involved
- Existing: `reSeasonS = regexp.MustCompile("(?i)[Ss](\d{1,2})")` — extracts season from `S##`
- Existing: `reSeasonEnd`, `reSeasonMid` — removes `S##` from titles
- New: `reMultiLang = regexp.MustCompile("(?i)\d+x(Rus|Ukr|Eng|Ger|Fre|Spa|Ita|Jpn|Kor|Chi|Por|Pol|Cze|Tur|Ara|Dut|Nor|Swe|Dan|Fin|Hun|Rom|Bul|Hrv|Srb|Slv|Heb)")` — optional, for future-proofing detection of multi-language suffixes

## Post-Completion
*Items requiring manual intervention or external systems - no checkboxes, informational only*

**Manual verification:**
- Rebuild Docker container and rescan media library
- Verify Ginny and Georgia shows as season 3 after rescan
- Check other series with NxRus/NxUkr suffixes are parsed correctly
- Verify the Updates tab still works correctly after rescan
