// Package kinozal implements the TrackerClient interface for kinozal.tv.
package kinozal

import (
	"fmt"
	"net/http"
	"net/url"
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

// GetEpisodeCount fetches the torrent page and returns the number of episodes available.
func (c *Client) GetEpisodeCount(_ string) (int, error) {
	return 0, fmt.Errorf("not implemented")
}

// DownloadTorrent downloads the .torrent file by tracker URL, returns raw bytes.
func (c *Client) DownloadTorrent(_ string) ([]byte, error) {
	return nil, fmt.Errorf("not implemented")
}
