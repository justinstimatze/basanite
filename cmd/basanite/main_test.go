package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/justinstimatze/basanite/internal/report"
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

// The rule that used to be the only one — age — cannot see the two changes
// most likely to matter: you edited the list, or you upgraded the tool. Both
// leave GeneratedAt exactly where it was.
func TestStaleReasonSeesInputChangesNotOnlyAge(t *testing.T) {
	const maxAge = 6 * 24 * time.Hour
	fresh := func() *report.Report {
		return &report.Report{
			GeneratedAt:  time.Now().Add(-time.Hour),
			Version:      buildVersion(),
			ListModified: listModTime(),
		}
	}
	if why := staleReason(fresh(), maxAge); why != "" {
		t.Errorf("a matching report is not stale, got %q", why)
	}
	if staleReason(nil, maxAge) == "" {
		t.Error("no report at all is the most stale a report can be")
	}

	old := fresh()
	old.GeneratedAt = time.Now().Add(-7 * 24 * time.Hour)
	if staleReason(old, maxAge) == "" {
		t.Error("the clock rule must still fire")
	}

	upgraded := fresh()
	upgraded.Version = "v0.0.1-something-else"
	if why := staleReason(upgraded, maxAge); why == "" {
		t.Error("a report built by another version is stale however recent")
	} else if !strings.Contains(why, "v0.0.1-something-else") {
		t.Errorf("the reason should name the version it was built by, got %q", why)
	}

	// Stub the list, so this asserts the rule and not whether this machine
	// happens to have a known-tics file.
	defer func(orig func() time.Time) { listModTime = orig }(listModTime)
	listed := time.Now().Add(-time.Hour).Truncate(time.Second)
	listModTime = func() time.Time { return listed }

	matching := fresh()
	matching.ListModified = listed
	if why := staleReason(matching, maxAge); why != "" {
		t.Errorf("a report built against this list is not stale, got %q", why)
	}
	edited := fresh()
	edited.ListModified = listed.Add(-30 * 24 * time.Hour)
	if staleReason(edited, maxAge) == "" {
		t.Error("a report built against an older list is stale")
	}

	// No list on this machine is not evidence the list changed.
	listModTime = func() time.Time { return time.Time{} }
	unlisted := fresh()
	unlisted.ListModified = time.Now().Add(-30 * 24 * time.Hour)
	if why := staleReason(unlisted, maxAge); why != "" {
		t.Errorf("an unreadable list must not force a rebuild, got %q", why)
	}
}

// Two binaries alternating at one path would otherwise rebuild every prompt:
// the version never matches, and unlike age that condition never resolves.
func TestStaleReasonHoldsOffOnAJustBuiltReport(t *testing.T) {
	justBuilt := &report.Report{GeneratedAt: time.Now(), Version: "some-other-build"}
	if why := staleReason(justBuilt, 6*24*time.Hour); why != "" {
		t.Errorf("an input change must not retrigger inside the interval, got %q", why)
	}
	older := &report.Report{
		GeneratedAt: time.Now().Add(-minRefreshInterval - time.Minute),
		Version:     "some-other-build",
	}
	if staleReason(older, 6*24*time.Hour) == "" {
		t.Error("past the interval the input change must fire")
	}
}

// Checking every prompt is only safe because failures back off. A refresh
// that fails leaves the report exactly as stale as it found it, and the
// report's own timestamp cannot express "tried and could not" — so without
// this, a broken pipeline starts a fresh attempt on every prompt for as long
// as it stays broken.
func TestSpawnRefreshBacksOffOnRecentAttempt(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, refreshLogName)

	// A just-written log means an attempt just happened, outcome irrelevant.
	if err := os.WriteFile(log, []byte("error: no data dir\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !refreshBackedOff(dir) {
		t.Error("an attempt inside the interval must not start another")
	}

	old := time.Now().Add(-minRefreshInterval - time.Minute)
	if err := os.Chtimes(log, old, old); err != nil {
		t.Fatal(err)
	}
	if refreshBackedOff(dir) {
		t.Error("past the interval the next attempt must be allowed")
	}
	if refreshBackedOff(t.TempDir()) {
		t.Error("no log at all means nothing has been tried yet")
	}
}
