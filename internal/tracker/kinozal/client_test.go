package kinozal

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"golang.org/x/text/encoding/charmap"
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
		{"https://evil.com/kinozal.tv/phish", false},
		{"https://not-kinozal.tv.example.com/page", false},
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
	if len(c.cookies) == 0 {
		t.Fatal("expected cookies to be set after login")
	}
	found := false
	for _, cookie := range c.cookies {
		if cookie.Name == "uid" && cookie.Value == "12345" {
			found = true
		}
	}
	if !found {
		t.Error("expected uid cookie with value 12345")
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

	// CheckRedirect is now set by default in NewClient/NewClientWithBaseURL
	c := NewClientWithBaseURL(server.URL, "user", "pass")

	if err := c.Login(); err != nil {
		t.Fatalf("Login() returned error: %v", err)
	}
	found := false
	for _, cookie := range c.cookies {
		if cookie.Name == "uid" && cookie.Value == "99999" {
			found = true
		}
	}
	if !found {
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
			want:  1,
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
				// Serve as Windows-1251 to match real Kinozal behavior
				page := fmt.Sprintf(`<html><head><title>%s</title></head><body></body></html>`, tt.title)
				encoded, _ := charmap.Windows1251.NewEncoder().Bytes([]byte(page))
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(encoded)
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
		// Serve as Windows-1251 to match real Kinozal behavior
		page := `<html><head><title>Сериал (1 сезон: 1-10 серии из 12)</title></head></html>`
		encoded, _ := charmap.Windows1251.NewEncoder().Bytes([]byte(page))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(encoded)
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
		{"Сериал (1 сезон: 1 серия из 10)", 1},
		{"Сериал (1 сезон: 5 серий из 10)", 5},
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
		{"https://kinozal.tv/details.php?id=abc", "", true},
		{"https://kinozal.tv/details.php?id=123%26evil=inject", "", true},
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

// sampleSearchHTML returns a minimal Kinozal browse page with search results.
func sampleSearchHTML(results []struct{ id, title, size string }) string {
	rows := ""
	for _, r := range results {
		rows += fmt.Sprintf(`<tr class=bg><td class="bt"><img src="/pic/cat/46.gif"></td><td class="nam"><a href="/details.php?id=%s" class="r1">%s</a><td class='s'>10</td>
<td class='s'>%s</td>
<td class='sl_s'>5</td>
<td class='sl_p'>1</td>
<td class='s'>01.01.2026 в 12:00</td>
<td class='sl'><a href='/userdetails.php?id=123'>user</a></td></tr>
`, r.id, r.title, r.size)
	}
	return fmt.Sprintf(`<html><body><table class="t_peer">%s</table></body></html>`, rows)
}

func TestSearch(t *testing.T) {
	results := []struct{ id, title, size string }{
		{"1111111", "Звёздные врата: ЗВ-1 (5 сезон: 1-22 серии из 22) / Stargate SG-1 / WEB-DL (1080p)", "45.3 ГБ"},
		{"2222222", "Звёздные врата: ЗВ-1 (4 сезон: 1-22 серии из 22) / Stargate SG-1 / DVDRip", "12.1 ГБ"},
	}
	page := sampleSearchHTML(results)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/takelogin.php" {
			http.SetCookie(w, &http.Cookie{Name: "uid", Value: "sess"})
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path == "/browse.php" {
			q := r.URL.Query().Get("s")
			if q == "" {
				t.Error("expected search query parameter 's'")
			}
			encoded, _ := charmap.Windows1251.NewEncoder().Bytes([]byte(page))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(encoded)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	c := NewClientWithBaseURL(server.URL, "u", "p")
	_ = c.Login()

	got, err := c.Search("Звёздные врата")
	if err != nil {
		t.Fatalf("Search() error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Search() returned %d results, want 2", len(got))
	}

	if got[0].Title != results[0].title {
		t.Errorf("result[0].Title = %q, want %q", got[0].Title, results[0].title)
	}
	if got[0].Size != "45.3 ГБ" {
		t.Errorf("result[0].Size = %q, want %q", got[0].Size, "45.3 ГБ")
	}
	wantURL0 := server.URL + "/details.php?id=1111111"
	if got[0].DetailsURL != wantURL0 {
		t.Errorf("result[0].DetailsURL = %q, want %q", got[0].DetailsURL, wantURL0)
	}

	if got[1].Title != results[1].title {
		t.Errorf("result[1].Title = %q, want %q", got[1].Title, results[1].title)
	}
	if got[1].Size != "12.1 ГБ" {
		t.Errorf("result[1].Size = %q, want %q", got[1].Size, "12.1 ГБ")
	}
}

func TestSearchParseHTML(t *testing.T) {
	tests := []struct {
		name    string
		html    string
		want    int
		wantErr bool
	}{
		{
			name: "single result",
			html: `<html><body><table class="t_peer">
<tr class=bg><td class="bt"><img src="/pic/cat/46.gif"></td><td class="nam"><a href="/details.php?id=999" class="r1">Some Series (1 сезон) / BDRip</a><td class='s'>5</td>
<td class='s'>8.5 ГБ</td>
<td class='sl_s'>1</td><td class='sl_p'>0</td><td class='s'>01.01.2026</td><td class='sl'><a href='/userdetails.php?id=1'>u</a></td></tr>
</table></body></html>`,
			want: 1,
		},
		{
			name: "multiple results",
			html: `<html><body><table class="t_peer">
<tr class=bg><td class="bt"><img src="/pic/cat/46.gif"></td><td class="nam"><a href="/details.php?id=100" class="r1">Series A</a><td class='s'>5</td>
<td class='s'>10 ГБ</td>
<td class='sl_s'>1</td><td class='sl_p'>0</td><td class='s'>01.01.2026</td><td class='sl'><a href='/userdetails.php?id=1'>u</a></td></tr>
<tr class=bg><td class="bt"><img src="/pic/cat/46.gif"></td><td class="nam"><a href="/details.php?id=200" class="r1">Series B</a><td class='s'>5</td>
<td class='s'>20 ГБ</td>
<td class='sl_s'>1</td><td class='sl_p'>0</td><td class='s'>01.01.2026</td><td class='sl'><a href='/userdetails.php?id=1'>u</a></td></tr>
<tr class=bg><td class="bt"><img src="/pic/cat/46.gif"></td><td class="nam"><a href="/details.php?id=300" class="r1">Series C</a><td class='s'>5</td>
<td class='s'>30 ГБ</td>
<td class='sl_s'>1</td><td class='sl_p'>0</td><td class='s'>01.01.2026</td><td class='sl'><a href='/userdetails.php?id=1'>u</a></td></tr>
</table></body></html>`,
			want: 3,
		},
		{
			name: "size in MB",
			html: `<html><body><table class="t_peer">
<tr class=bg><td class="bt"><img src="/pic/cat/46.gif"></td><td class="nam"><a href="/details.php?id=400" class="r1">Small torrent</a><td class='s'>5</td>
<td class='s'>500 МБ</td>
<td class='sl_s'>1</td><td class='sl_p'>0</td><td class='s'>01.01.2026</td><td class='sl'><a href='/userdetails.php?id=1'>u</a></td></tr>
</table></body></html>`,
			want: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseSearchResults([]byte(tt.html))
			if len(got) != tt.want {
				t.Errorf("parseSearchResults() returned %d results, want %d", len(got), tt.want)
			}
		})
	}
}

func TestSearchEmpty(t *testing.T) {
	emptyPages := []string{
		`<html><body><table class="t_peer"></table></body></html>`,
		`<html><body>Ничего не найдено</body></html>`,
		`<html><body></body></html>`,
	}

	for i, page := range emptyPages {
		t.Run(fmt.Sprintf("empty_%d", i), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/takelogin.php" {
					http.SetCookie(w, &http.Cookie{Name: "uid", Value: "sess"})
					w.WriteHeader(http.StatusOK)
					return
				}
				encoded, _ := charmap.Windows1251.NewEncoder().Bytes([]byte(page))
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(encoded)
			}))
			defer server.Close()

			c := NewClientWithBaseURL(server.URL, "u", "p")
			_ = c.Login()

			got, err := c.Search("nonexistent query")
			if err != nil {
				t.Fatalf("Search() error: %v", err)
			}
			if len(got) != 0 {
				t.Errorf("Search() returned %d results, want 0", len(got))
			}
		})
	}
}

func TestIsMultiSeasonPack(t *testing.T) {
	tests := []struct {
		title string
		want  bool
	}{
		{"Stargate SG-1 S01-S05 Complete BluRay 1080p", true},
		{"Stargate SG-1 S01–S10 Complete", true},
		{"Сериал (1-5 сезон) / DVDRip", true},
		{"Сериал (1–5 сезон) Complete", true},
		{"Show Seasons 1-3 Complete", true},
		{"Show Season 1-5 Pack", true},
		{"Stargate SG-1 S01-05 Complete BluRay 1080p", true},
		{"Show S01–05 Pack", true},
		{"Звёздные врата: ЗВ-1 (5 сезон: 1-22 серии из 22)", false},
		{"Stargate SG-1 S05 Complete BluRay 1080p", false},
		{"Some Series s03e01-e10", false},
		{"Сериал (3сезон) / DVDRip", false},
		{"Show Season 5 - 1080p", false},
		{"Show Season 5-1080p", false},
	}

	for _, tt := range tests {
		name := tt.title
		if len(name) > 30 {
			name = name[:30]
		}
		t.Run(name, func(t *testing.T) {
			got := isMultiSeasonPack(tt.title)
			if got != tt.want {
				t.Errorf("isMultiSeasonPack(%q) = %v, want %v", tt.title, got, tt.want)
			}
		})
	}
}

func TestMatchSeason(t *testing.T) {
	tests := []struct {
		title  string
		season int
		want   bool
	}{
		{"Звёздные врата: ЗВ-1 (5 сезон: 1-22 серии из 22) / Stargate SG-1 / WEB-DL (1080p)", 5, true},
		{"Звёздные врата: ЗВ-1 (5 сезон: 1-22 серии из 22) / Stargate SG-1 / WEB-DL (1080p)", 4, false},
		{"Stargate SG-1 S05 Complete BluRay 1080p", 5, true},
		{"Stargate SG-1 S05 Complete BluRay 1080p", 3, false},
		{"Stargate SG-1 Season 5 Complete BluRay 1080p", 5, true},
		{"Stargate SG-1 Season 5 Complete BluRay 1080p", 6, false},
		{"Stargate SG-1 Season5 1080p", 5, true},
		{"Some Series s03e01-e10", 3, true},
		{"Some Series s03e01-e10", 2, false},
		{"Сериал (3сезон) / DVDRip", 3, true},
		{"No season info here", 1, false},
		// Multi-season packs should be rejected
		{"Stargate SG-1 S01-S05 Complete BluRay 1080p", 5, false},
		{"Stargate SG-1 S01-S05 Complete BluRay 1080p", 1, false},
		{"Сериал (1-5 сезон) Complete", 5, false},
		{"Show Seasons 1-3 Complete", 3, false},
		{"Stargate SG-1 S01-05 Complete BluRay 1080p", 1, false},
		{"Stargate SG-1 S01-05 Complete BluRay 1080p", 5, false},
	}

	for _, tt := range tests {
		name := tt.title
		if len(name) > 20 {
			name = name[:20]
		}
		t.Run(fmt.Sprintf("%s_S%d", name, tt.season), func(t *testing.T) {
			got := matchSeason(tt.title, tt.season)
			if got != tt.want {
				t.Errorf("matchSeason(%q, %d) = %v, want %v", tt.title, tt.season, got, tt.want)
			}
		})
	}
}

func TestParseSizeBytes(t *testing.T) {
	tests := []struct {
		size string
		want float64
	}{
		{"45.3 ГБ", 45.3 * 1024 * 1024 * 1024},
		{"12.1 ГБ", 12.1 * 1024 * 1024 * 1024},
		{"500 МБ", 500 * 1024 * 1024},
		{"1,5 ГБ", 1.5 * 1024 * 1024 * 1024},
		{"10 GB", 10 * 1024 * 1024 * 1024},
		{"200 MB", 200 * 1024 * 1024},
		{"invalid", 0},
		{"", 0},
	}

	for _, tt := range tests {
		t.Run(tt.size, func(t *testing.T) {
			got := parseSizeBytes(tt.size)
			if got != tt.want {
				t.Errorf("parseSizeBytes(%q) = %f, want %f", tt.size, got, tt.want)
			}
		})
	}
}

func TestFindSeasonTorrent(t *testing.T) {
	results := []struct{ id, title, size string }{
		{"1111111", "Звёздные врата: ЗВ-1 (5 сезон: 1-22 серии из 22) / Stargate SG-1 / WEB-DL (1080p)", "45.3 ГБ"},
		{"2222222", "Звёздные врата: ЗВ-1 (5 сезон: 1-22 серии из 22) / Stargate SG-1 / DVDRip", "12.1 ГБ"},
		{"3333333", "Звёздные врата: ЗВ-1 (4 сезон: 1-22 серии из 22) / Stargate SG-1 / DVDRip", "10.5 ГБ"},
	}
	page := sampleSearchHTML(results)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/takelogin.php" {
			http.SetCookie(w, &http.Cookie{Name: "uid", Value: "sess"})
			w.WriteHeader(http.StatusOK)
			return
		}
		encoded, _ := charmap.Windows1251.NewEncoder().Bytes([]byte(page))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(encoded)
	}))
	defer server.Close()

	c := NewClientWithBaseURL(server.URL, "u", "p")
	_ = c.Login()

	// Should return the largest S05 torrent (45.3 ГБ)
	got, err := c.FindSeasonTorrent("Звёздные врата", 5)
	if err != nil {
		t.Fatalf("FindSeasonTorrent() error: %v", err)
	}
	if got == nil {
		t.Fatal("FindSeasonTorrent() returned nil, want result")
	}
	wantURL := server.URL + "/details.php?id=1111111"
	if got.DetailsURL != wantURL {
		t.Errorf("FindSeasonTorrent() returned %q, want largest S05 torrent", got.DetailsURL)
	}
	if got.Size != "45.3 ГБ" {
		t.Errorf("FindSeasonTorrent() size = %q, want %q", got.Size, "45.3 ГБ")
	}

	// Should return the S04 torrent
	got, err = c.FindSeasonTorrent("Звёздные врата", 4)
	if err != nil {
		t.Fatalf("FindSeasonTorrent() S04 error: %v", err)
	}
	if got == nil {
		t.Fatal("FindSeasonTorrent() S04 returned nil, want result")
	}
	wantURL4 := server.URL + "/details.php?id=3333333"
	if got.DetailsURL != wantURL4 {
		t.Errorf("FindSeasonTorrent() S04 returned %q, want S04 torrent", got.DetailsURL)
	}
}

func TestFindSeasonTorrentSkipsMultiSeasonPack(t *testing.T) {
	results := []struct{ id, title, size string }{
		// Multi-season pack is largest but should be skipped
		{"9999999", "Stargate SG-1 S01-S05 Complete BluRay 1080p", "100.0 ГБ"},
		// Single-season torrent is smaller but should be selected
		{"1111111", "Звёздные врата: ЗВ-1 (5 сезон: 1-22 серии из 22) / Stargate SG-1 / WEB-DL (1080p)", "45.3 ГБ"},
	}
	page := sampleSearchHTML(results)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/takelogin.php" {
			http.SetCookie(w, &http.Cookie{Name: "uid", Value: "sess"})
			w.WriteHeader(http.StatusOK)
			return
		}
		encoded, _ := charmap.Windows1251.NewEncoder().Bytes([]byte(page))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(encoded)
	}))
	defer server.Close()

	c := NewClientWithBaseURL(server.URL, "u", "p")
	_ = c.Login()

	got, err := c.FindSeasonTorrent("Stargate", 5)
	if err != nil {
		t.Fatalf("FindSeasonTorrent() error: %v", err)
	}
	if got == nil {
		t.Fatal("FindSeasonTorrent() returned nil, want single-season result")
	}
	wantURL := server.URL + "/details.php?id=1111111"
	if got.DetailsURL != wantURL {
		t.Errorf("FindSeasonTorrent() returned %q, want single-season torrent %q", got.DetailsURL, wantURL)
	}
}

func TestFindSeasonTorrentNoMatch(t *testing.T) {
	results := []struct{ id, title, size string }{
		{"1111111", "Звёздные врата: ЗВ-1 (5 сезон: 1-22 серии из 22) / Stargate SG-1 / WEB-DL (1080p)", "45.3 ГБ"},
	}
	page := sampleSearchHTML(results)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/takelogin.php" {
			http.SetCookie(w, &http.Cookie{Name: "uid", Value: "sess"})
			w.WriteHeader(http.StatusOK)
			return
		}
		encoded, _ := charmap.Windows1251.NewEncoder().Bytes([]byte(page))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(encoded)
	}))
	defer server.Close()

	c := NewClientWithBaseURL(server.URL, "u", "p")
	_ = c.Login()

	got, err := c.FindSeasonTorrent("Звёздные врата", 9)
	if err != nil {
		t.Fatalf("FindSeasonTorrent() error: %v", err)
	}
	if got != nil {
		t.Errorf("FindSeasonTorrent() returned %+v, want nil for non-matching season", got)
	}
}
