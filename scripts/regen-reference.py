#!/usr/bin/env python3
"""Rewrite the generated reference table in each engine page, in place.

The tables live between marker comments at the end of docs/<engine>/README.md.
Rewriting between markers rather than appending is what makes this runnable
twice: an append-only generator produces a page with two references and no
error, which is the failure a generator is supposed to remove.
"""

import os
import subprocess
import sys

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
BEGIN = "<!-- BEGIN GENERATED REFERENCE -->"
END = "<!-- END GENERATED REFERENCE -->"
PAGES = {"pg": "docs/pg/README.md", "sqlite": "docs/sqlite/README.md",
         "ch": "docs/ch/README.md", "mysql": "docs/mysql/README.md"}


def main():
    for pkg, page in PAGES.items():
        path = os.path.join(REPO, page)
        with open(path, encoding="utf-8") as fh:
            text = fh.read()
        if BEGIN not in text or END not in text:
            print(f"{page}: no reference markers; the page must carry them", file=sys.stderr)
            return 1

        table = subprocess.run(
            [sys.executable, os.path.join(REPO, "scripts", "gen-engine-reference.py"),
             os.path.join(REPO, pkg)],
            capture_output=True, text=True, check=True,
        ).stdout.rstrip("\n")

        head = text[: text.index(BEGIN)]
        tail = text[text.index(END) + len(END):]
        with open(path, "w", encoding="utf-8") as fh:
            fh.write(head + table + tail)
        rows = table.count(f"| `{pkg}.")
        print(f"{page}: {rows} names")
    return 0


if __name__ == "__main__":
    sys.exit(main())
