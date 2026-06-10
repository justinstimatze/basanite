package wordnet

import (
	"path/filepath"
	"testing"
)

// The testdata fixture is synthetic and committed, so unlike the real-data
// tests in wordnet_test.go these always run — including in CI, where the
// gitignored data assets are absent. The fixture deliberately includes
// malformed lines (truncated word list, oversized and negative pointer
// counts) that previously panicked the parser.
func loadHermetic(t *testing.T) *DB {
	t.Helper()
	db, err := Load(filepath.Join("testdata", "dict"), filepath.Join("testdata", "ic-test.dat"))
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func TestHermeticParse(t *testing.T) {
	db := loadHermetic(t)

	dog := db.Lookup("dog", 'n')
	if len(dog) != 1 {
		t.Fatalf("dog senses = %d, want 1", len(dog))
	}
	if len(dog[0].Words) != 2 || dog[0].Words[1] != "domestic dog" {
		t.Errorf("dog synset words = %v", dog[0].Words)
	}
	if len(dog[0].Hypernyms) != 1 || dog[0].Hypernyms[0] != "00001740n" {
		t.Errorf("dog hypernyms = %v", dog[0].Hypernyms)
	}

	// malformed data.noun lines must be skipped, not panic and not load
	for _, key := range []string{"00003000n", "00004000n", "00005000n"} {
		if db.Get(key) != nil {
			t.Errorf("malformed synset %s should have been skipped", key)
		}
	}
	// malformed index.noun lines (oversized and negative pointer counts)
	for _, lemma := range []string{"bad", "neg"} {
		if got := db.Lookup(lemma, 'n'); got != nil {
			t.Errorf("malformed index entry %q should have been skipped, got %v", lemma, got)
		}
	}

	// satellite adjective: marker stripped, similar-to pointer normalized to 'a'
	lb := db.Lookup("load-bearing", 'a')
	if len(lb) != 1 || lb[0].Words[0] != "load-bearing" {
		t.Fatalf("load-bearing lookup = %+v", lb)
	}
	if len(lb[0].Similar) != 1 || lb[0].Similar[0] != "00217297a" {
		t.Errorf("similar pointers = %v", lb[0].Similar)
	}
}

func TestHermeticIC(t *testing.T) {
	db := loadHermetic(t)
	root, ok := db.SynsetIC("00001740n")
	if !ok || root > 0.001 {
		t.Errorf("root IC = %f, %v; want ~0", root, ok)
	}
	dog, ok := db.SynsetIC("00002084n")
	if !ok || dog < 2.2 || dog > 2.4 {
		t.Errorf("dog IC = %f, %v; want ~ln(10)=2.3", dog, ok)
	}
}

func TestHermeticLadder(t *testing.T) {
	db := loadHermetic(t)
	ladders := db.Ladders("dog")
	if len(ladders) != 1 {
		t.Fatalf("dog ladders = %d, want 1", len(ladders))
	}
	rungs := ladders[0].Rungs
	if rungs[0].Word != "entity" || rungs[0].Source != "hypernym" {
		t.Errorf("weakest rung should be the hypernym: %+v", rungs)
	}
	if rungs[len(rungs)-1].IC < rungs[0].IC {
		t.Errorf("rungs out of order: %+v", rungs)
	}
}
