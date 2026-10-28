package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/hacka-tron/relay/internal/atomicfile"
)

// The window lets the user pick which update server to use and how often
// to check. Choices are saved in settings.json in the state directory, so
// they survive restarts and updates, and take effect on the next check
// without a restart.

// localServer is an update server running on this computer, used for
// development and demos.
const localServer = "http://localhost:8080"

// intervalChoices are the check intervals the window offers, in seconds.
var intervalChoices = []int{10, 30, 60, 300, 900, 3600}

// serverOption is one button in the window's server switch.
type serverOption struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	URL   string `json:"url"`
}

type settings struct {
	Server          string `json:"server"` // the ID of a serverOption
	IntervalSeconds int    `json:"interval_seconds"`
}

// settingsStore holds the current settings. The check loop reads it before
// every check, the window changes it, and changed wakes the check loop so
// a new choice is used right away.
type settingsStore struct {
	mu      sync.Mutex
	cur     settings
	options []serverOption
	path    string
	changed chan struct{}
}

// serverOptions lists the servers a build can switch between. Release
// builds have the cloud address compiled in (main.cloudServer); a local
// build without it offers only this computer.
func serverOptions() []serverOption {
	var opts []serverOption
	if cloudServer != "" {
		opts = append(opts, serverOption{ID: "cloud", Label: "Cloud", URL: cloudServer})
	}
	return append(opts, serverOption{ID: "local", Label: "This computer", URL: localServer})
}

// newSettingsStore loads saved settings from dir, falling back to the
// first server option and a one minute interval.
func newSettingsStore(dir string, options []serverOption) *settingsStore {
	s := &settingsStore{
		cur:     settings{Server: options[0].ID, IntervalSeconds: 60},
		options: options,
		path:    filepath.Join(dir, "settings.json"),
		changed: make(chan struct{}, 1),
	}
	var saved settings
	if b, err := os.ReadFile(s.path); err == nil && json.Unmarshal(b, &saved) == nil && s.check(saved) == nil {
		s.cur = saved
	}
	return s
}

func (s *settingsStore) get() settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cur
}

// serverURL is the address of the chosen server.
func (s *settingsStore) serverURL() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, o := range s.options {
		if o.ID == s.cur.Server {
			return o.URL
		}
	}
	return s.options[0].URL
}

// check accepts only the offered servers and intervals. The window is the
// only writer, but it is reachable from the local page, so nothing it
// sends is trusted.
func (s *settingsStore) check(v settings) error {
	found := false
	for _, o := range s.options {
		found = found || o.ID == v.Server
	}
	if !found {
		return fmt.Errorf("unknown server %q", v.Server)
	}
	for _, n := range intervalChoices {
		if n == v.IntervalSeconds {
			return nil
		}
	}
	return errors.New("interval must be one of the offered choices")
}

// set validates, saves and applies new settings, then wakes the check loop.
func (s *settingsStore) set(v settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check(v); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(v, "", "  ")
	if err := atomicfile.Write(s.path, b, 0o644); err != nil {
		return err
	}
	s.cur = v
	select {
	case s.changed <- struct{}{}:
	default: // a wake-up is already pending
	}
	return nil
}

// override applies command line flags for this run only: they win over
// the saved settings but are not saved. A --server address that is not one
// of the options is added as a "Custom" choice.
func (s *settingsStore) override(serverURL string, intervalSeconds int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if serverURL != "" {
		id := ""
		for _, o := range s.options {
			if o.URL == serverURL {
				id = o.ID
			}
		}
		if id == "" {
			id = "custom"
			s.options = append(s.options, serverOption{ID: id, Label: "Custom", URL: serverURL})
		}
		s.cur.Server = id
	}
	if intervalSeconds > 0 {
		s.cur.IntervalSeconds = intervalSeconds
	}
}
