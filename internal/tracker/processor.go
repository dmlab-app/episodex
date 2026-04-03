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
	RemoveAudioTracks(filePath string, keepTrackID int, keepOriginal bool) error
}

// PostDownloadProcessor checks for completed torrents and processes audio on new files.
type PostDownloadProcessor struct {
	db    *database.DB
	qbit  QbitClient
	audio AudioProcessor
}

// NewPostDownloadProcessor creates a new PostDownloadProcessor.
func NewPostDownloadProcessor(db *database.DB, qbit QbitClient, audio AudioProcessor) *PostDownloadProcessor {
	return &PostDownloadProcessor{
		db:    db,
		qbit:  qbit,
		audio: audio,
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

	// Skip if missing required fields
	if season.VoiceActorID == nil {
		return nil // no voice actor configured, can't auto-process
	}
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

	// Get the track to keep from previously processed files
	trackKept, found, err := p.db.GetTrackKeptForSeason(season.SeriesID, season.SeasonNumber)
	if err != nil {
		result.Error = fmt.Errorf("get track kept: %w", err)
		return result
	}
	if !found {
		// No previously processed files — user hasn't selected a track yet
		result.Skipped = true
		return result
	}

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
			"season_id", season.ID, "file", filePath, "track", trackKept)

		if err := p.audio.RemoveAudioTracks(filePath, trackKept, false); err != nil {
			slog.Error("Post-download processor: audio processing failed",
				"file", filePath, "error", err)
			result.Failed++
			continue
		}

		if err := p.db.InsertProcessedFile(filePath, season.SeriesID, season.SeasonNumber, trackKept); err != nil {
			slog.Error("Post-download processor: failed to mark file as processed",
				"file", filePath, "error", err)
			result.Failed++
			continue
		}

		result.Processed++
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
