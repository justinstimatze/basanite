package judge

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/justinstimatze/stull/spec"
)

// The live judge: stull's spec.Cell as the standalone fence, driven by a
// hand-rolled Anthropic Messages call. basanite's only new module dependency
// is stull (verified: package spec imports stdlib only); the API call is
// net/http + encoding/json, so nothing else is pulled in.
const (
	// DefaultModel is a cheap classify-from-options model; the judgment is a
	// three-way label plus a select-from-the-ladder, not generation.
	DefaultModel     = "claude-haiku-4-5-20251001"
	apiEndpoint      = "https://api.anthropic.com/v1/messages"
	anthropicVersion = "2023-06-01"
)

// cellJudge implements Judger with a real fenced LLM call, a verdict cache,
// and a calibration log.
type cellJudge struct {
	model    string
	apiKey   string
	store    *Store
	client   *http.Client
	endpoint string // overridable in tests
	now      func() time.Time
}

// New builds a live judge: it loads the API key (environment first, then a
// .env in the data dir, the working directory, or ~/.config/basanite) and
// opens the verdict store under stateDir.
func New(stateDir, dataDir, model string) (*cellJudge, error) {
	key := loadKey(dataDir)
	if key == "" {
		return nil, fmt.Errorf("no ANTHROPIC_API_KEY (set it in the environment or a .env — see .env.example)")
	}
	store, err := LoadStore(filepath.Join(stateDir, "verdicts.jsonl"))
	if err != nil {
		return nil, err
	}
	if model == "" {
		model = DefaultModel
	}
	return &cellJudge{
		model:    model,
		apiKey:   key,
		store:    store,
		client:   &http.Client{Timeout: 45 * time.Second},
		endpoint: apiEndpoint,
		now:      time.Now,
	}, nil
}

// Judge classifies one word, caching the verdict by word+ladder+model+schema.
// A transient API error or an inconclusive fence (malformed/incoherent
// verdict) returns ok=false so the caller keeps its un-gated behavior; only a
// well-formed, safe verdict is acted on — and only it is cached.
func (j *cellJudge) Judge(word string, ladder []string, samples [][]string) (Verdict, bool) {
	if r, ok := j.store.Lookup(word, ladder, j.model); ok {
		if !r.WellFormed || !r.Safe {
			return Verdict{}, false
		}
		return Verdict{Role: r.Role, DemoteTo: r.DemoteTo, Note: r.Note}, true
	}

	cell := spec.NewConfinedCell("verdict", j.model, Instructions, Schema(ladder), Grammar(ladder), Safety())
	raw, err := j.complete(cell.Schema, Payload(word, ladder, samples))
	if err != nil {
		return Verdict{}, false // transient: fail safe, do not cache a non-answer
	}

	res := cell.Check(raw)
	v, parsed := Parse(raw)
	_ = j.store.Append(RecordFrom(word, ladder, j.model, v, res.WellFormed, res.Safe, j.now()))
	if !res.WellFormed || !res.Safe || !parsed {
		return Verdict{}, false
	}
	return v, true
}

// complete drives the strict-tool call: the model is forced to emit JSON
// matching schema (generation-time confinement), and the tool input comes
// back as the raw verdict JSON. The instructions are marked for prompt
// caching — identical across the dozen per-report calls, so the batch pays
// the system-prompt input cost once.
func (j *cellJudge) complete(schema map[string]any, payload string) (string, error) {
	body := map[string]any{
		"model":      j.model,
		"max_tokens": 512,
		// temperature 0: the verdict is a classification, and a cached
		// verdict should be a stable fact, not a per-run coin flip
		"temperature": 0,
		"system": []map[string]any{{
			"type":          "text",
			"text":          Instructions,
			"cache_control": map[string]any{"type": "ephemeral"},
		}},
		"messages": []map[string]any{{"role": "user", "content": payload}},
		"tools": []map[string]any{{
			"name":         "verdict",
			"description":  "Record the judgment for this word.",
			"input_schema": schema,
		}},
		"tool_choice": map[string]any{"type": "tool", "name": "verdict"},
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequest(http.MethodPost, j.endpoint, bytes.NewReader(buf))
	if err != nil {
		return "", err
	}
	req.Header.Set("x-api-key", j.apiKey)
	req.Header.Set("anthropic-version", anthropicVersion)
	req.Header.Set("content-type", "application/json")

	resp, err := j.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var out struct {
		Content []struct {
			Type  string          `json:"type"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.Error != nil {
		return "", fmt.Errorf("anthropic: %s", out.Error.Message)
	}
	for _, c := range out.Content {
		if c.Type == "tool_use" && c.Name == "verdict" {
			return string(c.Input), nil
		}
	}
	return "", fmt.Errorf("anthropic: no verdict tool_use in response (status %d)", resp.StatusCode)
}

// loadKey resolves the credential: the environment wins, then a .env in any
// of the data dir, the working directory, or ~/.config/basanite. The .env
// path lets the SessionStart refresh hook reach the key without it being
// exported into every shell.
func loadKey(dataDir string) string {
	if k := strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")); k != "" {
		return k
	}
	var dirs []string
	if dataDir != "" {
		dirs = append(dirs, dataDir)
	}
	dirs = append(dirs, ".")
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".config", "basanite"))
	}
	for _, d := range dirs {
		if k := readEnvKey(filepath.Join(d, ".env")); k != "" {
			return k
		}
	}
	return ""
}

func readEnvKey(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if k, v, ok := strings.Cut(line, "="); ok && strings.TrimSpace(k) == "ANTHROPIC_API_KEY" {
			return strings.TrimSpace(strings.Trim(strings.TrimSpace(v), `"'`))
		}
	}
	return ""
}
