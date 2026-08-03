// Package audit answers the question a curated list cannot answer about
// itself: of the entries on it, how many have ever fired?
//
// An entry that never matches is indistinguishable, from the list's own point
// of view, from one that matches constantly. It costs a line, it costs a scan,
// and it quietly reassures you the tic is covered while nothing is watching
// for it. The same blind spot hides the opposite failure: when a word you know
// you overuse never shows up in a report, "the ranking is working as designed"
// and "the pattern never matches" look identical from outside.
//
// So this counts every entry against the corpus and says which of the three it
// is: reported, matching but below the cutoff, or dead.
package audit

import (
	"fmt"
	"sort"
	"strings"

	"github.com/justinstimatze/basanite/internal/corpus"
	"github.com/justinstimatze/basanite/internal/knowntics"
	"github.com/justinstimatze/basanite/internal/phrase"
	"github.com/justinstimatze/basanite/internal/report"
	"github.com/justinstimatze/basanite/internal/text"
)

// Status is what the audit concluded about one entry.
const (
	Reported = "reported"     // in the current report
	Below    = "below cutoff" // matches the corpus, ranked out
	Never    = "NEVER FIRES"  // no match at all in the window
)

// Entry is one curated term's standing against the corpus.
type Entry struct {
	Term     string
	IsPhrase bool
	Hits     int
	Rate     float64 // per 1k tokens
	Projects int
	Status   string
	Rank     int // 1-based position in the report, when reported
}

// Result is the whole list audited, plus the corpus it was measured against.
type Result struct {
	Entries  []Entry
	Tokens   int
	Turns    int
	Days     int
	Reported int
	Below    int
	Never    int
}

// Run counts every curated entry over the corpus. Words are matched against
// the lemmatized token stream (the same one the detector counts) and phrases
// against the surface word stream with stopwords kept, because that is the
// stream each was written to be found in — auditing a phrase against the
// tokenized stream would report every phrase dead for the wrong reason.
func Run(known *knowntics.Set, rep *report.Report, turns []corpus.Turn, days int) Result {
	res := Result{Days: days, Turns: len(turns)}
	if known == nil {
		return res
	}

	rank := map[string]int{}
	if rep != nil {
		for i, e := range rep.Entries {
			rank[strings.ToLower(e.Lemma)] = i + 1
		}
	}

	wordHits := map[string]int{}
	phraseHits := map[string]int{}
	projects := map[string]map[string]bool{}
	matcher := phrase.New(known.Phrases)

	for _, t := range turns {
		for _, tok := range text.Tokens(t.Text) {
			res.Tokens++
			if known.Words[tok] {
				wordHits[tok]++
				note(projects, tok, t.Project)
			}
		}
		if matcher.Empty() {
			continue
		}
		found := map[string]int{}
		matcher.Count(text.Words(strings.ToLower(t.Text)), found)
		for p, n := range found {
			phraseHits[p] += n
			note(projects, p, t.Project)
		}
	}

	add := func(term string, hits int, isPhrase bool) {
		e := Entry{Term: term, IsPhrase: isPhrase, Hits: hits,
			Projects: len(projects[term]), Rank: rank[term]}
		if res.Tokens > 0 {
			e.Rate = float64(hits) / float64(res.Tokens) * 1000
		}
		switch {
		case e.Rank > 0:
			e.Status, res.Reported = Reported, res.Reported+1
		case hits == 0:
			e.Status, res.Never = Never, res.Never+1
		default:
			e.Status, res.Below = Below, res.Below+1
		}
		res.Entries = append(res.Entries, e)
	}
	for w := range known.Words {
		add(w, wordHits[w], false)
	}
	for _, p := range known.Phrases {
		add(strings.ToLower(p), phraseHits[strings.ToLower(p)], true)
	}

	// Dead entries last: the list reads top-down as "working, working, …" and
	// ends on exactly the rows worth cutting.
	sort.Slice(res.Entries, func(i, j int) bool {
		if res.Entries[i].Hits != res.Entries[j].Hits {
			return res.Entries[i].Hits > res.Entries[j].Hits
		}
		return res.Entries[i].Term < res.Entries[j].Term
	})
	return res
}

func note(projects map[string]map[string]bool, term, project string) {
	if projects[term] == nil {
		projects[term] = map[string]bool{}
	}
	projects[term][project] = true
}

// Render formats the audit. onlyNever narrows it to the actionable rows — the
// entries that have never matched and can be cut.
func (r Result) Render(onlyNever bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "basanite audit — %d curated entries against %d turns / %dk tokens over %dd\n",
		len(r.Entries), r.Turns, r.Tokens/1000, r.Days)
	fmt.Fprintf(&b, "%d reported · %d below cutoff · %d never fire\n\n", r.Reported, r.Below, r.Never)
	if len(r.Entries) == 0 {
		return b.String()
	}

	fmt.Fprintf(&b, "  %-26s %7s %9s %5s  %s\n", "ENTRY", "HITS", "RATE/1K", "PROJ", "STATUS")
	shown := 0
	for _, e := range r.Entries {
		if onlyNever && e.Status != Never {
			continue
		}
		term := e.Term
		if e.IsPhrase {
			term = `"` + term + `"`
		}
		status := e.Status
		if e.Rank > 0 {
			status = fmt.Sprintf("%s (#%d)", Reported, e.Rank)
		}
		fmt.Fprintf(&b, "  %-26s %7d %9.3f %5d  %s\n", trunc(term, 26), e.Hits, e.Rate, e.Projects, status)
		shown++
	}
	if shown == 0 {
		b.WriteString("  every entry matched at least once.\n")
	}
	if !onlyNever && r.Never > 0 {
		fmt.Fprintf(&b, "\nA never-fired entry is not evidence of a working filter — it is an\n"+
			"untested pattern. `audit -never` lists just those; cut them or fix them.\n")
	}
	return b.String()
}

func trunc(s string, n int) string {
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}
