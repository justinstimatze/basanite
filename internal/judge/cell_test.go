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
