// Package audio provides MKV audio track analysis and processing.
package audio

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// AudioCutter handles audio track operations on MKV files
type AudioCutter struct { //nolint:revive // name is used across the codebase
	mkvmergePath    string
	mkvpropeditPath string
	ffmpegPath      string
	tempDir         string
}

// New creates a new AudioCutter
func New() *AudioCutter {
	tempDir := filepath.Join(os.TempDir(), "episodex-audio")
	_ = os.MkdirAll(tempDir, 0o750)

	return &AudioCutter{
		mkvmergePath:    "mkvmerge",    // Assumes mkvmerge is in PATH
		mkvpropeditPath: "mkvpropedit", // Assumes mkvpropedit is in PATH
		ffmpegPath:      "ffmpeg",      // Assumes ffmpeg is in PATH
		tempDir:         tempDir,
	}
}

// AudioTrack represents an audio track in an MKV file
type AudioTrack struct { //nolint:revive // name is used across the codebase
	Type     string `json:"type"`
	Codec    string `json:"codec"`
	Language string `json:"language"`
	Name     string `json:"name"`
	ID       int    `json:"id"`
	Channels int    `json:"channels"`
	Default  bool   `json:"default"`
}

// MKVInfo represents the structure of mkvmerge -J output
type MKVInfo struct {
	Tracks []struct {
		Type       string `json:"type"`
		Codec      string `json:"codec"`
		Properties struct {
			Language      string `json:"language"`
			TrackName     string `json:"track_name"`
			AudioChannels int    `json:"audio_channels"`
			DefaultTrack  bool   `json:"default_track"`
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
				Channels: track.Properties.AudioChannels,
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

		// Only process MKV files, skip temp files from mkvmerge
		if !info.IsDir() && strings.ToLower(filepath.Ext(path)) == ".mkv" && !strings.HasPrefix(info.Name(), ".tmp_") {
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

// RemoveAudioTracks removes all audio tracks except the one matching trackName.
// If keepOriginal is true, the original English audio track is also preserved.
func (ac *AudioCutter) RemoveAudioTracks(filePath string, trackName string, keepOriginal bool) error {
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return fmt.Errorf("file does not exist: %s", filePath)
	}

	cmd := exec.Command(ac.mkvmergePath, "-J", filePath) //nolint:gosec // controlled input
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to run mkvmerge: %w", err)
	}

	var info MKVInfo
	if err := json.Unmarshal(output, &info); err != nil {
		return fmt.Errorf("failed to parse mkvmerge output: %w", err)
	}

	// Find target track by name and collect IDs to keep
	kept := make(map[int]bool)
	var audioTrackIDs []string
	for _, track := range info.Tracks {
		if track.Type != "audio" {
			continue
		}
		keep := strings.EqualFold(track.Properties.TrackName, trackName)
		if keepOriginal && (track.Properties.Language == "eng" || track.Properties.Language == "und") {
			keep = true
		}
		if keep && !kept[track.ID] {
			kept[track.ID] = true
			audioTrackIDs = append(audioTrackIDs, fmt.Sprintf("%d", track.ID))
		}
	}

	if len(audioTrackIDs) == 0 {
		return fmt.Errorf("track %q not found in file", trackName)
	}

	dir := filepath.Dir(filePath)
	base := filepath.Base(filePath)
	tempFile := filepath.Join(dir, ".tmp_"+base)
	defer func() { _ = os.Remove(tempFile) }()

	trackArgs := []string{
		"-o", tempFile,
		"--audio-tracks", strings.Join(audioTrackIDs, ","),
		filePath,
	}

	mkvCmd := exec.Command(ac.mkvmergePath, trackArgs...) //nolint:gosec // controlled input
	if output, err := mkvCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("mkvmerge failed: %w, output: %s", err, string(output))
	}

	if err := os.Rename(tempFile, filePath); err != nil {
		ext := filepath.Ext(filePath)
		base := strings.TrimSuffix(filePath, ext)
		processedPath := base + " [processed]" + ext
		if renameErr := os.Rename(tempFile, processedPath); renameErr != nil {
			return fmt.Errorf("failed to save processed file: %w", renameErr)
		}
		slog.Warn("Could not replace original, saved as processed", "path", processedPath)
	}

	return nil
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

// SetDefaultAudioTrack sets the audio track matching trackName as default using mkvpropedit.
// It clears the default flag on all tracks and sets it on the matching audio track.
func (ac *AudioCutter) SetDefaultAudioTrack(filePath string, trackName string) error {
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return fmt.Errorf("file does not exist: %s", filePath)
	}

	cmd := exec.Command(ac.mkvmergePath, "-J", filePath) //nolint:gosec // controlled input
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to run mkvmerge: %w", err)
	}

	var info MKVInfo
	if err := json.Unmarshal(output, &info); err != nil {
		return fmt.Errorf("failed to parse mkvmerge output: %w", err)
	}

	// Find target track ID by name
	targetID := -1
	for _, track := range info.Tracks {
		if track.Type == "audio" && strings.EqualFold(track.Properties.TrackName, trackName) {
			targetID = track.ID
			break
		}
	}
	if targetID < 0 {
		return fmt.Errorf("audio track %q not found in file", trackName)
	}

	// Build mkvpropedit arguments:
	// Clear default on all tracks, then set default on target
	args := []string{filePath}
	for _, track := range info.Tracks {
		trackNum := track.ID + 1 // mkvpropedit uses 1-based track numbers
		args = append(args, "--edit", fmt.Sprintf("track:%d", trackNum), "--set", "flag-default=0")
	}
	targetNum := targetID + 1
	args = append(args, "--edit", fmt.Sprintf("track:%d", targetNum), "--set", "flag-default=1")

	editCmd := exec.Command(ac.mkvpropeditPath, args...) //nolint:gosec // controlled input
	if out, err := editCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("mkvpropedit failed: %w, output: %s", err, string(out))
	}

	return nil
}
