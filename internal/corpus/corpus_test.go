package corpus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const sampleJSONL = `{"type":"last-prompt","sessionId":"s1"}
{"type":"assistant","isSidechain":false,"timestamp":"2026-06-09T10:00:00.000Z","message":{"content":[{"type":"text","text":"Main prose turn."},{"type":"thinking","thinking":"hidden"},{"type":"tool_use","name":"Bash"}]}}
{"type":"assistant","isSidechain":true,"timestamp":"2026-06-09T10:01:00.000Z","message":{"content":[{"type":"text","text":"Sidechain turn, must be skipped."}]}}
{"type":"user","timestamp":"2026-06-09T10:02:00.000Z","message":{"content":[{"type":"text","text":"User turn, must be skipped."}]}}
{"type":"assistant","isSidechain":false,"timestamp":"2026-06-01T10:00:00.000Z","message":{"content":[{"type":"text","text":"Too old, must be skipped."}]}}
{"type":"assistant","isSidechain":false,"timestamp":"2026-06-09T10:03:00.000Z","message":{"content":[{"type":"text","text":"Second prose turn."}]}}
not json at all
`

func TestRead(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "-home-user-someproject")
	sub := filepath.Join(proj, "subagents")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(proj, "session.jsonl"), sampleJSONL)
	write(filepath.Join(sub, "agent-x.jsonl"),
		`{"type":"assistant","isSidechain":false,"timestamp":"2026-06-09T10:00:00.000Z","message":{"content":[{"type":"text","text":"Subagent dir, must be skipped."}]}}`)
	write(filepath.Join(proj, "notes.txt"), "not a transcript")

	since := time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC)
	turns, err := Read(root, since)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 2 {
		t.Fatalf("got %d turns, want 2: %+v", len(turns), turns)
	}
	for _, turn := range turns {
		if turn.Project != "-home-user-someproject" {
			t.Errorf("Project = %q", turn.Project)
		}
		if turn.Time.Before(since) {
			t.Errorf("turn before since: %v", turn.Time)
		}
	}
	if turns[0].Text != "Main prose turn." {
		t.Errorf("text blocks mis-extracted: %q", turns[0].Text)
	}
}

func TestReadMissingRootIsError(t *testing.T) {
	if _, err := Read(filepath.Join(t.TempDir(), "nope"), time.Time{}); err == nil {
		t.Error("a missing root must error — a typo'd -dir reading as a quiet week is the worst failure shape")
	}
}

func TestReadSkipsOversizedLines(t *testing.T) {
	root := t.TempDir()
	huge := `{"type":"assistant","isSidechain":false,"timestamp":"2026-06-09T10:00:00.000Z","message":{"content":[{"type":"text","text":"` +
		strings.Repeat("x", maxLineBytes+1024) +
		`"}]}}` + "\n" +
		`{"type":"assistant","isSidechain":false,"timestamp":"2026-06-09T10:01:00.000Z","message":{"content":[{"type":"text","text":"Normal turn after the blob."}]}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "s.jsonl"), []byte(huge), 0o644); err != nil {
		t.Fatal(err)
	}
	turns, err := Read(root, time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 1 || turns[0].Text != "Normal turn after the blob." {
		t.Fatalf("oversized line handling broken: %d turns", len(turns))
	}
}

func TestReadSkipsOldFilesByMtime(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "old.jsonl")
	if err := os.WriteFile(path, []byte(sampleJSONL), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().AddDate(0, 0, -60)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	turns, err := Read(root, time.Now().AddDate(0, 0, -30))
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 0 {
		t.Errorf("mtime-pruned file was parsed anyway: %d turns", len(turns))
	}
}
