package kinozal

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestCanHandle(t *testing.T) {
	c := NewClient("user", "pass")

	tests := []struct {
		url  string
		want bool
	}{
		{"https://kinozal.tv/details.php?id=123", true},
		{"http://kinozal.tv/details.php?id=456", true},
		{"https://www.kinozal.tv/details.php?id=789", true},
		{"https://rutracker.org/forum/viewtopic.php?t=123", false},
		{"https://example.com", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			if got := c.CanHandle(tt.url); got != tt.want {
				t.Errorf("CanHandle(%q) = %v, want %v", tt.url, got, tt.want)
			}
		})
	}
}

func TestLoginSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/takelogin.php" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Errorf("failed to parse form: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if r.FormValue("username") != "testuser" || r.FormValue("password") != "testpass" {
			w.WriteHeader(http.StatusForbidden)
			return
		}

		http.SetCookie(w, &http.Cookie{Name: "uid", Value: "12345"})
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	c := NewClientWithBaseURL(server.URL, "testuser", "testpass")
	if err := c.Login(); err != nil {
		t.Fatalf("Login() returned error: %v", err)
	}
	if c.cookie == nil {
		t.Fatal("expected cookie to be set after login")
	}
	if c.cookie.Value != "12345" {
		t.Errorf("cookie value = %q, want %q", c.cookie.Value, "12345")
	}
}

func TestLoginInvalidCredentials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	c := NewClientWithBaseURL(server.URL, "baduser", "badpass")
	err := c.Login()
	if err == nil {
		t.Fatal("expected error for invalid credentials")
	}
}

func TestLoginNoCookie(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	c := NewClientWithBaseURL(server.URL, "user", "pass")
	err := c.Login()
	if err == nil {
		t.Fatal("expected error when no uid cookie returned")
	}
}

func TestLoginRedirect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "uid", Value: "99999"})
		http.Redirect(w, r, "/", http.StatusFound)
	}))
	defer server.Close()

	// Disable redirect following so we can capture the 302 + cookie
	c := NewClientWithBaseURL(server.URL, "user", "pass")
	c.client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}

	if err := c.Login(); err != nil {
		t.Fatalf("Login() returned error: %v", err)
	}
	if c.cookie == nil || c.cookie.Value != "99999" {
		t.Fatal("expected uid cookie from redirect response")
	}
}

func TestAutoReloginOn403(t *testing.T) {
	var requestCount atomic.Int32
	var loginCount atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/takelogin.php" {
			loginCount.Add(1)
			http.SetCookie(w, &http.Cookie{Name: "uid", Value: "fresh-session"})
			w.WriteHeader(http.StatusOK)
			return
		}

		count := requestCount.Add(1)
		if count == 1 {
			// First request: return 403 to trigger re-login
			w.WriteHeader(http.StatusForbidden)
			return
		}
		// Second request after re-login: success
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	c := NewClientWithBaseURL(server.URL, "user", "pass")
	resp, err := c.doRequest(server.URL + "/some-page")
	if err != nil {
		t.Fatalf("doRequest() returned error: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	if loginCount.Load() != 1 {
		t.Errorf("expected 1 login attempt, got %d", loginCount.Load())
	}
}

func TestAutoReloginFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Always return 403 — both for the page and for login
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	c := NewClientWithBaseURL(server.URL, "user", "pass")
	resp, err := c.doRequest(server.URL + "/some-page")
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("expected error when re-login fails")
	}
}

func TestGetEpisodeCount(t *testing.T) {
	tests := []struct {
		name  string
		title string
		want  int
	}{
		{
			name:  "standard format 8 of 8",
			title: "Сериал (1 сезон: 1-8 серии из 8)",
			want:  8,
		},
		{
			name:  "standard format 17 of 18",
			title: "Сериал (2 сезон: 1-17 серии из 18)",
			want:  17,
		},
		{
			name:  "серий form 6 of 10",
			title: "Сериал (1 сезон: 1-6 серий из 10)",
			want:  6,
		},
		{
			name:  "no episode info",
			title: "Какой-то фильм (2024)",
			want:  0,
		},
		{
			name:  "серия singular",
			title: "Сериал (1 сезон: 1 серия из 10)",
			want:  0, // pattern expects N-M format
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/takelogin.php" {
					http.SetCookie(w, &http.Cookie{Name: "uid", Value: "sess"})
					w.WriteHeader(http.StatusOK)
					return
				}
				w.WriteHeader(http.StatusOK)
				fmt.Fprintf(w, `<html><head><title>%s</title></head><body></body></html>`, tt.title)
			}))
			defer server.Close()

			c := NewClientWithBaseURL(server.URL, "u", "p")
			_ = c.Login()

			got, err := c.GetEpisodeCount(server.URL + "/details.php?id=123")
			if err != nil {
				t.Fatalf("GetEpisodeCount() error: %v", err)
			}
			if got != tt.want {
				t.Errorf("GetEpisodeCount() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestGetEpisodeCountReloginOn403(t *testing.T) {
	var requestCount atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/takelogin.php" {
			http.SetCookie(w, &http.Cookie{Name: "uid", Value: "sess"})
			w.WriteHeader(http.StatusOK)
			return
		}
		count := requestCount.Add(1)
		if count == 1 {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `<html><head><title>Сериал (1 сезон: 1-10 серии из 12)</title></head></html>`)
	}))
	defer server.Close()

	c := NewClientWithBaseURL(server.URL, "u", "p")
	got, err := c.GetEpisodeCount(server.URL + "/details.php?id=123")
	if err != nil {
		t.Fatalf("GetEpisodeCount() error: %v", err)
	}
	if got != 10 {
		t.Errorf("GetEpisodeCount() = %d, want 10", got)
	}
}

func TestParseEpisodeCount(t *testing.T) {
	tests := []struct {
		title string
		want  int
	}{
		{"Сериал (1 сезон: 1-8 серии из 8)", 8},
		{"Сериал (2 сезон: 1-17 серии из 18)", 17},
		{"Сериал (1 сезон: 1-6 серий из 10)", 6},
		{"Фильм (2024)", 0},
		{"Сериал / Serial (3 сезон: 1-22 серии из 22) / Season 3", 22},
		{"", 0},
	}

	for _, tt := range tests {
		t.Run(tt.title, func(t *testing.T) {
			got := parseEpisodeCount(tt.title)
			if got != tt.want {
				t.Errorf("parseEpisodeCount(%q) = %d, want %d", tt.title, got, tt.want)
			}
		})
	}
}

func TestParseIDFromURL(t *testing.T) {
	tests := []struct {
		url     string
		want    string
		wantErr bool
	}{
		{"https://kinozal.tv/details.php?id=2107649", "2107649", false},
		{"http://kinozal.tv/details.php?id=123", "123", false},
		{"https://kinozal.tv/details.php?id=999&s=foo", "999", false},
		{"https://kinozal.tv/details.php", "", true},
		{"https://kinozal.tv/details.php?id=", "", true},
		{"not a url", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			got, err := parseIDFromURL(tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseIDFromURL(%q) error = %v, wantErr %v", tt.url, err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("parseIDFromURL(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}

func TestDownloadTorrent(t *testing.T) {
	torrentData := []byte("d8:announce35:http://tracker.example.com/announcee")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/takelogin.php" {
			http.SetCookie(w, &http.Cookie{Name: "uid", Value: "sess"})
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path == "/download.php" {
			if r.URL.Query().Get("id") != "2107649" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/x-bittorrent")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(torrentData)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	c := NewClientWithBaseURL(server.URL, "u", "p")
	_ = c.Login()

	got, err := c.DownloadTorrent(server.URL + "/details.php?id=2107649")
	if err != nil {
		t.Fatalf("DownloadTorrent() error: %v", err)
	}
	if !bytes.Equal(got, torrentData) {
		t.Errorf("DownloadTorrent() returned %q, want %q", got, torrentData)
	}
}

func TestDownloadTorrentReloginOn403(t *testing.T) {
	var downloadCount atomic.Int32
	torrentData := []byte("torrent-bytes-here")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/takelogin.php" {
			http.SetCookie(w, &http.Cookie{Name: "uid", Value: "new-sess"})
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path == "/download.php" {
			count := downloadCount.Add(1)
			if count == 1 {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			w.Header().Set("Content-Type", "application/x-bittorrent")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(torrentData)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	c := NewClientWithBaseURL(server.URL, "u", "p")
	got, err := c.DownloadTorrent(server.URL + "/details.php?id=123")
	if err != nil {
		t.Fatalf("DownloadTorrent() error: %v", err)
	}
	if !bytes.Equal(got, torrentData) {
		t.Errorf("DownloadTorrent() returned %q, want %q", got, torrentData)
	}
}

func TestDownloadTorrentAuthFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	c := NewClientWithBaseURL(server.URL, "u", "p")
	_, err := c.DownloadTorrent(server.URL + "/details.php?id=123")
	if err == nil {
		t.Fatal("expected error when auth fails")
	}
}

func TestDownloadTorrentInvalidURL(t *testing.T) {
	c := NewClient("u", "p")
	_, err := c.DownloadTorrent("https://kinozal.tv/details.php")
	if err == nil {
		t.Fatal("expected error for URL without id parameter")
	}
}

func TestDoRequestSendsCookie(t *testing.T) {
	var receivedCookie string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/takelogin.php" {
			http.SetCookie(w, &http.Cookie{Name: "uid", Value: "my-session"})
			w.WriteHeader(http.StatusOK)
			return
		}
		cookie, err := r.Cookie("uid")
		if err == nil {
			receivedCookie = cookie.Value
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	c := NewClientWithBaseURL(server.URL, "user", "pass")
	if err := c.Login(); err != nil {
		t.Fatalf("Login() failed: %v", err)
	}

	resp, err := c.doRequest(server.URL + "/details.php?id=123")
	if err != nil {
		t.Fatalf("doRequest() failed: %v", err)
	}
	_ = resp.Body.Close()

	if receivedCookie != "my-session" {
		t.Errorf("expected cookie 'my-session', got %q", receivedCookie)
	}
}
