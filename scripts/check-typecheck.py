#!/usr/bin/env python3
"""Check that every deliberately wrong statement in examples/typecheck fails.

The negative build used to ask one question, "does bad.go compile?", and pass
when it did not. Go stops reporting after ten errors and one error fails a
build, so every statement past the first could have started compiling without
the gate noticing: a type-safety claim quietly becoming false. This builds the
file with -gcflags=-e, which reports every error, and requires an error on the
line of each deliberate statement, naming any line that compiles.

A deliberate statement is a line of bad.go that assigns to the blank
identifier and carries a trailing comment saying what is wrong with it, the
form every one of them is written in. The check fails closed: finding no such
line is an error, since a gate matching nothing passes vacuously.
"""

import os
import re
import subprocess
import sys

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
PKG = "./examples/typecheck/"
FILE = os.path.join(REPO, "examples", "typecheck", "bad.go")
DELIBERATE = re.compile(r"^\s*(_|_, _)\s*=\s*.+//\s*\S")
ERROR = re.compile(r"bad\.go:(\d+):\d+: ")


def main():
    with open(FILE, encoding="utf-8") as fh:
        lines = fh.read().split("\n")
    want = {i + 1: l.strip() for i, l in enumerate(lines) if DELIBERATE.match(l)}
    if not want:
        print("check-typecheck: no deliberate statements found in bad.go; has its form changed?", file=sys.stderr)
        return 1
    res = subprocess.run(
        ["go", "build", "-gcflags=-e", "-o", os.devnull, "-tags", "typecheck", PKG],
        cwd=REPO, capture_output=True, text=True,
    )
    if res.returncode == 0:
        print("check-typecheck: bad.go compiled; every type-safety claim in it is false", file=sys.stderr)
        return 1
    failed = {int(m.group(1)) for m in ERROR.finditer(res.stderr)}
    compiled = sorted(set(want) - failed)
    for n in compiled:
        print(f"examples/typecheck/bad.go:{n}: compiles, so this claim is false: {want[n]}", file=sys.stderr)
    if compiled:
        return 1
    print(f"typecheck: all {len(want)} deliberately wrong statements are rejected by the compiler")
    return 0


if __name__ == "__main__":
    sys.exit(main())
