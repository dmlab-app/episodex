package tracker

import (
	"strings"
	"testing"
)

// mockClient is a test double for Client.
type mockClient struct {
	domain string
}

func (m *mockClient) CanHandle(trackerURL string) bool {
	return strings.Contains(trackerURL, m.domain)
}

func (m *mockClient) GetEpisodeCount(_ string) (int, error) {
	return 0, nil
}

func (m *mockClient) DownloadTorrent(_ string) ([]byte, error) {
	return nil, nil
}

func TestRegistryGetClient(t *testing.T) {
	registry := NewRegistry()

	kinozal := &mockClient{domain: "kinozal.tv"}
	rutracker := &mockClient{domain: "rutracker.org"}

	registry.Register(kinozal)
	registry.Register(rutracker)

	t.Run("returns kinozal client for kinozal URL", func(t *testing.T) {
		client, err := registry.GetClient("https://kinozal.tv/details.php?id=123")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if client != kinozal {
			t.Fatal("expected kinozal client")
		}
	})

	t.Run("returns rutracker client for rutracker URL", func(t *testing.T) {
		client, err := registry.GetClient("https://rutracker.org/forum/viewtopic.php?t=456")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if client != rutracker {
			t.Fatal("expected rutracker client")
		}
	})

	t.Run("returns error for unknown URL", func(t *testing.T) {
		_, err := registry.GetClient("https://unknown-tracker.com/torrent/789")
		if err == nil {
			t.Fatal("expected error for unknown URL")
		}
	})
}

func TestRegistryEmpty(t *testing.T) {
	registry := NewRegistry()

	_, err := registry.GetClient("https://kinozal.tv/details.php?id=123")
	if err == nil {
		t.Fatal("expected error from empty registry")
	}
}

func TestRegistryFirstMatchWins(t *testing.T) {
	registry := NewRegistry()

	first := &mockClient{domain: "kinozal.tv"}
	second := &mockClient{domain: "kinozal.tv"}

	registry.Register(first)
	registry.Register(second)

	client, err := registry.GetClient("https://kinozal.tv/details.php?id=123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client != first {
		t.Fatal("expected first registered client to win")
	}
}
