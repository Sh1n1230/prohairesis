# prohairesis

[日本語版](docs/ja/README.ja.md)

Machine-level infrastructure for running AI agents on your own computer.

Not a configuration pack for using an agent safely. The goal is the opposite of a
leash: give the agent **as much agency as possible**, by making the dangerous,
irreversible and unverifiable *effects* of its actions something the system absorbs.

> Human does not micromanage the Agent.
> Human designs the environment in which the Agent can act.

Start with [`docs/THESIS.md`](docs/THESIS.md). Read
[`docs/ENFORCEMENT-HONESTY.md`](docs/ENFORCEMENT-HONESTY.md) before trusting anything here.

## The name

προαίρεσις — in Aristotle, deliberate choice; in Epictetus, the faculty of choice
that is the one thing genuinely *up to us*, and the discipline of telling that
apart from everything that is not. Which is what this is for: the harness exists
to protect the agent's prohairesis, and the boundary model is that same
distinction, drawn in software.

"Harness" is kept as the category noun throughout the documentation. Prohairesis
is *a* harness; the schemas it emits are namespaced `harness.*` so that another
implementation can adopt them.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/Sh1n1230/prohairesis/main/install.sh | sh
```

The installer verifies the download against the release checksums and refuses to
continue on a mismatch. It never uses sudo and never edits your shell profile.

**Read it before you run it.** That is not a formality: this project argues that
you should not trust what you have not checked, and exempting its own installer
would be incoherent. Every release page also documents manual verification and
signed build provenance.

From source, for development:

```sh
git clone https://github.com/Sh1n1230/prohairesis.git prohairesis/main
cd prohairesis/main && go build -o bin/prohairesis ./cmd/prohairesis
```

## Status

**Works today.** Sessions, checkpoints, `diff`, `undo`, `doctor` — and, once you
register the hooks, **automatic checkpointing and an append-only record of what
the agent did**. This covers what the agent runtime's own rewind does not: files
edited through the shell, and files git ignores but you cannot afford to lose.

Registering the hooks is a separate, explicit step, and starts as a trial in one
repository:

```sh
prohairesis hooks install                  # this repository only
prohairesis hooks install --scope user     # everywhere, once you want it
prohairesis hooks uninstall                # byte-for-byte back to how it was
```

Nothing registered there can refuse a tool call. Every hook exits 0, on every
path, including the ones where it fails — asserted in
`tests/scenarios/p2-hook-never-blocks.sh`, not merely intended.

**Not built yet.** The verification contract, the meta-state layer, the policy
compiler, and the machine ceiling. Those are later phases, in that order.

So: at this stage prohairesis keeps your work recoverable and keeps a record of
what happened. It does not decide anything, and it cannot stop anything.

## Why reversibility first

A permission prompt is a pricing mechanism for irreversibility. Agency is
rationed today because effects cannot be taken back. Make them recoverable and the
price should fall to zero on its own — not because a human clicked
"don't ask again".

So the first thing built is the thing that makes turning prompts off defensible.

## Use

```sh
prohairesis doctor             # what is, and is not, in effect
prohairesis hooks install      # record this repository's sessions, and
                               # checkpoint them without being asked
prohairesis session start      # begin by hand, and capture the tree as it is now
prohairesis protect add data   # declare an ignored path you cannot lose
prohairesis checkpoint --label "before the refactor"
prohairesis diff               # what changed since the last checkpoint
prohairesis undo --dry-run     # what a restore would do
prohairesis undo               # put the tree back
prohairesis session end

prohairesis report             # what happened in one session
prohairesis metrics            # friction now, against the baseline before this
```

A session you start by hand stays open until you end it: an agent restarting is
not a reason to drop the point you wanted to be able to return to. A session the
hook opened for itself closes when the last agent using it goes away.

Exit codes are uniform everywhere: `0` clean, `1` gate failed, `2` error.

## The invariant that matters

With prohairesis active, `git log --all`, `git status`, `git stash list`,
`git branch -a`, the reflog, `HEAD` and `for-each-ref` must produce
**byte-identical** output to a repository it never touched. Nothing is written
inside `.git`. Your index is never read and never written.

A reversibility layer that perturbs your git state has taken away more agency
than it added. This is asserted in `tests/scenarios/p1-destroy-and-restore.sh`,
not merely intended. The first design for it — checkpoint refs under
`refs/harness/` — was rejected for failing that test; see
[`docs/adr/0001-checkpoint-store-location.md`](docs/adr/0001-checkpoint-store-location.md).

What the event log keeps, what it refuses to keep, and why the one performance
number in the design document was removed rather than adjusted:
[`docs/adr/0002-what-the-event-log-keeps.md`](docs/adr/0002-what-the-event-log-keeps.md).

## What it does not do

`prohairesis undo` restores a working tree. It does not un-push, un-send,
un-publish, or un-charge. Effects that already left the machine are a different
class of problem, and this project says so rather than blurring it.

## Measurement

This project can damage the thing it exists to protect, so the defence is
measurement rather than good intentions. A friction baseline is captured from real
sessions **before** anything is installed:

```sh
tools/baseline.sh > baseline.json
```

Once the hooks are recording, `prohairesis metrics` computes the same figures
again and prints them beside that baseline —- both sides from the same code, and
each figure that cannot yet be measured shown as `n/a` with the reason, never as
zero. What each figure can and cannot support is set out in
[`docs/METRICS.md`](docs/METRICS.md).

The first run on the development machine found, among other things, that a
hand-written regex denylist hook had refused 14 commands and **13 of them were
harmless — a 93% false-positive rate**. It blocked an append to `.gitignore`
whose purpose was to start ignoring a secrets file; it blocked a `git status`
check confirming that file was untracked; it blocked reading the hook's own
source. Those commands are frozen, sanitized, as a regression corpus in
`tests/golden/false-block-corpus.jsonl`. Any policy this project ships has to do
better, and has to prove it.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). `main` is protected; work happens on
`type/description` branches and arrives through pull requests with CI green.

```sh
bash tests/run.sh
```

## Design record

[`docs/DESIGN.ja.md`](docs/DESIGN.ja.md) is the design document, maintained in
Japanese. It is kept current; where a decision has been superseded, the ADR that
replaced it is linked inline.

## License

GPL-3.0. Knowledge and implementations of AI-agent safety and autonomy should
accumulate in the open, including in whatever gets built on top of this.
