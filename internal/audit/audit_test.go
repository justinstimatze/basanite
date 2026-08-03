package audit

import (
	"strings"
	"testing"
	"time"

	"github.com/justinstimatze/basanite/internal/corpus"
	"github.com/justinstimatze/basanite/internal/knowntics"
	"github.com/justinstimatze/basanite/internal/report"
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
	), 90)

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
	res := Run(known, nil, turns("that said, the tests pass"), 30)
	if len(res.Entries) != 1 || res.Entries[0].Hits != 1 {
		t.Fatalf("a stopword-only phrase must still be found: %+v", res.Entries)
	}
	if res.Never != 0 {
		t.Error("counting phrases on the wrong stream would report this dead")
	}
}

func TestRenderOrdersDeadEntriesLastAndCanNarrow(t *testing.T) {
	known := &knowntics.Set{Words: map[string]bool{"substrate": true, "quokka": true}}
	res := Run(known, nil, turns("substrate substrate"), 90)

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

func TestRunWithNoListIsEmptyNotPanic(t *testing.T) {
	if res := Run(nil, nil, turns("anything"), 7); len(res.Entries) != 0 {
		t.Errorf("no curated list means nothing to audit, got %+v", res.Entries)
	}
}
