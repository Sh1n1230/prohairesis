#!/usr/bin/env python3
"""Check the published golden records against the published schemas.

The shipped binary has no dependencies and is meant to keep it that way, so
nothing validates JSON Schema at runtime: it would cost something on every event
to confirm that this program's own output matches this program's own contract.
Confirming it once, here, is enough -- and it is the half of the check that
catches the failure that matters, which is the schema and the code drifting apart
between releases.

Usage: tools/validate-schema.py
Exit codes follow the project convention: 0 clean, 1 gate failed, 2 cannot run.
"""
import json
import pathlib
import sys

try:
    from jsonschema import Draft202012Validator
except ImportError:
    print("jsonschema is not installed; CI runs this check", file=sys.stderr)
    sys.exit(2)

ROOT = pathlib.Path(__file__).resolve().parent.parent
PAIRS = [
    ("schema/harness.event.v1.json", "tests/golden/event.v1.jsonl"),
    ("schema/harness.loss.v1.json", "tests/golden/loss.v1.jsonl"),
    ("schema/harness.verify.v1.json", "tests/golden/verify.v1.jsonl"),
]

failed = 0
for schema_path, golden_path in PAIRS:
    schema = json.loads((ROOT / schema_path).read_text())
    Draft202012Validator.check_schema(schema)
    validator = Draft202012Validator(schema)

    lines = [ln for ln in (ROOT / golden_path).read_text().splitlines() if ln.strip()]
    if not lines:
        print(f"{golden_path}: no records to validate", file=sys.stderr)
        failed = 1
        continue

    bad = 0
    for n, line in enumerate(lines, 1):
        for err in validator.iter_errors(json.loads(line)):
            print(f"{golden_path}:{n}: {err.message}", file=sys.stderr)
            bad += 1
    if bad:
        failed = 1
    print(f"{golden_path}: {len(lines)} record(s) against {schema_path}: "
          f"{'FAILED' if bad else 'ok'}")

sys.exit(1 if failed else 0)
