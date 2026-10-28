package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"testing"
)

func testOptions() []serverOption {
	return []serverOption{{ID: "cloud", Label: "Cloud", URL: "https://cloud.example"}, {ID: "local", Label: "This computer", URL: localServer}}
}

func TestSettingsDefaultsSaveAndReload(t *testing.T) {
	dir := t.TempDir()
	s := newSettingsStore(dir, testOptions())
	if got := s.get(); got.Server != "cloud" || got.IntervalSeconds != 60 {
		t.Fatalf("defaults: %+v", got)
	}
	if err := s.set(settings{Server: "local", IntervalSeconds: 300}); err != nil {
		t.Fatal(err)
	}
	if s.serverURL() != localServer {
		t.Fatalf("url: %s", s.serverURL())
	}
	select {
	case <-s.changed:
	default:
		t.Fatal("set did not wake the check loop")
	}
	again := newSettingsStore(dir, testOptions())
	if got := again.get(); got.Server != "local" || got.IntervalSeconds != 300 {
		t.Fatalf("not reloaded: %+v", got)
	}
}

func TestSettingsRejectUnofferedValues(t *testing.T) {
	s := newSettingsStore(t.TempDir(), testOptions())
	for _, v := range []settings{
		{Server: "evil", IntervalSeconds: 60},
		{Server: "local", IntervalSeconds: 1},
		{Server: "local", IntervalSeconds: 0},
	} {
		if s.set(v) == nil {
			t.Fatalf("accepted %+v", v)
		}
	}
	if s.get().Server != "cloud" {
		t.Fatal("a rejected value was applied")
	}
}

func TestSettingsOverrideIsNotSaved(t *testing.T) {
	dir := t.TempDir()
	s := newSettingsStore(dir, testOptions())
	s.override("http://other.example:9000", 8)
	if s.serverURL() != "http://other.example:9000" || s.get().IntervalSeconds != 8 {
		t.Fatalf("override not applied: %s %+v", s.serverURL(), s.get())
	}
	if again := newSettingsStore(dir, testOptions()); again.get().Server != "cloud" {
		t.Fatal("override was saved")
	}
}

func TestSettingsWithoutCloudOfferOnlyLocal(t *testing.T) {
	old := cloudServer
	cloudServer = ""
	defer func() { cloudServer = old }()
	opts := serverOptions()
	if len(opts) != 1 || opts[0].ID != "local" {
		t.Fatalf("options: %+v", opts)
	}
}

func TestSettingsEndpoint(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "app")
	ui := newUIState("1.0.0", "stable", "x", "cid", exe)
	ui.settings = newSettingsStore(filepath.Dir(exe), testOptions())
	url := serveUI("127.0.0.1:0", ui, slog.New(slog.NewTextHandler(io.Discard, nil)))

	post := func(contentType, body string) int {
		resp, err := http.Post(url+"/api/settings", contentType, bytes.NewBufferString(body))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := post("text/plain", `{"server":"local","interval_seconds":30}`); code != 415 {
		t.Fatalf("form-style post: %d, want 415", code)
	}
	if code := post("application/json", `{"server":"local","interval_seconds":7}`); code != 400 {
		t.Fatalf("bad interval: %d, want 400", code)
	}
	if code := post("application/json", `{"server":"local","interval_seconds":30}`); code != 204 {
		t.Fatalf("good settings: %d, want 204", code)
	}

	resp, err := http.Get(url + "/api/state")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got struct {
		Settings settings       `json:"settings"`
		Servers  []serverOption `json:"servers"`
		Version  string         `json:"version"`
	}
	json.NewDecoder(resp.Body).Decode(&got)
	if got.Settings.Server != "local" || got.Settings.IntervalSeconds != 30 || len(got.Servers) != 2 || got.Version != "1.0.0" {
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
	// Cleared for good: a restarted app loads nothing.
	if again := newUIState("1.0.0", "stable", "x", "cid", exe); len(again.History) != 0 {
		t.Fatal("history came back after restart")
	}
}
