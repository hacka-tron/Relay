package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/mod/semver"

	"github.com/hacka-tron/relay/internal/manifest"
)

// maxTargets bounds how many binaries one release.json may list.
const maxTargets = 20

// importRelease downloads the assets of GitHub release v<version> into
// releases/<version>. It uses the GitHub API so that private repositories
// work too, with GITHUB_TOKEN; public ones need no token. Everything is
// downloaded into a temporary directory and renamed into place only when
// complete, so a half imported release can never be promoted. The
// release.json comes from outside, so every name in it is checked before
// it is used as a file name.
func (h *handler) importRelease(ctx context.Context, version string) error {
	if err := checkVersion(version); err != nil {
		return err
	}
	if h.store.hasRelease(version) {
		return fmt.Errorf("release %s is already on disk", version)
	}
	api := "https://api.github.com/repos/" + h.githubRepo + "/releases/tags/v" + version
	var rel struct {
		Assets []struct {
			Name string `json:"name"`
			URL  string `json:"url"`
		} `json:"assets"`
	}
	if err := h.githubJSON(ctx, api, &rel); err != nil {
		return fmt.Errorf("release v%s on GitHub: %w", version, err)
	}
	assetURL := map[string]string{}
	for _, a := range rel.Assets {
		assetURL[a.Name] = a.URL
	}
	if assetURL["release.json"] == "" {
		return fmt.Errorf("release v%s has no release.json asset", version)
	}

	tmp, err := os.MkdirTemp(h.store.dir, ".import-"+version+"-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp) // a no-op once the rename below has happened

	if err := h.githubAsset(ctx, assetURL["release.json"], filepath.Join(tmp, "release.json"), 1<<20); err != nil {
		return err
	}
	var r manifest.Release
	if err := readJSON(filepath.Join(tmp, "release.json"), &r); err != nil {
		return err
	}
	if r.Version != version {
		return fmt.Errorf("release.json is for %s, not %s", r.Version, version)
	}
	if len(r.Targets) == 0 || len(r.Targets) > maxTargets {
		return fmt.Errorf("release.json lists %d targets, want 1 to %d", len(r.Targets), maxTargets)
	}
	for _, m := range r.Targets {
		if !safeSegment(m.URL) || strings.HasSuffix(strings.ToLower(m.URL), ".json") {
			return fmt.Errorf("release.json has an unsafe file name %q", m.URL)
		}
		u := assetURL[m.URL]
		if u == "" {
			return fmt.Errorf("release v%s is missing asset %s", version, m.URL)
		}
		if err := h.githubAsset(ctx, u, filepath.Join(tmp, m.URL), m.Size); err != nil {
			return err
		}
	}
	return os.Rename(tmp, filepath.Join(h.store.dir, version))
}

func (h *handler) githubRequest(ctx context.Context, url, accept string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	if h.githubToken != "" {
		req.Header.Set("Authorization", "Bearer "+h.githubToken)
	}
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("GitHub returned %s for %s", resp.Status, url)
	}
	return resp, nil
}

func (h *handler) githubJSON(ctx context.Context, url string, v any) error {
	resp, err := h.githubRequest(ctx, url, "application/vnd.github+json")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(v)
}

// githubAsset streams one asset to path. It reads at most size+1 bytes and
// fails if the body is not exactly size bytes, so a truncated or oversized
// asset is an error, never a silently wrong file. release.json has no
// declared size, so its caller passes a generous limit instead.
func (h *handler) githubAsset(ctx context.Context, url, path string, size int64) error {
	if size <= 0 || size > 500<<20 {
		return fmt.Errorf("refusing asset of %d bytes", size)
	}
	resp, err := h.githubRequest(ctx, url, "application/octet-stream")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	n, err := io.Copy(f, io.LimitReader(resp.Body, size+1))
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil && n > size {
		err = fmt.Errorf("%s is larger than %d bytes", filepath.Base(path), size)
	}
	if err == nil && n < size && filepath.Base(path) != "release.json" {
		err = fmt.Errorf("%s is %d bytes, release.json says %d", filepath.Base(path), n, size)
	}
	return err
}

// checkVersion accepts a plain MAJOR.MINOR.PATCH version that is safe to
// use as a directory name and in a URL.
func checkVersion(v string) error {
	if !safeSegment(v) || !semver.IsValid("v"+v) || semver.Canonical("v"+v) != "v"+v {
		return fmt.Errorf("version %q must look like 1.2.3", v)
	}
	return nil
}
