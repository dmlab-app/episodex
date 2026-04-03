// Package kinozal implements the TrackerClient interface for kinozal.tv.
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
)

// Client is a Kinozal tracker client that handles authentication and torrent operations.
type Client struct {
	baseURL  string
	user     string
	password string
	mu       sync.Mutex
	cookie   *http.Cookie
	client   *http.Client
}

// NewClient creates a new Kinozal client.
func NewClient(user, password string) *Client {
	return &Client{
		baseURL:  "https://kinozal.tv",
		user:     user,
		password: password,
		client:   &http.Client{Timeout: 30 * time.Second},
	}
}

// NewClientWithBaseURL creates a new Kinozal client with a custom base URL (for testing).
func NewClientWithBaseURL(baseURL, user, password string) *Client {
	return &Client{
		baseURL:  strings.TrimRight(baseURL, "/"),
		user:     user,
		password: password,
		client:   &http.Client{Timeout: 30 * time.Second},
	}
}

// CanHandle returns true if the URL is a kinozal.tv URL.
func (c *Client) CanHandle(trackerURL string) bool {
	return strings.Contains(trackerURL, "kinozal.tv")
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

	for _, cookie := range resp.Cookies() {
		if cookie.Name == "uid" {
			c.cookie = cookie
			return nil
		}
	}

	return fmt.Errorf("kinozal login failed: no uid cookie in response")
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
	if c.cookie != nil {
		req.AddCookie(c.cookie)
	}
	return c.client.Do(req)
}

var (
	titleRe   = regexp.MustCompile(`(?i)<title>([^<]+)</title>`)
	episodeRe = regexp.MustCompile(`(\d+)-(\d+)\s+сери[ийя]`)
)

// parseEpisodeCount extracts the max episode number from a Kinozal torrent page title.
// Title format: "Сериал (N сезон: 1-X серии из Y)" → returns X.
func parseEpisodeCount(title string) int {
	m := episodeRe.FindStringSubmatch(title)
	if m == nil {
		return 0
	}
	n, err := strconv.Atoi(m[2])
	if err != nil {
		return 0
	}
	return n
}

// GetEpisodeCount fetches the torrent page and returns the number of episodes available.
func (c *Client) GetEpisodeCount(trackerURL string) (int, error) {
	resp, err := c.doRequest(trackerURL)
	if err != nil {
		return 0, fmt.Errorf("kinozal fetch page failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("kinozal read body failed: %w", err)
	}

	m := titleRe.FindSubmatch(body)
	if m == nil {
		return 0, nil
	}

	return parseEpisodeCount(string(m[1])), nil
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
	return id, nil
}

// DownloadTorrent downloads the .torrent file by tracker URL, returns raw bytes.
func (c *Client) DownloadTorrent(trackerURL string) ([]byte, error) {
	id, err := parseIDFromURL(trackerURL)
	if err != nil {
		return nil, err
	}

	downloadURL := c.baseURL + "/download.php?id=" + id
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
