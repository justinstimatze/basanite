// Package wordnet is a minimal, offline reader for the Princeton WordNet 3.0
// database files plus a Resnik information-content table — just enough
// surface for basanite's specificity ladder: synonyms, hypernyms (the demote
// direction), similar-to clusters for adjectives, and an IC value per
// synset.
package wordnet

import (
	"bufio"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// POS tags follow WordNet's single-letter convention; satellite adjectives
// (s) are folded into a.
var posFiles = map[byte]string{'n': "noun", 'v': "verb", 'a': "adj", 'r': "adv"}

// Synset is one WordNet concept node.
type Synset struct {
	Offset    string // zero-padded 8-digit offset
	POS       byte
	Words     []string
	Hypernyms []string // keys (offset+pos) — @ and @i pointers
	Similar   []string // keys — & pointers (adjective clusters)
	Gloss     string
}

// Key is the synset's unique id: offset + pos letter.
func (s *Synset) Key() string { return s.Offset + string(s.POS) }

// DB is the loaded database.
type DB struct {
	index     map[byte]map[string][]string // pos -> lemma -> synset keys, sense order
	synsets   map[string]*Synset           // key -> synset
	ic        map[string]float64           // key -> Resnik IC (nouns/verbs only)
	wordFreq  map[string]int               // lemma -> SemCor tag count (all senses)
	freqTotal int
}

// Load reads the dict/ files and the IC table. icPath may be "" to skip IC
// (ladders then order purely by word frequency).
func Load(dictDir, icPath string) (*DB, error) {
	db := &DB{
		index:    map[byte]map[string][]string{},
		synsets:  map[string]*Synset{},
		ic:       map[string]float64{},
		wordFreq: map[string]int{},
	}
	for pos, name := range posFiles {
		if err := db.loadData(pos, filepath.Join(dictDir, "data."+name)); err != nil {
			return nil, err
		}
		if err := db.loadIndex(pos, filepath.Join(dictDir, "index."+name)); err != nil {
			return nil, err
		}
	}
	if err := db.loadSenseCounts(filepath.Join(dictDir, "index.sense")); err != nil {
		return nil, err
	}
	if icPath != "" {
		if err := db.loadIC(icPath); err != nil {
			return nil, err
		}
	}
	return db, nil
}

func eachLine(path string, fn func(string)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "  ") || line == "" { // license header
			continue
		}
		fn(line)
	}
	return sc.Err()
}

// loadData parses data.<pos>: offset lexfile sstype wcnt(hex) (word lexid)*
// pcnt (sym offset pos srctgt)* [frames] | gloss
func (db *DB) loadData(pos byte, path string) error {
	return eachLine(path, func(line string) {
		gloss := ""
		if i := strings.Index(line, "| "); i >= 0 {
			gloss = strings.TrimSpace(line[i+2:])
			line = line[:i]
		}
		f := strings.Fields(line)
		if len(f) < 4 {
			return
		}
		s := &Synset{Offset: f[0], POS: pos, Gloss: gloss}
		wcnt, err := strconv.ParseInt(f[3], 16, 0)
		if err != nil || wcnt < 1 {
			return
		}
		// bounds-check against the declared counts: a truncated or
		// corrupted data file must skip the line, not panic
		i := 4
		if i+2*int(wcnt) > len(f) {
			return
		}
		for w := 0; w < int(wcnt); w++ {
			s.Words = append(s.Words, cleanWord(f[i]))
			i += 2 // skip lex_id
		}
		pcnt, err := strconv.Atoi(f[i])
		if err != nil || pcnt < 0 {
			return
		}
		i++
		if i+4*pcnt > len(f) {
			return
		}
		for p := 0; p < pcnt; p++ {
			sym, off, ppos := f[i], f[i+1], f[i+2]
			if ppos == "" {
				return
			}
			key := off + normPOS(ppos[0])
			switch sym {
			case "@", "@i":
				s.Hypernyms = append(s.Hypernyms, key)
			case "&":
				s.Similar = append(s.Similar, key)
			}
			i += 4
		}
		db.synsets[s.Key()] = s
	})
}

// loadIndex parses index.<pos>: lemma pos synset_cnt p_cnt [syms]*
// sense_cnt tagsense_cnt offset*
func (db *DB) loadIndex(pos byte, path string) error {
	idx := map[string][]string{}
	db.index[pos] = idx
	return eachLine(path, func(line string) {
		f := strings.Fields(line)
		if len(f) < 6 {
			return
		}
		pcnt, err := strconv.Atoi(f[3])
		if err != nil || pcnt < 0 || 4+pcnt+2 > len(f) {
			return // malformed count: skip the line, not panic
		}
		offsets := f[4+pcnt+2:]
		keys := make([]string, 0, len(offsets))
		for _, off := range offsets {
			keys = append(keys, off+string(pos))
		}
		idx[f[0]] = keys
	})
}

// loadSenseCounts sums SemCor tag counts per lemma from index.sense:
// lemma%sense_key offset sense_number tag_cnt. This is the word-level
// frequency table used to order rungs when synset IC is unavailable
// (adjectives, adverbs, multiword synonyms).
func (db *DB) loadSenseCounts(path string) error {
	err := eachLine(path, func(line string) {
		f := strings.Fields(line)
		if len(f) != 4 {
			return
		}
		lemma, _, _ := strings.Cut(f[0], "%")
		n, err := strconv.Atoi(f[3])
		if err != nil {
			return
		}
		db.wordFreq[lemma] += n
		db.freqTotal += n
	})
	return err
}

// loadIC parses a WordNet::Similarity IC file: "<offset><pos> <count>
// [ROOT]" with unpadded offsets. IC = -ln(count / posRootTotal).
func (db *DB) loadIC(path string) error {
	type raw struct {
		key   string
		pos   byte
		count float64
	}
	var rows []raw
	rootTotal := map[byte]float64{}
	err := eachLine(path, func(line string) {
		f := strings.Fields(line)
		if len(f) < 2 || strings.HasPrefix(f[0], "wnver") {
			return
		}
		pos := f[0][len(f[0])-1]
		off := f[0][:len(f[0])-1]
		count, err := strconv.ParseFloat(f[1], 64)
		if err != nil {
			return
		}
		key := fmt.Sprintf("%08s", off) + string(pos)
		rows = append(rows, raw{key, pos, count})
		if len(f) > 2 && f[2] == "ROOT" {
			rootTotal[pos] += count
		}
	})
	if err != nil {
		return err
	}
	for _, r := range rows {
		t := rootTotal[r.pos]
		if t == 0 || r.count <= 0 {
			continue
		}
		db.ic[r.key] = -math.Log(r.count / t)
	}
	return nil
}

// SynsetIC returns the Resnik IC of a synset key, or ok=false when the IC
// table has no entry (adjectives, adverbs, zero-count synsets).
func (db *DB) SynsetIC(key string) (float64, bool) {
	v, ok := db.ic[key]
	return v, ok
}

// WordIC is the frequency-based fallback specificity: -ln of the lemma's
// smoothed SemCor probability. Rarer word -> higher value -> stronger rung.
func (db *DB) WordIC(lemma string) float64 {
	n := db.wordFreq[normLemma(lemma)]
	return -math.Log((float64(n) + 0.5) / float64(db.freqTotal))
}

// Lookup returns the synsets for a lemma at a POS, in WordNet sense order
// (most-common sense first).
func (db *DB) Lookup(lemma string, pos byte) []*Synset {
	var out []*Synset
	for _, key := range db.index[pos][normLemma(lemma)] {
		if s := db.synsets[key]; s != nil {
			out = append(out, s)
		}
	}
	return out
}

// Get resolves a synset key.
func (db *DB) Get(key string) *Synset { return db.synsets[key] }

func normPOS(p byte) string {
	if p == 's' {
		return "a"
	}
	return string(p)
}

func normLemma(w string) string {
	return strings.ReplaceAll(strings.ToLower(w), " ", "_")
}

// cleanWord strips adjective syntax markers — load-bearing(a) -> load-bearing
// — and converts underscores back to spaces for display.
func cleanWord(w string) string {
	if i := strings.IndexByte(w, '('); i >= 0 {
		w = w[:i]
	}
	return strings.ReplaceAll(strings.ToLower(w), "_", " ")
}
