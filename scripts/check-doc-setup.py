#!/usr/bin/env python3
"""Compile the Setup block of every per-package documentation page.

Each docs/<package>/README.md opens with a Setup section declaring the tables,
columns and helpers its snippets use, so a reader is never asked to infer where
a name came from. That block is Go source, and Go source in prose rots: a
renamed field or a changed signature leaves the page confidently wrong.

So the block is extracted into a real package and built. It is not run, because
a Setup block only declares; the snippets below it are checked separately, as
runnable Examples in examples/packages/.

Exit status is 1 if any block fails to compile, with the compiler's own message.
"""

import os
import re
import shutil
import subprocess
import sys
import tempfile

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
# A Setup block at any heading level, on any page under docs/. It was once
# only docs/<package>/README.md, which quietly left the Setup block added to
# docs/engines.md unchecked: the gate reported success while the snippet it was
# supposed to compile was never seen.
# The first Go fence after a Setup heading. Prose between the two is optional:
# requiring it made the pattern skip past the real block on a page that had
# none and capture a later fence, which then failed to compile for a reason
# that had nothing to do with the page.
SETUP = re.compile(r"#{2,4} Setup\n(?:(?!```)[\s\S])*?```go\n([\s\S]*?)```")


def blocks():
    """Yield (package name, Go source) for every page that has a Setup block.

    Walks every markdown file under docs/ rather than only the per-package
    READMEs, because a Setup block is worth compiling wherever it appears and
    the narrower version silently skipped one.
    """
    docs = os.path.join(REPO, "docs")
    for root, _, files in os.walk(docs):
        for name in sorted(files):
            if not name.endswith(".md"):
                continue
            path = os.path.join(root, name)
            with open(path, encoding="utf-8") as fh:
                found = SETUP.search(fh.read())
            if not found:
                continue
            rel = os.path.relpath(path, docs)
            # A package name Go will accept, derived from the page's path.
            pkg = re.sub(r"[^a-z0-9]", "", rel[:-3].replace("README", "").lower()) or "root"
            yield pkg, found.group(1)


def main():
    found = list(blocks())
    if not found:
        print("no Setup blocks found; has the docs layout changed?", file=sys.stderr)
        return 1

    work = tempfile.mkdtemp(prefix="sqlb-doc-setup-")
    try:
        with open(os.path.join(work, "go.mod"), "w", encoding="utf-8") as fh:
            fh.write(
                "module docsetup\n\ngo 1.24\n\n"
                "require github.com/ubgo/sqlb v0.1.0\n\n"
                f"replace github.com/ubgo/sqlb => {REPO}\n"
            )
        for pkg, src in found:
            d = os.path.join(work, pkg)
            os.makedirs(d, exist_ok=True)
            with open(os.path.join(d, "setup.go"), "w", encoding="utf-8") as fh:
                fh.write(f"package {pkg}setup\n\n{src}")

        env = dict(os.environ, GOFLAGS="-mod=mod", GOWORK="off")
        done = subprocess.run(
            ["go", "build", "./..."], cwd=work, env=env,
            capture_output=True, text=True,
        )
        if done.returncode != 0:
            print(done.stderr.rstrip(), file=sys.stderr)
            print(
                f"\nthe Setup block of one or more pages does not compile. "
                f"Fix the page, not this script: a snippet a reader cannot run "
                f"is worse than no snippet.",
                file=sys.stderr,
            )
            return 1
    finally:
        shutil.rmtree(work, ignore_errors=True)

    print(f"ok: {len(found)} documentation Setup blocks compile "
          f"({', '.join(p for p, _ in found)})")
    return 0


if __name__ == "__main__":
    sys.exit(main())
