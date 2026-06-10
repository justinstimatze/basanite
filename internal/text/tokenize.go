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
	weren't won't wouldn't couldn't shouldn't one two three first second third
	new old good bad big small long short high low out-of because actually
	really quite rather pretty bit lot lots kind sort part back next last off`
	m := make(map[string]bool)
	for _, w := range strings.Fields(list) {
		m[Lemma(w)] = true
		m[w] = true
	}
	return m
}()
