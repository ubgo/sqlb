package stmt_test

import (
	"strings"
	"testing"

	"github.com/ubgo/sqlb/dialect/postgres"
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/stmt"
)

var (
	files      = expr.Table{Name: "files", Alias: "f"}
	fileID     = expr.Col[int](files, "id")
	fileParent = expr.Col[*int](files, "parent_id")
	tree       = expr.Table{Name: "t", Alias: "t"}
	treeID     = expr.Col[int](tree, "id")
)

func seedAndStep() (*stmt.Select[postgres.Dialect], *stmt.Select[postgres.Dialect]) {
	seed := stmt.NewSelect(postgres.Dialect{}).
		Exprs(fileID).From(files).Where(expr.IsNull(fileParent))
	step := stmt.NewSelect(postgres.Dialect{}).
		Exprs(fileID).From(files).
		Join(stmt.Join{
			Type: stmt.JoinInner, Table: tree, Source: tree,
			On: []expr.Predicate{expr.EqExpr(fileParent, expr.Nullable(treeID))},
		})
	return seed, step
}

// TestWithRecursive_DoesNotMutateSeed pins a bug that made a shared seed
// render differently after being used.
//
// WithRecursive used to build its body with seed.UnionAll(step), and UnionAll
// mutates its receiver. A caller who built a seed, used it in a recursive CTE
// and then built that same seed again got a statement with a UNION ALL branch
// stapled to it. Nothing failed; the second statement was simply not the one
// that had been written.
func TestWithRecursive_DoesNotMutateSeed(t *testing.T) {
	seed, step := seedAndStep()

	before, err := seed.Build()
	if err != nil {
		t.Fatal(err)
	}

	if _, err := stmt.NewSelect(postgres.Dialect{}).
		WithRecursive("t", seed, step).
		Exprs(treeID).From(tree).Build(); err != nil {
		t.Fatal(err)
	}

	after, err := seed.Build()
	if err != nil {
		t.Fatal(err)
	}
	if before.SQL != after.SQL {
		t.Fatalf("the seed was mutated by being used\n before %s\n after  %s", before.SQL, after.SQL)
	}
}

// TestWithRecursive_SeedKeepsItsOwnLimit pins ent issue 2167, where a LIMIT and
// ORDER BY written for the seed of a recursive CTE ended up governing the whole
// recursion.
//
// The rule everywhere else is that a clause on the receiver of a set operation
// belongs to the compound; see the set-operation case in TestEverySlot. That is
// the right rule for a union a caller wrote by hand and the wrong one here,
// because a LIMIT on a recursive seed is unambiguously the seed's: it caps the
// starting rows, not the recursion. So a scoped seed is parenthesised.
func TestWithRecursive_SeedKeepsItsOwnLimit(t *testing.T) {
	seed, step := seedAndStep()
	seed = seed.Order(expr.OrderBy(fileID, expr.SortAsc)).Limit(5)

	q, err := stmt.NewSelect(postgres.Dialect{}).
		WithRecursive("t", seed, step).
		Exprs(treeID).From(tree).
		Limit(100).
		Build()
	if err != nil {
		t.Fatal(err)
	}

	// The seed's ORDER BY and LIMIT must sit inside its own parentheses.
	if !strings.Contains(q.SQL, `ORDER BY "f"."id" ASC LIMIT $1)`) {
		t.Errorf("the seed's clauses escaped its parentheses:\n%s", q.SQL)
	}
	// And the outer LIMIT must still be the outer statement's.
	if !strings.HasSuffix(q.SQL, `FROM "t" AS "t" LIMIT $2`) {
		t.Errorf("the outer LIMIT is not on the outer statement:\n%s", q.SQL)
	}
	// Two limits, bound in order, and neither duplicated.
	if len(q.Args) != 2 || q.Args[0] != 5 || q.Args[1] != 100 {
		t.Fatalf("args = %v, want [5 100]", q.Args)
	}
}

// TestWithRecursive_UnscopedSeedIsNotParenthesised keeps the fix from adding
// noise to the ordinary case, which is the shape almost every recursive CTE
// has.
func TestWithRecursive_UnscopedSeedIsNotParenthesised(t *testing.T) {
	seed, step := seedAndStep()

	q, err := stmt.NewSelect(postgres.Dialect{}).
		WithRecursive("t", seed, step).
		Exprs(treeID).From(tree).Build()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(q.SQL, `AS ((SELECT`) {
		t.Errorf("a seed with no clauses of its own should not be parenthesised:\n%s", q.SQL)
	}
}
