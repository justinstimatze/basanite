package wordnet

import (
	"os"
	"path/filepath"
	"testing"
)

// loadTestDB loads the real data assets, skipping when they aren't present
// (they're gitignored — see README data setup).
func loadTestDB(t *testing.T) *DB {
	t.Helper()
	dict := filepath.Join("..", "..", "data", "dict")
	if _, err := os.Stat(dict); err != nil {
		t.Skip("wordnet data not present")
	}
	ic := filepath.Join("..", "..", "data", "wordnet_ic", "ic-semcor.dat")
	if _, err := os.Stat(ic); err != nil {
		ic = ""
	}
	db, err := Load(dict, ic)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func TestLookupAndLadder(t *testing.T) {
	db := loadTestDB(t)

	if got := db.Lookup("spine", 'n'); len(got) != 5 {
		t.Errorf("spine noun senses = %d, want 5", len(got))
	}
	syns := db.Lookup("load-bearing", 'a')
	if len(syns) != 1 {
		t.Fatalf("load-bearing adj senses = %d, want 1", len(syns))
	}
	// adjective marker (a) must be stripped from words
	for _, w := range syns[0].Words {
		if w == "load-bearing(a)" {
			t.Error("adjective marker not stripped")
		}
	}

	ladders := db.Ladders("spine")
	if len(ladders) == 0 {
		t.Fatal("no ladders for spine")
	}
	l := ladders[0]
	// rungs must be sorted weakest -> strongest
	for i := 1; i < len(l.Rungs); i++ {
		if l.Rungs[i].IC < l.Rungs[i-1].IC {
			t.Errorf("rungs out of order at %d: %v", i, l.Rungs)
		}
	}
	// the most-common sense ladder should contain the obvious synonym
	found := false
	for _, r := range l.Rungs {
		if r.Word == "backbone" {
			found = true
		}
	}
	if !found {
		t.Error("backbone missing from spine's first-sense ladder")
	}
}

func TestICLoaded(t *testing.T) {
	db := loadTestDB(t)
	if len(db.ic) == 0 {
		t.Skip("IC table not present")
	}
	// entity (root noun synset, offset 00001740) must have IC ~0
	ic, ok := db.SynsetIC("00001740n")
	if !ok {
		t.Fatal("root noun synset missing from IC table")
	}
	if ic > 0.1 {
		t.Errorf("root IC = %f, want ~0", ic)
	}
}
