// Package install replaces the running executable with a downloaded one
// and gets the old one back if the new one does not work.
//
// Files it manages, all next to the executable so renames stay on one
// filesystem:
//
//	app            the running program
//	app.old        the previous version, kept until the new one proves healthy
//	app.bad        a version that was rolled back, removed on the next clean start
//	update-state.json   pending update marker and the list of versions to skip
//	update.lock    held while an update is in progress
package install

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/hacka-tron/relay/internal/atomicfile"
)

// MaxStartAttempts is how many times a freshly installed version may start
// before it is judged broken. A version is confirmed when the program calls
// Confirm after running healthily for a while; until then every start
// counts. On the attempt after this many, Startup rolls back.
const MaxStartAttempts = 2

// State is the content of update-state.json.
type State struct {
	// Pending is set from the moment a new binary is swapped in until it is
	// confirmed healthy or rolled back.
	Pending *Pending `json:"pending,omitempty"`
	// Skip lists versions that crash looped and must never be installed
	// again on this machine.
	Skip []string `json:"skip,omitempty"`
}

// Pending records a swap that has not yet been confirmed.
type Pending struct {
	Version  string `json:"version"`
	Attempts int    `json:"attempts"` // starts of the new binary so far
}

// Skips reports whether version was rolled back before.
func (s State) Skips(version string) bool {
	for _, v := range s.Skip {
		if v == version {
			return true
		}
	}
	return false
}

func statePath(exe string) string { return filepath.Join(filepath.Dir(exe), "update-state.json") }
func oldPath(exe string) string   { return exe + ".old" }
func badPath(exe string) string   { return exe + ".bad" }
func lockPath(exe string) string  { return filepath.Join(filepath.Dir(exe), "update.lock") }

// LoadState reads update-state.json. A missing file is an empty state. A
// file that cannot be parsed is moved aside and also treated as empty,
// so one corrupt write can never wedge the updater for good.
func LoadState(exe string) (State, error) {
	var s State
	b, err := os.ReadFile(statePath(exe))
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(b, &s); err != nil {
		os.Rename(statePath(exe), statePath(exe)+".corrupt")
		return State{}, nil
	}
	return s, nil
}

// Save writes the state back atomically, since it is rewritten at exactly
// the moments (just after an update) when crashes are most likely.
func (s State) Save(exe string) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(statePath(exe), b, 0o644)
}

// SelfTest runs the new binary with --selftest and requires it to exit 0
// and print exactly wantVersion within 10 seconds. It catches a build that
// cannot even start, before that build replaces anything. Downloads are
// written without execute permission, so this is where it is granted.
func SelfTest(path, wantVersion string) error {
	if err := os.Chmod(path, 0o755); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--selftest")
	// If the binary leaves a child holding stdout open, stop waiting for
	// the pipe shortly after the timeout instead of hanging forever.
	cmd.WaitDelay = 2 * time.Second
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("install: self-test of %s failed: %w", path, err)
	}
	if got := strings.TrimSpace(string(out)); got != wantVersion {
		return fmt.Errorf("install: self-test printed version %q, want %q", got, wantVersion)
	}
	return nil
}

// Swap makes newBinary the program at exe and keeps the previous program
// at exe.old. The final step is a rename, which is atomic, so a crash in
// the middle leaves either the old or the new program in place, never a
// half written one. How the previous program is preserved differs per OS;
// see preserveCurrent.
func Swap(exe, newBinary string) error {
	info, err := os.Stat(exe)
	if err != nil {
		return err
	}
	if err := os.Chmod(newBinary, info.Mode().Perm()); err != nil {
		return err
	}
	os.Remove(oldPath(exe))
	if err := preserveCurrent(exe, oldPath(exe)); err != nil {
		return fmt.Errorf("install: keep previous version: %w", err)
	}
	if err := os.Rename(newBinary, exe); err != nil {
		// On Windows preserveCurrent moved the program away, so put it
		// back rather than leave no program at all.
		if _, statErr := os.Stat(exe); statErr != nil {
			os.Rename(oldPath(exe), exe)
		}
		return fmt.Errorf("install: swap in new version: %w", err)
	}
	return nil
}

// Rollback puts exe.old back as the program. The current program is moved
// to exe.bad rather than deleted, because on Windows a running executable
// can be renamed but not deleted. Startup removes exe.bad later.
func Rollback(exe string) error {
	if _, err := os.Stat(oldPath(exe)); err != nil {
		return fmt.Errorf("install: nothing to roll back to: %w", err)
	}
	os.Remove(badPath(exe))
	if err := os.Rename(exe, badPath(exe)); err != nil {
		return err
	}
	return os.Rename(oldPath(exe), exe)
}

// Startup is called first thing when the program starts. If a new version
// was just swapped in, it counts this start. Too many starts without a
// Confirm in between means the new version keeps crashing, so it is rolled
// back and added to the skip list. The caller must Restart when rolledBack
// is true, because the program now on disk is not the one running.
func Startup(exe string) (rolledBack bool, err error) {
	os.Remove(badPath(exe))
	st, err := LoadState(exe)
	if err != nil || st.Pending == nil {
		return false, err
	}
	st.Pending.Attempts++
	if st.Pending.Attempts <= MaxStartAttempts {
		return false, st.Save(exe)
	}
	if err := Rollback(exe); err != nil {
		// Nothing to go back to (the .old file is gone), so stop counting
		// and carry on with what is installed.
		st.Pending = nil
		st.Save(exe)
		return false, err
	}
	st.Skip = append(st.Skip, st.Pending.Version)
	st.Pending = nil
	return true, st.Save(exe)
}

// Confirm is called once the program has run healthily for a while after
// an update. It clears the pending marker and drops the old binary, so the
// next start is a normal one.
func Confirm(exe string) error {
	st, err := LoadState(exe)
	if err != nil {
		return err
	}
	if st.Pending != nil {
		st.Pending = nil
		if err := st.Save(exe); err != nil {
			return err
		}
	}
	if err := os.Remove(oldPath(exe)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Lock stops two instances of the program from updating at the same time.
// It creates update.lock exclusively and returns a function that removes
// it. A lock older than ten minutes was left by a crash and is taken over.
func Lock(exe string) (unlock func(), err error) {
	path := lockPath(exe)
	if info, err := os.Stat(path); err == nil && time.Since(info.ModTime()) > 10*time.Minute {
		os.Remove(path)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("install: another update is in progress (%s)", path)
	}
	f.Close()
	return func() { os.Remove(path) }, nil
}
