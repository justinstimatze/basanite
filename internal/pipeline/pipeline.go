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
}

// Pass tokenizes every turn exactly once, producing the window counts for
// the riser detector, the full-window counts for the chronic detector, and
// the deduplicated sentence corpus for the cloze pass. text.Sentences is
// token-preserving, so the counts equal what whole-turn tokenization would
// produce. Turns older than baselineStart still feed the corpus and the
// full counts — the vet context window is deliberately wider than the scan
// windows.
func Pass(turns []corpus.Turn, recentStart, baselineStart time.Time) (Windows, *cloze.Corpus) {
	w := Windows{
		Recent:       map[string]int{},
		Baseline:     map[string]int{},
		PerProject:   map[string]map[string]int{},
		Full:         map[string]int{},
		FullProjects: map[string]map[string]bool{},
	}
	sents := cloze.NewCorpus()
	for _, t := range turns {
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
	MaxUses                  int     // sentences judged per word
	MinUses                  int     // below this, a word is skipped as unjudgeable
	Threshold                float64 // cosine floor for a clean substitution
	MinClean                 float64 // clean fraction a rung needs to survive
	ChronicTop               int     // max chronic entries to add after the risers
	MinChronicRate           float64 // per-1k full-window rate floor for chronic candidates
	RarityFloor              float64 // WordIC (SemCor -ln p) floor for the rare-word chronic route
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
)

// VectorLoader loads unit vectors restricted to vocab. Injected so tests
// supply a synthetic table and so Build stays ignorant of file layout.
// It is called at most once, and not at all when no risers survive.
type VectorLoader func(vocab map[string]bool) (*embed.Table, error)

// Build runs the whole offline pipeline over turns and returns the report
// the hook will inject from.
func Build(turns []corpus.Turn, wn *wordnet.DB, loadVectors VectorLoader, now time.Time, opts Options) (*report.Report, error) {
	recentStart := now.AddDate(0, 0, -opts.RecentDays)
	baselineStart := recentStart.AddDate(0, 0, -opts.BaselineDays)
	w, sents := Pass(turns, recentStart, baselineStart)
	risers := detect.Rank(w.Recent, w.PerProject, w.Baseline, w.RecentTotal, w.BaselineTotal, opts.MinCount, opts.MinRatio, opts.Top)

	type job struct {
		kind   string // "riser" or "chronic"
		riser  detect.Result
		lemma  string
		rate   float64 // full-window per-1k (chronic entries)
		frame  float64 // FrameFraction over the lemma's uses
		rarity float64 // WordIC, set only when the rarity route admitted it
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
		framed, rare := 0, 0
		for _, c := range list {
			if framed >= opts.ChronicTop && rare >= opts.ChronicTop {
				break
			}
			isRare := len(c.lemma) >= chronicMinRareLen && wn.WordIC(c.lemma) >= opts.RarityFloor
			j, ok := prepare(c.lemma)
			if !ok {
				continue
			}
			switch {
			case j.frame >= chronicFrameFloor && framed < opts.ChronicTop:
				framed++
			case isRare && rare < opts.ChronicTop:
				rare++
				j.rarity = wn.WordIC(c.lemma)
			default:
				continue
			}
			j.kind, j.rate = "chronic", c.rate
			jobs = append(jobs, j)
		}
	}

	rep := &report.Report{GeneratedAt: now, RecentDays: opts.RecentDays, BaselineDays: opts.BaselineDays}
	if len(jobs) == 0 {
		return rep, nil // quiet window: skip the vector scan entirely
	}

	tbl, err := loadVectors(vocab)
	if err != nil {
		return nil, err
	}
	base := cloze.Variance(tbl, baselineUses, "")

	chronicAdded := 0
	for _, j := range jobs {
		if j.kind == "chronic" && chronicAdded >= opts.ChronicTop {
			continue
		}
		v := cloze.Variance(tbl, j.uses, j.lemma)
		e := report.Entry{
			Kind:         j.kind,
			Lemma:        j.lemma,
			RecentCount:  j.riser.RecentCount,
			Ratio:        j.riser.Ratio,
			Rate:         j.rate,
			FrameFrac:    j.frame,
			Rarity:       j.rarity,
			ClusterDelta: v.Clustered - base.Clustered,
			Uses:         v.Uses,
		}
		cleanFloor := opts.MinClean
		if j.kind == "chronic" && cleanFloor < chronicCleanFloor {
			cleanFloor = chronicCleanFloor
		}
		ranked := cloze.RankSubstitutes(tbl, j.uses, j.lemma, j.cands, opts.Threshold)
		for _, c := range ranked {
			if float64(c.Clean)/float64(c.Total) < cleanFloor {
				continue
			}
			e.Ladder = append(e.Ladder, report.Rung{Word: c.Word, IC: j.candIC[c.Word], Clean: c.Clean, Total: c.Total})
		}
		if len(e.Ladder) == 0 && j.kind == "chronic" {
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
		rep.Entries = append(rep.Entries, e)
		if j.kind == "chronic" {
			chronicAdded++
		}
	}
	return rep, nil
}
