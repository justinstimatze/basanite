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
