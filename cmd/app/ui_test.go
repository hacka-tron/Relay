package main

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHistoryPersistsAcrossRestarts(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "app")
	first := newUIState("1.0.0", "stable", "http://s", "cid", exe)
	first.record("started", "started as v1.0.0")
	first.record("available", "update available: v1.1.0")

	// A "restarted" process loads the same file, newest first.
	second := newUIState("1.1.0", "stable", "http://s", "cid", exe)
	if len(second.History) != 2 || second.History[0].Step != "available" || second.History[1].Version != "1.0.0" {
		t.Fatalf("history after restart: %+v", second.History)
	}

	for i := 0; i < maxHistory+10; i++ {
		second.record("check", "x")
	}
	third := newUIState("1.1.0", "stable", "http://s", "cid", exe)
	if len(third.History) != maxHistory {
		t.Fatalf("history not trimmed: %d", len(third.History))
	}
}

func TestLoadHistorySkipsBadLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "h.jsonl")
	os.WriteFile(path, []byte("{not json\n{\"step\":\"started\",\"detail\":\"ok\"}\n"), 0o644)
	h := loadHistory(path)
	if len(h) != 1 || h[0].Step != "started" {
		t.Fatalf("got %+v", h)
	}
}

func TestServeUIStateEndpoint(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "app")
	s := newUIState("1.0.0", "beta", "http://s", "cid", exe)
	s.setStatus("checking", "asking")
	url := serveUI("127.0.0.1:0", s, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if url == "" {
		t.Fatal("could not bind")
	}
	resp, err := http.Get(url + "/api/state")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got map[string]any
	json.NewDecoder(resp.Body).Decode(&got)
	if got["status"] != "checking" || got["channel"] != "beta" || got["version"] != "1.0.0" {
		t.Fatalf("state: %v", got)
	}
	page, _ := http.Get(url + "/")
	body, _ := io.ReadAll(page.Body)
	if page.StatusCode != 200 || !strings.Contains(string(body), "Relay") {
		t.Fatalf("page: %d", page.StatusCode)
	}
	if other, _ := http.Get(url + "/other"); other.StatusCode != 404 {
		t.Fatalf("unknown path: %d", other.StatusCode)
	}
}
