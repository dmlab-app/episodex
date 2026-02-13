package hash

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestComputeFileHash(t *testing.T) {
	// Create a temporary test file
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "test.mkv")

	// Write test data
	testData := make([]byte, 200*1024) // 200KB file
	for i := range testData {
		testData[i] = byte(i % 256)
	}

	err := os.WriteFile(testFile, testData, 0o644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Compute hash
	hash1, err := ComputeFileHash(testFile)
	if err != nil {
		t.Fatalf("Failed to compute hash: %v", err)
	}

	if hash1.Hash == "" {
		t.Error("Hash should not be empty")
	}

	if hash1.Size != 200*1024 {
		t.Errorf("Expected size 200KB, got %d", hash1.Size)
	}

	// Compute again - should be identical
	hash2, err := ComputeFileHash(testFile)
	if err != nil {
		t.Fatalf("Failed to compute hash second time: %v", err)
	}

	if hash1.Hash != hash2.Hash {
		t.Error("Same file should produce same hash")
	}
}

func TestHashChangesOnFileModification(t *testing.T) {
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "test.mkv")

	// Create original file
	original := []byte("Original content at the beginning" + string(make([]byte, 100*1024)) + "Original content at the end")
	err := os.WriteFile(testFile, original, 0o644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	hash1, err := ComputeFileHash(testFile)
	if err != nil {
		t.Fatalf("Failed to compute initial hash: %v", err)
	}

	// Wait a bit to ensure mtime changes
	time.Sleep(10 * time.Millisecond)

	// Modify file content
	modified := []byte("MODIFIED content at the beginning" + string(make([]byte, 100*1024)) + "MODIFIED content at the end")
	err = os.WriteFile(testFile, modified, 0o644)
	if err != nil {
		t.Fatalf("Failed to modify test file: %v", err)
	}

	hash2, err := ComputeFileHash(testFile)
	if err != nil {
		t.Fatalf("Failed to compute hash after modification: %v", err)
	}

	if hash1.Hash == hash2.Hash {
		t.Error("Hash should change when file is modified")
	}
}

func TestHashChangesOnSizeChange(t *testing.T) {
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "test.mkv")

	// Create small file
	small := make([]byte, 50*1024)
	err := os.WriteFile(testFile, small, 0o644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	hash1, err := ComputeFileHash(testFile)
	if err != nil {
		t.Fatalf("Failed to compute initial hash: %v", err)
	}

	// Wait to ensure mtime changes
	time.Sleep(10 * time.Millisecond)

	// Create larger file with same beginning
	large := make([]byte, 150*1024)
	copy(large, small)
	err = os.WriteFile(testFile, large, 0o644)
	if err != nil {
		t.Fatalf("Failed to resize test file: %v", err)
	}

	hash2, err := ComputeFileHash(testFile)
	if err != nil {
		t.Fatalf("Failed to compute hash after resize: %v", err)
	}

	if hash1.Hash == hash2.Hash {
		t.Error("Hash should change when file size changes")
	}
}

func TestComputeMultiFileHash(t *testing.T) {
	tempDir := t.TempDir()

	// Create multiple test files
	file1 := filepath.Join(tempDir, "episode1.mkv")
	file2 := filepath.Join(tempDir, "episode2.mkv")
	file3 := filepath.Join(tempDir, "episode3.mkv")

	if err := os.WriteFile(file1, []byte("Episode 1 content"), 0o644); err != nil {
		t.Fatalf("Failed to write file1: %v", err)
	}
	if err := os.WriteFile(file2, []byte("Episode 2 content"), 0o644); err != nil {
		t.Fatalf("Failed to write file2: %v", err)
	}
	if err := os.WriteFile(file3, []byte("Episode 3 content"), 0o644); err != nil {
		t.Fatalf("Failed to write file3: %v", err)
	}

	// Compute combined hash
	hash1, err := ComputeMultiFileHash([]string{file1, file2, file3})
	if err != nil {
		t.Fatalf("Failed to compute multi-file hash: %v", err)
	}

	if hash1 == "" {
		t.Error("Multi-file hash should not be empty")
	}

	// Compute again - should be identical
	hash2, err := ComputeMultiFileHash([]string{file1, file2, file3})
	if err != nil {
		t.Fatalf("Failed to compute multi-file hash second time: %v", err)
	}

	if hash1 != hash2 {
		t.Error("Same files should produce same multi-file hash")
	}

	// Modify one file
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(file2, []byte("MODIFIED Episode 2 content"), 0o644); err != nil {
		t.Fatalf("Failed to write modified file2: %v", err)
	}

	// Hash should change
	hash3, err := ComputeMultiFileHash([]string{file1, file2, file3})
	if err != nil {
		t.Fatalf("Failed to compute multi-file hash after modification: %v", err)
	}

	if hash1 == hash3 {
		t.Error("Multi-file hash should change when any file is modified")
	}
}

func TestHasChanged(t *testing.T) {
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "test.mkv")

	// Create file
	content := []byte("Test content for change detection")
	err := os.WriteFile(testFile, content, 0o644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Get initial hash
	initial, err := ComputeFileHash(testFile)
	if err != nil {
		t.Fatalf("Failed to compute initial hash: %v", err)
	}

	// Check - should not have changed
	changed, err := HasChanged(testFile, initial.Hash)
	if err != nil {
		t.Fatalf("Failed to check if file changed: %v", err)
	}

	if changed {
		t.Error("File should not be marked as changed initially")
	}

	// Modify file
	time.Sleep(10 * time.Millisecond)
	modified := []byte("MODIFIED content for change detection")
	err = os.WriteFile(testFile, modified, 0o644)
	if err != nil {
		t.Fatalf("Failed to modify test file: %v", err)
	}

	// Check - should have changed
	changed, err = HasChanged(testFile, initial.Hash)
	if err != nil {
		t.Fatalf("Failed to check if file changed: %v", err)
	}

	if !changed {
		t.Error("File should be marked as changed after modification")
	}
}

func TestSmallFileHashing(t *testing.T) {
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "small.mkv")

	// Create file smaller than ChunkSize
	content := []byte("Small file content")
	err := os.WriteFile(testFile, content, 0o644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	hash, err := ComputeFileHash(testFile)
	if err != nil {
		t.Fatalf("Failed to compute hash for small file: %v", err)
	}

	if hash.Hash == "" {
		t.Error("Hash should not be empty for small file")
	}

	if hash.Size != int64(len(content)) {
		t.Errorf("Expected size %d, got %d", len(content), hash.Size)
	}
}

func BenchmarkComputeFileHash(b *testing.B) {
	// Create a large test file
	tempDir := b.TempDir()
	testFile := filepath.Join(tempDir, "large.mkv")

	// Create 100MB file
	largeData := make([]byte, 100*1024*1024)
	for i := range largeData {
		largeData[i] = byte(i % 256)
	}
	os.WriteFile(testFile, largeData, 0o644)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := ComputeFileHash(testFile)
		if err != nil {
			b.Fatalf("Failed to compute hash: %v", err)
		}
	}
}
