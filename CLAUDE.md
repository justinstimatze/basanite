# basanite — context for Claude

Orientation for working on this repo. `DESIGN.md` holds the full rationale —
read its "What is deliberately NOT in it" section before adding anything;
the design was trimmed on purpose.

## What this is

A deterministic, local, no-LLM tool that detects vocabulary tics (overused
words like `load-bearing`) by frequency drift over Claude Code JSONL
transcripts, and injects a ranked ladder of weaker/alternative words at turn
start so diction varies. The name is the mineral term for a touchstone: the
stone an assayer streaks a sample against to judge it.

## Conventions

- Go, stdlib only. `gofmt`, `go vet ./...`, `go test ./...` before committing.
- The version string comes from `git describe` via the Makefile — never add
  a hand-maintained version constant.
- Data assets (`data/`) are gitignored; `scripts/fetch-data.sh` fetches them.
  Tests that need them skip cleanly when absent.
- Output tone is awareness, never prohibition — see DESIGN.md on
  ironic-process priming before changing any injection wording.
