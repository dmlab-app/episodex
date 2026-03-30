package qbittorrent

import (
	"testing"
)

func TestFindTorrentByFolder_BasicMatch(t *testing.T) {
	torrents := []Torrent{
		{Name: "Breaking.Bad.S01.1080p.BluRay", SavePath: "/downloads/", Hash: "abc123"},
		{Name: "Better.Call.Saul.S03", SavePath: "/downloads/", Hash: "def456"},
	}

	result := FindTorrentByFolder(torrents, "/media/Breaking.Bad.S01.1080p.BluRay")
	if result == nil {
		t.Fatal("expected a match, got nil")
	}
	if result.Hash != "abc123" {
		t.Errorf("expected hash abc123, got %s", result.Hash)
	}
}

func TestFindTorrentByFolder_NoMatch(t *testing.T) {
	torrents := []Torrent{
		{Name: "Some.Other.Show.S01", SavePath: "/downloads/", Hash: "abc123"},
	}

	result := FindTorrentByFolder(torrents, "/media/Breaking.Bad.S01")
	if result != nil {
		t.Errorf("expected nil, got %+v", result)
	}
}

func TestFindTorrentByFolder_TrailingSlash(t *testing.T) {
	torrents := []Torrent{
		{Name: "Breaking.Bad.S01", SavePath: "/downloads/", Hash: "abc123"},
	}

	result := FindTorrentByFolder(torrents, "/media/Breaking.Bad.S01/")
	if result == nil {
		t.Fatal("expected a match, got nil")
	}
	if result.Hash != "abc123" {
		t.Errorf("expected hash abc123, got %s", result.Hash)
	}
}

func TestFindTorrentByFolder_CaseSensitive(t *testing.T) {
	torrents := []Torrent{
		{Name: "breaking.bad.s01", SavePath: "/downloads/", Hash: "abc123"},
	}

	result := FindTorrentByFolder(torrents, "/media/Breaking.Bad.S01")
	if result != nil {
		t.Errorf("expected nil (case sensitive), got %+v", result)
	}
}

func TestFindTorrentByFolder_FirstMatchWins(t *testing.T) {
	torrents := []Torrent{
		{Name: "Breaking.Bad.S01", SavePath: "/downloads/", Hash: "first"},
		{Name: "Breaking.Bad.S01", SavePath: "/other/", Hash: "second"},
	}

	result := FindTorrentByFolder(torrents, "/media/Breaking.Bad.S01")
	if result == nil {
		t.Fatal("expected a match, got nil")
	}
	if result.Hash != "first" {
		t.Errorf("expected first torrent to win, got %s", result.Hash)
	}
}

func TestFindTorrentByFolder_EmptyInputs(t *testing.T) {
	result := FindTorrentByFolder(nil, "/some/path")
	if result != nil {
		t.Errorf("expected nil for nil torrents, got %+v", result)
	}

	result = FindTorrentByFolder([]Torrent{}, "/some/path")
	if result != nil {
		t.Errorf("expected nil for empty torrents, got %+v", result)
	}
}
