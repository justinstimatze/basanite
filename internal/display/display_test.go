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

// A known tic the judge could not match to any ladder rung ("arm": known,
// no demote_to) is exactly the case FromReport must drop — there is no
// target to swap in — and exactly the case writecheck must still flag, since
// there is no live-display substitute masking it either.
func TestFromReportForDetectionKeepsWordsWithNoSubstitute(t *testing.T) {
	rep := &report.Report{Entries: []report.Entry{
		{Kind: "chronic", Lemma: "load-bearing", DemoteTo: "supporting", Known: true},
		{Kind: "chronic", Lemma: "arm", Known: true}, // known tic, no rung fit
		{Kind: "riser", Lemma: "five", DemoteTo: "figure"},
		{Kind: "phrase", Lemma: "worth noting", DemoteTo: "x"},
	}}
	s := FromReportForDetection(rep, false)
	if got, ok := s["arm"]; !ok || got != "" {
		t.Errorf(`FromReportForDetection(false)["arm"] = (%q, %v), want ("", true)`, got, ok)
	}
	if s["load-bearing"] != "supporting" {
		t.Errorf("curated entry with a rung missing from the detection table: %v", s)
	}
	for _, skip := range []string{"five", "worth noting"} {
		if _, ok := s[skip]; ok {
			t.Errorf("%q must not be detected by default: %v", skip, s)
		}
	}
	if s = FromReportForDetection(rep, true); s["five"] != "figure" {
		t.Errorf("all=true must opt into the unvetted rest: %v", s)
	}
	if _, ok := FromReportForDetection(rep, true)["worth noting"]; ok {
		t.Error("a phrase has no ladder and can never be detected, even with all=true")
	}
}

func TestApplySwapsProseAndCarriesCase(t *testing.T) {
	s := swaps()
	got, _, _ := s.Apply("Load-bearing checks sit on the substrate.", State{})
	if want := "Supporting checks sit on the component."; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got, _, _ := s.Apply("SUBSTRATE", State{}); got != "COMPONENT" {
		t.Errorf("all-caps must survive the swap, got %q", got)
	}
	// A compound that merely contains a flagged lemma is not that lemma.
	if got, _, _ := s.Apply("substrate-like things", State{}); strings.Contains(got, "component") {
		t.Errorf("swapped inside a longer word: %q", got)
	}
	// A plural is the same word. The table is keyed on lemmas, so matching
	// the surface form alone skipped every inflected use — "arms", the form
	// the lean actually takes, never swapped once.
	if got, _, _ := s.Apply("Both substrates held.", State{}); got != "Both components held." {
		t.Errorf("a plural must swap and stay plural, got %q", got)
	}
}

func TestPluralAgreementFollowsTheReplacement(t *testing.T) {
	// The rung is a WordNet noun, so the regular rule covers it — but the
	// replacement's own ending decides the suffix, not the original's.
	for _, c := range []struct{ lemma, rep, in, want string }{
		{"arm", "branch", "both arms", "both branches"},            // -ch takes -es
		{"lane", "path", "two lanes", "two paths"},                 // regular
		{"surface", "boundary", "the surfaces", "the boundaries"},  // -y takes -ies
		{"arm", "branch", "the arm's reach", "the branch's reach"}, // possessive, not plural
	} {
		got, _, _ := Swaps{c.lemma: c.rep}.Apply(c.in, State{})
		if got != c.want {
			t.Errorf("%q -> %q: got %q, want %q", c.lemma, c.rep, got, c.want)
		}
	}
}

// The reason this is not the blog post's regex: a swap inside code is text you
// might copy or run.
func TestApplyLeavesCodeAlone(t *testing.T) {
	s := swaps()
	got, _, _ := s.Apply("The `substrate` field in internal/report/substrate.go is load-bearing.", State{})
	if !strings.Contains(got, "`substrate`") {
		t.Errorf("inline code was rewritten: %q", got)
	}
	if !strings.Contains(got, "internal/report/substrate.go") {
		t.Errorf("a path was rewritten: %q", got)
	}
	if !strings.Contains(got, "supporting.") {
		t.Errorf("prose outside the protected spans should still swap: %q", got)
	}
	if got, _, _ := s.Apply("See https://x.dev/substrate now", State{}); !strings.Contains(got, "x.dev/substrate") {
		t.Errorf("a URL was rewritten: %q", got)
	}
}

// A fenced block opens in one streamed batch and closes in another, so the
// fence state has to survive between calls or the swap leaks into code.
func TestFenceStateCarriesAcrossBatches(t *testing.T) {
	s := swaps()
	first, st, _ := s.Apply("Here is the substrate:\n```go", State{})
	if !strings.Contains(first, "component:") {
		t.Errorf("prose before the fence should swap: %q", first)
	}
	if !st.InFence {
		t.Fatal("the opening fence must be recorded for the next batch")
	}
	second, st, _ := s.Apply(`x := "substrate"`, st)
	if second != `x := "substrate"` {
		t.Errorf("code inside a carried-over fence was rewritten: %q", second)
	}
	third, st, _ := s.Apply("```\nBack to substrate prose.", st)
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
	if got, _, _ := (Swaps{}).Apply("substrate", State{}); got != "substrate" {
		t.Errorf("no swaps must mean no change, got %q", got)
	}
}
