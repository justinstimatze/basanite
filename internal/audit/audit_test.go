package audit

import (
	"strings"
	"testing"
	"time"

	"github.com/justinstimatze/basanite/internal/corpus"
	"github.com/justinstimatze/basanite/internal/judge"
	"github.com/justinstimatze/basanite/internal/knowntics"
	"github.com/justinstimatze/basanite/internal/report"
	"github.com/justinstimatze/basanite/internal/text"
)

func turns(texts ...string) []corpus.Turn {
	out := make([]corpus.Turn, 0, len(texts))
	for i, t := range texts {
		out = append(out, corpus.Turn{Time: time.Now(), Project: "p" + string(rune('a'+i%2)), Text: t})
	}
	return out
}

// The three outcomes the list cannot distinguish on its own, and the reason
// this command exists: from outside, a dead pattern and a correctly-ranked-out
// one look identical.
func TestRunSeparatesTheThreeOutcomes(t *testing.T) {
	known := &knowntics.Set{
		Words:   map[string]bool{"substrate": true, "quokka": true, "surface": true},
		Phrases: []string{"worth noting", "let me be unequivocal"},
	}
	rep := &report.Report{Entries: []report.Entry{
		{Lemma: "substrate", Kind: "chronic"},
	}}
	res := Run(known, rep, turns(
		"the substrate is worth noting here",
		"another substrate on the surface, worth noting",
	), 90, nil)

	got := map[string]Entry{}
	for _, e := range res.Entries {
		got[e.Term] = e
	}
	if got["substrate"].Status != Reported || got["substrate"].Rank != 1 {
		t.Errorf("a term in the report is reported with its rank: %+v", got["substrate"])
	}
	if got["surface"].Status != Below || got["surface"].Hits != 1 {
		t.Errorf("a matching term absent from the report is below cutoff: %+v", got["surface"])
	}
	if got["quokka"].Status != Never || got["quokka"].Hits != 0 {
		t.Errorf("a term that never matches must be called dead: %+v", got["quokka"])
	}
	if e := got["worth noting"]; e.Hits != 2 || !e.IsPhrase {
		t.Errorf("phrases count over the surface stream: %+v", e)
	}
	if got["let me be unequivocal"].Status != Never {
		t.Errorf("an unmatched phrase is dead too: %+v", got["let me be unequivocal"])
	}
	// substrate reported; surface and "worth noting" match but are ranked out;
	// quokka and "let me be unequivocal" are dead.
	if res.Reported != 1 || res.Below != 2 || res.Never != 2 {
		t.Errorf("tallies wrong: %d reported, %d below, %d never", res.Reported, res.Below, res.Never)
	}
	if got["substrate"].Projects != 2 {
		t.Errorf("dispersion should count distinct projects, got %d", got["substrate"].Projects)
	}
	if got["substrate"].Rate <= 0 {
		t.Error("a matched term needs a per-1k rate")
	}
}

// A phrase audited against the tokenized stream would read as dead for the
// wrong reason — tokenization drops the stopwords the phrase is made of.
func TestPhrasesAreNotReportedDeadByTokenization(t *testing.T) {
	known := &knowntics.Set{
		Words:   map[string]bool{},
		Phrases: []string{"that said"},
	}
	res := Run(known, nil, turns("that said, the tests pass"), 30, nil)
	if len(res.Entries) != 1 || res.Entries[0].Hits != 1 {
		t.Fatalf("a stopword-only phrase must still be found: %+v", res.Entries)
	}
	if res.Never != 0 {
		t.Error("counting phrases on the wrong stream would report this dead")
	}
}

func TestRenderOrdersDeadEntriesLastAndCanNarrow(t *testing.T) {
	known := &knowntics.Set{Words: map[string]bool{"substrate": true, "quokka": true}}
	res := Run(known, nil, turns("substrate substrate"), 90, nil)

	full := res.Render(false)
	if i, j := strings.Index(full, "substrate"), strings.Index(full, "quokka"); i > j {
		t.Errorf("the dead entry must sort last:\n%s", full)
	}
	if !strings.Contains(full, "NEVER FIRES") || !strings.Contains(full, "1 never fire") {
		t.Errorf("render must name the dead entries and tally them:\n%s", full)
	}

	narrow := res.Render(true)
	if strings.Contains(narrow, "  substrate") {
		t.Errorf("-never must drop the working entries:\n%s", narrow)
	}
	if !strings.Contains(narrow, "quokka") {
		t.Errorf("-never must keep the dead ones:\n%s", narrow)
	}
}

// The failure this status exists for: calibration cleared every threshold
// (rate, dispersion, the curated route), reached the judge, and was suppressed
// as a term of art. Reported as "below cutoff" it reads as a tuning problem,
// and tuning will never move it.
func TestJudgeSuppressionIsNotBelowCutoff(t *testing.T) {
	known := &knowntics.Set{Words: map[string]bool{"calibration": true, "surface": true}}
	judged := map[string]string{"calibration": judge.RoleTermOfArt, "surface": judge.RoleTic}
	res := Run(known, nil, turns("calibration and surface", "more calibration here"), 90, judged)

	got := map[string]Entry{}
	for _, e := range res.Entries {
		got[e.Term] = e
	}
	if got["calibration"].Status != Suppressed {
		t.Errorf("a judged term of art must not read as ranked out: %+v", got["calibration"])
	}
	if got["surface"].Status != Below {
		t.Errorf("a term the judge kept is genuinely below cutoff: %+v", got["surface"])
	}
	if res.Suppressed != 1 || res.Below != 1 {
		t.Errorf("tallies wrong: %d suppressed, %d below", res.Suppressed, res.Below)
	}
	if out := res.Render(false); !strings.Contains(out, "term of art") {
		t.Errorf("render must name the suppression and say tuning won't move it:\n%s", out)
	}
}

// Writing about an entry puts it in the transcripts the next audit reads, so a
// row supported by one project and one hit is usually the tool citing itself.
func TestRenderFlagsSingleProjectRowsAsSelfCitation(t *testing.T) {
	known := &knowntics.Set{Words: map[string]bool{"substrate": true, "liminal": true}}
	// pa and pb alternate, so "substrate" disperses and "liminal" does not.
	res := Run(known, nil, turns("substrate", "substrate", "liminal"), 90, nil)
	out := res.Render(false)
	if !strings.Contains(out, "1 entry sits at 1–2 hits in a single project") {
		t.Errorf("a one-project one-hit row must be called out as self-citation:\n%s", out)
	}
}

// Truncating an entry makes its row un-actionable: you cannot delete a line
// you cannot read. This 30-character phrase was on the seed and reported dead,
// and the render cut it short of its last word — so the audit's own output
// hid the only entry the audit had a verdict on.
func TestRenderDoesNotTruncateALongPhrase(t *testing.T) {
	const long = "the thing underneath the thing"
	known := &knowntics.Set{Phrases: []string{long}}
	if out := Run(known, nil, turns("nothing here"), 90, nil).Render(false); !strings.Contains(out, long) {
		t.Errorf("the entry column must fit the seeded phrases whole:\n%s", out)
	}
}

func TestRunWithNoListIsEmptyNotPanic(t *testing.T) {
	if res := Run(nil, nil, turns("anything"), 7, nil); len(res.Entries) != 0 {
		t.Errorf("no curated list means nothing to audit, got %+v", res.Entries)
	}
}

// The condition on adding the name guard at all. A suppression the audit
// cannot see is precisely the failure this command exists to catch — the
// calibration hunt cost an investigation because "judged out" and "ranked
// out" wore the same label. A new silent suppressor eight days later would
// be the same bug wearing a different hat.
func TestNameSuppressionIsItsOwnStatus(t *testing.T) {
	known := &knowntics.Set{Words: map[string]bool{"chrome": true, "surface": true}}
	// Same sentence, same counts, same everything: only the case differs.
	texts := make([]string, text.MinNameUses)
	for i := range texts {
		texts[i] = "we shipped Chrome and the surface held"
	}
	res := Run(known, nil, turns(texts...), 90, nil)

	got := map[string]Entry{}
	for _, e := range res.Entries {
		got[e.Term] = e
	}
	if got["chrome"].Status != IsName {
		t.Errorf("a word the corpus capitalizes mid-sentence is suppressed as a name: %+v", got["chrome"])
	}
	if got["surface"].Status != Below {
		t.Errorf("the lowercase control must stay below-cutoff: %+v", got["surface"])
	}
	if res.Named != 1 {
		t.Errorf("tallies wrong: %d named", res.Named)
	}
	if out := res.Render(false); !strings.Contains(out, "read as name") {
		t.Errorf("the guard must be visible in the render, not silent:\n%s", out)
	}
}
