// Package phrase counts occurrences of a fixed set of known multi-word
// phrases in a surface word stream. The single-token frequency detector is
// structurally blind to stock phrases ("i want to honor that"): the words are
// individually unremarkable, so neither the riser nor the chronic route can
// see them — the tic is the sequence. A curated reference list (the known
// Claude leans) seeds the matcher; matching is exact over the lowercased
// surface words, stopwords kept, because the phrase's evidence is exactly what
// the lemma tokenizer drops.
package phrase

import (
	"strings"

	"github.com/justinstimatze/basanite/internal/text"
)

// Matcher holds the known phrases indexed by first word for a linear scan.
type Matcher struct {
	byFirst map[string][]entry
}

type entry struct {
	text  string   // canonical phrase, for reporting
	words []string // surface word sequence to match
}

// New builds a matcher from canonical phrase strings. Each is normalized to
// its surface word stream the same way the corpus is (lowercased, split on the
// tokenizer's character classes), so matching is apples-to-apples. Inputs that
// reduce to fewer than two words are ignored — single words belong to the
// token detector, not here.
func New(phrases []string) *Matcher {
	m := &Matcher{byFirst: map[string][]entry{}}
	for _, p := range phrases {
		words := text.Words(strings.ToLower(p))
		if len(words) < 2 {
			continue
		}
		first := words[0]
		m.byFirst[first] = append(m.byFirst[first], entry{
			text:  strings.Join(words, " "),
			words: words,
		})
	}
	return m
}

// Empty reports whether the matcher carries no phrases, so callers can skip
// the per-sentence scan entirely. A nil matcher is empty.
func (m *Matcher) Empty() bool { return m == nil || len(m.byFirst) == 0 }

// Count scans the word stream left to right and adds each known phrase's hits
// into the provided map. On a match the scan advances past the longest phrase
// matched at that position, so overlapping stock phrases don't double-count a
// shared tail. Passing the accumulator in lets a per-sentence caller reuse one
// map across the whole corpus.
func (m *Matcher) Count(words []string, into map[string]int) {
	if m == nil {
		return
	}
	for i := 0; i < len(words); {
		matched := 0
		for _, e := range m.byFirst[words[i]] {
			if matchAt(words, i, e.words) {
				into[e.text]++
				if len(e.words) > matched {
					matched = len(e.words)
				}
			}
		}
		if matched > 0 {
			i += matched
		} else {
			i++
		}
	}
}

func matchAt(words []string, i int, pat []string) bool {
	if i+len(pat) > len(words) {
		return false
	}
	for k, w := range pat {
		if words[i+k] != w {
			return false
		}
	}
	return true
}
