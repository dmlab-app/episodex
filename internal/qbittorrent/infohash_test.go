package qbittorrent

import (
	"crypto/sha1"
	"encoding/hex"
	"testing"
)

func TestComputeInfoHash_Valid(t *testing.T) {
	// Build a minimal torrent: d4:infod4:name4:test12:piece lengthi16384e6:pieces0:ee
	infoDict := []byte("d4:name4:test12:piece lengthi16384e6:pieces0:e")
	torrent := []byte("d4:info" + string(infoDict) + "e")

	expected := sha1.Sum(infoDict)
	expectedHex := hex.EncodeToString(expected[:])

	hash, err := ComputeInfoHash(torrent)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if hash != expectedHex {
		t.Errorf("expected hash %s, got %s", expectedHex, hash)
	}
}

func TestComputeInfoHash_WithOtherKeys(t *testing.T) {
	// Torrent with keys before and after "info"
	infoDict := []byte("d4:name4:teste")
	torrent := []byte("d7:comment4:test4:info" + string(infoDict) + "8:url-list3:urle")

	expected := sha1.Sum(infoDict)
	expectedHex := hex.EncodeToString(expected[:])

	hash, err := ComputeInfoHash(torrent)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if hash != expectedHex {
		t.Errorf("expected hash %s, got %s", expectedHex, hash)
	}
}

func TestComputeInfoHash_EmptyData(t *testing.T) {
	_, err := ComputeInfoHash([]byte{})
	if err == nil {
		t.Fatal("expected error for empty data")
	}
}

func TestComputeInfoHash_NotDict(t *testing.T) {
	_, err := ComputeInfoHash([]byte("l4:teste"))
	if err == nil {
		t.Fatal("expected error for non-dict torrent")
	}
}

func TestComputeInfoHash_NoInfoKey(t *testing.T) {
	_, err := ComputeInfoHash([]byte("d4:name4:teste"))
	if err == nil {
		t.Fatal("expected error when info key is missing")
	}
}

func TestComputeInfoHash_ConsistentResults(t *testing.T) {
	torrent := validTorrentData()
	hash1, err := ComputeInfoHash(torrent)
	if err != nil {
		t.Fatal(err)
	}
	hash2, err := ComputeInfoHash(torrent)
	if err != nil {
		t.Fatal(err)
	}
	if hash1 != hash2 {
		t.Errorf("inconsistent hashes: %s != %s", hash1, hash2)
	}
}
