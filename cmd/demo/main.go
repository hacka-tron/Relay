// Command demo runs the whole update system on this computer, so you can
// publish new versions and watch Relay update itself. It needs only Go and
// uses a throwaway signing key, never the real one.
//
//	go run ./cmd/demo start   start an update server and Relay 1.0.0; leave it running
//	go run ./cmd/demo next    publish the next version and watch Relay install it
//	go run ./cmd/demo bad     publish a tampered next version and watch Relay refuse it
//
// Everything goes in ./demo. Ctrl-C in the start terminal stops it all.
// `go run ./cmd/demo ci` runs start, next and bad in one go without a
// browser, which is how CI checks updates on every OS.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const dir = "demo"

var (
	exe       = map[bool]string{true: ".exe", false: ""}[runtime.GOOS == "windows"]
	tool      = filepath.Join(dir, "release"+exe)
	server    = filepath.Join(dir, "updateserver"+exe)
	key       = filepath.Join(dir, "signing.key")
	releases  = filepath.Join(dir, "releases")
	appPath   = filepath.Join(dir, "app", "relay-demo"+exe)
	uiURLFile = filepath.Join(dir, "window-url")
)

const usage = `usage:
  go run ./cmd/demo start   start an update server and Relay 1.0.0, and leave them running
  go run ./cmd/demo next    publish the next version and watch Relay install it
  go run ./cmd/demo bad     publish a tampered next version and watch Relay refuse it`

func main() {
	if _, err := os.Stat("go.mod"); err != nil {
		fail(errors.New("run this from the root of the Relay repository"))
	}
	if len(os.Args) != 2 {
		fmt.Println(usage)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	var err error
	switch os.Args[1] {
	case "start":
		err = start(ctx)
	case "next":
		err = publish(ctx, false)
	case "bad":
		err = publish(ctx, true)
	case "ci":
		err = ci(ctx)
	default:
		fmt.Println(usage)
		os.Exit(2)
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "\ndemo:", err)
	os.Exit(1)
}

// running holds the two background programs so they can be stopped.
type running struct{ server, app *exec.Cmd }

// start builds everything, starts the server and Relay 1.0.0, and waits
// for Ctrl-C.
func start(ctx context.Context) error {
	r, err := launch(ctx, true)
	defer r.stop()
	if err != nil {
		return err
	}
	fmt.Println("\nRelay 1.0.0 is running. Its window is", readURL())
	fmt.Println("\nIn another terminal, from this folder, run")
	fmt.Println("   go run ./cmd/demo next    to publish a new version and watch it install")
	fmt.Println("   go run ./cmd/demo bad     to publish a tampered version and watch it get refused")
	fmt.Println("\nPress Ctrl-C here to stop everything.")
	<-ctx.Done()
	return ctx.Err()
}

// launch builds the tools, makes a key, publishes 1.0.0, and starts the
// server and the app in the background. openWindow opens the app's page in
// the browser.
func launch(ctx context.Context, openWindow bool) (*running, error) {
	r := &running{}
	os.RemoveAll(dir)
	if err := os.MkdirAll(filepath.Join(dir, "app"), 0o755); err != nil {
		return r, err
	}
	step("Building the release tool and the update server")
	if err := goBuild(tool, "./cmd/release"); err != nil {
		return r, err
	}
	if err := goBuild(server, "./cmd/updateserver"); err != nil {
		return r, err
	}
	step("Making a throwaway signing key and building Relay 1.0.0")
	if err := run(tool, "keygen", "--out", dir); err != nil {
		return r, err
	}
	if err := build("1.0.0"); err != nil {
		return r, err
	}
	if err := run(tool, "promote", "--version", "1.0.0", "--releases", releases); err != nil {
		return r, err
	}

	port, err := freePort("8080")
	if err != nil {
		return r, err
	}
	serverURL := "http://localhost:" + port
	step("Starting the update server on " + serverURL)
	if r.server, err = background(filepath.Join(dir, "server.log"), server,
		"-addr", "127.0.0.1:"+port, "-releases", releases, "-base-url", serverURL); err != nil {
		return r, err
	}
	if err := waitFor(ctx, 30*time.Second, func() bool { return get(serverURL+"/health") == nil }); err != nil {
		return r, errors.New("the update server did not start; see demo/server.log")
	}

	uiPort, err := freePort("4790")
	if err != nil {
		return r, err
	}
	if err := os.WriteFile(uiURLFile, []byte("http://127.0.0.1:"+uiPort), 0o644); err != nil {
		return r, err
	}
	if err := copyFile(filepath.Join(releases, "1.0.0", binaryName()), appPath); err != nil {
		return r, err
	}
	step("Starting Relay 1.0.0, checking for updates every 5 seconds")
	if r.app, err = background(filepath.Join(dir, "app.log"), appPath, "--server", serverURL,
		"--interval", "5s", "--confirm-after", "8s", "--ui", "127.0.0.1:"+uiPort,
		"--open="+strconv.FormatBool(openWindow)); err != nil {
		return r, err
	}
	err = waitFor(ctx, 60*time.Second, func() bool { s, err := appState(); return err == nil && s.Version == "1.0.0" })
	if err != nil && !errors.Is(err, context.Canceled) {
		return r, errors.New("Relay did not start; see demo/app.log")
	}
	return r, err
}

// stop ends the server and the app. On Windows an update restarts the app
// as a new process, so it is also stopped by name.
func (r *running) stop() {
	for _, c := range []*exec.Cmd{r.server, r.app} {
		if c != nil && c.Process != nil {
			c.Process.Kill()
		}
	}
	if runtime.GOOS == "windows" {
		exec.Command("taskkill", "/F", "/IM", "relay-demo.exe").Run()
	}
}

// publish builds the next version, signs it with the demo key, makes the
// server offer it, and prints each step Relay reports until the update is
// installed or refused. A bad version has one byte changed after signing,
// as an attacker who controls the server or the download would do.
func publish(ctx context.Context, bad bool) error {
	if _, err := appState(); err != nil {
		return errors.New("the demo is not running. Start it first with: go run ./cmd/demo start")
	}
	current, err := offeredVersion()
	if err != nil {
		return err
	}
	next := bumpMinor(current)
	if bad {
		step("Building " + next + ", signing it, then changing its bytes as an attacker would")
	} else {
		step("Building and signing " + next)
	}
	if err := build(next); err != nil {
		return err
	}
	if bad {
		if err := tamper(filepath.Join(releases, next, binaryName())); err != nil {
			return err
		}
	}
	if err := run(tool, "promote", "--version", next, "--releases", releases); err != nil {
		return err
	}
	step("The server now offers " + next + ". Watching Relay, whose window shows the same steps")

	done := func(e event) bool {
		if bad {
			return e.Step == "rejected" && strings.Contains(e.Detail, "sha256")
		}
		return e.Step == "confirmed" && strings.Contains(e.Detail, next)
	}
	if err := watch(ctx, done); err != nil {
		return err
	}
	if bad {
		step("Relay refused " + next + " because its hash no longer matches the signed one, and kept running")
	} else {
		step("Relay updated itself to " + next)
	}
	return nil
}

// ci runs start, next and bad in a row without a browser.
func ci(ctx context.Context) error {
	r, err := launch(ctx, false)
	defer r.stop()
	if err != nil {
		return err
	}
	if err := publish(ctx, false); err != nil {
		return err
	}
	return publish(ctx, true)
}

type event struct {
	Time   time.Time `json:"time"`
	Step   string    `json:"step"`
	Detail string    `json:"detail"`
}

type state struct {
	Version string  `json:"version"`
	History []event `json:"history"`
}

func appState() (state, error) {
	var s state
	resp, err := http.Get(readURL() + "/api/state")
	if err != nil {
		return s, err
	}
	defer resp.Body.Close()
	return s, json.NewDecoder(resp.Body).Decode(&s)
}

// watch prints each new history entry as Relay records it, until done
// returns true for one. Entries from before it started are skipped.
func watch(ctx context.Context, done func(event) bool) error {
	seen := map[string]bool{}
	if s, err := appState(); err == nil {
		for _, e := range s.History {
			seen[e.Time.String()+e.Step+e.Detail] = true
		}
	}
	finished := false
	err := waitFor(ctx, 2*time.Minute, func() bool {
		s, err := appState()
		if err != nil {
			return false // Relay is restarting
		}
		for i := len(s.History) - 1; i >= 0; i-- { // oldest first
			e := s.History[i]
			id := e.Time.String() + e.Step + e.Detail
			if seen[id] {
				continue
			}
			seen[id] = true
			fmt.Printf("   %s  %s\n", e.Time.Local().Format("15:04:05"), e.Detail)
			finished = finished || done(e)
		}
		return finished
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		return errors.New("Relay did not finish within two minutes; see demo/app.log")
	}
	return err
}

// offeredVersion is the version the demo server currently offers.
func offeredVersion() (string, error) {
	var cfg struct {
		Channels map[string]struct {
			Version string `json:"version"`
		} `json:"channels"`
	}
	b, err := os.ReadFile(filepath.Join(releases, "rollout.json"))
	if err == nil {
		err = json.Unmarshal(b, &cfg)
	}
	if err != nil {
		return "", fmt.Errorf("reading the demo's rollout.json: %w", err)
	}
	return cfg.Channels["stable"].Version, nil
}

// bumpMinor turns 1.2.0 into 1.3.0.
func bumpMinor(v string) string {
	parts := strings.Split(v, ".")
	minor, _ := strconv.Atoi(parts[1])
	return parts[0] + "." + strconv.Itoa(minor+1) + ".0"
}

func readURL() string {
	b, _ := os.ReadFile(uiURLFile)
	return strings.TrimSpace(string(b))
}

// build makes one signed version for this computer only. It is the plain
// build, which shows its window in the browser, so it needs nothing but Go.
func build(version string) error {
	return run(tool, "build", "--version", version, "--key", key, "--out", releases,
		"--targets", runtime.GOOS+"/"+runtime.GOARCH)
}

func step(msg string) { fmt.Println("\n== " + msg) }

func binaryName() string { return "app-" + runtime.GOOS + "-" + runtime.GOARCH + exe }

func run(name string, args ...string) error {
	if out, err := exec.Command(name, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("%s %s: %v\n%s", filepath.Base(name), args[0], err, out)
	}
	return nil
}

func goBuild(out, pkg string) error { return run("go", "build", "-o", out, pkg) }

// background starts a program with its output going to logPath.
func background(logPath, name string, args ...string) (*exec.Cmd, error) {
	log, err := os.Create(logPath)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(name, args...)
	cmd.Stdout, cmd.Stderr = log, log
	return cmd, cmd.Start()
}

// freePort returns preferred if nothing is listening on it, otherwise any
// free port. It probes all interfaces, not just loopback, because a server
// listening on all interfaces would otherwise go unnoticed.
func freePort(preferred string) (string, error) {
	for _, addr := range []string{":" + preferred, ":0"} {
		if ln, err := net.Listen("tcp", addr); err == nil {
			_, port, _ := net.SplitHostPort(ln.Addr().String())
			ln.Close()
			return port, nil
		}
	}
	return "", errors.New("no free port")
}

func get(url string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", url, resp.Status)
	}
	return nil
}

func waitFor(ctx context.Context, limit time.Duration, ok func() bool) error {
	deadline := time.Now().Add(limit)
	for !ok() {
		if time.Now().After(deadline) {
			return errors.New("timed out")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return nil
}

func copyFile(from, to string) error {
	src, err := os.Open(from)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.OpenFile(to, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		return err
	}
	return dst.Close()
}

// tamper flips one byte in the middle of a file.
func tamper(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	b := make([]byte, 1)
	at := info.Size() / 2
	if _, err := f.ReadAt(b, at); err != nil {
		return err
	}
	b[0] ^= 0xFF
	_, err = f.WriteAt(b, at)
	return err
}
