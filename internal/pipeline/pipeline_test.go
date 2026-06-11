package pipeline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/justinstimatze/basanite/internal/corpus"
	"github.com/justinstimatze/basanite/internal/embed"
	"github.com/justinstimatze/basanite/internal/phrase"
	"github.com/justinstimatze/basanite/internal/report"
	"github.com/justinstimatze/basanite/internal/text"
	"github.com/justinstimatze/basanite/internal/wordnet"
)

func loadTestWN(t *testing.T) *wordnet.DB {
	t.Helper()
	base := filepath.Join("..", "wordnet", "testdata")
	db, err := wordnet.Load(filepath.Join(base, "dict"), filepath.Join(base, "ic-test.dat"))
	if err != nil {
		t.Fatal(err)
	}
	return db
}

// vectorFile returns a loader over a tiny table where "entity" is nearly
// identical to "dog" (so it survives the clean-substitution filter) and
// the context words exist.
func testLoader(t *testing.T) VectorLoader {
	t.Helper()
	path := filepath.Join(t.TempDir(), "vecs.txt")
	content := "dog 1.0 0.1 0.0\n" +
		"entity 1.0 0.11 0.0\n" +
		"hungry 0.6 0.8 0.1\n" +
		"barked 0.5 1.0 0.0\n" +
		"loudly 0.4 0.9 0.1\n" +
		"neighbor 0.7 0.4 0.2\n" +
		"yard 0.4 0.5 0.2\n" +
		"morning 0.3 0.6 0.3\n" +
		"garden 0.35 0.55 0.3\n" +
		"park 0.5 0.5 0.2\n" +
		"kitchen 0.45 0.6 0.25\n" +
		"door 0.55 0.45 0.2\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return func(vocab map[string]bool) (*embed.Table, error) {
		return embed.Load(path, vocab)
	}
}

func dogTurns(now time.Time) []corpus.Turn {
	mk := func(daysAgo int, project, text string) corpus.Turn {
		return corpus.Turn{Time: now.AddDate(0, 0, -daysAgo), Project: project, Text: text}
	}
	// recent window: dog used heavily across two projects, in six distinct
	// sentences that each keep >= 5 content tokens after stopword removal
	return []corpus.Turn{
		mk(1, "alpha", "The hungry dog barked loudly across the quiet yard fence. "+
			"Our neighbor's spotted dog barked sharply before the early morning walk."),
		mk(2, "alpha", "Another stray dog wandered slowly past the garden gate yesterday afternoon."),
		mk(3, "beta", "The shaggy dog chased several pigeons toward the empty park bench. "+
			"That muddy dog dragged a soggy leash across the wet kitchen floor."),
		mk(4, "beta", "A clever dog opened the heavy cellar door with a damp nose."),
		// baseline window: same flavor of prose, no dog
		mk(10, "alpha", "The hungry neighbor walked loudly across the quiet yard fence yesterday morning."),
		mk(12, "beta", "Several pigeons wandered slowly past the empty park bench near the garden gate."),
	}
}

func TestPassMatchesWholeTurnTokenization(t *testing.T) {
	now := time.Now()
	turns := dogTurns(now)
	recentStart := now.AddDate(0, 0, -7)
	baselineStart := recentStart.AddDate(0, 0, -14)
	w, sents := Pass(turns, recentStart, baselineStart, nil)

	// the token-preserving property: window totals must equal what
	// whole-turn tokenization produces
	var wantRecent, wantBaseline int
	for _, turn := range turns {
		n := len(text.Tokens(turn.Text))
		if !turn.Time.Before(recentStart) {
			wantRecent += n
		} else if !turn.Time.Before(baselineStart) {
			wantBaseline += n
		}
	}
	if w.RecentTotal != wantRecent || w.BaselineTotal != wantBaseline {
		t.Errorf("totals = %d/%d, want %d/%d", w.RecentTotal, w.BaselineTotal, wantRecent, wantBaseline)
	}
	if w.RecentTurns != 4 || w.BaselineTurns != 2 {
		t.Errorf("turns = %d/%d, want 4/2", w.RecentTurns, w.BaselineTurns)
	}
	if len(w.PerProject["dog"]) != 2 {
		t.Errorf("dog projects = %v, want alpha and beta", w.PerProject["dog"])
	}
	if uses := sents.Uses("dog", 0); len(uses) < 5 {
		t.Errorf("dog uses = %d, want >= 5 distinct sentences", len(uses))
	}
}

func TestCandidates(t *testing.T) {
	wn := loadTestWN(t)
	cands, ic, selfIC := Candidates(wn, "dog")
	// the synthetic fixture gives dog one synonym ("domestic dog", which
	// contains the lemma and must be filtered) and one hypernym (entity)
	if len(cands) != 1 || cands[0] != "entity" {
		t.Fatalf("cands = %v, want [entity]", cands)
	}
	if ic["entity"] >= selfIC {
		t.Errorf("hypernym IC %f should be below self IC %f", ic["entity"], selfIC)
	}
}

func TestBuildEndToEnd(t *testing.T) {
	now := time.Now()
	rep, err := Build(dogTurns(now), loadTestWN(t), testLoader(t), nil, now, Options{
		RecentDays: 7, BaselineDays: 14,
		Top: 8, MinCount: 5, MinRatio: 2.0,
		MaxUses: 50, MinUses: 5,
		Threshold: 0.97, MinClean: 0.4,
	})
	if err != nil {
		t.Fatal(err)
	}
	var entry *struct {
		lemma string
		words []string
	}
	for _, e := range rep.Entries {
		if e.Lemma == "dog" {
			var words []string
			for _, r := range e.Ladder {
				words = append(words, r.Word)
			}
			entry = &struct {
				lemma string
				words []string
			}{e.Lemma, words}
		}
	}
	if entry == nil {
		t.Fatalf("no dog entry in report: %+v", rep.Entries)
	}
	if strings.Join(entry.words, " ") != "entity dog" {
		t.Errorf("ladder = %v, want [entity dog] (weakest first, self last)", entry.words)
	}
}

// chronicTurns spreads framed "the dog of X" uses evenly across both scan
// windows and three projects: a steady rate (ratio ~1, invisible to the
// riser detector) with strong frame evidence.
func chronicTurns(now time.Time) []corpus.Turn {
	mk := func(daysAgo int, project, text string) corpus.Turn {
		return corpus.Turn{Time: now.AddDate(0, 0, -daysAgo), Project: project, Text: text}
	}
	return []corpus.Turn{
		mk(1, "alpha", "The dog of the household barked loudly across the quiet yard fence."),
		mk(3, "beta", "The dog of the neighbor chased several pigeons toward the empty park bench."),
		mk(5, "gamma", "The dog of the morning patrol wandered slowly past the garden gate."),
		mk(9, "alpha", "The dog of the kitchen door dragged a soggy leash across the wet floor."),
		mk(12, "beta", "The dog of the evening shift opened the heavy cellar door with a damp nose."),
		mk(16, "gamma", "The dog of the early hour barked sharply before the morning walk began."),
	}
}

func TestBuildChronicEntry(t *testing.T) {
	now := time.Now()
	rep, err := Build(chronicTurns(now), loadTestWN(t), testLoader(t), nil, now, Options{
		RecentDays: 7, BaselineDays: 14,
		Top: 8, MinCount: 3, MinRatio: 2.0,
		MaxUses: 50, MinUses: 5,
		Threshold: 0.97, MinClean: 0.4,
		// RarityFloor above any fixture WordIC: dog must come in through
		// the frame route alone
		ChronicTop: 4, MinChronicRate: 0.3, RarityFloor: 99,
	})
	if err != nil {
		t.Fatal(err)
	}
	var dog *report.Entry
	for i := range rep.Entries {
		if rep.Entries[i].Lemma == "dog" {
			dog = &rep.Entries[i]
		}
	}
	if dog == nil {
		t.Fatalf("no chronic dog entry: %+v", rep.Entries)
	}
	if dog.Kind != "chronic" {
		t.Errorf("kind = %q, want chronic (steady rate must not register as a riser)", dog.Kind)
	}
	if dog.FrameFrac < 0.99 {
		t.Errorf("frame fraction = %f, want 1.0 (every use is framed)", dog.FrameFrac)
	}
	if dog.Rate <= 0 {
		t.Errorf("chronic entry must carry its full-window rate, got %f", dog.Rate)
	}
}

// The rarity route: a word that's steady (not a riser), unframed, but
// rare in the reference frequency table — the load-bearing shape.
func TestBuildChronicRareWordRoute(t *testing.T) {
	now := time.Now()
	mk := func(daysAgo int, project, text string) corpus.Turn {
		return corpus.Turn{Time: now.AddDate(0, 0, -daysAgo), Project: project, Text: text}
	}
	turns := []corpus.Turn{
		mk(1, "alpha", "The load-bearing wall held the upper floor weight nicely today."),
		mk(3, "beta", "Another load-bearing beam carried the heavy roof truss without complaint."),
		mk(5, "gamma", "That load-bearing column supported the entire mezzanine deck structure alone."),
		mk(9, "alpha", "Every load-bearing joist under the kitchen floor needed careful inspection."),
		mk(13, "beta", "The old load-bearing lintel above the cellar door finally cracked."),
		mk(16, "gamma", "A reinforced load-bearing frame stiffened the whole garden shed noticeably."),
	}
	// fixture WordIC: load-bearing has SemCor tag count 1 of 16 total ->
	// WordIC ~2.4; a floor of 2.0 admits it while dog (~1.3) stays out
	rep, err := Build(turns, loadTestWN(t), rareLoader(t), nil, now, Options{
		RecentDays: 7, BaselineDays: 14,
		Top: 8, MinCount: 3, MinRatio: 2.0,
		MaxUses: 50, MinUses: 5,
		Threshold: 0.97, MinClean: 0.4,
		ChronicTop: 4, MinChronicRate: 0.3, RarityFloor: 2.0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Entries) != 1 || rep.Entries[0].Lemma != "load-bearing" {
		t.Fatalf("entries = %+v, want one chronic load-bearing entry", rep.Entries)
	}
	e := rep.Entries[0]
	if e.Kind != "chronic" || e.Rarity < 2.0 || e.FrameFrac >= 0.25 {
		t.Errorf("entry should be chronic via rarity, not frame: %+v", e)
	}
}

func rareLoader(t *testing.T) VectorLoader {
	t.Helper()
	path := filepath.Join(t.TempDir(), "vecs.txt")
	content := "load-bearing 1.0 0.1 0.0\n" +
		"supporting 1.0 0.11 0.0\n" +
		"supportive 1.0 0.12 0.0\n" +
		"wall 0.6 0.8 0.1\n" +
		"held 0.5 1.0 0.0\n" +
		"weight 0.4 0.9 0.1\n" +
		"floor 0.7 0.4 0.2\n" +
		"beam 0.4 0.5 0.2\n" +
		"roof 0.3 0.6 0.3\n" +
		"column 0.35 0.55 0.3\n" +
		"kitchen 0.5 0.5 0.2\n" +
		"door 0.45 0.6 0.25\n" +
		"frame 0.55 0.45 0.2\n" +
		"garden 0.5 0.6 0.3\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return func(vocab map[string]bool) (*embed.Table, error) {
		return embed.Load(path, vocab)
	}
}

// steadyDogTurns spreads "dog" evenly across both scan windows and three
// projects with NO genitive frame: a steady, dispersed rate that is neither a
// riser (ratio ~1) nor frame-shaped. Its only handle is the curated known-tics
// route — the calibration that the bingo reference catches common-English
// leans the rarity route structurally can't.
func steadyDogTurns(now time.Time) []corpus.Turn {
	mk := func(daysAgo int, project, text string) corpus.Turn {
		return corpus.Turn{Time: now.AddDate(0, 0, -daysAgo), Project: project, Text: text}
	}
	return []corpus.Turn{
		mk(1, "alpha", "The hungry dog barked loudly across the quiet yard fence today."),
		mk(3, "beta", "A stray dog chased several pigeons toward the empty park bench."),
		mk(5, "gamma", "Another clever dog wandered slowly past the garden gate this morning."),
		mk(9, "alpha", "That muddy dog dragged a soggy leash across the wet kitchen floor."),
		mk(12, "beta", "The shaggy dog opened the heavy cellar door with a damp nose."),
		mk(16, "gamma", "One spotted dog barked sharply before the early morning walk began."),
	}
}

// The known-tics route: a steady, unframed, common-English word the rarity and
// frame routes both miss is admitted only because it's on the curated list,
// and the entry is labelled as such.
func TestBuildChronicKnownRoute(t *testing.T) {
	now := time.Now()
	opts := Options{
		RecentDays: 7, BaselineDays: 14,
		Top: 8, MinCount: 3, MinRatio: 2.0,
		MaxUses: 50, MinUses: 5,
		Threshold: 0.97, MinClean: 0.4,
		// RarityFloor above any fixture WordIC so the rarity route can't admit
		// dog; the turns are unframed so the frame route can't either.
		ChronicTop: 4, MinChronicRate: 0.3, RarityFloor: 99,
	}

	// without the reference, dog has no admission handle at all
	bare, err := Build(steadyDogTurns(now), loadTestWN(t), testLoader(t), nil, now, opts)
	if err != nil {
		t.Fatal(err)
	}
	if hasEntry(bare, "dog") {
		t.Fatal("precondition: steady unframed common word must not surface without the reference")
	}

	// with dog on the curated list, the known route admits it
	opts.KnownTics = map[string]bool{"dog": true}
	rep, err := Build(steadyDogTurns(now), loadTestWN(t), testLoader(t), nil, now, opts)
	if err != nil {
		t.Fatal(err)
	}
	e := entry(rep, "dog")
	if e == nil {
		t.Fatalf("known-tics route did not admit dog: %+v", rep.Entries)
	}
	if e.Kind != "chronic" || !e.Known || e.Rarity != 0 || e.FrameFrac >= 0.25 {
		t.Errorf("dog should be a known-route chronic entry, not rarity/frame: %+v", e)
	}
}

// The phrase track: a curated stock phrase repeated across the corpus surfaces
// as an awareness-only entry, ladderless, even though its words are
// individually invisible to the token detector.
func TestBuildPhraseTrack(t *testing.T) {
	now := time.Now()
	mk := func(daysAgo int, project, text string) corpus.Turn {
		return corpus.Turn{Time: now.AddDate(0, 0, -daysAgo), Project: project, Text: text}
	}
	turns := []corpus.Turn{
		mk(1, "alpha", "I want to honor that, and the garden gate stayed open all morning."),
		mk(3, "beta", "Honestly, I want to honor that pause before the park bench fills up."),
		mk(5, "gamma", "I want to honor that quiet yard where the pigeons gather at dawn."),
		mk(9, "alpha", "Still, I want to honor that slow walk past the cellar door each day."),
		mk(12, "beta", "I want to honor that careful leash work across the wet kitchen floor."),
	}
	pm := phrase.New([]string{"I want to honor that"})
	rep, err := Build(turns, loadTestWN(t), testLoader(t), nil, now, Options{
		RecentDays: 7, BaselineDays: 14,
		Top: 8, MinCount: 99, MinRatio: 2.0, // MinCount high so no word entries compete
		MaxUses: 50, MinUses: 5,
		Threshold: 0.97, MinClean: 0.4,
		Phrases: pm, PhraseTop: 4, MinPhraseCount: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	e := entry(rep, "i want to honor that")
	if e == nil {
		t.Fatalf("phrase track did not surface the stock phrase: %+v", rep.Entries)
	}
	if e.Kind != "phrase" || len(e.Ladder) != 0 || e.Count != 5 {
		t.Errorf("phrase entry malformed: %+v", e)
	}
	if got := e.Rate; got <= 0 {
		t.Errorf("phrase entry must carry its full-window rate, got %f", got)
	}
}

// The phrase track is decided before the vector load, so a phrase-only corpus
// must surface its phrase without ever scanning the vector table.
func TestBuildPhraseSkipsVectors(t *testing.T) {
	now := time.Now()
	mk := func(daysAgo int, project, text string) corpus.Turn {
		return corpus.Turn{Time: now.AddDate(0, 0, -daysAgo), Project: project, Text: text}
	}
	turns := []corpus.Turn{
		mk(1, "alpha", "Take your time with the quiet yard and the open garden gate."),
		mk(3, "beta", "Take your time before the park bench near the cellar door fills."),
		mk(5, "gamma", "Take your time across the wet kitchen floor with the soggy leash."),
	}
	loader := func(map[string]bool) (*embed.Table, error) {
		t.Fatal("vector loader called for a phrase-only corpus")
		return nil, nil
	}
	rep, err := Build(turns, loadTestWN(t), loader, nil, now, Options{
		RecentDays: 7, BaselineDays: 14,
		Top: 8, MinCount: 99, MinRatio: 2.0,
		MaxUses: 50, MinUses: 5,
		Threshold: 0.97, MinClean: 0.4,
		Phrases: phrase.New([]string{"take your time"}), PhraseTop: 4, MinPhraseCount: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if entry(rep, "take your time") == nil {
		t.Fatalf("phrase-only corpus produced no phrase entry: %+v", rep.Entries)
	}
}

// A quiet window must not touch the vector table at all — that's the
// skip-the-347MB-scan optimization.
func TestBuildQuietWindowSkipsVectors(t *testing.T) {
	now := time.Now()
	loader := func(map[string]bool) (*embed.Table, error) {
		t.Fatal("vector loader called for a riser-free corpus")
		return nil, nil
	}
	rep, err := Build(nil, loadTestWN(t), loader, nil, now, Options{
		RecentDays: 7, BaselineDays: 14,
		Top: 8, MinCount: 5, MinRatio: 2.0,
		MaxUses: 50, MinUses: 5,
		Threshold: 0.97, MinClean: 0.4,
	})
	if err != nil || len(rep.Entries) != 0 {
		t.Fatalf("empty corpus: rep=%+v err=%v", rep, err)
	}
}
