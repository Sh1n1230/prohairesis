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
- **Tests assert invariants, not implementations.** The central one — that an
  active session leaves the user's git surface byte-identical — is asserted in
  `tests/scenarios/p1-destroy-and-restore.sh`. Do not relax it.
- Comments explain *why*. The code already says what.

## Language

Code, comments, and the primary documentation are in English. Japanese versions
live in `docs/ja/`. `docs/DESIGN.ja.md` is the design record and is maintained in
Japanese only; it is a development document rather than user-facing material.
