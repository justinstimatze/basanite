package judge

import (
	"encoding/json"
	"strings"
	"testing"
)

var ladder = []string{"layer", "surface", "stratum"}

func TestSchemaConfinesDemoteToLadder(t *testing.T) {
	s := Schema(ladder)
	props := s["properties"].(map[string]any)
	demote := props["demote_to"].(map[string]any)
	enum := demote["enum"].([]string)
	// the model can only emit "none" or a vetted rung — generation-time confinement
	want := map[string]bool{"none": true, "layer": true, "surface": true, "stratum": true}
	if len(enum) != len(want) {
		t.Fatalf("demote_to enum = %v, want %v", enum, want)
	}
	for _, e := range enum {
		if !want[e] {
			t.Errorf("demote_to enum has unvetted %q", e)
		}
	}
	if s["additionalProperties"] != false {
		t.Error("schema must set additionalProperties:false for strict tool use")
	}
}

func TestGrammar(t *testing.T) {
	g := Grammar(ladder)
	cases := []struct {
		name string
		raw  string
		term string
		ok   bool
	}{
		{"valid tic", `{"role":"tic","demote_to":"layer","note":"x"}`, "tic:layer", true},
		{"valid term of art", `{"role":"term_of_art","demote_to":"none","note":"x"}`, "term_of_art:none", true},
		{"valid mixed", `{"role":"mixed","demote_to":"surface","note":"x"}`, "mixed:surface", true},
		{"malformed json", `{"role":`, "", false},
		{"bad role", `{"role":"crutch","demote_to":"layer","note":"x"}`, "", false},
		{"demote not in ladder", `{"role":"tic","demote_to":"snare","note":"x"}`, "", false},
	}
	for _, c := range cases {
		term, ok := g(c.raw)
		if ok != c.ok || term != c.term {
			t.Errorf("%s: g(%s) = (%q,%v), want (%q,%v)", c.name, c.raw, term, ok, c.term, c.ok)
		}
	}
}

func TestSafetyRejectsIncoherent(t *testing.T) {
	s := Safety()
	cases := []struct {
		term string
		safe bool
	}{
		{"tic:layer", true},          // tic names a rung — actionable
		{"tic:none", false},          // tic with no rung — useless
		{"term_of_art:none", true},   // keep, no substitute
		{"term_of_art:layer", false}, // contradiction: a term of art offering a swap
		{"mixed:none", true},         // mixed may abstain
		{"mixed:surface", true},      // mixed may demote
		{"garbage-no-colon", false},  // not a term shape
	}
	for _, c := range cases {
		if got := s(c.term); got != c.safe {
			t.Errorf("Safety(%q) = %v, want %v", c.term, got, c.safe)
		}
	}
}

func TestParseCarriesNoteAndClearsNone(t *testing.T) {
	v, ok := Parse(`{"role":"term_of_art","demote_to":"none","note":"hook is the harness concept"}`)
	if !ok || v.Role != RoleTermOfArt || v.DemoteTo != "" || v.Note == "" {
		t.Fatalf("Parse term-of-art = %+v ok=%v", v, ok)
	}
	v, _ = Parse(`{"role":"tic","demote_to":"layer","note":"loose"}`)
	if v.DemoteTo != "layer" {
		t.Errorf("Parse tic demote = %q, want layer", v.DemoteTo)
	}
}

// The schema must round-trip through encoding/json — it is handed verbatim to
// the strict-tool call, so a non-serializable shape would fail at runtime.
func TestSchemaSerializable(t *testing.T) {
	if _, err := json.Marshal(Schema(ladder)); err != nil {
		t.Fatal(err)
	}
}

func TestPayload(t *testing.T) {
	p := Payload("substrate", ladder, [][]string{{"the", "substrate", "held"}})
	for _, want := range []string{"substrate", "layer, surface, stratum", "the substrate held"} {
		if !strings.Contains(p, want) {
			t.Errorf("payload missing %q:\n%s", want, p)
		}
	}
}
