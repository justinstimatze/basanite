# Changelog

## Unreleased — which entries have ever fired?

A curated list cannot answer the question that decides whether it is worth
curating. An entry that never matches looks, from the list's own point of
view, exactly like one that matches constantly. And when a word you know you
overuse never reaches a report, "the ranking is working as designed" and "the
pattern is broken" are indistinguishable from outside — which is the shape of
the `load-bearing` complaint that turned out to be a budget bug two releases
ago.

- **`basanite audit`** counts every curated entry against the corpus and buckets
  it: reported (with its rank), matching but below the cutoff, or `NEVER FIRES`.
  `-never` narrows to the rows worth cutting, `-days` sets the window,
  `-list` audits a file other than your own.
- Phrases count over the surface word stream and words over the lemmatized one,
  since that is the stream each is written to be found in — auditing a
  stopword-heavy phrase against the tokenized stream would call it dead for the
  wrong reason.
- It prints the list path it read. The first run of this command reported
  confidently on 19 entries when the list held 28: it had been handed an empty
  path, and `knowntics.Load` falls back to the embedded seed rather than
  failing, which is the right graceful default everywhere except here. An audit
  that reports on a list it never opened is the exact failure it exists to
  catch, so the source is now explicit and printed.

## v0.6.0 (2026-08-02) — not having to read it

Two ways a detected tic still reached the page. One was a budget that spent
every slot on the wrong lane, so the curated chronic entries were never
injected at all. The other is that even a landed injection only changes the
*next* turn, and sometimes not that — nothing was doing anything about the
word already on screen. This release closes both, plus an `install` that
registers the hooks itself.

### Not having to read it

The injection tries to change what gets written, which takes a turn to land and
does not always land. `basanite display` is the other half of the want: a
`MessageDisplay` hook that renders the vetted demote rung in place of the tic
as the message streams. You read `supporting`; the model wrote `load-bearing`.

- **Display-only, by design of the event.** The transcript and the model's
  context keep the original, so this changes nothing about the writing — and
  `report`, `trend` and `ledger` all read the transcripts, so the measurement
  stays honest whatever the screen shows. Relief, not intervention.
- **The rung comes from `report.json`**, so the swap table maintains itself.
  Default is curated known-tics only: a ladder is vetted for average
  substitutability, not per-occurrence correctness, and the live report demotes
  `turn` to `change` and `five` to `figure` — fine as awareness, ruinous as a
  display rule ("it is your change to indicate figure things"). `-all` opts
  into the rest; `-words a:b` overrides.
- **Code is never rewritten**: inline backticks, paths, URLs and fenced blocks,
  the last tracked across streamed batches since a fence opens in one and
  closes in another. Runs in ~4 ms, which matters because Claude Code holds
  each batch of lines until the hook returns.
- **`ledger -swaps`**: every replacement is appended to `swaps.jsonl`, because
  the transcript keeps the original and otherwise nothing would record that the
  swap happened. It counts what you were spared, which is deliberately not what
  `trend` and `ledger` count — those read the transcripts and report what was
  written, and the gap between the two numbers is the feature working.
  Append-only JSONL so concurrent sessions can't interleave mid-line, and one
  torn line costs a record rather than the history. `-no-log` opts out.
- **`basanite install`** registers all three hooks from the running binary,
  which is the one thing that knows its own absolute path — the README could
  only ever write it as `/home/you/go/bin/basanite` and ask you to paste it
  into nested JSON three times. Backs the settings file up, preserves every
  key and foreign hook it doesn't own, and repoints an existing registration
  instead of stacking a second one, so re-running after `go install` to a new
  location is the fix rather than the problem. `-dry-run`, `-status`,
  `-uninstall`.
- **Fence state is per session.** Found in review, and live for about an hour:
  concurrent sessions interleave batches through one binary, and a single
  state file keyed on message id let one session evict another's. The evicted
  session resumed mid-code-block believing it was in prose and swapped inside
  the fence — the one outcome the tracking exists to prevent. Reproduced (`var
  substrate` came back as `var component`), then keyed by session id and
  pruned by the sweep that already handles the injection markers. A batch with
  no valid session id runs stateless rather than sharing a file with everyone.
  Relatedly, `install` no longer writes when nothing changed: it backed up on
  every run, so a second run overwrote the backup with the already-modified
  file and the pre-basanite original was gone.

### The budget was eating the chronic lane

The v0.5.0 cap fixed a wall of eighteen entries and introduced a quieter
problem: `RenderHook` filled the word budget in report order, and report order
is risers first. Five slots, five risers, every time.

So the chronic lane — the one built for words that never rise because they have
been at the same rate since the model shipped — was detected, scored, curated
and then discarded at the last step. `load-bearing` is the first entry in the
shipped known-tics seed and is in the live list. It sat at position 13 of 24 in
the report as `kind=chronic, known=true`, at 0.55 per 1k, roughly sixty
occurrences a day across 23.4M characters of assistant prose. It had never once
been injected. The detector was right the whole time and nothing downstream let
it speak.

- **The word budget is split between the lanes**, half each with chronic
  rounding up: at the shipped cap of five that is three chronic and two risers.
  The lane that has gone unheard wins the odd slot.
- **Curated known-tics go first within the chronic share.** A riser is an
  observation that a habit may be forming, and it ages out on its own. A known
  tic is the writer having said in advance that they never want to see the word.
  A standing instruction should not lose its slot to a passing one.
- **Either lane's unused share spills to the other**, so a quiet week for one
  still fills the budget rather than shrinking the injection.

Three regression tests, including the one that names the original failure: a
curated known tic behind eight risers must still reach the injection.

- **The budget now counts only what renders.** `Render` drops an entry whose
  ladder leaves no rung below the lemma, so a slot spent on one shrank the
  injection to four words with nothing backfilling it — the same shape as the
  lane bug, one layer down. Both paths go through a single `renderable` test.
  Latent rather than firing: it needs a lemma weaker than every candidate it
  gathered, which the live report has never produced.

Docs caught up with two releases of drift: the `ledger` command was missing
from the README command list, the marked route had shipped undocumented, the
injection budget was unmentioned, and "no state accumulates" had been false
since the ledger landed.

## v0.5.0 (2026-07-28) — the injection earns a budget

The July 2026 report carried 18 entries and 4,613 chars of judge notes into
every injection — a wall the model skims, not awareness it holds. Measured
against Opus 5, which Anthropic's own docs say responds to fewer, clearer
directives, the full roster is overconstraint.

- **`RenderHook(maxWords, maxPhrases)`**: the turn-start view now renders
  the strongest 5 word entries and 2 phrase entries (`hook -top-words` /
  `-top-phrases`; 0 = uncapped). The console `report` view still shows
  everything — the cap is about what a model mid-task can act on, not about
  hiding data.
- **Judge notes cut to their first sentence at render time**: the judge
  prompt has always asked for "one short clause" and the judge ignores it —
  notes ran 239–519 chars, with sentences two onward restating the ladder
  the line already shows. Deterministic truncation enforces the contract
  the prompt could not. Live effect: 6,488 → 1,769 chars per injection.

## v0.4.2 (2026-07-14) — the effectiveness ledger

`trend` could always show a tic's rate falling, but nothing wrote it down —
"is basanite working?" meant eyeballing a chart and trusting memory. The
ledger records the before/after so the answer is a lookup, not a vibe.

- **`ledger` command + `internal/report` `Ledger`**: a persisted
  (`ledger.json`, beside the report) map of every flagged lemma to its
  cross-refresh history — when it was first flagged, its rate then and now,
  and whether it has since faded out. `basanite ledger` renders it
  still-flagged-first (longest-standing tics on top), then faded-out, each
  with the rate delta and a ↓/↑/→ arrow.
- **Updated automatically**: `buildAndSave` folds each report into the ledger
  as a best-effort side effect, so it accrues through the existing
  SessionStart `refresh` hook — no new thing to remember to run. A ledger
  failure never fails the build; it's a reassurance record, not part of the
  turn-start loop.
- The stamp is a *true first*: a faded tic that reappears keeps its original
  `first_flagged`, so "since" always means the first sighting. It's a
  recorded before/after, not a causality proof — direct callouts and topic
  drift stay unmeasured confounds, and the render says so.

## v0.4.1 (2026-07-07) — the mirror can't go dark silently

Three fixes to the turn-start loop after finding it had silently stopped
injecting for weeks: the SessionStart `refresh` was failing every run
(`defaultDataDir` only resolves the relative `data/` when cwd is the repo
root, but the hook runs from an arbitrary session cwd), the hook fail-closes
on a report past `max-age`, and so a stale report served nothing with no
signal that anything was wrong.

- **Visible-stale breadcrumb**: when the report has aged past `max-age`, the
  hook now injects a one-line note (age + the last `refresh.log` error)
  instead of silently injecting nothing. A missing report still stays silent
  — that's "not set up", not "broken". Converts a silent multi-week rot into
  a visible one.
- **Re-inject in long sessions**: the once-per-session marker was permanent,
  so a long or compacted session lost the turn-start block it was handed and
  never got it back. The marker is now time-boxed (`reinjectInterval`, 4h):
  awareness resurfaces rather than staying dark for the rest of a session.
- **Data setup**: the SessionStart `refresh` (already an `async` hook) only
  self-maintains if it can find its data from any cwd. Install the assets
  where the hook looks (`~/.local/share/basanite`, e.g. a symlink to the
  repo `data/`) so regeneration runs unattended.

## v0.4.0 (2026-06-16) — the known-tics reference, the marked route, sparklines

Three surfaces the derived deterministic signals couldn't reach: a curated
reference of common Claude leans, a route for live-metaphor tics the
frequency/rarity ranking buries, and at-a-glance trend sparklines.

### The known-tics reference (Claude Bingo)

A curated reference of words and phrases Claude is known to lean on, shipped
embedded in the binary as a conservative, high-precision **sample of the
globally common ones** — the assistant-register staples that recur in Claude
Code transcripts (`you're absolutely right`, `worth noting`, `that said`)
plus a few iconic signatures seeded from the community "Claude Bingo" card.
It complements the derived deterministic signals with crowd-sourced ground
truth, and it stays a reference, not a denylist — a seeded entry still has
to be one you're actually leaning on now before it surfaces, and the output
stays awareness, never prohibition. Niche or personal leans go in a local
`known-tics.txt`.

- `internal/knowntics`: the reference is a single **user-owned** list. The
  embedded content is a *starter seed* — on first run it's written to
  `~/.config/basanite/known-tics.txt`, and from then on only that file is
  read. It's yours to curate: entries accrete and fall out over time (a
  model's tells age out), and nothing upstream re-merges what you deleted.
  Single-word lines feed the chronic detector; spaced lines are phrases.
- **Known-tics route**: a third chronic admission route. The rarity route
  catches words rare in general English (`substrate`); the known route
  catches *common*-English leans it structurally can't see (`surface`,
  `frame`, `honor`) when they're steady and dispersed. Entries are labelled
  "a common Claude lean".
- **Phrase track** (`internal/phrase`): the single-token detector is blind to
  stock phrases (`i want to honor that`) — the words are individually
  unremarkable; the tic is the sequence. A matcher counts the curated phrases
  over the surface word stream (stopwords kept) and surfaces the most-used as
  awareness-only entries (no synonym ladder for a stock phrase). `report`
  gains `--phrases` / `--phrase-min`.

### The marked route — live-metaphor tics

Frequency-drift detection structurally misses low-frequency, high-salience
figurative tics like `load-bearing`: its rate is modest, and every cheap
statistic that might rank it (project spread, rarity, concreteness) is
shared by ordinary jargon — the rarity route admits it as a candidate, but
its modest rate keeps it out of the slots. The separating signal is
**context-incongruity** — the cosine distance between a word's literal sense
(its GloVe vector) and the centroid of the contexts it actually appears in.
A live metaphor (`load-bearing`, ~1.0) is a physical word recurring in
non-physical contexts; literal jargon (`running` 0.34, `slot` 0.60, `hook`
0.57) stays in its home neighborhood and sinks.

- `internal/pipeline`: a fourth **marked route** (after known, frame, rare)
  gathers dispersed, rare-in-English words, ranks them by incongruity, gates
  at a floor, and hands survivors to the existing judge — the only thing that
  separates a live metaphor (`load-bearing → tic`) from a noisy-vector term
  of art (`grep`, `config → suppressed`). `MarkedTop` budgets the entries;
  `-marked` exposes it. Model-name confounds (`haiku → poem`) are dropped by
  the existing proper-noun guard.

### Sparklines

- `internal/spark`: stdlib-only Unicode block sparklines (eight runes, no
  dependency), with NaN gaps and a `↑/↓/→` direction arrow.
- `trend` prints a per-lemma sparkline summary under the weekly table.
- `report` records a trailing 8-week per-1k series on each entry and renders
  an inline sparkline in the console view; the turn-start injection stays
  compact (`Render(showSpark bool)`).

## v0.3.1 (2026-06-10)

- The judge is now **on by default** when an API key is configured. The
  deterministic-only report is the one that confidently mis-suggests
  synonyms for terms of art (`hook → snare`) — the session's central
  finding — so gating is the default experience, not an opt-in. Without a
  key, `report` falls back to deterministic rather than failing;
  `--judge=false` forces it off. The status (`judge on` / `off`) prints
  with the entry count.

## v0.3.0 (2026-06-10) — the judge; coupled launch with stull

The deterministic detector can't tell a precise term of art (`hook`) from a
dilutable tic (`substrate`) — that's word-sense disambiguation, which static
embeddings provably can't do (the gloss-coherence discriminator was measured
and inverted). So one optional, fenced LLM judgment enters the loop.

- `internal/judge`: the cell-facing contract — per-word strict-tool schema
  confining `demote_to` to the vetted ladder (select, never invent), a
  stull-compatible `Grammar`/`Safety` pair (safety rejects incoherent
  verdicts), and a verdict `Store` that is both cache and calibration log.
- `pipeline.Build` gains an optional `judge.Judger` gate: `term_of_art`
  entries are suppressed, `tic`/`mixed` kept with the chosen rung and a
  one-clause note; an inconclusive verdict fails safe to the un-gated
  entry. Off by default — the deterministic pipeline is unchanged without
  a judge.
- The fence is stull's `spec.Cell` used standalone (verified: `package
  spec` imports only stdlib). basanite is stull's first public consumer of
  its standalone fenced-oracle entry point; the two ship coupled.
- Deterministic proper-noun guard ahead of the fence: a `proper-nouns.txt`
  (data dir or `~/.config/basanite`) of known project/tool names is
  suppressed outright — a frequency+sense pass reliably mistakes a project
  literally named `calque` for the common word. Runs without the judge and
  saves it a call. Found because the live judge made exactly that miss on
  the real corpus.
- Ablation test proves the gate earns its keep with a scripted judge — no
  LLM required to test the gate logic.
- The fence is stull's `spec.Cell` used standalone, pinned to the public
  `stull v0.1.0` (basanite is its first public consumer). A deterministic
  proper-noun guard (`proper-nouns.txt`) suppresses project/tool names
  before the fence. Off by default; the deterministic pipeline is unchanged
  without a key. Validated live on the real corpus (hook/local/transcript
  suppressed, substrate→layer, public/tier mixed) with hermetic httptest
  coverage of the request shape and the fail-safe paths.

## v0.2.0 (2026-06-10)

- Chronic-tic detection: the report adds steady high-rate words the riser
  detector is structurally blind to, admitted by two deterministic
  evidence routes — genitive-frame repetition ("the spine of X", ≥25% of
  uses) or rarity mismatch (rare in SemCor English while frequent in the
  corpus; WordIC floor 10.5, abbreviations excluded). Context clustering
  was evaluated as a route and rejected: measured on real data, domain
  vocabulary clusters at the same delta as genuine tics.
- `cloze.Corpus` keeps raw sentence text alongside tokens, enabling
  `FrameFraction` (computed over the stopwords tokenization drops); `vet`
  reports the frame share per word.
- `refresh` subcommand: SessionStart-friendly background regeneration —
  exits instantly when the report is fresh, single-flights via a lock
  file, never fails loudly, logs each attempt to the state dir.
- Render quality: chronic rungs use a stricter 0.5 clean floor (their
  multi-sense candidate sets leak more), with a floored fallback so the
  clean cliff can't silence a strongly-evidenced flag; entries with no
  demote rung to offer are skipped.
- Single-pass tokenization: `internal/pipeline` tokenizes each turn once
  via the new token-preserving `text.SentenceTokens`, feeding both the
  window counts and a deduplicated, lemma-indexed `cloze.Corpus` — report
  wall time dropped from ~2m to ~54s on a 90-day corpus, with output
  verified byte-identical against the previous implementation on a frozen
  corpus snapshot.
- The report composition moved out of `main` into `internal/pipeline`
  (`Pass`/`Candidates`/`Build`) with end-to-end tests over the synthetic
  WordNet fixture, including a guard that a riser-free corpus never
  touches the vector table.
- Hardening: report saves use an exclusive temp file (no collisions, no
  leaked temp on failure); the hook refuses symlinked or oversized report
  files and creates session markers with `O_CREATE|O_EXCL` (no
  double-inject race); transcript lines over 64 MB are skipped instead of
  accumulated; `trend` time math uses one representation for windowing,
  bucketing, and labels.
- `vet` now applies the same candidate filter as `report` (candidates
  containing the tic word are excluded).

## v0.1.0 (2026-06-10)

Initial release. Deterministic, local, no-LLM vocabulary-tic detection over
Claude Code JSONL transcripts.

- Corpus reader: walks the transcript tree, extracts non-sidechain assistant
  prose (skips `thinking`, `tool_use`, and subagent transcripts),
  mtime-prunes files older than the analysis window.
- Tokenizer: strips code fences, inline code, URLs, and paths before
  counting; keeps hyphenated words; conservative lemmatizer (plurals and
  possessives only).
- `scan`: rising-lemma detector — recent window vs trailing baseline, scored
  by outside-loudest-project count × log rate ratio with a ratio floor, so
  diction tics separate from single-project topic words.
- `trend`: weekly per-lemma rates straight from the transcripts — the
  effectiveness check, and the view that catches chronic
  (baseline-saturated) tics that delta-over-baseline can't see.
- `ladder`: per-sense specificity ladders — WordNet 3.0 synonyms, hypernym
  demote rungs, and adjective similar-to clusters, ordered weakest →
  strongest by Resnik IC (SemCor table) with word-frequency IC fallback.
- `vet`: cloze substitution against the writer's own past sentences via
  GloVe 100d mean-pooled vectors, ranked by clean-substitution count, with
  signature-vs-tic classification as a clustering delta over a corpus
  baseline.
- `report` + `hook`: the turn-start loop — `report` composes the pipeline
  offline into JSON state; `hook` injects the rendered ladders (demote
  direction only) on UserPromptSubmit once per session in ~4 ms, silently
  skipping stale or missing state.
- `scripts/fetch-data.sh` fetches the data assets (WordNet 3.0, SemCor IC
  tables, GloVe 6B) from their origins; nothing is redistributed here.
