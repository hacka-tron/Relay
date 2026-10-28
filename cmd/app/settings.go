package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/hacka-tron/relay/internal/atomicfile"
)

// Where updates come from is fixed when the app is built. Release builds
// have the cloud server compiled in (main.cloudServer); a build without
// one, such as the demo, uses a server on this computer. --server
// overrides it for one run, for testing.
//
// The one thing the window lets the user change is how often Relay
// checks. That is saved in settings.json in the state directory, so it
// survives restarts and updates, and applies on the next check.

// localServer is an update server running on this computer, used by local
// builds and the demo.
const localServer = "http://localhost:8080"

// updateServer is the server this build gets updates from.
func updateServer() string {
	if cloudServer != "" {
		return cloudServer
	}
	return localServer
}

// intervalChoices are the check intervals the window offers, in seconds.
var intervalChoices = []int{10, 30, 60, 300, 900, 3600}

type settings struct {
	IntervalSeconds int `json:"interval_seconds"`
}

// settingsStore holds the settings. saved is what settings.json holds and
// the window edits. cur is what the app uses: saved, with any --interval
// override for this run on top. The check loop reads cur before every
// wait, and changed wakes it so a new choice is used right away.
type settingsStore struct {
	mu      sync.Mutex
	saved   settings
	cur     settings
	path    string
	changed chan struct{}
}

// newSettingsStore loads saved settings from dir. Anything missing or not
// one of the offered choices falls back to one minute. Older versions also
// saved a server choice; it is ignored, so every release build now uses
// its compiled-in server.
func newSettingsStore(dir string) *settingsStore {
	s := &settingsStore{
		saved:   settings{IntervalSeconds: 60},
		path:    filepath.Join(dir, "settings.json"),
		changed: make(chan struct{}, 1),
	}
	var file settings
	if b, err := os.ReadFile(s.path); err == nil && json.Unmarshal(b, &file) == nil && knownInterval(file.IntervalSeconds) {
		s.saved = file
	}
	s.cur = s.saved
	return s
}

func (s *settingsStore) get() settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cur
}

func knownInterval(n int) bool {
	for _, c := range intervalChoices {
		if c == n {
			return true
		}
	}
	return false
}

// settingsChange is what the window sends.
type settingsChange struct {
	IntervalSeconds *int `json:"interval_seconds"`
}

// set applies a change from the window, saves it, and wakes the check
// loop. The page is reachable from the local browser, so nothing it sends
// is trusted: only the offered intervals are accepted. A change replaces
// any --interval override for the rest of the run.
func (s *settingsStore) set(c settingsChange) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c.IntervalSeconds == nil {
		return nil
	}
	if !knownInterval(*c.IntervalSeconds) {
		return errors.New("interval must be one of the offered choices")
	}
	next := settings{IntervalSeconds: *c.IntervalSeconds}
	b, _ := json.MarshalIndent(next, "", "  ")
	if err := atomicfile.Write(s.path, b, 0o644); err != nil {
		return err
	}
	s.saved, s.cur = next, next
	select {
	case s.changed <- struct{}{}:
	default: // a wake-up is already pending
	}
	return nil
}

// override applies --interval for this run only; it is never saved.
func (s *settingsStore) override(intervalSeconds int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if intervalSeconds > 0 {
		s.cur.IntervalSeconds = intervalSeconds
	}
}
