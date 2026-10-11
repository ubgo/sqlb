#!/usr/bin/env python3
"""Generate the complete reference table for an engine package.

Why generated rather than written: pg has 500 exported names. A hand-kept list
of that size is a list that is wrong, and its being wrong is invisible, because
nothing reads a reference table looking for absences. Every export already
carries a why-and-invariant doc comment, so the summary exists in the source
and the only question is whether a reader can find it.

Forgetting to regenerate is the obvious failure of a generator, and it is
handled elsewhere: check-doc-coverage.py fails when an export is not named in
the documentation, so adding a construct without regenerating fails the build.
That is what makes this a generator rather than a convention.

Usage: gen-engine-reference.py <package-dir>
Writes the table to stdout, between the marker comments the page carries.
"""

import os
import re
import sys

BEGIN = "<!-- BEGIN GENERATED REFERENCE -->"
END = "<!-- END GENERATED REFERENCE -->"

# Human titles for the source files, which are already thematic. A file with no
# entry here falls back to its own name, so adding a file degrades to something
# readable rather than to a crash.
TITLES = {
    "agg": "Aggregates", "array": "Arrays", "ext": "Extension plumbing",
    "fts": "Full-text search", "json": "JSON and JSONB",
    "jsontable": "JSON_TABLE", "lock": "Advisory locks and row locking",
    "locktable": "LOCK TABLE", "math": "Maths", "merge": "MERGE",
    "misc": "Miscellaneous", "more": "Further constructs", "ops": "Operators",
    "pg": "Dialect", "range": "Ranges and multiranges",
    "recursive": "Recursive CTE options", "select": "Select clauses",
    "source": "Table sources", "sqljson": "SQL/JSON",
    "text": "Text", "time": "Dates and times", "truncate": "TRUNCATE",
    "window": "Window functions", "write": "Writes",
    "alterforms": "Later ALTER forms",
    "sqlite": "Dialect and core",
    "ch": "ClickHouse constructs",
}

DECL = re.compile(r"^(?:func|type|var|const)\s+([A-Z]\w*)", re.M)
GROUPED = re.compile(r"^\t([A-Z]\w*)(?:\s|$)", re.M)


def first_sentence(lines):
    """The first sentence of a doc comment, as a reference summary.

    Go doc comments open by restating the name; the reference table has the
    name in its own column, so that prefix is stripped to keep the cell short.
    """
    text = " ".join(line.lstrip("/").strip() for line in lines).strip()
    if not text:
        return ""
    # Cut at the first sentence end that is not an abbreviation.
    m = re.search(r"\.(\s|$)", text)
    if m:
        text = text[: m.start()]
    return text.strip()


def parse(path):
    """Every exported name in one file, with its doc summary, in source order.

    The name set comes from the same two regexes check-doc-coverage.py uses, so
    the reference cannot be missing a name the gate counts; the summary is
    best-effort on top of that. Doing it the other way round, letting the
    summary parser decide what an export is, is what made an earlier version of
    this script emit 444 of pg's 500 names while looking like it had finished.
    """
    with open(path, encoding="utf-8") as fh:
        lines = fh.read().split("\n")

    # Authoritative set, matching the gate exactly.
    src = "\n".join(lines)
    names = set(DECL.findall(src)) | set(GROUPED.findall(src))

    # Summaries and source order, best-effort.
    summaries, order = {}, []
    doc = []
    for line in lines:
        stripped = line.strip()
        if stripped.startswith("//"):
            doc.append(stripped)
            continue
        m = DECL.match(line) or GROUPED.match(line)
        if m and m.group(1) in names:
            name = m.group(1)
            if name not in summaries:
                order.append(name)
                summaries[name] = strip_opening(name, first_sentence(doc))
        if stripped:
            doc = []

    # Anything the ordered walk missed still gets a row, so the table is
    # complete even when the source is laid out in a way this does not expect.
    for name in sorted(names - set(order)):
        order.append(name)
        summaries.setdefault(name, "")

    return [(n, summaries[n]) for n in order]


def strip_opening(name, summary):
    """Drop the "Name is ..." opening, since the table has a name column."""
    return re.sub(rf"^{re.escape(name)}\s+(is|are|returns?|renders?|builds?|reports?|holds?)\s+",
                  "", summary)


def main():
    if len(sys.argv) != 2:
        print("usage: gen-engine-reference.py <package-dir>", file=sys.stderr)
        return 2
    pkg = sys.argv[1].rstrip("/")
    alias = os.path.basename(pkg)

    files = sorted(f for f in os.listdir(pkg)
                   if f.endswith(".go") and not f.endswith("_test.go"))

    # One row per name across the whole package, not per file. The extraction
    # both scripts share counts a struct field as an export, so a field called
    # Name can occur in two files; the gate dedupes by set and this must agree
    # with it, or the table appears to hold names the gate has never heard of.
    seen = set()

    print(BEGIN)
    print()
    print(f"Generated from the source by `task docs:reference`. Every exported name in `{alias}` appears here.")
    total = 0
    for f in files:
        entries = [(n, d) for n, d in parse(os.path.join(pkg, f)) if n not in seen]
        seen |= {n for n, _ in entries}
        if not entries:
            continue
        stem = f[:-3]
        print()
        print(f"### {TITLES.get(stem, stem)}")
        print()
        print("| Name | What it is |")
        print("|---|---|")
        for name, summary in entries:
            summary = summary.replace("|", "\\|") or "—"
            print(f"| `{alias}.{name}` | {summary} |")
        total += len(entries)
    print()
    print(f"{total} exported names.")
    print()
    print(END)
    return 0


if __name__ == "__main__":
    sys.exit(main())
