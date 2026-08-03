package report

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func rep(entries ...Entry) *Report {
	return &Report{Entries: entries}
}

// The ledger stamps first_flagged once, tracks the rate across refreshes,
// records the fade-out when a tic drops from the report, and — the property
// that makes first_flagged mean "true first" — keeps the original stamp when
// a faded tic reappears.
func TestLedgerLifecycle(t *testing.T) {
	day := 24 * time.Hour
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	l := &Ledger{}

	// refresh 1: load-bearing appears at 0.80/1k
	l.Update(rep(Entry{Kind: "chronic", Lemma: "load-bearing", Rate: 0.80}), base)
	lb := l.Lemmas["load-bearing"]
	if lb == nil || !lb.FirstFlagged.Equal(base) || lb.FirstRate != 0.80 {
		t.Fatalf("first flag not stamped: %+v", lb)
	}
	if lb.Refreshes != 1 || lb.Faded {
		t.Errorf("after one sighting: refreshes=%d faded=%v", lb.Refreshes, lb.Faded)
	}

	// refresh 2, a week later: rate has fallen to 0.60, first_flagged unchanged
	l.Update(rep(Entry{Kind: "chronic", Lemma: "load-bearing", Rate: 0.60}), base.Add(7*day))
	lb = l.Lemmas["load-bearing"]
	if !lb.FirstFlagged.Equal(base) || lb.FirstRate != 0.80 {
		t.Errorf("first_flagged/first_rate must not move on re-sighting: %+v", lb)
	}
	if lb.LastRate != 0.60 || lb.Refreshes != 2 {
		t.Errorf("re-sighting: lastRate=%.2f refreshes=%d", lb.LastRate, lb.Refreshes)
	}
	if lb.delta() >= 0 {
		t.Errorf("falling rate must yield negative delta, got %.1f", lb.delta())
	}

	// refresh 3: load-bearing is gone — it fades out, dated to this refresh
	fadeDay := base.Add(14 * day)
	l.Update(rep(Entry{Kind: "chronic", Lemma: "substrate", Rate: 1.0}), fadeDay)
	lb = l.Lemmas["load-bearing"]
	if !lb.Faded || !lb.FadedAt.Equal(fadeDay) {
		t.Errorf("absent tic must fade out at this refresh: faded=%v at=%v", lb.Faded, lb.FadedAt)
	}
	if lb.Refreshes != 2 {
		t.Errorf("a fade is not a sighting; refreshes should stay 2, got %d", lb.Refreshes)
	}

	// refresh 4: it reappears — fade clears, original first_flagged survives
	l.Update(rep(Entry{Kind: "chronic", Lemma: "load-bearing", Rate: 0.55}), base.Add(21*day))
	lb = l.Lemmas["load-bearing"]
	if lb.Faded || !lb.FirstFlagged.Equal(base) {
		t.Errorf("reappearance must clear fade and keep the true first_flagged: %+v", lb)
	}
}

// A missing ledger loads empty, and a round-trip through Save/LoadLedger
// preserves the record — the persistence the manual trend eyeball lacked.
func TestLedgerPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.json")
	l, err := LoadLedger(path)
	if err != nil || len(l.Lemmas) != 0 {
		t.Fatalf("missing ledger must load empty, got %v err=%v", l, err)
	}
	l.Update(rep(Entry{Kind: "chronic", Lemma: "surface", Rate: 0.4}), time.Now())
	if err := l.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := LoadLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Lemmas["surface"] == nil || got.Lemmas["surface"].FirstRate != 0.4 {
		t.Errorf("round-trip lost the entry: %+v", got.Lemmas)
	}
}

// Render groups still-flagged before faded-out and shows the rate delta, so
// the "is it working?" scan reads top-down.
func TestLedgerRender(t *testing.T) {
	now := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	l := &Ledger{Lemmas: map[string]*LedgerEntry{
		"load-bearing": {Lemma: "load-bearing", Kind: "chronic",
			FirstFlagged: now.Add(-28 * 24 * time.Hour), FirstRate: 0.80,
			LastSeen: now, LastRate: 0.60, Refreshes: 4},
		"substrate": {Lemma: "substrate", Kind: "chronic",
			FirstFlagged: now.Add(-40 * 24 * time.Hour), FirstRate: 1.0,
			LastRate: 0.2, Refreshes: 3, Faded: true, FadedAt: now.Add(-5 * 24 * time.Hour)},
	}}
	out := l.Render(now)
	if !strings.Contains(out, "still flagged:") || !strings.Contains(out, "faded out") {
		t.Fatalf("render missing section headers:\n%s", out)
	}
	if i, j := strings.Index(out, "load-bearing"), strings.Index(out, "substrate"); i > j {
		t.Errorf("still-flagged must render before faded-out:\n%s", out)
	}
	if !strings.Contains(out, "↓") {
		t.Errorf("falling tic should show a ↓ arrow:\n%s", out)
	}
	// empty ledger renders a friendly placeholder, not a blank
	if !strings.Contains((&Ledger{}).Render(now), "no tics recorded") {
		t.Error("empty ledger must render a placeholder")
	}
}

// Refreshes counts report membership; Injected counts what reached a prompt.
// They diverge because the injection takes a fixed handful of the report, and
// the whole point of the second counter is that the gap is invisible from
// every other surface — a word in every report and no prompt looks exactly
// like a word being ranked fairly and losing.
func TestInjectedCountIsNotRefreshCount(t *testing.T) {
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	l := &Ledger{}
	for i := 0; i < 5; i++ {
		l.Update(rep(
			Entry{Kind: "chronic", Lemma: "substrate", Rate: 0.9, Known: true},
			Entry{Kind: "chronic", Lemma: "running", Rate: 1.8},
		), base.AddDate(0, 0, i))
		// only the curated word is ever picked for a slot
		l.RecordInjection([]string{"substrate"}, base.AddDate(0, 0, i))
	}

	sub, run := l.Lemmas["substrate"], l.Lemmas["running"]
	if sub.Refreshes != 5 || sub.Injected != 5 {
		t.Errorf("substrate = %d refreshes / %d injected, want 5/5", sub.Refreshes, sub.Injected)
	}
	if run.Refreshes != 5 {
		t.Errorf("running was in every report: refreshes=%d", run.Refreshes)
	}
	if run.Injected != 0 {
		t.Errorf("running never reached a prompt: injected=%d", run.Injected)
	}
	if run.LastInjected.IsZero() != true {
		t.Error("a never-injected word has no last-injected stamp")
	}
}

// An injected lemma was in the report, and the refresh that built it is what
// creates the ledger entry. A miss means the ledger was cleared, and inventing
// an entry here would stamp a first_flagged that is not the true first
// sighting — the one property TestLedgerLifecycle pins down.
func TestRecordInjectionDoesNotInventEntries(t *testing.T) {
	l := &Ledger{Lemmas: map[string]*LedgerEntry{}}
	l.RecordInjection([]string{"ghost"}, time.Now())
	if _, ok := l.Lemmas["ghost"]; ok {
		t.Error("an untracked lemma must not be created by the shown-counter")
	}
}

// The render has to say "never shown" out loud. A zero in a column of small
// numbers is not noticeable, and unnoticeable is the failure mode this counter
// exists to end.
func TestRenderNamesTheNeverShown(t *testing.T) {
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	l := &Ledger{}
	l.Update(rep(
		Entry{Kind: "chronic", Lemma: "substrate", Rate: 0.9},
		Entry{Kind: "chronic", Lemma: "running", Rate: 1.8},
	), base)
	l.RecordInjection([]string{"substrate"}, base)

	out := l.Render(base.AddDate(0, 0, 1))
	if !strings.Contains(out, "shown 1×") {
		t.Errorf("an injected word shows its count:\n%s", out)
	}
	if !strings.Contains(out, "never shown") {
		t.Errorf("and one that never reached a prompt says so:\n%s", out)
	}
	if !strings.Contains(out, "1 of 2 still-flagged never reached a prompt") {
		t.Errorf("with a tally, so the scale is visible:\n%s", out)
	}
}
