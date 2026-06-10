# Changelog

## Unreleased — v0.3.0 (the judge; coupled launch with stull)

In progress on the `judge` branch. The deterministic detector can't tell a
precise term of art (`hook`) from a dilutable tic (`substrate`) — that's
word-sense disambiguation, which static embeddings provably can't do (the
gloss-coherence discriminator was measured and inverted). So one fenced LLM
judgment enters the loop.

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
- Remaining before release: the live `cell.go` wiring (stull import +
  Anthropic SDK), gated on stull's public tag, then a real-corpus
  calibration pass (does it drop `hook`, keep `substrate`?).

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
