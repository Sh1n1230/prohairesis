#!/usr/bin/env bash
# Run everything that can be checked mechanically.
#
# Every scenario is self-contained: it builds its own repositories in a temp
# directory, points PROHAIRESIS_HOME somewhere disposable, and touches nothing on
# the machine it runs on.
set -uo pipefail

HERE=$(cd "$(dirname "$0")/.." && pwd)
cd "$HERE" || exit 2
export PATH="/opt/homebrew/bin:$PATH"

fail=0
run() {
  printf '\n=== %s ===\n' "$1"
  shift
  if "$@"; then :; else fail=1; fi
}

gofmt_clean() {
  local out
  out=$(gofmt -l .)
  if [ -n "$out" ]; then
    echo "not gofmt-clean:"; echo "$out"; return 1
  fi
  echo "clean"
}

# Lint is required in CI, where the tool is guaranteed. Locally it is optional so
# that a missing tool never stops someone running the suite -- the same
# graceful-degradation rule the rest of this project follows: report and skip,
# never error.
lint() {
  if command -v golangci-lint >/dev/null 2>&1; then
    golangci-lint run
  else
    echo "skipped (golangci-lint not installed; CI runs it)"
  fi
}

# The shipped binary validates nothing against JSON Schema -- that would cost
# something on every event to confirm this program's output matches this
# program's own contract. The check belongs here, once per build, where it
# catches the thing that actually goes wrong: the schema and the code drifting
# apart between releases.
schema_matches_golden() {
  if python3 -c "import jsonschema" 2>/dev/null; then
    python3 tools/validate-schema.py
  else
    echo "skipped (python jsonschema not installed; CI runs it)"
  fi
}

corpus_is_clean() {
  python3 tools/sanitize-corpus.py tests/golden/false-block-corpus.jsonl > /dev/null
}

# The scanners in ~/security-checker normalize and score tool output into the
# shape an agent can act on. That normalization is a local concern -- CI runs the
# underlying tools directly, because a public repository cannot check out a
# private one without breaking every pull request from a fork.
security_checker() {
  local sc="$HOME/security-checker/check.sh"
  if [ -x "$sc" ]; then
    bash "$sc" "$HERE"
  else
    echo "skipped (no security-checker at $sc; CI runs gitleaks/osv-scanner/trivy directly)"
  fi
}

run "format"     gofmt_clean
run "vet"        go vet ./...
run "lint"       lint
run "unit tests" go test -count=1 ./...
run "build"      go build -o bin/prohairesis ./cmd/prohairesis
run "spike: out-of-repo checkpoint store" bash tests/scenarios/spike-shadow-ref.sh
run "P1: destroy and restore"             bash tests/scenarios/p1-destroy-and-restore.sh
run "P2: the hook never blocks"           bash tests/scenarios/p2-hook-never-blocks.sh
run "golden records match the published schemas" schema_matches_golden
run "published corpus carries no personal data" corpus_is_clean
run "security-checker (optional)"               security_checker

printf '\n'
if [ "$fail" -eq 0 ]; then echo "ALL PASSED"; exit 0; fi
echo "FAILURES"; exit 1
