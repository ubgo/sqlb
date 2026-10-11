#!/usr/bin/env python3
"""Fail when a package has fewer end-to-end task tests than its floor.

Every other test in this repository asks "does this construct render, and does
it execute". That question can only confirm what somebody already thought of.
Every gap the library comparisons found — folding a join into a tree, reading
into a slice of structs, a select list chosen at runtime — was buildable the
whole time and invisible for the same reason: four hundred tests answered
"does this work" and none answered "how does a person show authors with their
books".

A task test is the other question. It does one job a person actually has, end
to end, using only what a consumer imports, and the finding is any task that
cannot be written without a hand-rolled loop or a raw string.

Floors are a ratchet, exactly like the documentation coverage gate: they only
go up, and raising one is how the work is recorded as done. A floor of zero is
not an exemption, it is a package nobody has written the paragraph for yet.

A task is executed, and only contrib/driver-sqlite can execute anything, so a
layer's task tests live there rather than in the layer's own directory. Each
layer claims one file by name; a claimed file counts for the layer that claimed
it and is excluded from the directory's own count, so no test holds up two
floors.

This counts test functions named TestTask_*, which is a convention rather than
a proof — a construct test can be renamed to look like a task test. The gate
makes the absence visible; only review makes the presence meaningful, and that
is stated here rather than implied.

Exit status is 1 if any package is below its floor.
"""

import os
import re
import sys

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

# A layer's task tests cannot live in the layer's own package, because a task
# is executed and only contrib/driver-sqlite can execute anything: SQLite needs
# no server, so it is the one module where a task runs on every machine. So a
# layer claims files in that directory by name, and a claimed file counts for
# the layer and not for the driver. Counting it for both would let one test
# hold up two floors, which is the ratchet quietly slipping.
#
# package -> (floor, directory, claimed file or None for "everything unclaimed")
FLOORS = {
    # The only module that can execute a statement, so the only place a task
    # can be proven rather than rendered. Three of the ten are the
    # cross-package composite, which is the claim the library exists for and
    # was rendered-only until 2026-09-20; three install, evolve and remove a
    # table through the DDL builders with the same Col declarations the
    # queries read, since 2026-09-21.
    "contrib/driver-sqlite": (10, "contrib/driver-sqlite", None),
    # One file per layer, written 2026-09-24, each doing the jobs that layer
    # exists for: statement shape, predicates over what a person typed, result
    # shapes, and a schema two packages contributed. These were zero, which is
    # not an exemption but a package nobody had written the paragraph for.
    "stmt": (3, "contrib/driver-sqlite", "task_stmt_test.go"),
    "scan": (3, "contrib/driver-sqlite", "task_scan_test.go"),
    "expr": (3, "contrib/driver-sqlite", "task_expr_test.go"),
    "schema": (3, "contrib/driver-sqlite", "task_schema_test.go"),
    # Postgres can execute a task too, for anyone with a server: the DDL gap
    # work of 2026-09-24 wrote its effects as tasks that read each one back
    # out of the catalogue rather than asserting a rendering, so this floor
    # exists and is not zero. It needs SQLB_PG_DSN, which is why it is not
    # where a layer's floor lives.
    "contrib/driver-pgx": (29, "contrib/driver-pgx", None),
    # MySQL was the one engine whose execution tests all answered "does this
    # construct run" and none answered "can a person do their job", which this
    # gate calls a package nobody has written the paragraph for. Written
    # 2026-09-24; the fifth of them surfaced a real gap, a trigger guard that
    # could only be written as a hand-rolled string.
    "contrib/driver-mysql": (5, "contrib/driver-mysql", None),
}

# Every file some layer claims, so the directory's own count can exclude them.
CLAIMED = {claim for _, (_, _, claim) in FLOORS.items() if claim}

TASK = re.compile(r"^func (TestTask_\w+)\(", re.M)


def count(rel, claim):
    d = os.path.join(REPO, rel)
    names = []
    if not os.path.isdir(d):
        return names
    for name in sorted(os.listdir(d)):
        if not name.endswith("_test.go"):
            continue
        if claim is None:
            if name in CLAIMED:
                continue
        elif name != claim:
            continue
        with open(os.path.join(d, name), encoding="utf-8") as fh:
            names.extend(TASK.findall(fh.read()))
    return names


def main():
    failed = False
    for pkg, (floor, rel, claim) in sorted(FLOORS.items()):
        found = count(rel, claim)
        status = "ok  "
        if len(found) < floor:
            status = "FAIL"
            failed = True
        where = rel if claim is None else f"{rel}/{claim}"
        print(f"{status} {pkg:<22} {len(found):>2} task tests   floor {floor}   in {where}")
        for name in found:
            print(f"       {name}")
    if failed:
        print()
        print("A package below its floor lost a task test. Restore it or, if the")
        print("task genuinely no longer exists, lower the floor deliberately.")
        return 1
    print()
    print("ok: every package meets its end-to-end task floor")
    return 0


if __name__ == "__main__":
    sys.exit(main())
