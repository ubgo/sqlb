#!/usr/bin/env python3
"""Print SQLite's grammar index as sqlite/testdata/lang-index.txt expects it.

The detector in sqlite/ddl_coverage_test.go classifies every entry of
https://sqlite.org/lang.html, so a statement SQLite has and sqlb cannot build is
visible. It reads that page on stdin and writes its entries one per line, verbatim,
with PRAGMA appended because the index links it from a separate page. The
fetch is curl's job, in `task detector:sqlite`, so certificate verification is
the system's rather than whatever this Python was installed with.

It exits non-zero rather than writing an empty list, because an empty list
would make the detector pass vacuously.
"""

import datetime
import html
import re
import sys

URL = "https://sqlite.org/lang.html"


def main():
    page = sys.stdin.read()
    entries = [html.unescape(e).strip()
               for e in re.findall(r"<a href=['\"]lang_[^'\"]*['\"][^>]*>([^<]*)", page)]
    entries = [e for e in entries if e]
    if not entries:
        print("sqlite-lang-index: parsed no entries from the page on stdin; expected " + URL, file=sys.stderr)
        return 1
    today = datetime.date.today().isoformat()
    print("# The entries of SQLite's grammar index, https://sqlite.org/lang.html,")
    print(f"# fetched {today}, one per line, verbatim. PRAGMA is appended from")
    print("# https://sqlite.org/pragma.html because the index links it separately.")
    print("# Regenerate with: task detector:sqlite")
    for e in entries:
        print(e)
    print("PRAGMA")
    return 0


if __name__ == "__main__":
    sys.exit(main())
