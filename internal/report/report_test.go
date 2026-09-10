package report

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sample() *Report {
	return &Report{
		GeneratedAt: time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC),
		RecentDays:  7, BaselineDays: 14,
		Entries: []Entry{{
			Lemma: "agent", RecentCount: 100, Ratio: 2.6,
			Ladder: []Rung{
				{Word: "delegate", IC: 1},
				{Word: "functionary", IC: 2},
				{Word: "official", IC: 3},
				{Word: "negotiator", IC: 4},
				{Word: "representative", IC: 5},
				{Word: "agent", IC: 6},
				{Word: "broker", IC: 7},
			},
		}},
	}
}

func TestSaveLoadRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.json")
	want := sample()
	if err := want.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || len(got.Entries) != 1 || got.Entries[0].Lemma != "agent" {
		t.Fatalf("roundtrip mismatch: %+v", got)
	}
}

func TestLoadMissingIsSilent(t *testing.T) {
	got, err := Load(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil || got != nil {
		t.Fatalf("missing file must be (nil, nil), got (%v, %v)", got, err)
	}
}

func TestLoadRefusesSymlinkAndOversize(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real.json")
	if err := sample().Save(real); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(real, link); err != nil {
		t.Skip("symlinks unavailable")
	}
	if _, err := Load(link); err == nil {
		t.Error("Load must refuse a symlinked report")
	}

	big := filepath.Join(dir, "big.json")
	if err := os.WriteFile(big, make([]byte, maxReportSize+1), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(big); err == nil {
		t.Error("Load must refuse an oversized report")
	}
}

func TestSaveCleansUpOnRenameFailure(t *testing.T) {
	dir := t.TempDir()
	// a directory at the target path makes the rename fail
	target := filepath.Join(dir, "report.json")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := sample().Save(target); err == nil {
		t.Fatal("Save onto a directory should fail")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".report-") {
			t.Errorf("temp file %s leaked after failed rename", e.Name())
		}
	}
}

func TestTrimLadderFallbackWhenLemmaMissing(t *testing.T) {
	rungs := []Rung{{Word: "a"}, {Word: "b"}, {Word: "c"}}
	got := trimLadder(rungs, "not-present")
	if len(got) != 3 {
		t.Errorf("missing lemma should fall back to trailing window, got %v", got)
	}
	if got := trimLadder(nil, "x"); len(got) != 0 {
		t.Errorf("empty ladder should trim to empty, got %v", got)
	}
}

func TestRenderDemoteOnlyAndTrimmed(t *testing.T) {
	out := sample().Render(false)
	if strings.Contains(out, "broker") {
		t.Error("rungs above the target must not render")
	}
	if strings.Contains(out, "delegate") {
		t.Error("ladder should trim to four rungs below the target")
	}
	if !strings.Contains(out, "functionary < official < negotiator < representative < [agent]") {
		t.Errorf("unexpected render:\n%s", out)
	}
	if (&Report{}).Render(false) != "" {
		t.Error("empty report must render to empty string")
	}
}

func TestRenderPhraseEntry(t *testing.T) {
	r := &Report{Entries: []Entry{
		{Kind: "phrase", Lemma: "i want to honor that", Count: 7, Projects: 3, Rate: 0.3},
	}}
	out := r.Render(false)
	if !strings.Contains(out, `"i want to honor that"`) {
		t.Errorf("phrase not rendered with its text: %q", out)
	}
	if !strings.Contains(out, "stock phrase, 7× across 3 projects") {
		t.Errorf("phrase note missing its count and dispersion: %q", out)
	}
	if strings.Contains(out, "<") {
		t.Errorf("phrase entry must render no ladder: %q", out)
	}
}

// A single-project phrase omits the dispersion clause rather than printing
// "across 1 projects".
func TestRenderPhraseSingleProject(t *testing.T) {
	r := &Report{Entries: []Entry{
		{Kind: "phrase", Lemma: "that said", Count: 6, Projects: 1, Rate: 0.2},
	}}
	out := r.Render(false)
	if !strings.Contains(out, "stock phrase, 6× this window") {
		t.Errorf("single-project phrase note malformed: %q", out)
	}
	if strings.Contains(out, "across") {
		t.Errorf("single-project phrase must not print a dispersion clause: %q", out)
	}
}

func TestRenderKnownLeanNote(t *testing.T) {
	r := &Report{Entries: []Entry{{
		Kind: "chronic", Lemma: "surface", Rate: 0.5, Known: true,
		Ladder: []Rung{{Word: "exterior", IC: 1}, {Word: "surface", IC: 5}},
	}}}
	if !strings.Contains(r.Render(false), "a common Claude lean") {
		t.Errorf("known-route entry must flag itself as a Claude lean:\n%s", r.Render(false))
	}
}

func TestSparklineRendersGapsAndDirection(t *testing.T) {
	if (Entry{}).sparkline() != "" {
		t.Error("no series should render to empty string")
	}
	// rising with a mid-series gap (-1): the gap is a space, direction is up
	e := Entry{Spark: []float64{1, -1, 2, 4}}
	got := e.sparkline()
	if !strings.Contains(got, " ") {
		t.Errorf("gap (-1) should render as a space: %q", got)
	}
	if !strings.HasSuffix(got, "↑") {
		t.Errorf("rising series should end with ↑: %q", got)
	}

	r := sample()
	r.Entries[0].Spark = []float64{3, 2, 1}
	if !strings.Contains(r.Render(true), "↓") {
		t.Error("a falling entry series should surface a ↓ when showSpark is true")
	}
	if strings.Contains(r.Render(false), "↓") {
		t.Error("the injection (showSpark false) must omit sparklines")
	}
}

// The hook view exists because the injection is read by a model mid-task:
// the July 2026 report carried 18 entries and 4,613 chars of judge notes,
// which is a wall to skim, not awareness to hold.
func TestRenderHookCapsRoutesSeparately(t *testing.T) {
	r := &Report{}
	for _, w := range []string{"alpha", "beta", "gamma"} {
		r.Entries = append(r.Entries, Entry{
			Kind: "chronic", Lemma: w, Rate: 0.5,
			Ladder: []Rung{{Word: "weak" + w, IC: 1}, {Word: w, IC: 5}},
		})
	}
	r.Entries = append(r.Entries,
		Entry{Kind: "phrase", Lemma: "worth noting", Count: 9, Projects: 2},
		Entry{Kind: "phrase", Lemma: "that said", Count: 6, Projects: 2},
	)
	out := r.RenderHook(2, 1, nil)
	for _, want := range []string{"alpha", "beta", `"worth noting"`} {
		if !strings.Contains(out, want) {
			t.Errorf("capped view lost %s:\n%s", want, out)
		}
	}
	for _, drop := range []string{"gamma", "that said"} {
		if strings.Contains(out, drop) {
			t.Errorf("capped view leaked %s past its route budget:\n%s", drop, out)
		}
	}
	if got := r.RenderHook(0, 0, nil); !strings.Contains(got, "gamma") || !strings.Contains(got, "that said") {
		t.Errorf("0 must mean uncapped, the pre-cap behavior:\n%s", got)
	}
}

func TestRenderHookCutsNotesToOneSentence(t *testing.T) {
	r := &Report{Entries: []Entry{{
		Kind: "chronic", Lemma: "tracker", Rate: 0.3,
		JudgeNote: `Used loosely to mean "record" or "log" in most sentences. When the writer says "update tracker" they often mean "log the finding", not a named system.`,
		Ladder:    []Rung{{Word: "log", IC: 1}, {Word: "tracker", IC: 5}},
	}}}
	out := r.RenderHook(5, 2, nil)
	if !strings.Contains(out, "in most sentences.") {
		t.Errorf("first sentence of the note must survive:\n%s", out)
	}
	if strings.Contains(out, "named system") {
		t.Errorf("second sentence of the note must be cut:\n%s", out)
	}
}

func TestFirstSentence(t *testing.T) {
	cases := []struct{ in, want string }{
		{`Used as filler (e.g., "cur roadmap," "cur ask") rather than as a precise term. "Individual" better captures it.`,
			`Used as filler (e.g., "cur roadmap," "cur ask") rather than as a precise term.`},
		{"One clause with no terminal boundary", "One clause with no terminal boundary"},
		{"Ends mid-list e.g. Bar and more after", "Ends mid-list e.g. Bar and more after"},
	}
	for _, c := range cases {
		if got := firstSentence(c.in); got != c.want {
			t.Errorf("firstSentence(%q):\n got %q\nwant %q", c.in, got, c.want)
		}
	}
}

// word builds a renderable word entry: Render needs a ladder before it will
// print one at all, which is why a fixture without rungs silently vanishes.
func word(kind, lemma string, known bool) Entry {
	return Entry{
		Kind: kind, Lemma: lemma, Known: known, RecentCount: 40, Ratio: 2.2,
		Ladder: []Rung{{Word: "plain", IC: 1}, {Word: lemma, IC: 2}},
	}
}

// The bug this exists to prevent: report order is risers first, so a
// first-come word cap spends every slot on them and the chronic lane is never
// heard. "load-bearing" sat at position 13 of 24 as a curated known tic,
// running near sixty a day, and was injected exactly never.
//
// load-bearing's rate here is deliberately the lowest of the bunch — this is
// the floor slot doing its job, not a tie or a fluke of ordering. Without the
// floor, the six higher-rate chronicN entries would take all three chronic
// slots and load-bearing would never appear.
func TestHookBudgetDoesNotStarveTheChronicLane(t *testing.T) {
	r := &Report{}
	for i := 0; i < 8; i++ {
		r.Entries = append(r.Entries, word("riser", fmt.Sprintf("riser%d", i), false))
	}
	for i := 0; i < 6; i++ {
		e := word("chronic", fmt.Sprintf("chronic%d", i), false)
		e.Rate = 2.0
		r.Entries = append(r.Entries, e)
	}
	lb := word("chronic", "load-bearing", true)
	lb.Rate = 0.3
	r.Entries = append(r.Entries, lb)

	out := r.RenderHook(5, 2, nil)
	if !strings.Contains(out, "load-bearing") {
		t.Errorf("the curated known tic never reached the injection:\n%s", out)
	}
	if !strings.Contains(out, "riser0") {
		t.Errorf("risers lost their share entirely:\n%s", out)
	}
}

// A week with nothing chronic must still fill the word budget rather than
// shrinking the injection to the riser half.
func TestOneEmptyLaneSpillsToTheOther(t *testing.T) {
	r := &Report{}
	for i := 0; i < 8; i++ {
		r.Entries = append(r.Entries, word("riser", fmt.Sprintf("riser%d", i), false))
	}
	out := r.RenderHook(5, 2, nil)
	for i := 0; i < 5; i++ {
		if !strings.Contains(out, fmt.Sprintf("riser%d", i)) {
			t.Errorf("riser%d missing — the chronic share was not spilled back:\n%s", i, out)
		}
	}
}

// The same shape as the lane bug, one layer down: a slot spent on an entry
// Render then drops shrinks the injection with nothing backfilling it. The
// ladder is sorted IC-ascending, so a lemma weaker than every candidate it
// gathered lands at index 0 with no rung below it to demote to.
func TestBudgetSkipsWhatRenderDrops(t *testing.T) {
	dead := Entry{
		Kind: "chronic", Lemma: "floor", RecentCount: 40, Ratio: 2.2,
		Ladder: []Rung{{Word: "floor", IC: 1}, {Word: "stronger", IC: 9}},
	}
	r := &Report{Entries: []Entry{dead}}
	if out := r.RenderHook(5, 2, nil); strings.Contains(out, "floor") {
		t.Fatalf("an entry with no rung below the lemma must not render:\n%s", out)
	}
	for i := 0; i < 3; i++ {
		r.Entries = append(r.Entries, word("chronic", fmt.Sprintf("chronic%d", i), false))
	}
	for i := 0; i < 3; i++ {
		r.Entries = append(r.Entries, word("riser", fmt.Sprintf("riser%d", i), false))
	}
	// Five renderable words: three chronic, two risers. chronic2 is the tell —
	// it only makes the cut if the dead entry never took a slot.
	out := r.RenderHook(5, 2, nil)
	for _, want := range []string{"chronic0", "chronic1", "chronic2", "riser0", "riser1"} {
		if !strings.Contains(out, want) {
			t.Errorf("%s missing — the dead entry ate a budget slot:\n%s", want, out)
		}
	}
}

// A curated entry is the writer's standing instruction, so it gets the one
// floor slot regardless of rate — unlike the five automatic entries here,
// all of which run at a higher rate and would otherwise out-rank it.
func TestKnownTicGetsTheFloorSlotEvenAtLowRate(t *testing.T) {
	r := &Report{}
	for i := 0; i < 5; i++ {
		e := word("chronic", fmt.Sprintf("auto%d", i), false)
		e.Rate = 3.0
		r.Entries = append(r.Entries, e)
	}
	curated := word("chronic", "curated", true)
	curated.Rate = 0.1
	r.Entries = append(r.Entries, curated)
	if out := r.RenderHook(5, 2, nil); !strings.Contains(out, "curated") {
		t.Errorf("a curated tic lost its floor slot to automatic ones:\n%s", out)
	}
}

// Replaces the old categorical rule this test used to document: curated-first
// as a total order, not a tiebreak, meant three renderable curated words took
// every chronic slot and automatically-detected ones were unreachable however
// high their rate. Measured live 2026-09-10: curating a 4th known-tic
// ("running", the corpus's top full-window rate) silently evicted a 3rd
// ("arm") on pure rate, with no distinction between "arm's lean faded" and
// "arm lost a coincidence." See DESIGN.md and CHANGELOG.
//
// The replacement: exactly one floor slot for the least-recently-injected
// known-tic, regardless of its rate — curated0 here, never injected — and
// every other chronic slot decided by rate alone, known or not. curated1 and
// curated2 are curated too, but neither is the floor pick and both run at a
// lower rate than auto0/auto1, so neither gets a slot this round — proving
// the floor guarantees one known-tic, not the whole curated bucket.
func TestChronicLaneFloorProtectsOneKnownTicWithoutStarvingHigherRateEntries(t *testing.T) {
	r := &Report{}
	rates := []float64{5.0, 5.0, 1.0, 1.0, 1.0, 1.0, 1.0, 1.0, 1.0, 1.0}
	for i, rate := range rates {
		e := word("chronic", fmt.Sprintf("auto%d", i), false)
		e.Rate = rate
		r.Entries = append(r.Entries, e)
	}
	curated0 := word("chronic", "curated0", true)
	curated0.Rate = 0.3
	curated1 := word("chronic", "curated1", true)
	curated1.Rate = 0.4
	curated2 := word("chronic", "curated2", true)
	curated2.Rate = 0.35
	r.Entries = append(r.Entries, curated0, curated1, curated2)
	// risers present, so the chronic share stays at three rather than
	// spilling into the empty riser slots
	for i := 0; i < 3; i++ {
		r.Entries = append(r.Entries, word("riser", fmt.Sprintf("riser%d", i), false))
	}

	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	ledger := &Ledger{Lemmas: map[string]*LedgerEntry{
		// curated0 has no ledger entry: never injected, oldest by construction.
		"curated1": {LastInjected: now},
		"curated2": {LastInjected: now},
	}}

	shown := r.HookEntries(5, 0, ledger)
	var chronicShown []string
	for _, e := range shown {
		if e.Kind == "chronic" {
			chronicShown = append(chronicShown, e.Lemma)
		}
	}
	want := map[string]bool{"curated0": true, "auto0": true, "auto1": true}
	if len(chronicShown) != 3 {
		t.Fatalf("chronic share = %v, want three slots", chronicShown)
	}
	for _, got := range chronicShown {
		if !want[got] {
			t.Errorf("chronic slots = %v, want exactly %v", chronicShown, want)
		}
	}
	for _, unwanted := range []string{"curated1", "curated2"} {
		if strings.Contains(strings.Join(chronicShown, ","), unwanted) {
			t.Errorf("%s is curated but not the floor pick and loses on rate — it must not appear: %v", unwanted, chronicShown)
		}
	}
}

// orderChronicLane's own contract, isolated from HookEntries's budget/spill
// logic: the floor goes to the least-recently-injected known entry, a
// never-injected one beats any real timestamp, and everything else — known
// or not — is ranked by rate alone.
func TestOrderChronicLaneFloorPicksLeastRecentlyInjected(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	chronic := []Entry{
		{Lemma: "shown-recently", Known: true, Rate: 0.9},
		{Lemma: "never-shown", Known: true, Rate: 0.1},
		{Lemma: "shown-long-ago", Known: true, Rate: 0.5},
		{Lemma: "not-curated", Known: false, Rate: 5.0},
	}
	ledger := &Ledger{Lemmas: map[string]*LedgerEntry{
		"shown-recently": {LastInjected: now},
		"shown-long-ago": {LastInjected: now.Add(-30 * 24 * time.Hour)},
		// "never-shown" has no ledger entry at all — the case that matters most.
	}}
	got := orderChronicLane(chronic, ledger)
	if len(got) != 4 || got[0].Lemma != "never-shown" {
		t.Fatalf("floor must go to the never-injected known entry, got %v", lemmaOrder(got))
	}
	// Only the floor slot cares about recency. Everything past it — including
	// shown-long-ago, a known entry that just lost the floor to never-shown —
	// is ranked by Rate alone, so shown-recently (0.9) outranks it (0.5).
	want := []string{"never-shown", "not-curated", "shown-recently", "shown-long-ago"}
	if lo := lemmaOrder(got); strings.Join(lo, ",") != strings.Join(want, ",") {
		t.Errorf("got order %v, want %v — floor first, then rate descending regardless of Known", lo, want)
	}
}

func TestOrderChronicLaneNilLedgerDoesNotPanic(t *testing.T) {
	chronic := []Entry{
		{Lemma: "a", Known: true, Rate: 0.1},
		{Lemma: "b", Known: false, Rate: 5.0},
	}
	got := orderChronicLane(chronic, nil)
	if len(got) != 2 || got[0].Lemma != "a" {
		t.Errorf("nil ledger must still give the known entry the floor deterministically, got %v", lemmaOrder(got))
	}
}

func lemmaOrder(es []Entry) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Lemma
	}
	return out
}

// RenderHook must print exactly what HookEntries selects — the count is
// recorded from one and read from the other, so a divergence would log words
// that were never shown.
func TestHookEntriesMatchesWhatRenderHookPrints(t *testing.T) {
	r := &Report{}
	for i := 0; i < 6; i++ {
		r.Entries = append(r.Entries, word("chronic", fmt.Sprintf("chronic%d", i), i < 2))
	}
	for i := 0; i < 4; i++ {
		r.Entries = append(r.Entries, word("riser", fmt.Sprintf("riser%d", i), false))
	}
	r.Entries = append(r.Entries, Entry{Kind: "phrase", Lemma: "worth noting", Count: 9})

	out := r.RenderHook(5, 1, nil)
	picked := r.HookEntries(5, 1, nil)
	if len(picked) != 6 {
		t.Fatalf("picked %d entries, want five words and one phrase", len(picked))
	}
	for _, e := range picked {
		if !strings.Contains(out, e.Lemma) {
			t.Errorf("%q was selected but not printed:\n%s", e.Lemma, out)
		}
	}
	for _, e := range r.Entries {
		wasPicked := false
		for _, p := range picked {
			if p.Lemma == e.Lemma {
				wasPicked = true
			}
		}
		if !wasPicked && strings.Contains(out, e.Lemma) {
			t.Errorf("%q was printed but not selected — the count would miss it", e.Lemma)
		}
	}
}
