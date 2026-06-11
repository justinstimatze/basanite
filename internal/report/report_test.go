package report

import (
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
	out := sample().Render()
	if strings.Contains(out, "broker") {
		t.Error("rungs above the target must not render")
	}
	if strings.Contains(out, "delegate") {
		t.Error("ladder should trim to four rungs below the target")
	}
	if !strings.Contains(out, "functionary < official < negotiator < representative < [agent]") {
		t.Errorf("unexpected render:\n%s", out)
	}
	if (&Report{}).Render() != "" {
		t.Error("empty report must render to empty string")
	}
}

func TestRenderPhraseEntry(t *testing.T) {
	r := &Report{Entries: []Entry{
		{Kind: "phrase", Lemma: "i want to honor that", Count: 7, Projects: 3, Rate: 0.3},
	}}
	out := r.Render()
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
	out := r.Render()
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
	if !strings.Contains(r.Render(), "a common Claude lean") {
		t.Errorf("known-route entry must flag itself as a Claude lean:\n%s", r.Render())
	}
}
