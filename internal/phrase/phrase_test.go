package phrase

import (
	"reflect"
	"testing"

	"github.com/justinstimatze/basanite/internal/text"
)

func count(m *Matcher, s string) map[string]int {
	out := map[string]int{}
	m.Count(text.Words(s), out)
	return out
}

func TestCountMatchesAndCases(t *testing.T) {
	m := New([]string{"I want to honor that", "take your time"})
	// surface match regardless of input case, stopwords kept
	got := count(m, "okay i want to honor that and also take your time please")
	want := map[string]int{"i want to honor that": 1, "take your time": 1}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestCountRepeats(t *testing.T) {
	m := New([]string{"that's not nothing"})
	got := count(m, "that's not nothing. and again that's not nothing here")
	if got["that's not nothing"] != 2 {
		t.Errorf("repeat count = %d, want 2 (%v)", got["that's not nothing"], got)
	}
}

func TestSingleWordIgnored(t *testing.T) {
	m := New([]string{"substrate", " ", "take your time"})
	if _, ok := m.byFirst["substrate"]; ok {
		t.Error("a single word must not become a phrase")
	}
	if m.Empty() {
		t.Error("the multi-word phrase should still be present")
	}
}

func TestEmptyMatcherNoHits(t *testing.T) {
	var m *Matcher
	if !m.Empty() {
		t.Error("nil matcher should report empty")
	}
	got := map[string]int{}
	m.Count([]string{"anything", "here"}, got) // must not panic
	if len(got) != 0 {
		t.Errorf("nil matcher produced hits: %v", got)
	}
	if !New(nil).Empty() {
		t.Error("matcher built from no phrases should be empty")
	}
}

// Overlapping phrases sharing a tail must not double-count: the longer match
// at a position consumes the shared words.
func TestOverlapAdvancesPastLongest(t *testing.T) {
	m := New([]string{"stay with that", "let's stay with that"})
	got := count(m, "okay let's stay with that for now")
	if got["let's stay with that"] != 1 || got["stay with that"] != 0 {
		t.Errorf("overlap mishandled: %v", got)
	}
}
