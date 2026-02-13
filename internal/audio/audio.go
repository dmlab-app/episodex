// Package audio provides MKV audio track analysis and processing.
package audio

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// AudioCutter handles audio track operations on MKV files
type AudioCutter struct { //nolint:revive // name is used across the codebase
	mkvmergePath string
	ffmpegPath   string
	tempDir      string
}

// New creates a new AudioCutter
func New() *AudioCutter {
	tempDir := filepath.Join(os.TempDir(), "episodex-audio")
	_ = os.MkdirAll(tempDir, 0o750)

	return &AudioCutter{
		mkvmergePath: "mkvmerge", // Assumes mkvmerge is in PATH
		ffmpegPath:   "ffmpeg",   // Assumes ffmpeg is in PATH
		tempDir:      tempDir,
	}
}

// AudioTrack represents an audio track in an MKV file
type AudioTrack struct { //nolint:revive // name is used across the codebase
	Type     string `json:"type"`
	Codec    string `json:"codec"`
	Language string `json:"language"`
	Name     string `json:"name"`
	ID       int    `json:"id"`
	Default  bool   `json:"default"`
}

// MKVInfo represents the structure of mkvmerge -J output
type MKVInfo struct {
	Tracks []struct {
		Type       string `json:"type"`
		Codec      string `json:"codec"`
		Properties struct {
			Language     string `json:"language"`
			TrackName    string `json:"track_name"`
			DefaultTrack bool   `json:"default_track"`
		} `json:"properties"`
		ID int `json:"id"`
	} `json:"tracks"`
}

// GetAudioTracks scans an MKV file and returns all audio tracks
func (ac *AudioCutter) GetAudioTracks(filePath string) ([]AudioTrack, error) {
	// Check if file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return nil, fmt.Errorf("file does not exist: %s", filePath)
	}

	// Run mkvmerge -J to get file info
	cmd := exec.Command(ac.mkvmergePath, "-J", filePath) //nolint:gosec // controlled input
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to run mkvmerge: %w", err)
	}

	// Parse JSON output
	var info MKVInfo
	if err := json.Unmarshal(output, &info); err != nil {
		return nil, fmt.Errorf("failed to parse mkvmerge output: %w", err)
	}

	// Extract audio tracks
	var audioTracks []AudioTrack
	for _, track := range info.Tracks {
		if track.Type == "audio" {
			audioTracks = append(audioTracks, AudioTrack{
				ID:       track.ID,
				Type:     track.Type,
				Codec:    track.Codec,
				Language: track.Properties.Language,
				Name:     track.Properties.TrackName,
				Default:  track.Properties.DefaultTrack,
			})
		}
	}

	return audioTracks, nil
}

// ScanFolderAudioTracks scans all MKV files in a folder and returns their audio tracks
func (ac *AudioCutter) ScanFolderAudioTracks(folderPath string) (map[string][]AudioTrack, error) {
	if _, err := os.Stat(folderPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("folder does not exist: %s", folderPath)
	}

	results := make(map[string][]AudioTrack)

	// Walk through folder
	err := filepath.Walk(folderPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Only process MKV files
		if !info.IsDir() && strings.ToLower(filepath.Ext(path)) == ".mkv" {
			tracks, err := ac.GetAudioTracks(path)
			if err != nil {
				// Log error but continue processing other files
				return nil
			}
			results[path] = tracks
		}

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to scan folder: %w", err)
	}

	return results, nil
}

// RemoveAudioTracks removes all audio tracks except the specified one.
// If keepOriginal is true, the original English audio track is also preserved.
func (ac *AudioCutter) RemoveAudioTracks(filePath string, keepTrackID int, keepOriginal bool) error {
	// Check if file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return fmt.Errorf("file does not exist: %s", filePath)
	}

	// Get all tracks to build the command
	tracks, err := ac.GetAudioTracks(filePath)
	if err != nil {
		return fmt.Errorf("failed to get audio tracks: %w", err)
	}

	// Verify that the track to keep exists
	trackExists := false
	for _, track := range tracks {
		if track.ID == keepTrackID {
			trackExists = true
			break
		}
	}

	if !trackExists {
		return fmt.Errorf("track ID %d does not exist in file", keepTrackID)
	}

	// Create output file path (temporary)
	dir := filepath.Dir(filePath)
	base := filepath.Base(filePath)
	tempFile := filepath.Join(dir, ".tmp_"+base)

	// Build track selection arguments
	// We need to keep video, subtitles, and the selected audio track
	var trackArgs []string

	// Get all tracks (not just audio)
	cmd := exec.Command(ac.mkvmergePath, "-J", filePath) //nolint:gosec // controlled input
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to run mkvmerge: %w", err)
	}

	var info MKVInfo
	if err := json.Unmarshal(output, &info); err != nil {
		return fmt.Errorf("failed to parse mkvmerge output: %w", err)
	}

	// Build track selection: keep all video and subtitle tracks, only specified audio track
	// If keepOriginal is set, also keep English audio tracks
	kept := make(map[int]bool)
	var audioTrackIDs []string
	for _, track := range info.Tracks {
		if track.Type == "audio" {
			keep := track.ID == keepTrackID
			if keepOriginal && (track.Properties.Language == "eng" || track.Properties.Language == "und") {
				keep = true
			}
			if keep && !kept[track.ID] {
				kept[track.ID] = true
				audioTrackIDs = append(audioTrackIDs, fmt.Sprintf("%d", track.ID))
			}
		}
	}

	if len(audioTrackIDs) == 0 {
		return fmt.Errorf("no audio track to keep")
	}

	// Build mkvmerge command
	// mkvmerge -o output.mkv --audio-tracks <keep_track_id> input.mkv
	trackArgs = []string{
		"-o", tempFile,
		"--audio-tracks", strings.Join(audioTrackIDs, ","),
		filePath,
	}

	// Execute mkvmerge
	mkvCmd := exec.Command(ac.mkvmergePath, trackArgs...) //nolint:gosec // controlled input
	if output, err := mkvCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("mkvmerge failed: %w, output: %s", err, string(output))
	}

	// Replace original file atomically — Rename overwrites the destination on UNIX,
	// so there's no window where the original is deleted but the temp isn't yet in place.
	if err := os.Rename(tempFile, filePath); err != nil {
		return fmt.Errorf("failed to replace original file: %w", err)
	}

	return nil
}

// ProcessFolder processes all MKV files in a folder, keeping only the specified audio track.
// If keepOriginal is true, the original English audio track is also preserved.
func (ac *AudioCutter) ProcessFolder(folderPath string, keepTrackID int, keepOriginal bool) (processed, failed []string, err error) {
	if _, err := os.Stat(folderPath); os.IsNotExist(err) {
		return nil, nil, fmt.Errorf("folder does not exist: %s", folderPath)
	}

	// Walk through folder
	err = filepath.Walk(folderPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Only process MKV files
		if !info.IsDir() && strings.ToLower(filepath.Ext(path)) == ".mkv" {
			if err := ac.RemoveAudioTracks(path, keepTrackID, keepOriginal); err != nil {
				failed = append(failed, path)
			} else {
				processed = append(processed, path)
			}
		}

		return nil
	})

	if err != nil {
		return processed, failed, fmt.Errorf("failed to process folder: %w", err)
	}

	return processed, failed, nil
}

// GeneratePreview generates a 30-second audio preview from an MKV file
// Returns a hash that can be used to retrieve the preview file
func (ac *AudioCutter) GeneratePreview(filePath string, trackIndex, duration int) (string, error) {
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return "", fmt.Errorf("file does not exist: %s", filePath)
	}

	// Create hash for caching
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s_%d", filePath, trackIndex))))
	outputFile := filepath.Join(ac.tempDir, hash+".mp3")

	// Check if preview already exists
	if _, err := os.Stat(outputFile); err == nil {
		return hash, nil
	}

	// Extract audio preview using ffmpeg
	// -ss 60: start at 1 minute
	// -t duration: extract for specified duration
	// -map 0:a:trackIndex: select audio track by index
	cmd := exec.Command(ac.ffmpegPath, //nolint:gosec // controlled input
		"-y",
		"-i", filePath,
		"-ss", "60",
		"-t", fmt.Sprintf("%d", duration),
		"-map", fmt.Sprintf("0:a:%d", trackIndex),
		"-acodec", "libmp3lame",
		"-q:a", "4",
		outputFile,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("ffmpeg failed: %w, output: %s", err, string(output))
	}

	return hash, nil
}

// GetPreviewPath returns the file path for a preview hash
func (ac *AudioCutter) GetPreviewPath(hash string) (string, error) {
	previewPath := filepath.Join(ac.tempDir, hash+".mp3")

	if _, err := os.Stat(previewPath); os.IsNotExist(err) {
		return "", fmt.Errorf("preview not found: %s", hash)
	}

	return previewPath, nil
}

// CleanupOldPreviews removes preview files older than 24 hours
func (ac *AudioCutter) CleanupOldPreviews() error {
	files, err := filepath.Glob(filepath.Join(ac.tempDir, "*.mp3"))
	if err != nil {
		return err
	}

	for _, file := range files {
		info, err := os.Stat(file)
		if err != nil {
			continue
		}

		// Remove files older than 24 hours
		if info.ModTime().Before(time.Now().Add(-24 * time.Hour)) {
			os.Remove(file) //nolint:errcheck // removal failure is non-critical
		}
	}

	return nil
}
