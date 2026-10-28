// Command app is a small long-running program that keeps itself up to
// date. The "application" is just a greeting printed every few seconds so
// a version change is easy to see; everything else is the updater.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"time"

	"github.com/hacka-tron/relay/internal/install"
	"github.com/hacka-tron/relay/internal/manifest"
	"github.com/hacka-tron/relay/internal/updater"
)

// These are injected at build time by the release tool:
//
//	-ldflags "-X main.version=1.2.0 -X main.publicKey=<base64> -X main.cloudServer=https://..."
//
// A plain `go build` leaves them at these defaults: no public key disables
// the updater, so a developer build never tries to replace itself, and no
// cloud server means the window only offers a server on this computer.
var (
	version     = "0.0.0-dev"
	publicKey   = ""
	cloudServer = ""
)

func main() {
	server := flag.String("server", "", "update server base URL, for testing; overrides the one built in")
	channel := flag.String("channel", "stable", "release channel to follow")
	interval := flag.Duration("interval", 0, "how often to check for updates; overrides the window's setting for this run")
	confirmAfter := flag.Duration("confirm-after", 30*time.Second, "how long a new version must run before it is confirmed and the previous one is dropped")
	selftest := flag.Bool("selftest", false, "print the version and exit; used to validate a new binary before installing it")
	showVersion := flag.Bool("version", false, "print the version and exit")
	uiAddr := flag.String("ui", "127.0.0.1:4780", "browser build only: address for the local page; empty disables it")
	open := flag.Bool("open", true, "browser build only: open the page in a browser on a fresh start")
	headless := flag.Bool("headless", false, "no window and no page: run as a background agent")
	flag.Parse()

	if *selftest || *showVersion {
		fmt.Println(version)
		return
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, nil)).With("version", version)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	var ui *uiState
	afterUpdate := false
	exe, err := os.Executable()
	if err != nil {
		log.Error("cannot find own executable; self-update disabled", "err", err)
	} else {
		store := newSettingsStore(install.StateDir(exe))
		store.override(int(interval.Seconds()))
		serverURL := updateServer()
		if *server != "" {
			serverURL = *server
		}
		up := newUpdater(serverURL, *channel, log)
		ui = newUIState(version, *channel, serverURL, clientIDOf(up), exe)
		ui.settings = store
		afterUpdate = startupChecks(exe, *confirmAfter, ui, log)
		if up != nil {
			up.Notify = ui.record
			go checkLoop(ctx, up, exe, store, ui, log)
		}
	}

	go greet(ctx)

	// showWindow blocks until the user quits. The desktop build opens a
	// native window (window_desktop.go); the plain build serves the page on
	// localhost and opens a browser tab (window_browser.go). After an
	// update a browser tab is already open and polling, so only a fresh
	// start opens one.
	showWindow(ctx, ui, windowOptions{addr: *uiAddr, openBrowser: *open && !afterUpdate, headless: *headless}, log)
}

// windowOptions are the window flags. addr and openBrowser apply only to
// the browser build.
type windowOptions struct {
	addr        string
	openBrowser bool
	headless    bool
}

// greet is the demo "application": it prints its version every few seconds.
func greet(ctx context.Context) {
	for {
		fmt.Printf("hello from v%s\n", version)
		select {
		case <-ctx.Done():
			return
		case <-time.After(3 * time.Second):
		}
	}
}

// newUpdater wires the updater from flags and build-time values. It
// returns nil, and logs why, when this build cannot update itself.
func newUpdater(server, channel string, log *slog.Logger) *updater.Client {
	if publicKey == "" {
		log.Warn("no public key compiled in; self-update disabled")
		return nil
	}
	pub, err := manifest.ParsePublicKey(publicKey)
	if err != nil {
		log.Error("bad compiled-in public key; self-update disabled", "err", err)
		return nil
	}
	id, err := clientID()
	if err != nil {
		log.Error("cannot read or create client id; self-update disabled", "err", err)
		return nil
	}
	return &updater.Client{
		ServerURL: server,
		Channel:   channel,
		ClientID:  id,
		Version:   version,
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
		PublicKey: pub,
		HTTP:      &http.Client{Timeout: 5 * time.Minute},
		Log:       log,
	}
}

// checkLoop checks once at startup and then every interval, with up to 20
// percent of jitter so a fleet of clients does not hit the server in step.
// Errors are logged and retried next time; they never stop the program.
// After every check it reads the interval from the settings, so a new
// choice in the window applies right away: changing it also cuts the wait
// short.
func checkLoop(ctx context.Context, up *updater.Client, exe string, store *settingsStore, ui *uiState, log *slog.Logger) {
	for {
		interval := max(time.Duration(store.get().IntervalSeconds)*time.Second, time.Second)
		ui.setStatus("checking", "asking the server what the "+up.Channel+" channel offers")
		if err := checkOnce(ctx, up, exe, ui, log); err != nil {
			log.Error("update check failed", "err", err)
			ui.setStatus("rejected", err.Error())
			// A release that fails its checks is recorded by the updater
			// itself; this records everything else, such as the server
			// being unreachable.
			if !up.Recorded(err) {
				ui.record("rejected", err.Error())
			}
		} else {
			ui.setStatus("idle", "up to date")
		}
		jitter := time.Duration(rand.Int64N(int64(interval) / 5))
		wait := interval - interval/10 + jitter
		ui.setChecked(time.Now().Add(wait))
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		case <-store.changed:
		}
	}
}

// checkOnce runs one update attempt. Apply only returns on failure; on
// success the process restarts as the new version.
func checkOnce(ctx context.Context, up *updater.Client, exe string, ui *uiState, log *slog.Logger) error {
	m, ok, err := up.Check(ctx)
	if err != nil || !ok {
		return err
	}
	// A release that was rolled back, or that failed its checks too often
	// in this run, is treated as no update, so it does not show as a
	// failure on every check.
	if st, err := install.LoadState(exe); (err == nil && st.Skips(m.Version)) || up.GaveUp(m) {
		return nil
	}
	log.Info("update available", "new_version", m.Version)
	ui.setStatus("updating", "installing v"+m.Version)
	if err := up.Apply(ctx, m, exe); !errors.Is(err, updater.ErrSkipped) {
		return err
	}
	return nil
}

// clientIDOf is the ID the updater sends, or "" when there is no updater.
func clientIDOf(up *updater.Client) string {
	if up == nil {
		return ""
	}
	return up.ClientID
}

// startupChecks runs the post-update bookkeeping: clean leftovers, count
// this start against a pending update and roll back if it keeps failing,
// and schedule the confirmation that marks the update good once the
// program has run for confirmAfter. It reports whether this start follows
// an update, which the window uses to decide whether to open a browser.
func startupChecks(exe string, confirmAfter time.Duration, ui *uiState, log *slog.Logger) (afterUpdate bool) {
	dir := filepath.Dir(exe)
	updater.CleanupTemp(dir)
	st, _ := install.LoadState(exe)
	afterUpdate = st.Pending != nil
	rolledBack, err := install.Startup(exe)
	if err != nil {
		log.Error("startup check failed", "err", err)
		return afterUpdate
	}
	if rolledBack {
		log.Warn("new version failed to start repeatedly; rolled back, restarting previous version")
		ui.record("rolledback", "v"+st.Pending.Version+" kept failing to start; rolled back")
		if err := install.Restart(exe); err != nil {
			log.Error("restart after rollback failed", "err", err)
		}
		return afterUpdate
	}
	if afterUpdate {
		ui.record("started", "started as v"+version+" after update, watching for problems")
	} else {
		ui.record("started", "started as v"+version)
	}
	go func() {
		time.Sleep(confirmAfter)
		if err := install.Confirm(exe); err != nil {
			log.Error("confirm update failed", "err", err)
			return
		}
		if afterUpdate {
			ui.record("confirmed", "v"+version+" confirmed healthy, previous version removed")
		}
	}()
	return afterUpdate
}
