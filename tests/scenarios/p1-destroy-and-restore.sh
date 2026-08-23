#!/usr/bin/env bash
# P1 acceptance: destroy a working tree the way an agent plausibly would, then
# restore it, and prove two things at once.
#
#   1. Recovery is complete. Tracked, untracked and declared-precious ignored
#      files all come back byte-identical.
#   2. Recovery is invisible. Everything the user can see through git -- log,
#      status, stash, branches, reflog, HEAD, refs -- is byte-identical to a
#      control repository that prohairesis never touched.
#
# The second assertion is the one that matters. A reversibility layer that
# perturbs the user's git state has taken away more agency than it added.
set -uo pipefail

HERE=$(cd "$(dirname "$0")/../.." && pwd)
BIN="$HERE/bin/prohairesis"
[ -x "$BIN" ] || { echo "build first: go build -o bin/prohairesis ./cmd/prohairesis" >&2; exit 2; }

WORK=$(mktemp -d) || exit 2
export PROHAIRESIS_HOME="$WORK/prohairesis-home"
trap 'rm -rf "$WORK"' EXIT

export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null
export GIT_AUTHOR_NAME=t GIT_AUTHOR_EMAIL=t@x GIT_COMMITTER_NAME=t GIT_COMMITTER_EMAIL=t@x
export GIT_AUTHOR_DATE="2020-01-01T00:00:00Z" GIT_COMMITTER_DATE="2020-01-01T00:00:00Z"

fail=0
check() { if [ "$2" = "$3" ]; then printf '  %-48s ok\n' "$1"
          else printf '  %-48s MISMATCH\n    expected: [%s]\n    actual:   [%s]\n' "$1" "$2" "$3"; fail=1; fi; }

build_repo() { # dir
  local d="$1"
  mkdir -p "$d/src" "$d/data" && git -C "$d" init -q
  printf 'ignored/\ndata/\n' > "$d/.gitignore"
  echo 'def main(): pass' > "$d/src/app.py"
  git -C "$d" add -A >/dev/null && git -C "$d" commit -qm init
  # a stash that must survive untouched
  echo 'stashed work' > "$d/src/wip.py"
  git -C "$d" add -A >/dev/null && git -C "$d" stash -q -u
  # the three categories, created after the stash so they stay in the tree
  echo '# edited by the agent' >> "$d/src/app.py"   # tracked, modified
  echo 'notes'                  > "$d/NOTES.md"     # untracked, not ignored
  echo 'expensive-to-recompute' > "$d/data/interim.bin"  # ignored, precious
  mkdir -p "$d/ignored" && echo junk > "$d/ignored/build.log"  # ignored, disposable
}

observe() { # dir -> the whole user-visible git surface
  local d="$1"
  git -C "$d" log --oneline --all
  echo "--"; git -C "$d" status --porcelain=v1 | sort
  echo "--"; git -C "$d" stash list
  echo "--"; git -C "$d" branch -a
  echo "--"; git -C "$d" reflog --format='%gd %gs'
  echo "--"; git -C "$d" rev-parse HEAD
  echo "--"; git -C "$d" for-each-ref --format='%(refname)'
}

echo "P1 acceptance: destroy and restore"

CTRL="$WORK/control"; REPO="$WORK/repo"
build_repo "$CTRL"; build_repo "$REPO"
check "control and subject start identical" "$(observe "$CTRL")" "$(observe "$REPO")"

cd "$REPO" || exit 2

# declare the ignored path we cannot afford to lose; leave the disposable one alone
"$BIN" protect add data >/dev/null || { echo "protect failed"; exit 2; }
"$BIN" session start --adapter test >/dev/null || { echo "session start failed"; exit 2; }

before_git=$(observe "$REPO")
before_app=$(cat src/app.py); before_notes=$(cat NOTES.md); before_data=$(cat data/interim.bin)

check "git surface unchanged by session start" "$(observe "$CTRL")" "$before_git"

# ---- destruction ------------------------------------------------------------
rm -rf src
rm -f NOTES.md
rm -f data/interim.bin
printf 'garbage\n' > .gitignore
echo 'stray' > src_leftover.txt
git clean -xdfq 2>/dev/null   # the agent reaching for a big hammer

echo "  (destroyed: src/, NOTES.md, data/interim.bin, .gitignore clobbered, git clean -xdf)"

# ---- recovery ---------------------------------------------------------------
"$BIN" undo >/dev/null || { echo "undo failed"; exit 2; }

check "tracked file restored"   "$before_app"   "$(cat src/app.py 2>/dev/null)"
check "untracked file restored" "$before_notes" "$(cat NOTES.md 2>/dev/null)"
check "precious ignored file restored" "$before_data" "$(cat data/interim.bin 2>/dev/null)"
check "stray file removed"      ""              "$(cat src_leftover.txt 2>/dev/null)"
check ".gitignore restored"     "$(printf 'ignored/\ndata/')" "$(cat .gitignore 2>/dev/null)"

# ---- the invariant ----------------------------------------------------------
check "git surface identical to control" "$(observe "$CTRL")" "$(observe "$REPO")"
check "nothing written inside .git"      "" "$(find "$REPO/.git" -name '*harness*' | head -1)"
check "no refs added"                    "$(git -C "$CTRL" for-each-ref | wc -l | tr -d ' ')" \
                                         "$(git -C "$REPO" for-each-ref | wc -l | tr -d ' ')"

"$BIN" session end >/dev/null
check "git surface identical after session end" "$(observe "$CTRL")" "$(observe "$REPO")"

echo
if [ "$fail" -eq 0 ]; then
  echo "P1 PASSED: recovery is complete and invisible"
  exit 0
fi
echo "P1 FAILED"
exit 1
