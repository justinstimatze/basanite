package display

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// LogName is the swap log, kept beside report.json in the state dir.
const LogName = "swaps.jsonl"

// Swap is one word replaced on screen in one streamed batch. The transcript
// keeps the original, so this is the only record that the substitution ever
// happened — and the only count that tracks what you actually read rather than
// what was written.
type Swap struct {
	At    time.Time `json:"t"`
	Lemma string    `json:"w"`
	To    string    `json:"to"`
	Count int       `json:"n"`
}

// AppendLog records one batch's swaps. Append-only JSONL because the hook runs
// on every streamed batch of every session: O_APPEND keeps concurrent sessions
// from interleaving mid-line, and a torn or malformed line costs one record
// rather than the file. Best-effort — a logging failure must never garble the
// terminal, which is what returning an error here would risk.
func AppendLog(path string, counts map[string]int, swaps Swaps, now time.Time) {
	if len(counts) == 0 || path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	lemmas := make([]string, 0, len(counts))
	for l := range counts {
		lemmas = append(lemmas, l)
	}
	sort.Strings(lemmas) // deterministic line order within a batch
	var b strings.Builder
	for _, l := range lemmas {
		line, err := json.Marshal(Swap{At: now, Lemma: l, To: swaps[l], Count: counts[l]})
		if err != nil {
			continue
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	f.WriteString(b.String())
}

// LoadLog reads the swap log. A missing file is an empty log, not an error,
// and a malformed line is skipped rather than failing the read — this file is
// written by a hot-path hook and one bad line should not cost the history.
func LoadLog(path string) ([]Swap, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Swap
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		var s Swap
		if json.Unmarshal(sc.Bytes(), &s) == nil && s.Lemma != "" {
			out = append(out, s)
		}
	}
	return out, sc.Err()
}

type swapTotal struct {
	lemma, to        string
	count            int
	first, last      time.Time
	replacementsSeen map[string]bool
}

// RenderLog is the "how often was I spared the word" view. It counts what was
// replaced on screen, which is deliberately not what report and trend count:
// those read the transcripts and report what was written. A gap between the
// two is the whole point of the display hook, not a discrepancy.
func RenderLog(swaps []Swap, now time.Time) string {
	if len(swaps) == 0 {
		return "basanite swap ledger — nothing replaced yet; it fills in as the display hook runs.\n"
	}
	byLemma := map[string]*swapTotal{}
	for _, s := range swaps {
		t := byLemma[s.Lemma]
		if t == nil {
			t = &swapTotal{lemma: s.Lemma, first: s.At, replacementsSeen: map[string]bool{}}
			byLemma[s.Lemma] = t
		}
		t.count += s.Count
		t.to = s.To
		t.replacementsSeen[s.To] = true
		if s.At.Before(t.first) {
			t.first = s.At
		}
		if s.At.After(t.last) {
			t.last = s.At
		}
	}
	totals := make([]*swapTotal, 0, len(byLemma))
	total, earliest := 0, now
	for _, t := range byLemma {
		totals = append(totals, t)
		total += t.count
		if t.first.Before(earliest) {
			earliest = t.first
		}
	}
	sort.Slice(totals, func(i, j int) bool {
		if totals[i].count != totals[j].count {
			return totals[i].count > totals[j].count
		}
		return totals[i].lemma < totals[j].lemma
	})

	var b strings.Builder
	b.WriteString("basanite swap ledger — tics replaced on screen by the display hook.\n")
	b.WriteString("The model still wrote them: this counts what you were spared, not what changed.\n\n")
	for _, t := range totals {
		arrow := t.lemma + " → " + t.to
		if len(t.replacementsSeen) > 1 {
			arrow += " (+alt)" // the rung changed at some point, e.g. a re-judge
		}
		fmt.Fprintf(&b, "  %-34s %5d×   last %s\n", arrow, t.count, t.last.Local().Format("2006-01-02"))
	}
	days := int(now.Sub(earliest).Hours()/24) + 1
	fmt.Fprintf(&b, "\n  %d replacement%s over %d day%s (%.1f/day)\n",
		total, plural(total), days, plural(days), float64(total)/float64(days))
	return b.String()
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
