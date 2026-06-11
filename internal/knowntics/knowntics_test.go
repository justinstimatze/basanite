package knowntics

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSeedOnFirstRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "known-tics.txt")
	s, seeded := Load(path)
	if !seeded {
		t.Error("first Load of a missing path should report a seed")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("seed file was not written: %v", err)
	}
	if !s.Words["substrate"] {
		t.Error("seeded set is missing the 'substrate' single word")
	}
	if countPhrase(s.Phrases, "i want to honor that") != 1 {
		t.Error("seeded set is missing a signature phrase")
	}
	// a phrase must keep its interior space; a single word must not leak in
	for _, p := range s.Phrases {
		if !containsSpace(p) {
			t.Errorf("phrase %q has no space — single words must not parse as phrases", p)
		}
	}
}

// The whole point of the single-list model: the user owns the file, so their
// edits stand. A second Load must read their copy, never re-merge the seed —
// a word they deleted stays deleted.
func TestUserEditsStandNoReSeed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "known-tics.txt")
	if _, seeded := Load(path); !seeded {
		t.Fatal("precondition: first Load should seed")
	}

	// the user curates: a custom word, a custom phrase, and substrate removed
	body := "# mine\nscaffolding\nmy stock phrase\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	s, seeded := Load(path)
	if seeded {
		t.Error("an existing file must not be re-seeded")
	}
	if !s.Words["scaffolding"] {
		t.Error("user's own word not read")
	}
	if s.Words["substrate"] {
		t.Error("a deleted seed word must stay gone — the seed must not re-merge")
	}
	if countPhrase(s.Phrases, "my stock phrase") != 1 || countPhrase(s.Phrases, "i want to honor that") != 0 {
		t.Errorf("Load must read only the user's file, got phrases %v", s.Phrases)
	}
}

// With no home dir / blank path, the feature degrades to the in-memory seed
// rather than disappearing.
func TestBlankPathFallsBackToSeed(t *testing.T) {
	s, seeded := Load("")
	if seeded {
		t.Error("a blank path writes nothing, so it cannot report a seed")
	}
	if !s.Words["substrate"] {
		t.Error("blank-path fallback should still parse the embedded seed")
	}
}

func containsSpace(s string) bool {
	for _, r := range s {
		if r == ' ' {
			return true
		}
	}
	return false
}

func countPhrase(ps []string, want string) int {
	n := 0
	for _, p := range ps {
		if p == want {
			n++
		}
	}
	return n
}
