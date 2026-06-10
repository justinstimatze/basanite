# basanite — design notes

## The problem

Overusing a precision word destroys the contrast that gives it meaning.
"Load-bearing" should mean *this one element — remove it and the rest
collapses*. Applied to anything merely important, it inflates; by the tenth
use it just means "thing," and then it can't buy specificity when actually
needed. The fix is to reach for another word — or *demote* to the weaker
word that's actually true. The only missing piece is awareness in the
moment, because the drift is unconscious by definition. So the tool's whole
job is: surface the writer's crutches back to them, with good alternatives,
and let them make the swap.

basanite targets a specific writer: a Claude Code session's own output,
read back from the JSONL transcripts it leaves behind. The same approach
works for any corpus you can reduce to timestamped prose.

## The name

Basanite is the dark stone an assayer streaks a sample against to judge it
against a reference — a touchstone. "Touchstone" itself is saturated as a
project name; basanite is the obscure mineral term *for* one, with the same
meaning and a clean namespace.

## Core mechanism — two deterministic pieces, no LLM

1. **Rising-word detector** (`scan`). Lemmatize the recent transcripts,
   rank lemmas by delta over the writer's own trailing baseline, weighted
   by concentration (5× this week vs ~0 baseline beats a flat common
   word). Pure counting: score = count × ln(smoothed rate ratio), a
   Poisson G-statistic shape.

   Counting alone surfaces *topic* words (project names, the week's domain
   nouns), not diction. Two deterministic filters fix that: leave-loudest-
   out (a diction tic rises across projects; a topic word rises in one, so
   each word's loudest project is excluded from its count) and a rate-ratio
   floor (ordinary vocabulary drifts at 1.2–1.6× with topic mix; real
   forming tics clear 2×).

2. **Thesaurus ladder** (`ladder` + `vet`). For the top risers, a local
   WordNet lookup produces candidates, refined by the two mitigations
   below, then injected at turn start:
   `agent (2.6× your baseline): negotiator < representative < [agent]`.

The output is **awareness, not prohibition** — never "don't say X." Naming
a word to suppress it is ironic-process priming and backfires. The
swap-or-demote-or-keep decision stays with the writer.

## The two mitigations

A flat WordNet synset is a synonym *set*, not a replacement — context-free,
often the wrong sense, not interchangeable. Two deterministic fixes:

- **Mitigation A — cloze substitution against the writer's own corpus.**
  You can't context-fit at turn start (no target sentence exists yet), but
  the past sentences that contain the word *are* the context. Mask the
  word in each real use, substitute each WordNet candidate, and compare
  static-embedding sentence vectors (GloVe, mean-pooled, local,
  deterministic). A candidate that preserves the vector across most real
  uses is a true replacement in this idiolect; a wrong-sense artifact
  wobbles and self-eliminates. Static embeddings are crude per sentence;
  the noise cancels averaged over a dozen uses.

- **Mitigation B — order by specificity, not similarity.** Synonyms aren't
  interchangeable; they're a cline of strength. Order the survivors by
  WordNet information content (Resnik IC from the SemCor table, with a
  word-frequency fallback where no hypernym tree exists) so the set reads
  weakest → strongest. This is the real fix for the dilution problem: the
  injection becomes "you grabbed the top rung reflexively; pick the rung
  that's actually true." Half the time the right move is demoting, not
  swapping sideways.

- **Freebie — variance as the signature-vs-tic discriminator.** The same
  embedding pass classifies for free: the variance of a word's use-vectors
  is the signal. Scattered across diverse contexts → flexible *signature*,
  leave it alone. Clustered in near-identical contexts → reflexive *tic*,
  flag it. Reported as a delta against a corpus baseline, because in a
  one-author corpus everything is topically similar and the absolute
  number means nothing. This freebie is why no LLM judge is needed.

## What is deliberately NOT in it

The design started bigger and was trimmed hard. Recording the cuts so they
don't creep back:

- **~~No LLM in the loop.~~ Reversed on measurement — see "The judge"
  below.** The original cut assumed the variance freebie covered the
  signature-vs-tic judgment. It doesn't reach the term-of-art case, and a
  deterministic substitute was tried and failed (gloss-context coherence
  did not separate `hook` from `substrate`). One fenced LLM judgment is now
  in the loop; everything else stays offline, local, deterministic.
- **No per-turn rewriting or post-processing.** Filtering output launders
  voice and adds latency for a signal that fires rarely. Awareness at turn
  start; the writer decides. (Still true — the judge runs at report-build
  time, not per turn; the hook stays a 4ms deterministic injector.)
- **No framework wrapper as the runtime.** The hook is one state with a
  self-loop — "on prompt submit, maybe inject once" — and a state-machine
  framework can't improve a single if-statement, so the *runtime* stays a
  plain subcommand. But the build-time judge *does* use stull's `spec.Cell`
  as a standalone fence (not its machine runtime) — see below.
- **Awareness, never prohibition** (see above).

## The judge — a measured reversal (the hybrid-loop seam)

The two deterministic mitigations detect and rank well, but the
*prescription* has a hard boundary the no-LLM design walked into: telling a
**dilutable tic** (`substrate` — reach for it loosely, a weaker word is
often truer) from a **precise term of art** (`hook` — the Claude Code
concept; there is no valid synonym, and `→ snare` would be actively wrong)
is word-sense disambiguation. Static embeddings and frequency are
sense-blind by construction; the context that disambiguates is exactly what
they discard.

This was not assumed — it was measured. The cheap deterministic candidate,
gloss-context coherence (does WordNet's sense of the word match the words it
co-occurs with in the corpus?), *inverted* on real data: `hook` 0.63,
`substrate` 0.57. So that one judgment — and only that one — crosses into a
fenced LLM cell:

- **The deterministic detector manufactures the cell's choice set.** Risers
  + the WordNet/cloze-vetted demote ladder + real sample sentences are the
  input. The frequency gate means the judge sees ~12 words per refresh,
  never the vocabulary.
- **The cell can only select, never invent.** Its strict-tool schema
  confines `demote_to` to an enum of exactly that word's vetted ladder plus
  "none"; a grammar re-parses; a safety check rejects incoherent verdicts
  (a term of art offering a swap, a tic naming none). Malformed or
  incoherent → fail safe to the un-gated entry. The LLM brings the sense
  judgment; the code keeps it on the rails.
- **The gate acts.** `term_of_art` → suppressed (no valid substitute);
  `tic`/`mixed` → kept, carrying the chosen rung and a one-clause note.
- **The verdict log is cache and calibration at once** — keyed by
  word+ladder+model+schema, so an unchanged word isn't re-judged, and the
  append-only trail is what an ablation reads to confirm the gate earns its
  keep.

The fence is stull's `spec.Cell` (`NewConfinedCell` + `Cell.Check`), used
**standalone** — verified from source: `package spec` imports only stdlib
`sort`, so the Cell is a fenced-oracle library independent of stull's
hook-statechart runtime. That runtime is the *wrong* host here (the
judgment is build-time batch over corpus payload, not per-hook-event over
transcript), so basanite uses stull's Cell as the fence, not its machine as
the shell. The two ship as a coupled launch: basanite is stull's first
public consumer of the standalone-Cell entry point.

Cost honestly stated: the judge needs Anthropic credentials at
report/refresh time (Haiku, prompt-cached across the dozen calls, offline,
cheap), and it is the one place basanite is no longer self-contained. It is
off by default; the deterministic pipeline runs unchanged without it.

## The known-tics reference (Claude Bingo)

The detector so far catches tics by *shape* — a rate that rose, a word rare
in general English, a frame that repeats. That misses two cases by
construction, and a curated reference is the honest fix for both. The
reference is a single **user-owned** list, not a baked-in one: what ships
embedded is a *starter seed* (a conservative, high-precision sample of the
globally common leans — the assistant-register staples that recur in Claude
Code transcripts, plus a few iconic signatures from the "Claude Bingo" card),
and on first run it is copied to the user's `known-tics.txt`, the only file
read thereafter. This is the deliberate choice over a baked-in list plus a
user override: two lists meant the seed silently re-added what the user
deleted, so a lean could never age out — and they do age out, as the model
underneath changes. One list the user owns lets entries accrete and fall away.
It is a *reference*, not a denylist: a seeded entry still has to clear the
chronic rate and dispersion gates before it surfaces, and the output stays
awareness, never prohibition.

- **Common-English single-word leans.** `substrate` and `load-bearing` are
  rare in general English, so the rarity route sees them. `surface`,
  `frame`, `honor` are *not* rare — they sit at ordinary WordIC — yet they're
  reflexive Claude leans. No frequency-or-rarity shape separates them from
  ordinary vocabulary, because there isn't one; the signal is external
  knowledge. So the known list is a third chronic admission route, parallel
  to frame and rarity, with its own output budget so it can't be crowded out.
  These still run the ladder and the judge — the reference only admits; it
  doesn't decide the verdict.

- **Phrases.** "I want to honor that", "sitting with you in this" — the tic
  is a multi-word sequence whose words are each unremarkable. The single-token
  pipeline (lemmas, WordNet ladders, cloze vectors) is structurally
  word-shaped and can't represent it. So phrases get a separate, simpler
  track: count the curated phrases over the *surface* word stream (stopwords
  kept — the phrase's evidence is exactly what the lemma tokenizer drops),
  surface the most-used. There is no synonym ladder for a stock phrase, so a
  phrase entry is awareness-only ("you keep reaching for this"). A fixed
  multi-word phrase is unambiguously diction, not topic, so it needs none of
  the leave-loudest-out / cross-project machinery the single-word risers use
  to separate diction from domain nouns — a count floor suffices.

The cost is honesty about provenance: the rest of the pipeline is *derived*
from the corpus; the reference is *asserted* from outside it. That's why it's
admission-only and gated, never a suppression or a verdict — the curated list
points the detector at a word; the deterministic stack (and the judge) still
have to earn the flag.

## Calibration findings (real data, ~770k tokens over 21 days)

- The substitutability ranking works: `problem → question/trouble` held in
  49/50 real sentences; literal-sense candidates for a metaphorically-used
  word maxed out near 24/50.
- Chronic tics are invisible to delta-over-baseline — a word used at a
  steady ~1/1k for weeks *is* its own baseline. `trend` (weekly rates
  straight from the transcripts) is the view that catches those, and
  doubles as the effectiveness check after the hook goes live.
- Frame-shaped tics ("the spine of X") evade the variance classifier: the
  contexts are topically diverse even though the *frame* repeats. The
  frame-fraction check (share of uses matching `<det> <word> of`, computed
  over raw sentence text because the evidence is exactly the stopwords
  tokenization drops) catches these and is one of the chronic stage's two
  admission routes.
- Chronic tics needed their own detector — a word at a steady ~1/1k for
  months is its own baseline, so delta-over-baseline is structurally
  blind. The working signal is **rarity mismatch**: frequent in the corpus
  while rare in a general-English reference (SemCor word frequency).
  Genuine leans (`load-bearing`, `substrate`) score 11–13 where ordinary
  domain words sit at 7–10. Context clustering was tried as a third route
  and rejected on measurement: domain vocabulary legitimately clusters at
  the same delta as real tics.
- Wrong-sense rungs still leak through the cloze filter at the default
  threshold (a movie-sense synonym surviving for `feature`; fishing-sense
  rungs for `hook`); 100d mean-pooled vectors only discriminate senses so
  far. The render mitigates by showing only the demote direction, and
  chronic entries use a stricter clean floor.
