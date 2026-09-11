package display

import (
	"bufio"
	_ "embed"
	"errors"
	"io/fs"
	"os"
	"strings"
)

//go:embed glyphs.txt
var glyphSeed string

// Glyphs maps a flagged lemma — or a flagged phrase, its key containing a
// space — to a single display glyph: a narrow, plain Unicode mark, not a
// word. Unlike Swaps, a glyph is never inflected or case-matched: there is
// no plural or capitalization of a symbol, and no vetted "demote rung" is
// needed, since a glyph never claims to be a sense-checked synonym. A word
// key present here always wins over Swaps for that lemma, in swapWords; a
// phrase key has no Swaps equivalent at all — see phraseGlyphs.
type Glyphs map[string]string

// GlyphsSeed is the embedded starter table — the bytes `basanite glyphs
// -init` writes to a fresh glyphs.txt. Exposed for tooling and tests.
func GlyphsSeed() string { return glyphSeed }

// GlyphsFromFile parses "lemma:glyph" pairs, one per line, from a user-owned
// file — "#" comments and blank lines skipped, same shape as
// internal/knowntics's seed file. A missing file, or a blank path
// (glyphsPath() returns "" with no home dir), both return (nil, nil): glyph
// mode is opt-in, so absence is the normal case, not an error.
//
// Unlike knowntics.Load, this never seeds the file — seeding is the explicit
// `basanite glyphs -init` action, not a side effect of every display call,
// so an upgrade can't silently turn glyph mode on for an existing install.
func GlyphsFromFile(path string) (Glyphs, error) {
	if path == "" {
		return nil, nil
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return parseGlyphs(string(b)), nil
}

func parseGlyphs(body string) Glyphs {
	g := Glyphs{}
	sc := bufio.NewScanner(strings.NewReader(body))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		lemma, glyph, ok := strings.Cut(line, ":")
		lemma, glyph = strings.TrimSpace(lemma), strings.TrimSpace(glyph)
		if !ok || lemma == "" || glyph == "" {
			continue
		}
		g[strings.ToLower(lemma)] = glyph
	}
	return g
}
