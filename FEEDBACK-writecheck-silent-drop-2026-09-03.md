# `writecheck` drops a payload silently, and a broken call looks exactly like a clean one

Found while sweeping another project's 107 tracked `.md` files through basanite from a
script rather than from the hook.

## What happened

I fed `writecheck` a synthetic PreToolUse event per file:

```json
{"tool_name":"Write","tool_input":{"file_path":"docs/CONCEPT.md","content":"…"}}
```

Every one of the 107 came back with no output and exit 0. I read that as 107 clean files
and reported it as such. It was wrong: the payload has no `session_id`, and
`runWritecheck` returns `nil` at `cmd/basanite/main.go:1058` when `validSessionID` fails.
Adding a `session_id` to the same payload flagged 10 files immediately — `substrate` ×6
in one of them.

The tell that convinced me it was my invocation rather than genuinely clean prose: a
control string stuffed with five known tics (`substrate`, `load-bearing`, "worth
noting", "the real question is", `round`) also produced nothing.

## Why it is worth a fix rather than a note

Three separate paths in `runWritecheck` return `nil` with no output, and a caller cannot
tell them apart from "no tics found":

- `main.go:1058` — undecodable JSON, or a `session_id` that fails `validSessionID`
- `main.go:1074` — no report, unreadable report, or a report older than `-max-age`
- `main.go:1078` — a report that yields no detection swaps

Silence is the correct behaviour for all three *as a hook* — a PreToolUse hook must not
chatter, and the stdout JSON contract has no room for a diagnostic. The problem is only
that the same silence is the tool's entire answer when it is invoked any other way, and
the report-staleness path in particular will hit real users: a report goes stale at seven
days, and from then on `writecheck` says nothing about anything, indistinguishably from a
clean session.

## Suggested fix

One line to stderr on each drop path. Claude Code reads a hook's stdout for the JSON and
leaves stderr alone, so it costs the hook contract nothing and makes every non-hook
caller correct:

```go
if json.NewDecoder(os.Stdin).Decode(&in) != nil || !validSessionID(in.SessionID) {
    fmt.Fprintln(os.Stderr, "basanite writecheck: no usable session_id — nothing checked")
    return nil
}
```

The staleness path is the one I would most want to say so out loud, since it is the one
that degrades a working install rather than a hand-rolled call:

```
basanite writecheck: report is 9d old (max-age 7d) — nothing checked; run `basanite refresh`
```

An alternative that leaves the hook path untouched entirely is a `-explain` flag that
turns the drops into stderr lines, defaulting off. That is strictly safer and strictly
less useful, because the person who needs the message is the one who does not know to
ask for it.

## Smaller thing, same shape

`basanite writecheck -h` prints nothing at all (`fs.SetOutput(io.Discard)` at
`main.go:1045`), so there is no way to discover `-no-dedup`, `-min`, `-max-age` or
`-report` from the CLI. The top-level `basanite --help` says "run 'basanite <command> -h'
for command flags", which is the one thing that does not work. Discarding parse *errors*
in a hook makes sense; discarding `-h` seems to be collateral.

## Context

basanite is otherwise doing exactly what it should here — the `substrate` finding was
real and six of the seventeen occurrences were genuine reflexive use, which I would not
have caught by reading. This is a report about the failure mode being quiet, not about
the detection.

Reported from a session on ettle (github.com/justinstimatze/ettle), 2026-09-03.
