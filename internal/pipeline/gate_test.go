package pipeline

import (
	"fmt"
	"strings"

	"github.com/justinstimatze/basanite/internal/corpus"
	"testing"
	"time"

	"github.com/justinstimatze/basanite/internal/judge"
	"github.com/justinstimatze/basanite/internal/report"
)

// scriptedJudge is a fake Judger: it returns the verdict mapped for a word,
// and ok=false (inconclusive → fail safe) for anything unmapped. No LLM — the
// gate logic is what's under test, not the model.
type scriptedJudge map[string]judge.Verdict

func (s scriptedJudge) Judge(word string, _ []string, _ [][]string) (judge.Verdict, bool) {
	v, ok := s[word]
	return v, ok
}

// The ablation the hybrid-loops design requires: does the gate earn its keep?
// Without it, a term-of-art entry survives into the report; with it, the same
// entry is suppressed while a real tic is kept. Same pipeline, same input —
// the only variable is the judge.
func TestGateAblation(t *testing.T) {
	now := time.Now()
	opts := Options{
		RecentDays: 7, BaselineDays: 14,
		Top: 8, MinCount: 5, MinRatio: 2.0,
		MaxUses: 50, MinUses: 5,
		Threshold: 0.97, MinClean: 0.4,
	}

	// baseline: no judge — the deterministic pipeline flags "dog"
	bare, err := Build(dogTurns(now), loadTestWN(t), testLoader(t), nil, now, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !hasEntry(bare, "dog") {
		t.Fatal("precondition: bare pipeline should flag dog")
	}

	// gated as term of art: "dog" must be suppressed
	asTermOfArt := scriptedJudge{"dog": {Role: judge.RoleTermOfArt, DemoteTo: "", Note: "fixed referent"}}
	gated, err := Build(dogTurns(now), loadTestWN(t), testLoader(t), asTermOfArt, now, opts)
	if err != nil {
		t.Fatal(err)
	}
	if hasEntry(gated, "dog") {
		t.Error("gate failed to suppress a term-of-art entry — substrate not earning its keep")
	}

	// gated as tic: kept, carrying the gate's chosen rung and note
	asTic := scriptedJudge{"dog": {Role: judge.RoleTic, DemoteTo: "entity", Note: "loose; entity is truer"}}
	kept, err := Build(dogTurns(now), loadTestWN(t), testLoader(t), asTic, now, opts)
	if err != nil {
		t.Fatal(err)
	}
	e := entry(kept, "dog")
	if e == nil {
		t.Fatal("gate dropped a tic it should have kept")
	}
	if e.JudgeRole != judge.RoleTic || e.DemoteTo != "entity" || e.JudgeNote == "" {
		t.Errorf("kept entry lost the verdict: role=%q demote=%q note=%q", e.JudgeRole, e.DemoteTo, e.JudgeNote)
	}
}

// The deterministic proper-noun guard suppresses a known project/tool name
// before the fence — and without needing a judge at all.
func TestProperNounGuardSuppresses(t *testing.T) {
	now := time.Now()
	opts := Options{
		RecentDays: 7, BaselineDays: 14,
		Top: 8, MinCount: 5, MinRatio: 2.0,
		MaxUses: 50, MinUses: 5,
		Threshold: 0.97, MinClean: 0.4,
		ProperNouns: map[string]bool{"dog": true},
	}
	rep, err := Build(dogTurns(now), loadTestWN(t), testLoader(t), nil, now, opts)
	if err != nil {
		t.Fatal(err)
	}
	if hasEntry(rep, "dog") {
		t.Error("a known proper noun must be suppressed deterministically, no judge involved")
	}
}

// An inconclusive verdict (ok=false) must fail safe to the un-gated entry —
// the gate never silences a flag on the strength of a fence that wobbled.
func TestGateFailSafeKeepsEntry(t *testing.T) {
	now := time.Now()
	inconclusive := scriptedJudge{} // maps nothing → ok=false for every word
	rep, err := Build(dogTurns(now), loadTestWN(t), testLoader(t), inconclusive, now, Options{
		RecentDays: 7, BaselineDays: 14,
		Top: 8, MinCount: 5, MinRatio: 2.0,
		MaxUses: 50, MinUses: 5,
		Threshold: 0.97, MinClean: 0.4,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasEntry(rep, "dog") {
		t.Error("inconclusive verdict must fail safe to the un-gated entry, not drop it")
	}
}

func hasEntry(r *report.Report, lemma string) bool { return entry(r, lemma) != nil }

func entry(r *report.Report, lemma string) *report.Entry {
	for i := range r.Entries {
		if r.Entries[i].Lemma == lemma {
			return &r.Entries[i]
		}
	}
	return nil
}

// The guard the curated list was standing in for. A project name reaches the
// chronic route looking exactly like a lean — steady rate, dispersed, ordinary
// English ladder — and the judge, told outright that a product name is a term
// of art, still called "chrome" a filler adjective meaning shiny. What the
// corpus knows and neither of them used is that it writes a name capitalized
// in the middle of a sentence, and a lean in lowercase.
//
// The control matters more than the assertion: both runs see the same turns,
// the same counts, the same everything. Only the case differs.
func TestNameGuardSuppressesWithoutACuratedList(t *testing.T) {
	now := time.Now()
	opts := Options{
		RecentDays: 7, BaselineDays: 14,
		Top: 8, MinCount: 5, MinRatio: 2.0,
		MaxUses: 50, MinUses: 5,
		Threshold: 0.97, MinClean: 0.4,
	} // note: no ProperNouns

	// Enough mid-sentence uses to clear the noise floor, written both ways.
	extra := func(word string) []corpus.Turn {
		ts := dogTurns(now)
		for i := 0; i < 14; i++ {
			ts = append(ts, corpus.Turn{Time: now.AddDate(0, 0, -1), Project: "alpha",
				Text: fmt.Sprintf("We shipped %s release %s without any incident today.", word, strings.Repeat("x", i+1))})
		}
		return ts
	}

	lower, err := Build(extra("dog"), loadTestWN(t), testLoader(t), nil, now, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !hasEntry(lower, "dog") {
		t.Fatal("control: written in lowercase, the same corpus must still flag dog")
	}

	upper, err := Build(extra("Dog"), loadTestWN(t), testLoader(t), nil, now, opts)
	if err != nil {
		t.Fatal(err)
	}
	if hasEntry(upper, "dog") {
		t.Error("capitalized mid-sentence, it is a name — and no curated list said so")
	}
}

// recordingJudge captures what the gate was actually offered, which is the
// thing under test — the verdict it returns is incidental.
type recordingJudge struct {
	seen map[string][]string
	v    judge.Verdict
}

func (r *recordingJudge) Judge(word string, ladder []string, _ [][]string) (judge.Verdict, bool) {
	r.seen[word] = ladder
	return r.v, true
}

// The reader is shown four rungs below the lemma; the gate used to be handed
// the whole ladder, which spans both directions around it. So the rung it
// chose could be stronger than the word it was demoting, or one the injection
// never displayed — a swap naming a word you were never offered. Measured on
// a live report before this: 9 of 21 outside the window, 2 inverted.
func TestDemoteOptionsAreOnlyTheWindowTheReaderSees(t *testing.T) {
	// A lemma sitting mid-ladder, which is the case the bug needed and the
	// test fixture's own corpus cannot produce (dog's ladder is [entity dog]).
	ladder := []report.Rung{
		{Word: "structure"}, {Word: "device"}, {Word: "instrument"},
		{Word: "weapon"}, {Word: "member"}, {Word: "limb"},
		{Word: "arm"}, // the lemma
		{Word: "sleeve"}, {Word: "branch"}, {Word: "rest"},
	}
	got := demoteOptions(ladder, "arm")
	want := []string{"instrument", "weapon", "member", "limb"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
	for _, w := range []string{"sleeve", "branch", "rest"} {
		for _, g := range got {
			if g == w {
				t.Errorf("%q is stronger than the lemma and must never be a demotion", w)
			}
		}
	}
	if len(demoteOptions([]report.Rung{{Word: "journal"}, {Word: "ledger"}}, "journal")) != 0 {
		t.Error("a lemma that is already the weakest rung has nothing to offer")
	}
}

// The wiring: Build must route through demoteOptions, not rebuild the list.
func TestBuildOffersTheGateExactlyDemoteOptions(t *testing.T) {
	now := time.Now()
	rec := &recordingJudge{seen: map[string][]string{}, v: judge.Verdict{Role: judge.RoleTic, DemoteTo: "entity", Note: "loose"}}
	rep, err := Build(dogTurns(now), loadTestWN(t), testLoader(t), rec, now, Options{
		RecentDays: 7, BaselineDays: 14,
		Top: 8, MinCount: 5, MinRatio: 2.0,
		MaxUses: 50, MinUses: 5,
		Threshold: 0.97, MinClean: 0.4,
	})
	if err != nil {
		t.Fatal(err)
	}
	e := entry(rep, "dog")
	if e == nil {
		t.Fatal("precondition: dog should be in the report")
	}
	offered, ok := rec.seen["dog"]
	if !ok {
		t.Fatal("precondition: the gate should have been asked about dog")
	}
	want := demoteOptions(e.Ladder, "dog")
	if len(offered) != len(want) {
		t.Fatalf("offered %v, want %v", offered, want)
	}
	for i := range want {
		if offered[i] != want[i] {
			t.Fatalf("offered %v, want %v", offered, want)
		}
	}
}
