package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestValidSessionID(t *testing.T) {
	valid := []string{"01dadadd-cc24-4eb5-bbaa-1ece1e141b86", "abc_DEF-123"}
	invalid := []string{
		"", "../../../../tmp/evil", "a/b", `a\b`, "id with space",
		"id\nnewline", "ünïcode", string(make([]byte, 200)),
	}
	for _, id := range valid {
		if !validSessionID(id) {
			t.Errorf("validSessionID(%q) = false, want true", id)
		}
	}
	for _, id := range invalid {
		if validSessionID(id) {
			t.Errorf("validSessionID(%q) = true, want false", id)
		}
	}
}

// The hook must never return an error or exit on bad input: a non-nil
// return becomes exit 1, and (worse) flag.ExitOnError would exit 2, which
// blocks the user's prompt in Claude Code.
func TestRunHookNeverFails(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	cases := [][]string{
		{"-bogus-flag"},
		{"-max-age", "7d"}, // ParseDuration rejects "d" — the realistic typo
		{"-report", "/nonexistent/report.json"},
		{},
	}
	for _, args := range cases {
		if err := runHook(args); err != nil {
			t.Errorf("runHook(%v) = %v, want nil", args, err)
		}
	}
}

// claimInjection injects on the first prompt, stays quiet within the
// interval, and re-injects once an earlier stamp has aged out — the property
// that lets awareness resurface in a long or compacted session.
func TestClaimInjectionTimeBox(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "injected-sess")
	if !claimInjection(marker, time.Hour) {
		t.Fatal("first claim (absent marker) must inject")
	}
	if claimInjection(marker, time.Hour) {
		t.Error("second claim within the interval must stay silent")
	}
	// backdate the stamp past the interval: the session has run long enough
	// that the injected context is gone
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(marker, old, old); err != nil {
		t.Fatal(err)
	}
	if !claimInjection(marker, time.Hour) {
		t.Error("claim after the interval must re-inject")
	}
	if claimInjection(marker, time.Hour) {
		t.Error("the re-inject must have reset the clock, silencing the next claim")
	}
}

// The stale breadcrumb names the age and surfaces the last refresh error, so
// a silently-failing background refresh becomes visible instead of dark.
func TestStaleNoteSurfacesRefreshError(t *testing.T) {
	dir := t.TempDir()
	log := "2026-07-01T00:00:00-07:00 ok: 8 entries\n" +
		"2026-07-07T00:00:00-07:00 error: open dict/data.noun: no such file or directory\n"
	if err := os.WriteFile(filepath.Join(dir, "refresh.log"), []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := lastRefreshError(dir); !strings.HasPrefix(got, "error: open dict/data.noun") {
		t.Errorf("lastRefreshError picked the wrong line: %q", got)
	}
	note := staleNote(time.Now().Add(-21*24*time.Hour), dir)
	if !strings.Contains(note, "21d stale") {
		t.Errorf("stale note missing the age: %q", note)
	}
	if !strings.Contains(note, "data.noun") {
		t.Errorf("stale note missing the last refresh error: %q", note)
	}
	// no log at all: the note still renders, just without the error clause
	if lastRefreshError(t.TempDir()) != "" {
		t.Error("a missing refresh.log must yield an empty error, not a fabricated one")
	}
}
