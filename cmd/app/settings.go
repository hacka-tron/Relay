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

// settingsStore holds the settings. saved is what settings.json holds and
// the window edits. cur is what the app uses: saved, with any command line
// overrides for this run on top. The check loop reads cur before every
// check, and changed wakes it so a new choice is used right away.
type settingsStore struct {
	mu      sync.Mutex
	saved   settings
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

// newSettingsStore loads saved settings from dir. Each field is checked on
// its own, so one stale value (say "cloud" in a build without a cloud
// server) falls back to its default without losing the other.
func newSettingsStore(dir string, options []serverOption) *settingsStore {
	s := &settingsStore{
		saved:   settings{Server: options[0].ID, IntervalSeconds: 60},
		options: options,
		path:    filepath.Join(dir, "settings.json"),
		changed: make(chan struct{}, 1),
	}
	var file settings
	if b, err := os.ReadFile(s.path); err == nil && json.Unmarshal(b, &file) == nil {
		if s.knownServer(file.Server) {
			s.saved.Server = file.Server
		}
		if knownInterval(file.IntervalSeconds) {
			s.saved.IntervalSeconds = file.IntervalSeconds
		}
	}
	s.cur = s.saved
	return s
}

func (s *settingsStore) get() settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cur
}

// serverURL is the address of the server in use.
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

// knownServer accepts only the compiled-in choices, never "custom": a
// --server address is for one run and is never saved.
func (s *settingsStore) knownServer(id string) bool {
	for _, o := range serverOptions() {
		if o.ID == id && id != "custom" {
			return true
		}
	}
	for _, o := range s.options {
		if o.ID == id && id != "custom" {
			return true
		}
	}
	return false
}

func knownInterval(n int) bool {
	for _, c := range intervalChoices {
		if c == n {
			return true
		}
	}
	return false
}

// settingsChange is what the window sends: only the field the user changed.
type settingsChange struct {
	Server          *string `json:"server"`
	IntervalSeconds *int    `json:"interval_seconds"`
}

// set applies a change from the window, saves it, and wakes the check
// loop. The window is reachable from the local page, so nothing it sends
// is trusted: only offered servers and intervals are accepted. A changed
// field replaces any command line override of that field.
func (s *settingsStore) set(c settingsChange) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.saved
	if c.Server != nil {
		if !s.knownServer(*c.Server) {
			return fmt.Errorf("unknown server %q", *c.Server)
		}
		next.Server = *c.Server
	}
	if c.IntervalSeconds != nil {
		if !knownInterval(*c.IntervalSeconds) {
			return errors.New("interval must be one of the offered choices")
		}
		next.IntervalSeconds = *c.IntervalSeconds
	}
	b, _ := json.MarshalIndent(next, "", "  ")
	if err := atomicfile.Write(s.path, b, 0o644); err != nil {
		return err
	}
	s.saved = next
	if c.Server != nil {
		s.cur.Server = next.Server
	}
	if c.IntervalSeconds != nil {
		s.cur.IntervalSeconds = next.IntervalSeconds
	}
	select {
	case s.changed <- struct{}{}:
	default: // a wake-up is already pending
	}
	return nil
}

// override applies command line flags for this run only: they change cur,
// never saved. A --server address that is not one of the options is added
// as a "Custom" choice.
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
