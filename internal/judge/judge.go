// Package judge is basanite's term-of-art gate: a fenced LLM oracle that
// classifies each flagged word as a dilutable tic or a precise term of art,
// and selects any demotion only from the deterministically-vetted ladder.
//
// It uses stull's spec.Cell as the fence — schema-confined generation plus a
// grammar parse-backstop and a safety check — driven at report-build time
// over the handful of risers, never the vocabulary. The deterministic
// detector manufactures the candidate ladder; the cell can only SELECT from
// it. This is the hybrid-loop seam: static embeddings + frequency provably
// cannot tell "hook" (the Claude Code concept, no valid synonym) from
// "substrate" (a real lean), so that judgment — and only that judgment —
// crosses into the LLM, fenced.
//
// This file is the cell-facing contract: the schema (the formal language L),
// the grammar that parses a completion into a term, the safety check that
// rejects incoherent verdicts, and the verdict parse. It imports neither
// stull nor an LLM SDK — the Grammar/Safety signatures match stull's frozen
// types, and the wiring (NewConfinedCell + the strict-tool call + Cell.Check)
// is assembled in cell.go once stull's public tag is pinned.
package judge

import (
	"encoding/json"
	"strings"
)

// SchemaVersion tags persisted verdicts; bump it when the schema or the
// instructions change so the cache invalidates rather than serving a verdict
// the current prompt would not produce.
const SchemaVersion = 3

// Roles a flagged word can play in the writer's usage.
const (
	RoleTic       = "tic"         // reached for loosely; a weaker rung is often truer
	RoleTermOfArt = "term_of_art" // fixed technical referent; no valid substitute — keep
	RoleMixed     = "mixed"       // both: a term of art that is also sometimes used loosely
)

// noDemote is the in-band sentinel for "no replacement"; the schema enum uses
// it because the strict-tool subset is cleaner with a single string type than
// a nullable.
const noDemote = "none"

// Verdict is the parsed judgment for one word.
type Verdict struct {
	Role     string `json:"role"`
	DemoteTo string `json:"demote_to"` // a rung from the offered ladder, or "" once parsed
	Note     string `json:"note"`
}

// Judger classifies a flagged word from its candidate ladder and real sample
// uses. ok=false means the judgment was inconclusive — the fence failed safe
// (malformed or incoherent verdict) or the call errored — and the caller must
// keep its current, un-gated behavior rather than guess. The concrete
// implementation owns its own caching and calibration log; this interface is
// pure so the pipeline gate can be tested with a scripted fake and no LLM.
type Judger interface {
	Judge(word string, ladder []string, samples [][]string) (v Verdict, ok bool)
}

// Schema builds the strict-tool JSON Schema for one word. demote_to is
// confined at generation time to exactly that word's vetted ladder plus
// "none", so the model cannot emit a replacement that was not deterministically
// vetted. It stays within the strict-tool-use subset stull documents: an
// object with additionalProperties:false and enums, no numeric bounds, string
// lengths, or recursion.
func Schema(ladder []string) map[string]any {
	demoteEnum := append([]string{noDemote}, ladder...)
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"role":      map[string]any{"type": "string", "enum": []string{RoleTic, RoleTermOfArt, RoleMixed}},
			"demote_to": map[string]any{"type": "string", "enum": demoteEnum},
			"note":      map[string]any{"type": "string"},
		},
		"required": []string{"role", "demote_to", "note"},
	}
}

// Grammar returns a stull-compatible Grammar (func(string)(string,bool)): it
// parses the tool-input JSON, validates role and demote_to against the
// ladder, and emits the canonical term "role:demote_to". Anything outside the
// language returns ("", false) — not well-formed — so the cell fails safe
// instead of acting on garbage. This is the parse-time backstop behind the
// schema's generation-time confinement; the two must agree.
func Grammar(ladder []string) func(string) (string, bool) {
	allowed := map[string]bool{noDemote: true}
	for _, w := range ladder {
		allowed[w] = true
	}
	return func(raw string) (string, bool) {
		var v Verdict
		if json.Unmarshal([]byte(raw), &v) != nil {
			return "", false
		}
		switch v.Role {
		case RoleTic, RoleTermOfArt, RoleMixed:
		default:
			return "", false
		}
		if !allowed[v.DemoteTo] {
			return "", false
		}
		return v.Role + ":" + v.DemoteTo, true
	}
}

// Safety returns a stull-compatible Safety (func(string)bool): it rejects
// *incoherent* verdicts so they fail safe rather than reach the report. A term
// of art that offers a demotion contradicts itself; a tic with no demotion is
// not actionable awareness. This is the safety stage earning its keep — the
// term denotes something the pipeline will act on (show, suppress, or demote),
// so an incoherent term must not pass.
func Safety() func(string) bool {
	return func(term string) bool {
		role, demote, ok := strings.Cut(term, ":")
		if !ok {
			return false
		}
		switch role {
		case RoleTermOfArt:
			return demote == noDemote // must offer no substitute
		case RoleTic:
			return demote != noDemote // must name the truer rung
		case RoleMixed:
			return true // may or may not demote
		}
		return false
	}
}

// Parse extracts the full Verdict from the validated tool-input JSON. Cell.Check
// (grammar+safety) is the gate and yields a single canonical Term string;
// Parse carries the note that the Term cannot. demote_to "none" becomes "".
func Parse(raw string) (Verdict, bool) {
	var v Verdict
	if json.Unmarshal([]byte(raw), &v) != nil {
		return Verdict{}, false
	}
	if v.DemoteTo == noDemote {
		v.DemoteTo = ""
	}
	return v, true
}

// Instructions is the cell's system instruction. The per-call payload (the
// word, its ladder, and sample sentences) is rendered by Payload and supplied
// by the caller at generation time.
const Instructions = `You judge whether a writer overuses a word as a reflexive crutch (a "tic") or uses it as a precise term of art that must not be swapped.

You are given a WORD, a LADDER of candidate weaker or more-general replacements, and SAMPLE SENTENCES showing how the writer actually uses the word.

Choose role:
- "term_of_art": the word has a fixed technical referent in these uses (a named API concept, a domain term, or a proper noun — a project, tool, or product name). No ladder word preserves the meaning. demote_to MUST be "none".
- "tic": the word is reached for loosely across these uses; a weaker or more general ladder word would often be truer. demote_to MUST be one ladder word — the best general-direction replacement.
- "mixed": both — a term of art in some uses, loose in others. demote_to may be a ladder word (for the loose uses) or "none".

Rules:
- demote_to may ONLY be "none" or a word from the provided ladder. Never invent a word.
- Judge from the SAMPLE SENTENCES, not from the dictionary. A word can be rare in general English yet a precise term of art here.
- note: one short clause the writer will read — why, and (for tic/mixed) when the weaker word is truer.`

// Payload renders the per-word user message: word, ladder, and sample uses.
func Payload(word string, ladder []string, samples [][]string) string {
	var b strings.Builder
	b.WriteString("WORD: ")
	b.WriteString(word)
	b.WriteString("\n\nLADDER (weakest to strongest; pick one for demote_to, or \"none\"): ")
	b.WriteString(strings.Join(ladder, ", "))
	b.WriteString("\n\nSAMPLE SENTENCES:\n")
	for _, s := range samples {
		b.WriteString("- ")
		b.WriteString(strings.Join(s, " "))
		b.WriteByte('\n')
	}
	return b.String()
}
