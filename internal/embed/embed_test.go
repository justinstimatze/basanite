package embed

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func load(t *testing.T, content string, vocab map[string]bool) *Table {
	t.Helper()
	path := filepath.Join(t.TempDir(), "vecs.txt")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	tbl, err := Load(path, vocab)
	if err != nil {
		t.Fatal(err)
	}
	return tbl
}

func TestLoadFiltersAndSkips(t *testing.T) {
	content := "good 1.0 0.0 0.0\n" +
		"unwanted 0.0 1.0 0.0\n" +
		"short 1.0 0.0\n" + // dim mismatch: skipped
		"garbled 1.0 abc 0.0\n" + // malformed float: whole word dropped
		"zero 0.0 0.0 0.0\n" // zero norm: skipped
	tbl := load(t, content, map[string]bool{"good": true, "short": true, "garbled": true, "zero": true})

	if !tbl.Has("good") {
		t.Error("good should load")
	}
	for _, w := range []string{"unwanted", "short", "garbled", "zero"} {
		if tbl.Has(w) {
			t.Errorf("%s should not load (filtered or malformed)", w)
		}
	}
}

func TestVectorsAreUnitNorm(t *testing.T) {
	tbl := load(t, "big 3.0 4.0 0.0\n", nil)
	v := tbl.Mean([]string{"big"})
	var norm float64
	for _, x := range v {
		norm += float64(x) * float64(x)
	}
	if math.Abs(norm-1) > 1e-6 {
		t.Errorf("norm² = %f, want 1", norm)
	}
}

func TestMeanAndCos(t *testing.T) {
	tbl := load(t, "x 1.0 0.0\ny 0.0 1.0\n", nil)
	if tbl.Mean([]string{"oov", "missing"}) != nil {
		t.Error("Mean of all-OOV words must be nil")
	}
	if tbl.Mean(nil) != nil {
		t.Error("Mean of nothing must be nil")
	}
	// OOV words are skipped, not zero-averaged
	withOOV := tbl.Mean([]string{"x", "oov"})
	xOnly := tbl.Mean([]string{"x"})
	if Cos(withOOV, xOnly) < 0.999 {
		t.Errorf("OOV word changed the mean: cos = %f", Cos(withOOV, xOnly))
	}
	if c := Cos(tbl.Mean([]string{"x"}), tbl.Mean([]string{"y"})); math.Abs(c) > 1e-6 {
		t.Errorf("orthogonal vectors: cos = %f, want 0", c)
	}
}
