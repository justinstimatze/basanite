// basanite detects vocabulary tics in Claude Code transcripts: words whose
// recent frequency has risen against the writer's own trailing baseline.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/justinstimatze/basanite/internal/cloze"
	"github.com/justinstimatze/basanite/internal/corpus"
	"github.com/justinstimatze/basanite/internal/detect"
	"github.com/justinstimatze/basanite/internal/embed"
	"github.com/justinstimatze/basanite/internal/judge"
	"github.com/justinstimatze/basanite/internal/knowntics"
	"github.com/justinstimatze/basanite/internal/phrase"
	"github.com/justinstimatze/basanite/internal/pipeline"
	"github.com/justinstimatze/basanite/internal/report"
	"github.com/justinstimatze/basanite/internal/text"
	"github.com/justinstimatze/basanite/internal/wordnet"
)

var version = "dev" // overridden via -ldflags "-X main.version=..."

const usage = `basanite — vocabulary-tic detection over Claude Code transcripts

usage: basanite <command> [flags]

  scan            rank rising lemmas: recent window vs trailing baseline
  trend <lemma>…  weekly rate per lemma — the effectiveness check
  ladder <word>…  specificity ladder per sense, weakest → strongest
  vet <word>…     judge candidates against your own past sentences
  report          full pipeline (scan→vet→ladder) → state file
  refresh         regenerate the state file if stale (SessionStart entry)
  hook            UserPromptSubmit entry: inject the report
  version         print version

Run 'basanite <command> -h' for command flags. See README.md for data setup.
`

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		fmt.Print(usage)
		return
	}
	cmd := args[0]
	args = args[1:]
	var err error
	switch cmd {
	case "scan":
		err = runScan(args)
	case "trend":
		err = runTrend(args)
	case "ladder":
		err = runLadder(args)
	case "vet":
		err = runVet(args)
	case "report":
		err = runReport(args)
	case "refresh":
		err = runRefresh(args)
	case "hook":
		err = runHook(args)
	case "version", "--version", "-v":
		fmt.Println("basanite", buildVersion())
	case "help", "--help", "-h":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "basanite: unknown command %q\n\n%s", cmd, usage)
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "basanite:", err)
		os.Exit(1)
	}
}

func defaultDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "projects")
}

// runScan ranks rising lemmas: recent window vs trailing baseline.
func runScan(args []string) error {
	fs := flag.NewFlagSet("scan", flag.ExitOnError)
	var (
		dir          = fs.String("dir", defaultDir(), "transcript root to scan")
		recentDays   = fs.Int("recent", 7, "recent window in days")
		baselineDays = fs.Int("baseline", 14, "baseline window in days (preceding the recent window)")
		top          = fs.Int("top", 25, "show top N risers (0 = all)")
		minCount     = fs.Int("min", 5, "minimum recent-window count")
		minRatio     = fs.Float64("ratio", 2.0, "minimum recent/baseline rate ratio")
	)
	fs.Parse(args)

	now := time.Now()
	recentStart := now.AddDate(0, 0, -*recentDays)
	baselineStart := recentStart.AddDate(0, 0, -*baselineDays)

	turns, err := corpus.Read(*dir, baselineStart)
	if err != nil {
		return err
	}

	win, _ := pipeline.Pass(turns, recentStart, baselineStart, nil)

	fmt.Printf("corpus: %d turns / %dk tokens recent (%dd) · %d turns / %dk tokens baseline (%dd)\n\n",
		win.RecentTurns, win.RecentTotal/1000, *recentDays, win.BaselineTurns, win.BaselineTotal/1000, *baselineDays)

	results := detect.Rank(win.Recent, win.PerProject, win.Baseline, win.RecentTotal, win.BaselineTotal, *minCount, *minRatio, *top)
	if len(results) == 0 {
		fmt.Println("no risers found (corpus too small, or windows empty)")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 2, 8, 2, ' ', 0)
	fmt.Fprintln(w, "LEMMA\tRECENT\tOUTSIDE\tPROJ\tR/1K\tBASE/1K\tRATIO\tSCORE")
	for _, r := range results {
		fmt.Fprintf(w, "%s\t%d\t%d\t%d\t%.2f\t%.2f\t%.1f×\t%.1f\n",
			r.Lemma, r.RecentCount, r.OutsideCount, r.Projects, r.RecentRate, r.BaselineRate, r.Ratio, r.Score)
	}
	return w.Flush()
}

// runTrend prints per-week rates for specific lemmas. The transcripts are
// the longitudinal record, so this needs no state file — and it doubles as
// the effectiveness check: after basanite starts injecting awareness of a
// tic, its weekly rate here should visibly fall.
func runTrend(args []string) error {
	fs := flag.NewFlagSet("trend", flag.ExitOnError)
	var (
		dir   = fs.String("dir", defaultDir(), "transcript root to scan")
		weeks = fs.Int("weeks", 8, "number of trailing 7-day buckets")
	)
	fs.Parse(args)
	lemmas := fs.Args()
	if len(lemmas) == 0 {
		return fmt.Errorf("trend needs at least one lemma argument")
	}
	want := map[string]int{} // lemma -> column index
	for i, l := range lemmas {
		// corpus tokens are lowercased, so the query must be too
		want[text.Lemma(strings.ToLower(l))] = i
	}

	// one time representation throughout: fixed 7-day buckets back from
	// now, for the window start, the bucketing, and the labels alike
	const week = 7 * 24 * time.Hour
	now := time.Now()
	start := now.Add(-time.Duration(*weeks) * week)
	turns, err := corpus.Read(*dir, start)
	if err != nil {
		return err
	}

	counts := make([][]int, *weeks) // [bucket][lemma]
	for i := range counts {
		counts[i] = make([]int, len(lemmas))
	}
	totals := make([]int, *weeks)
	for _, t := range turns {
		b := *weeks - 1 - int(now.Sub(t.Time)/week)
		if b < 0 || b >= *weeks {
			continue
		}
		for _, tok := range text.Tokens(t.Text) {
			totals[b]++
			if i, ok := want[tok]; ok {
				counts[b][i]++
			}
		}
	}

	w := tabwriter.NewWriter(os.Stdout, 2, 8, 2, ' ', 0)
	fmt.Fprint(w, "WEEK OF\tTOKENS")
	for _, l := range lemmas {
		fmt.Fprintf(w, "\t%s/1k", l)
	}
	fmt.Fprintln(w)
	for b := 0; b < *weeks; b++ {
		weekStart := now.Add(-time.Duration(*weeks-b) * week)
		fmt.Fprintf(w, "%s\t%dk", weekStart.Format("2006-01-02"), totals[b]/1000)
		for i := range lemmas {
			if totals[b] == 0 {
				fmt.Fprint(w, "\t-")
				continue
			}
			fmt.Fprintf(w, "\t%.2f", float64(counts[b][i])/float64(totals[b])*1000)
		}
		fmt.Fprintln(w)
	}
	return w.Flush()
}

// defaultDataDir finds the WordNet data assets: $BASANITE_DATA, then
// ./data, then ~/.local/share/basanite.
func defaultDataDir() string {
	if d := os.Getenv("BASANITE_DATA"); d != "" {
		return d
	}
	if _, err := os.Stat(filepath.Join("data", "dict")); err == nil {
		return "data"
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "basanite")
}

// runLadder prints the specificity ladder for each sense of a word:
// weakest -> strongest, so the move can be demote, not just swap.
func runLadder(args []string) error {
	fs := flag.NewFlagSet("ladder", flag.ExitOnError)
	dataDir := fs.String("data", defaultDataDir(), "directory holding dict/ and wordnet_ic/")
	fs.Parse(args)
	words := fs.Args()
	if len(words) == 0 {
		return fmt.Errorf("ladder needs at least one word argument")
	}

	db, err := loadWordNet(*dataDir)
	if err != nil {
		return err
	}

	for _, word := range words {
		ladders := db.Ladders(word)
		if len(ladders) == 0 {
			fmt.Printf("%s: not in WordNet\n", word)
			continue
		}
		for _, l := range ladders {
			gloss := l.Synset.Gloss
			if i := strings.IndexByte(gloss, ';'); i > 0 {
				gloss = gloss[:i]
			}
			fmt.Printf("%s (%c) %s\n", word, l.Synset.POS, gloss)
			for i, r := range l.Rungs {
				if i > 0 {
					fmt.Print(" < ")
				}
				marker := ""
				if r.Source == "self" {
					marker = "*"
				}
				fmt.Printf("%s%s(%.1f)", marker, r.Word, r.IC)
			}
			fmt.Println()
			fmt.Println()
		}
	}
	return nil
}

// runVet is Mitigation A plus the variance freebie for one word: collect
// my real past sentences using it, classify signature-vs-tic by use-vector
// clustering, and rank the WordNet ladder candidates by empirical
// substitutability in those sentences.
func runVet(args []string) error {
	fs := flag.NewFlagSet("vet", flag.ExitOnError)
	var (
		dir       = fs.String("dir", defaultDir(), "transcript root to scan")
		dataDir   = fs.String("data", defaultDataDir(), "directory holding dict/, wordnet_ic/, vectors/")
		days      = fs.Int("days", 90, "how far back to collect uses")
		maxUses   = fs.Int("uses", 50, "max sentences to judge against")
		threshold = fs.Float64("threshold", 0.97, "cosine floor for a clean substitution")
	)
	fs.Parse(args)
	words := fs.Args()
	if len(words) == 0 {
		return fmt.Errorf("vet needs at least one word argument")
	}

	wn, err := loadWordNet(*dataDir)
	if err != nil {
		return err
	}
	turns, err := corpus.Read(*dir, time.Now().AddDate(0, 0, -*days))
	if err != nil {
		return err
	}
	sents := pipeline.Sentences(turns)

	// Gather every word's uses and candidates first, so the 347MB vector
	// table is scanned exactly once with the union vocabulary.
	type job struct {
		target     string
		uses       [][]string
		candidates []string
	}
	var jobs []job
	vocab := map[string]bool{}
	baselineUses := sents.Sample(*maxUses)
	for w := range cloze.Vocab(baselineUses, nil) {
		vocab[w] = true
	}
	for _, word := range words {
		target := text.Lemma(strings.ToLower(word))
		candidates, _, _ := pipeline.Candidates(wn, target)
		j := job{target: target, uses: sents.Uses(target, *maxUses), candidates: candidates}
		for w := range cloze.Vocab(j.uses, j.candidates) {
			vocab[w] = true
		}
		vocab[target] = true
		jobs = append(jobs, j)
	}

	tbl, err := gloveLoader(*dataDir)(vocab)
	if err != nil {
		return err
	}

	base := cloze.Variance(tbl, baselineUses, "")
	fmt.Printf("corpus baseline clustering: %.3f over %d random sentences\n\n", base.Clustered, base.Uses)

	for _, j := range jobs {
		fmt.Printf("%s — %d uses in the last %dd\n", j.target, len(j.uses), *days)
		if len(j.uses) < 3 {
			fmt.Println("  too few uses to judge")
			continue
		}
		if len(j.candidates) == 0 {
			fmt.Println("  not in WordNet — no candidates to vet")
			continue
		}

		v := cloze.Variance(tbl, j.uses, j.target)
		fmt.Printf("  context clustering: %.3f (%+.3f vs baseline; above = tic-like, below = signature)\n",
			v.Clustered, v.Clustered-base.Clustered)
		if frac, n := sents.FrameFraction(j.target); frac > 0 {
			fmt.Printf("  frame %q: %d%% of %d uses\n", "the "+j.target+" of", int(frac*100+0.5), n)
		}

		ranked := cloze.RankSubstitutes(tbl, j.uses, j.target, j.candidates, *threshold)
		for i, c := range ranked {
			if i >= 12 {
				break
			}
			fmt.Printf("  %-22s clean %2d/%d  mean cos %.3f\n", c.Word, c.Clean, c.Total, c.MeanCos)
		}
		fmt.Println()
	}
	return nil
}

// runReport composes the whole pipeline offline — scan for risers, vet
// their candidates against real past sentences, order survivors by IC —
// and persists the result for the hook. The corpus is read once at the
// vet window and re-bucketed for the scan windows.
// defaultReportOptions are shared by report (as flag defaults) and refresh
// (verbatim), so the background path can't drift from the documented one.
func defaultReportOptions() pipeline.Options {
	return pipeline.Options{
		RecentDays: 7, BaselineDays: 14,
		Top: 8, MinCount: 10, MinRatio: 2.0,
		MaxUses: 50, MinUses: 5,
		Threshold: 0.97, MinClean: 0.4,
		ChronicTop: 4, MinChronicRate: 0.2, RarityFloor: 10.5,
		PhraseTop: 4, MinPhraseCount: 5,
	}
}

const defaultVetDays = 90

func runReport(args []string) error {
	def := defaultReportOptions()
	fs := flag.NewFlagSet("report", flag.ExitOnError)
	var (
		dir           = fs.String("dir", defaultDir(), "transcript root to scan")
		dataDir       = fs.String("data", defaultDataDir(), "directory holding dict/, wordnet_ic/, vectors/")
		out           = fs.String("out", "", "report path (default: state dir)")
		recentDays    = fs.Int("recent", def.RecentDays, "recent window in days")
		baselineDays  = fs.Int("baseline", def.BaselineDays, "baseline window in days")
		vetDays       = fs.Int("days", defaultVetDays, "how far back to collect uses for vetting")
		top           = fs.Int("top", def.Top, "max risers to consider")
		minCount      = fs.Int("min", def.MinCount, "minimum recent-window count")
		minRatio      = fs.Float64("ratio", def.MinRatio, "minimum recent/baseline rate ratio")
		maxUses       = fs.Int("uses", def.MaxUses, "max sentences to judge against")
		threshold     = fs.Float64("threshold", def.Threshold, "cosine floor for a clean substitution")
		minClean      = fs.Float64("clean", def.MinClean, "minimum clean-substitution fraction for a rung to survive")
		chronicTop    = fs.Int("chronic", def.ChronicTop, "max chronic (steady high-rate) entries; 0 disables")
		chronicRate   = fs.Float64("chronic-rate", def.MinChronicRate, "per-1k full-window rate floor for chronic candidates")
		chronicRarity = fs.Float64("chronic-rarity", def.RarityFloor, "SemCor WordIC floor for the rare-word chronic route")
		phraseTop     = fs.Int("phrases", def.PhraseTop, "max stock-phrase entries from the known-tics reference; 0 disables")
		phraseMin     = fs.Int("phrase-min", def.MinPhraseCount, "minimum full-window occurrences for a phrase to surface")
		useJudge      = fs.Bool("judge", true, "run the term-of-art judge when an API key is configured (default; --judge=false for the deterministic-only report)")
		judgeModel    = fs.String("judge-model", "", "judge model id (default: a cheap haiku)")
	)
	fs.Parse(args)

	def.RecentDays, def.BaselineDays = *recentDays, *baselineDays
	def.Top, def.MinCount, def.MinRatio = *top, *minCount, *minRatio
	def.MaxUses, def.Threshold, def.MinClean = *maxUses, *threshold, *minClean
	def.ChronicTop, def.MinChronicRate, def.RarityFloor = *chronicTop, *chronicRate, *chronicRarity
	def.PhraseTop, def.MinPhraseCount = *phraseTop, *phraseMin

	// The judge is the default: the deterministic-only report is the one that
	// confidently mis-suggests synonyms for terms of art (hook -> snare), the
	// finding that motivated the judge in the first place. It runs whenever a
	// key is configured; without one, fall back to deterministic rather than
	// fail — a keyless clone still works, with the documented rough edges.
	var jdg judge.Judger
	judgeStatus := "off (--judge=false)"
	if *useJudge {
		p, err := report.StateDir()
		if err != nil {
			return err
		}
		if cj, err := judge.New(p, *dataDir, *judgeModel); err == nil {
			jdg = cj
			judgeStatus = "on"
		} else {
			judgeStatus = "off (deterministic fallback)"
			fmt.Fprintf(os.Stderr, "basanite: %v — running deterministic; the term-of-art gate is off\n", err)
		}
	}

	rep, err := buildAndSave(*dir, *dataDir, *out, *vetDays, jdg, def)
	if err != nil {
		return err
	}
	fmt.Printf("report: %d entries (judge %s)\n", len(rep.Entries), judgeStatus)
	if s := rep.Render(); s != "" {
		fmt.Print(s)
	}
	return nil
}

// buildAndSave runs the offline pipeline and persists the report. out ==
// "" means the default state path; jdg nil disables the term-of-art gate.
func buildAndSave(dir, dataDir, out string, vetDays int, jdg judge.Judger, opts pipeline.Options) (*report.Report, error) {
	if out == "" {
		p, err := report.Path()
		if err != nil {
			return nil, err
		}
		out = p
	}
	wn, err := loadWordNet(dataDir)
	if err != nil {
		return nil, err
	}
	opts.ProperNouns = loadProperNouns(dataDir)
	known := knowntics.Load(knownTicsPaths(dataDir)...)
	opts.KnownTics = known.Words
	opts.Phrases = phrase.New(known.Phrases)
	now := time.Now()
	turns, err := corpus.Read(dir, now.AddDate(0, 0, -vetDays))
	if err != nil {
		return nil, err
	}
	rep, err := pipeline.Build(turns, wn, gloveLoader(dataDir), jdg, now, opts)
	if err != nil {
		return nil, err
	}
	if err := rep.Save(out); err != nil {
		return nil, err
	}
	return rep, nil
}

// runRefresh is the SessionStart entry point: regenerate the report in the
// background when it has gone stale, silently and at most one at a time.
// Like the hook, it must never fail loudly — the outcome of each attempt
// is recorded in refresh.log in the state dir for debugging.
func runRefresh(args []string) error {
	fs := flag.NewFlagSet("refresh", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var (
		dir     = fs.String("dir", defaultDir(), "transcript root to scan")
		dataDir = fs.String("data", defaultDataDir(), "directory holding dict/, wordnet_ic/, vectors/")
		maxAge  = fs.Duration("max-age", 6*24*time.Hour, "regenerate when the report is older than this")
	)
	if fs.Parse(args) != nil {
		return nil
	}

	path, err := report.Path()
	if err != nil {
		return nil
	}
	if rep, err := report.Load(path); err == nil && rep != nil && time.Since(rep.GeneratedAt) < *maxAge {
		return nil // fresh enough
	}

	stateDir, err := report.StateDir()
	if err != nil {
		return nil
	}
	// single-flight: several sessions starting together must not stack up
	// minute-long pipeline runs; a lock older than an hour is from a
	// crashed run and may be stolen
	lock := filepath.Join(stateDir, "refresh.lock")
	if fi, err := os.Lstat(lock); err == nil {
		if time.Since(fi.ModTime()) < time.Hour {
			return nil
		}
		os.Remove(lock)
	}
	f, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil
	}
	f.Close()
	defer os.Remove(lock)

	// best-effort gate: refresh runs unattended, so a missing key is not an
	// error here — it just means the report regenerates un-gated.
	var jdg judge.Judger
	if cj, err := judge.New(stateDir, *dataDir, ""); err == nil {
		jdg = cj
	}
	rep, err := buildAndSave(*dir, *dataDir, path, defaultVetDays, jdg, defaultReportOptions())
	status := fmt.Sprintf("%s ok: %d entries\n", time.Now().Format(time.RFC3339), entryCount(rep))
	if err != nil {
		status = fmt.Sprintf("%s error: %v\n", time.Now().Format(time.RFC3339), err)
	}
	os.WriteFile(filepath.Join(stateDir, "refresh.log"), []byte(status), 0o600)
	return nil
}

func entryCount(r *report.Report) int {
	if r == nil {
		return 0
	}
	return len(r.Entries)
}

// runHook is the UserPromptSubmit entry point: read the precomputed
// report, inject its rendering once per session, stay silent otherwise.
// It must never block or fail a prompt — every abnormal case is a silent
// success, and it touches no corpus, WordNet, or vector data.
func runHook(args []string) error {
	// ContinueOnError with discarded output: ExitOnError would os.Exit(2)
	// on a typo'd flag in settings.json, and exit 2 from a UserPromptSubmit
	// hook BLOCKS the prompt — the one failure this entry point must never
	// produce. A misconfigured hook injects nothing; it does not get to
	// take prompts down with it.
	fs := flag.NewFlagSet("hook", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var (
		path   = fs.String("report", "", "report path (default: state dir)")
		maxAge = fs.Duration("max-age", 7*24*time.Hour, "ignore reports older than this")
	)
	if fs.Parse(args) != nil {
		return nil
	}

	var in struct {
		SessionID string `json:"session_id"`
	}
	json.NewDecoder(os.Stdin).Decode(&in) // tolerate absent/odd stdin
	if !validSessionID(in.SessionID) {
		// No or malformed session id: skip entirely. Injecting without a
		// marker would repeat on every prompt, and the id becomes a file
		// name — path separators must never reach filepath.Join.
		return nil
	}

	if *path == "" {
		p, err := report.Path()
		if err != nil {
			return nil
		}
		*path = p
	}
	rep, err := report.Load(*path)
	if err != nil || rep == nil || time.Since(rep.GeneratedAt) > *maxAge {
		return nil
	}
	out := rep.Render()
	if out == "" {
		return nil
	}

	// once per session: a marker file keyed by session id
	dir, err := report.StateDir()
	if err != nil {
		return nil
	}
	// O_EXCL makes create-if-absent atomic: concurrent prompts in a fresh
	// session race here, and exactly one of them gets to inject
	marker := filepath.Join(dir, "injected-"+in.SessionID)
	f, err := os.OpenFile(marker, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil // marker exists (already injected) or dir unwritable: silent
	}
	f.WriteString(time.Now().Format(time.RFC3339))
	f.Close()
	pruneMarkers(dir)
	fmt.Print(out)
	return nil
}

// validSessionID accepts the shapes Claude Code emits (UUID-like) and
// rejects anything that could traverse paths or surprise the marker
// scheme — the id becomes part of a file name.
var validSessionID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`).MatchString

// loadProperNouns reads the suppress-list — known project/tool names that a
// frequency+sense pass mistakes for tics (e.g. the word "calque" when it's a
// project, not the linguistics term). One lemma per line, '#' comments; read
// from proper-nouns.txt in the data dir and ~/.config/basanite. Lemmatized
// and lowercased to match the corpus tokens.
func loadProperNouns(dataDir string) map[string]bool {
	set := map[string]bool{}
	paths := []string{filepath.Join(dataDir, "proper-nouns.txt")}
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, ".config", "basanite", "proper-nouns.txt"))
	}
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			set[text.Lemma(strings.ToLower(line))] = true
		}
		f.Close()
	}
	return set
}

// knownTicsPaths returns the optional user known-tics lists that extend the
// embedded reference: the data dir and ~/.config/basanite, mirroring the
// proper-nouns lookup. Absent files are skipped by knowntics.Load.
func knownTicsPaths(dataDir string) []string {
	paths := []string{filepath.Join(dataDir, "known-tics.txt")}
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, ".config", "basanite", "known-tics.txt"))
	}
	return paths
}

// loadWordNet opens the dict files plus the IC table when present (its
// absence just means ladders order by word frequency).
func loadWordNet(dataDir string) (*wordnet.DB, error) {
	icPath := filepath.Join(dataDir, "wordnet_ic", "ic-semcor.dat")
	if _, err := os.Stat(icPath); err != nil {
		icPath = ""
	}
	db, err := wordnet.Load(filepath.Join(dataDir, "dict"), icPath)
	if err != nil {
		return nil, fmt.Errorf("loading wordnet from %s: %w (see README for data setup)", dataDir, err)
	}
	return db, nil
}

// gloveLoader returns a pipeline.VectorLoader bound to the data dir.
func gloveLoader(dataDir string) pipeline.VectorLoader {
	return func(vocab map[string]bool) (*embed.Table, error) {
		tbl, err := embed.Load(filepath.Join(dataDir, "vectors", "glove.6B.100d.txt"), vocab)
		if err != nil {
			return nil, fmt.Errorf("loading vectors: %w (run scripts/fetch-data.sh)", err)
		}
		return tbl, nil
	}
}

// pruneMarkers drops session markers older than 30 days.
func pruneMarkers(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().AddDate(0, 0, -30)
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "injected-") {
			continue
		}
		if info, err := e.Info(); err == nil && info.ModTime().Before(cutoff) {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// buildVersion resolves the version string: ldflags-baked value, module
// version (go install @tag), VCS revision, then "dev". The git tag is the
// single source of truth — no hand-maintained const.
func buildVersion() string {
	if version != "dev" {
		return version
	}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return version
	}
	if v := bi.Main.Version; v != "" && v != "(devel)" {
		return v
	}
	var rev string
	var dirty bool
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if rev != "" {
		if len(rev) > 12 {
			rev = rev[:12]
		}
		if dirty {
			rev += "-dirty"
		}
		return rev
	}
	return version
}
