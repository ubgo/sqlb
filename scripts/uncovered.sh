#!/bin/sh
# uncovered.sh <coverprofile>
#
# Print every block in a Go coverage profile that no test executed.
#
# Why not `go tool cover -func`: that aggregates to whole functions, so a dead
# arm inside an otherwise-covered function is invisible. The profile is
# statement-level.
#
# Why the blocks are merged first: with -coverpkg=./... every test binary
# emits a row for every block in every instrumented package, so one block
# appears once per binary and is 0 in all the binaries that never touch it.
# Counting rows instead of blocks reports tens of thousands of false
# positives. A block counts as uncovered only when its highest count across
# all rows is still 0.
set -e
profile="${1:-cover.out}"
[ -f "$profile" ] || { echo "uncovered.sh: no such profile: $profile" >&2; exit 2; }

awk '
NR > 1 {
	key = $1                      # file:startLine.col,endLine.col
	stmts[key] = $2
	if (!(key in best) || $3 > best[key]) best[key] = $3
}
END {
	n = 0
	for (key in best) {
		if (best[key] == 0) {
			split(key, a, ":")
			printf "%s:%s  (%s statements)\n", a[1], a[2], stmts[key]
			n++
		}
	}
	printf "UNCOVERED_BLOCKS %d\n", n
}' "$profile" | sed -E 's#github.com/ubgo/sqlb/?##' | sort | sed -E 's#^UNCOVERED_BLOCKS ([0-9]+)$#uncovered blocks: \1#'
