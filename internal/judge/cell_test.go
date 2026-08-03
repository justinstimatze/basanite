package judge

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

// recorder captures what the cell sent, so the test asserts the strict-tool
// request shape without a real API.
type recorder struct {
	calls  int
	apiKey string
	body   map[string]any
}

// verdictServer returns a fake Anthropic endpoint that replies with a fixed
// tool_use verdict (or an error when verdict is nil).
func verdictServer(t *testing.T, verdict map[string]any, apiErr string) (*httptest.Server, *recorder) {
	t.Helper()
	rec := &recorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.calls++
		rec.apiKey = r.Header.Get("x-api-key")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &rec.body)
		var resp map[string]any
		if apiErr != "" {
			resp = map[string]any{"error": map[string]any{"message": apiErr}}
		} else {
			resp = map[string]any{"content": []map[string]any{
				{"type": "tool_use", "name": "verdict", "input": verdict},
			}}
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

func testJudge(t *testing.T, endpoint string) *cellJudge {
	t.Helper()
	store, err := LoadStore(filepath.Join(t.TempDir(), "verdicts.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	return &cellJudge{
		model: "test-model", apiKey: "test-key", store: store,
		client: http.DefaultClient, endpoint: endpoint,
		now: func() time.Time { return time.Unix(0, 0).UTC() },
	}
}

func TestJudgeTermOfArtAndRequestShape(t *testing.T) {
	srv, rec := verdictServer(t, map[string]any{"role": "term_of_art", "demote_to": "none", "note": "fixed referent"}, "")
	j := testJudge(t, srv.URL)

	v, ok := j.Judge("hook", []string{"snare", "bait"}, [][]string{{"the", "hook", "fires"}})
	if !ok || v.Role != RoleTermOfArt || v.DemoteTo != "" {
		t.Fatalf("verdict = %+v ok=%v, want term_of_art with no demote", v, ok)
	}
	// the strict-tool request: forced verdict tool, schema present, cached system, auth header
	if rec.apiKey != "test-key" {
		t.Errorf("x-api-key = %q", rec.apiKey)
	}
	tc, _ := rec.body["tool_choice"].(map[string]any)
	if tc["type"] != "tool" || tc["name"] != "verdict" {
		t.Errorf("tool_choice = %v, want forced verdict", tc)
	}
	sys, _ := rec.body["system"].([]any)
	if len(sys) == 0 {
		t.Fatal("system block missing")
	}
	if _, cached := sys[0].(map[string]any)["cache_control"]; !cached {
		t.Error("instructions not marked for prompt caching")
	}
}

func TestJudgeCachesByWordAndLadder(t *testing.T) {
	srv, rec := verdictServer(t, map[string]any{"role": "tic", "demote_to": "layer", "note": "loose"}, "")
	j := testJudge(t, srv.URL)
	ladder := []string{"layer", "surface"}
	for i := 0; i < 3; i++ {
		if _, ok := j.Judge("substrate", ladder, [][]string{{"the", "substrate", "holds"}}); !ok {
			t.Fatal("expected ok verdict")
		}
	}
	if rec.calls != 1 {
		t.Errorf("server calls = %d, want 1 (subsequent reads hit the cache)", rec.calls)
	}
}

func TestJudgeFailsSafeOnOffLadderVerdict(t *testing.T) {
	// the model returns a demotion that is NOT in the offered ladder — the
	// grammar backstop must reject it (not well-formed) so the gate fails safe
	srv, _ := verdictServer(t, map[string]any{"role": "tic", "demote_to": "bedrock", "note": "x"}, "")
	j := testJudge(t, srv.URL)
	if _, ok := j.Judge("substrate", []string{"layer", "surface"}, [][]string{{"the", "substrate", "holds"}}); ok {
		t.Error("an off-ladder demotion must fail safe, not pass")
	}
}

func TestJudgeFailsSafeOnIncoherentVerdict(t *testing.T) {
	// term_of_art with a demotion is incoherent — the safety stage rejects it
	srv, _ := verdictServer(t, map[string]any{"role": "term_of_art", "demote_to": "layer", "note": "x"}, "")
	j := testJudge(t, srv.URL)
	if _, ok := j.Judge("substrate", []string{"layer"}, [][]string{{"the", "substrate", "holds"}}); ok {
		t.Error("a term_of_art offering a demotion is incoherent and must fail safe")
	}
}

func TestJudgeFailsSafeOnAPIError(t *testing.T) {
	srv, _ := verdictServer(t, nil, "overloaded")
	j := testJudge(t, srv.URL)
	if _, ok := j.Judge("substrate", []string{"layer"}, [][]string{{"the", "substrate", "holds"}}); ok {
		t.Error("an API error must fail safe to the un-gated entry")
	}
}

// A refused verdict must not become a permanent one. The record is appended
// before the safety check runs, so serving it back as a cache hit means the
// word fails safe on every future run for that ladder — silently, and
// unrecoverably short of a schema bump. This is not hypothetical: relaxing
// Safety to allow "tic with nothing to offer" left six words stuck behind
// their own earlier refusals until this was fixed.
func TestRefusedVerdictIsNotServedFromCache(t *testing.T) {
	// first call is incoherent (a term of art offering a swap) and gets
	// refused; every call after it is a clean tic verdict
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		verdict := map[string]any{"role": "tic", "demote_to": "layer", "note": "loose"}
		if calls == 1 {
			verdict = map[string]any{"role": "term_of_art", "demote_to": "layer", "note": "x"}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"content": []map[string]any{
			{"type": "tool_use", "name": "verdict", "input": verdict},
		}})
	}))
	t.Cleanup(srv.Close)

	j := testJudge(t, srv.URL)
	ladder := []string{"layer", "surface"}
	samples := [][]string{{"the", "substrate", "holds"}}

	if _, ok := j.Judge("substrate", ladder, samples); ok {
		t.Fatal("precondition: the incoherent verdict should have been refused")
	}
	if r, found := j.store.Lookup("substrate", ladder, j.model); !found || r.Safe {
		t.Fatal("precondition: the refusal should be recorded as calibration data")
	}

	// Same word, same ladder, same schema. A refusal is not an answer, so the
	// gate must ask again rather than serve it back.
	v, ok := j.Judge("substrate", ladder, samples)
	if !ok || v.Role != RoleTic || v.DemoteTo != "layer" {
		t.Fatalf("verdict = %+v ok=%v, want the fresh tic verdict", v, ok)
	}
	if calls != 2 {
		t.Errorf("server calls = %d, want 2 — the refusal must not short-circuit the retry", calls)
	}
}

// audit reads verdicts through Latest, which did not apply the condition
// Judge applies. So a word could be reported as a suppressed term of art on
// the strength of a verdict the gate itself threw away.
func TestLatestSkipsARefusedVerdict(t *testing.T) {
	st, err := LoadStore(filepath.Join(t.TempDir(), "verdicts.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	refused := Record{
		Word: "substrate", LadderHash: LadderHash([]string{"layer"}), Model: "m",
		SchemaVersion: SchemaVersion, Role: RoleTermOfArt, DemoteTo: "layer",
		WellFormed: true, Safe: false, At: "2026-01-01T00:00:00Z",
	}
	if err := st.Append(refused); err != nil {
		t.Fatal(err)
	}
	if _, ok := st.Latest("substrate"); ok {
		t.Fatal("Latest must skip a refused verdict the way Judge does")
	}

	// and it still returns a later well-formed one
	good := refused
	good.Role, good.DemoteTo, good.Safe, good.At = RoleTic, "layer", true, "2026-01-02T00:00:00Z"
	if err := st.Append(good); err != nil {
		t.Fatal(err)
	}
	if r, ok := st.Latest("substrate"); !ok || r.Role != RoleTic {
		t.Errorf("Latest = %+v ok=%v, want the accepted tic verdict", r, ok)
	}
}
