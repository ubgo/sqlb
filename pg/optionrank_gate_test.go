package pg_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestOptionRank_EveryOrderSensitiveClauseIsRanked keeps ddl.OptionRank's
// promise that an unranked clause is only ever a caller's own. It reads the
// source of the two engines whose grammar fixes an order and fails on any
// clause made so that it would render in the caller's order:
//
//   - a CREATE TABLE option made with NewTableClause or NewTableClauseOn at
//     PositionOption, in pg or mysql, rather than NewOptionClause;
//   - a Postgres column clause at PartAfterType made with NewColumnClause
//     rather than NewRankedColumnClause, since Postgres fixes STORAGE,
//     COMPRESSION and COLLATE to that order;
//   - a type whose Position method returns PositionOption and which has no
//     OptionRank method, which is how mysql.Partitioning is ranked.
//
// Without it the bug this fixes comes back the first time someone adds an
// option: it builds, it renders, and the server refuses it only when a caller
// happens to pass it in the wrong place. SQLite is not scanned because its
// grammar takes STRICT and WITHOUT ROWID in either order, measured.
//
// The matchers allow gofmt's alignment and type parameters, and the scan
// fails closed when it finds no ranked clause at all, because this repository
// has already shipped one source-scanning gate that matched nothing and passed
// vacuously ever after.
func TestOptionRank_EveryOrderSensitiveClauseIsRanked(t *testing.T) {
	unrankedOption := regexp.MustCompile(`ddl\.NewTableClause(?:On)?\s*\[\s*Dialect\s*\]\s*\(\s*ddl\.PositionOption`)
	unrankedAfterType := regexp.MustCompile(`ddl\.NewColumnClause\s*\[\s*Dialect\s*\]\s*\([^)]*ddl\.PartAfterType`)
	ranked := regexp.MustCompile(`ddl\.New(?:OptionClause(?:On)?|RankedColumnClause)\s*\[`)
	// A receiver is "(T)", "(*T)", "(r T)" or "(r *T)". The receiver name, when
	// present, must be followed by whitespace, so a lone type is captured
	// whole: the first version used \w* for the name, which on the unnamed
	// "(Partitioning)" backtracked and captured only "g" — the gate then named
	// a type called g, and passed on the unbroken source by the same accident.
	positionOption := regexp.MustCompile(`func\s*\(\s*(?:\w+\s+)?\*?\s*(\w+)(?:\[[^\]]*\])?\s*\)\s*Position\(\)\s*ddl\.Position\s*\{\s*return\s+ddl\.PositionOption\s*\}`)
	hasRank := func(src, typ string) bool {
		return regexp.MustCompile(`func\s*\(\s*(?:\w+\s+)?\*?\s*` + regexp.QuoteMeta(typ) + `(?:\[[^\]]*\])?\s*\)\s*OptionRank\(\)`).MatchString(src)
	}

	for _, dir := range []string{".", "../mysql"} {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		found := 0
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			src := string(b)
			found += len(ranked.FindAllString(src, -1))
			if loc := unrankedOption.FindStringIndex(src); loc != nil {
				t.Errorf("%s: a CREATE TABLE option made with NewTableClause renders in the caller's order; make it with ddl.NewOptionClause and a rank from the engine's rank table", f)
			}
			if dir == "." && unrankedAfterType.MatchString(src) {
				t.Errorf("%s: a Postgres column clause at PartAfterType renders in the caller's order; make it with ddl.NewRankedColumnClause and a colRank constant", f)
			}
			for _, m := range positionOption.FindAllStringSubmatch(src, -1) {
				if !hasRank(src, m[1]) {
					t.Errorf("%s: %s renders at PositionOption and has no OptionRank method", f, m[1])
				}
			}
		}
		if found == 0 {
			t.Fatalf("scanned %s and found no ranked clause at all; the matcher is broken, not the package clean", dir)
		}
	}
}
