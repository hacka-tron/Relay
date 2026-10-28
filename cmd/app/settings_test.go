package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func interval(n int) settingsChange { return settingsChange{IntervalSeconds: &n} }

func TestSettingsDefaultSaveAndReload(t *testing.T) {
	dir := t.TempDir()
	s := newSettingsStore(dir)
	if got := s.get().IntervalSeconds; got != 60 {
		t.Fatalf("default interval %d, want 60", got)
	}
	if err := s.set(interval(300)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.changed:
	default:
		t.Fatal("set did not wake the check loop")
	}
	if got := newSettingsStore(dir).get().IntervalSeconds; got != 300 {
		t.Fatalf("not reloaded: %d", got)
	}
}

func TestSettingsRejectUnofferedIntervals(t *testing.T) {
	s := newSettingsStore(t.TempDir())
	for _, n := range []int{0, 1, 7, 86400} {
		if s.set(interval(n)) == nil {
			t.Fatalf("accepted %d", n)
		}
	}
	if s.get().IntervalSeconds != 60 {
		t.Fatal("a rejected value was applied")
	}
}

// A settings file from an older version, which also saved a server
// choice, keeps its interval and loses the server choice.
func TestSettingsIgnoreOldServerChoice(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"server":"local","interval_seconds":900}`), 0o644)
	if got := newSettingsStore(dir).get().IntervalSeconds; got != 900 {
		t.Fatalf("interval %d, want 900", got)
	}
}

func TestSettingsOverrideIsNotSaved(t *testing.T) {
	dir := t.TempDir()
	s := newSettingsStore(dir)
	s.override(8)
	if s.get().IntervalSeconds != 8 {
		t.Fatal("override not applied")
	}
	if newSettingsStore(dir).get().IntervalSeconds != 60 {
		t.Fatal("override was saved")
	}
}

func TestUpdateServerFollowsTheBuild(t *testing.T) {
	old := cloudServer
	defer func() { cloudServer = old }()
	cloudServer = ""
	if updateServer() != localServer {
		t.Fatal("a build without a cloud server should use the local one")
	}
	cloudServer = "https://cloud.example"
	if updateServer() != "https://cloud.example" {
		t.Fatal("a release build should use its cloud server")
	}
}

func TestSettingsEndpoint(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "app")
	ui := newUIState("1.0.0", "stable", "x", "cid", exe)
	ui.settings = newSettingsStore(filepath.Dir(exe))
	url := serveUI("127.0.0.1:0", ui, slog.New(slog.NewTextHandler(io.Discard, nil)))

	post := func(contentType, body string) int {
		resp, err := http.Post(url+"/api/settings", contentType, bytes.NewBufferString(body))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := post("text/plain", `{"interval_seconds":30}`); code != 415 {
		t.Fatalf("form-style post: %d, want 415", code)
	}
	if code := post("application/json", `{"interval_seconds":7}`); code != 400 {
		t.Fatalf("bad interval: %d, want 400", code)
	}
	if code := post("application/json", `{"interval_seconds":30}`); code != 204 {
		t.Fatalf("good interval: %d, want 204", code)
	}

	resp, err := http.Get(url + "/api/state")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got struct {
		Settings  settings `json:"settings"`
		Intervals []int    `json:"intervals"`
	}
	json.NewDecoder(resp.Body).Decode(&got)
	if got.Settings.IntervalSeconds != 30 || len(got.Intervals) != 6 {
		t.Fatalf("state: %+v", got)
	}
}

func TestClearHistory(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "app")
	ui := newUIState("1.0.0", "stable", "x", "cid", exe)
	ui.record("started", "started as v1.0.0")
	url := serveUI("127.0.0.1:0", ui, slog.New(slog.NewTextHandler(io.Discard, nil)))

	resp, err := http.Post(url+"/api/history/clear", "text/plain", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 415 || len(ui.History) != 1 {
		t.Fatalf("form-style post cleared history: %d", resp.StatusCode)
	}
	resp, err = http.Post(url+"/api/history/clear", "application/json", bytes.NewBufferString("{}"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 204 || len(ui.History) != 0 {
		t.Fatalf("clear: %d, %d entries left", resp.StatusCode, len(ui.History))
	}
	if again := newUIState("1.0.0", "stable", "x", "cid", exe); len(again.History) != 0 {
		t.Fatal("history came back after restart")
	}
}
