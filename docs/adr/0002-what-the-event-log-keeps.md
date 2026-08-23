# ADR 0002 — What the event log keeps, and what it refuses to

[日本語版](../ja/0002-what-the-event-log-keeps.ja.md)

**Status:** accepted
**Date:** 2026-08-23
**Amends:** the `harness.event.v1` field list and the P2 acceptance criterion in
`docs/DESIGN.ja.md` §10.5 and §11

## Context

P2 adds the observability layer: three hooks, an append-only log per session, and
the two commands that read it. Building it forced four decisions the design
document had already answered differently. They are collected in one ADR because
they were made together and share one premise — **a record that keeps more than
it needs, or claims more than it saw, is worse than a smaller honest one** — and
because separating them would leave four documents each too small to argue with.

## Decision

### 1. Command lines are hashed, never stored

The design's `harness.event.v1` had `action{kind, paths, argv, host}`. The shipped
schema has no `argv`. A shell action records the program name and a SHA-256 digest
of the full command line.

Every metric this project defines asks the same question of a command: *is this
the same one as before?* `recurrence_rate` needs identity. `rediscovery_cost`
needs identity. Nothing needs the text. Keeping the text would answer a question
nobody asked, and the answer would accumulate — every token pasted into a shell
command, every connection string, every credential typed once in a hurry — in a
file that is durable, that is not encrypted, and that the agent can read.

Redaction was considered and rejected. A regular expression over command lines,
deciding which parts are secret, is the mechanism this project already measured
and is replacing: on this machine the pre-existing denylist refused 14 commands
and 13 of them were harmless. Reproducing that logic on the recording side would
be worse, because a redaction miss is silent — nobody sees the thing that was not
removed.

### 2. There is no time budget

The design set P2's acceptance at "hook overhead p50 < 20 ms". That figure appears
exactly once in the document and is derived from nothing. The only quantitative
anchor anywhere near it is an estimate — "shell startup 30 ms × 500 calls" — used
to argue for writing the runtime in Go, which it does adequately without becoming
a threshold.

Measured, from outside the process, with the timer outside the measured program:

| invocation | p50 | p90 | p99 |
|---|---|---|---|
| observation only | 23 ms | 24 ms | 27 ms |
| observation that also takes a checkpoint | 58 ms | 60 ms | 62 ms |

Against the P0 baseline of ~103 tool calls per session, an observation-only cost
of 23 ms is about 2.4 s per session, against a median uninterrupted run of 285 s.

The criterion is dropped rather than adjusted. Adjusting it would mean inventing a
second number with no more basis than the first. `metrics` records the
distribution and prints it next to the words *recorded, not budgeted*. When
enough real sessions exist, a threshold can be derived from them — the same
sequence P0 followed for friction, which is why there is a baseline to compare
against at all.

The measurement did earn one change. It showed the hook running `git` eight times
per tool call to answer one question twice; removing the duplicate halved p50 from
42 ms to 23 ms. That is not optimization ahead of need, it is deleting work that
was being done twice.

### 3. `boundary_class` and `decision` are not in v1

The design's event carried `boundary_class` and `decision{outcome, enforcement,
reason}`. Neither exists: no boundary classification and no enforcement will exist
before P5.

They are omitted rather than defined-and-left-null. A field that exists and is
always empty is an invitation, and the invitation is addressed to whoever
implements P5 — *there is a slot here, fill it*. The layer's one rule is that it
records and does not decide, and the surest way to lose that rule is to leave the
shape of a verdict lying inside the record. The schema is versioned; when there is
a decision to record, there can be a v2.

`checkpoint_ref` is in v1, because it is answerable now: it names where an undo
would start from, and it is written on **every** event rather than only on the
ones that took a checkpoint. The question "how do I get back to here" has an
answer at every point in the log, and stating it is cheaper than making the reader
infer it.

### 4. Registration is a separate command, and starts in the project scope

`install.sh` does not touch the settings file. Hooks are registered by
`prohairesis hooks install`, defaulting to the project scope, with `--scope user`
asked for explicitly.

The installer's promise is that it puts a binary somewhere and does nothing else —
no shell profile, no configuration. Rewriting the file that decides how the agent
behaves, as a side effect of installing, would break that promise from the inside.

The project scope is the default because trying this should be reversible: one
repository, a file its owner already reads, undone by deleting it. Its cost is
real — observation stops at that repository's edge, and an event log that silently
covers one repository out of ten is worse than none, because its silence reads as
evidence. That cost is not hidden: `doctor` reports which scope is recording and
`metrics` prints which repositories its figures cover.

## Consequences

**Good.**
- The log cannot become the place secrets accumulate, and that property is fixed by
  a test rather than by care.
- No number in the acceptance criteria is unattributable to a measurement.
- P5 inherits a schema with no empty verdict-shaped hole in it.
- `hooks uninstall` restores the settings file byte for byte, which is what makes
  the trial genuinely free.

**Bad, and accepted.**
- A digest cannot be read back. Debugging "which command was that" from the log
  alone is impossible by construction; the runtime's own transcript remains the
  place to look.
- Without a budget, nothing fails when the hook gets slower. The distribution is
  recorded on every event, so the regression is visible — but only to someone
  looking.
- The project scope produces partial coverage by default, and partial coverage is
  the failure mode most likely to mislead. Everything that reads the log is
  therefore required to state its own coverage.

## Verification

- `tests/scenarios/p2-hook-never-blocks.sh` — the hook exits 0 with the state
  directory deleted, unwritable, replaced by a file, handed garbage, handed a
  200 KB payload, or run outside a repository; and a lost observation reaches a
  durable path that does not share a failure mode with the one that just failed.
- `internal/adapter/claudecode/payload_test.go` — a command line does not survive
  into an event, and identical commands still produce identical digests.
- `internal/adapter/claudecode/settings_test.go` — install then uninstall returns
  a realistic settings file byte for byte, including a hook belonging to someone
  else.
- `internal/event/schema_test.go` — the golden records round-trip through the Go
  types, and the schema has no field named `boundary_class`, `decision`,
  `enforcement` or `allowed`.
- `tools/hook-latency.py` — the figures above.
