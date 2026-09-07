#!/usr/bin/env bash
# P3 acceptance: verification is a contract, not a gate.
#
# The unit tests already cover discovery, normalization and scoring in isolation.
# What only an end-to-end run can establish is the set of promises an agent
# actually relies on when it types `prohairesis verify`:
#
#   1. It runs what the repository declared, and nothing else. A `deploy` target
#      sitting next to a `test` target is exactly the thing a verification layer
#      must never find interesting.
#   2. The same failure twice is the same fingerprint, through real subprocess
#      output with real clocks and real pids in it. Everything a later phase will
#      build -- attempt boundaries, recurrence, last-green -- is dead if this is
#      not true.
#   3. A missing tool is a skip and exit 0. A machine without the tool installed
#      is not a project that fails.
#   4. It changes nothing. No file in the repository, and no perturbation of the
#      git surface that P1 promised would stay byte-identical.
#   5. The context it injects at session start stays inside its budget.
set -uo pipefail

HERE=$(cd "$(dirname "$0")/../.." && pwd)
BIN="$HERE/bin/prohairesis"
[ -x "$BIN" ] || { echo "build first: go build -o bin/prohairesis ./cmd/prohairesis" >&2; exit 2; }

WORK=$(mktemp -d) || exit 2
trap 'rm -rf "$WORK"' EXIT

export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null
export GIT_AUTHOR_NAME=t GIT_AUTHOR_EMAIL=t@x GIT_COMMITTER_NAME=t GIT_COMMITTER_EMAIL=t@x
export PROHAIRESIS_HOME="$WORK/home"
export CLAUDE_CONFIG_DIR="$WORK/claude-config"
mkdir -p "$CLAUDE_CONFIG_DIR"

fail=0
ok()  { printf '  %-56s ok\n' "$1"; }
bad() { printf '  %-56s FAILED\n    %s\n' "$1" "$2"; fail=1; }

REPO="$WORK/repo"
mkdir -p "$REPO/src" "$REPO/scripts"
git -C "$REPO" init -q

# A repository that declares one check, fails it with a location-bearing
# diagnostic, and prints a clock reading and a pid alongside -- the two things
# that would make one failure look like a new one on every run.
cat > "$REPO/scripts/run_quality_checks.sh" <<'SH'
#!/bin/sh
echo "=== lint ==="
echo "started at $(date -u +%H:%M:%S) in $$"
echo "src/a.py:3:1: F821 undefined name 'frame'"
exit 1
SH
# A target that must never be discovered, in the file discovery would read if the
# aggregate script were not there.
cat > "$REPO/Makefile" <<'MK'
deploy:
	touch DEPLOYED
MK
echo 'x = frame' > "$REPO/src/a.py"
git -C "$REPO" add -A >/dev/null && git -C "$REPO" commit -qm init

# A session, so that the runs below have somewhere to be recorded. Verification
# does not need one -- reading whether a repository passes is useful on its own --
# but the record is what makes a fingerprint mean anything later.
(cd "$REPO" && "$BIN" session start >/dev/null) || exit 2

# The git surface, captured the way P1 captures it: whatever verify does, none of
# this may move.
git_surface() {
  git -C "$REPO" log --all --oneline 2>&1
  git -C "$REPO" status --porcelain 2>&1
  git -C "$REPO" stash list 2>&1
  git -C "$REPO" branch -a 2>&1
  git -C "$REPO" reflog 2>&1
  git -C "$REPO" for-each-ref 2>&1
}

echo "a declared check that fails"

OUT=$(cd "$REPO" && "$BIN" verify --json 2>&1); STATUS=$?
if [ "$STATUS" -ne 1 ]; then
  bad "a failing check exits 1" "exit $STATUS; output: $OUT"
else
  ok "a failing check exits 1"
fi

if printf '%s' "$OUT" | grep -q '"schema": "harness.verify.v1"'; then
  ok "the result declares the published schema"
else
  bad "the result declares the published schema" "$OUT"
fi

if printf '%s' "$OUT" | grep -q '"location": "src/a.py:3:1"'; then
  ok "the diagnostic became a normalized location"
else
  bad "the diagnostic became a normalized location" "$OUT"
fi

if printf '%s' "$OUT" | grep -q '"category": "quality"'; then
  ok "the aggregate script won the discovery ladder"
else
  bad "the aggregate script won the discovery ladder" "$OUT"
fi

[ -e "$REPO/DEPLOYED" ] && bad "a deploy target was never run" "the Makefile target ran" \
  || ok "a deploy target was never run"

echo
echo "the same failure twice is one failure"

FP1=$(printf '%s' "$OUT" | sed -n 's/.*"fingerprint": "\([0-9a-f]*\)".*/\1/p')
sleep 1  # so that the clock reading in the output is genuinely different
OUT2=$(cd "$REPO" && "$BIN" verify --json 2>&1)
FP2=$(printf '%s' "$OUT2" | sed -n 's/.*"fingerprint": "\([0-9a-f]*\)".*/\1/p')

if [ -n "$FP1" ] && [ "$FP1" = "$FP2" ]; then
  ok "a repeated failure keeps its fingerprint"
else
  bad "a repeated failure keeps its fingerprint" "$FP1 vs $FP2"
fi

# A different failure has to be a different failure, or recurrence detection
# would report every attempt as the same one and never notice progress.
sed -i.bak "s/F821 undefined name 'frame'/F821 undefined name 'window'/" "$REPO/scripts/run_quality_checks.sh"
rm -f "$REPO/scripts/run_quality_checks.sh.bak"
OUT3=$(cd "$REPO" && "$BIN" verify --json 2>&1)
FP3=$(printf '%s' "$OUT3" | sed -n 's/.*"fingerprint": "\([0-9a-f]*\)".*/\1/p')
if [ -n "$FP3" ] && [ "$FP3" != "$FP1" ]; then
  ok "a different failure gets a different fingerprint"
else
  bad "a different failure gets a different fingerprint" "$FP1 vs $FP3"
fi

echo
echo "graceful degradation"

MISSING="$WORK/missing"
mkdir -p "$MISSING"
git -C "$MISSING" init -q
cat > "$MISSING/Makefile" <<'MK'
test:
	@a-tool-that-is-not-installed-anywhere
MK
git -C "$MISSING" add -A >/dev/null && git -C "$MISSING" commit -qm init
printf '{"scripts":{"test":"vitest"}}' > "$MISSING/package.json"
rm "$MISSING/Makefile"  # leave only the manifest, whose runner is absent below

# A PATH holding git and nothing else. Naming a directory rather than trusting
# that some particular package manager is absent from this machine is what keeps
# this assertion from passing or failing for reasons unrelated to the code.
STUB="$WORK/stub-path"
mkdir -p "$STUB"
ln -s "$(command -v git)" "$STUB/git"

OUT4=$(cd "$MISSING" && PATH="$STUB" "$BIN" verify --json 2>&1); STATUS=$?
if [ "$STATUS" -eq 0 ] && printf '%s' "$OUT4" | grep -q '"skipped": true'; then
  ok "an absent tool is skipped, exit 0"
else
  bad "an absent tool is skipped, exit 0" "exit $STATUS; output: $OUT4"
fi

EMPTY="$WORK/empty"
mkdir -p "$EMPTY"
git -C "$EMPTY" init -q
OUT5=$(cd "$EMPTY" && "$BIN" verify 2>&1); STATUS=$?
if [ "$STATUS" -eq 0 ] && printf '%s' "$OUT5" | grep -q 'no checks are declared'; then
  ok "a repository that declares nothing is not a failure"
else
  bad "a repository that declares nothing is not a failure" "exit $STATUS; output: $OUT5"
fi

echo
echo "verification changes nothing"

# Snapshotted here rather than at the top: the edit above is the scenario's own,
# and the claim under test is that a verification run moves nothing.
BEFORE_GIT=$(git_surface)
BEFORE_TREE=$(cd "$REPO" && find . -path ./.git -prune -o -print | sort)
(cd "$REPO" && "$BIN" verify >/dev/null 2>&1)

AFTER_TREE=$(cd "$REPO" && find . -path ./.git -prune -o -print | sort)
if [ "$BEFORE_TREE" = "$AFTER_TREE" ]; then
  ok "no file was created or removed in the repository"
else
  bad "no file was created or removed in the repository" "$(diff <(printf '%s' "$BEFORE_TREE") <(printf '%s' "$AFTER_TREE"))"
fi

AFTER_GIT=$(git_surface)
if [ "$BEFORE_GIT" = "$AFTER_GIT" ]; then
  ok "the git surface is byte-identical"
else
  bad "the git surface is byte-identical" "$(diff <(printf '%s' "$BEFORE_GIT") <(printf '%s' "$AFTER_GIT"))"
fi

echo
echo "what is injected into an agent's context"

CTX=$(printf '{"hook_event_name":"SessionStart","session_id":"agent-1","cwd":"%s"}' "$REPO" \
  | "$BIN" hook session-start 2>/dev/null)
INJECTED=$(printf '%s' "$CTX" | sed -n 's/.*"additionalContext":"\(.*\)"}}/\1/p')
BYTES=${#INJECTED}

# 200 bytes is the design's "under 50 tokens" expressed in a unit that can be
# counted without shipping a tokenizer, at four bytes per token.
if [ "$BYTES" -gt 0 ] && [ "$BYTES" -le 200 ]; then
  ok "the session-start notice is $BYTES bytes, within budget"
else
  bad "the session-start notice is within budget" "$BYTES bytes: $INJECTED"
fi

if printf '%s' "$INJECTED" | grep -q 'prohairesis verify'; then
  ok "the notice names verify where checks are declared"
else
  bad "the notice names verify where checks are declared" "$INJECTED"
fi

CTX2=$(printf '{"hook_event_name":"SessionStart","session_id":"agent-2","cwd":"%s"}' "$EMPTY" \
  | "$BIN" hook session-start 2>/dev/null)
if printf '%s' "$CTX2" | grep -q 'verify'; then
  bad "the notice stays quiet where there is nothing to verify" "$CTX2"
else
  ok "the notice stays quiet where there is nothing to verify"
fi

echo
echo "the run is recorded, and the record keeps no output text"

RECORD=$(find "$PROHAIRESIS_HOME/sessions" -name verify.jsonl 2>/dev/null | head -1)
if [ -n "$RECORD" ]; then
  ok "runs inside a session are recorded"
  if grep -q '"redacted":true' "$RECORD" && ! grep -q 'undefined name' "$RECORD"; then
    ok "the record keeps the fingerprint and not the finding text"
  else
    bad "the record keeps the fingerprint and not the finding text" "$(head -c 400 "$RECORD")"
  fi
else
  bad "runs inside a session are recorded" "no verify.jsonl under $PROHAIRESIS_HOME/sessions"
fi

echo
if [ "$fail" -eq 0 ]; then echo "P3 SCENARIO PASSED"; exit 0; fi
echo "P3 SCENARIO FAILED"; exit 1
