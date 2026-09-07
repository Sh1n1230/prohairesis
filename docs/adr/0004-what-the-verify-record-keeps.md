# ADR 0004 — What verification runs, and what its record keeps

[日本語版](../ja/0004-what-the-verify-record-keeps.ja.md)

**Status:** accepted
**Date:** 2026-09-03
**Amends:** the `harness.verify.v1` field list and the P3 scope in
`docs/DESIGN.ja.md` §10.5 and §10.7

## Context

P3 adds L6: discover the checks a repository already declares, run them,
normalize what they said into one shape, and hand an agent a fingerprint stable
enough that "this is the third time" will be answerable in P4a.

Building it forced five decisions the design document either left open or
answered differently. They are collected in one ADR because they share a premise
and were made together:

> A verification layer may run **only** what the repository already declared, and
> may keep **only** what a later reader needs in order to recognize the same
> failure again.

The first half bounds what this program is allowed to cause. The second half is
ADR 0002's decision about command lines, arriving at the same door from the other
side: tool output is arbitrary program output, and a durable copy of it is a new
place for secrets to collect.

**Principle this answers to:** YAGNI and KISS, applied to the environment rather
than to the repository — the surface an agent has to learn is one command and one
output shape, and the surface this program is allowed to execute is a closed list.

## Decision

### 1. Discovery is a ladder, and only the most specific tier runs

The design named four discovery sources (`run_quality_checks.sh`, Makefile,
`package.json`, cargo) without saying what happens when a repository has several.
The answer is a ladder, not a union:

1. an aggregate quality script (`scripts/run_quality_checks.sh`, `run_quality_checks.sh`)
2. a Makefile's checking targets
3. language manifests — `package.json` scripts, `Cargo.toml`, `go.mod`

The first tier that matches wins. Only the manifest tier collects several sources
at once, because a polyglot repository's `Cargo.toml` genuinely does not claim to
cover its `package.json`.

A union would run the same tools twice in the common case — an aggregate script
whose whole job is to call the Makefile — and report every failure twice. That
does not merely produce noise: it makes the fingerprint a function of how many
ways a project happens to declare the same check, and "the same failure three
times" stops being a countable thing. Recurrence detection is what P3 exists to
enable, so the shape that breaks it is not an option.

`go.mod` was added to the design's list. It is the same tier as `Cargo.toml` —
a manifest whose toolchain declares its own verbs — and its absence would have
meant the tool could not check the repository it is developed in.

### 2. The set of runnable target names is closed

`check`, `verify`, `lint`, `typecheck`, `test`. Nothing else, from any tier.

Discovery decides what this program will execute in someone's working tree. A
repository with a `deploy` target next to its `test` target is not unusual, and a
verification layer that finds `deploy` interesting has become a thing that causes
irreversible effects while claiming to observe. The list is short, boring, and
extended only by an ADR.

### 3. prohairesis emits one severity, and the ladder still exists

Every finding this program derives is `HIGH`. The schema keeps
`CRITICAL/HIGH/MEDIUM/LOW`, and the scoring keeps security-checker's deduction
table unchanged.

The single severity is the honest answer, not a shortcut: this layer knows that
the repository declared a check and that the check did not pass. It does not know
whether an unused import is worse than a failing test, and any mapping it invented
would be this layer holding a definition of correctness — the one thing §5 says
L6 must not hold.

The rest of the ladder exists because a source that *does* grade its own findings
writes into this same shape. Folding a scanner's `CRITICAL` into `HIGH` would
discard a judgement somebody actually made.

The consequence is that `total_score` is coarse when prohairesis is the only
source: one finding is 90, four or more is the per-category cap of 40. That is
accepted. The score is a summary for a human reading a terminal; `Failed()` — any
finding at all — is what the exit code is derived from, and it is deliberately not
a threshold.

### 4. The stored record keeps the fingerprint and the location, never the message

A run inside a session is appended to `~/.prohairesis/sessions/<id>/verify.jsonl`
with every finding's `message` removed and `redacted: true` set. The `--json`
output an agent reads is complete; the durable copy is not.

This is ADR 0002's decision about `argv`, made again for the same reason and with
the same evidence. The first real run of this code, against `~/signate/_template`,
returned findings whose message text was **lines of source code containing the
word `token`** — because the check that produced them is a secret scanner. A
secret scanner's findings are, by construction, the most sensitive strings in the
repository. Writing them to a durable unencrypted file that outlives the session
and that the agent can read would make the verification layer the best place on
the machine to look for credentials.

Redaction of the message text was rejected for the same reason ADR 0002 rejected
it for command lines: a regular expression deciding which parts of arbitrary
output are secret is the mechanism this project measured and is replacing, and its
misses are silent.

What survives is what recurrence needs: category, severity, location, and the
fingerprint, which was computed from the message text before it was dropped. The
consequence is stated plainly rather than hidden — **the fingerprint in a stored
record cannot be recomputed from that record.** It is an identity, not a digest of
what sits beside it. A test asserts that it cannot be rehashed, so that the next
reader does not "fix" it.

### 5. The record is written before the phase that reads it

Nothing reads `verify.jsonl` yet. P4a will.

This sits uncomfortably against YAGNI, so the reasoning is written down rather
than assumed. A fingerprint with no history to compare against is decoration: the
question it answers — *is this the failure from last time?* — has no referent
until a second run exists to ask it of. Writing the record now is what lets P4a be
built against real sessions instead of against fixtures, and the alternative is to
ship a P3 whose central deliverable cannot be exercised.

What was *not* done is the part YAGNI actually forbids: there is no per-category
fingerprint, no attempt field, no `last_green` pointer, and no reader. Those
belong to the phase that has evidence about what shape they need.

### 6. Injected context is one line, and the budget is enforced by the code

`SessionStart` emits a single line naming `verify` and `undo`, under a hard
200-byte ceiling enforced inside the function that produces it.

200 bytes is the design's "under 50 tokens" (§6.6) expressed in a unit this
program can count without shipping a tokenizer, at the conservative English
approximation of four bytes per token. The measured line is 162 bytes.

Two details are decisions rather than defaults. The line names `verify` only where
discovery found something, because pointing an agent at a command that will answer
"nothing is declared here" spends context to buy a wasted tool call. And it is
emitted as the runtime's named context field rather than as bare text on stdout,
so that a diagnostic printed there by accident in some future change cannot become
an instruction to the agent.

A test asserts the line carries no strategy words. §5 forbids L7 from telling an
agent what to try; the injection path is where that rule is easiest to break by
accident, and it is one line away from being broken by someone with good
intentions.

## Consequences

**Good.**
- One output shape for every check, so an agent learns one thing.
- The layer executes a closed list of conventional check targets and nothing else.
- The verification record cannot become the place secrets accumulate, and that is
  fixed by a test rather than by care.
- A fingerprint survives clocks, pids, temporary directories and durations, which
  is the precondition for everything P4a does.

**Bad, and accepted.**
- **The ladder can pick the wrong tier.** A repository whose `Makefile` has a
  `test` target *and* a `package.json` with a different `test` script will only
  have the Makefile run. This is visible — `source` names the file that declared
  each check — but it is a real limitation, not a subtlety.
- **A stored record cannot be re-verified.** Its fingerprint is unattributable to
  anything the record contains.
- **The score is coarse.** With prohairesis as the only source, `total_score`
  takes few distinct values, and rank A can coexist with a failing run.
- **Scrubbing can merge two failures** that differ only in a number, since
  durations and hex identifiers are erased. The direction of the error was chosen
  deliberately: over-merging under-counts recurrence, while under-merging would
  make recurrence undetectable. Anything built on a count must present it as
  evidence and never act on it automatically.
- **`verify` runs commands the repository declared.** In a repository you have not
  read, `prohairesis verify` executes its Makefile or its scripts. This grants an
  *agent* nothing it did not have — it already has a shell — but it is a genuine
  consideration for a human running it in a fresh clone, and
  `docs/ENFORCEMENT-HONESTY.md` says so.

## Verification

- `tests/scenarios/p3-verify.sh` — a `deploy` target is never run; the same
  failure twice keeps one fingerprint through real subprocess output containing a
  clock reading and a pid; a different failure gets a different one; an absent
  tool is a skip and exit 0; a repository declaring nothing is not a failure; the
  repository's files and git surface are unchanged; the injected notice is within
  budget and stays quiet where there is nothing to verify; the stored record
  carries `redacted` and none of the finding text.
- `internal/verify/run_test.go` — a finding containing a secret does not survive
  into the stored record.
- `internal/verify/schema_test.go` — the golden records round-trip, their scores
  are the ones the code computes, and a redacted record cannot be rehashed.
- `internal/verify/discover_test.go` — the ladder, the closed target list, and the
  lockfile choosing the package manager.
- `internal/notice/notice_test.go` — the budget, one line, and no strategy.
