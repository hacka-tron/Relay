package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/hacka-tron/relay/internal/rollout"
)

// promote points a channel at a version. It is the deliberate step that
// makes a built release visible to clients, and the same command pauses
// or widens a rollout. The server re-reads rollout.json on every request,
// so the change is live immediately.
func promote(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("promote", flag.ContinueOnError)
	fs.SetOutput(out)
	version := fs.String("version", "", "version to offer (required)")
	channel := fs.String("channel", "stable", "channel to change")
	percent := fs.Int("percent", 100, "share of clients to offer it to, 0 to 100")
	paused := fs.Bool("paused", false, "pause the channel so nobody is offered anything")
	dir := fs.String("releases", "releases", "releases directory holding rollout.json")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *version == "" {
		return errors.New("--version is required")
	}
	if _, err := os.Stat(filepath.Join(*dir, *version, "release.json")); err != nil {
		return fmt.Errorf("no built release for %s in %s; run release build first", *version, *dir)
	}

	path := filepath.Join(*dir, "rollout.json")
	cfg := rollout.Config{Channels: map[string]rollout.Channel{}}
	if b, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(b, &cfg); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	}
	cfg.Channels[*channel] = rollout.Channel{Version: *version, RolloutPercent: *percent, Paused: *paused}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(out, "%s: %s -> %s at %d%% (paused=%v)\n", path, *channel, *version, *percent, *paused)
	return nil
}
