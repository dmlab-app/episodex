package tracker

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	ptn "github.com/middelink/go-parse-torrent-name"

	"github.com/episodex/episodex/internal/database"
	"github.com/episodex/episodex/internal/qbittorrent"
)

// QbitClient defines the qBittorrent operations needed by the checker.
type QbitClient interface {
	ListTorrents() ([]qbittorrent.Torrent, error)
	DeleteTorrent(hash string) error
	AddTorrent(torrentData []byte, category, savePath string) (string, error)
	GetTorrentFiles(hash string) ([]qbittorrent.TorrentFile, error)
	SetFilePriority(hash string, fileIndexes []int, priority int) error
}

// Checker periodically checks tracked seasons for new episodes on trackers
// and triggers redownload when updates are available.
type Checker struct {
	db       *database.DB
	registry *Registry
	qbit     QbitClient
}

// NewChecker creates a new Checker instance.
func NewChecker(db *database.DB, registry *Registry, qbit QbitClient) *Checker {
	return &Checker{
		db:       db,
		registry: registry,
		qbit:     qbit,
	}
}

// CheckResult holds the outcome of a single season check.
type CheckResult struct {
	SeasonID     int64
	SeriesID     int64
	Season       int
	TrackerEps   int
	DiskEps      int
	Redownloaded bool
	Error        error
}

// Check iterates all seasons with a tracker_url, compares episode counts,
// and triggers redownload when the tracker has more episodes.
func (c *Checker) Check() []CheckResult {
	seasons, err := c.db.GetSeasonsWithTrackerURL()
	if err != nil {
		slog.Error("Tracker check: failed to get seasons", "error", err)
		return nil
	}

	if len(seasons) == 0 {
		slog.Info("Tracker check: no seasons with tracker URL")
		return nil
	}

	slog.Info("Tracker check: checking seasons", "count", len(seasons))

	var results []CheckResult
	for i := range seasons {
		result := c.checkSeason(&seasons[i])
		results = append(results, result)
	}

	return results
}

func (c *Checker) checkSeason(season *database.Season) CheckResult {
	result := CheckResult{
		SeasonID: season.ID,
		SeriesID: season.SeriesID,
		Season:   season.SeasonNumber,
	}

	if season.TrackerURL == nil {
		result.Error = fmt.Errorf("tracker URL is nil for season %d", season.ID)
		return result
	}
	trackerURL := *season.TrackerURL

	client, err := c.registry.GetClient(trackerURL)
	if err != nil {
		result.Error = fmt.Errorf("no tracker client for %s: %w", trackerURL, err)
		slog.Warn("Tracker check: skipping season", "season_id", season.ID, "error", err)
		return result
	}

	trackerEps, err := client.GetEpisodeCount(trackerURL)
	if err != nil {
		result.Error = fmt.Errorf("get episode count: %w", err)
		slog.Error("Tracker check: failed to get episode count", "season_id", season.ID, "url", trackerURL, "error", err)
		return result
	}
	result.TrackerEps = trackerEps

	if trackerEps == 0 {
		slog.Debug("Tracker check: no episode info on tracker", "season_id", season.ID)
		return result
	}

	diskEps := c.getMaxEpisodeOnDisk(season.SeriesID, season.SeasonNumber)
	result.DiskEps = diskEps

	if trackerEps <= diskEps {
		slog.Debug("Tracker check: no new episodes",
			"season_id", season.ID, "tracker", trackerEps, "disk", diskEps)
		return result
	}

	slog.Info("Tracker check: new episodes available",
		"season_id", season.ID, "tracker", trackerEps, "disk", diskEps)

	redownloaded, err := c.redownload(season, client)
	if err != nil {
		result.Error = fmt.Errorf("redownload: %w", err)
		slog.Error("Tracker check: redownload failed", "season_id", season.ID, "error", err)
		return result
	}

	result.Redownloaded = redownloaded
	return result
}

func (c *Checker) redownload(season *database.Season, client Client) (bool, error) {
	trackerURL := *season.TrackerURL

	// Download new .torrent file
	torrentData, err := client.DownloadTorrent(trackerURL)
	if err != nil {
		return false, fmt.Errorf("download torrent: %w", err)
	}

	// Compute new hash and skip redownload if torrent hasn't changed
	newHash, err := qbittorrent.ComputeInfoHash(torrentData)
	if err != nil {
		return false, fmt.Errorf("compute info hash: %w", err)
	}
	if season.TorrentHash != nil && *season.TorrentHash == newHash {
		slog.Debug("Tracker check: torrent unchanged, skipping redownload",
			"season_id", season.ID, "hash", newHash)
		return false, nil
	}

	// Get category from existing torrent in qBit (if available)
	var category string
	var savePath string
	if season.TorrentHash != nil && *season.TorrentHash != "" {
		torrents, err := c.qbit.ListTorrents()
		if err == nil {
			for _, t := range torrents {
				if t.Hash == *season.TorrentHash {
					category = t.Category
					savePath = t.SavePath
					break
				}
			}
		}
	}

	// Add new torrent to qBit first (before deleting old one, so we don't lose the torrent on failure)
	if _, err := c.qbit.AddTorrent(torrentData, category, savePath); err != nil {
		return false, fmt.Errorf("add torrent: %w", err)
	}

	// Delete old torrent from qBit
	if season.TorrentHash != nil && *season.TorrentHash != "" {
		if err := c.qbit.DeleteTorrent(*season.TorrentHash); err != nil {
			slog.Warn("Tracker check: failed to delete old torrent", "hash", *season.TorrentHash, "error", err)
		}
	}

	// Wait for qBittorrent to index the torrent before setting file priorities
	if err := c.skipProcessedFilesWithRetry(newHash, season); err != nil {
		slog.Warn("Tracker check: failed to set file priorities", "error", err)
	}

	// Update torrent_hash in DB
	if err := c.db.UpdateTorrentHash(season.ID, newHash); err != nil {
		return false, fmt.Errorf("update torrent hash: %w", err)
	}

	slog.Info("Tracker check: redownload complete",
		"season_id", season.ID, "new_hash", newHash)

	return true, nil
}

func (c *Checker) skipProcessedFilesWithRetry(hash string, season *database.Season) error {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(2 * time.Second)
		}
		lastErr = c.skipProcessedFiles(hash, season)
		if lastErr == nil {
			return nil
		}
		slog.Debug("Tracker check: waiting for qBittorrent to index torrent",
			"hash", hash, "attempt", attempt+1, "error", lastErr)
	}
	return lastErr
}

func (c *Checker) skipProcessedFiles(hash string, season *database.Season) error {
	files, err := c.qbit.GetTorrentFiles(hash)
	if err != nil {
		return fmt.Errorf("get torrent files: %w", err)
	}

	if season.FolderPath == nil || *season.FolderPath == "" {
		return nil
	}

	processedPaths, err := c.db.GetProcessedFilePathsBySeason(season.SeriesID, season.SeasonNumber)
	if err != nil {
		return fmt.Errorf("get processed files: %w", err)
	}
	if len(processedPaths) == 0 {
		return nil
	}

	// Build a set of processed base file names for quick lookup
	processedSet := make(map[string]bool, len(processedPaths))
	for _, p := range processedPaths {
		processedSet[filepath.Base(p)] = true
	}

	var skipIndexes []int
	for i := range files {
		baseName := filepath.Base(files[i].Name)
		if processedSet[baseName] {
			skipIndexes = append(skipIndexes, files[i].Index)
		}
	}

	if len(skipIndexes) > 0 {
		slog.Info("Tracker check: skipping already-processed files",
			"hash", hash, "count", len(skipIndexes))
		return c.qbit.SetFilePriority(hash, skipIndexes, 0)
	}

	return nil
}

func (c *Checker) getMaxEpisodeOnDisk(seriesID int64, seasonNumber int) int {
	files, err := c.db.GetMediaFilesBySeason(seriesID, seasonNumber)
	if err != nil {
		return 0
	}

	maxEp := 0
	for i := range files {
		info, err := ptn.Parse(files[i].FileName)
		if err != nil {
			continue
		}
		if info.Episode > maxEp {
			maxEp = info.Episode
		}
	}
	return maxEp
}
