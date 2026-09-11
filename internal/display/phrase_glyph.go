package display

import (
	"regexp"
	"sort"
	"strings"
)

// phraseGlyph is one compiled multi-word glyph entry: an exact phrase,
// matched case-insensitively, with any run of whitespace standing in for
// the single space between its words so a stray double space doesn't defeat
// the match.
type phraseGlyph struct {
	re    *regexp.Regexp
	key   string // the lowercase phrase, for counts and the glyphs lookup
	glyph string
}

// phraseGlyphs pulls the multi-word entries out of g — anything containing a
// space is a phrase, everything else is a single word already handled by
// swapWords — and compiles each into a boundary-anchored, case-insensitive
// pattern. Sorted longest-pattern-first: no shipped phrase contains another
// today, but a future entry might, and a shorter neighbor matching first
// would otherwise claim part of it.
func phraseGlyphs(g Glyphs) []phraseGlyph {
	var out []phraseGlyph
	for phrase, glyph := range g {
		words := strings.Fields(phrase)
		if len(words) < 2 {
			continue
		}
		parts := make([]string, len(words))
		for i, w := range words {
			parts[i] = regexp.QuoteMeta(w)
		}
		re, err := regexp.Compile(`(?i)\b` + strings.Join(parts, `\s+`) + `\b`)
		if err != nil {
			continue
		}
		out = append(out, phraseGlyph{re: re, key: strings.ToLower(phrase), glyph: glyph})
	}
	sort.Slice(out, func(i, j int) bool { return len(out[i].re.String()) > len(out[j].re.String()) })
	return out
}

// applyPhraseGlyphs replaces every match of each compiled phrase in line, in
// order, recording one count per match under the phrase's own lowercase
// text — the same key AppendLog checks against g to attribute Mode "glyph".
// Matching is single-line only: a phrase split across two lines (by a
// message's own line wrap, or by SplitPending holding one line's tail back
// for the next batch) is never seen whole and is deliberately not matched —
// see phrase_glyph_test.go.
func applyPhraseGlyphs(line string, phrases []phraseGlyph, counts map[string]int) string {
	for _, p := range phrases {
		n := 0
		line = p.re.ReplaceAllStringFunc(line, func(string) string {
			n++
			return p.glyph
		})
		if n > 0 {
			counts[p.key] += n
		}
	}
	return line
}
