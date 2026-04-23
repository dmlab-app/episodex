// Package tmdb provides a client for the TMDB API.
package tmdb

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"
)

const (
	baseURL = "https://api.themoviedb.org/3"
	// PosterBaseURL is the TMDB image CDN prefix for poster_path values (w342 size).
	PosterBaseURL = "https://image.tmdb.org/t/p/w342"
)

// Client represents a TMDB API client.
type Client struct {
	mu         sync.Mutex
	httpClient *http.Client
	apiKey     string
	baseURL    string
}

// NewClient creates a new TMDB API client using the v3 API key.
func NewClient(apiKey string) *Client {
	return &Client{
		apiKey:  apiKey,
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// NewClientWithBaseURL creates a TMDB client with a custom base URL (for testing).
func NewClientWithBaseURL(apiKey, base string) *Client {
	return &Client{
		apiKey:  apiKey,
		baseURL: base,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// TMDBShow represents a TV show entry as returned by TMDB list endpoints.
type TMDBShow struct {
	Name         string  `json:"name"`
	OriginalName string  `json:"original_name"`
	Overview     string  `json:"overview"`
	PosterPath   string  `json:"poster_path"`
	FirstAirDate string  `json:"first_air_date"`
	VoteAverage  float64 `json:"vote_average"`
	GenreIDs     []int   `json:"genre_ids"`
	ID           int     `json:"id"`
}

// FindResult is the response from /find/{external_id}.
type FindResult struct {
	TVResults []TMDBShow `json:"tv_results"`
}

// RecommendationsResponse is the response from /tv/{id}/recommendations.
type RecommendationsResponse struct {
	Results []TMDBShow `json:"results"`
	Page    int        `json:"page"`
	Total   int        `json:"total_results"`
}

// ExternalIDs is the response from /tv/{id}/external_ids.
type ExternalIDs struct {
	IMDBId string `json:"imdb_id"`
	TVDBId int    `json:"tvdb_id"`
	ID     int    `json:"id"`
}

// makeRequest performs an authenticated GET request with a single 429 retry.
func (c *Client) makeRequest(method, path string, params url.Values) (*http.Response, error) {
	if c.apiKey == "" {
		return nil, fmt.Errorf("TMDB API key is not configured")
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if params == nil {
		params = url.Values{}
	}
	params.Set("api_key", c.apiKey)
	params.Set("language", "ru-RU")
	fullURL := c.baseURL + path + "?" + params.Encode()

	doRequest := func() (*http.Response, error) {
		req, err := http.NewRequest(method, fullURL, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}
		req.Header.Set("Accept", "application/json")
		return c.httpClient.Do(req)
	}

	resp, err := doRequest()
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		resp.Body.Close() //nolint:errcheck
		time.Sleep(1 * time.Second)
		resp, err = doRequest()
		if err != nil {
			return nil, fmt.Errorf("request retry failed: %w", err)
		}
	}

	return resp, nil
}

// PosterURL builds the full CDN URL for a poster_path, or returns "" if empty.
func PosterURL(posterPath string) string {
	if posterPath == "" {
		return ""
	}
	return PosterBaseURL + posterPath
}

// FindByTVDBID maps a TVDB ID to a TMDB show. Returns nil if TMDB has no match.
func (c *Client) FindByTVDBID(tvdbID int) (*TMDBShow, error) {
	params := url.Values{}
	params.Set("external_source", "tvdb_id")

	resp, err := c.makeRequest("GET", fmt.Sprintf("/find/%d", tvdbID), params)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("find failed with status %d: %s", resp.StatusCode, string(body))
	}

	var result FindResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode find response: %w", err)
	}

	if len(result.TVResults) == 0 {
		return nil, nil
	}
	show := result.TVResults[0]
	return &show, nil
}

// GetRecommendations returns TMDB's recommended TV shows for a given TMDB show ID.
func (c *Client) GetRecommendations(tmdbID int) ([]TMDBShow, error) {
	resp, err := c.makeRequest("GET", fmt.Sprintf("/tv/%d/recommendations", tmdbID), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("recommendations failed with status %d: %s", resp.StatusCode, string(body))
	}

	var result RecommendationsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode recommendations response: %w", err)
	}
	return result.Results, nil
}

// GetExternalIDs returns external IDs (incl. tvdb_id) for a TMDB TV show.
func (c *Client) GetExternalIDs(tmdbID int) (*ExternalIDs, error) {
	resp, err := c.makeRequest("GET", fmt.Sprintf("/tv/%d/external_ids", tmdbID), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("external_ids failed with status %d: %s", resp.StatusCode, string(body))
	}

	var result ExternalIDs
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode external_ids response: %w", err)
	}
	return &result, nil
}
