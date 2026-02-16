package tvdb

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newTestClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	client := NewClientWithBaseURL("test-key", ts.URL+"/v4")
	return client
}

func loginHandler(w http.ResponseWriter, _ *http.Request) {
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "success",
		"data":   map[string]string{"token": "test-token"},
	})
}

func TestGetSeriesEpisodes_SinglePage(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v4/login", loginHandler)
	mux.HandleFunc("/v4/series/100/episodes/official", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]interface{}{
				"episodes": []map[string]interface{}{
					{"id": 1, "seasonNumber": 1, "number": 1, "aired": "2024-01-15", "name": "Pilot"},
					{"id": 2, "seasonNumber": 1, "number": 2, "aired": "2024-01-22", "name": "Episode 2"},
					{"id": 3, "seasonNumber": 2, "number": 1, "aired": "2025-06-01", "name": "Season 2 Premiere"},
				},
			},
			"links": map[string]interface{}{"next": nil},
		})
	})

	client := newTestClient(t, mux)
	if err := client.Login(); err != nil {
		t.Fatalf("login failed: %v", err)
	}

	episodes, err := client.GetSeriesEpisodes(100)
	if err != nil {
		t.Fatalf("GetSeriesEpisodes failed: %v", err)
	}

	if len(episodes) != 3 {
		t.Fatalf("expected 3 episodes, got %d", len(episodes))
	}

	if episodes[0].Name != "Pilot" {
		t.Errorf("episode 0 name: expected 'Pilot', got %q", episodes[0].Name)
	}
	if episodes[0].SeasonNumber != 1 || episodes[0].Number != 1 {
		t.Errorf("episode 0: expected S01E01, got S%02dE%02d", episodes[0].SeasonNumber, episodes[0].Number)
	}
	if episodes[2].SeasonNumber != 2 {
		t.Errorf("episode 2: expected season 2, got %d", episodes[2].SeasonNumber)
	}
}

func TestGetSeriesEpisodes_MultiplePages(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v4/login", loginHandler)
	mux.HandleFunc("/v4/series/200/episodes/official", func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		switch page {
		case "0", "":
			next := "page1"
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"data": map[string]interface{}{
					"episodes": []map[string]interface{}{
						{"id": 1, "seasonNumber": 1, "number": 1, "aired": "2024-01-15", "name": "Ep 1"},
						{"id": 2, "seasonNumber": 1, "number": 2, "aired": "2024-01-22", "name": "Ep 2"},
					},
				},
				"links": map[string]interface{}{"next": &next},
			})
		case "1":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"data": map[string]interface{}{
					"episodes": []map[string]interface{}{
						{"id": 3, "seasonNumber": 1, "number": 3, "aired": "2024-01-29", "name": "Ep 3"},
					},
				},
				"links": map[string]interface{}{"next": nil},
			})
		default:
			t.Errorf("unexpected page: %s", page)
			w.WriteHeader(http.StatusBadRequest)
		}
	})

	client := newTestClient(t, mux)
	if err := client.Login(); err != nil {
		t.Fatalf("login failed: %v", err)
	}

	episodes, err := client.GetSeriesEpisodes(200)
	if err != nil {
		t.Fatalf("GetSeriesEpisodes failed: %v", err)
	}

	if len(episodes) != 3 {
		t.Fatalf("expected 3 episodes from 2 pages, got %d", len(episodes))
	}

	if episodes[2].Name != "Ep 3" {
		t.Errorf("last episode name: expected 'Ep 3', got %q", episodes[2].Name)
	}
}

func TestGetSeriesEpisodes_APIError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v4/login", loginHandler)
	mux.HandleFunc("/v4/series/999/episodes/official", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintln(w, "not found")
	})

	client := newTestClient(t, mux)
	if err := client.Login(); err != nil {
		t.Fatalf("login failed: %v", err)
	}

	_, err := client.GetSeriesEpisodes(999)
	if err == nil {
		t.Fatal("expected error for 404 response, got nil")
	}
}

func TestGetSeriesEpisodes_EmptyEpisodes(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v4/login", loginHandler)
	mux.HandleFunc("/v4/series/300/episodes/official", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]interface{}{
				"episodes": []interface{}{},
			},
			"links": map[string]interface{}{"next": nil},
		})
	})

	client := newTestClient(t, mux)
	if err := client.Login(); err != nil {
		t.Fatalf("login failed: %v", err)
	}

	episodes, err := client.GetSeriesEpisodes(300)
	if err != nil {
		t.Fatalf("GetSeriesEpisodes failed: %v", err)
	}

	if len(episodes) != 0 {
		t.Errorf("expected 0 episodes, got %d", len(episodes))
	}
}
