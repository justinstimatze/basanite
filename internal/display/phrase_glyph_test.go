package display

import (
	"strings"
	"testing"
)

func TestCuratedPhraseRendersAsItsGlyph(t *testing.T) {
	g := Glyphs{"worth noting": "∴"}
	got, _, counts := Swaps{}.ApplyWithGlyphs("This is worth noting today.", State{}, g)
	if want := "This is ∴ today."; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if counts["worth noting"] != 1 {
		t.Errorf("a phrase hit must count under its own key: %v", counts)
	}
}

func TestPhraseGlyphIsCaseInsensitive(t *testing.T) {
	g := Glyphs{"worth noting": "∴"}
	got, _, _ := Swaps{}.ApplyWithGlyphs("Worth Noting: this matters.", State{}, g)
	if !strings.HasPrefix(got, "∴:") {
		t.Errorf("case-insensitive phrase match failed: %q", got)
	}
}

// A phrase entirely inside a fenced code block is never reached at all —
// applyLine only ever runs on non-fence lines.
func TestPhraseGlyphLeavesFencedCodeAlone(t *testing.T) {
	g := Glyphs{"worth noting": "∴"}
	first, st, _ := Swaps{}.ApplyWithGlyphs("```go", State{}, g)
	if !st.InFence {
		t.Fatal("opening fence must be recorded")
	}
	second, _, _ := Swaps{}.ApplyWithGlyphs(`// worth noting: this is a comment`, st, g)
	if first != "```go" || second != `// worth noting: this is a comment` {
		t.Errorf("phrase glyphed inside a fence: %q / %q", first, second)
	}
}

// A phrase inside a protected span (inline code here) must be left alone —
// same rule word-swap already respects.
func TestPhraseGlyphLeavesProtectedSpansAlone(t *testing.T) {
	g := Glyphs{"worth noting": "∴"}
	got, _, _ := Swaps{}.ApplyWithGlyphs("The `worth noting` field is worth noting.", State{}, g)
	if !strings.Contains(got, "`worth noting`") {
		t.Errorf("inline code was glyphed: %q", got)
	}
	if !strings.Contains(got, "is ∴.") {
		t.Errorf("prose outside the protected span should still glyph: %q", got)
	}
}

// Phrase matching is single-line only: a phrase split across two lines — by
// a message's own wrap, or by SplitPending holding one line's tail back for
// the next batch — is never seen whole and is deliberately not matched.
// This documents the limitation as a passing test, not a silent gap.
func TestPhraseGlyphDoesNotMatchAcrossTwoLines(t *testing.T) {
	g := Glyphs{"worth noting": "∴"}
	got, _, counts := Swaps{}.ApplyWithGlyphs("This is worth\nnoting today.", State{}, g)
	if strings.Contains(got, "∴") {
		t.Errorf("a phrase split across two lines must not match: %q", got)
	}
	if counts["worth noting"] != 0 {
		t.Errorf("no cross-line count expected: %v", counts)
	}
}

// A single-word entry in the same Glyphs map must still take the word path,
// unaffected by the phrase pass added alongside it.
func TestWordGlyphStillWorksAlongsidePhraseGlyphs(t *testing.T) {
	g := Glyphs{"load-bearing": "†", "worth noting": "∴"}
	got, _, _ := Swaps{}.ApplyWithGlyphs("A load-bearing fact worth noting.", State{}, g)
	if want := "A † fact ∴."; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
