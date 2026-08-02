package display

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The swap log is the only record the substitution happened — the transcript
// keeps the original word — so a round-trip has to survive, and a torn line
// from the hot-path hook must cost one record rather than the history.
func TestLogRoundTripAndTolerance(t *testing.T) {
	path := filepath.Join(t.TempDir(), LogName)
	got, err := LoadLog(path)
	if err != nil || len(got) != 0 {
		t.Fatalf("a missing log must read empty, got %v err=%v", got, err)
	}
	s := swaps()
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	AppendLog(path, map[string]int{"load-bearing": 2, "substrate": 1}, s, now)
	AppendLog(path, map[string]int{"load-bearing": 1}, s, now.Add(24*time.Hour))
	AppendLog(path, nil, s, now) // nothing swapped: writes nothing

	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString("{ this is not json\n")
	f.Close()

	got, err = LoadLog(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 records (the malformed line skipped), got %d: %v", len(got), got)
	}
	if got[0].Lemma != "load-bearing" || got[0].To != "supporting" || got[0].Count != 2 {
		t.Errorf("first record lost detail: %+v", got[0])
	}
}

func TestRenderLogTotalsAndOrder(t *testing.T) {
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	log := []Swap{
		{At: now.Add(-2 * day), Lemma: "substrate", To: "component", Count: 3},
		{At: now.Add(-1 * day), Lemma: "load-bearing", To: "supporting", Count: 10},
		{At: now, Lemma: "load-bearing", To: "supporting", Count: 5},
	}
	out := RenderLog(log, now)
	if i, j := strings.Index(out, "load-bearing"), strings.Index(out, "substrate"); i > j {
		t.Errorf("the most-replaced tic must render first:\n%s", out)
	}
	if !strings.Contains(out, "15×") {
		t.Errorf("per-lemma counts must sum across records:\n%s", out)
	}
	if !strings.Contains(out, "18 replacements over 3 days") {
		t.Errorf("footer total/span wrong:\n%s", out)
	}
	// The distinction the whole feature rests on.
	if !strings.Contains(out, "model still wrote them") {
		t.Errorf("render must not let this be read as a rate change:\n%s", out)
	}
	if !strings.Contains(RenderLog(nil, now), "nothing replaced yet") {
		t.Error("an empty log needs a placeholder, not a blank")
	}
}
