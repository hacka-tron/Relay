// Command app is a small long-running program that keeps itself up to
// date. The "application" is just a greeting printed every few seconds so
// a version change is easy to see; everything else is the updater.
package main

import (
	"context"
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

// Both values are injected at build time by the release tool:
//
//	-ldflags "-X main.version=1.2.0 -X main.publicKey=<base64>"
//
// A plain `go build` leaves them at these defaults, which disables the
// updater, so a developer build never tries to replace itself.
var (
	version   = "0.0.0-dev"
	publicKey = ""
)

func main() {
	server := flag.String("server", "http://localhost:8080", "update server base URL")
	channel := flag.String("channel", "stable", "release channel to follow")
	interval := flag.Duration("interval", time.Minute, "how often to check for updates")
	selftest := flag.Bool("selftest", false, "print the version and exit; used to validate a new binary before installing it")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()

	if *selftest || *showVersion {
		fmt.Println(version)
		return
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, nil)).With("version", version)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	exe, err := os.Executable()
	if err != nil {
		log.Error("cannot find own executable; self-update disabled", "err", err)
	} else {
		startupChecks(exe, log)
		if up := newUpdater(*server, *channel, log); up != nil {
			go checkLoop(ctx, up, exe, *interval, log)
		}
	}

	// The demo "application".
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
func checkLoop(ctx context.Context, up *updater.Client, exe string, interval time.Duration, log *slog.Logger) {
	for {
		if err := checkOnce(ctx, up, exe, log); err != nil {
			log.Error("update check failed", "err", err)
		}
		jitter := time.Duration(rand.Int64N(int64(interval) / 5))
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval - interval/10 + jitter):
		}
	}
}

// checkOnce runs one update attempt. Apply only returns on failure; on
// success the process restarts as the new version.
func checkOnce(ctx context.Context, up *updater.Client, exe string, log *slog.Logger) error {
	m, ok, err := up.Check(ctx)
	if err != nil || !ok {
		return err
	}
	log.Info("update available", "new_version", m.Version)
	return up.Apply(ctx, m, exe)
}

// healthyAfter is how long the program must run after an update before
// that update is confirmed and the previous binary is dropped.
const healthyAfter = 30 * time.Second

// startupChecks runs the post-update bookkeeping: clean leftovers, count
// this start against a pending update and roll back if it keeps failing,
// and schedule the confirmation that marks the update good.
func startupChecks(exe string, log *slog.Logger) {
	dir := filepath.Dir(exe)
	updater.CleanupTemp(dir)
	rolledBack, err := install.Startup(exe)
	if err != nil {
		log.Error("startup check failed", "err", err)
		return
	}
	if rolledBack {
		log.Warn("new version failed to start repeatedly; rolled back, restarting previous version")
		if err := install.Restart(exe); err != nil {
			log.Error("restart after rollback failed", "err", err)
		}
		return
	}
	go func() {
		time.Sleep(healthyAfter)
		if err := install.Confirm(exe); err != nil {
			log.Error("confirm update failed", "err", err)
		}
	}()
}
