// Package hash provides file hashing utilities for change detection.
package hash

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

const (
	// ChunkSize defines how many bytes to read from start and end of file
	// 64KB is enough to detect most file changes while being fast
	ChunkSize = 64 * 1024
)

// FileHash represents a computed file hash with metadata
type FileHash struct {
	Hash     string
	Size     int64
	ModTime  int64 // Unix timestamp
	FilePath string
}

// ComputeFileHash computes a fast hash for a file using:
// - File size
// - First ChunkSize bytes
// - Last ChunkSize bytes
//
// ModTime is tracked in the result for quick-check optimization but excluded
// from the hash itself so that touching or restoring a file doesn't cause
// false change detection.
func ComputeFileHash(filePath string) (*FileHash, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close() //nolint:errcheck

	// Get file info
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("failed to stat file: %w", err)
	}

	size := info.Size()
	modTime := info.ModTime().Unix()

	// Create SHA256 hasher
	hasher := sha256.New()

	// Write size to hash
	fmt.Fprintf(hasher, "size:%d|", size) //nolint:errcheck

	// Read first ChunkSize bytes
	firstChunk := make([]byte, ChunkSize)
	n, err := file.Read(firstChunk)
	if err != nil && err != io.EOF {
		return nil, fmt.Errorf("failed to read first chunk: %w", err)
	}
	hasher.Write(firstChunk[:n])

	// If file is larger than ChunkSize, also read last ChunkSize bytes
	if size > ChunkSize {
		// Seek to ChunkSize bytes before end
		seekPos := size - ChunkSize
		if seekPos < ChunkSize {
			// File is between ChunkSize and 2*ChunkSize, read from where first chunk ended
			seekPos = ChunkSize
		}

		_, err = file.Seek(seekPos, io.SeekStart)
		if err != nil {
			return nil, fmt.Errorf("failed to seek to end: %w", err)
		}

		lastChunk := make([]byte, ChunkSize)
		n, err = file.Read(lastChunk)
		if err != nil && err != io.EOF {
			return nil, fmt.Errorf("failed to read last chunk: %w", err)
		}
		hasher.Write(lastChunk[:n])
	}

	// Compute final hash
	hashBytes := hasher.Sum(nil)
	hashString := hex.EncodeToString(hashBytes)

	return &FileHash{
		Hash:     hashString,
		Size:     size,
		ModTime:  modTime,
		FilePath: filePath,
	}, nil
}
