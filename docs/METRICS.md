# What each figure can and cannot support

[日本語版](ja/METRICS.ja.md)

This project asks to be judged by numbers, so the numbers have to be honest about
what they are. Every figure here is a proxy for something not directly
observable. A proxy that is read as the thing itself stops being a measurement
and becomes a target, and the cheapest way to hit a target is to change what you
measure rather than what you do.

This document is the companion to `ENFORCEMENT-HONESTY.md`. That one says what is
actually enforced. This one says what is actually measured.

## Two rules

**A figure that has not been measured is not zero.** Anything without a value
prints as `n/a` with the reason attached. `false_block_rate` is not 0 because
nothing has been wrongly refused — it is `n/a` because nothing refuses anything
yet, and there is no decision available to be wrong. Printing 0 would be the same
species of untruth this project refuses everywhere else, told with a number
instead of a sentence.

**Both sides of a comparison come from the same code.** The pre-install baseline
and the current figure are computed by the same functions over the same inputs,
split by time. A baseline computed one way and a current figure computed another
is not a comparison: the difference between two implementations is
indistinguishable from the effect being measured.

## Where the numbers come from

Two sources, and neither one alone is enough.

**The runtime's transcripts** (`~/.claude/projects/**.jsonl`) are the only place a
*human* is visible. The hooks this project installs see tool calls; they do not
see the person typing. Interruptions and the length of an uninterrupted run can
only be read there.

**The event log** (`~/.prohairesis/sessions/<id>/events.jsonl`) is the only place
this project's own behaviour is visible: what was recorded, what was checkpointed,
what was lost, and how long the hook took.

The boundary between "before" and "since" is **derived from the event log itself**
— the earliest event ever recorded is, by definition, the moment this project
started watching. It is not stored anywhere. A recorded install date would be a
second copy of that fact, free to disagree with it.

## Coverage, and why it is printed every time

Hooks can be registered for one repository or for the machine. In the project
scope, work in any other repository is invisible — and an event log that silently
covers one repository out of ten is worse than no log at all, because its silence
reads as evidence.

So `metrics` prints which repositories its figures cover, and `doctor` reports
which scope is recording. A figure without its coverage is not a smaller truth; it
is a different claim.

The same applies to holes. When an observation cannot be written, the loss is
recorded on a durable path of its own (`~/.prohairesis/telemetry-loss.jsonl`), and
both `doctor` and `metrics` show the count. Gaps in the sequence numbers are
counted when the log is read. A log with unmarked holes would let absence be read
as quiet.

## The figures

### Measured now

| figure | counted as | direction | what it cannot tell you |
|---|---|---|---|
| `interrupts_per_session` | human turns plus explicit tool rejections | down | Why the human stepped in. A question asked out of interest counts the same as one forced by a blocked action. |
| `mean_uninterrupted_run` | wall clock between consecutive human turns | up | Nothing about sessions the human walked away from — those inflate it badly. Read the median. |
| `median_uninterrupted_run` | as above, at the median | up | The shape of the distribution; a bimodal one hides here. |
| `tool_calls_per_human_turn` | ratio of totals | up | Whether the extra calls were useful. More calls per turn is not more progress per turn. |
| `recurrence_rate` | share of failures whose normalized fingerprint already occurred earlier in the same session | down | Whether two failures with different wording were really the same failure. The fingerprint is mechanical — lowercase, strip paths, hex blobs and numbers — with no interpretation, which is what keeps it from quietly becoming a judgement. |

### Recorded, and deliberately not budgeted

`hook time`, p50 and p99, is printed and compared against nothing.

The design document set an acceptance criterion of "p50 < 20 ms". That figure had
no derivation anywhere — it appeared once and was anchored to an estimate used for
a different argument. Rather than replace it with a second unattributable number,
P2 records the distribution and waits. When enough real sessions exist, a
threshold can be derived from them. That is the same sequence P0 followed for
friction, and the reason there is a baseline to compare against at all. See
[ADR 0002](adr/0002-what-the-event-log-keeps.md).

Two things about this number are worth knowing before quoting it:

- **The hook's own figure understates it.** `hook_ms` in the event log is measured
  inside the process, which cannot time the exec that started it. Process start is
  a large share of the cost. `tools/hook-latency.py` measures the whole invocation
  from outside, which is the number that matters.
- **Taking a checkpoint costs more, and is measured separately.** Automatic
  checkpoints run synchronously by design — an effect that lands before its
  checkpoint is an effect outside the only guarantee this project makes — so their
  cost is shown on its own rather than averaged into the common case.

Measured on this machine (macOS, APFS): observation only, p50 23 ms, p99 27 ms;
observation that also takes a checkpoint, p50 58 ms, p99 62 ms.

### Defined, not yet measurable

These are listed by `metrics` rather than omitted. An indicator that quietly
disappears until the phase that fills it is an indicator nobody notices is
missing, and the set of things being measured drifts to whatever happens to be
easy.

| figure | waiting on |
|---|---|
| `false_block_rate` | P5. Nothing refuses anything yet, so there is no decision available to be wrong. |
| `context_tax` | P3/P4. Nothing is injected into the agent's context yet. |
| `resignation_rate` | P3. Telling "gave up" from "correctly concluded it was impossible" needs the verification contract. |
| `strategy_revision_rate` | P4. It measures the effect of the layer that does not exist. |
| `rediscovery_cost` | P4. Knowing something was re-explored needs a record of what had already been explored. |

`resignation_rate` deserves a warning it will still deserve when it exists: it is
an approximation of resignation and cannot, alone, tell a session that gave up
from one that correctly concluded the task was impossible, or one a human simply
ended. It is never to be read alone. See DESIGN §9.1.

## The one figure that decides the project's future

`mean_uninterrupted_run` is the test of the working hypothesis behind the phase
order — that autonomy is roughly the product of agency, verification, persistent
state and recovery. If it has not moved by the end of P4, the hypothesis was
wrong, and the honest response is to shrink this project to its reversibility and
audit layers rather than to look for a kinder metric.

That is written here, in advance, on purpose.

## The baseline, and the port that replaced its implementation

`~/.prohairesis/metrics/baseline.json` was captured by `tools/baseline.sh` (bash
and jq) before this project changed anything on the machine — deliberately, so
that a baseline could not be influenced by the thing it was measuring.

`prohairesis metrics` does not read those numbers. It recomputes both sides, in
Go, from the same transcripts, because a comparison across two implementations
measures the implementations as much as the effect. The bash tool remains as the
P0 artefact and as an independent check.

Run on the same corpus at the same moment, the two agree:

| figure | `tools/baseline.sh` | `prohairesis metrics` |
|---|---|---|
| sessions | 74 | 74 |
| `interrupts_per_session` | 7.9865 | 7.99 |
| `mean_uninterrupted_run` | 2756.4 s | 2.76e+03 s |
| `median_uninterrupted_run` | 291 s | 290 s |
| `tool_calls_per_human_turn` | 13.7718 | 13.8 |
| `recurrence_rate` | 0.1445 | 0.144 |

The median differs by one second, consistent with a one-element difference in the
list of gaps. Nothing else differs at the precision displayed.

Note that these figures are not the ones in the committed `baseline.json`, which
was captured nine hours earlier: the corpus grows while you work, so re-running
the tool never reproduces an older snapshot exactly. That is a property of the
measurement, not a defect in either implementation, and it is the reason the
comparison above was run on both at once.
