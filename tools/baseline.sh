#!/usr/bin/env bash
# harness P0: capture an agency-friction baseline from existing agent transcripts,
# BEFORE this project changes anything on the machine.
#
# Reads Claude Code transcripts (read-only) and emits normalized JSON.
# Usage: tools/baseline.sh [transcript-root] > baseline.json
#
# Exit codes follow the project convention: 0 clean, 1 gate failed, 2 execution error.
set -uo pipefail

ROOT="${1:-$HOME/.claude/projects}"

command -v jq >/dev/null 2>&1 || { echo "harness: jq is required" >&2; exit 2; }
[ -d "$ROOT" ] || { echo "harness: no transcript root at $ROOT" >&2; exit 2; }

# Sentinel emitted by the agent runtime when a human declines a tool call.
# Deliberately avoids the apostrophe: the runtime uses U+2019, and shells,
# editors and locales disagree about it. Match the stable substring instead.
REJECT_MARK="want to proceed with this tool use"
# Marker written by the pre-existing regex denylist hook this project replaces.
HOOKBLOCK_MARK="ブロック:"

per_session() {
  local f="$1"
  jq -s \
    --arg reject "$REJECT_MARK" \
    --arg hookblock "$HOOKBLOCK_MARK" \
    --arg file "$f" '
    # ---- helpers -------------------------------------------------------------
    def txt:
      if type == "string" then .
      elif type == "array" then ([.[]? | .text? // ""] | join(" "))
      else "" end;

    # A failure fingerprint must be stable across runs: strip absolute paths,
    # numbers, hex blobs and whitespace noise. Derived mechanically -- no model,
    # no interpretation. See docs/META-STATE.md.
    def fingerprint:
      ascii_downcase
      | gsub("/[a-z0-9_./-]+"; "<path>")
      | gsub("[0-9a-f]{8,}"; "<hex>")
      | gsub("[0-9]+"; "0")
      | gsub("\\s+"; " ")
      | .[0:160];

    def ts: (.timestamp // empty);
    # fromdateiso8601 rejects fractional seconds; agent transcripts include them.
    def epoch: sub("\\.[0-9]+Z$"; "Z") | fromdateiso8601;

    # ---- partition -----------------------------------------------------------
    map(select(.type != null)) as $all

    | ($all | map(select(.type=="user"))
            | map(select(.isSidechain != true))
            | map(select(
                (.message.content | type) == "string"
                or ((.message.content // []) | map(.type?) | index("tool_result") | not)
              ))) as $human

    | ($all | map(select(.type=="assistant"))
            | map(.message.content // [] | .[]? | select(.type=="tool_use"))) as $calls

    | ($all | map(.message.content // [] | .[]? | select(.type=="tool_result"))) as $results
    | ($results | map(select(.is_error == true))) as $errors

    | ($errors | map(.content | txt)) as $errtexts
    | ($errtexts | map(select(test($reject; "n")))) as $rejects
    | ($errtexts | map(select(startswith($hookblock)))) as $hookblocks

    # ---- recurrence: an error whose fingerprint was already seen earlier -----
    | ($errtexts | map(select(length > 0) | fingerprint)) as $fps
    | (reduce $fps[] as $fp ({seen: [], n: 0};
         if (.seen | index($fp)) != null
         then .n += 1
         else .seen += [$fp] end)
       | .n) as $repeat_errors

    # ---- timing --------------------------------------------------------------
    | ($all | map(ts) | map(select(. != null)) | sort) as $times
    | (if ($times | length) > 1
       then (($times[-1] | epoch) - ($times[0] | epoch))
       else 0 end) as $duration

    | ($human | map(ts) | map(select(. != null)) | map(epoch) | sort) as $htimes
    | (if ($htimes | length) > 1
       then ([range(1; $htimes|length) | $htimes[.] - $htimes[.-1]])
       else [] end) as $gaps

    # ---- emit ----------------------------------------------------------------
    | {
        file: $file,
        session_id: ($all | map(.sessionId) | map(select(. != null)) | first),
        cwd:        ($all | map(.cwd)       | map(select(. != null)) | first),
        records:            ($all | length),
        human_turns:        ($human | length),
        tool_calls:         ($calls | length),
        tool_results:       ($results | length),
        tool_errors:        ($errors | length),
        repeat_errors:      $repeat_errors,
        user_rejections:    ($rejects | length),
        hook_blocks:        ($hookblocks | length),
        duration_s:         ($duration | floor),
        uninterrupted_gaps_s: ($gaps | map(floor)),
        tool_calls_per_human_turn:
          (if ($human|length) > 0
           then (($calls|length) / ($human|length))
           else null end)
      }
  ' "$f" 2>/dev/null
}

{
  find "$ROOT" -name '*.jsonl' -type f -print0 \
  | while IFS= read -r -d '' f; do per_session "$f"; done
} | jq -s '
    map(select(.records != null and .records > 0)) as $s
    | ($s | map(.uninterrupted_gaps_s) | add // []) as $gaps
    | ($gaps | length) as $ng
    | {
        schema: "harness.baseline.v1",
        captured_at: (now | todateiso8601),
        source: "claude-code-transcripts",
        note: "Pre-install baseline. All figures are APPROXIMATIONS derived from transcripts; see docs/METRICS.md for what each proxy can and cannot support.",
        sessions: ($s | length),
        totals: {
          human_turns:     ($s | map(.human_turns)     | add // 0),
          tool_calls:      ($s | map(.tool_calls)      | add // 0),
          tool_errors:     ($s | map(.tool_errors)     | add // 0),
          repeat_errors:   ($s | map(.repeat_errors)   | add // 0),
          user_rejections: ($s | map(.user_rejections) | add // 0),
          hook_blocks:     ($s | map(.hook_blocks)     | add // 0)
        },
        metrics: {
          interrupts_per_session: {
            value: (($s | map(.human_turns + .user_rejections) | add // 0) / (($s|length) | if . == 0 then 1 else . end)),
            proxy: "human turns + explicit tool rejections",
            direction: "down"
          },
          mean_uninterrupted_run_s: {
            value: (if $ng > 0 then (($gaps | add) / $ng) else null end),
            median: (if $ng > 0 then ($gaps | sort | .[($ng / 2 | floor)]) else null end),
            samples: $ng,
            proxy: "wall-clock between consecutive human turns",
            direction: "up",
            note: "Inflated by sessions the human walked away from. Compare medians, not means."
          },
          tool_calls_per_human_turn: {
            value: (($s | map(.tool_calls) | add // 0) / (($s | map(.human_turns) | add // 0) | if . == 0 then 1 else . end)),
            direction: "up"
          },
          recurrence_rate: {
            value: (($s | map(.repeat_errors) | add // 0) / (($s | map(.tool_errors) | add // 0) | if . == 0 then 1 else . end)),
            proxy: "share of failures whose normalized fingerprint already occurred earlier in the same session",
            direction: "down"
          },
          context_tax: { value: 0, note: "nothing is injected before install, by definition" }
        },
        sessions_detail: ($s | sort_by(-.tool_calls) | map(del(.uninterrupted_gaps_s)))
      }'
