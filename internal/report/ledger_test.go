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
