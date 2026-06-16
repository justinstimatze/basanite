// Package report is the precomputed bridge between the offline pipeline
// (scan -> vet -> ladder, ~minutes) and the turn-start hook (~ms): report
// Build writes JSON state, hook Render reads it.
package report

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/justinstimatze/basanite/internal/spark"
)

// Rung is one surviving candidate on an entry's ladder.
type Rung struct {
	Word  string  `json:"word"`
	IC    float64 `json:"ic"`
	Clean int     `json:"clean,omitempty"` // vet: uses where substitution held
	Total int     `json:"total,omitempty"`
}

// Entry is one flagged word with its vetted, IC-ordered ladder. Kind
// "riser" (or empty, for reports from older versions) means recent
// frequency rose against the trailing baseline; "chronic" means a steady
// high rate with tic evidence (frame repetition, rarity, or a curated
// known-tics match); "phrase" is an awareness-only stock-phrase entry with
// no ladder, where Lemma holds the phrase text.
type Entry struct {
	Kind         string    `json:"kind,omitempty"`
	Lemma        string    `json:"lemma"`
	RecentCount  int       `json:"recent_count,omitempty"`
	Ratio        float64   `json:"ratio,omitempty"`
	Rate         float64   `json:"rate,omitempty"`       // per-1k rate (full window for chronic/phrase)
	FrameFrac    float64   `json:"frame_frac,omitempty"` // share of uses in the "<det> X of" frame
	Rarity       float64   `json:"rarity,omitempty"`     // WordIC, set when the rare-word route flagged it
	Known        bool      `json:"known,omitempty"`      // admitted via the curated known-tics route
	Count        int       `json:"count,omitempty"`      // phrase: full-window occurrences
	Projects     int       `json:"projects,omitempty"`   // phrase: distinct projects it appears in
	JudgeRole    string    `json:"judge_role,omitempty"` // tic|mixed when the LLM gate ran (term_of_art entries are dropped, never stored)
	JudgeNote    string    `json:"judge_note,omitempty"` // the gate's one-clause awareness payload
	DemoteTo     string    `json:"demote_to,omitempty"`  // the gate's chosen rung, when it named one
	ClusterDelta float64   `json:"cluster_delta"`        // vs corpus baseline; >0 = tic-like
	Uses         int       `json:"uses"`
	Ladder       []Rung    `json:"ladder"`          // weakest -> strongest, includes the lemma itself
	Spark        []float64 `json:"spark,omitempty"` // trailing weekly per-1k rates, oldest -> newest; -1 marks a gap (no tokens that week)
}

// Report is the persisted pipeline output.
type Report struct {
	GeneratedAt  time.Time `json:"generated_at"`
	RecentDays   int       `json:"recent_days"`
	BaselineDays int       `json:"baseline_days"`
	Entries      []Entry   `json:"entries"`
}

// StateDir resolves the basanite state directory ($XDG_STATE_HOME/basanite
// or ~/.local/state/basanite) and creates it.
func StateDir() (string, error) {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "state")
	}
	dir := filepath.Join(base, "basanite")
	return dir, os.MkdirAll(dir, 0o755)
}

// Path is the default report location.
func Path() (string, error) {
	dir, err := StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "report.json"), nil
}

// Save writes the report atomically: an exclusive temp file in the target
// directory (so the rename can't cross filesystems and concurrent runs
// can't collide), renamed over path on success, removed on failure.
func (r *Report) Save(path string) error {
	b, err := json.MarshalIndent(r, "", " ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".report-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op once the rename has happened
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// maxReportSize bounds what the hook will read on every prompt: a report
// is a few KB; anything near the cap is not ours.
const maxReportSize = 8 << 20

// Load reads a report; a missing file returns (nil, nil) — absence is the
// hook's normal silent case, not an error. Symlinks and oversized files
// are refused: the hook runs on every prompt and renders this file into
// model context, so it only ever reads a plausibly-shaped regular file.
func Load(path string) (*Report, error) {
	fi, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() || fi.Size() > maxReportSize {
		return nil, fmt.Errorf("report %s: not a regular file under %d bytes", path, maxReportSize)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r Report
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// Render formats the turn-start injection. Tone per the design: awareness,
// never prohibition — naming a word to suppress it is ironic-process
// priming and backfires. The ladder reads weakest -> strongest so the move
// can be demote, not just swap.
func (r *Report) Render() string {
	if len(r.Entries) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "basanite — words and phrases you lean on in your output (awareness, not prohibition; the weaker rung is often the truer one):\n")
	rendered := 0
	for _, e := range r.Entries {
		// Phrase entries carry no ladder — there's no synonym for a stock
		// phrase, only the awareness that you keep reaching for it.
		if e.Kind == "phrase" {
			fmt.Fprintf(&b, "  %q (%s)\n", e.Lemma, e.note())
			rendered++
			continue
		}
		trimmed := trimLadder(e.Ladder, e.Lemma)
		if len(trimmed) < 2 {
			continue // nothing below the target: no demotion to offer
		}
		words := make([]string, 0, len(trimmed))
		for _, rung := range trimmed {
			w := rung.Word
			if w == e.Lemma {
				w = "[" + w + "]"
			}
			words = append(words, w)
		}
		line := strings.Join(words, " < ")
		if e.JudgeNote != "" {
			line += " — " + e.JudgeNote
		}
		head := e.Lemma
		if s := e.sparkline(); s != "" {
			head += " " + s
		}
		fmt.Fprintf(&b, "  %s (%s): %s\n", head, e.note(), line)
		rendered++
	}
	if rendered == 0 {
		return ""
	}
	return b.String()
}

// sparkline renders the entry's trailing weekly series as a block sparkline
// plus a direction arrow, or "" when no series was recorded. The stored -1
// gap sentinel becomes a NaN so dead weeks read as holes, not zeros.
func (e Entry) sparkline() string {
	if len(e.Spark) == 0 {
		return ""
	}
	vals := make([]float64, len(e.Spark))
	for i, v := range e.Spark {
		if v < 0 {
			vals[i] = math.NaN()
		} else {
			vals[i] = v
		}
	}
	return spark.Line(vals) + " " + spark.Trend(vals)
}

// note is the per-entry evidence summary in the rendered line.
func (e Entry) note() string {
	if e.Kind == "phrase" {
		span := ""
		if e.Projects > 1 {
			span = fmt.Sprintf(" across %d projects", e.Projects)
		}
		return fmt.Sprintf("a stock phrase, %d×%s this window — reach for a fresh one", e.Count, span)
	}
	if e.Kind != "chronic" {
		return fmt.Sprintf("%.1f× your baseline", e.Ratio)
	}
	note := fmt.Sprintf("steady %.2f/1k", e.Rate)
	if e.FrameFrac >= 0.25 {
		note += fmt.Sprintf(", %q frame in %d%%", "the "+e.Lemma+" of", int(e.FrameFrac*100+0.5))
	}
	if e.Rarity > 0 {
		note += ", uncommon in general English"
	}
	if e.Known {
		note += ", a common Claude lean"
	}
	return note
}

// trimLadder keeps the injection readable and demote-only: the four rungs
// just below the target, then the target. A 20-rung WordNet dump is noise,
// and the stronger-than-target direction is where wrong-sense survivors
// tend to sit — the useful move is down the ladder, not up.
func trimLadder(rungs []Rung, lemma string) []Rung {
	self := len(rungs) - 1
	for i, r := range rungs {
		if r.Word == lemma {
			self = i
			break
		}
	}
	lo := self - 4
	if lo < 0 {
		lo = 0
	}
	return rungs[lo : self+1]
}
