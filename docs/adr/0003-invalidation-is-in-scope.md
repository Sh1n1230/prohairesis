# ADR 0003 — A belief that went stale is in scope, not only one that was lost

[日本語版](../ja/0003-invalidation-is-in-scope.ja.md)

**Status:** accepted
**Date:** 2026-08-30
**Amends:** the L7 scope test in `docs/DESIGN.ja.md` §6.0

**Principle this answers to:** the corollary in `docs/THESIS.md` §4 — controlling the
environment is not only restriction. Telling an agent "remember that a file may have
changed under you" is agent control: probabilistic, context-consuming, erased by
compaction. Making the change observable is environment control.

## Context

§6.0 fixes the boundary of the meta-state layer with one question, and closes it hard:

> *What does the agent already know at time T that it has lost by time T+n?*
> **That set is L7's scope, and nothing else is.**

The closure is deliberate. It exists to stop the layer drifting from *externalization*
into *augmentation* — from putting back what the agent had into supplying what it
never had. "It would be useful to know" is explicitly ruled out as a criterion.

A failure mode was then observed that the question does not cover. When a human or a
second agent works in the same directory concurrently, the agent behaves erratically:
it read a file once and has no notion that the file can change underneath it. Nothing
was lost. The agent holds a belief it acquired legitimately, and the belief became
false.

The layer as designed cannot see this. §6.3 scopes topology to what *this session*
touched, which presumes a workspace only this session perturbs. Concurrency is handled
one level down — `internal/session/lock.go` guards the "which session is current"
decision precisely because two hooks can race — but not at the level of what the agent
believes about a file.

The obvious repair, re-reading the workspace before each action, is rejected without
further argument: it is a latency and context tax on every call, and §5 already forbids
L7 from active scanning.

## Decision

**The scope test is extended to cover invalidation as well as loss.**

> *What does the agent know at time T that, by time T+n, is either lost or no longer
> true?*

**The externalization/augmentation boundary does not move.** Invalidation is not "useful
to know". The fact being externalized was established by the agent's own earlier read;
the harness returns a verdict on a belief the agent already formed, and nothing more.
What the harness must not do is say what the change means or what to do about it — that
is strategy, and §6.0's non-goals are unchanged.

Four constraints keep the extension mechanical rather than open-ended:

1. **A belief exists only where the ledger records a read.** `action.paths` is the
   operational definition. This keeps the widened test as sharp as the original: there
   is no arguing about what the agent "really believed".
2. **The check is a join over data that already exists.** Every event carries a
   `checkpoint_ref` — "written on every event, not only on the ones that took a
   checkpoint" — so the content at read time is already reconstructable. Cost is
   proportional to the session's read set, not to the repository. Nothing new is stored,
   and no cache is kept: §6.3's rule against a second source of truth applies here with
   particular force, because a feature that detects stale information is the worst
   possible place to introduce stale information.
3. **Attribution stops at `unattributed`.** What is derivable is
   `changed ∧ ¬(this session's writes)`. Another harnessed session is distinguishable;
   an editor, a build, or a person is not. The event schema already settled this posture
   for `kind: "opaque"` — a first-class answer, because collapsing an unmappable case
   into a real one manufactures the illusion of complete coverage. "A human edited this"
   must not be written.
4. **It reports; it never blocks.** No lock, no refusal to write a stale file, no daemon.
   L5's rule stands unchanged.

The result surfaces as a second structured signal beside `failure_recurrence`, fired at
the attempt boundaries §6.2 already derives, carrying facts and an enumeration of
existing affordances only.

## Consequences

**Good.**

- A failure mode the layer would otherwise ignore by construction becomes visible, and
  it is one users hit whenever more than one worker shares a directory.
- The mechanism is a projection over P1 and P2 output. It adds no store, no watcher, and
  no new record — which is the same evidence §6.0 offers that this layer is correctly
  designed: an implementation that stays small because it contains no inference.
- It removes a human decision point rather than adding one. Today this is handled by the
  human interrupting to say "I changed that file, re-read it", which is counted in
  `interrupts_per_session`. This is one of the few features that can be justified against
  the friction budget by lowering the headline number.

**Bad, and accepted.**

- **The read set is not complete.** `truncated` records that an event exceeded what a
  single `write(2)` could carry and dropped its paths. The feature therefore cannot claim
  that no change is missed — only that a miss is visible in the record. Whether the miss
  rate is low enough for the signal to be worth having is measured at A1 before it ships;
  if it is not, this is theater in the sense of §12's fourth counter-argument and should
  not ship at all.
- **Attribution is coarse.** A user who wants to know *who* changed the file will not get
  an answer here.
- **The scope test is harder to apply than it was.** "Lost" was a sharp question; "no
  longer true" invites argument about what counts as a belief. Constraint 1 is the whole
  of the defence, and it must not be relaxed: no ledger read, no belief.

## Verification

**None of these exist yet. This ADR precedes phase A1**, in the same way
`ENFORCEMENT-HONESTY.md` precedes the enforcement code. They are the conditions under
which A1 may close:

- A scenario test in which a second writer changes a file between the read and the next
  attempt boundary: the signal fires, and `changed_by` claims nothing beyond
  `unattributed`.
- `tests/golden/signal_has_no_strategy` (§13, item 4) extended to `stale_read`: every
  field is inside the closed schema set and no field carries strategy wording.
- The signal path asserted non-blocking under the conditions already used for the hook in
  `tests/scenarios/p2-hook-never-blocks.sh`.
- A measured miss rate for the read set under `truncated`, recorded rather than budgeted,
  as ADR 0002 requires of any figure this project prints.
