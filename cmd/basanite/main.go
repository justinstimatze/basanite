// basanite detects vocabulary tics in Claude Code transcripts: words whose
// recent frequency has risen against the writer's own trailing baseline.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/justinstimatze/basanite/internal/audit"
	"github.com/justinstimatze/basanite/internal/cloze"
	"github.com/justinstimatze/basanite/internal/corpus"
	"github.com/justinstimatze/basanite/internal/detect"
	"github.com/justinstimatze/basanite/internal/display"
	"github.com/justinstimatze/basanite/internal/embed"
	"github.com/justinstimatze/basanite/internal/install"
	"github.com/justinstimatze/basanite/internal/judge"
	"github.com/justinstimatze/basanite/internal/knowntics"
	"github.com/justinstimatze/basanite/internal/phrase"
	"github.com/justinstimatze/basanite/internal/pipeline"
	"github.com/justinstimatze/basanite/internal/report"
	"github.com/justinstimatze/basanite/internal/spark"
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
  refresh         regenerate the state file if stale (runs from both hooks)
  hook            UserPromptSubmit entry: inject the report
  display         MessageDisplay entry: show the demote rung instead of the tic
  install         register the hooks in ~/.claude/settings.json (-status, -uninstall)
  ledger          flagged tics over time — is a tic's rate falling?
                  (-swaps: what got replaced on screen; -verdicts: judge churn)
  audit           which curated known-tics entries have ever fired?
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
	case "audit":
		err = runAudit(args)
	case "install":
		err = runInstall(args)
	case "display":
		err = runDisplay(args)
	case "ledger":
		err = runLedger(args)
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
	if err := w.Flush(); err != nil {
		return err
	}

	// Per-lemma sparkline summary: the weekly table read left-to-right as
	// shape, with the endpoint rates and a direction arrow. A bucket with no
	// tokens is a gap (NaN), so dead weeks read as holes, not zeros.
	sw := tabwriter.NewWriter(os.Stdout, 2, 8, 2, ' ', 0)
	fmt.Fprintln(sw)
	for i, l := range lemmas {
		series := make([]float64, *weeks)
		for b := 0; b < *weeks; b++ {
			if totals[b] == 0 {
				series[b] = math.NaN()
				continue
			}
			series[b] = float64(counts[b][i]) / float64(totals[b]) * 1000
		}
		first, last := firstLastPresent(series)
		fmt.Fprintf(sw, "%s\t%s\t%s\t%.2f → %.2f/1k\n", l, spark.Line(series), spark.Trend(series), first, last)
	}
	return sw.Flush()
}

// firstLastPresent returns the first and last non-NaN values of a series,
// for the sparkline's numeric endpoints; 0,0 when the series is all gaps.
func firstLastPresent(vals []float64) (first, last float64) {
	have := false
	for _, v := range vals {
		if math.IsNaN(v) {
			continue
		}
		if !have {
			first, have = v, true
		}
		last = v
	}
	return first, last
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
		ChronicTop: 4, MarkedTop: 6, MinChronicRate: 0.2, RarityFloor: 10.5,
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
		markedTop     = fs.Int("marked", def.MarkedTop, "max marked (concrete figurative tic, e.g. load-bearing) entries; 0 disables")
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
	def.MarkedTop = *markedTop

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

	known, seeded := applyKnownTics(&def)

	rep, err := buildAndSave(*dir, *dataDir, *out, *vetDays, jdg, def)
	if err != nil {
		return err
	}
	fmt.Printf("report: %d entries (judge %s)\n", len(rep.Entries), judgeStatus)
	if s := rep.Render(true); s != "" { // console view: show the trajectory sparklines
		fmt.Print(s)
	}
	// Always surface where the editable list lives and how big it is, so the
	// user can curate it regardless of whether a background refresh seeded it
	// first (the first-run announcement alone would miss that case).
	if p := knownTicsPath(); p != "" {
		verb := "edit"
		if seeded {
			verb = "seeded; edit"
		}
		fmt.Printf("known-tics: %d words, %d phrases — %s %s to curate\n",
			len(known.Words), len(known.Phrases), verb, p)
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
	now := time.Now()
	turns, err := corpus.Read(dir, now.AddDate(0, 0, -vetDays))
	if err != nil {
		return nil, err
	}
	rep, err := pipeline.Build(turns, wn, gloveLoader(dataDir), jdg, now, opts)
	if err != nil {
		return nil, err
	}
	rep.Version, rep.ListModified = buildVersion(), listModTime()
	if err := rep.Save(out); err != nil {
		return nil, err
	}
	recordLedger(rep, filepath.Dir(out), now)
	return rep, nil
}

const (
	// defaultReportMaxAge is when a report goes stale on the clock alone.
	defaultReportMaxAge = 6 * 24 * time.Hour
	// refreshLogName holds the outcome of the last refresh attempt. Its mtime
	// doubles as the attempt clock spawnRefresh backs off against.
	refreshLogName = "refresh.log"
	// minRefreshInterval bounds how often an input change may trigger a
	// rebuild. The clock rule is self-limiting; the version and list rules are
	// not, and two binaries alternating at one path would otherwise rebuild on
	// every prompt.
	minRefreshInterval = 15 * time.Minute
)

// listModTime is the curated list's last edit, or the zero time when there is
// no list to read — in which case it is not evidence of anything and the
// staleness check ignores it.
// It is a var so a test can state a list mtime without depending on whether
// the machine running the test happens to have a list — the condition that
// made the first version of this pass locally and fail on a clean checkout.
var listModTime = func() time.Time {
	fi, err := os.Stat(knownTicsPath())
	if err != nil {
		return time.Time{}
	}
	return fi.ModTime()
}

// staleReason says why a report needs regenerating, or "" if it does not. It
// is worded for refresh.log, which is the only place anyone reads it.
//
// Age is the weakest of the three rules and was for a while the only one: a
// report can be minutes old and still describe a list you have since edited or
// a pipeline two versions back, and neither shows up in its timestamp.
func staleReason(rep *report.Report, maxAge time.Duration) string {
	if rep == nil {
		return "no report yet"
	}
	age := time.Since(rep.GeneratedAt)
	if age >= maxAge {
		return fmt.Sprintf("older than %s", maxAge)
	}
	if age < minRefreshInterval {
		return "" // just built: give the input rules room to settle
	}
	if v := buildVersion(); rep.Version != v {
		was := rep.Version
		if was == "" {
			was = "an unstamped build"
		}
		return fmt.Sprintf("built by %s, running %s", was, v)
	}
	if m := listModTime(); !m.IsZero() && !m.Equal(rep.ListModified) {
		return "known-tics list edited since"
	}
	return ""
}

// spawnRefresh starts a detached `basanite refresh` and returns at once.
//
// The caller is a UserPromptSubmit hook whose stdout is the injection itself,
// so the child must never inherit it and the prompt must never wait on it: the
// pipeline takes minutes. Report.Save renames a temp file into place, so a
// child killed partway leaves the previous report whole, and the refresh lock
// is stealable after an hour if one dies holding it.
//
// The backoff is on attempts, not outcomes, and it is what makes checking
// every prompt safe. A refresh that fails leaves the report exactly as stale
// as it found it, so a broken pipeline would otherwise start a fresh attempt
// on every prompt for as long as it stayed broken — the report's own timestamp
// cannot express "tried and could not". refresh.log is written on success and
// failure alike, so its mtime is the attempt clock. Only this path backs off:
// `basanite refresh` run by hand still runs.
func spawnRefresh(stateDir string) {
	if refreshBackedOff(stateDir) {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	cmd := exec.Command(exe, "refresh")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	if cmd.Start() == nil {
		_ = cmd.Process.Release()
	}
}

// recordLedger folds this refresh into the persisted before/after ledger,
// beside the report. Best-effort: a ledger failure must never fail the
// build — the ledger is a reassurance record, not part of the turn-start
// loop, so a corrupt or unwritable ledger is dropped silently rather than
// blocking a report the hook depends on.
func recordLedger(rep *report.Report, dir string, now time.Time) {
	path := filepath.Join(dir, report.LedgerName)
	l, err := report.LoadLedger(path)
	if err != nil {
		return
	}
	l.Update(rep, now)
	_ = l.Save(path)
}

// refreshBackedOff reports whether an attempt was made too recently to make
// another one. refresh.log is written on success and on failure alike, so its
// mtime is the attempt clock — which is the one the automatic path needs, since
// a failed attempt moves no other timestamp.
func refreshBackedOff(stateDir string) bool {
	fi, err := os.Stat(filepath.Join(stateDir, refreshLogName))
	return err == nil && time.Since(fi.ModTime()) < minRefreshInterval
}

// runRefresh regenerates the report when it has gone stale, silently and at
// most one at a time. It is the SessionStart entry point and is also what
// spawnRefresh starts from the prompt hook, because SessionStart alone fires
// once per session and a session can run for weeks.
//
// Like the hook, it must never fail loudly — the outcome of each attempt is
// recorded in refresh.log in the state dir, which doubles as the clock the
// automatic path backs off against.
func runRefresh(args []string) error {
	fs := flag.NewFlagSet("refresh", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var (
		dir     = fs.String("dir", defaultDir(), "transcript root to scan")
		dataDir = fs.String("data", defaultDataDir(), "directory holding dict/, wordnet_ic/, vectors/")
		maxAge  = fs.Duration("max-age", defaultReportMaxAge, "regenerate when the report is older than this")
	)
	if fs.Parse(args) != nil {
		return nil
	}

	path, err := report.Path()
	if err != nil {
		return nil
	}
	why := "no report yet"
	if rep, err := report.Load(path); err == nil {
		if why = staleReason(rep, *maxAge); why == "" {
			return nil // fresh enough
		}
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
	opts := defaultReportOptions()
	applyKnownTics(&opts)
	rep, err := buildAndSave(*dir, *dataDir, path, defaultVetDays, jdg, opts)
	status := fmt.Sprintf("%s ok: %d entries (%s)\n", time.Now().Format(time.RFC3339), entryCount(rep), why)
	if err != nil {
		status = fmt.Sprintf("%s error: %v (%s)\n", time.Now().Format(time.RFC3339), err, why)
	}
	os.WriteFile(filepath.Join(stateDir, refreshLogName), []byte(status), 0o600)
	return nil
}

// runAudit counts every curated known-tics entry against the corpus and says
// whether it is reported, matching but ranked out, or dead.
//
// The list cannot answer this about itself: an entry that never matches looks
// exactly like one that matches constantly, and "the ranking is working" looks
// exactly like "the pattern is broken". Both are answered by counting.
func runAudit(args []string) error {
	fs := flag.NewFlagSet("audit", flag.ExitOnError)
	var (
		dir       = fs.String("dir", defaultDir(), "transcript root to scan")
		days      = fs.Int("days", defaultVetDays, "how far back to count")
		path      = fs.String("report", "", "report path (default: state dir)")
		list      = fs.String("list", knownTicsPath(), "known-tics list to audit")
		onlyNever = fs.Bool("never", false, "list only the entries that have never matched")
	)
	fs.Parse(args)

	// The list path is explicit and printed. knowntics.Load falls back to the
	// embedded seed when it cannot read the file, which for every other caller
	// is the right graceful default and for this one is a silent lie: an audit
	// that reports on a list it never opened is the failure it exists to catch.
	known, _ := knowntics.Load(*list)
	if known == nil || (len(known.Words) == 0 && len(known.Phrases) == 0) {
		return fmt.Errorf("no known-tics list found; run `basanite report` once to seed it")
	}
	if *path == "" {
		p, err := report.Path()
		if err != nil {
			return err
		}
		*path = p
	}
	rep, err := report.Load(*path) // absent is fine: everything is then unreported
	if err != nil {
		return err
	}
	turns, err := corpus.Read(*dir, time.Now().AddDate(0, 0, -*days))
	if err != nil {
		return err
	}
	// The judge's standing verdicts, so an entry the gate suppressed reads as
	// suppressed rather than as merely ranked out. Best-effort: an unreadable
	// log costs one status, never the audit.
	judged := map[string]string{}
	if dir, err := report.StateDir(); err == nil {
		if st, err := judge.LoadStore(filepath.Join(dir, "verdicts.jsonl")); err == nil {
			for w := range known.Words {
				if r, ok := st.Latest(w); ok {
					judged[w] = r.Role
				}
			}
		}
	}

	fmt.Printf("list: %s\n", *list)
	fmt.Print(audit.Run(known, rep, turns, *days, judged).Render(*onlyNever))
	return nil
}

// runInstall registers the three hooks in the user's Claude Code settings.
// The binary knows its own absolute path, which is the part the README could
// only ever write as "/home/you/go/bin/basanite", pasted into nested JSON
// three times.
func runInstall(args []string) error {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	var (
		path      = fs.String("settings", "", "settings file (default: ~/.claude/settings.json)")
		dryRun    = fs.Bool("dry-run", false, "print what would change and write nothing")
		uninstall = fs.Bool("uninstall", false, "remove basanite's hooks")
		status    = fs.Bool("status", false, "show what is registered right now")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *path == "" {
		p, err := install.DefaultPath()
		if err != nil {
			return err
		}
		*path = p
	}
	settings, err := install.Load(*path)
	if err != nil {
		return err
	}

	// A tracker is a claim about a past intention; only the live file says
	// whether the hooks are actually on.
	if *status {
		fmt.Print(install.RenderStatus(settings.Registered(), *path))
		return nil
	}

	bin, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(bin); err == nil {
		bin = resolved // hooks run from an arbitrary cwd and outlive a symlink
	}

	var changes []install.Change
	if *uninstall {
		changes = settings.Remove()
	} else {
		changes = settings.Apply(bin)
	}
	fmt.Print(install.Render(changes, bin))

	if *dryRun {
		fmt.Printf("\ndry run: %s not written\n", *path)
		return nil
	}
	if install.Settled(changes) {
		fmt.Printf("\nalready registered: %s unchanged\n", *path)
		return nil
	}
	backup, err := settings.Save(*path)
	if err != nil {
		return err
	}
	fmt.Printf("\nwrote %s", *path)
	if backup != "" {
		fmt.Printf(" (backup: %s)", backup)
	}
	fmt.Println("\nHooks load at startup — open a new session for this to take effect.")
	return nil
}

// runDisplay is the MessageDisplay hook: it renders the vetted demote rung in
// place of a flagged tic in the text streaming to the terminal. Display-only —
// the transcript and the model's context keep the original word, so this
// changes nothing about the writing and nothing about what basanite measures.
// It is the "I don't want to read it" surface, not a second injection.
//
// Like runHook it never fails: Claude Code holds each batch of lines until this
// returns and shows the original text on any error, so every abnormal case
// exits silently rather than stalling or garbling the terminal.
func runDisplay(args []string) error {
	fs := flag.NewFlagSet("display", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var (
		path   = fs.String("report", "", "report path (default: state dir)")
		all    = fs.Bool("all", false, "swap every judged entry, not just curated known-tics")
		extra  = fs.String("words", "", "explicit word:replacement pairs, comma-separated")
		maxAge = fs.Duration("max-age", 7*24*time.Hour, "ignore reports older than this")
		noLog  = fs.Bool("no-log", false, "don't record swaps to the swap ledger")
	)
	if fs.Parse(args) != nil {
		return nil
	}

	var in struct {
		SessionID string `json:"session_id"`
		MessageID string `json:"message_id"`
		Delta     string `json:"delta"`
	}
	if json.NewDecoder(os.Stdin).Decode(&in) != nil || in.Delta == "" {
		return nil // nothing to render: leave the original alone
	}

	if *path == "" {
		p, err := report.Path()
		if err != nil {
			return nil
		}
		*path = p
	}
	swaps := display.Swaps{}
	// A stale report is still a fine swap table — the words on it were tics
	// last week and the rungs are still vetted. max-age only refuses one old
	// enough to predate the curation entirely.
	if rep, err := report.Load(*path); err == nil && rep != nil && time.Since(rep.GeneratedAt) <= *maxAge {
		swaps = display.FromReport(rep, *all)
	}
	if *extra != "" {
		swaps.Add(strings.Split(*extra, ","))
	}
	if len(swaps) == 0 {
		return nil
	}

	st := loadDisplayState(in.SessionID, in.MessageID)
	out, st, counts := swaps.Apply(in.Delta, st)
	saveDisplayState(in.SessionID, st)
	// The transcript keeps the original word, so this log is the only record
	// that the swap happened — and the only count that tracks what was read
	// rather than what was written.
	if !*noLog {
		display.AppendLog(displayLogPath(), counts, swaps, time.Now())
	}

	var resp struct {
		HookSpecificOutput struct {
			HookEventName  string `json:"hookEventName"`
			DisplayContent string `json:"displayContent"`
		} `json:"hookSpecificOutput"`
	}
	resp.HookSpecificOutput.HookEventName = "MessageDisplay"
	resp.HookSpecificOutput.DisplayContent = out
	json.NewEncoder(os.Stdout).Encode(&resp)
	return nil
}

// displayStatePrefix names the per-session fence state carried between the
// batches of one message.
//
// Per session, not one shared file: concurrent sessions interleave batches, so
// a single file lets one session's message id evict another's state. The
// evicted session then resumes mid-code-block believing it is in prose and
// swaps inside the fence — the one outcome the fence tracking exists to
// prevent. Same shape as the injected- markers, and pruned the same way.
const displayStatePrefix = "display-"

func displayStatePath(sessionID string) string {
	if !validSessionID(sessionID) {
		return "" // no id: run stateless rather than share a file with everyone
	}
	return stateFile(displayStatePrefix + sessionID + ".json")
}

// displayLogPath is the swap ledger, beside the report and the tic ledger.
func displayLogPath() string { return stateFile(display.LogName) }

func stateFile(name string) string {
	dir, err := report.StateDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, name)
}

func loadDisplayState(sessionID, messageID string) display.State {
	fresh := display.State{MessageID: messageID}
	p := displayStatePath(sessionID)
	if p == "" || messageID == "" {
		return fresh
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return fresh
	}
	var st display.State
	if json.Unmarshal(b, &st) != nil || st.MessageID != messageID {
		return fresh // a new message always starts outside a fence
	}
	return st
}

func saveDisplayState(sessionID string, st display.State) {
	p := displayStatePath(sessionID)
	if p == "" {
		return
	}
	if b, err := json.Marshal(st); err == nil {
		os.WriteFile(p, b, 0o600)
	}
}

// runLedger prints the persisted before/after record of every flagged tic:
// when it was first flagged, its rate then and now, and whether it has faded
// out. This is the "is basanite working?" surface — recorded rate-over-time
// instead of eyeballing a single trend chart.
func runLedger(args []string) error {
	fs := flag.NewFlagSet("ledger", flag.ContinueOnError)
	path := fs.String("ledger", "", "ledger file (default: state dir)")
	showSwaps := fs.Bool("swaps", false, "instead show what the display hook replaced on screen")
	showVerdicts := fs.Bool("verdicts", false, "instead show whether the judge gives a word the same answer twice")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *showVerdicts {
		// A third view for the same reason as -swaps: this counts what the
		// gate decided, which is neither what was written nor what was read.
		dir, err := report.StateDir()
		if err != nil {
			return err
		}
		st, err := judge.LoadStore(filepath.Join(dir, "verdicts.jsonl"))
		if err != nil {
			return err
		}
		fmt.Print(judge.RenderChurn(st.Records()))
		return nil
	}
	if *showSwaps {
		// Deliberately a separate view, not another section: this counts what
		// was read, the tic ledger counts what was written, and merging them
		// would invite reading one number as the other.
		swaps, err := display.LoadLog(displayLogPath())
		if err != nil {
			return err
		}
		fmt.Print(display.RenderLog(swaps, time.Now()))
		return nil
	}
	p := *path
	if p == "" {
		lp, err := report.LedgerPath()
		if err != nil {
			return err
		}
		p = lp
	}
	l, err := report.LoadLedger(p)
	if err != nil {
		return err
	}
	fmt.Print(l.Render(time.Now()))
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
		path       = fs.String("report", "", "report path (default: state dir)")
		maxAge     = fs.Duration("max-age", 7*24*time.Hour, "ignore reports older than this")
		topWords   = fs.Int("top-words", 5, "max word entries in the injection (0 = all)")
		topPhrases = fs.Int("top-phrases", 2, "max phrase entries in the injection (0 = all)")
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
	dir, err := report.StateDir()
	if err != nil {
		return nil
	}
	rep, err := report.Load(*path)
	if err != nil {
		return nil
	}
	// Staleness is checked here, on every prompt, and not only at SessionStart.
	// A session that runs for days evaluates a SessionStart rule exactly once —
	// at the moment the report is newest — so the interval it enforces is not
	// six days but however long the session stays open. The check is a stat and
	// two comparisons; the rebuild it may start is detached and the current
	// prompt is served from the report already in hand.
	if rep != nil && staleReason(rep, defaultReportMaxAge) != "" {
		spawnRefresh(dir)
	}
	var out string
	var shown []report.Entry
	switch {
	case rep == nil:
		// No report at all: basanite isn't set up here. Stay silent rather
		// than nag someone who never opted in.
		return nil
	case time.Since(rep.GeneratedAt) > *maxAge:
		// The report exists but has gone stale — the SessionStart refresh
		// has been failing. Surface a breadcrumb instead of silently serving
		// nothing, so a broken pipeline can't rot invisibly for weeks.
		out = staleNote(rep.GeneratedAt, dir)
	default:
		shown = rep.HookEntries(*topWords, *topPhrases)
		out = rep.RenderHook(*topWords, *topPhrases)
	}
	if out == "" {
		return nil
	}

	// Once per session, then again only after reinjectInterval: a long or
	// compacted session drops the turn-start block it was handed, so let the
	// awareness resurface rather than staying dark for the rest of it.
	marker := filepath.Join(dir, "injected-"+in.SessionID)
	if !claimInjection(marker, reinjectInterval) {
		return nil // injected recently in this session: silent
	}
	pruneMarkers(dir)
	recordInjection(dir, shown)
	fmt.Print(out)
	return nil
}

// recordInjection notes which lemmas actually reached this prompt. It runs
// only past claimInjection, so it counts injections rather than hook calls.
//
// Best-effort in the same sense as recordLedger: every error path is a silent
// return. A counter is not worth failing a prompt over, and this entry point
// must never do that.
func recordInjection(dir string, shown []report.Entry) {
	if len(shown) == 0 {
		return
	}
	lemmas := make([]string, 0, len(shown))
	for _, e := range shown {
		lemmas = append(lemmas, e.Lemma)
	}
	path := filepath.Join(dir, report.LedgerName)
	l, err := report.LoadLedger(path)
	if err != nil {
		return
	}
	l.RecordInjection(lemmas, time.Now())
	_ = l.Save(path)
}

// reinjectInterval bounds how often a single session re-injects. The marker
// used to be permanent (inject exactly once, ever), but a long or compacted
// session loses the turn-start context it injected — so after this long the
// awareness is allowed to surface again.
const reinjectInterval = 4 * time.Hour

// claimInjection reports whether this call should inject and, on a yes,
// stamps the marker's clock. True when the marker is absent (the session's
// first prompt) or older than interval (an earlier injection has aged out of
// a long session). O_EXCL keeps concurrent first-prompts to a single
// injector; the aged-out path tolerates a rare double-inject (one extra
// block after hours) rather than carry a lock.
func claimInjection(marker string, interval time.Duration) bool {
	f, err := os.OpenFile(marker, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err == nil {
		f.WriteString(time.Now().Format(time.RFC3339))
		f.Close()
		return true
	}
	if !os.IsExist(err) {
		return false // dir unwritable
	}
	fi, err := os.Stat(marker)
	if err != nil || time.Since(fi.ModTime()) < interval {
		return false
	}
	now := time.Now()
	return os.Chtimes(marker, now, now) == nil
}

// staleNote is the breadcrumb injected when the report has aged past
// max-age: the SessionStart refresh has been failing and serving nothing
// hides that. It names the age and the last refresh error so the rot is
// visible instead of silent.
func staleNote(generatedAt time.Time, stateDir string) string {
	days := int(time.Since(generatedAt).Hours() / 24)
	msg := fmt.Sprintf("basanite: word-tic state is %dd stale — the background refresh isn't updating it; run `basanite refresh` or check its data setup.", days)
	if e := lastRefreshError(stateDir); e != "" {
		msg += " Last refresh error: " + e
	}
	return msg + "\n"
}

// lastRefreshError returns the message from the most recent error line in
// refresh.log, or "" when the log is absent or clean.
func lastRefreshError(stateDir string) string {
	b, err := os.ReadFile(filepath.Join(stateDir, refreshLogName))
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if idx := strings.Index(lines[i], "error:"); idx >= 0 {
			return strings.TrimSpace(lines[i][idx:])
		}
	}
	return ""
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

// knownTicsPath is the user-owned known-tics list: ~/.config/basanite/known-tics.txt,
// seeded from the embedded starter on first run and the user's to curate after.
// "" when there is no home dir, in which case knowntics.Load uses the seed
// in memory.
func knownTicsPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "basanite", "known-tics.txt")
}

// applyKnownTics loads the user-owned known-tics list (seeding it on first
// run) and sets the detector inputs on opts. It returns the parsed set and
// whether the file was just seeded, so an interactive caller can report where
// the list now lives. Both report and refresh call this, so the background
// path can't drift from the documented one.
func applyKnownTics(opts *pipeline.Options) (*knowntics.Set, bool) {
	known, seeded := knowntics.Load(knownTicsPath())
	opts.KnownTics = known.Words
	opts.Phrases = phrase.New(known.Phrases)
	return known, seeded
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
		if !strings.HasPrefix(e.Name(), "injected-") && !strings.HasPrefix(e.Name(), displayStatePrefix) {
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
