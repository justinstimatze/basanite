// Package pipeline composes the offline analysis — riser detection, ladder
// candidates, cloze vetting — into the persisted report, tokenizing the
// corpus exactly once along the way.
package pipeline

import (
	"sort"
	"strings"
	"time"

	"github.com/justinstimatze/basanite/internal/cloze"
	"github.com/justinstimatze/basanite/internal/corpus"
	"github.com/justinstimatze/basanite/internal/detect"
	"github.com/justinstimatze/basanite/internal/embed"
	"github.com/justinstimatze/basanite/internal/judge"
	"github.com/justinstimatze/basanite/internal/phrase"
	"github.com/justinstimatze/basanite/internal/report"
	"github.com/justinstimatze/basanite/internal/text"
	"github.com/justinstimatze/basanite/internal/wordnet"
)

// Windows holds the token counts: the scan windows (recent vs trailing
// baseline) plus the full input window, which the chronic detector rates
// against — a chronic tic is its own baseline, so only the long flat rate
// can see it.
type Windows struct {
	Recent, Baseline           map[string]int
	PerProject                 map[string]map[string]int
	RecentTotal, BaselineTotal int
	RecentTurns, BaselineTurns int
	Full                       map[string]int
	FullTotal                  int
	FullProjects               map[string]map[string]bool // lemma -> projects using it, full window
	// Phrase track: counts of known multi-word phrases over the full window,
	// kept separate from the lemma counts because phrases are matched on the
	// surface word stream (stopwords kept), not the lemma stream.
	Phrases        map[string]int
	PhraseProjects map[string]map[string]bool // phrase -> projects using it
	// The name guard's inputs: mid-sentence uses of each lemma, and how many
	// of those were title-cased. Full window, since being a name is a property
	// of the word rather than of a window.
	MidUses, MidCapped map[string]int
}

// IsName reports whether the corpus writes this lemma as a name. See
// text.NameCounts for what the two tallies mean and text.NameCapFloor for
// where the threshold came from.
func (w Windows) IsName(lemma string) bool {
	return text.IsName(w.MidUses[lemma], w.MidCapped[lemma])
}

// Pass tokenizes every turn exactly once, producing the window counts for
// the riser detector, the full-window counts for the chronic detector, and
// the deduplicated sentence corpus for the cloze pass. text.Sentences is
// token-preserving, so the counts equal what whole-turn tokenization would
// produce. Turns older than baselineStart still feed the corpus and the
// full counts — the vet context window is deliberately wider than the scan
// windows.
func Pass(turns []corpus.Turn, recentStart, baselineStart time.Time, pm *phrase.Matcher) (Windows, *cloze.Corpus) {
	w := Windows{
		Recent:         map[string]int{},
		Baseline:       map[string]int{},
		PerProject:     map[string]map[string]int{},
		Full:           map[string]int{},
		FullProjects:   map[string]map[string]bool{},
		Phrases:        map[string]int{},
		PhraseProjects: map[string]map[string]bool{},
		MidUses:        map[string]int{},
		MidCapped:      map[string]int{},
	}
	sents := cloze.NewCorpus()
	for _, t := range turns {
		// Case has to come off the turn text: Sentence.Raw is lowercased for
		// the frame detector, and storing a second cased copy would double the
		// cloze corpus for one boolean per word.
		text.NameCounts(t.Text, w.MidUses, w.MidCapped)
		inBaseline := !t.Time.Before(baselineStart) && t.Time.Before(recentStart)
		inRecent := !t.Time.Before(recentStart)
		switch {
		case inRecent:
			w.RecentTurns++
		case inBaseline:
			w.BaselineTurns++
		}
		for _, sent := range text.Sentences(t.Text) {
			sents.Add(sent)
			if !pm.Empty() {
				hits := map[string]int{}
				pm.Count(text.Words(sent.Raw), hits)
				for ph, n := range hits {
					w.Phrases[ph] += n
					pp := w.PhraseProjects[ph]
					if pp == nil {
						pp = map[string]bool{}
						w.PhraseProjects[ph] = pp
					}
					pp[t.Project] = true
				}
			}
			for _, tok := range sent.Tokens {
				w.Full[tok]++
				w.FullTotal++
				fp := w.FullProjects[tok]
				if fp == nil {
					fp = map[string]bool{}
					w.FullProjects[tok] = fp
				}
				fp[t.Project] = true
				if inBaseline {
					w.Baseline[tok]++
					w.BaselineTotal++
				} else if inRecent {
					w.Recent[tok]++
					w.RecentTotal++
					pp := w.PerProject[tok]
					if pp == nil {
						pp = map[string]int{}
						w.PerProject[tok] = pp
					}
					pp[t.Project]++
				}
			}
		}
	}
	return w, sents
}

// Sentences builds just the cloze corpus, for callers (vet) that don't
// need window counts.
func Sentences(turns []corpus.Turn) *cloze.Corpus {
	sents := cloze.NewCorpus()
	for _, t := range turns {
		for _, sent := range text.Sentences(t.Text) {
			sents.Add(sent)
		}
	}
	return sents
}

// Candidates gathers the WordNet ladder candidates for lemma across all
// senses: each candidate's IC (first occurrence wins, in sense order), the
// lemma's own IC, and the deduplicated candidate list. Candidates that
// contain the lemma itself as a word are excluded — "calque formation" is
// no alternative to "calque".
func Candidates(wn *wordnet.DB, lemma string) (cands []string, ic map[string]float64, selfIC float64) {
	ic = map[string]float64{}
	selfSet := false
	for _, l := range wn.Ladders(lemma) {
		for _, rung := range l.Rungs {
			if rung.Source == "self" {
				if !selfSet {
					selfIC, selfSet = rung.IC, true
				}
				continue
			}
			if containsWord(rung.Word, lemma) {
				continue
			}
			if _, ok := ic[rung.Word]; !ok {
				ic[rung.Word] = rung.IC
				cands = append(cands, rung.Word)
			}
		}
	}
	return cands, ic, selfIC
}

// demoteOptions is what the gate may choose a demotion from: exactly the
// window the reader is shown, minus the lemma itself.
//
// The full ladder spans both directions around the lemma — it is sorted by
// information content and the lemma sits wherever its own specificity puts
// it. Offering all of it lets the gate pick a rung *stronger* than the word
// it is demoting, and rungs the injection never displayed, so the display
// hook could swap in a word the reader was never offered. Measured on a live
// report before this: nine of twenty-one demotions fell outside the shown
// window and two inverted the direction outright (ledger -> journal,
// group -> set). After: none of sixteen.
//
// An empty result is a real answer, not a failure. It means the lemma is
// already the weakest rung on its own ladder, and judge.Safety treats a tic
// with nothing to offer as coherent for exactly that reason.
func demoteOptions(ladder []report.Rung, lemma string) []string {
	shown := report.TrimLadder(ladder, lemma)
	out := make([]string, 0, len(shown))
	for _, r := range shown {
		if r.Word != lemma {
			out = append(out, r.Word)
		}
	}
	return out
}

func containsWord(candidate, lemma string) bool {
	for _, p := range strings.Fields(candidate) {
		if p == lemma {
			return true
		}
	}
	return false
}

// Options parameterizes Build. All fields are required (no zero-value
// defaults are applied here — the CLI owns the defaults). ChronicTop 0
// disables the chronic stage.
type Options struct {
	RecentDays, BaselineDays int
	Top, MinCount            int
	MinRatio                 float64
	MaxUses                  int             // sentences judged per word
	MinUses                  int             // below this, a word is skipped as unjudgeable
	Threshold                float64         // cosine floor for a clean substitution
	MinClean                 float64         // clean fraction a rung needs to survive
	ChronicTop               int             // max chronic entries to add after the risers
	MarkedTop                int             // max marked entries (concrete figurative tics) to add; 0 disables
	MinChronicRate           float64         // per-1k full-window rate floor for chronic candidates
	RarityFloor              float64         // WordIC (SemCor -ln p) floor for the rare-word chronic route
	ProperNouns              map[string]bool // lemmas to suppress outright — known project/tool names a frequency+sense pass mistakes for tics
	KnownTics                map[string]bool // curated single-word leans — a third chronic admission route, for common-English words the rarity route can't see (surface, frame, honor)
	Phrases                  *phrase.Matcher // curated multi-word leans, counted as a separate awareness-only track
	PhraseTop                int             // max phrase entries to add; 0 disables the phrase track
	MinPhraseCount           int             // minimum full-window occurrences for a phrase to surface
}

// Chronic evidence gates: a steady high-rate word is only flagged when a
// tic signal fires. Two deterministic routes — the genitive frame
// repeating across its uses ("the spine of X"), or the word being rare in
// general English while frequent in this corpus (load-bearing: WordIC
// 13.1 vs ~8 for ordinary domain words like test/session). Context
// clustering is deliberately NOT a route: domain vocabulary legitimately
// clusters at the same +0.02..0.07 delta as real tics, so it can't
// separate them (measured, not assumed).
const (
	chronicFrameFloor  = 0.25
	chronicMinProjects = 3
	// chronicMinRareLen guards the rarity route against abbreviations:
	// 3-letter "rare words" (doc, env, app) are almost always shorthand
	// whose WordNet senses mislead (doc -> doctor). The frame route is
	// exempt — repeated framing is direct evidence regardless of length.
	chronicMinRareLen = 4
	// chronicCleanFloor is the stricter rung filter for chronic entries:
	// their candidate sets merge many senses, and at the riser floor the
	// wrong-sense hypernyms (slot -> coin machine) survive into the demote
	// window. Measured on real data: the right-sense candidates of true
	// chronic tics clear 0.5; most wrong-sense artifacts don't.
	chronicCleanFloor = 0.5
	// fallbackCleanFloor bounds the chronic empty-ladder fallback: rungs
	// below it are wrong-sense artifacts, not weakly-supported substitutes.
	fallbackCleanFloor = 0.3
	// markedIncongFloor gates the marked route on context-incongruity: the
	// cosine distance between a word's literal sense (its GloVe vector) and
	// the centroid of the contexts it actually appears in. A live metaphor
	// (load-bearing, measured at 1.00) is a physical word recurring in
	// non-physical contexts, so its sense and its contexts diverge sharply;
	// literal jargon (running 0.34, slot 0.60, hook 0.57) is used in its home
	// neighborhood, so they agree. The floor is a recall net — it keeps the
	// figurative candidates and drops the literal ones with no seeding — and
	// hands survivors to the judge, the only thing that separates a live
	// metaphor (load-bearing -> tic) from a noisy-vector term of art (grep,
	// config -> suppressed). Set below load-bearing's 1.00 with margin for
	// less-extreme metaphors, above the literal-jargon cluster (~0.3-0.7).
	markedIncongFloor = 0.85
)

// markParts splits a lemma into its hyphen/underscore components, for the
// GloVe fallback when the compound itself is out of vocabulary (load-bearing
// -> mean(load, bearing)).
func markParts(lemma string) []string {
	return strings.FieldsFunc(lemma, func(r rune) bool { return r == '-' || r == '_' })
}

// incongruity scores how far a lemma's literal sense sits from the contexts
// it appears in: 1 - cos(wordVector, contextCentroid). The word vector is the
// lemma's GloVe vector, or the mean of its parts when the compound is out of
// vocabulary; the context centroid is the mean of every other token across
// the lemma's sample uses. ok is false when either side is unrepresentable.
func incongruity(tbl *embed.Table, lemma string, uses [][]string) (float64, bool) {
	var wv []float32
	if tbl.Has(lemma) {
		wv = tbl.Mean([]string{lemma})
	} else {
		wv = tbl.Mean(markParts(lemma))
	}
	var ctx []string
	for _, u := range uses {
		for _, tok := range u {
			if tok != lemma {
				ctx = append(ctx, tok)
			}
		}
	}
	cv := tbl.Mean(ctx)
	if wv == nil || cv == nil {
		return 0, false
	}
	return 1 - embed.Cos(wv, cv), true
}

// VectorLoader loads unit vectors restricted to vocab. Injected so tests
// supply a synthetic table and so Build stays ignorant of file layout.
// It is called at most once, and not at all when no risers survive.
type VectorLoader func(vocab map[string]bool) (*embed.Table, error)

// Build runs the whole offline pipeline over turns and returns the report
// the hook will inject from.
//
// jdg is the optional term-of-art gate: when non-nil, each assembled entry's
// word, vetted demote rungs, and real sample uses are put to the fenced LLM
// judge, which the deterministic stack provably cannot replace — it tells a
// precise term of art ("hook", suppressed) from a dilutable tic ("substrate",
// kept, with the truer rung named). nil disables the gate (pure-deterministic
// behavior); an inconclusive verdict fails safe to the un-gated entry.
func Build(turns []corpus.Turn, wn *wordnet.DB, loadVectors VectorLoader, jdg judge.Judger, now time.Time, opts Options) (*report.Report, error) {
	recentStart := now.AddDate(0, 0, -opts.RecentDays)
	baselineStart := recentStart.AddDate(0, 0, -opts.BaselineDays)
	w, sents := Pass(turns, recentStart, baselineStart, opts.Phrases)
	risers := detect.Rank(w.Recent, w.PerProject, w.Baseline, w.RecentTotal, w.BaselineTotal, opts.MinCount, opts.MinRatio, opts.Top)

	type job struct {
		kind   string // "riser" or "chronic"
		riser  detect.Result
		lemma  string
		rate   float64 // full-window per-1k (chronic entries)
		frame  float64 // FrameFraction over the lemma's uses
		rarity float64 // WordIC, set only when the rarity route admitted it
		known  bool    // admitted via the curated known-tics route
		incong float64 // context-incongruity, set for marked candidates post-vectors
		uses   [][]string
		cands  []string
		candIC map[string]float64
		selfIC float64
	}
	vocab := map[string]bool{}
	baselineUses := sents.Sample(opts.MaxUses)
	for w := range cloze.Vocab(baselineUses, nil) {
		vocab[w] = true
	}

	// prepare gathers everything about a lemma that doesn't need vectors;
	// ok=false means it can't make an actionable entry (no WordNet ladder —
	// which also drops project-name noise — or too few real uses to judge).
	prepare := func(lemma string) (job, bool) {
		cands, candIC, selfIC := Candidates(wn, lemma)
		if len(cands) == 0 {
			return job{}, false
		}
		uses := sents.Uses(lemma, opts.MaxUses)
		if len(uses) < opts.MinUses {
			return job{}, false
		}
		frame, _ := sents.FrameFraction(lemma)
		for w := range cloze.Vocab(uses, cands) {
			vocab[w] = true
		}
		vocab[lemma] = true
		return job{lemma: lemma, frame: frame, uses: uses, cands: cands, candIC: candIC, selfIC: selfIC}, true
	}

	var jobs []job
	flagged := map[string]bool{}
	for _, r := range risers {
		j, ok := prepare(r.Lemma)
		if !ok {
			continue
		}
		j.kind, j.riser, j.rate = "riser", r, r.RecentRate
		flagged[r.Lemma] = true
		jobs = append(jobs, j)
	}

	// Chronic stage: steady high-rate dispersed words the riser detector
	// structurally can't see (a chronic tic is its own baseline). Both
	// admission routes — frame repetition, rare-in-English — need no
	// vectors, so the stage is fully decided before the vector load.
	if opts.ChronicTop > 0 && w.FullTotal > 0 {
		type cand struct {
			lemma string
			rate  float64
		}
		var list []cand
		for lemma, n := range w.Full {
			rate := float64(n) / float64(w.FullTotal) * 1000
			if rate < opts.MinChronicRate || flagged[lemma] || len(w.FullProjects[lemma]) < chronicMinProjects {
				continue
			}
			list = append(list, cand{lemma, rate})
		}
		sort.Slice(list, func(i, j int) bool {
			if list[i].rate != list[j].rate {
				return list[i].rate > list[j].rate
			}
			return list[i].lemma < list[j].lemma
		})
		framed, rare, known := 0, 0, 0
		for _, c := range list {
			if framed >= opts.ChronicTop && rare >= opts.ChronicTop && known >= opts.ChronicTop {
				break
			}
			isKnown := opts.KnownTics[c.lemma]
			isRare := len(c.lemma) >= chronicMinRareLen && wn.WordIC(c.lemma) >= opts.RarityFloor
			j, ok := prepare(c.lemma)
			if !ok {
				continue
			}
			switch {
			// Known route first: a curated lean carries its own evidence, so
			// label it as such even when it would also clear the frame or
			// rarity gate (surface/frame/honor are common in English — the
			// rarity route structurally can't see them).
			case isKnown && known < opts.ChronicTop:
				known++
				j.known = true
			case j.frame >= chronicFrameFloor && framed < opts.ChronicTop:
				framed++
			case isRare && rare < opts.ChronicTop:
				rare++
				j.rarity = wn.WordIC(c.lemma)
			default:
				continue
			}
			j.kind, j.rate = "chronic", c.rate
			flagged[c.lemma] = true // dedupe: keep the marked route off chronic words
			jobs = append(jobs, j)
		}
	}

	// Marked stage: concrete-vehicle figurative tics (load-bearing) that the
	// frequency-, spread-, and rarity-ranked routes all bury, because their
	// rate is modest and every cheap statistic that separates them from
	// ordinary jargon (rare, concrete, dispersed) is shared by dead-metaphor
	// terms of art (hook, stack). The split is semantic, so the net is wide
	// (dispersed + rare-in-English + concrete) and the judge is the
	// discriminator: it keeps the live metaphors and drops the terms of art.
	// Concreteness needs vectors, so it is applied in the vector loop; here we
	// only gather the rare-and-dispersed candidates and order them by rate so
	// the highest-rate confirmed tics surface first within the MarkedTop slots.
	if opts.MarkedTop > 0 && w.FullTotal > 0 {
		type cand struct {
			lemma string
			rate  float64
		}
		var list []cand
		for lemma, n := range w.Full {
			rate := float64(n) / float64(w.FullTotal) * 1000
			if rate < opts.MinChronicRate || flagged[lemma] || len(w.FullProjects[lemma]) < chronicMinProjects {
				continue
			}
			if len(lemma) < chronicMinRareLen || wn.WordIC(lemma) < opts.RarityFloor {
				continue
			}
			list = append(list, cand{lemma, rate})
		}
		sort.Slice(list, func(i, j int) bool {
			if list[i].rate != list[j].rate {
				return list[i].rate > list[j].rate
			}
			return list[i].lemma < list[j].lemma
		})
		for _, c := range list {
			j, ok := prepare(c.lemma)
			if !ok {
				continue
			}
			j.kind, j.rate, j.rarity = "marked", c.rate, wn.WordIC(c.lemma)
			jobs = append(jobs, j)
		}
	}

	rep := &report.Report{GeneratedAt: now, RecentDays: opts.RecentDays, BaselineDays: opts.BaselineDays}

	// Phrase track: curated stock phrases the single-token detector can't see.
	// Awareness-only — there is no synonym ladder for "i want to honor that" —
	// so it needs no vectors and is decided here, before the vector load and
	// the quiet-window short-circuit. Appended after the word entries so the
	// actionable ladders lead.
	phrases := phraseEntries(w, opts)

	if len(jobs) == 0 {
		rep.Entries = append(rep.Entries, phrases...)
		return rep, nil // quiet window: skip the vector scan entirely
	}

	// The marked route's incongruity score needs each candidate compound's
	// parts (load-bearing -> load, bearing) in the table for the out-of-vocab
	// fallback; the context tokens are already corpus words, hence in vocab.
	for _, j := range jobs {
		if j.kind == "marked" {
			for _, p := range markParts(j.lemma) {
				vocab[p] = true
			}
		}
	}

	tbl, err := loadVectors(vocab)
	if err != nil {
		return nil, err
	}
	base := cloze.Variance(tbl, baselineUses, "")

	// Score and order the marked candidates by context-incongruity now that
	// vectors are loaded: the live metaphors rise, the literal jargon sinks.
	// The marked jobs are a contiguous suffix (gathered last), so sorting that
	// slice puts the most-figurative candidates first for the judge to vet.
	firstMarked := -1
	for i := range jobs {
		if jobs[i].kind == "marked" {
			if firstMarked < 0 {
				firstMarked = i
			}
			jobs[i].incong, _ = incongruity(tbl, jobs[i].lemma, jobs[i].uses)
		}
	}
	if firstMarked >= 0 {
		ms := jobs[firstMarked:]
		sort.SliceStable(ms, func(a, b int) bool { return ms[a].incong > ms[b].incong })
	}

	// Each route earns its own output budget so curated leans (known) and
	// figurative tics (marked) aren't crowded out by frame/rarity finds.
	added := map[string]int{}
	for _, j := range jobs {
		if j.kind == "chronic" {
			bucket := "chronic"
			if j.known {
				bucket = "known"
			}
			if added[bucket] >= opts.ChronicTop {
				continue
			}
		}
		if j.kind == "marked" {
			if added["marked"] >= opts.MarkedTop {
				continue
			}
			// Recall net: keep the figurative candidates (high sense/context
			// divergence), drop the literal ones, before spending a ladder vet
			// or a judge call. The judge then drops the noisy-vector terms of
			// art that incongruity admits for the wrong reason (grep, config).
			if j.incong <= markedIncongFloor {
				continue
			}
		}
		isChronic := j.kind == "chronic" || j.kind == "marked"
		v := cloze.Variance(tbl, j.uses, j.lemma)
		kind := j.kind
		if kind == "marked" {
			kind = "chronic" // a marked tic is a chronic; render and note it as one
		}
		e := report.Entry{
			Kind:         kind,
			Lemma:        j.lemma,
			RecentCount:  j.riser.RecentCount,
			Ratio:        j.riser.Ratio,
			Rate:         j.rate,
			FrameFrac:    j.frame,
			Rarity:       j.rarity,
			Known:        j.known,
			ClusterDelta: v.Clustered - base.Clustered,
			Uses:         v.Uses,
		}
		cleanFloor := opts.MinClean
		if isChronic && cleanFloor < chronicCleanFloor {
			cleanFloor = chronicCleanFloor
		}
		ranked := cloze.RankSubstitutes(tbl, j.uses, j.lemma, j.cands, opts.Threshold)
		for _, c := range ranked {
			if float64(c.Clean)/float64(c.Total) < cleanFloor {
				continue
			}
			e.Ladder = append(e.Ladder, report.Rung{Word: c.Word, IC: j.candIC[c.Word], Clean: c.Clean, Total: c.Total})
		}
		if len(e.Ladder) == 0 && isChronic {
			// A chronic word already passed strong evidence gates (rate +
			// dispersion + frame/rarity); the clean cliff must not silence
			// the flag entirely — keep the two best-fitting candidates that
			// still clear the wrong-sense floor. Risers get no such mercy:
			// their empty-ladder drop doubles as the noise filter for
			// borderline topic words.
			for i, c := range ranked {
				if i >= 2 || float64(c.Clean)/float64(c.Total) < fallbackCleanFloor {
					break
				}
				e.Ladder = append(e.Ladder, report.Rung{Word: c.Word, IC: j.candIC[c.Word], Clean: c.Clean, Total: c.Total})
			}
		}
		if len(e.Ladder) == 0 {
			continue // a tic with no vetted alternative isn't actionable awareness
		}
		e.Ladder = append(e.Ladder, report.Rung{Word: j.lemma, IC: j.selfIC})
		// stable: equal-IC rungs (same-synset synonyms) keep their
		// RankSubstitutes order — best empirical substitute first
		sort.SliceStable(e.Ladder, func(a, b int) bool { return e.Ladder[a].IC < e.Ladder[b].IC })

		// Deterministic proper-noun guard, ahead of the fence: a project or
		// tool name is never a dilutable tic, and a frequency+sense pass
		// (human or LLM) reliably mistakes it for one. The judge is told
		// outright that a product name is a term of art and still called
		// "chrome" a filler adjective meaning shiny, with a fluent paragraph
		// on why. Suppressing here also saves the call.
		//
		// The curated list stays as the override for what the rate misses —
		// an all-caps ticket prefix, a name that is also an ordinary word in
		// lowercase — but it is no longer the only thing standing here, which
		// is what "install and forget" requires.
		if opts.ProperNouns[j.lemma] || w.IsName(j.lemma) {
			continue
		}

		// The gate: the one judgment the deterministic stack can't make.
		if jdg != nil {
			if v, ok := jdg.Judge(j.lemma, demoteOptions(e.Ladder, j.lemma), j.uses); ok {
				if v.Role == judge.RoleTermOfArt {
					continue // precise term of art: no valid substitute — suppress, don't count it
				}
				e.JudgeRole, e.JudgeNote, e.DemoteTo = v.Role, v.Note, v.DemoteTo
			}
		}

		rep.Entries = append(rep.Entries, e)
		switch {
		case j.kind == "chronic" && j.known:
			added["known"]++
		case j.kind == "chronic":
			added["chronic"]++
		case j.kind == "marked":
			added["marked"]++
		}
	}
	rep.Entries = append(rep.Entries, phrases...)
	attachSparklines(rep, turns, now, sparkWeeks)
	return rep, nil
}

// phraseEntries surfaces the most-used curated phrases as awareness-only
// entries. A fixed multi-word phrase is unambiguously diction, not topic, so
// it needs no cross-project dispersion test (the trick single words rely on to
// separate a tic from a domain noun) — a count floor plus a top-N cap keeps it
// honest, and the entries carry no ladder because no synonym replaces a stock
// phrase. PhraseTop 0 disables the track.
func phraseEntries(w Windows, opts Options) []report.Entry {
	if opts.PhraseTop <= 0 || w.FullTotal == 0 {
		return nil
	}
	type cand struct {
		text  string
		count int
		proj  int
	}
	var list []cand
	for ph, n := range w.Phrases {
		if n < opts.MinPhraseCount {
			continue
		}
		list = append(list, cand{ph, n, len(w.PhraseProjects[ph])})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].count != list[j].count {
			return list[i].count > list[j].count
		}
		return list[i].text < list[j].text
	})
	if len(list) > opts.PhraseTop {
		list = list[:opts.PhraseTop]
	}
	out := make([]report.Entry, 0, len(list))
	for _, c := range list {
		out = append(out, report.Entry{
			Kind:     "phrase",
			Lemma:    c.text,
			Count:    c.count,
			Projects: c.proj,
			Rate:     float64(c.count) / float64(w.FullTotal) * 1000,
		})
	}
	return out
}

// sparkWeeks is how many trailing 7-day buckets each entry's sparkline spans.
const sparkWeeks = 8

// attachSparklines records a trailing weekly per-1k rate series on every
// non-phrase entry, for the render-time sparkline. One pass over the turns
// buckets the entry lemmas by week; a week with no tokens is stored as -1 (a
// gap), which keeps the series valid JSON where a NaN would not. Phrase
// entries are skipped: their multi-word lemma never matches a single token, so
// a series would be all gaps. Bucketing matches the trend command: fixed
// 7-day windows counted back from now.
func attachSparklines(rep *report.Report, turns []corpus.Turn, now time.Time, weeks int) {
	if len(rep.Entries) == 0 {
		return
	}
	const week = 7 * 24 * time.Hour
	idx := make(map[string]int, len(rep.Entries))
	for i, e := range rep.Entries {
		if e.Kind == "phrase" {
			continue
		}
		idx[e.Lemma] = i
	}
	counts := make([][]int, weeks)
	for b := range counts {
		counts[b] = make([]int, len(rep.Entries))
	}
	totals := make([]int, weeks)
	for _, t := range turns {
		b := weeks - 1 - int(now.Sub(t.Time)/week)
		if b < 0 || b >= weeks {
			continue
		}
		for _, tok := range text.Tokens(t.Text) {
			totals[b]++
			if i, ok := idx[tok]; ok {
				counts[b][i]++
			}
		}
	}
	for i := range rep.Entries {
		if rep.Entries[i].Kind == "phrase" {
			continue
		}
		series := make([]float64, weeks)
		for b := 0; b < weeks; b++ {
			if totals[b] == 0 {
				series[b] = -1 // gap sentinel; render turns this back into a hole
				continue
			}
			series[b] = float64(counts[b][i]) / float64(totals[b]) * 1000
		}
		rep.Entries[i].Spark = series
	}
}
