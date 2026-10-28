package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/hacka-tron/relay/internal/manifest"
	"github.com/hacka-tron/relay/internal/rollout"
)

// store reads release data from the releases directory on every call. The
// files are tiny and re-reading means an operator can edit rollout.json
// and have the change take effect on the next request with no restart.
//
// Layout:
//
//	releases/rollout.json
//	releases/1.0.0/release.json
//	releases/1.0.0/app-linux-amd64   (and the other platform binaries)
type store struct {
	dir string
}

func (s store) rollout() (rollout.Config, error) {
	var cfg rollout.Config
	err := readJSON(filepath.Join(s.dir, "rollout.json"), &cfg)
	return cfg, err
}

func (s store) release(version string) (manifest.Release, error) {
	var r manifest.Release
	err := readJSON(filepath.Join(s.dir, version, "release.json"), &r)
	return r, err
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}
