package knowntics

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuiltinSplitsWordsAndPhrases(t *testing.T) {
	s := Load()
	if !s.Words["substrate"] {
		t.Error("expected the single word 'substrate' in the builtin reference")
	}
	if len(s.Phrases) == 0 {
		t.Fatal("builtin reference has no phrases")
	}
	found := false
	for _, p := range s.Phrases {
		if p == "i want to honor that" {
			found = true
		}
		// a phrase must keep its interior space; a single word must not leak in
		if len(p) > 0 && !containsSpace(p) {
			t.Errorf("phrase %q has no space — single words must not be parsed as phrases", p)
		}
	}
	if !found {
		t.Error("expected the phrase 'i want to honor that' in the builtin reference")
	}
}

func TestUserListExtendsAndDedups(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "known-tics.txt")
	body := "# my own\nscaffolding\nmy stock phrase\ni want to honor that\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	base := Load()
	ext := Load(path, filepath.Join(dir, "absent.txt"))

	if !ext.Words["scaffolding"] {
		t.Error("user single word not merged")
	}
	if countPhrase(ext.Phrases, "my stock phrase") != 1 {
		t.Error("user phrase not merged")
	}
	// the duplicate phrase from the user list must not double up
	if countPhrase(ext.Phrases, "i want to honor that") != 1 {
		t.Error("duplicate phrase across builtin and user list was not deduped")
	}
	if len(ext.Phrases) != len(base.Phrases)+1 {
		t.Errorf("user list added %d phrases, want exactly 1 new", len(ext.Phrases)-len(base.Phrases))
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
