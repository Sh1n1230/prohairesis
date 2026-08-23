#!/usr/bin/env bash
# SPIKE (plan-flagged uncertainty): can we checkpoint a working tree without
# perturbing anything the user or the agent can observe through git?
#
# The invariant under test:
#   With prohairesis active, `git log --all`, `git status`, `git stash list`,
#   `git branch -a`, the reflog and HEAD must produce byte-identical output to a
#   control repo without it.
#
# ROUND 1 RESULT (recorded, do not re-litigate):
#   Refs under refs/harness/ ARE visible to `git log --all`, which globs refs/*.
#   The in-repo shadow-ref design is therefore rejected.
#
# ROUND 2 (this file): a separate bare object store outside the repository, with
# objects/info/alternates borrowing the repo's existing objects so that unchanged
# blobs are never copied. Nothing whatsoever is written inside the user's .git.
set -uo pipefail

WORK=$(mktemp -d) || exit 2
trap 'rm -rf "$WORK"' EXIT
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null
export GIT_AUTHOR_NAME=h GIT_AUTHOR_EMAIL=h@x GIT_COMMITTER_NAME=h GIT_COMMITTER_EMAIL=h@x
export GIT_AUTHOR_DATE="2020-01-01T00:00:00Z" GIT_COMMITTER_DATE="2020-01-01T00:00:00Z"

fail=0
note()  { printf '  %-46s %s\n' "$1" "$2"; }
check() { if [ "$2" = "$3" ]; then note "$1" "ok"
          else note "$1" "MISMATCH"; printf '    expected: [%s]\n    actual:   [%s]\n' "$2" "$3"; fail=1; fi; }

build_repo() { # dir
  local d="$1"
  mkdir -p "$d" && git -C "$d" init -q
  mkdir -p "$d/src"; echo tracked > "$d/src/a.txt"; echo "ignored/" > "$d/.gitignore"
  git -C "$d" add -A >/dev/null && git -C "$d" commit -qm init
  # a pre-existing stash, so we can prove we never disturb it
  echo stashme > "$d/src/b.txt"; git -C "$d" add -A >/dev/null; git -C "$d" stash -q -u
  # AFTER the stash: the three categories git treats differently
  echo dirty       >> "$d/src/a.txt"          # tracked, modified
  echo untracked    > "$d/untracked.txt"      # untracked, not ignored
  mkdir -p "$d/ignored"; echo precious > "$d/ignored/data.bin"   # ignored
}

observe() { # dir -> everything the user can see through git
  local d="$1"
  git -C "$d" log --oneline --all
  echo "--"; git -C "$d" status --porcelain=v1 | sort
  echo "--"; git -C "$d" stash list
  echo "--"; git -C "$d" branch -a
  echo "--"; git -C "$d" reflog --format='%gd %gs'
  echo "--"; git -C "$d" rev-parse HEAD
  echo "--"; git -C "$d" for-each-ref --format='%(refname)'
  echo "--"; git -C "$d" count-objects -v | grep -E '^(count|in-pack):'
}

# ---- mechanism under test ----------------------------------------------------
store_init() { # repo store
  local repo="$1" store="$2"
  git init -q --bare "$store"
  # borrow, never copy, the objects the repo already has
  printf '%s\n' "$(cd "$repo" && git rev-parse --absolute-git-dir)/objects" \
    > "$store/objects/info/alternates"
}

checkpoint() { # repo store seq -> prints commit sha
  local repo="$1" store="$2" seq="$3"
  local idx="$store/harness-index"
  rm -f "$idx"
  GIT_DIR="$store" GIT_WORK_TREE="$repo" GIT_INDEX_FILE="$idx" \
    git add -A -- "$repo" >/dev/null 2>&1 || \
  GIT_DIR="$store" GIT_WORK_TREE="$repo" GIT_INDEX_FILE="$idx" \
    git --git-dir="$store" --work-tree="$repo" add -A >/dev/null 2>&1
  local tree commit
  tree=$(GIT_DIR="$store" GIT_INDEX_FILE="$idx" git write-tree) || return 2
  commit=$(GIT_DIR="$store" git commit-tree "$tree" -m "harness checkpoint $seq") || return 2
  GIT_DIR="$store" git update-ref "refs/checkpoints/$seq" "$commit"
  rm -f "$idx"
  echo "$commit"
}

restore() { # repo store commit
  local repo="$1" store="$2" commit="$3"
  local idx="$store/harness-restore-index"
  rm -f "$idx"
  GIT_DIR="$store" GIT_INDEX_FILE="$idx" git read-tree "$commit" || return 2
  # remove non-ignored files that the checkpoint does not contain
  local want; want=$(GIT_DIR="$store" git ls-tree -r --name-only "$commit" | sort)
  local have; have=$( cd "$repo" && git ls-files --cached --others --exclude-standard | sort )
  comm -13 <(printf '%s\n' "$want") <(printf '%s\n' "$have") \
    | while IFS= read -r p; do [ -n "$p" ] && rm -f "$repo/$p"; done
  GIT_DIR="$store" GIT_WORK_TREE="$repo" GIT_INDEX_FILE="$idx" \
    git checkout-index -a -f -u || return 2
  rm -f "$idx"
}

echo "spike: out-of-repo checkpoint store"

CTRL="$WORK/control"; TEST="$WORK/test"; STORE="$WORK/store.git"
build_repo "$CTRL"; build_repo "$TEST"
store_init "$TEST" "$STORE"

before=$(observe "$TEST")
check "control and test start identical" "$(observe "$CTRL")" "$before"

sha=$(checkpoint "$TEST" "$STORE" 1)
check "user-visible git surface unchanged" "$before" "$(observe "$TEST")"
check "nothing added inside .git" "" \
  "$(find "$TEST/.git" -name 'harness*' -o -name '*harness*' | head -3)"

show() { GIT_DIR="$STORE" git show "$sha:$1" 2>/dev/null | tr -d '\n'; }
check "dirty tracked content captured"        "trackeddirty" "$(show src/a.txt)"
check "untracked file captured"               "untracked"    "$(show untracked.txt)"
check "ignored file NOT in the git layer"     ""             "$(show ignored/data.bin)"

# now destroy the tree the way an agent plausibly would
rm -rf "$TEST/src" "$TEST/untracked.txt"
echo garbage > "$TEST/.gitignore"
git -C "$TEST" add -A >/dev/null 2>&1

restore "$TEST" "$STORE" "$sha"
check "src/a.txt restored"     "trackeddirty" "$(cat "$TEST/src/a.txt" 2>/dev/null | tr -d '\n')"
check "untracked.txt restored" "untracked"    "$(cat "$TEST/untracked.txt" 2>/dev/null | tr -d '\n')"
check ".gitignore restored"    "ignored/"     "$(cat "$TEST/.gitignore" 2>/dev/null | tr -d '\n')"

# a hostile gc in the user's repo must not destroy the checkpoint's own objects
git -C "$TEST" gc -q --prune=now 2>/dev/null
check "checkpoint survives gc in user repo" "$sha" \
  "$(GIT_DIR="$STORE" git rev-parse -q --verify refs/checkpoints/1)"
check "checkpoint content still readable"   "trackeddirty" "$(show src/a.txt)"

fsck_out=$(git -C "$TEST" fsck --no-progress 2>&1 | grep -viE '^(checking|notice)' | head -2)
check "git fsck clean in user repo" "" "$fsck_out"

echo
if [ "$fail" -eq 0 ]; then echo "SPIKE PASSED: out-of-repo store is invisible, durable, and restores"; exit 0
else echo "SPIKE FAILED"; exit 1; fi
