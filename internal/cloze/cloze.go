// Package cloze implements Mitigation A and the variance freebie: judge
// WordNet candidates by real substitutability in the writer's own past
// sentences, and classify a word as signature vs tic by how clustered its
// contexts of use are.
//
// The trick: at turn start there is no target sentence to context-fit
// against — but the corpus of past uses IS the context. A candidate that
// preserves the sentence vector across most real uses is a true replacement
// in this idiolect; a wrong-sense artifact wobbles and self-eliminates.
package cloze

import (
	"hash/fnv"
	"sort"
	"strings"

	"github.com/justinstimatze/basanite/internal/embed"
	"github.com/justinstimatze/basanite/internal/text"
)

// MinSentenceTokens is the floor below which a sentence fragment is too
// short to carry a usable context vector.
const MinSentenceTokens = 5

// Corpus is a deduplicated, lemma-indexed store of sentences, built once
// per run so neither Uses nor Sample re-tokenizes anything. Raw sentence
// text is kept alongside the token stream for positional analyses (frame
// detection) that need stopwords back.
type Corpus struct {
	sents []text.Sentence
	index map[string][]int // lemma -> indices into sents
	seen  map[uint64]bool  // fnv-1a of the joined tokens, for dedup
}

// NewCorpus returns an empty corpus.
func NewCorpus() *Corpus {
	return &Corpus{index: map[string][]int{}, seen: map[uint64]bool{}}
}

// Add records one sentence. Fragments shorter than MinSentenceTokens and
// exact token-stream duplicates of earlier sentences are ignored —
// repeated boilerplate can't fake a clustered context.
func (c *Corpus) Add(sent text.Sentence) {
	if len(sent.Tokens) < MinSentenceTokens {
		return
	}
	h := fnv.New64a()
	for _, t := range sent.Tokens {
		h.Write([]byte(t))
		h.Write([]byte{0})
	}
	key := h.Sum64()
	if c.seen[key] {
		return
	}
	c.seen[key] = true
	i := len(c.sents)
	c.sents = append(c.sents, sent)
	indexed := map[string]bool{}
	for _, t := range sent.Tokens {
		if !indexed[t] {
			indexed[t] = true
			c.index[t] = append(c.index[t], i)
		}
	}
}

// Len reports the number of stored sentences.
func (c *Corpus) Len() int { return len(c.sents) }

// Uses returns the token streams of up to max sentences containing the
// target lemma, evenly stride-sampled over the whole window — not the
// first max hits, so one chatty week can't own the sample.
func (c *Corpus) Uses(target string, max int) [][]string {
	hits := stride(c.index[target], max)
	out := make([][]string, 0, len(hits))
	for _, i := range hits {
		out = append(out, c.sents[i].Tokens)
	}
	return out
}

// Sample returns up to max sentence token streams evenly across the corpus
// regardless of content: the reference distribution for Verdict.Clustered.
// In a one-author dev corpus everything is topically similar, so the
// absolute clustering number is uninterpretable — what matters is the
// delta above this baseline.
func (c *Corpus) Sample(max int) [][]string {
	idx := make([]int, len(c.sents))
	for i := range idx {
		idx[i] = i
	}
	idx = stride(idx, max)
	out := make([][]string, 0, len(idx))
	for _, i := range idx {
		out = append(out, c.sents[i].Tokens)
	}
	return out
}

// frameDeterminers are the words that may open a "<det> <target> of"
// genitive frame.
var frameDeterminers = map[string]bool{
	"the": true, "a": true, "an": true, "this": true, "that": true,
	"these": true, "those": true, "its": true, "their": true, "your": true,
	"my": true, "our": true, "his": true, "her": true, "each": true,
	"every": true,
}

// FrameFraction measures frame-shaped tic-ness: the share of the target's
// uses matching the genitive metaphor frame "<det> <target> of" ("the
// spine of the design"). A word can be topically diverse — invisible to
// the variance classifier — while the frame repeats; the frame is the tic.
// Computed over raw sentence text because the frame's evidence is exactly
// the stopwords the token stream drops.
func (c *Corpus) FrameFraction(target string) (frac float64, uses int) {
	hits := c.index[target]
	if len(hits) == 0 {
		return 0, 0
	}
	framed := 0
	for _, i := range hits {
		words := text.Words(c.sents[i].Raw)
		for j := 1; j+1 < len(words); j++ {
			if words[j+1] == "of" && frameDeterminers[words[j-1]] && text.Lemma(words[j]) == target {
				framed++
				break
			}
		}
	}
	return float64(framed) / float64(len(hits)), len(hits)
}

func stride(idx []int, max int) []int {
	if max <= 0 || len(idx) <= max {
		return idx
	}
	out := make([]int, 0, max)
	for i := 0; i < max; i++ {
		out = append(out, idx[i*len(idx)/max])
	}
	return out
}

// Vocab returns the word set needed from the embedding table for these
// uses plus candidates — pass to embed.Load to avoid loading 400k vectors.
func Vocab(uses [][]string, candidates []string) map[string]bool {
	v := map[string]bool{}
	for _, u := range uses {
		for _, w := range u {
			v[w] = true
		}
	}
	for _, c := range candidates {
		for _, part := range strings.Fields(c) {
			v[part] = true
		}
	}
	return v
}

// Verdict is the variance freebie: the mean pairwise cosine of a word's
// use-vectors (sentence vectors with the word itself excluded). Scattered
// across diverse contexts -> flexible signature, leave it alone. Clustered
// in near-identical contexts -> reflexive tic, flag it.
type Verdict struct {
	Uses      int
	Clustered float64 // mean pairwise cosine; higher = more tic-like
}

// Variance computes the verdict for the target over its uses.
func Variance(tbl *embed.Table, uses [][]string, target string) Verdict {
	var vecs [][]float32
	for _, u := range uses {
		if v := tbl.Mean(without(u, target)); v != nil {
			vecs = append(vecs, v)
		}
	}
	v := Verdict{Uses: len(vecs)}
	if len(vecs) < 2 {
		return v
	}
	var sum float64
	var n int
	for i := 0; i < len(vecs); i++ {
		for j := i + 1; j < len(vecs); j++ {
			sum += embed.Cos(vecs[i], vecs[j])
			n++
		}
	}
	v.Clustered = sum / float64(n)
	return v
}

// Candidate is one substitution candidate with its empirical fit.
type Candidate struct {
	Word    string
	Clean   int     // uses where substitution preserved the sentence vector
	Total   int     // uses scored
	MeanCos float64 // mean cosine(original, substituted) across uses
}

// RankSubstitutes cloze-tests each candidate against every use: mask the
// target, substitute the candidate, and compare sentence vectors. clean
// counts uses with cosine >= threshold. Candidates sort by clean count,
// then mean cosine.
//
// An out-of-vocabulary target still works: the original vector simply
// excludes it, and the comparison measures how well the candidate fits the
// surrounding context — which is the question anyway.
func RankSubstitutes(tbl *embed.Table, uses [][]string, target string, candidates []string, threshold float64) []Candidate {
	var out []Candidate
	for _, cand := range candidates {
		if cand == target {
			continue
		}
		parts := strings.Fields(cand) // multiword candidates: mean of parts
		inVocab := true
		for _, p := range parts {
			if !tbl.Has(p) {
				inVocab = false
				break
			}
		}
		if !inVocab {
			continue // unjudgeable: an OOV substitution would score a free near-1 cosine
		}
		c := Candidate{Word: cand}
		var sum float64
		for _, u := range uses {
			rest := without(u, target)
			orig := tbl.Mean(u)
			subst := tbl.Mean(append(append([]string{}, rest...), parts...))
			if orig == nil || subst == nil {
				continue
			}
			cos := embed.Cos(orig, subst)
			sum += cos
			c.Total++
			if cos >= threshold {
				c.Clean++
			}
		}
		if c.Total == 0 {
			continue
		}
		c.MeanCos = sum / float64(c.Total)
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Clean != out[j].Clean {
			return out[i].Clean > out[j].Clean
		}
		return out[i].MeanCos > out[j].MeanCos
	})
	return out
}

func without(toks []string, target string) []string {
	out := make([]string, 0, len(toks))
	for _, t := range toks {
		if t != target {
			out = append(out, t)
		}
	}
	return out
}
