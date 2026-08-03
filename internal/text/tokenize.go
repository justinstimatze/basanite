// Package text turns assistant prose into a stream of normalized lemmas.
package text

import (
	"regexp"
	"strings"
	"unicode"
)

var (
	fencedCode = regexp.MustCompile("(?s)```.*?```")
	inlineCode = regexp.MustCompile("`[^`\n]*`")
	urls       = regexp.MustCompile(`https?://\S+`)
	paths      = regexp.MustCompile(`[~./]?(?:[\w.-]+/)+[\w.-]*`)
)

// Clean strips the non-prose surfaces from markdown: code fences, inline
// code, URLs, and filesystem paths — identifier soup would otherwise
// dominate any frequency or embedding pass.
func Clean(s string) string {
	s = fencedCode.ReplaceAllString(s, " ")
	s = inlineCode.ReplaceAllString(s, " ")
	s = urls.ReplaceAllString(s, " ")
	return paths.ReplaceAllString(s, " ")
}

// Tokens extracts lowercase lemma tokens from markdown prose (Clean is
// applied first). Hyphenated words survive as single tokens (load-bearing,
// oracle-free).
func Tokens(s string) []string {
	return tokenize(Clean(s))
}

// Sentence is one sentence's two representations: the lemma-token stream
// the frequency and embedding passes run on, and the raw lowercased text
// for analyses that need stopwords back (frame detection — "the spine of"
// is invisible in a stream that drops "the" and "of").
type Sentence struct {
	Tokens []string
	Raw    string
}

// Sentences cleans once, splits into sentences, and tokenizes each. It is
// token-preserving: the concatenation of the Tokens fields equals
// Tokens(s), because sentence delimiters are never token characters — so
// counts derived from sentences match counts derived from whole turns, and
// one tokenization pass can feed both the frequency model and the cloze
// corpus.
func Sentences(s string) []Sentence {
	s = Clean(s)
	var out []Sentence
	for _, sent := range strings.FieldsFunc(s, isSentenceDelim) {
		if toks := tokenize(sent); len(toks) > 0 {
			out = append(out, Sentence{Tokens: toks, Raw: strings.ToLower(strings.TrimSpace(sent))})
		}
	}
	return out
}

// Words splits raw lowercased prose into its full word stream — stopwords
// kept, no lemmatization — using the same character classes as the
// tokenizer. For positional patterns over Sentence.Raw.
func Words(s string) []string {
	var words []string
	var b strings.Builder
	flush := func() {
		if b.Len() == 0 {
			return
		}
		if w := strings.Trim(b.String(), "-'"); w != "" {
			words = append(words, w)
		}
		b.Reset()
	}
	for _, r := range s {
		if unicode.IsLetter(r) || r == '-' || r == '\'' {
			b.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return words
}

// NameCounts accumulates, per lemma, how often a word appears mid-sentence and
// how often it is title-cased when it does. A word capitalized in the middle of
// a sentence most of the time is a name — a project, a product, a tool — and no
// ladder word substitutes for a name.
//
// Two exclusions carry the signal. Sentence-initial words are skipped because
// their case says nothing. ALL-CAPS is skipped because it is emphasis and
// acronyms, which is a different signal and a much noisier one.
func NameCounts(s string, mid, capped map[string]int) {
	for _, sent := range strings.FieldsFunc(Clean(s), isSentenceDelim) {
		for i, w := range Words(sent) {
			if i == 0 {
				continue
			}
			lemma := Lemma(strings.ToLower(w))
			if !keep(lemma) {
				continue
			}
			mid[lemma]++
			if r := []rune(w); unicode.IsUpper(r[0]) && strings.ToUpper(w) != w {
				capped[lemma]++
			}
		}
	}
}

const (
	// NameCapFloor: above this share of title-cased mid-sentence uses, a lemma
	// is a name and never a dilutable tic.
	//
	// Measured over ~120 judged words in 90 days of transcripts. Seven names
	// landed between 65% and 98% (chrome, haiku, coop, python, doppler, wick,
	// ruffle); the highest ordinary word was 31%; nothing at all fell between.
	// Real leans sit at the bottom: surface 0.5%, arm 0.9%, substrate 1.7%.
	// The floor sits in the middle of a gap that wide because it can.
	NameCapFloor = 0.5
	// MinNameUses is where the share stops being noise. A word with fewer
	// mid-sentence uses than this across the whole window is orders of
	// magnitude below the rate floor anyway, so the guard never decides it.
	MinNameUses = 20
)

// IsName reports whether a lemma's mid-sentence capitalization marks it as a
// name, given its mid-sentence use count and how many of those were title-cased.
func IsName(mid, capped int) bool {
	return mid >= MinNameUses && float64(capped)/float64(mid) >= NameCapFloor
}

func isSentenceDelim(r rune) bool {
	return r == '.' || r == '!' || r == '?' || r == '\n' || r == ';'
}

// tokenize is the raw pass over already-Cleaned prose.
func tokenize(s string) []string {
	var toks []string
	var b strings.Builder
	flush := func() {
		if b.Len() == 0 {
			return
		}
		w := strings.Trim(b.String(), "-'")
		b.Reset()
		w = Lemma(w)
		if keep(w) {
			toks = append(toks, w)
		}
	}
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || r == '-' || r == '\'' {
			b.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return toks
}

// Lemma applies conservative normalization: possessives and plurals only.
// Verb inflections (-ing, -ed) are deliberately left alone — naive suffix
// stripping makes junk lemmas (during→dur), and since both analysis windows
// use the same rules, split verb-form counts cancel in the delta anyway.
func Lemma(w string) string {
	w = strings.TrimSuffix(w, "'s")
	w = strings.TrimSuffix(w, "'")
	switch {
	case len(w) > 4 && strings.HasSuffix(w, "ies"):
		return w[:len(w)-3] + "y"
	case len(w) > 4 && (strings.HasSuffix(w, "sses") || strings.HasSuffix(w, "shes") || strings.HasSuffix(w, "ches") || strings.HasSuffix(w, "xes")):
		return w[:len(w)-2]
	case len(w) > 3 && strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss") && !strings.HasSuffix(w, "us") && !strings.HasSuffix(w, "is"):
		return w[:len(w)-1]
	}
	return w
}

func keep(w string) bool {
	if len(w) < 3 {
		return false
	}
	if stopwords[w] {
		return false
	}
	return true
}

// stopwords: function words plus conversational filler that no one needs
// flagged as a "tic". Content words only past this gate.
//
// Number words are here in full. The list used to stop at "three", which read
// as a reasonable place to stop and was not one: "four" and "five" then passed
// as content, and they are common enough in prose about files and steps to
// clear the chronic rate floor. Both reached the judge, which — obliged to
// pick a rung — returned "four -> whole number" and "five -> figure".
var stopwords = func() map[string]bool {
	list := `the a an and or but if then else when while for nor so yet as at by
	in into of off on onto out over to under up with within without about above
	across after against along among around before behind below beneath beside
	between beyond down during except from inside near outside since through
	throughout till toward towards until upon via i me my mine we us our ours
	you your yours he him his she her hers it its they them their theirs this
	that these those who whom whose which what where why how am is are was were
	be been being have has had having do does did doing will would shall should
	can could may might must ought need dare not no nor never none nothing
	neither either both each every all any some few many much more most other
	another such only own same so than too very just also even still already
	again once here there now then always often sometimes usually rarely ever
	yes yeah okay let lets get got gets getting make makes made making go goes
	going gone went come comes came coming take takes took taken taking see
	sees saw seen seeing know knows knew known knowing think thinks thought
	thinking want wants wanted use uses used using way ways thing things stuff
	well right like don doesn didn isn aren wasn weren won wouldn couldn
	shouldn can't cannot it's that's there's here's what's let's i'm i've i'll
	you're you've we're we've they're don't doesn't didn't isn't aren't wasn't
	weren't won't wouldn't couldn't shouldn't
	new old good bad big small long short high low out-of because actually
	really quite rather pretty bit lot lots kind sort part back next last off
	one two three four five six seven eight nine ten eleven twelve twenty
	thirty forty fifty hundred thousand million
	first second third fourth fifth sixth seventh eighth ninth tenth`
	m := make(map[string]bool)
	for _, w := range strings.Fields(list) {
		m[Lemma(w)] = true
		m[w] = true
	}
	return m
}()
