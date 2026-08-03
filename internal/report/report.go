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
	"regexp"
	"sort"
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
	// What produced this report, so "stale" can mean the inputs changed and
	// not only that the clock ran out. A report built by an older binary or
	// against an older curated list is stale however recently it was written,
	// and nothing else records that: editing the list and upgrading the tool
	// both leave GeneratedAt untouched.
	Version      string    `json:"version,omitempty"`
	ListModified time.Time `json:"list_modified,omitempty"`
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

// RenderHook is the turn-start injection view: the strongest maxWords word
// entries and maxPhrases phrase entries, judge notes cut to their first
// sentence. The console view renders everything; the injection is read by a
// model mid-task, and eighteen simultaneous vocabulary directives compete
// with the actual work — a handful it can hold beats a wall it skims. Note
// truncation enforces the judge prompt's own "one short clause" contract,
// which the judge ignores: sentences two onward restate the ladder the line
// already shows.
//
// The word budget is split between the two lanes rather than filled in report
// order, because report order is risers first and a first-come cap therefore
// spends every slot on them. That is how "load-bearing" — detected, curated,
// sitting at position 13 of 24 as a known chronic entry — went months without
// ever being injected while its rate ran near sixty a day. The chronic lane was
// working the whole time and the budget was eating it.
//
// Within the chronic share, curated known-tics go first. A riser is an
// observation that a habit may be forming and it ages out on its own; a known
// tic is the writer having said in advance that they never want to see the
// word. A standing instruction should not lose its slot to a passing one.
//
// Either lane's unused share spills to the other, so a quiet week for one still
// fills the budget rather than shrinking the injection.
func (r *Report) RenderHook(maxWords, maxPhrases int) string {
	sub := &Report{GeneratedAt: r.GeneratedAt}
	for _, e := range r.HookEntries(maxWords, maxPhrases) {
		e.JudgeNote = firstSentence(e.JudgeNote)
		sub.Entries = append(sub.Entries, e)
	}
	return sub.Render(false)
}

// HookEntries is the selection RenderHook prints: the entries that actually
// reach the prompt, in order. Split out from the rendering because "which
// words were shown" is a fact worth recording, and the string is a poor place
// to read it back from — the ledger counts report membership, which differs
// from injection precisely because this function takes three of four.
func (r *Report) HookEntries(maxWords, maxPhrases int) []Entry {
	if maxWords <= 0 {
		maxWords = len(r.Entries) // 0 = uncapped, the pre-cap behavior
	}
	if maxPhrases <= 0 {
		maxPhrases = len(r.Entries)
	}

	var risers, chronic, phrases []Entry
	for _, e := range r.Entries {
		// Budget only what Render will actually print. An entry whose ladder
		// leaves nothing below the lemma is dropped at render time, and a slot
		// spent on it would silently shrink the injection with no backfill —
		// the same shape as the lane bug above, one layer down.
		if !e.renderable() {
			continue
		}
		switch {
		case e.Kind == "phrase":
			phrases = append(phrases, e)
		case e.Kind == "chronic":
			chronic = append(chronic, e)
		default:
			risers = append(risers, e)
		}
	}
	// Stable, so entries keep the pipeline's ordering within each group and
	// only the curated ones move.
	sort.SliceStable(chronic, func(i, j int) bool { return chronic[i].Known && !chronic[j].Known })

	// Half the word budget to each lane, chronic rounding up: at the shipped
	// cap of five that is three chronic and two risers, and the lane that has
	// gone unheard is the one that should win the odd slot.
	chronicShare := (maxWords + 1) / 2
	picked := take(chronic, chronicShare)
	picked = append(picked, take(risers, maxWords-len(picked))...)
	// Spill: whichever lane came up short leaves room for the other.
	if short := maxWords - len(picked); short > 0 {
		picked = append(picked, take(chronic[min(len(chronic), chronicShare):], short)...)
	}
	picked = append(picked, take(phrases, maxPhrases)...)
	return picked
}

func take(es []Entry, n int) []Entry {
	if n <= 0 {
		return nil
	}
	if n > len(es) {
		n = len(es)
	}
	return es[:n]
}

// noteSentenceEnd marks a sentence boundary inside a judge note: terminal
// punctuation (optionally closing a quote or paren) followed by space and a
// capital or opening quote. "e.g.," and "i.e.," survive because a comma, not
// a space-plus-capital, follows their period.
var noteSentenceEnd = regexp.MustCompile(`[.!?]["')\]]*\s+["'A-Z(]`)

func firstSentence(note string) string {
	loc := noteSentenceEnd.FindStringIndex(note)
	if loc == nil {
		return note
	}
	head := note[:loc[0]]
	if strings.HasSuffix(strings.ToLower(head), "e.g") || strings.HasSuffix(strings.ToLower(head), "i.e") {
		return note // the boundary was an abbreviation; keep the note whole
	}
	end := loc[0] + 1
	for end < len(note) && strings.ContainsRune(`"')]`, rune(note[end])) {
		end++
	}
	return note[:end]
}

// Render formats the report lines. Tone per the design: awareness, never
// prohibition — naming a word to suppress it is ironic-process priming and
// backfires. The ladder reads weakest -> strongest so the move can be demote,
// not just swap. showSpark adds a per-entry trajectory sparkline: useful in
// the `report` console view, kept off the turn-start injection to hold the
// injection compact.
func (r *Report) Render(showSpark bool) string {
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
		if !e.renderable() {
			continue // nothing below the target: no demotion to offer
		}
		trimmed := trimLadder(e.Ladder, e.Lemma)
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
		if showSpark {
			if s := e.sparkline(); s != "" {
				head += " " + s
			}
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
// renderable reports whether Render will print this entry — the single test
// both Render and RenderHook's budget go through, so a slot is never spent on
// an entry the reader never sees. A phrase carries no ladder and always
// prints; a word entry needs at least one rung below the lemma, since the
// injection offers a demotion or nothing.
func (e Entry) renderable() bool {
	return e.Kind == "phrase" || len(trimLadder(e.Ladder, e.Lemma)) >= 2
}

// TrimLadder returns the window the reader is shown: the four rungs below the
// lemma, plus the lemma itself. Exported because the judge must be offered
// exactly this — anything wider and the chosen rung can be a word stronger
// than the lemma, or one the injection never displayed.
func TrimLadder(rungs []Rung, lemma string) []Rung { return trimLadder(rungs, lemma) }

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
