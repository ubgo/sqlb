#!/usr/bin/env python3
"""Fail when an entry point taking a statement is not covered by the reuse test.

A builder METHOD may accumulate: calling Where twice meaning two predicates is
the design. A function that merely TAKES a statement may not write to it,
because a caller holds both values and cannot be asked to remember which half
is the mutable one.

scan.All broke that rule. It finished the caller's statement with the Reader's
columns, so a second call selected every column twice — legal SQL, so the
failure surfaced elsewhere — and two goroutines calling it raced on the same
slice. The fix is one clone; what is worth keeping is the class, because the
next such function will look exactly as innocent.

So every exported function in scan/ and exec/ whose parameters include a
statement must be named in the reuse test's ENTRY POINTS line. Adding one
without a test fails here rather than in production a year later.

Exit status is 1 if an entry point is uncovered, naming it.
"""

import os
import re
import sys

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

# Packages whose exported functions are entry points, and the file that must
# cover them. The test lives in contrib/driver-sqlite because that is the only
# module here that can execute a statement against a real database.
SOURCES = {"scan": "scan", "exec": "exec"}
TEST = os.path.join(REPO, "contrib", "driver-sqlite", "reuse_test.go")

# A function taking a statement: the parameter list mentions *stmt.Select, with
# or without its dialect type argument, or stmt.Query. Methods are skipped — a method on a builder is allowed to mutate
# its receiver, which is the distinction this whole check rests on.
FUNC = re.compile(r"^func ([A-Z]\w*)(?:\[[^\]]*\])?\(([^)]*)\)", re.M)
STATEMENT = re.compile(r"\*?stmt\.(Select|Query)(\[[^\]]*\])?\b")


def entry_points():
    """Yield (package, function) for every exported statement-taking function."""
    for pkg, rel in sorted(SOURCES.items()):
        d = os.path.join(REPO, rel)
        for name in sorted(os.listdir(d)):
            if not name.endswith(".go") or name.endswith("_test.go"):
                continue
            with open(os.path.join(d, name), encoding="utf-8") as fh:
                src = fh.read()
            for fn, params in FUNC.findall(src):
                if STATEMENT.search(params):
                    yield pkg, fn


def main():
    try:
        with open(TEST, encoding="utf-8") as fh:
            covered = fh.read()
    except FileNotFoundError:
        print(f"the reuse test is missing: {os.path.relpath(TEST, REPO)}")
        return 1

    found = list(entry_points())
    if not found:
        # The regex matching nothing would make every check below pass
        # vacuously, which is the failure mode this repository has already had
        # once in a source-scanning guard.
        print("no entry points found at all; the scan is broken, not the code")
        return 1

    missing = []
    for pkg, fn in found:
        if f"{pkg}.{fn}" not in covered:
            missing.append(f"{pkg}.{fn}")

    if missing:
        for name in missing:
            print(f"{name} takes a statement and is not covered by the reuse test")
        print()
        print("Add it to contrib/driver-sqlite/reuse_test.go, both in the")
        print("ENTRY POINTS line and as a subtest calling it twice on one value.")
        return 1

    print(f"ok: {len(found)} statement-taking entry points, all covered by the reuse test")
    return 0


if __name__ == "__main__":
    sys.exit(main())
