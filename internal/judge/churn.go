package judge

import (
	"fmt"
	"sort"
	"strings"
)

// The verdict cache is keyed on the ladder, and the ladder moves whenever
// tokenization does. So a word gets re-judged not because anything about how
// it is used changed, but because a stopword was added three commits ago and
// one rung fell out of its candidate set. Near-identical ladders can then come
// back with materially different answers.
//
// This file measures that instead of arguing about it. The question the
// numbers have to settle is whether the instability is in the ladder (which
// A1's trimming was expected to shrink) or in the judgment itself (which
// would mean keying the cache differently).

// Churn is one word's verdict history across every ladder it was judged
// against. Only words judged against two or more distinct ladders are
// interesting — a word with one ladder has nothing to be unstable about.
type Churn struct {
	Word     string
	Ladders  int      // distinct ladder hashes
	Verdicts int      // accepted verdicts (refusals counted separately)
	Refused  int      // rows the gate threw away
	Roles    []string // distinct roles, in first-seen order
	Rungs    []string // distinct demotions, in first-seen order ("-" for none)
	Schemas  []int    // distinct schema versions, ascending

	// SameSchema* are the flips that happened with the prompt held still.
	//
	// This is the distinction the whole diagnostic turns on. A schema bump
	// reworded the question, so a word answering differently across one is
	// the tool's own history showing up in the log, not the gate being
	// inconsistent. Counting those as churn would blame the gate for every
	// prompt edit ever made — and since the bumps are what invalidate the
	// cache, nearly every long-lived word has crossed one.
	SameSchemaRoles bool
	SameSchemaRungs bool
	SameSchemaTOA   bool // a term-of-art crossing under one unchanged prompt
}

// CrossesTermOfArt reports whether the flips cross the suppress boundary.
// This is the flip that matters most: term_of_art suppresses the word from
// the report entirely, so a word that alternates is alternately present and
// absent from the injection for reasons that have nothing to do with the
// writing.
func (c Churn) CrossesTermOfArt() bool {
	var toa, other bool
	for _, r := range c.Roles {
		if r == RoleTermOfArt {
			toa = true
		} else {
			other = true
		}
	}
	return toa && other
}

// Unstable is the summary judgment: the word came back with more than one
// role, or more than one demotion, across its ladders.
func (c Churn) Unstable() bool { return len(c.Roles) > 1 || len(c.Rungs) > 1 }

// UnstableWithinSchema is the same judgment with prompt changes excluded —
// the number that actually says whether the gate is inconsistent.
func (c Churn) UnstableWithinSchema() bool { return c.SameSchemaRoles || c.SameSchemaRungs }

// ChurnBy folds the raw log into per-word histories. It reads the whole log,
// not the cache index, because a word re-judged against the same ladder
// writes two rows the index collapses into one — and that collapse is
// precisely the churn being measured.
//
// Refused rows are counted but contribute no role or rung: the gate did not
// act on them, so treating them as a flip would report instability the reader
// never saw. Their count is worth showing on its own, since a word that keeps
// failing the fence is a different problem from one that keeps changing its
// mind.
func ChurnBy(records []Record) []Churn {
	type perSchema struct {
		roles map[string]bool
		rungs map[string]bool
	}
	type acc struct {
		c        Churn
		ladders  map[string]bool
		roles    map[string]bool
		rungs    map[string]bool
		schemas  map[int]*perSchema
		schemaLs []int
	}
	byWord := map[string]*acc{}
	var order []string

	for _, r := range records {
		a, ok := byWord[r.Word]
		if !ok {
			a = &acc{
				c:       Churn{Word: r.Word},
				ladders: map[string]bool{}, roles: map[string]bool{},
				rungs: map[string]bool{}, schemas: map[int]*perSchema{},
			}
			byWord[r.Word] = a
			order = append(order, r.Word)
		}
		a.ladders[r.LadderHash] = true
		ps, ok := a.schemas[r.SchemaVersion]
		if !ok {
			ps = &perSchema{roles: map[string]bool{}, rungs: map[string]bool{}}
			a.schemas[r.SchemaVersion] = ps
			a.schemaLs = append(a.schemaLs, r.SchemaVersion)
		}
		if !r.WellFormed || !r.Safe {
			a.c.Refused++
			continue
		}
		a.c.Verdicts++
		if !a.roles[r.Role] {
			a.roles[r.Role] = true
			a.c.Roles = append(a.c.Roles, r.Role)
		}
		rung := r.DemoteTo
		if rung == "" {
			rung = "-"
		}
		if !a.rungs[rung] {
			a.rungs[rung] = true
			a.c.Rungs = append(a.c.Rungs, rung)
		}
		ps.roles[r.Role], ps.rungs[rung] = true, true
	}

	out := make([]Churn, 0, len(order))
	for _, w := range order {
		a := byWord[w]
		a.c.Ladders = len(a.ladders)
		sort.Ints(a.schemaLs)
		a.c.Schemas = a.schemaLs
		for _, ps := range a.schemas {
			if len(ps.roles) > 1 {
				a.c.SameSchemaRoles = true
				if ps.roles[RoleTermOfArt] {
					// term_of_art on one side and something else on the
					// other, under one unchanged prompt
					a.c.SameSchemaTOA = true
				}
			}
			if len(ps.rungs) > 1 {
				a.c.SameSchemaRungs = true
			}
		}
		out = append(out, a.c)
	}
	return out
}

// RenderChurn is the human view, split by the only distinction that decides
// anything: words that answered differently with the prompt held still, and
// words whose only disagreement straddles a schema bump.
//
// Words judged against a single ladder are excluded entirely — they have had
// no opportunity to disagree with themselves, so counting them as stable
// would flatter the number the diagnostic exists to report.
func RenderChurn(records []Record) string {
	all := ChurnBy(records)
	var multi, live, crossBump []Churn
	for _, c := range all {
		if c.Ladders < 2 {
			continue
		}
		multi = append(multi, c)
		switch {
		case c.UnstableWithinSchema():
			live = append(live, c)
		case c.Unstable():
			crossBump = append(crossBump, c)
		}
	}

	var b strings.Builder
	b.WriteString("basanite verdict churn — does the gate give the same answer twice?\n")
	b.WriteString("The cache is keyed on the ladder, and the ladder moves when tokenization does,\n")
	b.WriteString("so a word is re-judged for reasons unrelated to how it is used.\n")

	if len(all) == 0 {
		b.WriteString("\nno verdicts recorded yet.\n")
		return b.String()
	}
	if len(multi) == 0 {
		fmt.Fprintf(&b, "\n%d word%s judged, none against more than one ladder — nothing to compare yet.\n",
			len(all), plural(len(all)))
		return b.String()
	}

	sortChurn(live)
	sortChurn(crossBump)

	if len(live) > 0 {
		b.WriteString("\nchanged its answer under one unchanged prompt:\n")
		for _, c := range live {
			writeChurnRow(&b, c, c.SameSchemaTOA)
		}
	}
	if len(crossBump) > 0 {
		b.WriteString("\nonly disagreed across a schema bump (expected — the question was reworded):\n")
		for _, c := range crossBump {
			writeChurnRow(&b, c, false)
		}
	}

	fmt.Fprintf(&b, "\n%d of %d words judged against 2+ ladders changed their answer with the prompt held still",
		len(live), len(multi))
	if n := crossings(live); n > 0 {
		fmt.Fprintf(&b, "; %d of those crossed the term-of-art boundary (!), which decides whether the word appears at all", n)
	}
	b.WriteString(".\n")
	if len(crossBump) > 0 {
		fmt.Fprintf(&b, "A further %d disagreed only across a schema bump and are not counted.\n", len(crossBump))
	}
	return b.String()
}

// worst first: a role flip outranks a rung flip, then more distinct answers,
// then more ladders — a word that changed its mind over two ladders is less
// interesting than one that changed it over five.
func sortChurn(cs []Churn) {
	sort.SliceStable(cs, func(i, j int) bool {
		a, c := cs[i], cs[j]
		if a.SameSchemaTOA != c.SameSchemaTOA {
			return a.SameSchemaTOA
		}
		if a.SameSchemaRoles != c.SameSchemaRoles {
			return a.SameSchemaRoles
		}
		if len(a.Rungs) != len(c.Rungs) {
			return len(a.Rungs) > len(c.Rungs)
		}
		return a.Ladders > c.Ladders
	})
}

func writeChurnRow(b *strings.Builder, c Churn, mark bool) {
	m := " "
	if mark {
		m = "!"
	}
	fmt.Fprintf(b, " %s %-16s %d ladders  role: %-24s rung: %s",
		m, c.Word, c.Ladders, strings.Join(c.Roles, "/"), strings.Join(c.Rungs, "/"))
	if c.Refused > 0 {
		fmt.Fprintf(b, "  (%d refused)", c.Refused)
	}
	if len(c.Schemas) > 1 {
		fmt.Fprintf(b, "  schema %s", joinInts(c.Schemas))
	}
	b.WriteByte('\n')
}

func crossings(cs []Churn) int {
	n := 0
	for _, c := range cs {
		if c.SameSchemaTOA {
			n++
		}
	}
	return n
}

func joinInts(xs []int) string {
	parts := make([]string, len(xs))
	for i, x := range xs {
		parts[i] = fmt.Sprint(x)
	}
	return strings.Join(parts, "/")
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
