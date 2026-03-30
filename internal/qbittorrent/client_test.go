package qbittorrent

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestNewClient(t *testing.T) {
	c := NewClient("http://localhost:8080", "admin", "secret")
	if c.baseURL != "http://localhost:8080" {
		t.Errorf("expected baseURL http://localhost:8080, got %s", c.baseURL)
	}
	if c.user != "admin" {
		t.Errorf("expected user admin, got %s", c.user)
	}
	if c.password != "secret" {
		t.Errorf("expected password secret, got %s", c.password)
	}
}

func TestLogin_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/auth/login" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.FormValue("username") != "admin" || r.FormValue("password") != "secret" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "SID", Value: "test-session-id"})
		w.Write([]byte("Ok."))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "admin", "secret")
	err := c.Login()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if c.cookie == nil || c.cookie.Value != "test-session-id" {
		t.Error("expected SID cookie to be set")
	}
}

func TestLogin_Failure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte("Fails."))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "admin", "wrong")
	err := c.Login()
	if err == nil {
		t.Fatal("expected error on failed login")
	}
}

func TestAutoRelogin_On403(t *testing.T) {
	var reqCount atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/auth/login" {
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "new-session"})
			w.Write([]byte("Ok."))
			return
		}

		count := reqCount.Add(1)
		// First request to /api/v2/torrents/info returns 403 (expired session)
		if count == 1 {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		// After re-login, second request succeeds
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "admin", "secret")
	// Set an expired cookie to simulate expired session
	c.cookie = &http.Cookie{Name: "SID", Value: "expired-session"}

	resp, err := c.doRequest("GET", "/api/v2/torrents/info", nil)
	if err != nil {
		t.Fatalf("expected successful request after relogin, got %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 after relogin, got %d", resp.StatusCode)
	}
	if c.cookie.Value != "new-session" {
		t.Error("expected cookie to be updated after relogin")
	}
}

func TestAutoRelogin_FailsIfReloginFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// All requests return 403 — login also fails
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "admin", "secret")
	c.cookie = &http.Cookie{Name: "SID", Value: "expired"}

	_, err := c.doRequest("GET", "/api/v2/torrents/info", nil)
	if err == nil {
		t.Fatal("expected error when relogin fails")
	}
}
