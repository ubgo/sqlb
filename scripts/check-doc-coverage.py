#!/usr/bin/env python3
"""Fail when a package's exports are not named in the documentation.

This is the inverse of check-doc-names.py and exists because that gate only
asks one of the two questions. It reads every Go fence in the documentation and
fails on a name that does not exist, which catches a page describing a method
that was renamed. Nothing asked the other direction: whether an export appears
in the documentation at all.

The difference is not academic. When this was first run, pg had 500 exported
names and 83 of them appeared in any document; pg.ArrayAgg was used in eleven
source files and named in none. A reader had no way to discover it existed
short of reading the source, and no gate anywhere said so.

Floors rather than a flat 100%: some names are genuinely internal vocabulary a
reader never types. An operator constant like OpLike is used by expr.Like and
nobody writes it; a slot name is a position in a grammar. Demanding every one
be documented would push prose into pages nobody reads, so each package carries
the floor it has earned and the floor only goes up. Raising one is how
documenting a package is recorded as done.
"""

import os
import re
import sys

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

# Package alias -> directory. The same map check-doc-names.py uses, plus the
# contrib modules, because a module boundary changes nothing about whether a
# name is documented.
PACKAGES = {
    "render": "render", "expr": "expr", "stmt": "stmt", "exec": "exec",
    "bind": "bind", "scan": "scan", "schema": "schema", "sqlb": ".",
    "ddl": "ddl",
    "postgres": "dialect/postgres", "sqlite-dialect": "dialect/sqlite", "mysql-dialect": "dialect/mysql",
    "clickhouse": "dialect/clickhouse", "pg": "pg", "ch": "ch",
    "sqlite": "sqlite", "mysql": "mysql",
    "structbind": "contrib/structbind", "debug": "contrib/debug",
    "migrate": "contrib/migrate", "driver-sqlite": "contrib/driver-sqlite",
    "driver-pgx": "contrib/driver-pgx", "driver-mysql": "contrib/driver-mysql", "vet": "contrib/vet",
}

# Minimum fraction of a package's exports that must be named somewhere in the
# documentation. A floor is a ratchet: when a package is documented, its floor
# is raised to what it then has, so the work cannot quietly come undone.
FLOORS = {
    "render": 1.00, "expr": 1.00, "stmt": 1.00, "exec": 1.00,
    "bind": 1.00, "scan": 1.00, "schema": 1.00, "sqlb": 1.00, "ddl": 1.00,
    "postgres": 1.00, "sqlite-dialect": 1.00, "mysql-dialect": 1.00, "clickhouse": 1.00,
    "pg": 1.00, "ch": 1.00, "sqlite": 1.00, "mysql": 1.00,
    "structbind": 1.00, "debug": 1.00, "migrate": 1.00, "driver-pgx": 1.00, "driver-mysql": 1.00, "vet": 1.00,
    "driver-sqlite": 1.00,
}

# Markdown that counts as documentation. docs/ plus the top-level pages and
# every contrib README, since a contrib README is the only documentation its
# module has.
EXTRA_DOCS = [
    "README.md",
    "contrib/structbind/README.md",
    "contrib/debug/README.md",
    "contrib/migrate/README.md",
    "contrib/driver-sqlite/README.md",
    "contrib/driver-pgx/README.md",
    "contrib/driver-mysql/README.md",
    "contrib/vet/README.md",
]


def exported_names(rel):
    """Exported package-level names declared in a directory.

    Deliberately the same extraction as check-doc-names.py: two gates
    disagreeing about what an export is would be worse than either being
    slightly wrong.
    """
    out = set()
    d = os.path.join(REPO, rel)
    if not os.path.isdir(d):
        return out
    for name in os.listdir(d):
        if not name.endswith(".go") or name.endswith("_test.go"):
            continue
        with open(os.path.join(d, name), encoding="utf-8") as fh:
            src = fh.read()
        out |= set(re.findall(r"^(?:func|type|var|const)\s+([A-Z]\w*)", src, re.M))
        out |= set(re.findall(r"^\t([A-Z]\w*)(?:\s|$)", src, re.M))
    return out


def documentation_text():
    parts = []
    for root, _, files in os.walk(os.path.join(REPO, "docs")):
        for name in sorted(files):
            if name.endswith(".md"):
                parts.append(open(os.path.join(root, name), encoding="utf-8").read())
    for extra in EXTRA_DOCS:
        path = os.path.join(REPO, extra)
        if os.path.exists(path):
            parts.append(open(path, encoding="utf-8").read())
    return "\n".join(parts)


def main():
    text = documentation_text()

    # A name counts when it appears as a word. The lookbehind excludes only a
    # word character, so the qualified spelling a document actually uses,
    # pg.ArrayAgg, counts for ArrayAgg.
    def documented(name):
        return re.search(rf"(?<!\w){re.escape(name)}\b", text) is not None

    # Self-check, because a bug here reads as "everything is documented" and
    # that is the failure nobody notices. Two names known to be in the docs
    # must register; if they do not, the matcher is broken and the run is
    # meaningless.
    for probe in ("MustNew", "Maps"):
        if not documented(probe):
            print(f"self-check failed: {probe} is in the docs but did not match; "
                  "the matcher is broken", file=sys.stderr)
            return 2

    failed = False
    rows = []
    for alias, rel in sorted(PACKAGES.items()):
        names = exported_names(rel)
        if not names:
            continue
        missing = sorted(n for n in names if not documented(n))
        have = len(names) - len(missing)
        ratio = have / len(names)
        floor = FLOORS.get(alias, 0.0)
        ok = ratio >= floor
        failed = failed or not ok
        rows.append((alias, len(names), have, ratio, floor, ok, missing))

    width = max(len(r[0]) for r in rows)
    for alias, total, have, ratio, floor, ok, missing in rows:
        mark = "ok  " if ok else "FAIL"
        print(f"{mark} {alias:<{width}}  {have:4}/{total:<4} {ratio:6.1%}  floor {floor:5.0%}")
        if not ok:
            shown = ", ".join(missing[:25])
            more = f" … and {len(missing) - 25} more" if len(missing) > 25 else ""
            print(f"       not named in any document: {shown}{more}")

    if failed:
        print("\na package's exports are not documented to its floor. Write the "
              "documentation, never lower the floor.", file=sys.stderr)
        return 1
    print("\nok: every package meets its documentation floor")
    return 0


if __name__ == "__main__":
    sys.exit(main())
