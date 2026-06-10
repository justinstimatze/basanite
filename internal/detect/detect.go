// Package detect ranks lemmas by frequency drift: recent rate against the
// user's own trailing baseline, weighted so concentration wins (5× this week
// vs ~0 baseline beats a flat common word).
package detect

import (
	"math"
	"sort"
)

// Result is one rising lemma with its window statistics.
type Result struct {
	Lemma        string
	RecentCount  int
	OutsideCount int     // recent count outside the loudest project
	Projects     int     // distinct projects using it in the recent window
	RecentRate   float64 // per 1k tokens
	BaselineRate float64 // per 1k tokens
	Ratio        float64 // RecentRate / BaselineRate (smoothed)
	Score        float64
}

// Rank scores every lemma seen in the recent window against the baseline
// window and returns the top risers, descending by score.
//
// Score = outsideCount × ln(smoothed rate ratio), a Poisson G-statistic
// shape with two deterministic weights folded in:
//
//   - The log-ratio term is the concentration weight: a word at ~0 baseline
//     gets a large multiplier, while a flat common word's ratio ~1 logs to
//     ~0 regardless of raw count. Add-half smoothing keeps never-seen words
//     finite.
//   - outsideCount is the recent count with the single loudest project
//     excluded (leave-loudest-out). A diction tic rises across projects; a
//     topic word (project names, this week's domain nouns) rises in one, so
//     dropping each word's own loudest project zeroes topic noise without
//     touching dispersed tics.
//
// minRatio floors the rate ratio: common dev vocabulary drifts at 1.2–1.6×
// with the week's topic mix, and a floor near 2 cuts that noise while real
// tics (forming ones) clear it easily.
func Rank(recent map[string]int, perProject map[string]map[string]int, baseline map[string]int, recentTotal, baselineTotal int, minCount int, minRatio float64, top int) []Result {
	if recentTotal == 0 || baselineTotal == 0 {
		return nil
	}
	var out []Result
	for lemma, rc := range recent {
		if rc < minCount {
			continue
		}
		bc := baseline[lemma]
		rr := (float64(rc) + 0.5) / float64(recentTotal) * 1000
		br := (float64(bc) + 0.5) / float64(baselineTotal) * 1000
		ratio := rr / br
		// ratio <= 1 guards Score's math.Log against a caller passing a
		// sub-1 minRatio; at the shipped defaults the floor subsumes it
		if ratio <= 1 || ratio < minRatio {
			continue
		}
		loudest := 0
		for _, c := range perProject[lemma] {
			if c > loudest {
				loudest = c
			}
		}
		outside := rc - loudest
		if outside == 0 {
			continue // single-project word: topic, not diction
		}
		out = append(out, Result{
			Lemma:        lemma,
			RecentCount:  rc,
			OutsideCount: outside,
			Projects:     len(perProject[lemma]),
			RecentRate:   float64(rc) / float64(recentTotal) * 1000,
			BaselineRate: float64(bc) / float64(baselineTotal) * 1000,
			Ratio:        ratio,
			Score:        float64(outside) * math.Log(ratio),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Lemma < out[j].Lemma
	})
	if top > 0 && len(out) > top {
		out = out[:top]
	}
	return out
}
