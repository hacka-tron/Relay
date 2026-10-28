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

	"github.com/hacka-tron/relay/internal/manifest"
)

// importRelease downloads the assets of GitHub release v<version> into
// releases/<version>. It uses the GitHub API so that private repositories
// work too, with GITHUB_TOKEN; public ones need no token. release.json is
// fetched first and must parse, then each binary it lists.
func (h *handler) importRelease(ctx context.Context, version string) error {
	if version == "" || strings.ContainsAny(version, `/\`) || strings.HasPrefix(version, ".") {
		return fmt.Errorf("bad version %q", version)
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

	dir := filepath.Join(h.store.dir, version)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := h.githubAsset(ctx, assetURL["release.json"], filepath.Join(dir, "release.json")); err != nil {
		return err
	}
	var r manifest.Release
	if err := readJSON(filepath.Join(dir, "release.json"), &r); err != nil {
		return err
	}
	if r.Version != version {
		return fmt.Errorf("release.json is for %s, not %s", r.Version, version)
	}
	for _, m := range r.Targets {
		u := assetURL[m.URL]
		if u == "" {
			return fmt.Errorf("release v%s is missing asset %s", version, m.URL)
		}
		if err := h.githubAsset(ctx, u, filepath.Join(dir, m.URL)); err != nil {
			return err
		}
	}
	return nil
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

// githubAsset streams one asset to path, writing to a temp file first so a
// half downloaded binary never sits under its final name.
func (h *handler) githubAsset(ctx context.Context, url, path string) error {
	resp, err := h.githubRequest(ctx, url, "application/octet-stream")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	tmp, err := os.CreateTemp(filepath.Dir(path), ".import-*")
	if err != nil {
		return err
	}
	_, err = io.Copy(tmp, io.LimitReader(resp.Body, 500<<20))
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		os.Remove(tmp.Name())
	}
	return err
}
