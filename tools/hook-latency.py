#!/usr/bin/env python3
"""Measure what the hook actually costs, from outside.

The hook records its own duration, and that figure is systematically wrong in the
direction that flatters it: a process cannot time the exec that started it, and
process start is a large part of the cost. This measures the whole invocation the
way the agent runtime experiences it -- fork, exec, runtime start, work, exit.

The timer has to be outside the measured process and inside a single one of its
own. An earlier version of this took its two timestamps by starting a fresh
interpreter each time, which put that interpreter's own start-up inside the
window and roughly doubled every number.

Nothing here is a budget. There is no measured basis for a threshold yet, and the
last figure this project carried without one turned out to have no derivation at
all. It prints a distribution. What to do about it waits for enough real sessions
to decide from.

Usage: tools/hook-latency.py [iterations]
Exit codes follow the project convention: 0 clean, 2 cannot run.
"""
import json
import os
import pathlib
import shutil
import subprocess
import sys
import tempfile
import time

ROOT = pathlib.Path(__file__).resolve().parent.parent
BIN = ROOT / "bin" / "prohairesis"


def git(cwd, *args):
    subprocess.run(["git", "-C", str(cwd), *args], check=True,
                   stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)


def main():
    n = int(sys.argv[1]) if len(sys.argv) > 1 else 1000
    if not BIN.exists():
        print("build first: go build -o bin/prohairesis ./cmd/prohairesis", file=sys.stderr)
        return 2

    work = pathlib.Path(tempfile.mkdtemp())
    try:
        env = dict(os.environ)
        env.update({
            "PROHAIRESIS_HOME": str(work / "state"),
            "GIT_CONFIG_GLOBAL": "/dev/null", "GIT_CONFIG_SYSTEM": "/dev/null",
            "GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@x",
            "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@x",
        })
        repo = work / "repo"
        (repo / "src").mkdir(parents=True)
        (repo / "src" / "a.txt").write_text("one\n")
        git(repo, "init", "-q")
        git(repo, "add", "-A")
        git(repo, "commit", "-qm", "init")

        def run(sub, payload):
            return subprocess.run(
                [str(BIN), "hook", sub], input=json.dumps(payload).encode(),
                env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)

        run("session-start", {"hook_event_name": "SessionStart",
                              "session_id": "bench", "cwd": str(repo)})

        # A read: the common case, and the one that takes no checkpoint. The
        # cost of a checkpoint is measured separately below, because averaging
        # the two would hide both.
        read = {"hook_event_name": "PostToolUse", "session_id": "bench",
                "cwd": str(repo), "tool_name": "Read",
                "tool_input": {"file_path": str(repo / "src" / "a.txt")},
                "tool_response": {"is_error": False}}

        print(f"measuring {n} invocations of: prohairesis hook post-tool")
        samples = []
        for _ in range(n):
            start = time.perf_counter()
            run("post-tool", read)
            samples.append((time.perf_counter() - start) * 1000)
        report(samples)

        # The checkpoint path, on its own. It runs synchronously and by design:
        # an effect that lands before its checkpoint is an effect outside the
        # only guarantee this project makes. What that costs should be visible,
        # not averaged away.
        print()
        print("the same, for an invocation that takes a checkpoint")
        write = dict(read, tool_name="Write")
        ckpt = []
        for i in range(min(n, 50)):
            (repo / "src" / f"b{i}.txt").write_text("x\n")
            # Backdate the last checkpoint so the interval has always elapsed.
            force_due(work)
            start = time.perf_counter()
            run("post-tool", write)
            ckpt.append((time.perf_counter() - start) * 1000)
        report(ckpt)
        return 0
    finally:
        shutil.rmtree(work, ignore_errors=True)


def force_due(work):
    """Age the most recent checkpoint so the debounce interval has passed."""
    sessions = work / "state" / "sessions"
    for meta in sessions.glob("*/meta.json"):
        doc = json.loads(meta.read_text())
        for c in doc.get("checkpoints", []):
            c["at"] = "2020-01-01T00:00:00Z"
        meta.write_text(json.dumps(doc))


def report(samples):
    xs = sorted(samples)
    if not xs:
        print("  no samples")
        return

    def pct(p):
        return xs[min(len(xs) - 1, int(p * (len(xs) - 1)))]

    print(f"  n     {len(xs)}")
    for label, value in (("p50", pct(0.50)), ("p90", pct(0.90)),
                         ("p99", pct(0.99)), ("max", xs[-1])):
        print(f"  {label}   {value:.1f}ms")
    print()
    print("  Recorded, not budgeted. See docs/METRICS.md.")


if __name__ == "__main__":
    sys.exit(main())
