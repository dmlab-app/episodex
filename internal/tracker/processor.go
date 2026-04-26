package tracker

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/episodex/episodex/internal/database"
	"github.com/episodex/episodex/internal/qbittorrent"
)

// AudioProcessor defines the audio operations needed by the post-download processor.
type AudioProcessor interface {
	RemoveAudioTracks(filePath string, trackName string, keepOriginal bool) error
}

// PostDownloadProcessor checks for completed torrents and processes audio on new files.
type PostDownloadProcessor struct {
	db       *database.DB
	qbit     QbitClient
	audio    AudioProcessor
	procLock *database.ProcessingLock
}

// NewPostDownloadProcessor creates a new PostDownloadProcessor.
func NewPostDownloadProcessor(db *database.DB, qbit QbitClient, audio AudioProcessor, procLock *database.ProcessingLock) *PostDownloadProcessor {
	return &PostDownloadProcessor{
		db:       db,
		qbit:     qbit,
		procLock: procLock,
		audio:    audio,
	}
}

// completedStates are qBittorrent torrent states that indicate the download is finished.
var completedStates = map[string]bool{
	"uploading":  true,
	"stalledUP":  true,
	"pausedUP":   true,
	"forcedUP":   true,
	"queuedUP":   true,
	"checkingUP": true,
}

// ProcessResult holds the outcome of processing a single season.
type ProcessResult struct {
	SeasonID  int64
	SeriesID  int64
	Season    int
	Processed int
	Failed    int
	Skipped   bool
	Error     error
}

// ProcessCompleted checks all seasons with tracker URLs for completed downloads
// and runs audio processing on any new (unprocessed) files.
func (p *PostDownloadProcessor) ProcessCompleted() []ProcessResult {
	seasons, err := p.db.GetSeasonsWithTrackerURL()
	if err != nil {
		slog.Error("Post-download processor: failed to get seasons", "error", err)
		return nil
	}

	// Get all torrents once to check states
	torrents, err := p.qbit.ListTorrents()
	if err != nil {
		slog.Error("Post-download processor: failed to list torrents", "error", err)
		return nil
	}

	torrentByHash := make(map[string]qbittorrent.Torrent, len(torrents))
	for _, t := range torrents {
		torrentByHash[t.Hash] = t
	}

	var results []ProcessResult
	for i := range seasons {
		result := p.processSeason(&seasons[i], torrentByHash)
		if result != nil {
			results = append(results, *result)
		}
	}

	return results
}

func (p *PostDownloadProcessor) processSeason(season *database.Season, torrentByHash map[string]qbittorrent.Torrent) *ProcessResult {
	result := &ProcessResult{
		SeasonID: season.ID,
		SeriesID: season.SeriesID,
		Season:   season.SeasonNumber,
	}

	// Skip if not flagged for auto-processing (only checker-initiated downloads)
	if !season.AutoProcess {
		return nil
	}

	if season.TrackName == nil || *season.TrackName == "" {
		return nil // no track configured, can't auto-process
	}

	// Skip if already being processed (e.g. by frontend)
	if !p.procLock.TryLock(season.SeriesID, int64(season.SeasonNumber)) {
		return nil
	}
	defer p.procLock.Unlock(season.SeriesID, int64(season.SeasonNumber))
	if season.TorrentHash == nil || *season.TorrentHash == "" {
		return nil
	}
	if season.FolderPath == nil || *season.FolderPath == "" {
		return nil
	}

	// Check if torrent is completed
	torrent, ok := torrentByHash[*season.TorrentHash]
	if !ok {
		return nil // torrent not in qBit
	}
	if !completedStates[torrent.State] {
		return nil // still downloading
	}

	trackName := *season.TrackName

	// Find unprocessed MKV files in the folder
	processedPaths, err := p.db.GetProcessedFilePathsBySeason(season.SeriesID, season.SeasonNumber)
	if err != nil {
		result.Error = fmt.Errorf("get processed files: %w", err)
		return result
	}
	processedSet := make(map[string]bool, len(processedPaths))
	for _, pp := range processedPaths {
		processedSet[pp] = true
	}

	mkvFiles, err := listMKVFiles(*season.FolderPath)
	if err != nil {
		result.Error = fmt.Errorf("list MKV files: %w", err)
		return result
	}

	// Process each unprocessed file
	for _, filePath := range mkvFiles {
		if processedSet[filePath] {
			continue
		}

		slog.Info("Post-download processor: processing file",
			"season_id", season.ID, "file", filePath, "track", trackName)

		if err := p.audio.RemoveAudioTracks(filePath, trackName, false); err != nil {
			slog.Error("Post-download processor: audio processing failed",
				"file", filePath, "error", err)
			result.Failed++
			continue
		}

		if err := p.db.InsertProcessedFile(filePath, season.SeriesID, season.SeasonNumber, trackName); err != nil {
			slog.Error("Post-download processor: failed to mark file as processed",
				"file", filePath, "error", err)
			result.Failed++
			continue
		}

		result.Processed++
	}

	// Clear auto_process flag — this download batch is done
	if result.Failed == 0 {
		p.db.Exec(`UPDATE seasons SET auto_process = 0 WHERE id = ?`, season.ID) //nolint:errcheck
	}

	return result
}

// listMKVFiles returns all MKV file paths in the given directory.
func listMKVFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.ToLower(filepath.Ext(e.Name())) == ".mkv" {
			files = append(files, filepath.Join(dir, e.Name()))
		}
	}
	return files, nil
}
