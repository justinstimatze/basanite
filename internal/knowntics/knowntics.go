// Package knowntics holds the curated reference of words and phrases Claude
// is known to lean on — the seed the chronic detector and the phrase track
// match against. The built-in list ships embedded in the binary: a
// conservative, high-precision sample of the globally common leans (the
// assistant-register staples that recur in Claude Code transcripts, plus a
// few iconic signatures seeded from the community "Claude Bingo" card). Users
// extend it with their own known-tics.txt in the data dir or ~/.config/basanite,
// which is where niche or personal leans belong.
//
// It is a reference, not a denylist. A seeded word still has to clear the
// chronic detector's rate and dispersion gates, and a seeded phrase still has
// to be one you're actually repeating, before either surfaces — and the
// output stays awareness, never prohibition (see DESIGN.md).
package knowntics

import (
	"bufio"
	_ "embed"
	"os"
	"strings"

	"github.com/justinstimatze/basanite/internal/text"
)

//go:embed known-tics.txt
var builtin string

// Set is the parsed reference: single-word lemmas the chronic detector can
// admit as a third route, and the multi-word phrases the phrase track counts.
type Set struct {
	Words   map[string]bool // single-word lemmas, lowercased + lemmatized
	Phrases []string        // multi-word phrases, lowercased, surface form
}

// Load parses the embedded list, then merges any user lists found at the
// given extra paths (later files extend, never replace). Missing or unreadable
// files are skipped — a personal list is optional.
func Load(extra ...string) *Set {
	s := &Set{Words: map[string]bool{}}
	seen := map[string]bool{}
	s.parse(builtin, seen)
	for _, p := range extra {
		if b, err := os.ReadFile(p); err == nil {
			s.parse(string(b), seen)
		}
	}
	return s
}

// parse reads one list body. A line with an interior space is a phrase; the
// rest are single words, lemmatized and lowercased to match corpus tokens. The
// seen set dedups phrases across the builtin and user lists.
func (s *Set) parse(body string, seen map[string]bool) {
	sc := bufio.NewScanner(strings.NewReader(body))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.ToLower(line)
		if strings.ContainsRune(line, ' ') {
			if !seen[line] {
				seen[line] = true
				s.Phrases = append(s.Phrases, line)
			}
			continue
		}
		s.Words[text.Lemma(line)] = true
	}
}
