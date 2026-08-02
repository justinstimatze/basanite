package display

import (
	"strings"
	"testing"

	"github.com/justinstimatze/basanite/internal/report"
)

func swaps() Swaps {
	return Swaps{"load-bearing": "supporting", "substrate": "component"}
}

// The default table is the curated list only. A ladder is vetted for average
// substitutability, not per-occurrence correctness, and the live report demotes
// "five" to "figure" — fine as awareness, ruinous as a display rewrite.
func TestFromReportDefaultsToCuratedEntries(t *testing.T) {
	rep := &report.Report{Entries: []report.Entry{
		{Kind: "chronic", Lemma: "load-bearing", DemoteTo: "supporting", Known: true},
		{Kind: "riser", Lemma: "five", DemoteTo: "figure"},
		{Kind: "chronic", Lemma: "unjudged", Known: true}, // no rung: not eligible
		{Kind: "phrase", Lemma: "worth noting", DemoteTo: "x"},
	}}
	s := FromReport(rep, false)
	if s["load-bearing"] != "supporting" {
		t.Errorf("curated entry missing from the default table: %v", s)
	}
	for _, skip := range []string{"five", "unjudged", "worth noting"} {
		if _, ok := s[skip]; ok {
			t.Errorf("%q must not be swapped by default: %v", skip, s)
		}
	}
	if s = FromReport(rep, true); s["five"] != "figure" {
		t.Errorf("all=true must opt into the unvetted rest: %v", s)
	}
	if _, ok := FromReport(rep, true)["worth noting"]; ok {
		t.Error("a phrase has no ladder and can never be swapped, even with all=true")
	}
}

func TestApplySwapsProseAndCarriesCase(t *testing.T) {
	s := swaps()
	got, _ := s.Apply("Load-bearing checks sit on the substrate.", State{})
	if want := "Supporting checks sit on the component."; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got, _ := s.Apply("SUBSTRATE", State{}); got != "COMPONENT" {
		t.Errorf("all-caps must survive the swap, got %q", got)
	}
	// A word that merely contains a flagged lemma is not that lemma.
	if got, _ := s.Apply("substrates and substrate-like things", State{}); strings.Contains(got, "component") {
		t.Errorf("swapped inside a longer word: %q", got)
	}
}

// The reason this is not the blog post's regex: a swap inside code is text you
// might copy or run.
func TestApplyLeavesCodeAlone(t *testing.T) {
	s := swaps()
	got, _ := s.Apply("The `substrate` field in internal/report/substrate.go is load-bearing.", State{})
	if !strings.Contains(got, "`substrate`") {
		t.Errorf("inline code was rewritten: %q", got)
	}
	if !strings.Contains(got, "internal/report/substrate.go") {
		t.Errorf("a path was rewritten: %q", got)
	}
	if !strings.Contains(got, "supporting.") {
		t.Errorf("prose outside the protected spans should still swap: %q", got)
	}
	if got, _ := s.Apply("See https://x.dev/substrate now", State{}); !strings.Contains(got, "x.dev/substrate") {
		t.Errorf("a URL was rewritten: %q", got)
	}
}

// A fenced block opens in one streamed batch and closes in another, so the
// fence state has to survive between calls or the swap leaks into code.
func TestFenceStateCarriesAcrossBatches(t *testing.T) {
	s := swaps()
	first, st := s.Apply("Here is the substrate:\n```go", State{})
	if !strings.Contains(first, "component:") {
		t.Errorf("prose before the fence should swap: %q", first)
	}
	if !st.InFence {
		t.Fatal("the opening fence must be recorded for the next batch")
	}
	second, st := s.Apply(`x := "substrate"`, st)
	if second != `x := "substrate"` {
		t.Errorf("code inside a carried-over fence was rewritten: %q", second)
	}
	third, st := s.Apply("```\nBack to substrate prose.", st)
	if st.InFence {
		t.Error("the closing fence must clear the state")
	}
	if !strings.Contains(third, "component prose") {
		t.Errorf("prose after the fence should swap again: %q", third)
	}
}

func TestAddOverridesTheReport(t *testing.T) {
	s := swaps()
	s.Add([]string{"load-bearing:critical", "  seam : joint ", "malformed", ":", "x:"})
	if s["load-bearing"] != "critical" {
		t.Errorf("an explicit pair must win over the report: %v", s)
	}
	if s["seam"] != "joint" {
		t.Errorf("surrounding space should be trimmed: %v", s)
	}
	if len(s) != 3 {
		t.Errorf("malformed pairs must be ignored, got %v", s)
	}
}

func TestEmptyTableIsIdentity(t *testing.T) {
	if got, _ := (Swaps{}).Apply("substrate", State{}); got != "substrate" {
		t.Errorf("no swaps must mean no change, got %q", got)
	}
}
