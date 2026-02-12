package hash_test

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/episodex/episodex/internal/hash"
)

// ExampleComputeFileHash demonstrates basic file hashing
func ExampleComputeFileHash() {
	// Create a temporary test file
	tmpDir := os.TempDir()
	testFile := filepath.Join(tmpDir, "example_video.mkv")

	// Write some test data
	data := make([]byte, 1024*1024) // 1MB file
	if err := os.WriteFile(testFile, data, 0o644); err != nil {
		fmt.Println(err)
		return
	}
	defer os.Remove(testFile)

	// Compute hash
	fileHash, err := hash.ComputeFileHash(testFile)
	if err != nil {
		fmt.Println(err)
		return
	}

	fmt.Printf("File: %s\n", filepath.Base(fileHash.FilePath))
	fmt.Printf("Size: %d bytes\n", fileHash.Size)
	fmt.Printf("Hash length: %d characters\n", len(fileHash.Hash))

	// Output:
	// File: example_video.mkv
	// Size: 1048576 bytes
	// Hash length: 64 characters
}

// ExampleHasChanged demonstrates change detection
func ExampleHasChanged() {
	// Create a temporary test file
	tmpDir := os.TempDir()
	testFile := filepath.Join(tmpDir, "example_episode.mkv")

	// Original content
	original := []byte("Original episode content")
	if err := os.WriteFile(testFile, original, 0o644); err != nil {
		fmt.Println(err)
		return
	}
	defer os.Remove(testFile)

	// Get initial hash
	initialHash, err := hash.ComputeFileHash(testFile)
	if err != nil {
		fmt.Println(err)
		return
	}

	// Check - should not have changed
	changed, err := hash.HasChanged(testFile, initialHash.Hash)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Printf("File changed (initial): %v\n", changed)

	// Modify the file
	modified := []byte("MODIFIED episode content - different quality")
	if err := os.WriteFile(testFile, modified, 0o644); err != nil {
		fmt.Println(err)
		return
	}

	// Check again - should have changed
	changed, err = hash.HasChanged(testFile, initialHash.Hash)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Printf("File changed (after modification): %v\n", changed)

	// Output:
	// File changed (initial): false
	// File changed (after modification): true
}

// ExampleComputeMultiFileHash demonstrates hashing multiple files
func ExampleComputeMultiFileHash() {
	// Create temporary test files (simulating season episodes)
	tmpDir := os.TempDir()
	seasonDir := filepath.Join(tmpDir, "example_season")
	os.MkdirAll(seasonDir, 0o755)
	defer os.RemoveAll(seasonDir)

	// Create 3 episode files
	episodes := []string{
		filepath.Join(seasonDir, "episode_01.mkv"),
		filepath.Join(seasonDir, "episode_02.mkv"),
		filepath.Join(seasonDir, "episode_03.mkv"),
	}

	for i, ep := range episodes {
		content := fmt.Sprintf("Episode %d content", i+1)
		if err := os.WriteFile(ep, []byte(content), 0o644); err != nil {
			fmt.Println(err)
			return
		}
	}

	// Compute combined hash for all episodes
	combinedHash, err := hash.ComputeMultiFileHash(episodes)
	if err != nil {
		fmt.Println(err)
		return
	}

	fmt.Printf("Episodes: %d\n", len(episodes))
	fmt.Printf("Combined hash length: %d characters\n", len(combinedHash))
	fmt.Println("Combined hash represents all files in season")

	// Output:
	// Episodes: 3
	// Combined hash length: 64 characters
	// Combined hash represents all files in season
}

// ExampleComputeFileHash_largeFile demonstrates hashing performance
func ExampleComputeFileHash_largeFile() {
	// Create a large temporary test file (simulating real video file)
	tmpDir := os.TempDir()
	testFile := filepath.Join(tmpDir, "large_video.mkv")

	// Create 10MB file
	data := make([]byte, 10*1024*1024)
	for i := range data {
		data[i] = byte(i % 256)
	}

	if err := os.WriteFile(testFile, data, 0o644); err != nil {
		fmt.Println(err)
		return
	}
	defer os.Remove(testFile)

	// Compute hash - should be very fast even for large file
	fileHash, err := hash.ComputeFileHash(testFile)
	if err != nil {
		fmt.Println(err)
		return
	}

	fmt.Printf("Large file size: %d MB\n", fileHash.Size/(1024*1024))
	fmt.Printf("Hash computed successfully\n")
	fmt.Printf("Only reads first and last 64KB for speed\n")

	// Output:
	// Large file size: 10 MB
	// Hash computed successfully
	// Only reads first and last 64KB for speed
}
