// Package rollout decides which version, if any, a client should be
// offered. It is pure: no I/O, no clock, so every rule is easy to unit
// test and easy to extend live.
package rollout

import (
	"hash/fnv"
	"strings"

	"golang.org/x/mod/semver"
)

// Facts is what the client tells us about itself on each check. The client
// sends all of these from day one so new server rules rarely need a client
// change.
type Facts struct {
	ClientID string
	Version  string // the version the client is running now
	OS       string
	Arch     string
	Channel  string // stable or beta
}

// Channel is one entry of rollout.json. Version is the newest version this
// channel is pointing at, RolloutPercent is how many clients get it, and
// Paused is the kill switch.
type Channel struct {
	Version        string `json:"version"`
	RolloutPercent int    `json:"rollout_percent"`
	Paused         bool   `json:"paused"`
}

// Config is the whole of rollout.json.
type Config struct {
	Channels map[string]Channel `json:"channels"`
}

// Decide returns the version the client should move to and true, or "" and
// false when the client should stay where it is. The rules, in order:
//
//  1. The channel must exist.
//  2. The channel must not be paused.
//  3. The target must be strictly newer than what the client runs. Never
//     offer a downgrade or the same version.
//  4. The client must fall inside the rollout percentage.
func Decide(f Facts, cfg Config) (string, bool) {
	ch, ok := cfg.Channels[f.Channel]
	if !ok || ch.Paused || ch.Version == "" {
		return "", false
	}
	if !isNewer(ch.Version, f.Version) {
		return "", false
	}
	if !inRollout(f.ClientID, ch.Version, ch.RolloutPercent) {
		return "", false
	}
	return ch.Version, true
}

// isNewer reports whether target is strictly greater than current by
// semver. Versions are stored without the leading v that the semver
// package wants, so it is added here (after removing one if a client sent
// it). An unparseable current version compares as lower than any valid
// one, so a client with a corrupt version string is still offered the
// update rather than being stranded.
func isNewer(target, current string) bool {
	return semver.Compare(canon(target), canon(current)) > 0
}

func canon(v string) string {
	return "v" + strings.TrimPrefix(v, "v")
}

// inRollout buckets a client into 0..99 by hashing its ID together with the
// target version. The same client always lands in the same bucket for a
// given version, so it does not flip in and out of a rollout between
// checks, and a new version reshuffles who goes first.
func inRollout(clientID, version string, percent int) bool {
	if percent <= 0 {
		return false
	}
	if percent >= 100 {
		return true
	}
	h := fnv.New32a()
	h.Write([]byte(clientID + version))
	return int(h.Sum32()%100) < percent
}
