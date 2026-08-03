# basanite

[![ci](https://github.com/justinstimatze/basanite/actions/workflows/ci.yml/badge.svg)](https://github.com/justinstimatze/basanite/actions/workflows/ci.yml)

A deterministic, local tool — **no LLM in the default path** — that detects
**vocabulary tics** in your Claude Code sessions' output — words the model
reaches for reflexively (`load-bearing`, `spine`, …) until overuse debases
them. It measures frequency drift over the JSONL transcripts, then injects
awareness of each tic, plus a ranked ladder of alternatives, at turn start.
A fenced LLM judge handles the one judgment the deterministic stack can't —
telling a term of art from a dilutable tic — and runs by default when an API
key is configured (it falls back to deterministic without one).

Basanite is the dark stone an assayer streaks a sample against to judge it —
a touchstone. Design rationale, including what was deliberately left out,
lives in [DESIGN.md](DESIGN.md).

## Commands

```
basanite scan            # rank rising lemmas: recent window vs trailing baseline
basanite trend <lemma>…  # weekly rate per lemma — the effectiveness check
basanite ladder <word>…  # specificity ladder per sense, weakest → strongest
basanite vet <word>…     # judge candidates against your own past sentences
basanite report          # full pipeline (scan→vet→ladder) → state file, ~1 min
                         #   judges out terms of art by default; --judge=false for deterministic-only
basanite refresh         # regenerate the state file if stale (runs from both hooks)
basanite hook            # UserPromptSubmit entry: inject the report, ~4 ms
basanite display         # MessageDisplay entry: show the demote rung instead of the tic
basanite ledger          # flagged tics over time — is a tic's rate falling?
basanite audit           # which curated known-tics entries have ever fired?
basanite version
```

`scan` flags: `-recent 7` / `-baseline 14` (window sizes in days), `-top 25`,
`-min 5` (minimum recent count), `-ratio 2.0` (minimum rate ratio),
`-dir ~/.claude/projects`.

## Setup

```
make install                                      # version comes from git describe
scripts/fetch-data.sh ~/.local/share/basanite     # data assets (see Data below)
basanite report                                   # build the first state file
basanite install                                  # register the hooks
```

`basanite install` writes all three hook registrations into
`~/.claude/settings.json` using its own resolved absolute path — hooks run in
whatever environment Claude Code was launched from, which may not have your Go
bin directory on `PATH`, so the path has to be absolute and only the binary
knows it. It backs the file up first, preserves everything it does not own,
and repoints an existing basanite registration rather than adding a second
one, so re-running it after `go install` to a new location is the fix rather
than the problem. `-dry-run` prints the changes and writes nothing;
`-status` shows what is registered right now; `-uninstall` removes them.

Hooks load at startup, so open a new session afterwards.

Pass the fetch script a real path (as above) rather than letting it default
to `./data` — the default only works when you run basanite from the
checkout, since `./data` is resolved against the current directory.

The three hooks `install` registers, and what each is for:

| event | command | why |
| --- | --- | --- |
| `SessionStart` | `basanite refresh` | regenerate the report when it goes stale. Exits instantly when it's fresh, regenerates in the background when not (`async`, so it never delays the session), single-flights via a lock file, and logs each attempt to `refresh.log` in the state dir. |
| `UserPromptSubmit` | `basanite hook` | inject tic awareness at turn start, ~4 ms. Also checks staleness and starts a detached `refresh` when needed — `SessionStart` fires once per session, and a session can run for weeks. |
| `MessageDisplay` | `basanite display` | render the demote rung instead of the tic, ~4 ms. See [below](#not-having-to-read-it-display) — this one is optional and changes only what you read. |

A report is stale once it is older than six days, or once it was built by a
different version of basanite, or once you have edited the known-tics list
since — age alone cannot see the last two, and both leave the timestamp
exactly where it was. Past seven days the hook gives up on it and injects a
one-line breadcrumb rather than silently serving nothing.

Regeneration is automatic and needs no attention. Attempts back off fifteen
minutes from the last one, recorded in `refresh.log`, so a pipeline that
cannot run retries occasionally instead of on every prompt.

## How it works

### Separating tics from topics (`scan`)

Score = outside-loudest-project count × ln(smoothed rate ratio):

- **log-ratio** weights concentration — 5× this week vs ~0 baseline beats a
  flat common word (a Poisson G-statistic shape, add-half smoothed);
- **leave-loudest-out** kills topic words — a diction tic rises across
  projects, a topic word (project names, the week's domain nouns) rises in
  one, so each word's loudest single project is excluded from its count;
- **ratio floor** (default 2×) cuts ordinary vocabulary drifting at 1.2–1.6×
  with the week's topic mix.

### The ladder (specificity ordering)

`ladder` orders each sense's candidates by **specificity**, weakest →
strongest — Resnik information content from the SemCor IC table for nouns
and verbs, word-frequency IC as the fallback (adjectives and adverbs have no
hypernym tree). Rungs come from same-synset synonyms, one and two hypernym
levels up (the *demote* direction: toward the weaker, more general word
that's often the truer one), and similar-to clusters for adjectives;
ties within a synset break toward the more common word. The `*` marks where
the flagged word itself sits:

```
load-bearing (a) capable of bearing a structural load
supporting(11.2) < bearing(11.2) < *load-bearing(13.1)
```

### The cloze pass (substitutability in *your* sentences)

`vet` can't context-fit at turn start (no target sentence exists yet), so
your past sentences are the context: for each WordNet candidate, mask the
target in up to 50 real uses (evenly sampled over the window, deduped),
substitute the candidate, and compare GloVe mean-pooled sentence vectors. A
candidate that preserves the vector across most uses is a true replacement
in your idiolect; wrong-sense artifacts wobble and self-eliminate.
Out-of-vocabulary candidates are skipped, not scored — an OOV substitution
would earn a free near-1 cosine.

The same pass classifies signature vs tic for free: the mean pairwise cosine
of a word's use-vectors (sentences minus the word), reported as a **delta
against the corpus baseline** — in a one-author corpus everything is
topically similar, so only the delta means anything. Above baseline =
clustered contexts = tic-like; below = diverse = signature, leave it alone.

### Chronic tics (the frame and rarity routes)

A chronic tic is invisible to `scan` — a word used at a steady ~1/1k for
months is its own baseline. The report adds steady high-rate words used
across several projects, each admitted by a deterministic evidence route.
Two are described here; a third (the curated known-tics list) and a fourth
(the marked route, for live metaphors) have their own sections below.

- **frame**: the genitive metaphor frame `<det> <word> of` ("the spine of
  the design") repeats across ≥25% of uses. A word can be topically
  diverse while the frame is the tic — this is computed over raw sentence
  text, since the evidence is exactly the stopwords tokenization drops.
- **rarity**: the word is rare in general English (SemCor word frequency)
  while frequent in your corpus. `load-bearing` and `substrate` score
  11–13 where ordinary domain words (`test`, `session`, `file`) sit at
  7–10; the floor is 10.5. Three-letter "rare words" are excluded — they
  are almost always abbreviations whose WordNet senses mislead.

Context clustering is deliberately **not** an admission route: measured on
real data, domain vocabulary legitimately clusters at the same delta as
genuine tics, so it can't separate them.

### Live metaphors (the marked route)

`load-bearing` is the case the routes above can detect but not *rank*. The
rarity route admits it as a candidate, then its modest rate keeps it out of
the slots — and every other cheap statistic that might promote it
(dispersion, rarity, concreteness) is shared by ordinary dev jargon. The
separating signal is
**context-incongruity**: the cosine distance between a word's literal sense
(its GloVe vector) and the centroid of the contexts it actually appears in.
A live metaphor is a physical word recurring in non-physical contexts
(`load-bearing` scores ~1.0); literal jargon stays in its home
neighborhood and sinks (`running` 0.34, `hook` 0.57, `slot` 0.60).

The route gathers dispersed, rare-in-English candidates, ranks them by
incongruity, gates at a floor of 0.85, and hands the survivors to the same
judge — which is what separates a live metaphor from a term of art whose
vector is merely noisy (`grep`, `config`). A marked entry renders and is
counted as chronic. `-marked N` sets the cap; `-marked 0` disables it.

### The term-of-art judge (optional)

Everything above is deterministic and offline. But the deterministic stack
has one boundary it provably can't cross: telling a **dilutable tic**
(`substrate` — reach for it loosely, a weaker word is often truer) from a
**precise term of art** (`hook` — the Claude Code concept; `→ snare` would
be actively wrong). That's word-sense disambiguation, and static embeddings
are sense-blind (measured: the deterministic discriminator inverted, scoring
`hook` *more* substitutable than `substrate`). So `report --judge` adds one
fenced LLM judgment — and only that one.

The deterministic detector hands each riser its vetted demote ladder and
real sample sentences; the judge classifies `tic` / `term_of_art` / `mixed`
and, for a tic, selects the truer rung **from that ladder only** — a strict
tool schema confines it to the vetted set, so it can never invent a word, and
a malformed or incoherent verdict fails safe to the un-gated entry.
`term_of_art` words are dropped (no valid substitute); `mixed` words are kept
with a per-sense note. The fence is [stull](https://github.com/justinstimatze/stull)'s
`spec.Cell` used as a standalone fenced-oracle library.

It runs **by default when a key is configured** — the deterministic-only
report is the one that confidently mis-suggests synonyms for terms of art
(`hook → snare`), so the judge is the default experience, not an add-on. It
needs `ANTHROPIC_API_KEY` (in the environment or a `.env` — see
`.env.example`), runs at report time (not per turn), and uses a cheap model
with prompt caching. Without a key it falls back to deterministic rather than
fail. A `proper-nouns.txt` (data dir or `~/.config/basanite`) of your
project/tool names is suppressed deterministically *before* the judge — a
frequency+sense pass otherwise mistakes a project literally named `calque`
for the common word.

```
basanite report                  # judge runs when a key is configured
basanite report --judge=false    # deterministic-only, no API calls
```

### The known-tics reference (Claude Bingo)

The derived signals (rising rate, rarity, repeated frame) catch tics from
their *shape*. Some leans are known by reputation instead. basanite ships a
conservative **sample of the globally common ones** — the assistant-register
staples that recur across Claude Code transcripts (`you're absolutely right`,
`worth noting`, `that said`) plus a few iconic signatures seeded from the
community "Claude Bingo" card — embedded as `known-tics.txt`. It is kept
high-precision on purpose; niche or personal leans go in your own list. It
feeds two things:

- **Known single words** become a third chronic admission route. The rarity
  route catches words rare in general English (`substrate`, `load-bearing`);
  the known route catches *common*-English leans it can't see by shape
  (`surface`, `frame`, `honor`) — but only when they're steady and dispersed,
  and they still go through the ladder and the judge. Flagged "a common
  Claude lean".
- **Phrases** get their own track. The single-token detector is blind to
  stock phrases (`i want to honor that`) — the words are individually
  unremarkable; the tic is the sequence. A matcher counts the curated phrases
  over the surface word stream (stopwords kept) and surfaces the most-used as
  awareness-only entries — there's no synonym ladder for a stock phrase, just
  the awareness that you keep reaching for it.

It stays a reference, not a denylist: a seeded entry only surfaces when
you're actually leaning on it now. And it's a *seed*, not a baked-in list —
on first run the starter set is written to `~/.config/basanite/known-tics.txt`,
and from then on that file is the only one read. It's yours to curate: add
your own leans, delete ones that stop mattering as models change. Nothing
upstream re-applies over your edits (one entry per line, `#` comments; a line
with a space is a phrase, otherwise a single word). `--phrases N` /
`--phrase-min N` tune the phrase track; `--phrases=0` disables it.

### The hook

`report` composes the pipeline offline (one corpus read; risers with no
WordNet entry drop out — which conveniently kills project-name noise; rungs
survive only if they were clean substitutions in ≥40% of real uses — ≥50%
for chronic entries, whose multi-sense candidate sets leak more — and don't
contain the tic word itself). `hook` reads the resulting JSON, injects once
per session, and treats every abnormal case — missing report, stale report,
no session id — as silent success. It never touches the corpus, WordNet, or
vectors, and never blocks a prompt.

The injection is **awareness, not prohibition** — never "don't say X":
naming a word in order to suppress it tends to prime it instead
([ironic process theory](https://en.wikipedia.org/wiki/Ironic_process_theory)).
The ladder reads weakest → strongest so the move can be *demote*, not just
swap.

The console `report` view shows every entry; the **injection is budgeted** to
5 words and 2 phrases (`hook -top-words` / `-top-phrases`; 0 = uncapped),
with judge notes cut to their first sentence. A model reading eighteen
vocabulary directives mid-task skims them — a handful it can hold is worth
more than a wall it doesn't.

The word budget is **split between the lanes**, half each with chronic
rounding up, rather than filled in report order. Report order is risers
first, so a first-come cap spends every slot on them: `load-bearing` sat in
the report as a curated chronic entry for months, at roughly sixty uses a
day, and was injected exactly never. Within the chronic share the curated
known-tics go first — a riser is an observation that a habit *may* be
forming and it ages out on its own, while a known tic is you having said in
advance that you never want to see the word. Either lane's unused share
spills to the other.

### Not having to read it (`display`)

The hook above tries to change what gets written, which takes a turn to land
and does not always land. `basanite display` is the other half: a
`MessageDisplay` hook that swaps a flagged word for its vetted demote rung in
the text streaming to your terminal. You read `supporting`; the model wrote
`load-bearing`.

```json
{"hooks": {"MessageDisplay": [{"hooks": [{"type": "command", "command": "/home/you/go/bin/basanite display"}]}]}}
```

It is **display-only, by design of the event**: the transcript and the model's
own context keep the original word. Two consequences worth being clear about.
The model never sees the swap, so this changes nothing about what it writes —
it is relief, not intervention. And because `report`, `trend` and `ledger` all
read the transcripts, the measurement stays honest no matter what the screen
shows: the rate you're told is the rate that was written.

The replacement is the rung the judge picked, read live from `report.json`, so
the table maintains itself as your tics change. By default only **curated
known-tics** are swapped. A ladder is vetted for how well a word substitutes
*across your uses on average*, which is the right test for offering awareness
and the wrong one for rewriting every occurrence — the live report demotes
`turn` to `change` and `five` to `figure`, which as a display rule gives you
"it is your change to indicate figure things". Words on the curated list are
the ones you already declared unwanted, and they tend to be unwanted precisely
because they're loose figurative intensifiers, where any occurrence can take
the weaker word.

- `-all` opts into every judged entry, garbage included.
- `-words load-bearing:critical,seam:joint` overrides or adds pairs — the
  escape hatch for a lean basanite can't see, since it only reads assistant
  prose and not, say, your own hook templates.
- Code is left alone: inline backticks, paths, URLs, and fenced blocks
  (tracked across streamed batches) are never rewritten, so what you copy is
  what was written. Stock phrases are never swapped — they carry no ladder.

Claude Code holds each batch of lines until the hook returns, so it runs in
~4 ms and treats every abnormal case as silent success; on any error the
original text is displayed.

Every swap is recorded to `swaps.jsonl` in the state dir, since the transcript
keeps the original and nothing else would know it happened:

```
$ basanite ledger -swaps
basanite swap ledger — tics replaced on screen by the display hook.
The model still wrote them: this counts what you were spared, not what changed.

  load-bearing → supporting            142×   last 2026-08-14
  substrate → component                 88×   last 2026-08-14

  230 replacements over 12 days (19.2/day)
```

That count deliberately does not match `trend` or `ledger`, which read the
transcripts and report what was *written*. The gap between the two is the
display hook doing its job. Use `-no-log` to turn the recording off.

### Knowing whether it works

The transcripts are the longitudinal record, so the intervention is
measurable: after the hook goes live, a flagged word's rate should fall and
its alternatives' rates rise.

`basanite trend <lemma>` reads weekly rates straight from the transcripts.
It also exposes the two tic shapes: *forming* (rate rising from zero — what
`scan` catches) and *chronic* (rate high and flat, invisible to
delta-over-baseline because the baseline is already saturated — `trend` is
the view for those).

`basanite ledger` is the same question without having to name a word or
remember a number. Each refresh folds the report into `ledger.json` beside
it, so every flagged lemma keeps its first-flagged date, its rate then and
now, and — the part no single report can show — the date it dropped off the
list entirely:

```
still flagged:
  substrate        since 2026-07-14 (2w, 4 refreshes)  1.08 → 0.94/1k  ↓13%  chronic
  load-bearing     since 2026-07-14 (2w, 4 refreshes)  0.61 → 0.55/1k  ↓11%  chronic
  running          since 2026-07-14 (2w, 4 refreshes)  1.62 → 1.80/1k  ↑11%  chronic

faded out (dropped from the report):
  spine            2026-06-02 → gone 2026-07-14  0.71 → 0.12/1k  ↓83%  chronic
```

It accrues through the `refresh` hook with nothing to run by hand. Treat it
as a record, not a proof: a falling rate is consistent with the loop
working, but direct callouts and topic drift are unmeasured confounds.

### Auditing the curated list (`audit`)

A curated list cannot answer the one question that decides whether it is worth
curating: **which entries have ever fired?** From outside, an entry that never
matches looks exactly like one that matches constantly — it costs a line, it
costs a scan, and it reassures you the tic is covered while nothing is watching
for it. The same blind spot hides the opposite case: when a word you know you
overuse never appears in a report, "the ranking is working as designed" and
"the pattern never matches" are indistinguishable.

`basanite audit` counts every entry against the corpus and says where it
stands:

```
  ENTRY                             HITS   RATE/1K  PROJ  STATUS
  substrate                         3695     0.931    50  reported (#12)
  calibration                       1154     0.291    43  term of art
  chrome                            1733     0.436    31  read as name
  texture                            314     0.079    25  below cutoff
  "the thing underneath the thing"     0     0.000     0  NEVER FIRES
```

Dead entries become visible and can be cut, so the list stays honest, and a
`NEVER FIRES` row separates "not a problem for you" from "the pattern is
broken". The seed shipped for a while with a block of iconic phrases everyone
quotes about Claude; the audit found that not one of them occurred anywhere in
months of transcripts, and they have been cut. Folklore about a model's
register turns out not to be evidence about it.

`term of art` is separated from `below cutoff` because they look identical from
outside and have opposite fixes. A word that clears every threshold, reaches
the judge, and is judged unsubstitutable will never surface no matter how the
rate floor moves — reading that as "ranked out" sends you tuning a number that
was never the reason. The judge's note in `verdicts.jsonl` says why.

`read as name` is the same kind of fact one step earlier. A project or product
name reaches the chronic route looking exactly like a lean — steady rate, wide
dispersion, an ordinary English ladder — so the scan checks how the corpus
writes the word before spending a judge call on it. A word capitalized in the
middle of a sentence more than half the time is a name, and no ladder word
substitutes for a name. It appears here rather than vanishing quietly, because
a suppression you cannot see is the failure this command exists to catch.

Read the `PROJ` column before believing a row. Writing about an entry puts it
in the transcripts the next audit reads, so an entry supported by a single hit
in a single project is usually this tool citing its own output back to itself
— a real lean disperses across projects. The audit says so at the bottom when
it sees rows of that shape.

Phrases are counted over the surface word stream and words over the lemmatized
one, since that is the stream each is written to be found in; auditing a
stopword-heavy phrase against the tokenized stream would call it dead for the
wrong reason. `-never` narrows the output to just the entries worth cutting,
`-days` sets the window (default 90), and `-list` audits a file other than
your own. It prints the list path it read, because an audit that reports on a
list it never opened is the exact failure it exists to catch.

It reads the whole window in one pass and takes a few minutes on a large
corpus — it's an occasional command, not part of any loop.

## Data

`scripts/fetch-data.sh` (needs `curl`, `tar`, `unzip`; ~1.2 GB transient
disk) downloads the assets and verifies each against a pinned sha256:

- **WordNet 3.0** database files (~35 MB unpacked) — from Princeton, under
  the [WordNet license](https://wordnet.princeton.edu/license-and-commercial-use).
- **WordNet-InfoContent** tables (SemCor Resnik IC) — via the
  [nltk_data](https://github.com/nltk/nltk_data) mirror.
- **GloVe 6B** vectors (822 MB download, the 100d table — 347 MB — is kept)
  — [Stanford NLP's release](https://nlp.stanford.edu/projects/glove/)
  (Open Data Commons PDDL), fetched from the
  [stanfordnlp/glove](https://huggingface.co/stanfordnlp/glove) Hugging
  Face mirror.

Nothing is redistributed in this repository. The binary looks for assets in
`$BASANITE_DATA`, `./data`, then `~/.local/share/basanite`.

## Known limitations

- Without `--judge`, wrong-sense rungs leak through the cloze filter (100d
  mean-pooled vectors only discriminate senses so far) — most visibly for
  dev jargon, where `hook`'s WordNet senses are fishing and boxing. The
  demote-only render hides most of it; the judge suppresses the rest by
  recognizing the term of art.
- The judge is an LLM and is not deterministic across prompt wording —
  tuning it to fix one word can perturb another (observed: a prompt edit to
  catch project-name proper nouns regressed `local`). `temperature: 0`
  makes a *cached* verdict stable, but the judgment remains a model call,
  not a proof. Project-name proper nouns are handled deterministically by
  `proper-nouns.txt`, not by the model.
- The chronic stage needs frame, rarity, or known-tics evidence; a chronic
  tic that is a common English word, used without a repeating frame and not
  on the curated reference, won't be flagged.
- Phrase detection is exact match against the curated list — it catches the
  known phrases, not novel ones, and a heavily reworded variant slips it.
- Two separate caps, and the tighter one is the injection's. The report
  admits up to 8 risers, 4 chronic, 4 known, 6 marked and 4 phrases; the
  turn-start injection then shows 5 words and 2 phrases of that. So a word
  can be correctly detected, judged and written to the report and still not
  reach you — a tic below either cut waits its turn. `basanite report` on
  the console is the view with nothing withheld.

## License

MIT (code). Data assets are fetched from their origins under their own
licenses — see Data above.
