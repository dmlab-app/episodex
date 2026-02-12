package tvdb

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const (
	baseURL = "https://api4.thetvdb.com/v4"
)

// Client represents a TVDB API client
type Client struct {
	apiKey     string
	token      string
	httpClient *http.Client
	tokenExp   time.Time
}

// NewClient creates a new TVDB API client
func NewClient(apiKey string) *Client {
	return &Client{
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Login authenticates with TVDB API and obtains a token
func (c *Client) Login() error {
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

	resp, err := c.httpClient.Post(baseURL+"/login", "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to login to TVDB: %w", err)
	}
	defer resp.Body.Close()

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

// ensureToken makes sure we have a valid token
func (c *Client) ensureToken() error {
	if c.token == "" || time.Now().After(c.tokenExp) {
		return c.Login()
	}
	return nil
}

// makeRequest makes an authenticated request to TVDB API
func (c *Client) makeRequest(method, path string, body interface{}) (*http.Response, error) {
	if err := c.ensureToken(); err != nil {
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

	req, err := http.NewRequest(method, baseURL+path, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.token)
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
	TVDBId       int    `json:"tvdb_id"`
	Name         string `json:"name"`
	Image        string `json:"image"`
	Year         string `json:"year"`
	Status       string `json:"status"`
	Overview     string `json:"overview"`
	PrimaryType  string `json:"primary_type"`
	FirstAired   string `json:"first_aired"`
}

// SearchSeries searches for series by name
func (c *Client) SearchSeries(query string) ([]SeriesSearchResult, error) {
	resp, err := c.makeRequest("GET", "/search?query="+url.QueryEscape(query)+"&type=series", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("search failed with status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Status string `json:"status"`
		Data   []struct {
			ObjectID    string `json:"objectID"`
			Aliases     []string `json:"aliases"`
			Country     string `json:"country"`
			ID          string `json:"id"`
			ImageURL    string `json:"image_url"`
			Name        string `json:"name"`
			FirstAired  string `json:"first_air_time"`
			Overview    string `json:"overview"`
			PrimaryType string `json:"primary_type"`
			Status      string `json:"status"`
			Type        string `json:"type"`
			TvdbID      string `json:"tvdb_id"`
			Year        string `json:"year"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode search response: %w", err)
	}

	results := make([]SeriesSearchResult, 0, len(result.Data))
	for _, item := range result.Data {
		// Parse tvdb_id from string to int
		var tvdbID int
		fmt.Sscanf(item.TvdbID, "%d", &tvdbID)

		results = append(results, SeriesSearchResult{
			TVDBId:      tvdbID,
			Name:        item.Name,
			Image:       item.ImageURL,
			Year:        item.Year,
			Status:      item.Status,
			Overview:    item.Overview,
			PrimaryType: item.PrimaryType,
			FirstAired:  item.FirstAired,
		})
	}

	return results, nil
}

// SeriesDetails represents detailed information about a series
type SeriesDetails struct {
	TVDBId       int              `json:"tvdb_id"`
	Name         string           `json:"name"`
	Image        string           `json:"image"`
	Status       string           `json:"status"`
	Overview     string           `json:"overview"`
	FirstAired   string           `json:"first_aired"`
	LastAired    string           `json:"last_aired"`
	OriginalName string           `json:"original_name"`
	Seasons      []SeasonInfo     `json:"seasons"`
}

// SeriesExtended represents full information about a series including all metadata
type SeriesExtended struct {
	TVDBId           int               `json:"tvdb_id"`
	Name             string            `json:"name"`
	OriginalName     string            `json:"original_name"`
	Slug             string            `json:"slug"`
	Overview         string            `json:"overview"`
	Image            string            `json:"image"`
	Backdrop         string            `json:"backdrop"`
	Status           string            `json:"status"`
	FirstAired       string            `json:"first_aired"`
	LastAired        string            `json:"last_aired"`
	Year             int               `json:"year"`
	Runtime          int               `json:"runtime"`
	Score            float64           `json:"score"`
	ContentRatings   []ContentRating   `json:"content_ratings"`
	OriginalCountry  string            `json:"original_country"`
	OriginalLanguage string            `json:"original_language"`
	Genres           []Genre           `json:"genres"`
	Networks         []Company         `json:"networks"`
	Studios          []Company         `json:"studios"`
	Characters       []Character       `json:"characters"`
	Artworks         []Artwork         `json:"artworks"`
	Seasons          []SeasonInfo      `json:"seasons"`
}

// ContentRating represents content rating for a series
type ContentRating struct {
	Name    string `json:"name"`
	Country string `json:"country"`
}

// Genre represents a genre
type Genre struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

// Company represents a network or studio
type Company struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

// Character represents a character and actor
type Character struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	PersonName string `json:"person_name"`
	Image      string `json:"image"`
	Sort       int    `json:"sort"`
}

// Artwork represents artwork (poster, background, banner, etc.)
type Artwork struct {
	ID        int     `json:"id"`
	Type      int     `json:"type"`
	TypeName  string  `json:"type_name"`
	URL       string  `json:"url"`
	Thumbnail string  `json:"thumbnail"`
	Language  string  `json:"language"`
	Score     float64 `json:"score"`
	Width     int     `json:"width"`
	Height    int     `json:"height"`
}

// SeasonInfo represents information about a season
type SeasonInfo struct {
	ID     int    `json:"id"`
	Number int    `json:"number"`
	Name   string `json:"name"`
	Type   string `json:"type"`
	Year   string `json:"year"`
	Image  string `json:"image"`
}

// SeasonExtended represents detailed information about a season with episodes
type SeasonExtended struct {
	ID         int       `json:"id"`
	Number     int       `json:"number"`
	Name       string    `json:"name"`
	Overview   string    `json:"overview"`
	Image      string    `json:"image"`
	FirstAired string    `json:"first_aired"`
	Episodes   []Episode `json:"episodes"`
}

// Episode represents an episode
type Episode struct {
	ID       int     `json:"id"`
	Number   int     `json:"number"`
	Name     string  `json:"name"`
	Overview string  `json:"overview"`
	Image    string  `json:"image"`
	AirDate  string  `json:"air_date"`
	Runtime  int     `json:"runtime"`
	Rating   float64 `json:"rating"`
}

// ArtworkType represents an artwork type definition
type ArtworkType struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// GetSeriesDetails fetches detailed information about a series
func (c *Client) GetSeriesDetails(tvdbID int) (*SeriesDetails, error) {
	resp, err := c.makeRequest("GET", fmt.Sprintf("/series/%d/extended", tvdbID), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("get series failed with status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Status string `json:"status"`
		Data   struct {
			ID       int    `json:"id"`
			Name     string `json:"name"`
			Image    string `json:"image"`
			Status   struct {
				Name string `json:"name"`
			} `json:"status"`
			Overview     string `json:"overview"`
			FirstAired   string `json:"firstAired"`
			LastAired    string `json:"lastAired"`
			OriginalName string `json:"originalName"`
			Seasons      []struct {
				ID     int    `json:"id"`
				Number int    `json:"number"`
				Name   string `json:"name"`
				Year   string `json:"year"`
				Image  string `json:"image"`
				Type   struct {
					Name string `json:"name"`
					Type string `json:"type"`
				} `json:"type"`
			} `json:"seasons"`
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
		Seasons:      make([]SeasonInfo, 0),
	}

	// Filter out special seasons (season 0) and only include aired seasons
	// We only include seasons with type "official" or "aired" that have a year set
	for _, season := range result.Data.Seasons {
		if season.Number > 0 {
			// Only include seasons that are "official" type (already aired)
			// Skip seasons marked as "upcoming" or without a type
			seasonType := season.Type.Type
			if seasonType == "official" || seasonType == "aired" || seasonType == "" {
				// If year is set and not empty, it means the season has aired
				// If no year info, we assume it's aired (backwards compatibility)
				if season.Year != "" || seasonType == "official" {
					details.Seasons = append(details.Seasons, SeasonInfo{
						ID:     season.ID,
						Number: season.Number,
						Name:   season.Name,
						Type:   season.Type.Name,
						Year:   season.Year,
						Image:  season.Image,
					})
				}
			}
		}
	}

	return details, nil
}

// GetSeriesExtendedFull fetches all metadata for a series including characters, artworks, etc.
func (c *Client) GetSeriesExtendedFull(tvdbID int) (*SeriesExtended, error) {
	resp, err := c.makeRequest("GET", fmt.Sprintf("/series/%d/extended?meta=translations", tvdbID), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("get series extended failed with status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Status string `json:"status"`
		Data   struct {
			ID       int    `json:"id"`
			Name     string `json:"name"`
			Slug     string `json:"slug"`
			Image    string `json:"image"`
			Status   struct {
				Name string `json:"name"`
			} `json:"status"`
			Overview       string `json:"overview"`
			FirstAired     string `json:"firstAired"`
			LastAired      string `json:"lastAired"`
			OriginalName   string `json:"originalName"`
			Year           string `json:"year"`
			Score          float64 `json:"score"`
			AverageRuntime int    `json:"averageRuntime"`
			OriginalCountry string `json:"originalCountry"`
			OriginalLanguage string `json:"originalLanguage"`
			ContentRatings []struct {
				Name    string `json:"name"`
				Country string `json:"country"`
			} `json:"contentRatings"`
			Genres []struct {
				ID   int    `json:"id"`
				Name string `json:"name"`
				Slug string `json:"slug"`
			} `json:"genres"`
			Companies []struct {
				ID   int    `json:"id"`
				Name string `json:"name"`
				Slug string `json:"slug"`
				CompanyType struct {
					CompanyTypeName string `json:"companyTypeName"`
				} `json:"companyType"`
			} `json:"companies"`
			Characters []struct {
				ID         int    `json:"id"`
				Name       string `json:"name"`
				PersonName string `json:"personName"`
				Image      string `json:"image"`
				Sort       int    `json:"sort"`
			} `json:"characters"`
			Artworks []struct {
				ID        int     `json:"id"`
				Type      int     `json:"type"`
				TypeName  string  `json:"typeName"`
				Image     string  `json:"image"`
				Thumbnail string  `json:"thumbnail"`
				Language  string  `json:"language"`
				Score     float64 `json:"score"`
				Width     int     `json:"width"`
				Height    int     `json:"height"`
			} `json:"artworks"`
			Seasons []struct {
				ID     int    `json:"id"`
				Number int    `json:"number"`
				Name   string `json:"name"`
				Year   string `json:"year"`
				Image  string `json:"image"`
				Type   struct {
					Name string `json:"name"`
					Type string `json:"type"`
				} `json:"type"`
			} `json:"seasons"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode extended response: %w", err)
	}

	// Parse year from string
	var year int
	if result.Data.Year != "" {
		fmt.Sscanf(result.Data.Year, "%d", &year)
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
		if comp.CompanyType.CompanyTypeName == "Network" {
			extended.Networks = append(extended.Networks, company)
		} else if comp.CompanyType.CompanyTypeName == "Studio" {
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
	for _, season := range result.Data.Seasons {
		if season.Number > 0 {
			seasonType := season.Type.Type
			if seasonType == "official" || seasonType == "aired" || seasonType == "" {
				if season.Year != "" || seasonType == "official" {
					extended.Seasons = append(extended.Seasons, SeasonInfo{
						ID:     season.ID,
						Number: season.Number,
						Name:   season.Name,
						Type:   season.Type.Name,
						Year:   season.Year,
						Image:  season.Image,
					})
				}
			}
		}
	}

	return extended, nil
}

// GetSeasonEpisodes fetches all episodes for a specific season
func (c *Client) GetSeasonEpisodes(tvdbSeasonID int) ([]Episode, error) {
	resp, err := c.makeRequest("GET", fmt.Sprintf("/seasons/%d/extended", tvdbSeasonID), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("get season episodes failed with status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Status string `json:"status"`
		Data   struct {
			Episodes []struct {
				ID              int     `json:"id"`
				Number          int     `json:"number"`
				Name            string  `json:"name"`
				Overview        string  `json:"overview"`
				Image           string  `json:"image"`
				Aired           string  `json:"aired"`
				Runtime         int     `json:"runtime"`
				AverageRuntime  int     `json:"averageRuntime"`
				Score           float64 `json:"score"`
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

// GetTotalSeasons returns the total number of regular seasons (excluding specials)
func (c *Client) GetTotalSeasons(tvdbID int) (int, error) {
	details, err := c.GetSeriesDetails(tvdbID)
	if err != nil {
		return 0, err
	}
	return len(details.Seasons), nil
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
	defer resp.Body.Close()

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
	englishOverview := details.Overview

	// Try to get Russian translation
	rusTrans, err := c.GetSeriesTranslation(tvdbID, "rus")
	if err != nil {
		// Log error but continue with English
		// Non-critical: just use English names
	}

	if rusTrans != nil && rusTrans.Name != "" {
		// Use Russian name as primary
		details.Name = rusTrans.Name
		details.OriginalName = englishName
		if rusTrans.Overview != "" {
			details.Overview = rusTrans.Overview
		}
	} else {
		// No Russian translation, keep English as Name, OriginalName stays as is
		details.OriginalName = englishName
	}

	// If OriginalName is same as Name, clear it to avoid duplication
	if details.OriginalName == details.Name {
		details.OriginalName = englishOverview // Actually keep english name
		details.OriginalName = englishName
	}

	return details, nil
}
