package cloze

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/justinstimatze/basanite/internal/embed"
	"github.com/justinstimatze/basanite/internal/text"
)

// tinyTable writes a toy GloVe file and loads it. "cat" and "feline" are
// near-identical; "rocket" is orthogonal to both.
func tinyTable(t *testing.T) *embed.Table {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "vecs.txt")
	content := "cat 1.0 0.1 0.0\n" +
		"feline 1.0 0.12 0.0\n" +
		"rocket 0.0 0.0 1.0\n" +
		"chased 0.5 1.0 0.0\n" +
		"mouse 0.9 0.3 0.0\n" +
		"hungry 0.6 0.8 0.1\n" +
		"garden 0.4 0.5 0.2\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	tbl, err := embed.Load(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	return tbl
}

func TestRankSubstitutes(t *testing.T) {
	tbl := tinyTable(t)
	uses := [][]string{
		{"hungry", "cat", "chased", "mouse", "garden"},
		{"cat", "chased", "hungry", "mouse", "garden"},
	}
	ranked := RankSubstitutes(tbl, uses, "cat", []string{"feline", "rocket", "unobtainium"}, 0.99)
	if len(ranked) != 2 {
		t.Fatalf("got %d candidates, want 2 (OOV unobtainium must be skipped, not scored)", len(ranked))
	}
	if ranked[0].Word != "feline" {
		t.Errorf("top candidate = %s, want feline", ranked[0].Word)
	}
	if ranked[0].MeanCos <= ranked[1].MeanCos {
		t.Errorf("feline (%f) should outscore rocket (%f)", ranked[0].MeanCos, ranked[1].MeanCos)
	}
}

func sent(toks ...string) text.Sentence {
	return text.Sentence{Tokens: toks, Raw: strings.Join(toks, " ")}
}

func TestCorpusDedupAndIndex(t *testing.T) {
	c := NewCorpus()
	c.Add(sent("hungry", "cat", "chased", "small", "mouse"))
	c.Add(sent("hungry", "cat", "chased", "small", "mouse")) // exact dup
	c.Add(sent("cat", "sat", "garden", "watching", "bird"))
	c.Add(sent("too", "short"))                       // under MinSentenceTokens
	c.Add(sent("no", "target", "word", "here", "at")) // counts, no cat

	if c.Len() != 3 {
		t.Fatalf("Len = %d, want 3 (dup and short fragment dropped)", c.Len())
	}
	uses := c.Uses("cat", 0)
	if len(uses) != 2 {
		t.Fatalf("Uses(cat) = %d sentences, want 2", len(uses))
	}
	if got := c.Uses("cat", 1); len(got) != 1 {
		t.Errorf("Uses(cat, max=1) = %d, want 1", len(got))
	}
	if got := c.Uses("absent", 0); len(got) != 0 {
		t.Errorf("Uses(absent) = %d, want 0", len(got))
	}
	if got := c.Sample(2); len(got) != 2 {
		t.Errorf("Sample(2) = %d, want 2", len(got))
	}
}

func TestFrameFraction(t *testing.T) {
	c := NewCorpus()
	c.Add(text.Sentence{
		Tokens: []string{"spine", "design", "held", "weight", "nicely"},
		Raw:    "the spine of the design held the weight nicely",
	})
	c.Add(text.Sentence{
		Tokens: []string{"narrative", "spine", "carry", "whole", "chapter"},
		Raw:    "a narrative spine must carry the whole chapter", // no frame
	})
	c.Add(text.Sentence{
		Tokens: []string{"spine", "document", "needed", "real", "support"},
		Raw:    "its spine of the document needed real support",
	})
	frac, uses := c.FrameFraction("spine")
	if uses != 3 {
		t.Fatalf("uses = %d, want 3", uses)
	}
	if frac < 0.66 || frac > 0.67 {
		t.Errorf("frame fraction = %f, want 2/3", frac)
	}
	if frac, _ := c.FrameFraction("absent"); frac != 0 {
		t.Errorf("absent word frame fraction = %f, want 0", frac)
	}
}

func TestVarianceClusteredVsDiverse(t *testing.T) {
	tbl := tinyTable(t)
	clustered := Variance(tbl, [][]string{
		{"hungry", "cat", "chased", "mouse"},
		{"hungry", "cat", "chased", "garden"},
	}, "cat")
	diverse := Variance(tbl, [][]string{
		{"hungry", "cat", "chased", "mouse"},
		{"cat", "rocket"},
	}, "cat")
	if clustered.Clustered <= diverse.Clustered {
		t.Errorf("clustered (%f) should exceed diverse (%f)", clustered.Clustered, diverse.Clustered)
	}
}
