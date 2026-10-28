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
	seq     uint64 // counts check-ins; orders entries even when clock readings tie
}

type clientSeen struct {
	rollout.Facts
	Offered  string // version offered on the last check, or ""
	LastSeen time.Time
	seq      uint64
}

func newClientLog(maxSize int) *clientLog {
	return &clientLog{seen: map[string]clientSeen{}, maxSize: maxSize}
}

// record stores the latest check-in. When full, the entry that checked in
// longest ago is dropped. Order comes from a counter, not the clock,
// because on Windows several check-ins can share one clock reading.
func (l *clientLog) record(f rollout.Facts, offered string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.seen[f.ClientID]; !ok && len(l.seen) >= l.maxSize {
		var oldestID string
		var oldest uint64
		for id, c := range l.seen {
			if oldestID == "" || c.seq < oldest {
				oldestID, oldest = id, c.seq
			}
		}
		delete(l.seen, oldestID)
	}
	l.seq++
	l.seen[f.ClientID] = clientSeen{Facts: f, Offered: offered, LastSeen: time.Now(), seq: l.seq}
}

// list returns clients, most recently seen first.
func (l *clientLog) list() []clientSeen {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]clientSeen, 0, len(l.seen))
	for _, c := range l.seen {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].seq > out[j].seq })
	return out
}
