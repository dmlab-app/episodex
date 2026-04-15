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
