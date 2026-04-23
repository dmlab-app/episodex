package tmdb

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func newTestClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return NewClientWithBaseURL("test-token", ts.URL)
}

func TestFindByTVDBID_Success(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/find/12345", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("api_key"); got != "test-token" {
			t.Errorf("expected api_key query param, got %q", got)
		}
		if got := r.URL.Query().Get("external_source"); got != "tvdb_id" {
			t.Errorf("expected external_source=tvdb_id, got %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"tv_results": []map[string]interface{}{
				{
					"id":             500,
					"name":           "Breaking Bad",
					"original_name":  "Breaking Bad",
					"overview":       "Chem teacher cooks meth",
					"poster_path":    "/abc.jpg",
					"first_air_date": "2008-01-20",
					"vote_average":   9.5,
					"genre_ids":      []int{18, 80},
				},
			},
		})
	})

	client := newTestClient(t, mux)
	show, err := client.FindByTVDBID(12345)
	if err != nil {
		t.Fatalf("FindByTVDBID failed: %v", err)
	}
	if show == nil {
		t.Fatal("expected show, got nil")
	}
	if show.ID != 500 {
		t.Errorf("expected ID 500, got %d", show.ID)
	}
	if show.Name != "Breaking Bad" {
		t.Errorf("expected name 'Breaking Bad', got %q", show.Name)
	}
	if show.VoteAverage != 9.5 {
		t.Errorf("expected vote_average 9.5, got %v", show.VoteAverage)
	}
	if len(show.GenreIDs) != 2 || show.GenreIDs[0] != 18 {
		t.Errorf("unexpected genre_ids: %v", show.GenreIDs)
	}
}

func TestFindByTVDBID_NoResults(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/find/99999", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"tv_results": []interface{}{},
		})
	})
	client := newTestClient(t, mux)
	show, err := client.FindByTVDBID(99999)
	if err != nil {
		t.Fatalf("FindByTVDBID failed: %v", err)
	}
	if show != nil {
		t.Errorf("expected nil show, got %+v", show)
	}
}

func TestFindByTVDBID_404(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/find/404", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	client := newTestClient(t, mux)
	show, err := client.FindByTVDBID(404)
	if err != nil {
		t.Fatalf("unexpected error on 404: %v", err)
	}
	if show != nil {
		t.Errorf("expected nil show, got %+v", show)
	}
}

func TestFindByTVDBID_MalformedJSON(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/find/1", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("{not valid json"))
	})
	client := newTestClient(t, mux)
	_, err := client.FindByTVDBID(1)
	if err == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}
	if !strings.Contains(err.Error(), "decode") {
		t.Errorf("expected decode error, got: %v", err)
	}
}

func TestFindByTVDBID_MissingAPIKey(t *testing.T) {
	client := NewClient("")
	_, err := client.FindByTVDBID(1)
	if err == nil {
		t.Fatal("expected error for missing api key, got nil")
	}
}

func TestGetRecommendations_Success(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/tv/500/recommendations", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"page": 1,
			"results": []map[string]interface{}{
				{"id": 101, "name": "Show A", "vote_average": 8.2, "poster_path": "/a.jpg"},
				{"id": 102, "name": "Show B", "vote_average": 7.1, "poster_path": "/b.jpg"},
			},
			"total_results": 2,
		})
	})
	client := newTestClient(t, mux)
	results, err := client.GetRecommendations(500)
	if err != nil {
		t.Fatalf("GetRecommendations failed: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].ID != 101 || results[0].Name != "Show A" {
		t.Errorf("unexpected first result: %+v", results[0])
	}
}

func TestGetRecommendations_429Retry(t *testing.T) {
	var calls int32
	mux := http.NewServeMux()
	mux.HandleFunc("/tv/500/recommendations", func(w http.ResponseWriter, _ *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"results": []map[string]interface{}{
				{"id": 1, "name": "Retried"},
			},
		})
	})
	client := newTestClient(t, mux)
	results, err := client.GetRecommendations(500)
	if err != nil {
		t.Fatalf("GetRecommendations failed: %v", err)
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Errorf("expected 2 calls (original + retry), got %d", calls)
	}
	if len(results) != 1 || results[0].Name != "Retried" {
		t.Errorf("unexpected results: %+v", results)
	}
}

func TestGetRecommendations_429Persistent(t *testing.T) {
	var calls int32
	mux := http.NewServeMux()
	mux.HandleFunc("/tv/500/recommendations", func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusTooManyRequests)
	})
	client := newTestClient(t, mux)
	_, err := client.GetRecommendations(500)
	if err == nil {
		t.Fatal("expected error after persistent 429, got nil")
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Errorf("expected 2 calls, got %d", calls)
	}
}

func TestGetRecommendations_404(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/tv/999/recommendations", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	client := newTestClient(t, mux)
	results, err := client.GetRecommendations(999)
	if err != nil {
		t.Fatalf("unexpected error on 404: %v", err)
	}
	if results != nil {
		t.Errorf("expected nil results on 404, got %+v", results)
	}
}

func TestGetRecommendations_MalformedJSON(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/tv/1/recommendations", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("{broken"))
	})
	client := newTestClient(t, mux)
	_, err := client.GetRecommendations(1)
	if err == nil {
		t.Fatal("expected decode error, got nil")
	}
}

func TestGetExternalIDs_Success(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/tv/500/external_ids", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"id":      500,
			"imdb_id": "tt0903747",
			"tvdb_id": 81189,
		})
	})
	client := newTestClient(t, mux)
	ext, err := client.GetExternalIDs(500)
	if err != nil {
		t.Fatalf("GetExternalIDs failed: %v", err)
	}
	if ext == nil {
		t.Fatal("expected external ids, got nil")
	}
	if ext.TVDBId != 81189 {
		t.Errorf("expected tvdb_id 81189, got %d", ext.TVDBId)
	}
	if ext.IMDBId != "tt0903747" {
		t.Errorf("expected imdb_id tt0903747, got %q", ext.IMDBId)
	}
}

func TestGetExternalIDs_404(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/tv/404/external_ids", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	client := newTestClient(t, mux)
	ext, err := client.GetExternalIDs(404)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ext != nil {
		t.Errorf("expected nil, got %+v", ext)
	}
}

func TestPosterURL(t *testing.T) {
	if got := PosterURL(""); got != "" {
		t.Errorf("empty poster path: expected empty URL, got %q", got)
	}
	got := PosterURL("/abc.jpg")
	want := PosterBaseURL + "/abc.jpg"
	if got != want {
		t.Errorf("PosterURL: expected %q, got %q", want, got)
	}
}
