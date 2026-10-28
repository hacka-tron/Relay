package main

import (
	"bufio"
	_ "embed"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"
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
		historyPath: filepath.Join(filepath.Dir(exe), "update-history.jsonl")}
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
// survives the restart that every update causes.
func (s *uiState) record(step, detail string) {
	ev := Event{Time: time.Now(), Step: step, Detail: detail, Version: s.Version}
	s.mu.Lock()
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
func loadHistory(path string) []Event {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var all []Event
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var ev Event
		if json.Unmarshal(sc.Bytes(), &ev) == nil {
			all = append(all, ev)
		}
	}
	if len(all) > maxHistory {
		all = all[len(all)-maxHistory:]
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

// serveUI starts the local page and returns its URL, or "" if the port
// could not be bound (another copy is probably running; the app itself
// keeps going without a window).
func serveUI(addr string, s *uiState, log *slog.Logger) string {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Warn("window not available", "addr", addr, "err", err)
		return ""
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(uiHTML)
	})
	mux.HandleFunc("GET /api/state", s.handleState)
	go http.Serve(ln, mux)
	return "http://" + ln.Addr().String()
}

// openBrowser opens url in the user's default browser. Failure is fine;
// the URL is logged either way.
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	cmd.Start()
}
