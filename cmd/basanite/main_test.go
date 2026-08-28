package main

import (
	"encoding/json"
	"io"
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

// A compaction erases the injected block outright, so the marker's clock is
// measuring the wrong thing from that moment. Clearing it lets the existing
// UserPromptSubmit path re-inject on the next prompt instead of waiting out
// the remainder of a four-hour interval that started before the wipe.
func TestCompactedSessionClearsTheMarker(t *testing.T) {
	for name, tc := range map[string]struct {
		payload string
		want    string
	}{
		"compact":       {`{"session_id":"sess-1","source":"compact"}`, "sess-1"},
		"startup":       {`{"session_id":"sess-1","source":"startup"}`, ""},
		"resume":        {`{"session_id":"sess-1","source":"resume"}`, ""},
		"no source":     {`{"session_id":"sess-1"}`, ""},
		"bad id":        {`{"session_id":"../evil","source":"compact"}`, ""},
		"empty id":      {`{"source":"compact"}`, ""},
		"garbage":       {`not json`, ""},
		"empty payload": {``, ""},
	} {
		f := filepath.Join(t.TempDir(), "stdin")
		if err := os.WriteFile(f, []byte(tc.payload), 0o600); err != nil {
			t.Fatal(err)
		}
		fh, err := os.Open(f)
		if err != nil {
			t.Fatal(err)
		}
		got := compactedSession(fh)
		fh.Close()
		if got != tc.want {
			t.Errorf("%s: compactedSession = %q, want %q", name, got, tc.want)
		}
	}
}

// `refresh` is a SessionStart hook AND a command someone types. Reading stdin
// unconditionally would hang it at a terminal, waiting for JSON nobody types.
func TestCompactedSessionIgnoresATerminal(t *testing.T) {
	tty, err := os.Open("/dev/tty")
	if err != nil {
		t.Skip("no controlling terminal in this environment")
	}
	defer tty.Close()
	if got := compactedSession(tty); got != "" {
		t.Errorf("compactedSession(tty) = %q, want empty", got)
	}
}

func TestRunWritecheckNeverFails(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	for _, args := range [][]string{
		{"-bogus-flag"}, {"-max-age", "7d"}, {"-report", "/nonexistent/r.json"}, {},
	} {
		if err := runWritecheck(args); err != nil {
			t.Errorf("runWritecheck(%v) = %v, want nil", args, err)
		}
	}
}

// Write/Edit send file_path+content/new_string; mcp__linear__save_comment sends body,
// mcp__linear__save_issue sends description on a full-content update. Content and new_string
// take precedence so a Write/Edit call is never reinterpreted through Linear's fields.
func TestExtractWritecheckText(t *testing.T) {
	build := func(filePath, content, newString, body, description string) writecheckInput {
		var in writecheckInput
		in.ToolInput.FilePath = filePath
		in.ToolInput.Content = content
		in.ToolInput.NewString = newString
		in.ToolInput.Body = body
		in.ToolInput.Description = description
		return in
	}
	for _, tc := range []struct {
		name      string
		in        writecheckInput
		wantText  string
		wantLabel string
	}{
		{"write", build("/a/app.go", "package main", "", "", ""), "package main", "app.go"},
		{"edit", build("/a/app.go", "", "func f() {}", "", ""), "func f() {}", "app.go"},
		{"linear comment", build("", "", "", "this is load-bearing", ""), "this is load-bearing", "Linear"},
		{"linear issue description", build("", "", "", "", "full ticket body"), "full ticket body", "Linear"},
		{"content wins over body when both present", build("/a/app.go", "file wins", "", "comment loses", ""), "file wins", "app.go"},
		{"empty", build("", "", "", "", ""), "", "Linear"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gotText, gotLabel := extractWritecheckText(tc.in)
			if gotText != tc.wantText || gotLabel != tc.wantLabel {
				t.Errorf("extractWritecheckText(%+v) = (%q, %q), want (%q, %q)",
					tc.in, gotText, gotLabel, tc.wantText, tc.wantLabel)
			}
		})
	}
}

// Once per word per session: the curated list carries short entries that
// collide with ordinary identifiers, so a wrong match must cost one line.
func TestSeenWordsRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "written-sess")
	if got := loadSeenWords(p); len(got) != 0 {
		t.Fatalf("absent file should read empty, got %v", got)
	}
	saveSeenWords(p, map[string]bool{}, []string{"substrate", "texture"})
	seen := loadSeenWords(p)
	if !seen["substrate"] || !seen["texture"] || len(seen) != 2 {
		t.Fatalf("round-trip lost words: %v", seen)
	}
	saveSeenWords(p, seen, []string{"calibration"})
	if seen = loadSeenWords(p); len(seen) != 3 || !seen["calibration"] {
		t.Errorf("append lost prior words: %v", seen)
	}
}

// A Write/Edit call always carries file_path; none of the five Linear write
// tools do. writecheckExternal reads that same signal extractWritecheckText
// already uses for the label, rather than string-matching the "Linear" label
// itself, so the two can't drift apart.
func TestWritecheckExternal(t *testing.T) {
	var local, external writecheckInput
	local.ToolInput.FilePath = "/a/app.go"
	local.ToolInput.NewString = "func f() {}"
	external.ToolInput.Body = "this is load-bearing"
	if writecheckExternal(local) {
		t.Error("a Write/Edit call (file_path set) should not read as external")
	}
	if !writecheckExternal(external) {
		t.Error("a Linear call (no file_path) should read as external")
	}
}

func TestWritecheckSeenNameSeparatesClasses(t *testing.T) {
	local := writecheckSeenName("sess", false)
	external := writecheckSeenName("sess", true)
	if local == external {
		t.Fatalf("local and external dedup files must differ, both got %q", local)
	}
	if local != "written-sess" {
		t.Errorf("local seen name = %q, want %q", local, "written-sess")
	}
	if external != "written-external-sess" {
		t.Errorf("external seen name = %q, want %q", external, "written-external-sess")
	}
}

// runWritecheckCapture feeds stdinJSON to runWritecheck through a swapped
// os.Stdin and returns whatever it wrote to a swapped os.Stdout.
// runWritecheck reads/writes the real os.Stdin/os.Stdout directly (same as
// runHook and runDisplay), so this is the only way to exercise it end to end.
func runWritecheckCapture(t *testing.T, args []string, stdinJSON string) string {
	t.Helper()
	inR, inW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		inW.WriteString(stdinJSON)
		inW.Close()
	}()
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldIn, oldOut := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = inR, outW
	err = runWritecheck(args)
	os.Stdin, os.Stdout = oldIn, oldOut
	outW.Close()
	inR.Close()
	if err != nil {
		t.Fatalf("runWritecheck(%v) = %v, want nil", args, err)
	}
	out, err := io.ReadAll(outR)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// TestRunWritecheckDedupesPerDestinationClass is the regression test for the
// incident that motivated writecheckExternal: "load-bearing" was flagged once
// in a local scratch write early in a session, then reached a posted Linear
// comment 40+ turns later with zero fresh warning, because a single seen-set
// shared across every matched tool call had already spent the session's one
// flag on the local hit. Local and external must dedupe independently, and
// each must still dedupe once within its own class — the noise reduction the
// "once per session" design exists for in the first place.
func TestRunWritecheckDedupesPerDestinationClass(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	rep := &report.Report{
		GeneratedAt: time.Now(),
		Entries: []report.Entry{
			{Lemma: "load-bearing", Known: true, DemoteTo: "supporting"},
		},
	}
	path, err := report.Path()
	if err != nil {
		t.Fatal(err)
	}
	if err := rep.Save(path); err != nil {
		t.Fatal(err)
	}

	const sessID = "sess-dedup-test"
	toJSON := func(in writecheckInput) string {
		in.SessionID = sessID
		b, err := json.Marshal(in)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	localWrite := func(content string) writecheckInput {
		var in writecheckInput
		in.ToolInput.FilePath = "/tmp/notes.md"
		in.ToolInput.Content = content
		return in
	}
	linearComment := func(body string) writecheckInput {
		var in writecheckInput
		in.ToolInput.Body = body
		return in
	}

	if out := runWritecheckCapture(t, nil, toJSON(localWrite("this fix is load-bearing"))); !strings.Contains(out, "load-bearing") {
		t.Fatalf("first local write should flag load-bearing, got %q", out)
	}
	if out := runWritecheckCapture(t, nil, toJSON(localWrite("still load-bearing here"))); strings.Contains(out, "load-bearing") {
		t.Fatalf("second local write should be suppressed by local dedup, got %q", out)
	}
	if out := runWritecheckCapture(t, nil, toJSON(linearComment("shipping this load-bearing comment"))); !strings.Contains(out, "load-bearing") {
		t.Fatalf("first external write must flag despite the earlier local flag, got %q", out)
	}
	if out := runWritecheckCapture(t, nil, toJSON(linearComment("another load-bearing comment"))); strings.Contains(out, "load-bearing") {
		t.Fatalf("second external write should be suppressed by external dedup, got %q", out)
	}
}

// TestRunWritecheckNoDedup covers the -no-dedup flag added for a second,
// independent caller (e.g. another PreToolUse hook forwarding the same event
// to fold this verdict into its own gate decision) that would otherwise race
// the registered hook's seen-set file for that same event: whichever runs
// second would find every word already marked seen and report nothing.
// -no-dedup must flag every call regardless of prior calls in the same
// session, and must not touch the seen-set files at all.
func TestRunWritecheckNoDedup(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateDir)

	rep := &report.Report{
		GeneratedAt: time.Now(),
		Entries: []report.Entry{
			{Lemma: "load-bearing", Known: true, DemoteTo: "supporting"},
		},
	}
	path, err := report.Path()
	if err != nil {
		t.Fatal(err)
	}
	if err := rep.Save(path); err != nil {
		t.Fatal(err)
	}

	toJSON := func(in writecheckInput) string {
		in.SessionID = "sess-no-dedup-test"
		in.ToolInput.Body = "this comment is load-bearing"
		b, err := json.Marshal(in)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	var in writecheckInput
	stdin := toJSON(in)

	for i := 0; i < 2; i++ {
		out := runWritecheckCapture(t, []string{"-no-dedup"}, stdin)
		if !strings.Contains(out, "load-bearing") {
			t.Fatalf("call %d: -no-dedup should flag load-bearing every time, got %q", i, out)
		}
		if !strings.Contains(out, "no session dedup") {
			t.Errorf("call %d: -no-dedup message should say so, got %q", i, out)
		}
	}

	entries, err := os.ReadDir(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	var found []string
	filepath.WalkDir(stateDir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasPrefix(d.Name(), "written-") {
			found = append(found, p)
		}
		return nil
	})
	if len(found) != 0 {
		t.Errorf("-no-dedup should touch no seen-set file, found %v (state dir entries: %v)", found, entries)
	}
}

// pruneMarkers used to sweep only "injected-" and "display-" markers, so
// "written-" (and, after writecheckSeenName split it in two,
// "written-external-") accumulated forever.
func TestPruneMarkersSweepsWritecheckSeenFiles(t *testing.T) {
	dir := t.TempDir()
	old := time.Now().AddDate(0, 0, -31)
	touch := func(name string, mtime time.Time) {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
	touch("written-sess1", old)
	touch("written-external-sess1", old)
	touch("injected-sess1", old)
	touch("display-sess1.json", old)
	touch("written-sess2", time.Now()) // fresh: must survive
	touch("report.json", old)          // not a session marker: must survive regardless of age

	pruneMarkers(dir)

	for _, gone := range []string{"written-sess1", "written-external-sess1", "injected-sess1", "display-sess1.json"} {
		if _, err := os.Stat(filepath.Join(dir, gone)); !os.IsNotExist(err) {
			t.Errorf("%s should have been pruned, stat err = %v", gone, err)
		}
	}
	for _, stay := range []string{"written-sess2", "report.json"} {
		if _, err := os.Stat(filepath.Join(dir, stay)); err != nil {
			t.Errorf("%s should have survived, stat err = %v", stay, err)
		}
	}
}
