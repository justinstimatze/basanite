package judge

import (
	"path/filepath"
	"strings"
	"testing"
)

func rec(word, hash, role, demote string, schema int, safe bool) Record {
	return Record{
		Word: word, LadderHash: hash, Model: "m", SchemaVersion: schema,
		Role: role, DemoteTo: demote, WellFormed: true, Safe: safe,
		At: "2026-01-01T00:00:00Z",
	}
}

func find(t *testing.T, cs []Churn, word string) Churn {
	t.Helper()
	for _, c := range cs {
		if c.Word == word {
			return c
		}
	}
	t.Fatalf("no churn row for %q", word)
	return Churn{}
}

func TestChurnCountsDistinctAnswersPerWord(t *testing.T) {
	cs := ChurnBy([]Record{
		// same word, three ladders, two roles and two rungs
		rec("substrate", "aaa", RoleTic, "layer", 4, true),
		rec("substrate", "bbb", RoleTic, "component", 4, true),
		rec("substrate", "ccc", RoleTermOfArt, "", 4, true),
		// steady across two ladders
		rec("spec", "aaa", RoleTic, "specification", 4, true),
		rec("spec", "bbb", RoleTic, "specification", 4, true),
	})

	sub := find(t, cs, "substrate")
	if sub.Ladders != 3 || sub.Verdicts != 3 {
		t.Errorf("substrate = %d ladders / %d verdicts, want 3/3", sub.Ladders, sub.Verdicts)
	}
	if !sub.Unstable() {
		t.Error("two roles and two rungs is unstable")
	}
	if !sub.CrossesTermOfArt() {
		t.Error("tic alternating with term_of_art crosses the suppress boundary")
	}
	if got := strings.Join(sub.Rungs, "/"); got != "layer/component/-" {
		t.Errorf("rungs = %q, want first-seen order layer/component/-", got)
	}

	sp := find(t, cs, "spec")
	if sp.Unstable() {
		t.Error("the same answer twice is not churn")
	}
	if sp.CrossesTermOfArt() {
		t.Error("one role cannot cross a boundary")
	}
}

// The index keeps one record per key, so a word re-judged against the SAME
// ladder loses its earlier verdict there. That collapse is exactly what churn
// measures, so the fold must read the log.
func TestChurnSeesRejudgementsTheCacheIndexCollapses(t *testing.T) {
	st, err := LoadStore(filepath.Join(t.TempDir(), "verdicts.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range []Record{
		rec("calibration", "same", RoleTic, "activity", 4, true),
		rec("calibration", "same", RoleTermOfArt, "", 4, true),
	} {
		if err := st.Append(r); err != nil {
			t.Fatal(err)
		}
	}
	if got := len(st.Records()); got != 2 {
		t.Fatalf("Records() = %d rows, want both writes — the log, not the index", got)
	}
	c := find(t, ChurnBy(st.Records()), "calibration")
	if c.Ladders != 1 {
		t.Errorf("ladders = %d, want 1 — same ladder both times", c.Ladders)
	}
	if !c.CrossesTermOfArt() {
		t.Error("re-judging the same ladder to a different role is the worst flip and must be seen")
	}
}

// A refusal is not the gate changing its mind — it never gave an answer. It
// gets its own count so a fence problem is not read as an instability problem.
func TestChurnSeparatesRefusalsFromFlips(t *testing.T) {
	c := find(t, ChurnBy([]Record{
		rec("arm", "aaa", RoleTic, "limb", 4, false),
		rec("arm", "bbb", RoleTic, "limb", 4, true),
	}), "arm")
	if c.Refused != 1 || c.Verdicts != 1 {
		t.Errorf("arm = %d refused / %d verdicts, want 1/1", c.Refused, c.Verdicts)
	}
	if c.Unstable() {
		t.Error("one accepted verdict cannot be unstable, whatever the refusal said")
	}
}

// A word with one ladder has had no opportunity to disagree with itself, so
// counting it as stable would flatter the number the diagnostic exists to
// report.
func TestRenderExcludesWordsWithASingleLadder(t *testing.T) {
	out := RenderChurn([]Record{
		rec("hook", "aaa", RoleTermOfArt, "", 4, true),
		rec("spec", "aaa", RoleTic, "specification", 4, true),
		rec("spec", "bbb", RoleTic, "specification", 4, true),
	})
	if !strings.Contains(out, "0 of 1 words judged against 2+ ladders") {
		t.Errorf("only spec qualifies; hook has one ladder:\n%s", out)
	}
}

func TestRenderFlagsTheTermOfArtCrossing(t *testing.T) {
	out := RenderChurn([]Record{
		rec("substrate", "aaa", RoleTic, "layer", 4, true),
		rec("substrate", "bbb", RoleTermOfArt, "", 4, true),
		rec("rest", "aaa", RoleTic, "remainder", 4, true),
		rec("rest", "bbb", RoleTic, "balance", 4, true),
	})
	if !strings.Contains(out, "! substrate") {
		t.Errorf("the suppress-boundary flip must be marked:\n%s", out)
	}
	if !strings.Contains(out, "1 of those crossed the term-of-art boundary") {
		t.Errorf("and counted:\n%s", out)
	}
	// rest flipped its rung but not its role — churn, not a crossing
	if strings.Contains(out, "! rest") {
		t.Errorf("a rung flip is not a boundary crossing:\n%s", out)
	}
	if !strings.Contains(out, "2 of 2 words") {
		t.Errorf("both words changed their answer:\n%s", out)
	}
}

// A schema bump reworded the question, so a word answering differently across
// one is basanite's own history in the log, not the gate being inconsistent.
// Since the bumps are what invalidate the cache, nearly every long-lived word
// has crossed one — counting those would make the headline number mostly
// noise, and it is the number that decides whether A3 gets built.
func TestChurnSeparatesPromptChangesFromRealInstability(t *testing.T) {
	cs := ChurnBy([]Record{
		// answered the same way each time the prompt was held still
		rec("toggle", "aaa", RoleTic, "switch", 3, true),
		rec("toggle", "bbb", RoleTic, "switch", 3, true),
		rec("toggle", "ccc", RoleTermOfArt, "", 4, true),
		// disagreed with itself under one prompt
		rec("panel", "aaa", RoleTic, "display", 4, true),
		rec("panel", "bbb", RoleMixed, "board", 4, true),
	})

	tog := find(t, cs, "toggle")
	if !tog.Unstable() {
		t.Error("precondition: toggle did change its answer overall")
	}
	if tog.UnstableWithinSchema() {
		t.Error("toggle only disagreed across a schema bump — that is the prompt changing, not the gate")
	}

	pan := find(t, cs, "panel")
	if !pan.UnstableWithinSchema() {
		t.Error("panel flipped both role and rung under one unchanged prompt")
	}
	if pan.SameSchemaTOA {
		t.Error("tic/mixed is not a term-of-art crossing")
	}

	out := RenderChurn([]Record{
		rec("toggle", "aaa", RoleTic, "switch", 3, true),
		rec("toggle", "bbb", RoleTermOfArt, "", 4, true),
		rec("panel", "aaa", RoleTic, "display", 4, true),
		rec("panel", "bbb", RoleMixed, "board", 4, true),
	})
	if !strings.Contains(out, "1 of 2 words") {
		t.Errorf("only panel counts toward the headline:\n%s", out)
	}
	if !strings.Contains(out, "only disagreed across a schema bump") {
		t.Errorf("toggle belongs in the discounted section:\n%s", out)
	}
	if !strings.Contains(out, "A further 1 disagreed only across a schema bump") {
		t.Errorf("and is tallied separately:\n%s", out)
	}
}

func TestRenderEmptyLog(t *testing.T) {
	if !strings.Contains(RenderChurn(nil), "no verdicts recorded yet") {
		t.Error("an empty log should say so, not render an empty table")
	}
}
