// Package display is the MessageDisplay hook: it swaps flagged tics for their
// vetted demote rung in the text streamed to the terminal.
//
// This is the one surface that changes nothing about the writing. MessageDisplay
// is display-only — the transcript and the model's own context keep the original
// word, so the model never sees the swap and basanite keeps measuring the true
// rate. The report and the ledger stay honest whatever the screen shows. It buys
// exactly one thing: not having to read the word.
//
// Which is also why it is not a substitute for the turn-start injection. The
// injection is the part that can change what gets written; this is relief.
package display

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/justinstimatze/basanite/internal/report"
)

// Swaps maps a flagged lemma to the word shown in its place.
type Swaps map[string]string

// FromReport builds the swap table. Only entries the judge gave a demote rung
// are eligible: that rung is the sense-checked one, and an unvetted ladder pick
// is how you get "hook -> snare" rendered into your terminal all day.
//
// The default is narrower still — curated known-tics only. A ladder is vetted
// for how well a word substitutes *across your uses on average*, which is the
// right test for offering awareness and the wrong one for rewriting every
// occurrence: the live report demotes "turn" to "change" ("your change", "in
// change"), "five" to "figure" ("figure files"), "say" to "indicate"
// ("indicate that again"). Words on the curated list are the ones already
// declared unwanted, and they are unwanted because they are loose figurative
// intensifiers — exactly the case where any occurrence can take the weaker
// word. all=true opts into the rest.
func FromReport(rep *report.Report, all bool) Swaps {
	s := Swaps{}
	if rep == nil {
		return s
	}
	for _, e := range rep.Entries {
		// A stock phrase has no ladder — there is no synonym for one, only
		// the awareness that you keep reaching for it.
		if e.Kind == "phrase" || e.DemoteTo == "" || e.Lemma == "" {
			continue
		}
		if !all && !e.Known {
			continue
		}
		if strings.EqualFold(e.DemoteTo, e.Lemma) {
			continue
		}
		s[strings.ToLower(e.Lemma)] = e.DemoteTo
	}
	return s
}

// Add merges explicit word:replacement pairs, which win over the report. This
// is the escape hatch for a lean basanite cannot see (it only reads assistant
// prose) and for overriding a rung you disagree with.
func (s Swaps) Add(pairs []string) {
	for _, p := range pairs {
		word, rep, ok := strings.Cut(p, ":")
		word, rep = strings.TrimSpace(word), strings.TrimSpace(rep)
		if !ok || word == "" || rep == "" {
			continue
		}
		s[strings.ToLower(word)] = rep
	}
}

// State carries what one message's swap needs between streaming batches.
// MessageDisplay delivers a message in increments, so whether we are inside a
// fenced code block is not knowable from a single delta.
type State struct {
	MessageID string `json:"message_id"`
	InFence   bool   `json:"in_fence"`
}

var (
	// A fence line: optional indent, three or more backticks or tildes.
	fenceLine = regexp.MustCompile("^\\s{0,3}(```+|~~~+)")
	// Inline code, a path, or a URL — spans a swap must not touch, because
	// the text inside is something you may copy or run.
	protected = regexp.MustCompile("`[^`\n]*`" + `|\bhttps?://\S+|(?:[\w.-]*/){1,}[\w.-]+`)
)

// Apply swaps flagged words in one streamed batch, leaving code untouched. It
// returns the text to display, the fence state for the next batch, and a count
// per lemma of what it replaced — the only record that the substitution
// happened at all, since the transcript keeps the original.
//
// Batches end on line boundaries (except a message's last), so tracking fences
// per line is sound.
func (s Swaps) Apply(delta string, st State) (string, State, map[string]int) {
	counts := map[string]int{}
	if len(s) == 0 || delta == "" {
		return delta, st, counts
	}
	lines := strings.Split(delta, "\n")
	for i, line := range lines {
		if fenceLine.MatchString(line) {
			st.InFence = !st.InFence
			continue // the fence line itself is never prose
		}
		if st.InFence {
			continue
		}
		lines[i] = s.applyLine(line, counts)
	}
	return strings.Join(lines, "\n"), st, counts
}

// applyLine swaps outside the protected spans of a single prose line.
func (s Swaps) applyLine(line string, counts map[string]int) string {
	spans := protected.FindAllStringIndex(line, -1)
	var b strings.Builder
	last := 0
	for _, sp := range spans {
		b.WriteString(s.swapWords(line[last:sp[0]], counts))
		b.WriteString(line[sp[0]:sp[1]]) // verbatim
		last = sp[1]
	}
	b.WriteString(s.swapWords(line[last:], counts))
	return b.String()
}

// wordish matches a run that could be a flagged lemma, hyphens included, since
// the tics that most want swapping are compounds like "load-bearing".
var wordish = regexp.MustCompile(`[\p{L}][\p{L}'-]*`)

func (s Swaps) swapWords(text string, counts map[string]int) string {
	if text == "" {
		return text
	}
	return wordish.ReplaceAllStringFunc(text, func(w string) string {
		lemma := strings.ToLower(w)
		rep, ok := s[lemma]
		if !ok {
			return w
		}
		counts[lemma]++
		return matchCase(w, rep)
	})
}

// matchCase carries the original's capitalization onto the replacement, so a
// swap at the start of a sentence does not read as a typo. Only the leading
// character and all-caps are worth honoring; anything else is left alone.
func matchCase(orig, rep string) string {
	if orig == "" || rep == "" {
		return rep
	}
	o := []rune(orig)
	if len(o) > 1 && strings.ToUpper(orig) == orig {
		return strings.ToUpper(rep)
	}
	if unicode.IsUpper(o[0]) {
		r := []rune(rep)
		r[0] = unicode.ToUpper(r[0])
		return string(r)
	}
	return rep
}
