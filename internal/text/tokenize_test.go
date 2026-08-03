package text

import (
	"slices"
	"testing"
)

func TestTokensStripsNonProse(t *testing.T) {
	in := "The detector uses cosine math.\n```go\nfunc main() {}\n```\nSee `corpus.Read` and https://example.com/docs plus internal/detect/detect.go for details."
	got := Tokens(in)
	for _, bad := range []string{"func", "main", "read", "http", "example", "detect"} {
		if slices.Contains(got, bad) {
			t.Errorf("token %q should have been stripped (got %v)", bad, got)
		}
	}
	if !slices.Contains(got, "detector") || !slices.Contains(got, "cosine") {
		t.Errorf("prose tokens missing: %v", got)
	}
}

func TestTokensKeepsHyphenated(t *testing.T) {
	got := Tokens("A load-bearing wall, an oracle-free design.")
	if !slices.Contains(got, "load-bearing") || !slices.Contains(got, "oracle-free") {
		t.Errorf("hyphenated tokens lost: %v", got)
	}
}

func TestLemma(t *testing.T) {
	cases := map[string]string{
		"walls":      "wall",
		"ladders":    "ladder",
		"strategies": "strategy",
		"boxes":      "box",
		"glass":      "glass",
		"corpus":     "corpus",
		"analysis":   "analysis", // -is exclusion keeps Greek singulars intact
		"claude's":   "claude",
		"during":     "during", // no verb-suffix stripping by design
	}
	for in, want := range cases {
		if got := Lemma(in); got != want {
			t.Errorf("Lemma(%q) = %q, want %q", in, got, want)
		}
	}
}

// Sentences must be token-preserving relative to Tokens — the pipeline
// counts from sentences and the property is what makes those counts equal
// whole-turn counts.
func TestSentencesPreserveTokens(t *testing.T) {
	in := "The detector works well; it caught the tic.\nA second paragraph! With `code` and a load-bearing wall? Yes."
	sents := Sentences(in)
	var flat []string
	for _, sent := range sents {
		flat = append(flat, sent.Tokens...)
	}
	whole := Tokens(in)
	if !slices.Equal(flat, whole) {
		t.Errorf("sentence tokens %v != whole-turn tokens %v", flat, whole)
	}
	if len(sents) < 3 {
		t.Errorf("expected at least 3 sentences, got %d", len(sents))
	}
	// Raw keeps stopwords for frame detection
	if got := Words(sents[0].Raw); !slices.Contains(got, "the") {
		t.Errorf("raw words should keep stopwords: %v", got)
	}
}

func TestStopwordsFiltered(t *testing.T) {
	got := Tokens("the and because actually really just basically")
	if len(got) != 1 || got[0] != "basically" {
		t.Errorf("stopword filtering: got %v, want [basically]", got)
	}
}

// The signal that separates a project name from a lean without a curated
// list: names are capitalized in the middle of a sentence, ordinary words
// are not. Measured on real transcripts the gap is 31% to 65% with nothing
// in it, which is why a flat threshold is enough.
func TestNameCountsIgnoresSentenceStartAndAllCaps(t *testing.T) {
	mid, capped := map[string]int{}, map[string]int{}
	NameCounts("Chrome is fine. We ran Chrome again; the chrome trim held.", mid, capped)

	// Three uses, but the sentence-initial one carries no information and is
	// not counted at all.
	if mid["chrome"] != 2 || capped["chrome"] != 1 {
		t.Errorf("mid=%d capped=%d, want 2 and 1", mid["chrome"], capped["chrome"])
	}

	mid, capped = map[string]int{}, map[string]int{}
	NameCounts("the ERROR path and the error path", mid, capped)
	if capped["error"] != 0 {
		t.Errorf("ALL-CAPS is emphasis, not a name: capped=%d", capped["error"])
	}
	if mid["error"] != 2 {
		t.Errorf("both uses are still mid-sentence uses: mid=%d", mid["error"])
	}
}

func TestNameCountsSkipsCodeAndNeedsEnoughUses(t *testing.T) {
	mid, capped := map[string]int{}, map[string]int{}
	NameCounts("we ran `Chrome` here and see internal/Chrome/main.go too", mid, capped)
	if mid["chrome"] != 0 {
		t.Errorf("inline code and paths are not prose: mid=%d", mid["chrome"])
	}
	// A handful of uses cannot tell a name from a coincidence.
	if IsName(MinNameUses-1, MinNameUses-1) {
		t.Error("below the use floor nothing is a name, however capitalized")
	}
	if !IsName(MinNameUses, MinNameUses) {
		t.Error("at the floor, all-capitalized must read as a name")
	}
	if IsName(100, 49) || !IsName(100, 50) {
		t.Error("the threshold is half of the mid-sentence uses")
	}
}
