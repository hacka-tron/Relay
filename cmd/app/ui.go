package main

import (
	"bufio"
	"bytes"
	_ "embed"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/hacka-tron/relay/internal/atomicfile"
	"github.com/hacka-tron/relay/internal/install"
)

// The window: a small page served on localhost that shows what the updater
// is doing, with a character that reacts to each step and a history of
// updates. The page polls /api/state once a second. It is read only, bound
// to the loopback address, and needs no dependencies: one embedded file.

//go:embed ui.html
var uiHTML []byte

// Event is one line of the history file.
type Event struct {
	Time    time.Time `json:"time"`
	Step    string    `json:"step"` // started, check, available, downloaded, installed, rejected, rolledback, confirmed
	Detail  string    `json:"detail"`
	Version string    `json:"version"` // version running when the event happened
}

// uiState is everything the page shows. One instance is shared by the
// updater loop (writer) and the HTTP handler (reader).
type uiState struct {
	mu          sync.Mutex
	Version     string    `json:"version"`
	Channel     string    `json:"channel"`
	Server      string    `json:"server"`
	ClientID    string    `json:"client_id"`
	Status      string    `json:"status"` // idle, checking, updating, rejected
	StatusNote  string    `json:"status_note"`
	LastCheck   time.Time `json:"last_check"`
	NextCheck   time.Time `json:"next_check"`
	History     []Event   `json:"history"`
	historyPath string
}

const maxHistory = 200

func newUIState(version, channel, server, clientID, exe string) *uiState {
	s := &uiState{Version: version, Channel: channel, Server: server, ClientID: clientID, Status: "idle",
		historyPath: filepath.Join(install.StateDir(exe), "update-history.jsonl")}
	s.History = loadHistory(s.historyPath)
	return s
}

// setStatus changes what the character is doing.
func (s *uiState) setStatus(status, note string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Status, s.StatusNote = status, note
}

func (s *uiState) setChecked(next time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.LastCheck, s.NextCheck = time.Now(), next
}

// record appends an event to memory and to the history file, so the list
// survives the restart that every update causes. A repeat of the newest
// entry (the same failure every check, say) is not recorded again.
func (s *uiState) record(step, detail string) {
	ev := Event{Time: time.Now(), Step: step, Detail: detail, Version: s.Version}
	s.mu.Lock()
	if len(s.History) > 0 && s.History[0].Step == step && s.History[0].Detail == detail && s.History[0].Version == s.Version {
		s.mu.Unlock()
		return
	}
	s.History = append([]Event{ev}, s.History...)
	if len(s.History) > maxHistory {
		s.History = s.History[:maxHistory]
	}
	s.mu.Unlock()
	if f, err := os.OpenFile(s.historyPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
		json.NewEncoder(f).Encode(ev)
		f.Close()
	}
}

// loadHistory reads the file newest first, keeping the last maxHistory.
// When the file has grown past that, it is rewritten with just those
// lines so it does not grow without bound.
func loadHistory(path string) []Event {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	var all []Event
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var ev Event
		if json.Unmarshal(sc.Bytes(), &ev) == nil {
			all = append(all, ev)
		}
	}
	f.Close()
	if len(all) > maxHistory {
		all = all[len(all)-maxHistory:]
		var buf bytes.Buffer
		for _, ev := range all {
			json.NewEncoder(&buf).Encode(ev)
		}
		atomicfile.Write(path, buf.Bytes(), 0o644)
	}
	for i, j := 0, len(all)-1; i < j; i, j = i+1, j-1 {
		all[i], all[j] = all[j], all[i]
	}
	return all
}

func (s *uiState) handleState(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(s)
}

// serveUI starts the local page and returns its URL, or "" if it could not
// (another copy is probably running; the app keeps going without a
// window). Only loopback addresses are accepted, because the page exposes
// the client ID and history. The bind is retried for a few seconds: on
// Windows the previous version is still shutting down when the new one
// starts, and it holds the port until then.
func serveUI(addr string, s *uiState, log *slog.Logger) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil || !(host == "localhost" || net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback()) {
		log.Warn("window address must be loopback; window disabled", "addr", addr)
		return ""
	}
	var ln net.Listener
	for attempt := 0; attempt < 25; attempt++ {
		if ln, err = net.Listen("tcp", addr); err == nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err != nil {
		log.Warn("window not available", "addr", addr, "err", err)
		return ""
	}
	go http.Serve(ln, localOnly(ln.Addr().String(), s.handler()))
	return "http://" + ln.Addr().String()
}

// handler serves the page and its state. The browser build puts it behind
// a loopback listener; the desktop build hands it straight to the native
// window, which needs no port at all.
func (s *uiState) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(uiHTML)
	})
	mux.HandleFunc("GET /api/state", s.handleState)
	return mux
}

// localOnly refuses requests whose Host header is not this listener. A
// hostile web page cannot read our responses directly (no CORS), but a
// DNS rebinding trick could point its own name at 127.0.0.1; checking the
// Host header shuts that down.
func localOnly(self string, next http.Handler) http.Handler {
	_, port, _ := net.SplitHostPort(self)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != self && r.Host != "localhost:"+port {
			http.Error(w, "wrong host", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
