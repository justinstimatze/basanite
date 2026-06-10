package wordnet

import "sort"

// Rung is one candidate word on a specificity ladder.
type Rung struct {
	Word   string
	Source string  // "self", "synonym", "hypernym", "hypernym2", "similar"
	IC     float64 // Resnik synset IC, or word-frequency IC where no table entry exists
}

// SenseLadder is the ladder for one sense of the target word, rungs ordered
// weakest -> strongest. Synonyms aren't interchangeable, they're a cline of
// strength: the point of the ordering is that the right move is often
// *demoting* to the weaker rung that's actually true, not swapping sideways.
type SenseLadder struct {
	Synset *Synset
	Rungs  []Rung
}

// Ladders builds one ladder per sense of lemma across all POS, in WordNet
// sense order (most common sense first).
//
// Rung sources: same-synset synonyms, direct + second-level hypernyms (the
// demote direction), and similar-to clusters for adjectives. Ordering uses
// Resnik synset IC where the IC table covers the source synset (nouns,
// verbs); elsewhere it falls back to word-level SemCor frequency IC — both
// are -ln(probability), so rarer/more specific sorts higher.
func (db *DB) Ladders(lemma string) []SenseLadder {
	var out []SenseLadder
	for _, pos := range []byte{'n', 'v', 'a', 'r'} {
		for _, sense := range db.Lookup(lemma, pos) {
			if l := db.ladderFor(lemma, sense); len(l.Rungs) > 1 {
				out = append(out, l)
			}
		}
	}
	return out
}

func (db *DB) ladderFor(lemma string, sense *Synset) SenseLadder {
	seen := map[string]bool{}
	var rungs []Rung
	add := func(words []string, src *Synset, kind string) {
		for _, w := range words {
			if seen[w] {
				continue
			}
			seen[w] = true
			r := Rung{Word: w, Source: kind}
			if ic, ok := db.SynsetIC(src.Key()); ok {
				r.IC = ic
			} else {
				r.IC = db.WordIC(w)
			}
			rungs = append(rungs, r)
		}
	}

	display := cleanWord(lemma)
	seen[display] = true
	self := Rung{Word: display, Source: "self"}
	if ic, ok := db.SynsetIC(sense.Key()); ok {
		self.IC = ic
	} else {
		self.IC = db.WordIC(lemma)
	}
	rungs = append(rungs, self)

	add(sense.Words, sense, "synonym")
	for _, hk := range sense.Hypernyms {
		h := db.Get(hk)
		if h == nil {
			continue
		}
		add(h.Words, h, "hypernym")
		for _, hk2 := range h.Hypernyms {
			if h2 := db.Get(hk2); h2 != nil {
				add(h2.Words, h2, "hypernym2")
			}
		}
	}
	for _, sk := range sense.Similar {
		if s := db.Get(sk); s != nil {
			add(s.Words, s, "similar")
		}
	}

	// Same-synset words tie on synset IC; word frequency breaks the tie so
	// the common word (the natural demote) sits left of the rare one.
	sort.SliceStable(rungs, func(i, j int) bool {
		if rungs[i].IC != rungs[j].IC {
			return rungs[i].IC < rungs[j].IC
		}
		return db.WordIC(rungs[i].Word) < db.WordIC(rungs[j].Word)
	})
	return SenseLadder{Synset: sense, Rungs: rungs}
}
