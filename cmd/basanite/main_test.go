package main

import "testing"

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
