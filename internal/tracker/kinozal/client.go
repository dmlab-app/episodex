// Package kinozal implements the tracker.Client interface for kinozal.tv.
package kinozal

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/text/encoding/charmap"
)

// Client is a Kinozal tracker client that handles authentication and torrent operations.
type Client struct {
	baseURL     string
	downloadURL string
	user        string
	password    string
	mu          sync.Mutex
	cookies     []*http.Cookie
	client      *http.Client
}

// NewClient creates a new Kinozal client.
func NewClient(user, password string) *Client {
	return &Client{
		baseURL:     "https://kinozal.tv",
		downloadURL: "https://dl.kinozal.tv",
		user:        user,
		password:    password,
		client: &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// NewClientWithBaseURL creates a new Kinozal client with a custom base URL (for testing).
func NewClientWithBaseURL(baseURL, user, password string) *Client {
	return &Client{
		baseURL:     strings.TrimRight(baseURL, "/"),
		downloadURL: strings.TrimRight(baseURL, "/"),
		user:        user,
		password:    password,
		client: &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// CanHandle returns true if the URL is a kinozal.tv URL.
func (c *Client) CanHandle(trackerURL string) bool {
	u, err := url.Parse(trackerURL)
	if err != nil {
		return false
	}
	host := u.Hostname()
	return host == "kinozal.tv" || host == "www.kinozal.tv"
}

// Login authenticates with Kinozal and stores the session cookie.
func (c *Client) Login() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.login()
}

func (c *Client) login() error {
	form := url.Values{
		"username": {c.user},
		"password": {c.password},
	}

	resp, err := c.client.PostForm(c.baseURL+"/takelogin.php", form)
	if err != nil {
		return fmt.Errorf("kinozal login request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("kinozal login failed: invalid credentials")
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusFound {
		return fmt.Errorf("kinozal login failed: status %d", resp.StatusCode)
	}

	cookies := resp.Cookies()
	hasUID := false
	for _, cookie := range cookies {
		if cookie.Name == "uid" {
			hasUID = true
			break
		}
	}
	if !hasUID {
		return fmt.Errorf("kinozal login failed: no uid cookie in response")
	}

	c.cookies = cookies
	return nil
}

// doRequest performs an authenticated GET request with automatic re-login on 403.
func (c *Client) doRequest(reqURL string) (*http.Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	resp, err := c.rawRequest(reqURL)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode == http.StatusForbidden {
		_ = resp.Body.Close()
		if err := c.login(); err != nil {
			return nil, fmt.Errorf("kinozal re-login failed: %w", err)
		}
		return c.rawRequest(reqURL)
	}

	return resp, nil
}

func (c *Client) rawRequest(reqURL string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, reqURL, http.NoBody)
	if err != nil {
		return nil, err
	}
	for _, cookie := range c.cookies {
		req.AddCookie(cookie)
	}
	return c.client.Do(req)
}

// SearchResult represents a single search result from Kinozal browse page.
type SearchResult struct {
	Title      string
	Size       string
	DetailsURL string
}

var (
	titleRe         = regexp.MustCompile(`(?i)<title>([^<]+)</title>`)
	episodeRangeRe  = regexp.MustCompile(`(\d+)-(\d+)\s+сери[ийя]`)
	episodeSingleRe = regexp.MustCompile(`(\d+)\s+сери[ийя]`)
	updatedAtRe     = regexp.MustCompile(`Обновлялся\s+(.+?)</`)
	searchRowRe     = regexp.MustCompile(`(?s)<tr\s+class="bg">\s*<td\s+class="nam"><a\s+href="([^"]+)">([^<]+)</a></td>\s*<td\s+class="s">([^<]+)</td>`)
)

// parseEpisodeCount extracts the max episode number from a Kinozal torrent page title.
// Title format: "Сериал (N сезон: 1-X серии из Y)" → returns X.
// Also handles single-episode format: "Сериал (N сезон: 1 серия из Y)" → returns 1.
func parseEpisodeCount(title string) int {
	// Try range format first: "1-8 серии"
	m := episodeRangeRe.FindStringSubmatch(title)
	if m != nil {
		n, err := strconv.Atoi(m[2])
		if err != nil {
			return 0
		}
		return n
	}
	// Fallback to single-episode format: "1 серия"
	m = episodeSingleRe.FindStringSubmatch(title)
	if m != nil {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			return 0
		}
		return n
	}
	return 0
}

// parseSearchResults extracts search results from Kinozal browse page HTML.
func parseSearchResults(body []byte) []SearchResult {
	matches := searchRowRe.FindAllSubmatch(body, -1)
	results := make([]SearchResult, 0, len(matches))
	for _, m := range matches {
		results = append(results, SearchResult{
			DetailsURL: string(m[1]),
			Title:      string(m[2]),
			Size:       strings.TrimSpace(string(m[3])),
		})
	}
	return results
}

// Search searches Kinozal for torrents matching the query and returns parsed results.
func (c *Client) Search(query string) ([]SearchResult, error) {
	searchURL := c.baseURL + "/browse.php?s=" + url.QueryEscape(query) + "&g=0&c=0&v=0&d=0&w=0&t=0&f=0"
	body, err := c.fetchPage(searchURL)
	if err != nil {
		return nil, fmt.Errorf("kinozal search failed: %w", err)
	}
	return parseSearchResults(body), nil
}

// fetchPage downloads a Kinozal page and returns decoded UTF-8 body.
func (c *Client) fetchPage(trackerURL string) ([]byte, error) {
	resp, err := c.doRequest(trackerURL)
	if err != nil {
		return nil, fmt.Errorf("kinozal fetch page failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("kinozal fetch page: status %d", resp.StatusCode)
	}

	// Kinozal pages are served in Windows-1251 encoding; decode to UTF-8
	reader := charmap.Windows1251.NewDecoder().Reader(resp.Body)
	return io.ReadAll(reader)
}

// GetEpisodeCount fetches the torrent page and returns the number of episodes available.
func (c *Client) GetEpisodeCount(trackerURL string) (int, error) {
	body, err := c.fetchPage(trackerURL)
	if err != nil {
		return 0, err
	}

	m := titleRe.FindSubmatch(body)
	if m == nil {
		return 0, nil
	}

	return parseEpisodeCount(string(m[1])), nil
}

// GetPageInfo fetches the torrent page and returns episode count + last updated string in one request.
func (c *Client) GetPageInfo(trackerURL string) (episodeCount int, lastUpdated string, err error) {
	body, err := c.fetchPage(trackerURL)
	if err != nil {
		return 0, "", err
	}

	if m := titleRe.FindSubmatch(body); m != nil {
		episodeCount = parseEpisodeCount(string(m[1]))
	}

	if m := updatedAtRe.FindSubmatch(body); m != nil {
		lastUpdated = strings.TrimSpace(string(m[1]))
	}

	return episodeCount, lastUpdated, nil
}

// GetLastUpdated fetches the torrent page and returns the update timestamp string.
// Implements tracker.UpdateChecker interface.
func (c *Client) GetLastUpdated(trackerURL string) (string, error) {
	_, lastUpdated, err := c.GetPageInfo(trackerURL)
	return lastUpdated, err
}

// parseIDFromURL extracts the torrent ID from a Kinozal URL like /details.php?id=2107649.
func parseIDFromURL(trackerURL string) (string, error) {
	u, err := url.Parse(trackerURL)
	if err != nil {
		return "", fmt.Errorf("invalid tracker URL: %w", err)
	}
	id := u.Query().Get("id")
	if id == "" {
		return "", fmt.Errorf("no id parameter in tracker URL: %s", trackerURL)
	}
	if _, err := strconv.Atoi(id); err != nil {
		return "", fmt.Errorf("invalid non-numeric id in tracker URL: %s", id)
	}
	return id, nil
}

// DownloadTorrent downloads the .torrent file by tracker URL, returns raw bytes.
func (c *Client) DownloadTorrent(trackerURL string) ([]byte, error) {
	id, err := parseIDFromURL(trackerURL)
	if err != nil {
		return nil, err
	}

	downloadURL := c.downloadURL + "/download.php?id=" + id
	resp, err := c.doRequest(downloadURL)
	if err != nil {
		return nil, fmt.Errorf("kinozal download failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("kinozal download failed: status %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("kinozal read torrent failed: %w", err)
	}

	return data, nil
}
