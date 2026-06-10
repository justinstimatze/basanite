package pipeline

import (
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
