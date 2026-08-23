# ADR 0001 — Checkpoints live in an object store outside the repository

[日本語版](../ja/0001-checkpoint-store-location.ja.md)

**Status:** accepted
**Date:** 2026-08-23
**Supersedes:** the "git shadow ref" approach named in the design plan

## Context

The reversibility layer must be able to restore a working tree — including files git
does not track — without changing anything the user or the agent can observe.

The invariant, stated as a test rather than an intention:

> With prohairesis active, `git log --all`, `git status`, `git stash list`, `git branch -a`,
> the reflog, `HEAD`, and `for-each-ref` must produce byte-identical output to a control
> repository without it.

The plan proposed writing checkpoint commits to `refs/harness/sessions/<id>/<seq>` inside
the user's repository, and flagged as an explicit uncertainty whether that would stay
invisible in practice.

## Decision

**It does not stay invisible. The in-repo shadow-ref design is rejected.**

`git log --all` globs `refs/*`, not `refs/heads` plus `refs/remotes` plus `refs/tags`.
A checkpoint ref under `refs/harness/` appears in `git log --all` output, and in
`for-each-ref`. That is a visible change to the user's repository, and it fails the
invariant on the first checkpoint. Measured in `tests/scenarios/spike-shadow-ref.sh`,
round 1.

Instead: **each session gets a bare object store outside the repository**, at
`~/.prohairesis/sessions/<session-id>/store.git`, with the repository's own object database
borrowed read-only through `objects/info/alternates`.

```
GIT_DIR=<store> GIT_WORK_TREE=<repo> GIT_INDEX_FILE=<store>/prohairesis-index git add -A
tree=$(GIT_DIR=<store> ... git write-tree)
commit=$(GIT_DIR=<store> git commit-tree $tree -m ...)
GIT_DIR=<store> git update-ref refs/checkpoints/<seq> $commit
```

Nothing is written inside `.git`. No ref, no index, no reflog entry, no config. The
user's index is never read and never written, because a throwaway index file is used.

## Consequences

**Good.**
- The invariant holds. Verified across the full observable surface in round 2 of the spike,
  including a destructive scenario (`rm -rf src`, clobbered `.gitignore`) followed by restore.
- Unchanged blobs are borrowed via alternates rather than copied, so a checkpoint costs
  roughly one `git add` worth of work, not a tree copy.
- `git gc --prune=now` in the user's repository does not destroy checkpoints: objects the
  harness writes live in the store, and the borrowed ones are still reachable from the
  user's own history.
- Uninstalling is trivial and complete — delete a directory outside the repository. There
  is nothing to clean up inside `.git`, so uninstall cannot corrupt a repository.

**Bad, and accepted.**
- Alternates create a dependency on the repository's object database. If the user rewrites
  history *and* garbage-collects, a checkpoint can lose a borrowed object. Restore must
  therefore verify object availability and fail loudly rather than restoring a partial tree.
  It must never silently produce a half-restored working tree.
- The store must be located by repository identity, not by path, so that moving or renaming
  a repository does not orphan its checkpoints.

**Out of scope for this layer.** Ignored files — the `data/` directories, build outputs,
local environment files — are deliberately absent from the git layer, which honours
`.gitignore` by design. They are covered by the copy-on-write snapshot substrate, and the
large ones are protected by denying writes rather than by copying. See the plan's boundary
classes B2 and B3.

## Verification

`tests/scenarios/spike-shadow-ref.sh` — 12 assertions, all passing. It keeps round 1's
rejected result documented in its header so the in-repo design is not re-proposed later.
