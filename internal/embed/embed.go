// Package embed loads a static word-embedding table (GloVe text format) and
// provides the vector arithmetic for Mitigation A: cloze substitution and
// use-vector variance. Static embeddings are crude per sentence, but the
// noise cancels averaged over a dozen real uses — and they're local and
// deterministic, which is the whole point.
package embed

import (
	"bufio"
	"math"
	"os"
	"strconv"
	"strings"
)

// Table maps words to unit-normalized vectors.
type Table struct {
	dim  int
	vecs map[string][]float32
}

// Load reads a GloVe-format text file: one "word v1 v2 ... vd" per line.
// vocab, when non-nil, restricts loading to listed words (plus nothing
// else) — pass the corpus + candidate vocabulary to cut memory and load
// time by ~10x.
func Load(path string, vocab map[string]bool) (*Table, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	t := &Table{vecs: map[string][]float32{}}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<16), 1<<16)
	for sc.Scan() {
		line := sc.Text()
		i := strings.IndexByte(line, ' ')
		if i <= 0 {
			continue
		}
		word := line[:i]
		if vocab != nil && !vocab[word] {
			continue
		}
		fields := strings.Fields(line[i+1:])
		if t.dim == 0 {
			t.dim = len(fields)
		}
		if len(fields) != t.dim {
			continue
		}
		v := make([]float32, t.dim)
		var norm float64
		ok := true
		for j, fld := range fields {
			x, err := strconv.ParseFloat(fld, 32)
			if err != nil {
				ok = false // drop the whole word: a zeroed component would silently skew every cosine
				break
			}
			v[j] = float32(x)
			norm += x * x
		}
		if !ok || norm == 0 {
			continue
		}
		n := float32(math.Sqrt(norm))
		for j := range v {
			v[j] /= n
		}
		t.vecs[word] = v
	}
	return t, sc.Err()
}

// Has reports whether word is in the table.
func (t *Table) Has(word string) bool { _, ok := t.vecs[word]; return ok }

// Mean returns the unit-normalized mean of the words' vectors, skipping
// out-of-vocabulary words; nil when nothing was in vocabulary.
func (t *Table) Mean(words []string) []float32 {
	sum := make([]float64, t.dim)
	n := 0
	for _, w := range words {
		v := t.vecs[w]
		if v == nil {
			continue
		}
		for j, x := range v {
			sum[j] += float64(x)
		}
		n++
	}
	if n == 0 {
		return nil
	}
	var norm float64
	for _, x := range sum {
		norm += x * x
	}
	if norm == 0 {
		return nil
	}
	out := make([]float32, t.dim)
	scale := 1 / math.Sqrt(norm)
	for j, x := range sum {
		out[j] = float32(x * scale)
	}
	return out
}

// Cos is the cosine similarity of two unit vectors (a plain dot product).
func Cos(a, b []float32) float64 {
	var dot float64
	for j := range a {
		dot += float64(a[j]) * float64(b[j])
	}
	return dot
}
