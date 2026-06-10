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

- **No LLM in the loop.** The signature-vs-tic judgment is covered by the
  variance freebie. Everything is offline, local, deterministic.
- **No per-turn rewriting or post-processing.** Filtering output launders
  voice and adds latency for a signal that fires rarely. Awareness at turn
  start; the writer decides.
- **No framework wrapper.** The runtime behavior is one state with a
  self-loop — "on prompt submit, maybe inject once." A state-machine
  framework can't improve a single if-statement; the hook stays a plain
  subcommand. Revisit only if the tool grows a real cross-turn protocol
  (e.g. adaptive nagging: inject → watch the rate → escalate or back off
  as genuinely distinct states).
- **Awareness, never prohibition** (see above).

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
