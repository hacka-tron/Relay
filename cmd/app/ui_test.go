package main

import (
	"encoding/json"
	"fmt"
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
		second.record("check", fmt.Sprint(i))
	}
	third := newUIState("1.1.0", "stable", "http://s", "cid", exe)
	if len(third.History) != maxHistory {
		t.Fatalf("history not trimmed: %d", len(third.History))
	}
	b, _ := os.ReadFile(filepath.Join(filepath.Dir(exe), "update-history.jsonl"))
	if lines := strings.Count(string(b), "\n"); lines != maxHistory {
		t.Fatalf("file not trimmed on disk: %d lines", lines)
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
	// A request that arrived through another host name (DNS rebinding) is refused.
	req, _ := http.NewRequest("GET", url+"/api/state", nil)
	req.Host = "evil.example:4780"
	if resp, _ := http.DefaultClient.Do(req); resp.StatusCode != 403 {
		t.Fatalf("foreign host: %d, want 403", resp.StatusCode)
	}
}

func TestServeUIRefusesNonLoopback(t *testing.T) {
	s := newUIState("1.0.0", "stable", "http://s", "cid", filepath.Join(t.TempDir(), "app"))
	if url := serveUI("0.0.0.0:0", s, slog.New(slog.NewTextHandler(io.Discard, nil))); url != "" {
		t.Fatal("bound a non-loopback address")
	}
}

func TestRecordSkipsRepeats(t *testing.T) {
	s := newUIState("1.0.0", "stable", "http://s", "cid", filepath.Join(t.TempDir(), "app"))
	s.record("rejected", "server down")
	s.record("rejected", "server down")
	s.record("rejected", "other")
	if len(s.History) != 2 {
		t.Fatalf("history: %+v", s.History)
	}
}
