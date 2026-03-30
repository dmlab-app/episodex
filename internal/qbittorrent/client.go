// Package qbittorrent provides a client for the qBittorrent Web API.
package qbittorrent

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

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
	cookie   *http.Cookie
	client   *http.Client
}

// NewClient creates a new qBittorrent API client.
func NewClient(baseURL, user, password string) *Client {
	return &Client{
		baseURL:  strings.TrimRight(baseURL, "/"),
		user:     user,
		password: password,
		client:   &http.Client{},
	}
}

// Login authenticates with the qBittorrent API and stores the session cookie.
func (c *Client) Login() error {
	form := url.Values{
		"username": {c.user},
		"password": {c.password},
	}

	resp, err := c.client.PostForm(c.baseURL+"/api/v2/auth/login", form)
	if err != nil {
		return fmt.Errorf("qbittorrent login request failed: %w", err)
	}
	defer resp.Body.Close()

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
func (c *Client) doRequest(method, path string, body io.Reader) (*http.Response, error) {
	resp, err := c.rawRequest(method, path, body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode == http.StatusForbidden {
		resp.Body.Close()
		if err := c.Login(); err != nil {
			return nil, fmt.Errorf("re-login failed: %w", err)
		}
		return c.rawRequest(method, path, body)
	}

	return resp, nil
}

// ListTorrents returns all torrents from qBittorrent.
func (c *Client) ListTorrents() ([]Torrent, error) {
	resp, err := c.doRequest("GET", "/api/v2/torrents/info", nil)
	if err != nil {
		return nil, fmt.Errorf("list torrents: %w", err)
	}
	defer resp.Body.Close()

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
	resp, err := c.doRequest("GET", "/api/v2/torrents/properties?hash="+url.QueryEscape(hash), nil)
	if err != nil {
		return nil, fmt.Errorf("get torrent properties: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("get torrent properties: torrent not found")
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

func (c *Client) rawRequest(method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest(method, c.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	if c.cookie != nil {
		req.AddCookie(c.cookie)
	}
	return c.client.Do(req)
}
