# Contributing

Japanese: [docs/ja/CONTRIBUTING.ja.md](docs/ja/CONTRIBUTING.ja.md)

## Getting the source

```bash
git clone https://github.com/Sh1n1230/prohairesis.git prohairesis/main
cd prohairesis/main
go build -o bin/prohairesis ./cmd/prohairesis
bash tests/run.sh
```

The nested path is not a typo. See [Parallel work](#parallel-work-with-git-worktree).

If you only want to *use* prohairesis, you do not need any of this — see the
install instructions in [README.md](README.md).

## Branches

`main` is the only permanent branch and is always in a working state. **Direct
pushes to `main` are rejected.** Every change arrives through a pull request, and
CI must be green before it can merge.

Work happens on short-lived topic branches, deleted after merge.

```
<type>/<what-it-does>
<type>/#<issue>-<what-it-does>
```

| Prefix | For | Example |
| --- | --- | --- |
| `feat/` | new capability | `feat/event-ledger` |
| `fix/` | a defect | `fix/restore-empty-dirs` |
| `refactor/` | structure, no behaviour change | `refactor/policy-evaluator` |
| `chore/` | config, dependencies, tooling | `chore/update-actions` |
| `docs/` | documentation only | `docs/adapter-interface` |

## Parallel work with `git worktree`

A worktree gives a branch its own directory, so you can leave work in progress
untouched while doing something else. That matters here more than in most
projects: this codebase's tests destroy and restore working trees, and a stashed
half-finished change is exactly the thing you do not want in scope when that runs.

Recommended layout — the clone lives in `main/`, and every branch is a sibling:

```
prohairesis/
├── main/          the repository, on main
├── feat-ledger/   worktree for feat/event-ledger
└── fix-restore/   worktree for fix/restore-empty-dirs
```

```bash
cd prohairesis/main
git worktree add ../feat-ledger -b feat/event-ledger
cd ../feat-ledger
# ... work, commit, push, open a PR ...

# after the PR merges
cd ../main
git worktree remove ../feat-ledger
git branch -d feat/event-ledger
```

`git worktree list` shows what you have open.

## What CI requires

Every check below must pass before a pull request can merge. There are no human
approvals in the gate: with a single maintainer, an approval requirement is
either a deadlock or a formality, so the gate is machine-checked instead.

| Check | Command |
| --- | --- |
| Formatting | `gofmt -l .` reports nothing |
| Lint | `golangci-lint run` |
| Types and suspicious constructs | `go vet ./...` |
| Unit tests | `go test ./...` |
| Build | `go build ./...`, plus every release target |
| Scenario tests | `tests/scenarios/*.sh` |
| Published corpus is clean | `tools/sanitize-corpus.py` finds no identifying tokens |

Run the lot locally with:

```bash
bash tests/run.sh
```

### The corpus check is not optional

`tests/golden/false-block-corpus.jsonl` is derived from real agent sessions. The
published copy is sanitized; the raw copy never leaves the machine it came from.
CI re-runs the sanitizer's verification so that a future regeneration cannot
publish personal data by accident. If that check fails, do not weaken it.

## Writing changes

- **Design decisions go in `docs/adr/`.** If a change contradicts something in
  `docs/DESIGN.ja.md`, add an ADR; do not quietly edit the design document to
  match the code.
- **`docs/ENFORCEMENT-HONESTY.md` constrains the code, not the other way round.**
  If a change makes a claim there false, either the change is wrong or that
  document must be corrected in the same commit, with the weakened guarantee
  stated plainly.
- **Every ADR states which principle its decision answers to.** One line is
  enough. It is there because the ADRs are what a future reader argues with, and
  a decision whose reasoning is not written down gets re-made by whoever inherits
  it.
- **Tests assert invariants, not implementations.** The central one — that an
  active session leaves the user's git surface byte-identical — is asserted in
  `tests/scenarios/p1-destroy-and-restore.sh`. Do not relax it.
- Comments explain *why*. The code already says what.

## Three principles, applied twice

**YAGNI. KISS. DRY.** They apply to this repository, and they apply to the
environment this repository hands an agent. The second half is the part worth
saying out loud, because it is easy to write a small clean tool that puts a large
complicated thing in front of somebody else.

`docs/DESIGN.ja.md` §5 already states these as a list of what each layer must not
hold. That list is the specific form; this is the general one.

- **YAGNI.** No field, command, or abstraction for a phase that has not arrived.
  A schema field waiting to be filled is an instruction to whoever arrives next,
  and the instruction is usually "make this layer do more than it should".
  The event schema has no place to record a verdict for exactly this reason —
  see [ADR 0002](docs/adr/0002-what-the-event-log-keeps.md).
- **KISS.** In the environment, this means the surface an agent has to know about
  stays small. Every command an agent must learn is a tax on the context window
  and a thing that can be misunderstood. P2 adds none: `report`, `metrics` and
  `hooks` are for people.
- **DRY.** One fact, one home. `meta.json` is the record of what a checkpoint is;
  the event log points at it and does not restate it. The boundary between
  "before this project was watching" and "since" is derived from the earliest
  event rather than stored, because a stored install date is a second copy of a
  fact that is free to disagree with the first.

A principle is not a licence to collapse things that only look alike. `report` and
`metrics` stay separate commands because they have different inputs and different
readers; the adapter package stays a package because without it the canonical
action vocabulary is decorative. When a principle and a reason point in opposite
directions, write the reason down in the ADR.

## Language

Code, comments, and the primary documentation are in English. Japanese versions
live in `docs/ja/`. `docs/DESIGN.ja.md` is the design record and is maintained in
Japanese only; it is a development document rather than user-facing material.
