package rollout

import (
	"fmt"
	"testing"
)

func stableAt(version string, percent int) Config {
	return Config{Channels: map[string]Channel{
		"stable": {Version: version, RolloutPercent: percent},
	}}
}

func facts(version string) Facts {
	return Facts{ClientID: "client-1", Version: version, OS: "linux", Arch: "amd64", Channel: "stable"}
}

func TestOffersNewerVersion(t *testing.T) {
	got, ok := Decide(facts("1.0.0"), stableAt("1.1.0", 100))
	if !ok || got != "1.1.0" {
		t.Fatalf("got %q,%v want 1.1.0,true", got, ok)
	}
}

func TestNeverOffersSameOrOlderVersion(t *testing.T) {
	for _, current := range []string{"1.1.0", "1.2.0", "2.0.0", "1.1.1-rc1", "v1.1.0", "v2.0.0"} {
		if v, ok := Decide(facts(current), stableAt("1.1.0", 100)); ok {
			t.Errorf("client on %s was offered %s", current, v)
		}
	}
	// 1.1.0-rc1 is a prerelease of 1.1.0, so 1.1.0 is newer.
	if _, ok := Decide(facts("1.1.0-rc1"), stableAt("1.1.0", 100)); !ok {
		t.Error("prerelease client was not offered the final version")
	}
	// A client whose version string is garbage still gets the update.
	if _, ok := Decide(facts("corrupt"), stableAt("1.1.0", 100)); !ok {
		t.Error("client with invalid version was not offered the update")
	}
}

func TestPausedOffersNothing(t *testing.T) {
	cfg := stableAt("1.1.0", 100)
	ch := cfg.Channels["stable"]
	ch.Paused = true
	cfg.Channels["stable"] = ch
	if _, ok := Decide(facts("1.0.0"), cfg); ok {
		t.Fatal("paused channel offered an update")
	}
}

func TestUnknownChannelOffersNothing(t *testing.T) {
	f := facts("1.0.0")
	f.Channel = "nightly"
	if _, ok := Decide(f, stableAt("1.1.0", 100)); ok {
		t.Fatal("unknown channel offered an update")
	}
	if _, ok := Decide(f, Config{}); ok {
		t.Fatal("empty config offered an update")
	}
}

func TestRolloutPercentEdges(t *testing.T) {
	for i := 0; i < 50; i++ {
		f := facts("1.0.0")
		f.ClientID = fmt.Sprintf("c%d", i)
		if _, ok := Decide(f, stableAt("1.1.0", 0)); ok {
			t.Fatal("0 percent offered an update")
		}
		if _, ok := Decide(f, stableAt("1.1.0", 100)); !ok {
			t.Fatal("100 percent withheld an update")
		}
	}
}

func TestBucketingIsDeterministicAndRoughlyMatchesPercent(t *testing.T) {
	const n = 10000
	cfg := stableAt("1.1.0", 25)
	offered := 0
	for i := 0; i < n; i++ {
		f := facts("1.0.0")
		f.ClientID = fmt.Sprintf("client-%d", i)
		_, first := Decide(f, cfg)
		_, second := Decide(f, cfg)
		if first != second {
			t.Fatalf("client %s flipped between checks", f.ClientID)
		}
		if first {
			offered++
		}
	}
	pct := 100 * offered / n
	if pct < 22 || pct > 28 {
		t.Fatalf("25%% rollout offered to %d%% of clients", pct)
	}
}

// A client that was not in the 25% for one version should have a fresh
// chance for the next version, so the hash includes the version.
func TestBucketChangesWithVersion(t *testing.T) {
	same := 0
	for i := 0; i < 1000; i++ {
		f := facts("1.0.0")
		f.ClientID = fmt.Sprintf("client-%d", i)
		_, a := Decide(f, stableAt("1.1.0", 50))
		_, b := Decide(f, stableAt("1.2.0", 50))
		if a == b {
			same++
		}
	}
	if same > 650 {
		t.Fatalf("buckets for two versions agreed %d/1000 times; version is not in the hash", same)
	}
}
