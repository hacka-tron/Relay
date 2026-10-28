package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func adminReq(t *testing.T, srv *httptest.Server, method, path, pass string, form url.Values, origin string) *http.Response {
	t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, _ := http.NewRequest(method, srv.URL+path, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if pass != "" {
		req.SetBasicAuth("admin", pass)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestAdminRequiresToken(t *testing.T) {
	srv, _ := newTestServer(t, stable110)
	if resp := adminReq(t, srv, "GET", "/admin", "", nil, ""); resp.StatusCode != 401 {
		t.Fatalf("no auth: %d", resp.StatusCode)
	}
	if resp := adminReq(t, srv, "GET", "/admin", "wrong", nil, ""); resp.StatusCode != 401 {
		t.Fatalf("wrong token: %d", resp.StatusCode)
	}
	resp := adminReq(t, srv, "GET", "/admin", "secret-token-16ch", nil, "")
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(body), "1.1.0") {
		t.Fatalf("right token: %d %s", resp.StatusCode, body[:min(len(body), 200)])
	}
}

func TestAdminDisabledWithoutStrongToken(t *testing.T) {
	for _, token := range []string{"", "short"} {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "rollout.json"), []byte(stable110), 0o644)
		h := newHandler(store{dir: dir}, "http://x", slog.New(slog.NewTextHandler(io.Discard, nil)))
		h.adminToken = token
		srv := httptest.NewServer(h.routes())
		if resp := adminReq(t, srv, "GET", "/admin", token, nil, ""); resp.StatusCode != 404 {
			t.Fatalf("token %q: admin reachable: %d", token, resp.StatusCode)
		}
		srv.Close()
	}
}

func TestAdminSetsFramingAndCacheHeaders(t *testing.T) {
	srv, _ := newTestServer(t, stable110)
	resp := adminReq(t, srv, "GET", "/admin", "secret-token-16ch", nil, "")
	if resp.Header.Get("X-Frame-Options") != "DENY" || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("headers: %v", resp.Header)
	}
}

func TestAdminShowsMissingVersionAsSelected(t *testing.T) {
	srv, _ := newTestServer(t, `{"channels":{"stable":{"version":"7.7.7","rollout_percent":100}}}`)
	resp := adminReq(t, srv, "GET", "/admin", "secret-token-16ch", nil, "")
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `value="7.7.7" selected`) {
		t.Fatal("channel pointing at a missing release does not keep it selected")
	}
}

func TestUpdateRejectsOverlongFacts(t *testing.T) {
	srv, _ := newTestServer(t, stable110)
	long := strings.Repeat("x", 65)
	resp := adminReq(t, srv, "GET", "/v1/update?client_id=c&version="+long+"&os=linux&arch=amd64", "", nil, "")
	if resp.StatusCode != 400 {
		t.Fatalf("status %d, want 400", resp.StatusCode)
	}
}

func TestCheckVersion(t *testing.T) {
	for _, bad := range []string{"", "..", "1.0", "v1.0.0", "1.0.0?x", "1.0.0/..", ".1.0.0", "1.0.0+meta"} {
		if checkVersion(bad) == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	for _, good := range []string{"1.0.0", "1.2.3-rc1", "10.20.30"} {
		if err := checkVersion(good); err != nil {
			t.Errorf("rejected %q: %v", good, err)
		}
	}
}

func TestAdminSetChannelWritesRollout(t *testing.T) {
	srv, dir := newTestServer(t, stable110)
	form := url.Values{"channel": {"beta"}, "version": {"1.1.0"}, "percent": {"25"}, "paused": {"on"}}
	resp := adminReq(t, srv, "POST", "/admin/channel", "secret-token-16ch", form, srv.URL)
	if resp.StatusCode != 303 || !strings.HasPrefix(resp.Header.Get("Location"), "/admin?msg=") {
		t.Fatalf("status %d location %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	cfg, err := store{dir: dir}.rollout()
	if err != nil {
		t.Fatal(err)
	}
	beta := cfg.Channels["beta"]
	if beta.Version != "1.1.0" || beta.RolloutPercent != 25 || !beta.Paused {
		t.Fatalf("beta = %+v", beta)
	}
	if cfg.Channels["stable"].Version != "1.1.0" {
		t.Fatal("stable channel was lost")
	}

	// A paused channel really stops offers.
	resp = adminReq(t, srv, "GET", "/v1/update?client_id=c1&version=1.0.0&os=linux&arch=amd64&channel=beta", "", nil, "")
	if resp.StatusCode != 204 {
		t.Fatalf("paused beta offered: %d", resp.StatusCode)
	}

	resp = adminReq(t, srv, "POST", "/admin/channel/delete", "secret-token-16ch", url.Values{"channel": {"beta"}}, srv.URL)
	if resp.StatusCode != 303 {
		t.Fatalf("delete: %d", resp.StatusCode)
	}
	cfg, _ = store{dir: dir}.rollout()
	if _, ok := cfg.Channels["beta"]; ok {
		t.Fatal("beta not deleted")
	}
}

func TestAdminRejectsBadInputAndCrossSite(t *testing.T) {
	srv, dir := newTestServer(t, stable110)
	before, _ := os.ReadFile(filepath.Join(dir, "rollout.json"))
	cases := []struct {
		name   string
		form   url.Values
		origin string
		want   string // substring expected in the redirect, or "" for a non-redirect
		code   int
	}{
		{"unbuilt version", url.Values{"channel": {"stable"}, "version": {"9.9.9"}, "percent": {"100"}}, srv.URL, "err=", 303},
		{"percent 101", url.Values{"channel": {"stable"}, "version": {"1.1.0"}, "percent": {"101"}}, srv.URL, "err=", 303},
		{"empty channel", url.Values{"channel": {""}, "version": {"1.1.0"}, "percent": {"1"}}, srv.URL, "err=", 303},
		{"no origin", url.Values{"channel": {"stable"}, "version": {"1.1.0"}, "percent": {"1"}}, "", "", 403},
		{"other origin", url.Values{"channel": {"stable"}, "version": {"1.1.0"}, "percent": {"1"}}, "https://evil.example", "", 403},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := adminReq(t, srv, "POST", "/admin/channel", "secret-token-16ch", tc.form, tc.origin)
			if resp.StatusCode != tc.code || !strings.Contains(resp.Header.Get("Location"), tc.want) {
				t.Fatalf("status %d location %q", resp.StatusCode, resp.Header.Get("Location"))
			}
		})
	}
	after, _ := os.ReadFile(filepath.Join(dir, "rollout.json"))
	if string(before) != string(after) {
		t.Fatal("rollout.json changed by a refused request")
	}
}

func TestAdminShowsClientsThatCheckedIn(t *testing.T) {
	srv, _ := newTestServer(t, stable110)
	adminReq(t, srv, "GET", "/v1/update?client_id=visible-client&version=1.0.0&os=linux&arch=amd64", "", nil, "")
	resp := adminReq(t, srv, "GET", "/admin", "secret-token-16ch", nil, "")
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "visible-client") {
		t.Fatal("client check-in not shown on admin page")
	}
}

func TestReleasesIgnoreTempAndOddDirs(t *testing.T) {
	srv, dir := newTestServer(t, stable110)
	for _, d := range []string{".import-1.2.0-abc", "notaversion"} {
		os.MkdirAll(filepath.Join(dir, d), 0o755)
		os.WriteFile(filepath.Join(dir, d, "release.json"), []byte(`{"version":"1.2.0"}`), 0o644)
	}
	resp := adminReq(t, srv, "GET", "/admin", "secret-token-16ch", nil, "")
	body, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(body), "<code>1.2.0</code>") {
		t.Fatal("temp or odd directory listed as a release")
	}
	if !strings.Contains(string(body), "<code>1.1.0</code>") {
		t.Fatal("real release missing")
	}
}

func TestClientLogEvictsOldest(t *testing.T) {
	l := newClientLog(2)
	for _, id := range []string{"a", "b", "c"} {
		l.record(facts(id), "")
	}
	got := l.list()
	if len(got) != 2 || got[0].ClientID != "c" || got[1].ClientID != "b" {
		t.Fatalf("got %+v", got)
	}
}

func TestImportRejectsBadVersion(t *testing.T) {
	srv, _ := newTestServer(t, stable110)
	resp := adminReq(t, srv, "POST", "/admin/import", "secret-token-16ch", url.Values{"version": {"../x"}}, srv.URL)
	if resp.StatusCode != 303 || !strings.Contains(resp.Header.Get("Location"), "err=") {
		t.Fatalf("status %d location %q", resp.StatusCode, resp.Header.Get("Location"))
	}
}
