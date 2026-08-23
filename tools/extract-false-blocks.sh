#!/usr/bin/env bash
# harness P0: extract every Bash command that the pre-existing regex denylist hook
# refused, from recorded transcripts, into a regression corpus.
#
# These are real commands from real sessions. The corpus is permanent: any future
# policy evaluator in this project must reproduce a verdict for each of them, and
# the ones marked expect="allow" must be allowed. It exists to stop this project
# from re-inventing the thing it replaces.
#
# Usage: tools/extract-false-blocks.sh [transcript-root] > tests/golden/false-block-corpus.jsonl
set -uo pipefail

ROOT="${1:-$HOME/.claude/projects}"
MARK="ブロック:"

command -v jq >/dev/null 2>&1 || { echo "harness: jq is required" >&2; exit 2; }
[ -d "$ROOT" ] || { echo "harness: no transcript root at $ROOT" >&2; exit 2; }

tmp_calls=$(mktemp) || exit 2
tmp_blocked=$(mktemp) || exit 2
trap 'rm -f "$tmp_calls" "$tmp_blocked"' EXIT

# every Bash tool_use, keyed by its id
find "$ROOT" -name '*.jsonl' -type f -exec cat {} + \
| jq -c 'select(.type=="assistant")
         | {cwd, ts: .timestamp}
           + (.message.content[]? | select(.type=="tool_use" and .name=="Bash")
              | {id, command: .input.command})' \
  2>/dev/null | jq -c 'select(.id != null)' > "$tmp_calls"

# ids whose result carries the hook marker
find "$ROOT" -name '*.jsonl' -type f -exec cat {} + \
| jq -r --arg m "$MARK" '
    .message.content[]?
    | select(.type=="tool_result")
    | select(((if (.content|type)=="string" then .content
               else ([.content[]?.text] | join(" ")) end)) | contains($m))
    | .tool_use_id' 2>/dev/null | sort -u > "$tmp_blocked"

jq -s -c --slurpfile ids <(jq -R . "$tmp_blocked") '
    ($ids | map(.)) as $wanted
    | map(select([.id] | inside($wanted)))
    | unique_by(.command)
    | .[]
    | {
        schema: "harness.golden.blocked-command.v1",
        source: "pre-existing regex denylist hook",
        cwd, command,
        expect: "unknown",
        note: "Verdict not yet triaged. Set expect to allow|deny and add a reason."
      }' "$tmp_calls"
