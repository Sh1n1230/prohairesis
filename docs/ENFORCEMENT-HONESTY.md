# Enforcement Honesty

[日本語版](ja/ENFORCEMENT-HONESTY.ja.md)

This document exists **before** any enforcement code in this repository. That ordering is
deliberate: it is written to constrain the code, not to excuse it.

If a future change makes a claim in this document false, the change is wrong — or this
document must be updated in the same commit, with the weakened guarantee stated plainly.

---

## The one-sentence guarantee

> For an agent that is **confused rather than adversarial**, every effect inside your
> workspace is recoverable, every effect is recorded, and the history of what was tried and
> what was ruled out survives compaction and session boundaries. Effects outside your
> workspace are bounded by a kernel sandbox where one is configured. **Nothing here defends
> against an agent that is actively trying to escape.**

That guarantee is exactly what is needed to justify turning permission prompts off, because
the failure mode of a modern coding agent is overwhelmingly *error*, not *malice*.

It is **not** the guarantee needed to run an untrusted agent, on an untrusted repository,
with live production secrets. For that, use a container or a VM. This project will say so
rather than pretend otherwise.

---

## What is kernel-enforced

Survives an adversarial agent, because the agent's compliance is not an input.

| Mechanism | What it actually enforces |
|---|---|
| Claude Code native sandbox (macOS seatbelt) | Filesystem read/write scope for the agent's process tree |
| `sandbox.network.strictAllowlist` | Network egress destinations |
| `sandbox.credentials` (env var deny/mask, file masking) | Secret material never materializing in the process |
| `managed-settings.json` ownership (`root:wheel`) | The policy ceiling itself, because the agent does not run as root |

## What is advisory only

A speed bump against **error and drift**. Not against **intent**.

**Everything at L2 (the `PreToolUse` evaluator).** The hook is invoked in-process by the
agent's own runtime, and the agent runs arbitrary shell. Any policy that matches on command
strings is defeated by, at minimum:

```
X=rm; $X -rf target/            # variable indirection
python - <<'PY' ... PY          # here-doc into an interpreter
env -i sh -c '...'              # environment reset
make clean                      # indirection through a build file
eval "$(printf '\x72\x6d ...')" # encoding
```

…plus subagents and MCP servers whose transport never reaches `Bash` at all.

**Do not model L2 as security.** Model it as two things:

1. the component that catches an agent making an ordinary mistake, and
2. the component that produces the event log.

Every decision emitted by this system carries an `enforcement` field with one of
`kernel` / `advisory` / `record-only`. That field is not documentation — it is the
structural commitment that makes this page checkable rather than aspirational.

Boundary classification of shell actions is likewise heuristic. Extracting the set of
affected paths from a shell string is undecidable in general and always will be. This is
why **reversibility is the primary mechanism and classification is the secondary one**, and
never the reverse.

### Evidence: the anti-pattern this project replaces

The machine this project was designed on already ran a regex denylist hook over `Bash`
command strings. Its measured record over 74 recorded sessions:

- **13 commands blocked. Roughly 11 of them were false positives.**
- It blocked a `cat` of several project files because one was named `vite-env.d.ts`, which
  matched a pattern intended for `.env` files.
- It blocked an append to `.gitignore` whose *purpose* was to start ignoring a secrets file.
- It blocked a `git status` check whose *purpose* was to confirm a secrets file was untracked.
- It blocked reading the hook's own source.
- It blocked the first attempt to write **this document**, because the text contains the
  word "credentials" and a path under `.ssh`.

Meanwhile it does not stop `cat "$HOME"/.env` under several quoting forms.

Simultaneously too tight and too loose is the *characteristic* failure of string-matching
policy, not an implementation defect to be tuned away. It is retained here as a permanent
regression corpus, not as something to fix.

## Verification is a report, never a gate

`prohairesis verify` runs the checks a repository already declared and normalizes what
they said. It has no verdict of its own. It refuses nothing, blocks nothing, and holds no
threshold: its exit code is `1` when any declared check produced a finding, and that is a
statement about your tools' output, not a permission this project granted or withheld.

Two things follow, and both are limitations rather than features.

**It runs commands out of the repository.** Discovery reads `scripts/run_quality_checks.sh`,
a `Makefile`, `package.json`, `Cargo.toml` and `go.mod`, and executes the conventional
checking targets it finds there — with your privileges, in your working tree. In a
repository you have not read, `prohairesis verify` runs code you have not read. This grants
an *agent* nothing it did not already have, since an agent has a shell; it is a real
consideration for a human typing the command in a fresh clone. The set of target names it
will run is closed (`check`, `verify`, `lint`, `typecheck`, `test`) and extending it
requires an ADR, so a `deploy` target is never discovered. That bounds the blast radius. It
does not eliminate it, and there is no trust check in front of it until P5.

**Its findings are approximate.** The fingerprint that identifies a failure is computed
after erasing timestamps, process ids, durations, temporary paths and hex identifiers,
because those are what make one failure look like a new one. That erasure can merge two
failures that differ only in a number. Any count built on it is evidence for a human or an
agent to weigh, and nothing in this project may act on one automatically.

## What is not enforced at all

- Exfiltration through an **allowlisted** destination. An agent may push a secret to a
  permitted GitHub repository, and nothing here stops it.
- Anything performed through an MCP server that does not surface as a `Bash` action.
- Effects of anything that ran before the sandbox was established.
- Effects on any other machine.
- Effects that already left the machine: a sent message, a published package, an executed
  payment, a deleted remote resource. `prohairesis undo` restores a tree. It does not un-send.
- Anything a repository's own declared checks do when `prohairesis verify` runs them. There
  is no sandbox in front of them yet.

## Where real enforcement comes from

In ascending order of strength, and descending order of practicality:

| Mechanism | Strength | Cost | Status |
|---|---|---|---|
| Claude Code native sandbox (seatbelt) | Strong for FS + network, same-user | Low; some builds break under it | Planned |
| Harness-generated `sandbox-exec` profile for other agents | Same, but this project maintains the profile | Medium; profile maintenance is real work | Planned, later |
| A dedicated unix user for the agent | Strong — cannot reach your keychain, your key files, or the ceiling | High: ownership, GUI auth, and dev tooling all break | Documented escape path only |
| Container or VM | Strongest available | Highest, and it defeats the "works in any repo on my machine" premise | **Out of scope. Use one directly if you need it.** |

## The standing rule

Any component of this system may be **removed** for costing more agency than it saves.
No component may be **retained** on the strength of a guarantee it does not provide.
