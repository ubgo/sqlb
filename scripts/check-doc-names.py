#!/usr/bin/env python3
"""Check that every symbol named in a documentation snippet actually exists.

The Setup block of each page is compiled (see check-doc-setup.py), but the
snippets below it are prose: fragments of a builder chain, not whole files, so
they cannot be built. That gap is not theoretical. The stmt page documented a
method called Clauses for a while; the method is Clause, and nothing caught it
because no tool had ever read that line.

This reads every Go fenced block in docs/ and checks two things against
the real source:

  pkg.Name    every qualified reference, against that package's exported names
  ).Method(   every chained method call, against the module's method names

The second is deliberately narrow. Without type information a method call
cannot be attributed to a receiver, so only calls chained onto the result of
another call are checked, which is what a builder chain looks like and where
this class of mistake lives. A method on a plain identifier is usually stdlib
and checking it produces noise rather than findings.

Exit status is 1 if a name is not found, naming the file and line.
"""

import os
import re
import sys

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

# Packages a snippet may refer to by name, and where their source lives.
PACKAGES = {
    "render": "render", "expr": "expr", "stmt": "stmt", "exec": "exec",
    "bind": "bind", "scan": "scan", "schema": "schema", "sqlb": ".",
    "ddl": "ddl",
    "postgres": "dialect/postgres",
    # "sqlite" is ambiguous and both readings are legitimate: dialect/sqlite
    # and sqlite/ are both imported under that name, so a page writing
    # sqlite.Dialect{} and one writing sqlite.Arrow(...) are each correct. The
    # union is the only answer that accepts both. Before this, the alias
    # resolved to dialect/sqlite alone, which has two exports, so every
    # construct in the sqlite package read as "does not exist" — undetected
    # because no page had yet named one.
    "sqlite": ["dialect/sqlite", "sqlite"],
    "clickhouse": "dialect/clickhouse", "pg": "pg", "ch": "ch",
    # "mysql" is two packages, as "sqlite" is: dialect/mysql and the mysql
    # construct package are both imported under that name.
    "mysql": ["dialect/mysql", "mysql"],
    # contrib modules, so their own READMEs are checked too. A module is a
    # separate go.mod, which changes nothing here: the check reads source.
    "structbind": "contrib/structbind", "debug": "contrib/debug",
    "migrate": "contrib/migrate", "driverpgx": "contrib/driver-pgx", "drivermysql": "contrib/driver-mysql", "vet": "contrib/vet",
    # driversqlite was missing here until 2026-09-20, so roughly ten snippets
    # naming it had never been read by anything — the same shape as a guard
    # whose matcher matches nothing and passes vacuously.
    "driversqlite": "contrib/driver-sqlite",
    # The cookbook's four packages, so the READMEs beside them are checked
    # like any other page. They are examples, not API, but a snippet that
    # names a function which no longer exists is just as wrong there.
    "authlite": "examples/cookbook/authlite", "phone": "examples/cookbook/phone",
    "workspace": "examples/cookbook/workspace", "app": "examples/cookbook/app",
}

# Every markdown file in the repository is read, discovered rather than
# listed. A hand-kept list is a contract enforced by convention: it was three
# entries long while a dozen pages naming API went unread, and every time a
# page was added someone had to remember. A file that names no API simply
# produces no findings, so discovery costs nothing and cannot fall behind.
#
# Skipped: .git, and the private docsi overlay, which is a symlink os.walk
# does not follow anyway.
SKIP_DIRS = {".git", "docsi", "node_modules", "testdata"}

# Names that look qualified but are not ours: stdlib, and a snippet's own vars.
IGNORE_QUALIFIERS = {
    "fmt", "strings", "errors", "context", "sql", "time", "os", "json",
    "t", "b", "a", "q", "s", "d", "e", "p", "u", "i", "r", "set", "users",
    "db", "tx", "ctx", "rows", "err", "cfg", "w", "g", "c", "m", "n", "v",
}

GO_BLOCK = re.compile(r"```go\n(.*?)```", re.S)

# A qualified name inside an inline code span, which is where prose puts an
# API name: `expr.Contains`, `stmt.Query`. Scanned as well as the Go fences,
# because a page can name a hundred symbols and contain no fence at all --
# every per-package README is that shape, so restricting the gate to fences
# meant those pages were listed as checked while nothing in them was read.
# Fenced blocks are removed before this runs, so a name is reported once.
INLINE_SPAN = re.compile(r"`([^`\n]+)`")

QUALIFIED = re.compile(r"\b([a-z][a-zA-Z0-9]*)\.([A-Z]\w*)")
# Only methods called on the result of another call, which is what a builder
# chain looks like. A method on a plain identifier cannot be attributed without
# type information, and most of those are stdlib (fmt.Println, b.WriteString),
# so checking them produces noise rather than findings.
# A chained method call, in the two shapes gofmt produces: on one line as
# ").Method(", or split across lines with the dot ending the previous line and
# the name beginning the next. A method on a plain identifier is skipped: it
# cannot be attributed without type information and is usually stdlib.
METHOD = re.compile(r"\)\.([A-Z]\w*)\(")
CHAINED = re.compile(r"^\s*([A-Z]\w*)\(")


def exported_names(rel):
    """Exported package-level names declared in a directory."""
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
        # grouped declarations, including bare names in an iota block
        out |= set(re.findall(r"^\t([A-Z]\w*)(?:\s|$)", src, re.M))
    return out


def all_methods():
    """Every exported method name on any type in the module."""
    out = set()
    for root, dirs, files in os.walk(REPO):
        dirs[:] = [d for d in dirs if d not in (".git", "contrib", "examples")]
        for name in files:
            if not name.endswith(".go") or name.endswith("_test.go"):
                continue
            with open(os.path.join(root, name), encoding="utf-8") as fh:
                out |= set(re.findall(r"^func \([^)]+\) ([A-Z]\w*)\(", fh.read(), re.M))
    return out


def main():
    api = {}
    for alias, rel in PACKAGES.items():
        dirs = rel if isinstance(rel, list) else [rel]
        names = set()
        for d in dirs:
            names |= exported_names(d)
        api[alias] = names
    methods = all_methods()
    problems = []

    paths = []
    for root, dirs, files in os.walk(REPO):
        dirs[:] = [d for d in dirs if d not in SKIP_DIRS]
        for name in sorted(files):
            if name.endswith(".md"):
                paths.append(os.path.join(root, name))
    paths.sort()
    if not paths:
        print("check-doc-names: found no markdown at all, which cannot be right",
              file=sys.stderr)
        return 1

    for path in paths:
        rel = os.path.relpath(path, REPO)
        with open(path, encoding="utf-8") as fh:
            text = fh.read()
        for i, span in enumerate(INLINE_SPAN.findall(GO_BLOCK.sub("", text))):
            for qual, sym in QUALIFIED.findall(span):
                if qual in IGNORE_QUALIFIERS or qual not in api:
                    continue
                if sym not in api[qual]:
                    problems.append(f"{rel}: `{span}` names {qual}.{sym}, which does not exist")

        for block in GO_BLOCK.findall(text):
            line0 = text[: text.index(block)].count("\n") + 1
            prev_ends_dot = False
            for i, line in enumerate(block.split("\n")):
                if line.lstrip().startswith("//"):
                    continue
                chained = prev_ends_dot
                prev_ends_dot = line.rstrip().endswith(".")
                where = f"{rel}:{line0 + i}"
                for qual, sym in QUALIFIED.findall(line):
                    if qual in IGNORE_QUALIFIERS or qual not in api:
                        continue
                    if sym not in api[qual]:
                        problems.append(f"{where}: {qual}.{sym} does not exist")
                names = METHOD.findall(line)
                if chained:
                    names += CHAINED.findall(line)
                for meth in names:
                    if meth not in methods:
                        problems.append(f"{where}: no exported method named {meth}")

    if problems:
        for p in problems:
            print(p, file=sys.stderr)
        print(f"\n{len(problems)} name(s) in documentation snippets do not exist. "
              f"Fix the documentation, not this script.", file=sys.stderr)
        return 1

    print("ok: every symbol named in a documentation snippet exists")
    return 0


if __name__ == "__main__":
    sys.exit(main())
