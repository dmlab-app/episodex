// Package qbittorrent provides a client for the qBittorrent Web API.
package qbittorrent

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

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
