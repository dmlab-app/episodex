// Package scanner provides media folder scanning and series discovery.
package scanner

import (
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	ptn "github.com/middelink/go-parse-torrent-name"

	"github.com/episodex/episodex/internal/database"
	"github.com/episodex/episodex/internal/hash"
	"github.com/episodex/episodex/internal/tvdb"
)

// Pre-compiled regexps for performance (used in hot path during folder scan)
var (
	reSeasonS       = regexp.MustCompile(`(?i)[Ss](\d{1,2})`)
	reSeasonWord    = regexp.MustCompile(`(?i)[Ss]eason\s*(\d{1,2})`)
	reSeasonEnd     = regexp.MustCompile(`(?i)\s*[Ss]\d{1,2}$`)
	reSeasonWordEnd = regexp.MustCompile(`(?i)\s*[Ss]eason\s*\d{1,2}$`)
	reSeasonMid     = regexp.MustCompile(`(?i)\s+[Ss]\d{1,2}\s+`)
	reSeasonWordMid = regexp.MustCompile(`(?i)\s+[Ss]eason\s*\d{1,2}\s+`)
	reMultiSpace    = regexp.MustCompile(`\s+`)

	reQualityPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\s*\bHDR\b`),
		regexp.MustCompile(`(?i)\s*\bDV\b`),
		regexp.MustCompile(`(?i)\s*\bDolby\s*Vision\b`),
		regexp.MustCompile(`(?i)\s*\b4K\b`),
		regexp.MustCompile(`(?i)\s*\b2160p\b`),
		regexp.MustCompile(`(?i)\s*\b1080p\b`),
		regexp.MustCompile(`(?i)\s*\b720p\b`),
		regexp.MustCompile(`(?i)\s*\b480p\b`),
		regexp.MustCompile(`(?i)\s*\bWEB-?DL\b`),
		regexp.MustCompile(`(?i)\s*\bWEB-?Rip\b`),
		regexp.MustCompile(`(?i)\s*\bWEB\b`),
		regexp.MustCompile(`(?i)\s*\bBluRay\b`),
		regexp.MustCompile(`(?i)\s*\bBDRip\b`),
		regexp.MustCompile(`(?i)\s*\bx264\b`),
		regexp.MustCompile(`(?i)\s*\bx265\b`),
		regexp.MustCompile(`(?i)\s*\bH\.?264\b`),
		regexp.MustCompile(`(?i)\s*\bH\.?265\b`),
		regexp.MustCompile(`(?i)\s*\bHEVC\b`),
		regexp.MustCompile(`(?i)\s*\bAtmos\b`),
		regexp.MustCompile(`(?i)\s*\bDDP\d+\.?\d*\b`),
		regexp.MustCompile(`(?i)\s*\bLostFilm\b`),
		regexp.MustCompile(`(?i)\s*\bLF\b`),
		regexp.MustCompile(`(?i)\s*\bRus\b`),
		regexp.MustCompile(`(?i)\s*\bEng\b`),
	}

	reParseSeasonFolder = []*regexp.Regexp{
		regexp.MustCompile(`^[Ss]eason\s*(\d{1,2})$`),
		regexp.MustCompile(`^[Ss](\d{1,2})$`),
		regexp.MustCompile(`^(\d{1,2})$`),
	}

	// extractTitleFromName patterns
	reExtractSeasonSuffix = regexp.MustCompile(`(?i)\s*[Ss]\d{1,2}.*$`)
	reExtractJunkSuffix   = regexp.MustCompile(`(?i)\s*(WEB[-\s]?DL|WEB[-\s]?Rip|BluRay|BDRip|1080p|2160p|720p|480p|HDR|DV|x264|x265|HEVC|H\.?264|H\.?265|LostFilm|LF|Rus|Eng|DD\d+\.\d+|Atmos|DDP).*$`)

	// parseEpisodeNumber patterns
	reEpisodeSE = regexp.MustCompile(`(?i)[Ss]\d{1,2}[Ee](\d{1,3})`)
	reEpisodeX  = regexp.MustCompile(`(?i)\d{1,2}[xX](\d{1,3})`)
	reEpisodeEP = regexp.MustCompile(`(?i)ep?(\d{1,3})`)
)

// Scanner scans media folders for TV series
type Scanner struct {
	db        *database.DB
	tvdb      *tvdb.Client
	mediaPath string
	scanMu    sync.Mutex
}

// SeriesInfo holds parsed series information
type SeriesInfo struct {
	Title  string
	Path   string
	Season int
}

// New creates a new Scanner
func New(db *database.DB, tvdbClient *tvdb.Client, mediaPath string) *Scanner {
	return &Scanner{
		db:        db,
		tvdb:      tvdbClient,
		mediaPath: mediaPath,
	}
}

// Scan scans the media folder for series and seasons.
// Only one scan can run at a time; concurrent calls return immediately.
func (s *Scanner) Scan() error {
	if !s.scanMu.TryLock() {
		slog.Info("Scan already in progress, skipping")
		return nil
	}
	defer s.scanMu.Unlock()

	slog.Info("Starting media scan", "path", s.mediaPath)

	entries, err := os.ReadDir(s.mediaPath)
	if err != nil {
		slog.Error("Failed to read media directory", "error", err)
		return err
	}

	var scannedSeries []SeriesInfo

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		// Skip hidden folders (starting with dot)
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}

		seriesPath := filepath.Join(s.mediaPath, entry.Name())
		info := s.parseSeriesFolder(entry.Name(), seriesPath)

		if info != nil {
			scannedSeries = append(scannedSeries, *info)
		} else {
			// Check for season subfolders
			seasons := s.scanSeasonFolders(entry.Name(), seriesPath)
			scannedSeries = append(scannedSeries, seasons...)
		}
	}

	slog.Info("Scan complete", "found", len(scannedSeries))

	// Process found series
	for _, info := range scannedSeries {
		if err := s.processSeriesInfo(info); err != nil {
			slog.Error("Failed to process series", "title", info.Title, "error", err)
		}
	}

	// Clean up seasons whose folders have been removed or emptied
	if err := s.cleanupRemovedSeasons(); err != nil {
		slog.Error("Failed to cleanup removed seasons", "error", err)
	}

	return nil
}

// cleanupRemovedSeasons checks all seasons with is_owned=1 and clears those
// whose folder_path no longer exists or no longer contains video files.
func (s *Scanner) cleanupRemovedSeasons() error {
	rows, err := s.db.Query(`
		SELECT id, series_id, season_number, folder_path
		FROM seasons
		WHERE is_owned = 1 AND folder_path IS NOT NULL AND folder_path != ''
	`)
	if err != nil {
		return fmt.Errorf("failed to query owned seasons: %w", err)
	}

	type ownedSeason struct {
		id           int64
		seriesID     int64
		seasonNumber int
		folderPath   string
	}

	var toCheck []ownedSeason
	for rows.Next() {
		var sn ownedSeason
		if err := rows.Scan(&sn.id, &sn.seriesID, &sn.seasonNumber, &sn.folderPath); err != nil {
			rows.Close() //nolint:errcheck
			return fmt.Errorf("failed to scan owned season row: %w", err)
		}
		toCheck = append(toCheck, sn)
	}
	rows.Close() //nolint:errcheck
	if err := rows.Err(); err != nil {
		return fmt.Errorf("error iterating owned seasons: %w", err)
	}

	for _, sn := range toCheck {
		// Check if the folder still exists and has video files
		if folderHasVideoFiles(sn.folderPath) {
			continue
		}

		slog.Info("Season folder missing or empty, clearing is_owned",
			"series_id", sn.seriesID, "season", sn.seasonNumber, "path", sn.folderPath)

		// Clear is_owned and folder_path
		if _, err := s.db.Exec(`
			UPDATE seasons SET is_owned = 0, folder_path = NULL, updated_at = CURRENT_TIMESTAMP
			WHERE id = ?
		`, sn.id); err != nil {
			slog.Error("Failed to clear is_owned", "season_id", sn.id, "error", err)
			continue
		}

		// Delete media_files for this season
		if err := s.db.DeleteMediaFilesBySeason(sn.seriesID, sn.seasonNumber); err != nil {
			slog.Error("Failed to delete media files", "season_id", sn.id, "error", err)
		}

		// Clear episode file fields (preserve voice_actor_id, TVDB metadata, is_watched)
		if _, err := s.db.Exec(`
			UPDATE episodes SET file_path = NULL, file_hash = NULL, file_size = NULL, updated_at = CURRENT_TIMESTAMP
			WHERE season_id = ?
		`, sn.id); err != nil {
			slog.Error("Failed to clear episode file fields", "season_id", sn.id, "error", err)
		}
	}

	return nil
}

// folderHasVideoFiles checks if a folder exists and contains video files.
func folderHasVideoFiles(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return false
	}
	return hasVideoFiles(path)
}

// parseSeriesFolder parses folder name using torrent name parsing library
func (s *Scanner) parseSeriesFolder(name, path string) *SeriesInfo {
	// Use the go-parse-torrent-name library to extract title and season
	info, err := ptn.Parse(name)
	if err != nil {
		slog.Debug("Failed to parse folder name", "name", name, "error", err)
		return nil
	}

	season := info.Season
	title := info.Title

	// If ptn didn't find season, try manual extraction
	if season == 0 {
		season = extractSeasonNumber(name)
	}

	// If title is empty or looks wrong (e.g., just season number), extract from raw name
	if title == "" || strings.HasPrefix(strings.ToUpper(title), "S0") || strings.HasPrefix(strings.ToUpper(title), "SEASON") {
		title = extractTitleFromName(name)
	}

	// Only return SeriesInfo if we found a season number
	if season > 0 && title != "" {
		return &SeriesInfo{
			Title:  cleanSeriesTitle(title),
			Season: season,
			Path:   path,
		}
	}

	// No season found, return nil to fall through to scanSeasonFolders
	return nil
}

// scanSeasonFolders scans for season subfolders within a series folder
func (s *Scanner) scanSeasonFolders(seriesName, seriesPath string) []SeriesInfo {
	var results []SeriesInfo

	entries, err := os.ReadDir(seriesPath)
	if err != nil {
		return results
	}

	// Parse series name using the torrent library, fallback to raw name if parsing fails
	var cleanName string
	if info, err := ptn.Parse(seriesName); err == nil && info.Title != "" {
		cleanName = info.Title
	} else {
		// If ptn fails, try to extract title manually
		cleanName = extractTitleFromName(seriesName)
	}

	// Always clean the title to remove quality tags
	cleanName = cleanSeriesTitle(cleanName)

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		seasonNum := parseSeasonFolder(entry.Name())
		if seasonNum > 0 {
			results = append(results, SeriesInfo{
				Title:  cleanName,
				Season: seasonNum,
				Path:   filepath.Join(seriesPath, entry.Name()),
			})
		}
	}

	// If no season folders found, treat as season 1
	if len(results) == 0 && hasVideoFiles(seriesPath) {
		results = append(results, SeriesInfo{
			Title:  cleanName,
			Season: 1,
			Path:   seriesPath,
		})
	}

	return results
}

// extractTitleFromName extracts title from raw folder name when ptn library fails
func extractTitleFromName(name string) string {
	// Replace dots with spaces first
	cleaned := strings.ReplaceAll(name, ".", " ")

	// Remove season patterns
	cleaned = reExtractSeasonSuffix.ReplaceAllString(cleaned, "")

	// Remove quality and other junk patterns at the end
	cleaned = reExtractJunkSuffix.ReplaceAllString(cleaned, "")

	// Trim and normalize spaces
	cleaned = strings.TrimSpace(cleaned)
	cleaned = reMultiSpace.ReplaceAllString(cleaned, " ")

	return cleaned
}

// extractSeasonNumber manually extracts season number from folder name
func extractSeasonNumber(name string) int {
	// Match patterns like S01, S02, S37, etc.
	for _, pattern := range []*regexp.Regexp{reSeasonS, reSeasonWord} {
		matches := pattern.FindStringSubmatch(name)
		if len(matches) >= 2 {
			num, err := strconv.Atoi(matches[1])
			if err == nil && num > 0 {
				return num
			}
		}
	}

	return 0
}

// cleanSeriesTitle removes season markers and quality tags from series title
func cleanSeriesTitle(title string) string {
	// Replace dots with spaces first
	cleaned := strings.ReplaceAll(title, ".", " ")

	// Remove season patterns like "S01", "S02", "Season 1", etc.
	for _, pattern := range []*regexp.Regexp{reSeasonEnd, reSeasonWordEnd, reSeasonMid, reSeasonWordMid} {
		cleaned = pattern.ReplaceAllString(cleaned, " ")
	}

	// Remove quality tags and release group tags
	for _, pattern := range reQualityPatterns {
		cleaned = pattern.ReplaceAllString(cleaned, "")
	}

	// Trim and normalize spaces
	cleaned = strings.TrimSpace(cleaned)
	cleaned = reMultiSpace.ReplaceAllString(cleaned, " ")

	return cleaned
}

// parseSeasonFolder extracts season number from folder name
func parseSeasonFolder(name string) int {
	for _, pattern := range reParseSeasonFolder {
		matches := pattern.FindStringSubmatch(name)
		if len(matches) >= 2 {
			num, err := strconv.Atoi(matches[1])
			if err == nil {
				return num
			}
		}
	}

	return 0
}

// hasVideoFiles checks if folder contains video files
func hasVideoFiles(path string) bool {
	entries, err := os.ReadDir(path)
	if err != nil {
		return false
	}

	videoExts := map[string]bool{
		".mkv": true, ".mp4": true, ".avi": true, ".m4v": true,
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if videoExts[ext] {
			return true
		}
	}

	return false
}

// parseEpisodeNumber extracts episode number from filename
func parseEpisodeNumber(filename string) int {
	for _, pattern := range []*regexp.Regexp{reEpisodeSE, reEpisodeX, reEpisodeEP} {
		matches := pattern.FindStringSubmatch(filename)
		if len(matches) >= 2 {
			num, err := strconv.Atoi(matches[1])
			if err == nil && num > 0 {
				return num
			}
		}
	}

	return 0
}

// processSeriesInfo adds or updates series in database using TVDB for identification
func (s *Scanner) processSeriesInfo(info SeriesInfo) error {
	var seriesID int64
	var tvdbID int
	var seriesTitle string

	// Try to find series in TVDB if client is available
	if s.tvdb != nil {
		slog.Info("Searching TVDB for series", "title", info.Title)

		results, err := s.tvdb.SearchSeries(info.Title)
		switch {
		case err != nil:
			slog.Warn("TVDB search failed", "title", info.Title, "error", err)
		case len(results) == 0:
			slog.Warn("Series not found in TVDB", "title", info.Title)
		default:
			// Use the first (best) match
			tvdbSeries := results[0]
			tvdbID = tvdbSeries.TVDBId
			seriesTitle = tvdbSeries.Name

			slog.Info("Found series in TVDB",
				"parsed_title", info.Title,
				"tvdb_title", seriesTitle,
				"tvdb_id", tvdbID)

			// Get detailed information about the series (with Russian translation)
			details, err := s.tvdb.GetSeriesDetailsWithRussian(tvdbID)
			if err != nil {
				slog.Warn("Failed to get series details from TVDB", "tvdb_id", tvdbID, "error", err)
			} else {
				// Check if series with this tvdb_id already exists
				err = s.db.QueryRow(`SELECT id FROM series WHERE tvdb_id = ?`, tvdbID).Scan(&seriesID)

				if err != nil && err != sql.ErrNoRows {
					return fmt.Errorf("failed to check existing series by tvdb_id %d: %w", tvdbID, err)
				}
				if err == sql.ErrNoRows {
					// Create new series with TVDB metadata
					result, err := s.db.Exec(`
						INSERT INTO series (tvdb_id, title, original_title, poster_url, status, total_seasons, aired_seasons, created_at, updated_at)
						VALUES (?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
					`, tvdbID, details.Name, details.OriginalName, details.Image, details.Status, len(details.Seasons), tvdb.MaxAiredSeasonNumber(details.Seasons))

					if err != nil {
						return err
					}
					seriesID, _ = result.LastInsertId()
					slog.Info("Created new series from TVDB",
						"title", details.Name,
						"tvdb_id", tvdbID,
						"id", seriesID,
						"total_seasons", len(details.Seasons))
				} else {
					// Series already exists — only update cosmetic fields (title, poster, etc.).
					// Do NOT update total_seasons, aired_seasons, or updated_at here:
					// those are managed by CheckForTVDBUpdates + SyncSeriesMetadata which also
					// creates the corresponding non-owned season rows. Updating counts here
					// without creating season rows would break GET /api/updates (empty new_seasons)
					// and consume the "changed" signal that CheckForTVDBUpdates needs for retry.
					_, err = s.db.Exec(`
						UPDATE series
						SET title = ?, original_title = ?, poster_url = ?, status = ?
						WHERE id = ?
					`, details.Name, details.OriginalName, details.Image, details.Status, seriesID)

					if err != nil {
						slog.Error("Failed to update series metadata", "id", seriesID, "error", err)
					} else {
						slog.Info("Updated series metadata from TVDB",
							"title", details.Name,
							"tvdb_id", tvdbID,
							"id", seriesID)
					}
				}
			}
		}
	}

	// Fallback: if TVDB lookup failed or client not available, use parsed title
	if seriesID == 0 {
		slog.Warn("Using parsed title without TVDB", "title", info.Title)
		seriesTitle = info.Title

		// First, check if this folder_path already exists in seasons
		err := s.db.QueryRow(`
			SELECT series_id FROM seasons WHERE folder_path = ?
		`, info.Path).Scan(&seriesID)

		if err == nil {
			// Found existing series by folder path
			slog.Info("Found existing series by folder path", "path", info.Path, "series_id", seriesID)
		} else {
			// Check if series exists by title
			err = s.db.QueryRow(`SELECT id FROM series WHERE title = ? COLLATE NOCASE`, info.Title).Scan(&seriesID)

			if err != nil && err != sql.ErrNoRows {
				return fmt.Errorf("failed to check existing series by title %q: %w", info.Title, err)
			}
			if err == sql.ErrNoRows {
				// Create new series without TVDB data
				result, err := s.db.Exec(`
					INSERT INTO series (title, status, created_at, updated_at)
					VALUES (?, 'unknown', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
				`, info.Title)
				if err != nil {
					return err
				}
				seriesID, _ = result.LastInsertId()

				// Create alert about series not found in TVDB (deduplicate)
				msg := "Series '" + info.Title + "' not found in TVDB database"
				_, _ = s.db.Exec(`
					INSERT INTO system_alerts (type, message, created_at, dismissed)
					SELECT ?, ?, CURRENT_TIMESTAMP, 0
					WHERE NOT EXISTS (
						SELECT 1 FROM system_alerts WHERE type = ? AND message = ? AND dismissed = 0
					)
				`, "tvdb_not_found", msg, "tvdb_not_found", msg)

				slog.Info("Added new series without TVDB", "title", info.Title, "id", seriesID)
			}
		}
	}

	// Upsert the season — avoids race condition with concurrent TVDB sync
	result, err := s.db.Exec(`
		INSERT INTO seasons (series_id, season_number, folder_path, is_watched, is_owned, discovered_at, created_at, updated_at)
		VALUES (?, ?, ?, 1, 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT(series_id, season_number) DO UPDATE SET
			folder_path = excluded.folder_path,
			is_watched = 1,
			is_owned = 1,
			updated_at = CURRENT_TIMESTAMP
	`, seriesID, info.Season, info.Path)
	if err != nil {
		return fmt.Errorf("failed to upsert season %d for series %d: %w", info.Season, seriesID, err)
	}
	if rows, _ := result.RowsAffected(); rows > 0 {
		slog.Info("Upserted season", "series", seriesTitle, "season", info.Season, "path", info.Path)
	}

	// Scan and hash media files in this season
	if err := s.scanMediaFiles(seriesID, info.Season, info.Path); err != nil {
		slog.Error("Failed to scan media files", "series", seriesTitle, "season", info.Season, "error", err)
	}

	return nil
}

// scanMediaFiles scans all video files in a season folder and computes/stores their hashes
func (s *Scanner) scanMediaFiles(seriesID int64, seasonNumber int, folderPath string) error {
	videoExts := map[string]bool{
		".mkv": true, ".mp4": true, ".avi": true, ".m4v": true,
	}

	var filesScanned int
	var filesChanged int
	var filesNew int

	// Get season from new schema
	season, err := s.db.GetSeasonBySeriesAndNumber(seriesID, seasonNumber)
	if err != nil {
		slog.Error("Failed to get season", "series_id", seriesID, "season", seasonNumber, "error", err)
		return err
	}

	var seasonID int64
	if season != nil {
		seasonID = season.ID
	} else {
		// Create season if it doesn't exist
		newSeason := &database.Season{
			SeriesID:     seriesID,
			SeasonNumber: seasonNumber,
			FolderPath:   &folderPath,
			IsWatched:    true,
		}
		seasonID, err = s.db.UpsertSeason(newSeason)
		if err != nil {
			slog.Error("Failed to create season", "error", err)
			return err
		}
	}

	err = filepath.Walk(folderPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip directories
		if info.IsDir() {
			return nil
		}

		// Only process video files
		ext := strings.ToLower(filepath.Ext(path))
		if !videoExts[ext] {
			return nil
		}

		filesScanned++

		// Parse episode number from filename
		episodeNum := parseEpisodeNumber(filepath.Base(path))
		if episodeNum == 0 {
			slog.Warn("Could not parse episode number from filename", "path", path)
			// Still process the file, just don't link to episode
		}

		// Compute file hash
		fileHash, err := hash.ComputeFileHash(path)
		if err != nil {
			slog.Warn("Failed to compute file hash", "path", path, "error", err)
			return nil // Continue processing other files
		}

		// Check if file changed
		changed, err := s.db.CheckFileChanged(path, fileHash.Hash)
		if err != nil {
			slog.Error("Failed to check file change", "path", path, "error", err)
			return nil
		}

		if changed {
			filesChanged++
			slog.Info("Detected file change",
				"path", path,
				"size", fileHash.Size,
				"hash", fileHash.Hash[:16]+"...")
		}

		// Check if file is new
		existing, err := s.db.GetMediaFileByPath(path)
		if err != nil {
			slog.Error("Failed to get media file", "path", path, "error", err)
			return nil
		}

		if existing == nil {
			filesNew++
		}

		// Upsert media file record (old schema - for backward compatibility)
		mediaFile := &database.MediaFile{
			SeriesID:     seriesID,
			SeasonNumber: seasonNumber,
			FilePath:     path,
			FileName:     filepath.Base(path),
			FileSize:     fileHash.Size,
			FileHash:     fileHash.Hash,
			ModTime:      fileHash.ModTime,
		}

		if err := s.db.UpsertMediaFile(mediaFile); err != nil {
			slog.Error("Failed to upsert media file", "path", path, "error", err)
			return nil
		}

		// If we successfully parsed episode number, create/update episode record
		if episodeNum > 0 && seasonID > 0 {
			episode := &database.Episode{
				SeasonID:      seasonID,
				EpisodeNumber: episodeNum,
				FilePath:      &path,
				FileHash:      &fileHash.Hash,
				FileSize:      &fileHash.Size,
				IsWatched:     true,
			}

			_, err := s.db.UpsertEpisode(episode)
			if err != nil {
				slog.Error("Failed to upsert episode", "episode", episodeNum, "error", err)
			} else {
				slog.Debug("Linked file to episode", "file", filepath.Base(path), "episode", episodeNum)
			}
		}

		return nil
	})

	if err != nil {
		return err
	}

	slog.Info("Scanned media files",
		"series_id", seriesID,
		"season", seasonNumber,
		"scanned", filesScanned,
		"new", filesNew,
		"changed", filesChanged)

	return nil
}

// RescanSeason forces a rescan of all media files in a season.
// Acquires scanMu to avoid interleaving with a full Scan.
func (s *Scanner) RescanSeason(seriesID int64, seasonNumber int) error {
	if !s.scanMu.TryLock() {
		return fmt.Errorf("scan already in progress, try again later")
	}
	defer s.scanMu.Unlock()

	// Get the season folder path
	var folderPath string
	err := s.db.QueryRow(`
		SELECT folder_path FROM seasons
		WHERE series_id = ? AND season_number = ?
	`, seriesID, seasonNumber).Scan(&folderPath)

	if err != nil {
		return err
	}

	slog.Info("Force rescanning season", "series_id", seriesID, "season", seasonNumber, "path", folderPath)

	// Invalidate all cached data first
	if err := s.db.InvalidateCachedDataForSeason(seriesID, seasonNumber); err != nil {
		slog.Error("Failed to invalidate cached data", "error", err)
	}

	// Rescan all files
	if err := s.scanMediaFiles(seriesID, seasonNumber, folderPath); err != nil {
		return err
	}

	// Set is_owned only if the folder actually contains video files
	if folderHasVideoFiles(folderPath) {
		if _, err := s.db.Exec(`
			UPDATE seasons SET is_owned = 1, updated_at = CURRENT_TIMESTAMP
			WHERE series_id = ? AND season_number = ?
		`, seriesID, seasonNumber); err != nil {
			slog.Error("Failed to set is_owned on rescan", "error", err)
		}
	}

	return nil
}
