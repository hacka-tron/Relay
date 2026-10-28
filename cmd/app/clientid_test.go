package main

import (
	"os"
	"regexp"
	"testing"
)

func TestClientIDIsStableAcrossRuns(t *testing.T) {
	// Point the config dir at a temp dir so the test never touches the
	// real one. UserConfigDir reads XDG_CONFIG_HOME on Linux, HOME on
	// macOS and AppData on Windows; set all three.
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)
	t.Setenv("AppData", dir)

	first, err := clientID()
	if err != nil {
		t.Fatal(err)
	}
	second, err := clientID()
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("id changed between runs: %s vs %s", first, second)
	}
	uuid := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	if !uuid.MatchString(first) {
		t.Fatalf("not a v4 uuid: %s", first)
	}
	if entries, _ := os.ReadDir(dir); len(entries) == 0 {
		t.Fatal("nothing written under the config dir")
	}
}
