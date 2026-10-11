#!/usr/bin/env python3
"""Keep docs/ddl/not-built.md complete against the DDL detectors.

The detectors classify every statement each engine has: pg/ddl_coverage_test.go
(pgCommands, pgAlterTable), sqlite/ddl_coverage_test.go (sqliteIndex) and
mysql/ddl_coverage_test.go (mysqlDDL). This script reads those maps and does
two things.

It writes the appendix between the marker comments: every entry of every
detector, built ones included, with its status and reason, so the page is the
whole classification rather than the part somebody remembered to copy.

It checks the hand-written part. Every entry marked partial or left-out must be
named by a ### heading inside its engine's ## section, and that section must
hold a code fence, because an entry without a written workaround is exactly
the gap the page exists to close. The check runs the other way too: a heading
naming an entry that is no longer partial or left-out is a section describing
work that shipped, and fails until it is removed.

With --check it writes nothing and exits 1 on any difference, which is how
`task docs:not-built` keeps the page from rotting. It fails closed: a map that
parses to nothing, or a section missing entirely, is an error, because a gate
whose matcher matches nothing passes vacuously.
"""

import os
import re
import sys

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
PAGE = os.path.join(REPO, "docs", "ddl", "not-built.md")
BEGIN = "<!-- BEGIN GENERATED DETECTOR APPENDIX -->"
END = "<!-- END GENERATED DETECTOR APPENDIX -->"

# (page section heading, source file, map variable, what the entries are)
DETECTORS = [
    ("Postgres commands", "pg/ddl_coverage_test.go", "pgCommands", "every command in pg/testdata/commands-17.txt and commands-18.txt, the Postgres 17 and 18 SQL command lists"),
    ("Postgres ALTER TABLE forms", "pg/ddl_coverage_test.go", "pgAlterTable", "every action in pg/testdata/alter-table-17.txt and alter-table-18.txt, the Postgres 17 and 18 ALTER TABLE synopses"),
    ("SQLite", "sqlite/ddl_coverage_test.go", "sqliteIndex", "every entry in sqlite/testdata/lang-index.txt, SQLite's language index"),
    ("MySQL", "mysql/ddl_coverage_test.go", "mysqlDDL", "every topic in mysql/testdata/ddl-8.4.txt, MySQL 8.4's HELP 'Data Definition'"),
]

# The statuses that need a written section. Every other status is either built
# or declined for a stated reason that the appendix carries in full.
NEEDS_SECTION = ("partial", "left-out")

# A floor per map, so a reformat that breaks the entry pattern fails loudly.
MIN_ENTRIES = 30

ENTRY = re.compile(r'^\s*"([^"]+)":\s*"((?:[^"\\]|\\.)*)",\s*$')


def parse(path, var):
    """Return {entry: status line} for the map literal named var in path."""
    with open(os.path.join(REPO, path), encoding="utf-8") as fh:
        lines = fh.read().split("\n")
    start = None
    for i, line in enumerate(lines):
        if re.match(r"^var " + var + r" = map\[string\]string\{", line):
            start = i + 1
            break
    if start is None:
        sys.exit(f"{path}: map {var} not found; has the detector moved?")
    out = {}
    for line in lines[start:]:
        if line.startswith("}"):
            break
        m = ENTRY.match(line)
        if m:
            out[m.group(1)] = m.group(2).replace('\\"', '"')
    if len(out) < MIN_ENTRIES:
        sys.exit(f"{path}: {var} parsed to {len(out)} entries, under the floor of {MIN_ENTRIES}")
    return out


def split(status):
    kind, _, rest = status.partition(" ")
    return kind, rest


def cell(s):
    return s.replace("|", "\\|")


# LIMITS is the answer to the question the detectors structurally cannot ask.
#
# A detector entry reads `built <Name>`, and the test only checks that <Name> is
# exported. So `built` asserts that a statement HAS A CONSTRUCTOR, never that
# every form of it builds, and there is nowhere in that grammar to record a
# limit inside a built statement. On 2026-09-24 that hole was found to be
# hiding real absences behind `built` on every engine: MySQL's CREATE TABLE had
# no TEMPORARY, no LIKE and no AS SELECT; SQLite's virtual tables silently
# dropped a schema; Postgres accepted two object kinds ALTER EXTENSION refuses.
# The audits that followed closed what was buildable; these are what is left.
#
# Each entry is (statement, what is missing, why it stays missing). Every one
# was measured against the engine, not read from a grammar, and every reason is
# the engine ignoring the form, the operator owning it, or a better spelling
# already built — never "no consumer needs it", which is not a reason for a
# limit nobody can see. check_limits requires each to appear in the page, so a
# limit is recorded where a reader meets it rather than in a commit message.
LIMITS = {
    "Postgres": [
        ("CREATE TABLE", "OF type_name", "A typed table takes its column list from a composite type instead of declaring one, and ddl.CreateTable is a column list; AlterTable's OfType reaches the same state from a table whose columns already match."),
        ("CREATE TABLE", "GLOBAL and LOCAL before TEMPORARY", "Postgres documents both as accepted for compatibility and ignored, so a keyword that changes nothing would only mislead."),
        ("FOREIGN KEY", "MATCH PARTIAL", "Postgres parses it and refuses to implement it, so a constructor for it would be a compile-time promise the server breaks."),
        ("Everywhere it appears", "TABLESPACE", "A tablespace names a directory on a machine this code has never seen."),
        ("ALTER TABLE, ALTER INDEX", "SET STATISTICS, attribute options, CLUSTER ON", "These tune how the planner samples a table that already exists, and the value that is right depends on the data in one deployment rather than on the schema."),
        ("CREATE SCHEMA", "Nested schema_elements", "The nested form exists so the objects belong to the role AUTHORIZATION names; a library installing into its own schema runs its statements as that role or hands them over with SetOwner, and nesting every CREATE inside one statement would be a second way to write every statement this package already builds."),
        ("CREATE SCHEMA", "AUTHORIZATION with no name", "A schema whose name is not in the declaration is a schema no Table value can name."),
        ("CREATE FUNCTION", "BEGIN ATOMIC ... END", "It is a list of statements rather than a string, and the same function written with a body string under LangSQL is what this builds instead."),
        ("COMMENT ON", "The remaining object kinds", "COMMENT ON names a kind for every object Postgres has, and this package comments on the objects it can create; a comment on an object it cannot create is a comment on somebody else's object."),
        ("Every DROP", "Several objects in one statement", "SQLite's DROP TABLE takes one name, so every engine gets one statement per table and a runner can report which one failed."),
    ],
    "SQLite": [
        ("CHECK", "The conflict clause", "SQLite does not honour it: a column-level one is a syntax error and a table-level one is accepted and ignored, every action behaving as ABORT, so building it would promise behaviour the engine does not perform."),
        ("FOREIGN KEY", "MATCH", "SQLite parses MATCH and ignores it, so building it would promise an enforcement the engine does not perform."),
        ("PRAGMA integrity_check, quick_check", "The N argument", "The table-valued form the wrapped check is built on reads any argument as a table name and answers 'no such table: 5', measured on 3.50.3, and a cap on how many errors are listed is meaningless under MustReturnNoRows, which asks only whether there is one."),
        ("ALTER TABLE", "ALTER COLUMN SET/DROP NOT NULL", "SQLite's current documentation lists it and 3.50.3 does not have it; sqlite.Rebuild is how a column's nullability changes."),
    ],
    "MySQL": [
        ("Seven object statements", "DEFINER = user", "A MySQL account is an operator's grant rather than a schema a library ships; the security context a library can choose is SQL SECURITY, which is built."),
        ("ALTER TABLE", "ORDER BY", "It reorders rows physically on MyISAM and is ignored by InnoDB, the only engine a library's table uses; it is reachable through RawAlter."),
        ("CREATE TABLE, ALTER TABLE", "The storage-tuning table options", "Each is an operator's storage tuning rather than a schema a library ships, and every one is reachable through RawTableOption and RawAlter."),
        ("CREATE TABLE, ALTER TABLE, CREATE INDEX", "ENGINE_ATTRIBUTE and SECONDARY_ENGINE_ATTRIBUTE", "Each is a JSON blob whose meaning belongs to a storage engine this module does not target."),
        ("CREATE TABLE, ALTER TABLE", "COLUMN_FORMAT and STORAGE on a column", "Both are NDB Cluster storage placement and are ignored by InnoDB."),
        ("CREATE TABLE", "The inline column UNIQUE KEY and PRIMARY KEY", "The table-level PrimaryKey and Unique build the same constraint and, unlike the inline form, can be named, which is what DropKey and RenameKey need."),
        ("FOREIGN KEY", "MATCH FULL, PARTIAL, SIMPLE", "InnoDB parses it and ignores it, so building it would imply a guarantee no engine here provides."),
        ("FOREIGN KEY", "The optional index name", "MySQL names the backing index after the constraint when it is left out, which is what a declared TableConstraint already gives."),
        ("DROP TABLE, DROP VIEW", "RESTRICT and CASCADE", "MySQL parses both and does nothing with either."),
        ("DROP VIEW", "Several views in one statement", "DropTable's precedent is one statement per object, so a runner can report which one failed."),
        ("CREATE EVENT, ALTER EVENT", "DISABLE ON SLAVE", "It is the deprecated spelling of EventDisableOnReplica, which is built."),
        ("ALTER TABLE", "Per-partition DISCARD and IMPORT TABLESPACE", "Transportable tablespaces per partition are an operator's backup path; the table-level pair is built."),
        ("CREATE FUNCTION", "The loadable-function form, RETURNS INTEGER SONAME", "It is a different statement sharing the name, and it loads a shared library into the server."),
        ("CREATE PROCEDURE, CREATE FUNCTION", "A validated routine body", "The body is a caller-supplied string because MySQL's stored-program language is not SQL a dialect can check; the statement around it builds and nothing reads what is inside."),
        ("CREATE TRIGGER", "The procedural syntax itself", "StepBlock takes the IF, WHILE, REPEAT, CASE and DECLARE text as an unvalidated opener and closer, so what stays raw is the control flow; every statement inside a block is a typed step, which it was not before 2026-09-24."),
    ],
}

LIMITS_HEADING = "Limits beyond the detectors"


def check_limits(text):
    """Every recorded limit must appear in the page, so it is read and not filed."""
    errs = []
    body = sections(text.split(BEGIN)[0]).get(LIMITS_HEADING)
    if body is None:
        return [f"no '## {LIMITS_HEADING}' section"]
    for engine, rows in LIMITS.items():
        for stmt, missing, _ in rows:
            if missing not in body:
                errs.append(f"{engine}: the limit '{missing}' on {stmt} is recorded in LIMITS and is not in the page")
    return errs


def limits_table(engine):
    out = ["| Statement | What is missing | Why |", "|---|---|---|"]
    for stmt, missing, why in LIMITS[engine]:
        out.append(f"| {cell(stmt)} | {cell(missing)} | {cell(why)} |")
    return "\n".join(out)


def appendix(maps):
    out = [BEGIN, ""]
    for (section, path, var, what), entries in zip(DETECTORS, maps):
        counts = {}
        for st in entries.values():
            k = split(st)[0]
            counts[k] = counts.get(k, 0) + 1
        summary = ", ".join(f"{n} {k}" for k, n in sorted(counts.items()))
        out.append(f"### {section} detector")
        out.append("")
        out.append(f"`{var}` in `{path}` classifies {what}: {len(entries)} entries, {summary}.")
        out.append("")
        out.append("| Entry | Status | Construct or reason |")
        out.append("|---|---|---|")
        for name in sorted(entries):
            kind, rest = split(entries[name])
            out.append(f"| {cell(name)} | {kind} | {cell(rest)} |")
        out.append("")
    out.append(END)
    return "\n".join(out)


def sections(text):
    """Return {## heading: body} for the hand-written part of the page."""
    out = {}
    for m in re.finditer(r"^## (.+)\n([\s\S]*?)(?=^## |\Z)", text, re.M):
        out[m.group(1).strip()] = m.group(2)
    return out


def check_sections(text, maps):
    errs = []
    secs = sections(text.split(BEGIN)[0])
    for (section, path, var, _), entries in zip(DETECTORS, maps):
        want = {n for n, st in entries.items() if split(st)[0] in NEEDS_SECTION}
        body = secs.get(section)
        if body is None:
            errs.append(f"no '## {section}' section")
            continue
        named = {}
        for m in re.finditer(r"^### (.+)\n([\s\S]*?)(?=^### |\Z)", body, re.M):
            sub = m.group(2)
            for key in m.group(1).split(","):
                named[key.strip().strip("`")] = sub
        for n in sorted(want - named.keys()):
            errs.append(f"{section}: {n} is {split(entries[n])[0]} in {var} and has no ### section")
        for n in sorted(named.keys() - want):
            st = entries.get(n)
            errs.append(f"{section}: ### {n} names an entry that is {split(st)[0] if st else 'not in ' + var}; remove the section")
        for n in sorted(want & named.keys()):
            if "```" not in named[n]:
                errs.append(f"{section}: ### {n} has no code fence; write the workaround")
    return errs


def main():
    check = "--check" in sys.argv[1:]
    maps = [parse(p, v) for _, p, v, _ in DETECTORS]
    with open(PAGE, encoding="utf-8") as fh:
        text = fh.read()
    if BEGIN not in text or END not in text:
        sys.exit(f"{PAGE}: generated-appendix markers not found")
    head, rest = text.split(BEGIN, 1)
    _, tail = rest.split(END, 1)
    new = head + appendix(maps) + tail
    errs = check_sections(text, maps) + check_limits(text)
    if check:
        if new != text:
            errs.append("the appendix is stale; run task docs:not-built:gen")
        for e in errs:
            print(e, file=sys.stderr)
        return 1 if errs else 0
    with open(PAGE, "w", encoding="utf-8") as fh:
        fh.write(new)
    for e in errs:
        print(e, file=sys.stderr)
    return 1 if errs else 0


if __name__ == "__main__":
    sys.exit(main())
