# Thesis

[日本語版](ja/THESIS.ja.md)

> Human does not micromanage the Agent.
> Human designs the environment in which the Agent can act.

This is not a configuration pack for using a coding agent safely. It is a machine-level
substrate that lets an agent hold **maximum agency** by absorbing the dangerous,
irreversible, and unverifiable *effects* of its actions at the system level.

---

## 1. Four words that must not be confused

| Term | Definition | Set by | This project's stance |
|---|---|---|---|
| **Capability** | What the agent can *physically execute* | OS, installed tools, sandbox profile | Set the floor generously. Narrow only at a boundary. |
| **Agency** | What the agent may *decide, choose, and change without asking* | Policy, and the **absence of prompts** | **Maximize.** Every component must justify itself as agency-increasing or agency-neutral. |
| **Autonomy** | How long it runs without human intervention | Emergent (see H1) | Increase it through verification and recovery, never by loosening a boundary. |
| **Harness** | Context, tools, permissions, verification, memory, feedback, rollback, observability | This project | The environment, not the leash. |

## 2. Working hypothesis H1 — *not a proven law*

```
H1:  Autonomy  ≈  Agency × Verification × Persistent State × Recovery
```

The design bets on this. Stated honestly:

- **The claim** is that it is *multiplicative*. If any factor is near zero, autonomy is near
  zero, and raising model capability does not compensate.
- **What falsifies it**: if Persistent State and Recovery are taken from ~0 to 1 and
  `mean_uninterrupted_run` does not materially improve against baseline, H1 is wrong, and
  autonomy was mostly a function of model capability after all. The correct response is to
  cut this project back to its reversibility and audit layers. That is a legitimate
  conclusion, not a failure to be hidden.
- **What does not depend on H1**: reversibility and observability are independently useful
  (you can undo what broke; you can see what happened). The bet is on the *priority order*
  of the later layers, not on the project's existence.

## 3. Two central ideas

### (1) A permission prompt is a pricing mechanism for irreversibility

Agency is rationed today because effects are unrecoverable. Make the effect recoverable and
the price should fall to zero **automatically** — not because a human bravely clicked
"yes, and don't ask again".

> **Design rule: every prompt the human sees is a bug report about the harness.**
> Either the effect should have been contained, or it belongs to the small genuinely
> irreversible outward-facing class — in which case the prompt is correct and should be rare.

### (2) The problem is not absent meta-cognition. It is a weak external substrate.

```
task → attempt → failure → attempt → failure → "I cannot solve this"
```

The agent is not incapable of reasoning about its own process. It knew, at time *T*, what it
had tried, how long it had spent, and which hypotheses it had ruled out. By time *T+n* it has
lost the reference, not the capability.

> **Therefore the thing to build is not a large new AI system.**
> It is a structured, external place to put what existing agents forget.

## 4. Controlling the agent vs. controlling the environment

Not a difference of degree.

| | Controlling the agent (prompts, rules, skills) | Controlling the environment (policy, checkpoints, state) |
|---|---|---|
| Unit | A decision, per tool call | An invariant or affordance, over all calls |
| Timing | Prior — you must predict what is dangerous | Posterior-tolerant — bound it or undo it after |
| Failure mode | **Silent.** Non-compliance looks like compliance | **Loud.** A refused action is an observable event |
| Model dependence | Degrades across model generations | Invariant across models and providers |
| Composition | Rules conflict, accumulate, and rot | Boundaries intersect; monotone and checkable |
| Cost to agency | High and diffuse — narrows safe actions too | Concentrated at the boundary; zero inside it |

Concrete test: *"instruct the agent not to run `rm -rf`"* versus *"the writable set is the
workspace, and its prior state is restorable"*. The first loses to
`python -c "import shutil; shutil.rmtree(...)"`, to a subagent, to a Makefile, and to a model
update that reads the instruction differently. The second does not care how the deletion
happened.

> **Design rule: anything expressible as an environmental invariant or affordance must never
> be written as a prompt instruction.**

**Important corollary.** Controlling the environment does not only mean restricting it.
Memory, time, state, and topology are *affordances the environment provides*. Telling an
agent "remember what you tried" is agent control: probabilistic, context-consuming, erased by
compaction. Making what it tried exist in a queryable place is environment control:
deterministic, free of context, and it survives compaction. **The meta-state layer increases
agency; it does not restrict it.**

## 5. If the harness damages agency, that is a design failure

Held as an operational commitment, not a slogan:

1. **Default-non-blocking.** No component denies by default. Denial requires membership in a
   named boundary class. Everything else at most *records*.
2. **Friction budget.** Every component declares its friction cost. A component whose measured
   friction exceeds its justification is **removed, not tuned**.
3. **Regression gate.** If human interrupts or injected context go up without a corresponding
   recovery event, that is a regression in this project and it fails `prohairesis doctor`.

The natural drift of a project like this is to become a very sophisticated way of stopping an
agent from doing things. Measurement is the only thing that prevents that drift, which is why
it ships early rather than last.

## 6. Can we grant large agency without trusting the AI?

Partially — and the partition is sharp.

**Yes, without trust**, for effects whose entire footprint is inside a boundary you control
and can restore: workspace writes, process spawning, local state, git operations that stay
local. Safety there is a property of the substrate, so it holds even against a maximally
adversarial agent.

**No, not without trust**, for three residual classes:

1. **Outward-facing effects.** Money, a used secret, a sent message, a published artifact, a
   mutated production system. No local substrate can un-send. The only trust-free moves are
   to withhold the capability, to **convert the action into a reversible surrogate**
   (a branch instead of a shared ref, a draft instead of a send, a plan instead of an apply,
   a test key instead of a live one), or to gate on a human. There is no fourth option.
2. **Exfiltration.** Reading a secret and encoding it into outbound traffic is neither
   reversible nor detectable by a snapshot. It is solved at the network and secret-masking
   layers or not at all.
3. **This project's own control plane.** An in-process hook cannot constrain a process that
   runs arbitrary shell. Only the kernel layer survives adversarial intent.

The contained-and-reversible class is where the overwhelming majority of agent actions live,
and it generates essentially all of today's permission prompts. **Removing the prompts from
that class is the entire product.**

---

See [`ENFORCEMENT-HONESTY.md`](ENFORCEMENT-HONESTY.md) for what is actually enforced, and by
what.
