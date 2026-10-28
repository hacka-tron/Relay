package main

import (
	"sort"
	"sync"
	"time"

	"github.com/hacka-tron/relay/internal/rollout"
)

// clientLog remembers the most recent check-in from each client so the
// admin page can show who is running what. It lives in memory only: it is
// a window onto the fleet, not a record, and it is per server instance.
type clientLog struct {
	mu      sync.Mutex
	seen    map[string]clientSeen
	maxSize int
}

type clientSeen struct {
	rollout.Facts
	Offered  string // version offered on the last check, or ""
	LastSeen time.Time
}

func newClientLog(maxSize int) *clientLog {
	return &clientLog{seen: map[string]clientSeen{}, maxSize: maxSize}
}

// record stores the latest check-in. When full, the oldest entry is dropped.
func (l *clientLog) record(f rollout.Facts, offered string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.seen[f.ClientID]; !ok && len(l.seen) >= l.maxSize {
		oldestID, oldest := "", time.Now()
		for id, c := range l.seen {
			if c.LastSeen.Before(oldest) {
				oldestID, oldest = id, c.LastSeen
			}
		}
		delete(l.seen, oldestID)
	}
	l.seen[f.ClientID] = clientSeen{Facts: f, Offered: offered, LastSeen: time.Now()}
}

// list returns clients, most recently seen first.
func (l *clientLog) list() []clientSeen {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]clientSeen, 0, len(l.seen))
	for _, c := range l.seen {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastSeen.After(out[j].LastSeen) })
	return out
}
