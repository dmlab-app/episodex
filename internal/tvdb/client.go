// Package tvdb provides a client for the TVDB API.
package tvdb

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"
)

const (
	baseURL = "https://api4.thetvdb.com/v4"
)

// nowFunc is the time function used for aired detection. Override in tests.
var nowFunc = time.Now

// Client represents a TVDB API client
type Client struct {
	mu         sync.Mutex
	httpClient *http.Client
	tokenExp   time.Time
	apiKey     string
	token      string
	baseURL    string
}

// NewClient creates a new TVDB API client
func NewClient(apiKey string) *Client {
	return &Client{
		apiKey:  apiKey,
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// NewClientWithBaseURL creates a TVDB client with a custom base URL (for testing).
func NewClientWithBaseURL(apiKey, base string) *Client {
	return &Client{
		apiKey:  apiKey,
		baseURL: base,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Login authenticates with TVDB API and obtains a token
func (c *Client) Login() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.loginLocked()
}

// loginLocked performs the actual login. Caller must hold c.mu.
func (c *Client) loginLocked() error {
	if c.apiKey == "" {
		return fmt.Errorf("TVDB API key is not configured")
	}

	reqBody := map[string]string{
		"apikey": c.apiKey,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("failed to marshal login request: %w", err)
	}

	resp, err := c.httpClient.Post(c.baseURL+"/login", "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to login to TVDB: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("TVDB login failed with status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Status string `json:"status"`
		Data   struct {
			Token string `json:"token"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("failed to decode login response: %w", err)
	}

	if result.Data.Token == "" {
		return fmt.Errorf("no token received from TVDB")
	}

	c.token = result.Data.Token
	c.tokenExp = time.Now().Add(24 * time.Hour) // Token valid for 24 hours

	return nil
}

// getToken returns a valid token, refreshing if needed (thread-safe)
func (c *Client) getToken() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token == "" || time.Now().After(c.tokenExp) {
		if err := c.loginLocked(); err != nil {
			return "", err
		}
	}
	return c.token, nil
}

// makeRequest makes an authenticated request to TVDB API
func (c *Client) makeRequest(method, path string, body interface{}) (*http.Response, error) { //nolint:unparam
	token, err := c.getToken()
	if err != nil {
		return nil, err
	}

	var reqBody io.Reader
	if body != nil {
		jsonData, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewBuffer(jsonData)
	}

	req, err := http.NewRequest(method, c.baseURL+path, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	return resp, nil
}

// SeriesSearchResult represents a search result from TVDB
type SeriesSearchResult struct {
	Name        string `json:"name"`
	Image       string `json:"image"`
	Year        string `json:"year"`
	Status      string `json:"status"`
	Overview    string `json:"overview"`
	PrimaryType string `json:"primary_type"`
	FirstAired  string `json:"first_aired"`
	TVDBId      int    `json:"tvdb_id"`
}

// SearchSeries searches for series by name
func (c *Client) SearchSeries(query string) ([]SeriesSearchResult, error) {
	resp, err := c.makeRequest("GET", "/search?query="+url.QueryEscape(query)+"&type=series", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("search failed with status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Status string `json:"status"`
		Data   []struct {
			FirstAired  string   `json:"first_air_time"`
			ObjectID    string   `json:"objectID"`
			Country     string   `json:"country"`
			ID          string   `json:"id"`
			ImageURL    string   `json:"image_url"`
			Name        string   `json:"name"`
			Overview    string   `json:"overview"`
			PrimaryType string   `json:"primary_type"`
			Status      string   `json:"status"`
			Type        string   `json:"type"`
			TvdbID      string   `json:"tvdb_id"`
			Year        string   `json:"year"`
			Aliases     []string `json:"aliases"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode search response: %w", err)
	}

	results := make([]SeriesSearchResult, 0, len(result.Data))
	for i := range result.Data {
		// Parse tvdb_id from string to int
		var tvdbID int
		fmt.Sscanf(result.Data[i].TvdbID, "%d", &tvdbID) //nolint:errcheck

		results = append(results, SeriesSearchResult{
			TVDBId:      tvdbID,
			Name:        result.Data[i].Name,
			Image:       result.Data[i].ImageURL,
			Year:        result.Data[i].Year,
			Status:      result.Data[i].Status,
			Overview:    result.Data[i].Overview,
			PrimaryType: result.Data[i].PrimaryType,
			FirstAired:  result.Data[i].FirstAired,
		})
	}

	return results, nil
}

// SeriesDetails represents detailed information about a series
type SeriesDetails struct {
	Name         string       `json:"name"`
	Image        string       `json:"image"`
	Status       string       `json:"status"`
	Overview     string       `json:"overview"`
	FirstAired   string       `json:"first_aired"`
	LastAired    string       `json:"last_aired"`
	OriginalName string       `json:"original_name"`
	Seasons      []SeasonInfo `json:"seasons"`
	TVDBId       int          `json:"tvdb_id"`
}

// SeriesExtended represents full information about a series including all metadata
type SeriesExtended struct {
	LastAired        string          `json:"last_aired"`
	OriginalCountry  string          `json:"original_country"`
	OriginalLanguage string          `json:"original_language"`
	Overview         string          `json:"overview"`
	FirstAired       string          `json:"first_aired"`
	Status           string          `json:"status"`
	Slug             string          `json:"slug"`
	Name             string          `json:"name"`
	OriginalName     string          `json:"original_name"`
	Backdrop         string          `json:"backdrop"`
	Image            string          `json:"image"`
	Genres           []Genre         `json:"genres"`
	Seasons          []SeasonInfo    `json:"seasons"`
	Artworks         []Artwork       `json:"artworks"`
	Characters       []Character     `json:"characters"`
	ContentRatings   []ContentRating `json:"content_ratings"`
	Studios          []Company       `json:"studios"`
	Networks         []Company       `json:"networks"`
	Score            float64         `json:"score"`
	TVDBId           int             `json:"tvdb_id"`
	Year             int             `json:"year"`
	Runtime          int             `json:"runtime"`
}

// ContentRating represents content rating for a series
type ContentRating struct {
	Name    string `json:"name"`
	Country string `json:"country"`
}

// Genre represents a genre
type Genre struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
	ID   int    `json:"id"`
}

// Company represents a network or studio
type Company struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
	ID   int    `json:"id"`
}

// Character represents a character and actor
type Character struct {
	Name       string `json:"name"`
	PersonName string `json:"person_name"`
	Image      string `json:"image"`
	ID         int    `json:"id"`
	Sort       int    `json:"sort"`
}

// Artwork represents artwork (poster, background, banner, etc.)
type Artwork struct {
	TypeName  string  `json:"type_name"`
	URL       string  `json:"url"`
	Thumbnail string  `json:"thumbnail"`
	Language  string  `json:"language"`
	Score     float64 `json:"score"`
	ID        int     `json:"id"`
	Type      int     `json:"type"`
	Width     int     `json:"width"`
	Height    int     `json:"height"`
}

// SeasonInfo represents information about a season
type SeasonInfo struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	Year   string `json:"year"`
	Image  string `json:"image"`
	ID     int    `json:"id"`
	Number int    `json:"number"`
	Aired  bool   `json:"aired"`
}

// rawSeason represents the season data as returned by TVDB API responses.
// Used internally to deduplicate the filtering logic across API methods.
type rawSeason struct {
	Type struct {
		Name string `json:"name"`
		Type string `json:"type"`
	} `json:"type"`
	Name   string `json:"name"`
	Year   string `json:"year"`
	Image  string `json:"image"`
	ID     int    `json:"id"`
	Number int    `json:"number"`
}

// filterSeasons converts raw TVDB season data into SeasonInfo, filtering out
// specials (season 0), non-official types, and seasons without air date info.
func filterSeasons(raw []rawSeason) []SeasonInfo {
	seasons := make([]SeasonInfo, 0, len(raw))
	for _, season := range raw {
		if season.Number <= 0 {
			continue
		}
		seasonType := season.Type.Type
		if seasonType != "official" && seasonType != "aired" && seasonType != "" {
			continue
		}
		if season.Year == "" && seasonType != "official" {
			continue
		}
		aired := isSeasonAired(season.Year)
		if season.Year == "" && seasonType == "official" {
			aired = true
		}
		seasons = append(seasons, SeasonInfo{
			ID:     season.ID,
			Number: season.Number,
			Name:   season.Name,
			Type:   season.Type.Name,
			Year:   season.Year,
			Image:  season.Image,
			Aired:  aired,
		})
	}
	return seasons
}

// isSeasonAired checks whether a season has aired based on its year.
// A season is considered aired if its year is non-empty and <= the current year.
func isSeasonAired(year string) bool {
	if year == "" {
		return false
	}
	var y int
	if _, err := fmt.Sscanf(year, "%d", &y); err != nil || y <= 0 {
		return false
	}
	return y <= nowFunc().Year()
}

// MaxAiredSeasonNumber returns the highest season number among aired seasons.
// This is used for comparison against the user's max owned season number.
func MaxAiredSeasonNumber(seasons []SeasonInfo) int {
	maxNum := 0
	for _, s := range seasons {
		if s.Aired && s.Number > maxNum {
			maxNum = s.Number
		}
	}
	return maxNum
}

// AiredSeasonNumbers returns the season numbers of all aired seasons.
func AiredSeasonNumbers(seasons []SeasonInfo) []int {
	nums := make([]int, 0)
	for _, s := range seasons {
		if s.Aired {
			nums = append(nums, s.Number)
		}
	}
	return nums
}

// SeasonExtended represents detailed information about a season with episodes
type SeasonExtended struct {
	Name       string    `json:"name"`
	Overview   string    `json:"overview"`
	Image      string    `json:"image"`
	FirstAired string    `json:"first_aired"`
	Episodes   []Episode `json:"episodes"`
	ID         int       `json:"id"`
	Number     int       `json:"number"`
}

// Episode represents an episode
type Episode struct {
	Name     string  `json:"name"`
	Overview string  `json:"overview"`
	Image    string  `json:"image"`
	AirDate  string  `json:"air_date"`
	Rating   float64 `json:"rating"`
	ID       int     `json:"id"`
	Number   int     `json:"number"`
	Runtime  int     `json:"runtime"`
}

// ArtworkType represents an artwork type definition
type ArtworkType struct {
	Name string `json:"name"`
	ID   int    `json:"id"`
}

// GetSeriesDetails fetches detailed information about a series
func (c *Client) GetSeriesDetails(tvdbID int) (*SeriesDetails, error) {
	resp, err := c.makeRequest("GET", fmt.Sprintf("/series/%d/extended", tvdbID), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("get series failed with status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Status string `json:"status"`
		Data   struct {
			Status struct {
				Name string `json:"name"`
			} `json:"status"`
			Name         string      `json:"name"`
			Image        string      `json:"image"`
			Overview     string      `json:"overview"`
			FirstAired   string      `json:"firstAired"`
			LastAired    string      `json:"lastAired"`
			OriginalName string      `json:"originalName"`
			Seasons      []rawSeason `json:"seasons"`
			ID           int         `json:"id"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode series response: %w", err)
	}

	details := &SeriesDetails{
		TVDBId:       result.Data.ID,
		Name:         result.Data.Name,
		Image:        result.Data.Image,
		Status:       result.Data.Status.Name,
		Overview:     result.Data.Overview,
		FirstAired:   result.Data.FirstAired,
		LastAired:    result.Data.LastAired,
		OriginalName: result.Data.OriginalName,
		Seasons:      filterSeasons(result.Data.Seasons),
	}

	return details, nil
}

// GetSeriesExtendedFull fetches all metadata for a series including characters, artworks, etc.
func (c *Client) GetSeriesExtendedFull(tvdbID int) (*SeriesExtended, error) {
	resp, err := c.makeRequest("GET", fmt.Sprintf("/series/%d/extended?meta=translations", tvdbID), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("get series extended failed with status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Status string `json:"status"`
		Data   struct {
			OriginalCountry  string `json:"originalCountry"`
			FirstAired       string `json:"firstAired"`
			OriginalLanguage string `json:"originalLanguage"`
			Year             string `json:"year"`
			OriginalName     string `json:"originalName"`
			Slug             string `json:"slug"`
			Status           struct {
				Name string `json:"name"`
			} `json:"status"`
			Name      string      `json:"name"`
			Image     string      `json:"image"`
			Overview  string      `json:"overview"`
			LastAired string      `json:"lastAired"`
			Seasons   []rawSeason `json:"seasons"`
			Genres    []struct {
				Name string `json:"name"`
				Slug string `json:"slug"`
				ID   int    `json:"id"`
			} `json:"genres"`
			Artworks []struct {
				TypeName  string  `json:"typeName"`
				Image     string  `json:"image"`
				Thumbnail string  `json:"thumbnail"`
				Language  string  `json:"language"`
				Score     float64 `json:"score"`
				ID        int     `json:"id"`
				Type      int     `json:"type"`
				Width     int     `json:"width"`
				Height    int     `json:"height"`
			} `json:"artworks"`
			Characters []struct {
				Name       string `json:"name"`
				PersonName string `json:"personName"`
				Image      string `json:"image"`
				ID         int    `json:"id"`
				Sort       int    `json:"sort"`
			} `json:"characters"`
			ContentRatings []struct {
				Name    string `json:"name"`
				Country string `json:"country"`
			} `json:"contentRatings"`
			Companies []struct {
				CompanyType struct {
					CompanyTypeName string `json:"companyTypeName"`
				} `json:"companyType"`
				Name string `json:"name"`
				Slug string `json:"slug"`
				ID   int    `json:"id"`
			} `json:"companies"`
			Score          float64 `json:"score"`
			ID             int     `json:"id"`
			AverageRuntime int     `json:"averageRuntime"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode extended response: %w", err)
	}

	// Parse year from string
	var year int
	if result.Data.Year != "" {
		fmt.Sscanf(result.Data.Year, "%d", &year) //nolint:errcheck
	}

	extended := &SeriesExtended{
		TVDBId:           result.Data.ID,
		Name:             result.Data.Name,
		OriginalName:     result.Data.OriginalName,
		Slug:             result.Data.Slug,
		Overview:         result.Data.Overview,
		Image:            result.Data.Image,
		Status:           result.Data.Status.Name,
		FirstAired:       result.Data.FirstAired,
		LastAired:        result.Data.LastAired,
		Year:             year,
		Runtime:          result.Data.AverageRuntime,
		Score:            result.Data.Score,
		OriginalCountry:  result.Data.OriginalCountry,
		OriginalLanguage: result.Data.OriginalLanguage,
		ContentRatings:   make([]ContentRating, 0),
		Genres:           make([]Genre, 0),
		Networks:         make([]Company, 0),
		Studios:          make([]Company, 0),
		Characters:       make([]Character, 0),
		Artworks:         make([]Artwork, 0),
		Seasons:          make([]SeasonInfo, 0),
	}

	// Find backdrop from artworks
	for _, art := range result.Data.Artworks {
		if art.TypeName == "background" && extended.Backdrop == "" {
			extended.Backdrop = art.Image
		}
	}

	// Parse content ratings
	for _, cr := range result.Data.ContentRatings {
		extended.ContentRatings = append(extended.ContentRatings, ContentRating{
			Name:    cr.Name,
			Country: cr.Country,
		})
	}

	// Parse genres
	for _, g := range result.Data.Genres {
		extended.Genres = append(extended.Genres, Genre{
			ID:   g.ID,
			Name: g.Name,
			Slug: g.Slug,
		})
	}

	// Parse companies (networks and studios)
	for _, comp := range result.Data.Companies {
		company := Company{
			ID:   comp.ID,
			Name: comp.Name,
			Slug: comp.Slug,
		}
		switch comp.CompanyType.CompanyTypeName {
		case "Network":
			extended.Networks = append(extended.Networks, company)
		case "Studio":
			extended.Studios = append(extended.Studios, company)
		}
	}

	// Parse characters
	for _, char := range result.Data.Characters {
		extended.Characters = append(extended.Characters, Character{
			ID:         char.ID,
			Name:       char.Name,
			PersonName: char.PersonName,
			Image:      char.Image,
			Sort:       char.Sort,
		})
	}

	// Parse artworks
	for _, art := range result.Data.Artworks {
		extended.Artworks = append(extended.Artworks, Artwork{
			ID:        art.ID,
			Type:      art.Type,
			TypeName:  art.TypeName,
			URL:       art.Image,
			Thumbnail: art.Thumbnail,
			Language:  art.Language,
			Score:     art.Score,
			Width:     art.Width,
			Height:    art.Height,
		})
	}

	// Parse seasons (filter aired only)
	extended.Seasons = filterSeasons(result.Data.Seasons)

	return extended, nil
}

// GetSeasonEpisodes fetches all episodes for a specific season
func (c *Client) GetSeasonEpisodes(tvdbSeasonID int) ([]Episode, error) {
	resp, err := c.makeRequest("GET", fmt.Sprintf("/seasons/%d/extended", tvdbSeasonID), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("get season episodes failed with status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Status string `json:"status"`
		Data   struct {
			Episodes []struct {
				Name           string  `json:"name"`
				Overview       string  `json:"overview"`
				Image          string  `json:"image"`
				Aired          string  `json:"aired"`
				Score          float64 `json:"score"`
				ID             int     `json:"id"`
				Number         int     `json:"number"`
				Runtime        int     `json:"runtime"`
				AverageRuntime int     `json:"averageRuntime"`
			} `json:"episodes"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode season response: %w", err)
	}

	episodes := make([]Episode, 0, len(result.Data.Episodes))
	for _, ep := range result.Data.Episodes {
		runtime := ep.Runtime
		if runtime == 0 {
			runtime = ep.AverageRuntime
		}
		episodes = append(episodes, Episode{
			ID:       ep.ID,
			Number:   ep.Number,
			Name:     ep.Name,
			Overview: ep.Overview,
			Image:    ep.Image,
			AirDate:  ep.Aired,
			Runtime:  runtime,
			Rating:   ep.Score,
		})
	}

	return episodes, nil
}

// SeriesTranslation represents a translation for a series
type SeriesTranslation struct {
	Name     string `json:"name"`
	Overview string `json:"overview"`
	Language string `json:"language"`
}

// GetSeriesTranslation fetches translation for a series in specified language
func (c *Client) GetSeriesTranslation(tvdbID int, language string) (*SeriesTranslation, error) {
	resp, err := c.makeRequest("GET", fmt.Sprintf("/series/%d/translations/%s", tvdbID, language), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		// Translation not found is not an error, just return nil
		if resp.StatusCode == http.StatusNotFound {
			return nil, nil
		}
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("get translation failed with status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Status string `json:"status"`
		Data   struct {
			Name     string `json:"name"`
			Overview string `json:"overview"`
			Language string `json:"language"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode translation response: %w", err)
	}

	// If name is empty, translation doesn't exist
	if result.Data.Name == "" {
		return nil, nil
	}

	return &SeriesTranslation{
		Name:     result.Data.Name,
		Overview: result.Data.Overview,
		Language: result.Data.Language,
	}, nil
}

// GetSeriesDetailsWithRussian fetches series details with Russian translation preferred
// Returns: Name = Russian (or English fallback), OriginalName = English
func (c *Client) GetSeriesDetailsWithRussian(tvdbID int) (*SeriesDetails, error) {
	// Get base details
	details, err := c.GetSeriesDetails(tvdbID)
	if err != nil {
		return nil, err
	}

	// Store English name as original
	englishName := details.Name

	// Try to get Russian translation
	rusTrans, _ := c.GetSeriesTranslation(tvdbID, "rus") // Non-critical: just use English names

	if rusTrans != nil && rusTrans.Name != "" {
		// Use Russian name as primary
		details.Name = rusTrans.Name
		details.OriginalName = englishName
		if rusTrans.Overview != "" {
			details.Overview = rusTrans.Overview
		}
	} else if details.OriginalName == "" {
		// No Russian translation — only set OriginalName if TVDB didn't provide one
		// (preserves non-English originals like Japanese for anime)
		details.OriginalName = englishName
	}

	return details, nil
}
