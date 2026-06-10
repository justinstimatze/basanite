package judge

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"sort"
	"strings"
	"time"
)

// Record is one persisted verdict. It is two things at once: a cache entry
// (so an unchanged word+ladder is not re-judged on the next refresh) and a
// calibration row (the append-only log the hybrid-loop discipline requires —
// did the gate decide well? the answer is read back from these, not asserted).
type Record struct {
	Word          string `json:"word"`
	LadderHash    string `json:"ladder_hash"`
	Model         string `json:"model"`
	SchemaVersion int    `json:"schema_version"`
	Role          string `json:"role"`
	DemoteTo      string `json:"demote_to,omitempty"`
	Note          string `json:"note,omitempty"`
	WellFormed    bool   `json:"well_formed"`
	Safe          bool   `json:"safe"`
	At            string `json:"at"` // RFC3339; caller stamps (clocks are injected, not read here)
}

// LadderHash keys a verdict to its inputs. Order-independent, so reordering
// rungs (which the cloze ranker may do run to run) does not bust the cache;
// a changed *set* of rungs does, because the judgment could differ.
func LadderHash(ladder []string) string {
	s := append([]string(nil), ladder...)
	sort.Strings(s)
	sum := sha256.Sum256([]byte(strings.Join(s, "\x00")))
	return hex.EncodeToString(sum[:8])
}

// cacheKey identifies a verdict by everything that affects it: the word, its
// ladder, the model, and the schema/prompt version. Any change misses.
func cacheKey(word, ladderHash, model string, schemaVersion int) string {
	return word + "|" + ladderHash + "|" + model + "|" + string(rune('0'+schemaVersion%10))
}

// Store is the append-only verdict log, indexed for cache lookup. Last write
// wins per key, so a re-judged word supersedes its stale verdict.
type Store struct {
	path  string
	byKey map[string]Record
}

// LoadStore reads the JSONL at path (absent file = empty store).
func LoadStore(path string) (*Store, error) {
	s := &Store{path: path, byKey: map[string]Record{}}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<16), 1<<20)
	for sc.Scan() {
		var r Record
		if json.Unmarshal(sc.Bytes(), &r) != nil {
			continue // a corrupt line never blocks the cache
		}
		s.byKey[cacheKey(r.Word, r.LadderHash, r.Model, r.SchemaVersion)] = r
	}
	return s, sc.Err()
}

// Lookup returns a cached verdict for word+ladder under model at the current
// schema version, if one was recorded.
func (s *Store) Lookup(word string, ladder []string, model string) (Record, bool) {
	r, ok := s.byKey[cacheKey(word, LadderHash(ladder), model, SchemaVersion)]
	return r, ok
}

// Append writes one record to the log and updates the index. The append is
// the calibration trail; the index update is the cache.
func (s *Store) Append(r Record) error {
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		return err
	}
	s.byKey[cacheKey(r.Word, r.LadderHash, r.Model, r.SchemaVersion)] = r
	return nil
}

// RecordFrom builds a Record from a verdict and its inputs, stamped at now.
func RecordFrom(word string, ladder []string, model string, v Verdict, wellFormed, safe bool, now time.Time) Record {
	return Record{
		Word:          word,
		LadderHash:    LadderHash(ladder),
		Model:         model,
		SchemaVersion: SchemaVersion,
		Role:          v.Role,
		DemoteTo:      v.DemoteTo,
		Note:          v.Note,
		WellFormed:    wellFormed,
		Safe:          safe,
		At:            now.UTC().Format(time.RFC3339),
	}
}
