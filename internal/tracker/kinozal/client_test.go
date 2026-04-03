package kinozal

import (
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
	_, err := c.doRequest(server.URL + "/some-page")
	if err == nil {
		t.Fatal("expected error when re-login fails")
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
