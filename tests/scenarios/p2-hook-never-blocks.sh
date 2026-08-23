#!/usr/bin/env bash
# P2 acceptance: the recording layer cannot fail the thing it records.
#
# The P1 scenario earns its keep by destroying a working tree rather than by
# checking that a healthy one still works. This is the same move on the other
# side of the seam: the interesting question is not whether the hook records an
# event when everything is fine, it is what the hook does when everything is not.
#
# Two claims are under test.
#
#   1. It never blocks. Every hook exits 0 -- with the state directory deleted,
#      unwritable, replaced by a file, given garbage on stdin, given a payload
#      far larger than any real one, or run outside a repository entirely.
#      A recording layer that can fail a tool call has stopped being one.
#
#   2. It never loses a loss. When an observation cannot be written, that fact
#      reaches a durable path of its own -- one that does not share a failure
#      with the path that just failed. A log with unmarked holes is worse than
#      no log, because its silence reads as evidence.
set -uo pipefail

HERE=$(cd "$(dirname "$0")/../.." && pwd)
BIN="$HERE/bin/prohairesis"
[ -x "$BIN" ] || { echo "build first: go build -o bin/prohairesis ./cmd/prohairesis" >&2; exit 2; }

WORK=$(mktemp -d) || exit 2
trap 'chmod -R u+rwx "$WORK" 2>/dev/null; rm -rf "$WORK"' EXIT

export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null
export GIT_AUTHOR_NAME=t GIT_AUTHOR_EMAIL=t@x GIT_COMMITTER_NAME=t GIT_COMMITTER_EMAIL=t@x
# Point the runtime's configuration at a throwaway directory. Without this the
# scenario would read whatever settings the machine running it happens to have,
# and pass or fail for reasons that have nothing to do with the code.
export CLAUDE_CONFIG_DIR="$WORK/claude-config"
mkdir -p "$CLAUDE_CONFIG_DIR"

fail=0
ok()   { printf '  %-56s ok\n' "$1"; }
bad()  { printf '  %-56s FAILED\n    %s\n' "$1" "$2"; fail=1; }

REPO="$WORK/repo"
mkdir -p "$REPO/src"
git -C "$REPO" init -q
echo 'one' > "$REPO/src/a.txt"
git -C "$REPO" add -A >/dev/null && git -C "$REPO" commit -qm init

payload() { # tool
  printf '{"hook_event_name":"PostToolUse","session_id":"agent-1","cwd":"%s","tool_name":"%s","tool_input":{"file_path":"%s/src/a.txt"},"tool_response":{"is_error":false}}' \
    "$REPO" "$1" "$REPO"
}

# expect_zero NAME HOME_DIR STDIN...
expect_zero() { # name, home, payload, subcommand
  local name="$1" home="$2" body="$3" sub="${4:-post-tool}"
  local out status
  out=$(printf '%s' "$body" | PROHAIRESIS_HOME="$home" "$BIN" hook "$sub" 2>&1)
  status=$?
  if [ "$status" -ne 0 ]; then
    bad "$name" "exit $status; output: $out"
  else
    ok "$name"
  fi
}

echo "the hook exits 0 no matter what it is handed"

HOME_OK="$WORK/home-ok"
expect_zero "healthy: session start"        "$HOME_OK" "$(printf '{"hook_event_name":"SessionStart","session_id":"agent-1","cwd":"%s"}' "$REPO")" session-start
expect_zero "healthy: tool call"            "$HOME_OK" "$(payload Edit)"
expect_zero "unmappable tool"               "$HOME_OK" "$(payload mcp__unknown__thing)"

# The failure that actually happens: somebody cleaned up, or the directory was
# never created, or it belongs to another user.
expect_zero "state directory missing"       "$WORK/gone/deeper" "$(payload Edit)"

RO="$WORK/home-readonly"
mkdir -p "$RO" && chmod 500 "$RO"
expect_zero "state directory not writable"  "$RO" "$(payload Edit)"

NOTDIR="$WORK/home-is-a-file"
: > "$NOTDIR"
expect_zero "state directory is a file"     "$NOTDIR" "$(payload Edit)"

expect_zero "stdin is not JSON"             "$HOME_OK" 'this is not json at all'
expect_zero "stdin is empty"                "$HOME_OK" ''
expect_zero "payload is enormous"           "$HOME_OK" "$(printf '{"hook_event_name":"PostToolUse","session_id":"agent-1","cwd":"%s","tool_name":"Write","tool_input":{"file_path":"%s"}}' "$REPO" "$(printf 'a%.0s' $(seq 1 200000))")"
expect_zero "unknown hook name"             "$HOME_OK" "$(payload Edit)" not-a-real-hook

OUTSIDE="$WORK/not-a-repo"
mkdir -p "$OUTSIDE"
expect_zero "outside any repository"        "$HOME_OK" "$(printf '{"hook_event_name":"PostToolUse","session_id":"agent-1","cwd":"%s","tool_name":"Edit","tool_input":{}}' "$OUTSIDE")"

echo
echo "an observation that cannot be written is still accounted for"

# The loss file must be reachable when the session directory is not. Here the
# state directory itself is fine and the session directory inside it is blocked,
# which is the arrangement the two-tier fallback exists for.
LOSS_HOME="$WORK/home-loss"
mkdir -p "$LOSS_HOME/sessions"
printf '%s' "$(printf '{"hook_event_name":"SessionStart","session_id":"agent-1","cwd":"%s"}' "$REPO")" \
  | PROHAIRESIS_HOME="$LOSS_HOME" "$BIN" hook session-start >/dev/null 2>&1
SID=$(ls "$LOSS_HOME/sessions" 2>/dev/null | head -1)
if [ -z "$SID" ]; then
  bad "a session was created to lose events from" "no session directory appeared"
else
  chmod 500 "$LOSS_HOME/sessions/$SID"
  printf '%s' "$(payload Edit)" | PROHAIRESIS_HOME="$LOSS_HOME" "$BIN" hook post-tool >/dev/null 2>&1
  status=$?
  [ "$status" -eq 0 ] || bad "hook still exits 0 when the log is unwritable" "exit $status"
  chmod 700 "$LOSS_HOME/sessions/$SID"

  if [ -s "$LOSS_HOME/telemetry-loss.jsonl" ]; then
    ok "the loss reached a durable path outside the session directory"
  else
    bad "the loss reached a durable path outside the session directory" \
        "no $LOSS_HOME/telemetry-loss.jsonl"
  fi
  if grep -q '"schema":"harness.loss.v1"' "$LOSS_HOME/telemetry-loss.jsonl" 2>/dev/null; then
    ok "the loss record follows the published schema"
  else
    bad "the loss record follows the published schema" "$(cat "$LOSS_HOME/telemetry-loss.jsonl" 2>/dev/null)"
  fi
  # It records that something was lost, never what was lost. Otherwise the loss
  # file becomes a second event log holding exactly what the first one refuses
  # to hold, reached by a path with weaker guarantees.
  if grep -qE '"(action|argv|paths|command)"' "$LOSS_HOME/telemetry-loss.jsonl" 2>/dev/null; then
    bad "the loss record carries no part of the observation" \
        "$(cat "$LOSS_HOME/telemetry-loss.jsonl")"
  else
    ok "the loss record carries no part of the observation"
  fi
  # doctor exits non-zero when it finds something serious, which it may well do
  # here; the question is what it says, so capture the output rather than piping
  # it and letting the exit status decide.
  doctor_out=$(PROHAIRESIS_HOME="$LOSS_HOME" "$BIN" doctor 2>/dev/null)
  if printf '%s' "$doctor_out" | grep -q "could not be written"; then
    ok "doctor says the record has holes in it"
  else
    bad "doctor says the record has holes in it" "doctor did not mention the loss"
  fi
fi

echo
echo "a healthy session produces a log that parses"
LOG=$(ls "$HOME_OK"/sessions/*/events.jsonl 2>/dev/null | head -1)
if [ -z "$LOG" ]; then
  bad "events were recorded" "no events.jsonl under $HOME_OK"
else
  n=$(wc -l < "$LOG" | tr -d ' ')
  if command -v python3 >/dev/null 2>&1; then
    if python3 -c "
import json,sys
seq=0
for line in open('$LOG'):
    e=json.loads(line)
    assert e['schema']=='harness.event.v1', e
    assert e['seq']==seq+1, (seq, e['seq'])
    seq=e['seq']
    a=e.get('action') or {}
    body=json.dumps(e)
    assert 'src/a.txt' not in body or a.get('paths')==['src/a.txt'], body
"; then
      ok "$n events, every line valid, sequence unbroken"
    else
      bad "every line valid and the sequence unbroken" "see above"
    fi
  fi
fi

printf '\n'
if [ "$fail" -eq 0 ]; then echo "P2 PASSED"; exit 0; fi
echo "P2 FAILED"; exit 1
