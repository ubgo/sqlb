#!/usr/bin/env python3
"""Write a correctly merged coverage profile for one module.

Why this exists rather than `go test -coverpkg=./... -coverprofile=… ./...`:
that command silently loses data here. With -coverpkg every test binary emits
counters for every package in the module, and the profile the go command writes
for a multi-package run keeps one copy of each block rather than summing the
copies. The surviving copy is usually from a binary that never executed the
block, so a package with real tests reads as zero. Measured: `pg` alone reports
40.9%, and in the ./... run every one of its blocks is 0.

The failure is silent and it reads low, which is the dangerous direction: it
looks like missing tests, so the reflex is to go write tests that already
exist. Running each package separately and summing the counters is what the
merged profile was supposed to be.

Usage: covmerge.py <out.profile>
"""

import collections
import os
import subprocess
import sys
import tempfile


def main() -> int:
    if len(sys.argv) != 2:
        print("usage: covmerge.py <out.profile>", file=sys.stderr)
        return 2
    out = sys.argv[1]

    pkgs = subprocess.run(
        ["go", "list", "./..."], capture_output=True, text=True, check=True
    ).stdout.split()

    mode = "atomic"
    # A block is identified by "file:startline.col,endline.col numstmts"; the
    # count is the only part that differs between binaries, so summing the
    # counts per block is the whole merge.
    blocks: dict[str, int] = collections.defaultdict(int)
    order: list[str] = []
    failed: list[str] = []

    with tempfile.TemporaryDirectory() as d:
        for i, pkg in enumerate(pkgs):
            f = os.path.join(d, f"{i}.out")
            r = subprocess.run(
                ["go", "test", "-coverpkg=./...", "-covermode=atomic",
                 f"-coverprofile={f}", pkg],
                capture_output=True, text=True,
            )
            if r.returncode != 0:
                failed.append(pkg)
            if not os.path.exists(f):
                continue
            with open(f) as fh:
                for line_no, line in enumerate(fh):
                    line = line.strip()
                    if line_no == 0:
                        mode = line.split(":", 1)[1].strip() if ":" in line else mode
                        continue
                    if not line:
                        continue
                    key, count = line.rsplit(" ", 1)
                    if key not in blocks:
                        order.append(key)
                    blocks[key] += int(count)

    if failed:
        print("tests failed in: " + " ".join(failed), file=sys.stderr)
        return 1

    with open(out, "w") as fh:
        fh.write(f"mode: {mode}\n")
        for key in order:
            fh.write(f"{key} {blocks[key]}\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
