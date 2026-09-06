# Feature request: a way to run `writecheck` ad hoc without a real hook payload

*Untracked note, written 2026-09-03 from costrel's session. Commit it,
gitignore it, or delete it — nothing depends on it.*

## What happened

Tried to run a draft README through `basanite writecheck` outside a real
Claude Code `PreToolUse` call — hand-building the JSON payload and piping it
in on stdin, the way you'd smoke-test any hook consumer. First attempt used
a payload with `tool_input.file_path` and `tool_input.content` but no
`session_id` field. `writecheck` exited 0 with empty stdout.

That's indistinguishable from "no tics found." `runWritecheck` (`cmd/basanite/main.go:1058`)
decodes stdin into `writecheckInput` and bails silently — same code path,
same empty output — on a JSON parse failure, an invalid `session_id`
(`validSessionID`, line 1470, requires `^[A-Za-z0-9_-]{1,128}$`), a missing
or stale `report.json` (`max-age` default 7 days), or an empty extracted
text field. Four different "can't actually check this" reasons collapse
into the same silence as "checked it, found nothing."

Confirmed it wasn't real by running a positive control first — same
payload shape, content deliberately containing `load-bearing` and
`substrate` (already known-flagged in the live report). Same silent
nothing. Only after reading `runWritecheck`'s source to find the missing
`session_id` requirement did the positive control actually fire.

## Why this is a real gap, not user error

`writecheck`'s fail-open design is correct for its actual job — a hook
standing in front of every write has no business failing loudly, and the
doc comment says so directly (`main.go:979`). The gap is that there's no
second entry point for the case this note hit: a human (or another tool)
wanting to check a piece of text against the same report and swap table,
outside the exact hook wire format and without an existing fresh
`report.json` already on disk from a real session. `cope-gate -check <file>`
(or `-` for stdin) is exactly this shape for cope's own card-scoring — a
direct, ad hoc, one-shot check that doesn't require reconstructing a fake
hook event.

## Suggested fix

A `basanite check <file>` (or `-` for stdin) that runs the same
`display.FromReportForDetection` + `swaps.Apply` pipeline `writecheck`
uses, but:

- takes text directly, no `writecheckInput` JSON envelope, no `session_id`
- skips the per-session dedup entirely (equivalent to `writecheck`'s
  existing `-no-dedup`, unconditionally)
- if the report is missing or stale, says so on stderr and exits nonzero,
  rather than exiting 0 with nothing — the ad hoc case has no reason to
  fail as silently as the hook does, since there's no tool call it could
  block by complaining

This is close to what `writecheck -no-dedup` already does internally; the
gap is entirely in the input contract, not the detection logic.

## Confirmed independently, 2026-09-06

Asked (from a session on afferent, github.com/justinstimatze/afferent) to run
that project's 5 tracked `.md` files through both cope and basanite. cope-gate
has exactly this shape already (`-check <file>`, or `-` for stdin) and it
worked immediately. Reached for the basanite equivalent and hit this same gap
from scratch, independently of this note — a second, unrelated project asking
for the identical thing three days later is real signal that this is a
recurring need, not a one-off from costrel's session.

Also: the installed binary (`~/Documents/basanite/basanite`, built 2026-06-10)
predates `writecheck` entirely (`unknown command "writecheck"`) — main.go
gained it by 2026-08-28. So on top of the gap this note describes, anyone
who reaches for `writecheck` right now via the binary on PATH gets a flatly
wrong error, not even the silent-drop this note is about, until the binary
is rebuilt.
