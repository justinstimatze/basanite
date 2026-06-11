// Package knowntics holds the reference of words and phrases Claude is known
// to lean on — the seed the chronic detector and the phrase track match
// against.
//
// The model is a single user-owned list, not a baked-in one. The embedded
// content is a *starter seed*: on first run it is written to the user's
// known-tics.txt, and from then on that file is the only source read. So the
// list is the user's to curate — entries accrete and fall out over time (a tic
// that was a given model's tell stops mattering when the model changes), and
// nothing upstream silently re-adds what the user deleted.
package knowntics

import (
	"bufio"
	_ "embed"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/justinstimatze/basanite/internal/text"
)

//go:embed known-tics.txt
var seed string

// Set is the parsed reference: single-word lemmas the chronic detector can
// admit as a third route, and the multi-word phrases the phrase track counts.
type Set struct {
	Words   map[string]bool // single-word lemmas, lowercased + lemmatized
	Phrases []string        // multi-word phrases, lowercased, surface form
}

// Seed is the embedded starter list — the bytes written to a fresh install's
// known-tics.txt. Exposed for tooling and tests.
func Seed() string { return seed }

// Load reads the user-owned known-tics list at path, creating it from the
// embedded seed the first time so the list becomes the user's to curate. A
// blank path, or any create/read error, falls back to the embedded seed
// parsed in memory: the feature degrades to the starter set, it never
// vanishes. The bool reports whether the file was just seeded (first run), so
// a caller can point the user at their new editable list.
func Load(path string) (*Set, bool) {
	seeded := false
	if path != "" {
		if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err == nil {
				if os.WriteFile(path, []byte(seed), 0o644) == nil {
					seeded = true
				}
			}
		}
		if b, err := os.ReadFile(path); err == nil {
			return parse(string(b)), seeded
		}
	}
	return parse(seed), seeded
}

// parse reads one list body. A line with an interior space is a phrase; the
// rest are single words, lemmatized and lowercased to match corpus tokens.
func parse(body string) *Set {
	s := &Set{Words: map[string]bool{}}
	seen := map[string]bool{}
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
	return s
}
