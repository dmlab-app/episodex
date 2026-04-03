// Package qbittorrent provides a client for the qBittorrent Web API.
package qbittorrent

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"
)

// ErrTorrentNotFound is returned when a torrent hash no longer exists.
var ErrTorrentNotFound = errors.New("torrent not found")

// Torrent represents a torrent from qBittorrent.
type Torrent struct {
	Name     string `json:"name"`
	SavePath string `json:"save_path"`
	Hash     string `json:"hash"`
}

// Properties represents torrent properties from qBittorrent.
type Properties struct {
	Comment string `json:"comment"`
}

// Client communicates with a qBittorrent instance via its Web API.
type Client struct {
	baseURL  string
	user     string
	password string
	mu       sync.Mutex
	cookie   *http.Cookie
	client   *http.Client
}

// NewClient creates a new qBittorrent API client.
func NewClient(baseURL, user, password string) *Client {
	return &Client{
		baseURL:  strings.TrimRight(baseURL, "/"),
		user:     user,
		password: password,
		client:   &http.Client{Timeout: 10 * time.Second},
	}
}

// Login authenticates with the qBittorrent API and stores the session cookie.
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

	resp, err := c.client.PostForm(c.baseURL+"/api/v2/auth/login", form)
	if err != nil {
		return fmt.Errorf("qbittorrent login request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("qbittorrent login failed: invalid credentials")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("qbittorrent login failed: status %d", resp.StatusCode)
	}

	for _, cookie := range resp.Cookies() {
		if cookie.Name == "SID" {
			c.cookie = cookie
			return nil
		}
	}

	return fmt.Errorf("qbittorrent login failed: no SID cookie in response")
}

// doRequest performs an authenticated HTTP request with automatic re-login on 403.
func (c *Client) doRequest(reqPath string) (*http.Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	resp, err := c.rawRequest(reqPath)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode == http.StatusForbidden {
		_ = resp.Body.Close()
		if err := c.login(); err != nil {
			return nil, fmt.Errorf("re-login failed: %w", err)
		}
		return c.rawRequest(reqPath)
	}

	return resp, nil
}

// ListTorrents returns all torrents from qBittorrent.
func (c *Client) ListTorrents() ([]Torrent, error) {
	resp, err := c.doRequest("/api/v2/torrents/info")
	if err != nil {
		return nil, fmt.Errorf("list torrents: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list torrents: unexpected status %d", resp.StatusCode)
	}

	var torrents []Torrent
	if err := json.NewDecoder(resp.Body).Decode(&torrents); err != nil {
		return nil, fmt.Errorf("list torrents: %w", err)
	}
	return torrents, nil
}

// GetTorrentProperties returns properties for a torrent identified by its hash.
func (c *Client) GetTorrentProperties(hash string) (*Properties, error) {
	resp, err := c.doRequest("/api/v2/torrents/properties?hash=" + url.QueryEscape(hash))
	if err != nil {
		return nil, fmt.Errorf("get torrent properties: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrTorrentNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get torrent properties: unexpected status %d", resp.StatusCode)
	}

	var props Properties
	if err := json.NewDecoder(resp.Body).Decode(&props); err != nil {
		return nil, fmt.Errorf("get torrent properties: %w", err)
	}
	return &props, nil
}

// FindTorrentByFolder finds a torrent whose Name matches the basename of folderPath.
// Returns nil if no match is found.
func FindTorrentByFolder(torrents []Torrent, folderPath string) *Torrent {
	folderName := path.Base(strings.TrimRight(folderPath, "/"))
	for i := range torrents {
		if torrents[i].Name == folderName {
			return &torrents[i]
		}
	}
	return nil
}

// DeleteTorrent removes a torrent from qBittorrent. Does NOT delete files on disk.
func (c *Client) DeleteTorrent(hash string) error {
	form := url.Values{}
	form.Set("hashes", hash)
	form.Set("deleteFiles", "false")

	req, err := http.NewRequest(http.MethodPost, c.baseURL+"/api/v2/torrents/delete", strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("delete torrent: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if c.cookie != nil {
		req.AddCookie(c.cookie)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("delete torrent: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode == http.StatusForbidden {
		if loginErr := c.Login(); loginErr != nil {
			return fmt.Errorf("delete torrent: re-login failed: %w", loginErr)
		}
		return c.DeleteTorrent(hash)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("delete torrent: status %d", resp.StatusCode)
	}
	return nil
}

func (c *Client) rawRequest(reqPath string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, c.baseURL+reqPath, http.NoBody)
	if err != nil {
		return nil, err
	}
	if c.cookie != nil {
		req.AddCookie(c.cookie)
	}
	return c.client.Do(req)
}
