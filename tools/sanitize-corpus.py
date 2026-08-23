#!/usr/bin/env python3
"""Strip personal detail from the blocked-command corpus before publication.

The corpus exists to prove one thing: which *shapes* of shell command a substring
denylist refuses. That property lives entirely in the trigger tokens -- `.env`,
`credentials`, `.key`, `.ssh/`, and so on -- and not at all in whose machine the
command ran on or which project it belonged to.

So: keep every trigger token exactly as it appeared, and replace everything that
identifies a person, a project, or a third-party service.

The last step is a scan for a denylist of forbidden tokens. If any survive, this
exits non-zero and writes nothing. A sanitizer you have to trust is not a
sanitizer; this one is checkable.

Usage:
  tools/sanitize-corpus.py raw.jsonl > tests/golden/false-block-corpus.jsonl
"""

import json
import re
import sys

# Ordered literal replacements. Longest and most specific first.
REPLACEMENTS = [
    # third-party service the user has a real money account with
    ("NETKEIBA_LOGIN_ID", "SERVICE_LOGIN_ID"),
    ("NETKEIBA_PASSWORD", "SERVICE_PASSWORD"),
    ("netkeiba", "example-service"),
    # private project directories -> stable neutral names
    ("keiba-ai", "project-a"),
    ("ptcg-ai-bot", "project-b"),
    ("ga_hackathon_7", "project-c"),
    ("ga_hackathon", "project-c"),
    ("rogii-wellbore-geology-prediction", "project-d"),
    ("tech-ocean-student-cup-2026", "project-e"),
    # personal namespaces
    ("sh1n1230", "user"),
    ("shin1230", "user"),
]

# Paths: /Users/<anyone>/... -> $HOME/...
HOME_RE = re.compile(r"/Users/[A-Za-z0-9._-]+")

# Free prose inside commit messages and docs describes private work and carries
# no regression value. Collapse long heredoc bodies, keeping the opening lines
# (where the trigger tokens live) and saying plainly what was dropped.
HEREDOC_KEEP_LINES = 6

# Anything matching these must not survive. Case-insensitive.
FORBIDDEN = [
    "netkeiba", "keiba", "shin1230", "sh1n1230", "/Users/",
    "ptcg", "hackathon", "rogii", "student-cup",
]


def collapse_heredocs(cmd: str) -> str:
    """Truncate long heredoc bodies, preserving the head where triggers appear."""
    lines = cmd.split("\n")
    if len(lines) <= HEREDOC_KEEP_LINES + 2:
        return cmd
    kept = lines[:HEREDOC_KEEP_LINES]
    dropped = len(lines) - HEREDOC_KEEP_LINES
    return "\n".join(kept) + f"\n... [{dropped} lines of body elided for publication]"


def sanitize(text: str) -> str:
    for old, new in REPLACEMENTS:
        text = text.replace(old, new)
        text = text.replace(old.upper(), new.upper())
    text = HOME_RE.sub("$HOME", text)
    return text


def main() -> int:
    if len(sys.argv) < 2:
        print(__doc__, file=sys.stderr)
        return 2

    rows = []
    with open(sys.argv[1], encoding="utf-8") as fh:
        for line in fh:
            line = line.strip()
            if not line:
                continue
            rec = json.loads(line)
            rec["command"] = sanitize(collapse_heredocs(rec.get("command", "")))
            rec["cwd"] = sanitize(rec.get("cwd", ""))
            rec["reason"] = sanitize(rec.get("reason", ""))
            rec["sanitized"] = True
            rows.append(rec)

    # verification: nothing identifying may survive
    blob = json.dumps(rows, ensure_ascii=False).lower()
    leaked = [tok for tok in FORBIDDEN if tok.lower() in blob]
    if leaked:
        print(
            "sanitize: refusing to write; these tokens survived: " + ", ".join(leaked),
            file=sys.stderr,
        )
        return 1

    for r in rows:
        print(json.dumps(r, ensure_ascii=False, sort_keys=True))

    triggers = ["\\.env", "credentials", "\\.key", "\\.ssh", "settings.json"]
    present = sum(1 for t in triggers if re.search(t, blob))
    print(
        f"sanitize: {len(rows)} entries, 0 identifying tokens, "
        f"{present}/{len(triggers)} trigger classes preserved",
        file=sys.stderr,
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
