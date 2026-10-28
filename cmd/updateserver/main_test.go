package main

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hacka-tron/relay/internal/manifest"
	"github.com/hacka-tron/relay/internal/rollout"
)

// newTestServer builds a releases dir with one rollout.json and one
// release (1.1.0 for linux/amd64) and serves it.
func newTestServer(t *testing.T, rolloutJSON string) (*httptest.Server, string) {
	t.Helper()
	dir := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(dir, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("rollout.json", rolloutJSON)
	rel := manifest.Release{Version: "1.1.0", Targets: []manifest.Manifest{
		{Version: "1.1.0", OS: "linux", Arch: "amd64", URL: "app-linux-amd64", SHA256: "ab", Size: 5, Signature: "sig"},
		{Version: "1.1.0", OS: "windows", Arch: "amd64", URL: "https://cdn.example.com/app.exe", SHA256: "cd", Size: 5, Signature: "sig"},
	}}
	b, _ := json.Marshal(rel)
	write("1.1.0/release.json", string(b))
	write("1.1.0/app-linux-amd64", "bytes")

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := newHandler(store{dir: dir}, "http://updates.test/", logger)
	h.adminToken = "secret-token-16ch"
	srv := httptest.NewServer(h.routes())
	t.Cleanup(srv.Close)
	return srv, dir
}

const stable110 = `{"channels":{"stable":{"version":"1.1.0","rollout_percent":100}}}`

func get(t *testing.T, url string) (*http.Response, []byte) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, body
}

func TestUpdateOffersManifestWithAbsoluteURL(t *testing.T) {
	srv, _ := newTestServer(t, stable110)
	resp, body := get(t, srv.URL+"/v1/update?client_id=c1&version=1.0.0&os=linux&arch=amd64&channel=stable")
	if resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	var m manifest.Manifest
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}
	if m.Version != "1.1.0" || m.Signature != "sig" || m.SHA256 != "ab" {
		t.Fatalf("manifest fields not passed through: %+v", m)
	}
	if m.URL != "http://updates.test/files/1.1.0/app-linux-amd64" {
		t.Fatalf("url = %q", m.URL)
	}
	// The download link it handed out must actually work.
	resp, body = get(t, srv.URL+"/files/1.1.0/app-linux-amd64")
	if resp.StatusCode != 200 || string(body) != "bytes" {
		t.Fatalf("file download: %d %q", resp.StatusCode, body)
	}
}

func TestUpdateKeepsAbsoluteURLFromRelease(t *testing.T) {
	srv, _ := newTestServer(t, stable110)
	_, body := get(t, srv.URL+"/v1/update?client_id=c1&version=1.0.0&os=windows&arch=amd64")
	var m manifest.Manifest
	json.Unmarshal(body, &m)
	if m.URL != "https://cdn.example.com/app.exe" {
		t.Fatalf("url = %q", m.URL)
	}
}

func TestUpdateNoContentCases(t *testing.T) {
	srv, _ := newTestServer(t, stable110)
	cases := map[string]string{
		"already current":    "client_id=c1&version=1.1.0&os=linux&arch=amd64",
		"newer than offered": "client_id=c1&version=2.0.0&os=linux&arch=amd64",
		"no build for arch":  "client_id=c1&version=1.0.0&os=linux&arch=mips",
		"unknown channel":    "client_id=c1&version=1.0.0&os=linux&arch=amd64&channel=nightly",
	}
	for name, query := range cases {
		t.Run(name, func(t *testing.T) {
			resp, body := get(t, srv.URL+"/v1/update?"+query)
			if resp.StatusCode != 204 {
				t.Fatalf("status %d: %s", resp.StatusCode, body)
			}
		})
	}
}

func TestUpdateDefaultsToStableChannel(t *testing.T) {
	srv, _ := newTestServer(t, stable110)
	resp, _ := get(t, srv.URL+"/v1/update?client_id=c1&version=1.0.0&os=linux&arch=amd64")
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

func TestUpdateRejectsMissingParams(t *testing.T) {
	srv, _ := newTestServer(t, stable110)
	resp, _ := get(t, srv.URL+"/v1/update?version=1.0.0&os=linux&arch=amd64")
	if resp.StatusCode != 400 {
		t.Fatalf("status %d, want 400", resp.StatusCode)
	}
}

func TestPausedIsReadWithoutRestart(t *testing.T) {
	srv, dir := newTestServer(t, stable110)
	resp, _ := get(t, srv.URL+"/v1/update?client_id=c1&version=1.0.0&os=linux&arch=amd64")
	if resp.StatusCode != 200 {
		t.Fatalf("before pause: %d", resp.StatusCode)
	}
	paused := `{"channels":{"stable":{"version":"1.1.0","rollout_percent":100,"paused":true}}}`
	os.WriteFile(filepath.Join(dir, "rollout.json"), []byte(paused), 0o644)
	resp, _ = get(t, srv.URL+"/v1/update?client_id=c1&version=1.0.0&os=linux&arch=amd64")
	if resp.StatusCode != 204 {
		t.Fatalf("after pause: %d, want 204", resp.StatusCode)
	}
}

func TestConfigErrorsAre500(t *testing.T) {
	srv, dir := newTestServer(t, `{"channels":{"stable":{"version":"9.9.9","rollout_percent":100}}}`)
	resp, _ := get(t, srv.URL+"/v1/update?client_id=c1&version=1.0.0&os=linux&arch=amd64")
	if resp.StatusCode != 500 {
		t.Fatalf("missing release.json: %d, want 500", resp.StatusCode)
	}
	os.WriteFile(filepath.Join(dir, "rollout.json"), []byte("{not json"), 0o644)
	resp, _ = get(t, srv.URL+"/v1/update?client_id=c1&version=1.0.0&os=linux&arch=amd64")
	if resp.StatusCode != 500 {
		t.Fatalf("bad rollout.json: %d, want 500", resp.StatusCode)
	}
}

func TestFilesServesOnlyVersionedBinaries(t *testing.T) {
	srv, dir := newTestServer(t, stable110)
	os.WriteFile(filepath.Join(dir, "1.1.0", ".hidden"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, "signing.key"), []byte("x"), 0o644)
	for _, p := range []string{
		"/files/", "/files/1.1.0/", "/files/1.1.0", // listings and bare dirs
		"/files/rollout.json", "/files/1.1.0/release.json", "/files/1.1.0/RELEASE.JSON", // config files
		"/files/1.1.0/.hidden", "/files/./signing.key", "/files/../signing.key", // hidden and traversal
		"/files/..%2f/signing.key", "/files/%2e%2e/signing.key",
	} {
		resp, _ := get(t, srv.URL+p)
		if resp.StatusCode == 200 {
			t.Errorf("%s was served", p)
		}
	}
}

func TestUpdateRejectsMismatchedReleaseFile(t *testing.T) {
	srv, dir := newTestServer(t, stable110)
	// Simulate a 1.0.0 release.json copied into the 1.1.0 folder.
	rel := manifest.Release{Version: "1.0.0", Targets: []manifest.Manifest{
		{Version: "1.0.0", OS: "linux", Arch: "amd64", URL: "app", SHA256: "ab", Size: 1, Signature: "s"},
	}}
	b, _ := json.Marshal(rel)
	os.WriteFile(filepath.Join(dir, "1.1.0", "release.json"), b, 0o644)
	resp, _ := get(t, srv.URL+"/v1/update?client_id=c1&version=1.0.0&os=linux&arch=amd64")
	if resp.StatusCode != 500 {
		t.Fatalf("status %d, want 500", resp.StatusCode)
	}
}

func TestUpdateRejectsOverlongClientID(t *testing.T) {
	srv, _ := newTestServer(t, stable110)
	resp, _ := get(t, srv.URL+"/v1/update?client_id="+strings.Repeat("a", 129)+"&version=1.0.0&os=linux&arch=amd64")
	if resp.StatusCode != 400 {
		t.Fatalf("status %d, want 400", resp.StatusCode)
	}
}

func TestHealthz(t *testing.T) {
	srv, _ := newTestServer(t, stable110)
	resp, body := get(t, srv.URL+"/healthz")
	if resp.StatusCode != 200 || string(body) != "ok\n" {
		t.Fatalf("%d %q", resp.StatusCode, body)
	}
}

func facts(id string) rollout.Facts {
	return rollout.Facts{ClientID: id, Version: "1.0.0", OS: "linux", Arch: "amd64", Channel: "stable"}
}

func TestDownloadRateSlowsFiles(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "rollout.json"), []byte(stable110), 0o644)
	os.MkdirAll(filepath.Join(dir, "1.1.0"), 0o755)
	body := strings.Repeat("x", 128<<10) // the file server writes 32 KB at a time
	os.WriteFile(filepath.Join(dir, "1.1.0", "app"), []byte(body), 0o644)
	h := newHandler(store{dir: dir}, "http://x", slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.downloadRate = 256 << 10 // 128 KB at 256 KB/s, so the last piece is held back about 0.4 s
	srv := httptest.NewServer(h.routes())
	defer srv.Close()

	began := time.Now()
	resp, got := get(t, srv.URL+"/files/1.1.0/app")
	if resp.StatusCode != 200 || string(got) != body {
		t.Fatalf("status %d, %d bytes", resp.StatusCode, len(got))
	}
	if took := time.Since(began); took < 300*time.Millisecond {
		t.Fatalf("download took %v, want it slowed to about 400ms", took)
	}
}
