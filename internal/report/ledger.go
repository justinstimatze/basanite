package report

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// LedgerName is the ledger file, kept beside report.json in the state dir.
const LedgerName = "ledger.json"

// LedgerEntry is one flagged lemma's cross-refresh history: when it first
// entered a report, its rate then and now, and whether it has since faded
// out. The report's Spark shows a tic's trajectory *within* one refresh; the
// ledger is the trajectory *across* refreshes — and, unlike the spark, it
// records the fade-out (a word that drops from the report leaves no entry, so
// only the ledger can say "beaten on this date"). It is recorded, not proof:
// a falling rate is consistent with the awareness loop working, but direct
// callouts and topic drift are unmeasured confounds.
type LedgerEntry struct {
	Lemma        string    `json:"lemma"`
	Kind         string    `json:"kind"`
	FirstFlagged time.Time `json:"first_flagged"`
	FirstRate    float64   `json:"first_rate"`
	LastSeen     time.Time `json:"last_seen"`
	LastRate     float64   `json:"last_rate"`
	Refreshes    int       `json:"refreshes"` // times seen flagged
	Faded        bool      `json:"faded"`     // absent from the most recent refresh
	FadedAt      time.Time `json:"faded_at,omitempty"`
}

// Ledger maps each flagged lemma to its history. It is the persisted
// before/after record the manual `trend` eyeball never wrote down.
type Ledger struct {
	Lemmas map[string]*LedgerEntry `json:"lemmas"`
}

// LoadLedger reads the ledger; a missing file is an empty ledger, not an
// error (the first refresh has nothing to load). Kept lenient like Load: the
// refresh must never fail on ledger trouble.
func LoadLedger(path string) (*Ledger, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &Ledger{Lemmas: map[string]*LedgerEntry{}}, nil
	}
	if err != nil {
		return nil, err
	}
	var l Ledger
	if err := json.Unmarshal(b, &l); err != nil {
		return nil, err
	}
	if l.Lemmas == nil {
		l.Lemmas = map[string]*LedgerEntry{}
	}
	return &l, nil
}

// LedgerPath is the default ledger location, beside the report.
func LedgerPath() (string, error) {
	dir, err := StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, LedgerName), nil
}

// Update folds one refresh's report into the ledger: new lemmas get a
// first_flagged stamp, returning ones get their rate and count updated, and
// any tracked lemma absent from this report fades out (once — a later
// reappearance clears the fade and keeps the original first_flagged, so the
// stamp always means the true first sighting).
func (l *Ledger) Update(rep *Report, now time.Time) {
	if l.Lemmas == nil {
		l.Lemmas = map[string]*LedgerEntry{}
	}
	present := make(map[string]bool, len(rep.Entries))
	for _, e := range rep.Entries {
		present[e.Lemma] = true
		kind := e.Kind
		if kind == "" {
			kind = "riser"
		}
		le := l.Lemmas[e.Lemma]
		if le == nil {
			le = &LedgerEntry{Lemma: e.Lemma, FirstFlagged: now, FirstRate: e.Rate}
			l.Lemmas[e.Lemma] = le
		}
		le.Kind = kind
		le.LastSeen = now
		le.LastRate = e.Rate
		le.Refreshes++
		le.Faded = false
		le.FadedAt = time.Time{}
	}
	for lemma, le := range l.Lemmas {
		if !present[lemma] && !le.Faded {
			le.Faded = true
			le.FadedAt = now
		}
	}
}

// Save writes the ledger atomically (exclusive temp + rename), matching
// Report.Save so a crashed refresh can't leave a half-written ledger.
func (l *Ledger) Save(path string) error {
	b, err := json.MarshalIndent(l, "", " ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".ledger-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// delta is the signed percent change from first to last rate; negative means
// the rate fell (the loop's intended direction). Returns 0 when there is no
// baseline to divide by.
func (e LedgerEntry) delta() float64 {
	if e.FirstRate == 0 {
		return 0
	}
	return (e.LastRate - e.FirstRate) / e.FirstRate * 100
}

// arrow renders the delta's direction for the eyeball scan: ↓ is the tic
// fading, ↑ is it climbing, → is flat.
func arrow(delta float64) string {
	switch {
	case delta <= -1:
		return "↓"
	case delta >= 1:
		return "↑"
	default:
		return "→"
	}
}

// Render is the human view: still-flagged tics first (longest-standing —
// the ones the loop hasn't beaten yet — at the top), then faded-out ones
// (most recently beaten first). This is the reassurance surface: at a glance,
// is a tic's rate falling since it was first flagged, and which have dropped
// out entirely.
func (l *Ledger) Render(now time.Time) string {
	if len(l.Lemmas) == 0 {
		return "basanite ledger — no tics recorded yet; it fills in as the report refreshes.\n"
	}
	var live, faded []*LedgerEntry
	for _, e := range l.Lemmas {
		if e.Faded {
			faded = append(faded, e)
		} else {
			live = append(live, e)
		}
	}
	sort.Slice(live, func(i, j int) bool {
		return live[i].FirstFlagged.Before(live[j].FirstFlagged)
	})
	sort.Slice(faded, func(i, j int) bool {
		return faded[i].FadedAt.After(faded[j].FadedAt)
	})

	var b strings.Builder
	b.WriteString("basanite ledger — flagged tics over time (rate = per-1k words; ↓ = fading since first flagged).\n")
	b.WriteString("Recorded, not proof: direct callouts and topic drift are unmeasured confounds.\n")

	if len(live) > 0 {
		b.WriteString("\nstill flagged:\n")
		for _, e := range live {
			d := e.delta()
			fmt.Fprintf(&b, "  %-16s since %s (%s, %d refresh%s)  %.2f → %.2f/1k  %s%.0f%%  %s\n",
				e.Lemma, e.FirstFlagged.Format("2006-01-02"), humanAge(now.Sub(e.FirstFlagged)),
				e.Refreshes, plural(e.Refreshes), e.FirstRate, e.LastRate,
				arrow(d), absPct(d), e.Kind)
		}
	}
	if len(faded) > 0 {
		b.WriteString("\nfaded out (dropped from the report):\n")
		for _, e := range faded {
			d := e.delta()
			fmt.Fprintf(&b, "  %-16s %s → gone %s  %.2f → %.2f/1k  %s%.0f%%  %s\n",
				e.Lemma, e.FirstFlagged.Format("2006-01-02"), e.FadedAt.Format("2006-01-02"),
				e.FirstRate, e.LastRate, arrow(d), absPct(d), e.Kind)
		}
	}
	return b.String()
}

func absPct(d float64) float64 {
	if d < 0 {
		return -d
	}
	return d
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "es"
}

// humanAge renders a duration as weeks or days for the "since" column.
func humanAge(d time.Duration) string {
	days := int(d.Hours() / 24)
	if days >= 14 {
		return fmt.Sprintf("%dw", days/7)
	}
	if days <= 0 {
		return "today"
	}
	return fmt.Sprintf("%dd", days)
}
