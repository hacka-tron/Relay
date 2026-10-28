package updater

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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
)

// fakeServer answers /v1/update with whatever manifest the test sets and
// serves /files/app with whatever bytes the test sets.
type fakeServer struct {
	*httptest.Server
	status int // for /v1/update
	body   []byte
	m      manifest.Manifest
}

func newFakeServer(t *testing.T) *fakeServer {
	t.Helper()
	fs := &fakeServer{status: 200}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/update", func(w http.ResponseWriter, r *http.Request) {
		if fs.status != 200 {
			w.WriteHeader(fs.status)
			return
		}
		json.NewEncoder(w).Encode(fs.m)
	})
	mux.HandleFunc("/files/app", func(w http.ResponseWriter, r *http.Request) {
		w.Write(fs.body)
	})
	fs.Server = httptest.NewServer(mux)
	t.Cleanup(fs.Close)
	return fs
}

// signed makes a manifest for body, signed with priv, pointing at fs.
func (fs *fakeServer) signed(t *testing.T, priv ed25519.PrivateKey, version string, body []byte) manifest.Manifest {
	t.Helper()
	sum := sha256.Sum256(body)
	m := manifest.Manifest{
		Version: version, OS: "linux", Arch: "amd64",
		URL: fs.URL + "/files/app", SHA256: hex.EncodeToString(sum[:]), Size: int64(len(body)),
	}
	if err := manifest.Sign(priv, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func newClient(t *testing.T, serverURL string, pub ed25519.PublicKey) *Client {
	t.Helper()
	return &Client{
		ServerURL: serverURL, Channel: "stable", ClientID: "test", Version: "1.0.0",
		OS: "linux", Arch: "amd64", PublicKey: pub,
		HTTP: &http.Client{Timeout: 5 * time.Second},
		Log:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func setup(t *testing.T) (*fakeServer, *Client, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, _ := manifest.GenerateKey()
	fs := newFakeServer(t)
	fs.body = []byte("new binary bytes")
	fs.m = fs.signed(t, priv, "1.1.0", fs.body)
	return fs, newClient(t, fs.URL, pub), priv
}

func TestHappyPath(t *testing.T) {
	fs, c, _ := setup(t)
	m, ok, err := c.Check(context.Background())
	if err != nil || !ok {
		t.Fatalf("Check: ok=%v err=%v", ok, err)
	}
	dir := t.TempDir()
	path, err := c.Download(context.Background(), m, dir)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(fs.body) {
		t.Fatalf("downloaded %q", got)
	}
	if filepath.Dir(path) != dir {
		t.Fatalf("download landed in %s, want %s", filepath.Dir(path), dir)
	}
}

func TestNoContentMeansNoUpdate(t *testing.T) {
	fs, c, _ := setup(t)
	fs.status = 204
	_, ok, err := c.Check(context.Background())
	if err != nil || ok {
		t.Fatalf("ok=%v err=%v, want false,nil", ok, err)
	}
}

func TestServerProblemsAreErrorsNotPanics(t *testing.T) {
	fs, c, _ := setup(t)
	fs.status = 500
	if _, _, err := c.Check(context.Background()); err == nil {
		t.Fatal("500 was not an error")
	}
	fs.Close()
	if _, _, err := c.Check(context.Background()); err == nil {
		t.Fatal("unreachable server was not an error")
	}
}

func TestBadSignatureRejected(t *testing.T) {
	fs, c, _ := setup(t)
	_, otherPriv, _ := manifest.GenerateKey()
	fs.m = fs.signed(t, otherPriv, "1.1.0", fs.body)
	_, _, err := c.Check(context.Background())
	if !errors.Is(err, manifest.ErrBadSignature) {
		t.Fatalf("want ErrBadSignature, got %v", err)
	}
}

func TestTamperedManifestFieldsRejected(t *testing.T) {
	fs, c, _ := setup(t)
	good := fs.m
	cases := map[string]func(m *manifest.Manifest){
		"version bumped after signing": func(m *manifest.Manifest) { m.Version = "9.9.9" },
		"hash swapped after signing":   func(m *manifest.Manifest) { m.SHA256 = strings.Repeat("0", 64) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			fs.m = good
			mutate(&fs.m)
			if _, _, err := c.Check(context.Background()); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

func TestDowngradeAndSameVersionRejected(t *testing.T) {
	fs, c, priv := setup(t)
	for _, v := range []string{"1.0.0", "0.9.0"} {
		fs.m = fs.signed(t, priv, v, fs.body) // genuinely signed, still refused
		if _, _, err := c.Check(context.Background()); err == nil {
			t.Fatalf("accepted version %s while running 1.0.0", v)
		}
	}
}

func TestWrongPlatformRejected(t *testing.T) {
	fs, c, priv := setup(t)
	m := fs.signed(t, priv, "1.1.0", fs.body)
	m.OS = "windows"
	// Re-sign so only the platform check can fail.
	if err := manifest.Sign(priv, &m); err != nil {
		t.Fatal(err)
	}
	fs.m = m
	if _, _, err := c.Check(context.Background()); err == nil {
		t.Fatal("accepted a windows build on linux")
	}
}

func TestNonHTTPURLRejected(t *testing.T) {
	fs, c, _ := setup(t)
	fs.m.URL = "file:///etc/passwd"
	if _, _, err := c.Check(context.Background()); err == nil {
		t.Fatal("accepted a file:// url")
	}
}

func TestHashMismatchOnDownloadRemovesFile(t *testing.T) {
	fs, c, _ := setup(t)
	m, _, err := c.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	fs.body = []byte("different bytes!") // same length, different content
	dir := t.TempDir()
	if _, err := c.Download(context.Background(), m, dir); err == nil {
		t.Fatal("hash mismatch accepted")
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "*")); len(left) != 0 {
		t.Fatalf("leftover files: %v", left)
	}
}

func TestSizeMismatchRejected(t *testing.T) {
	fs, c, _ := setup(t)
	m, _, _ := c.Check(context.Background())
	for _, body := range []string{"short", "new binary bytes plus extra"} {
		fs.body = []byte(body)
		if _, err := c.Download(context.Background(), m, t.TempDir()); err == nil {
			t.Fatalf("accepted body of %d bytes for manifest size %d", len(body), m.Size)
		}
	}
}

func TestOversizeManifestRefusedBeforeDownload(t *testing.T) {
	fs, c, priv := setup(t)
	c.MaxSize = 8
	fs.m = fs.signed(t, priv, "1.1.0", fs.body) // 16 bytes
	if _, _, err := c.Check(context.Background()); err == nil {
		t.Fatal("accepted manifest over MaxSize")
	}
}

func TestCleanupTemp(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "update-123.tmp"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, "keep.txt"), []byte("x"), 0o644)
	CleanupTemp(dir)
	if _, err := os.Stat(filepath.Join(dir, "update-123.tmp")); err == nil {
		t.Fatal("temp file not removed")
	}
	if _, err := os.Stat(filepath.Join(dir, "keep.txt")); err != nil {
		t.Fatal("unrelated file removed")
	}
}
