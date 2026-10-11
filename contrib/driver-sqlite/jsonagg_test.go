package driversqlite_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	driversqlite "github.com/ubgo/sqlb/contrib/driver-sqlite"
	"github.com/ubgo/sqlb/dialect/sqlite"
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/scan"
	sqlitecon "github.com/ubgo/sqlb/sqlite"
	"github.com/ubgo/sqlb/stmt"
)

// TestJSONAgg_AggregatesInTheDatabase is the other half of nesting: instead of
// folding a join's repeated rows in Go, the database builds the array and
// returns one row per parent.
//
// Worth executing rather than rendering, because the interesting part is not
// the SQL but what comes back: the column arrives as text, and a parent with
// no children arrives as something that is not an empty array.
func TestJSONAgg_AggregatesInTheDatabase(t *testing.T) {
	ctx := context.Background()
	db, err := driversqlite.Memory()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	for _, q := range []string{
		`CREATE TABLE actors (id INTEGER NOT NULL, name TEXT NOT NULL)`,
		`CREATE TABLE films  (id INTEGER NOT NULL, actor_id INTEGER NOT NULL, title TEXT NOT NULL)`,
		`INSERT INTO actors VALUES (1,'Ada'), (2,'Bob'), (3,'Cyd')`,
		`INSERT INTO films VALUES (10,1,'Alpha'), (11,1,'Beta'), (12,3,'Gamma')`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}

	actors := expr.Table{Name: "actors", Alias: "a"}
	films := expr.Table{Name: "films", Alias: "f"}
	aID, aName := expr.Col[int64](actors, "id"), expr.Col[string](actors, "name")
	fActor := expr.Col[int64](films, "actor_id")
	fTitle := expr.Col[*string](films, "title")

	type actorOut struct {
		Name  string
		Films []*string
	}

	reader := scan.Map2(
		scan.Field(aName),
		scan.JSON[[]*string](sqlitecon.JSONGroupArray(fTitle)),
		func(n string, titles []*string) actorOut { return actorOut{n, titles} },
	)

	sel := stmt.NewSelect(sqlite.Dialect{}).
		From(actors).
		Join(stmt.Join{
			Type: stmt.JoinLeft, Table: films, Source: films,
			On: []expr.Predicate{expr.EqExpr(aID, fActor)},
		}).
		GroupBy(aID, aName).
		Order(expr.OrderBy(aID, expr.SortAsc))

	got, err := scan.All(ctx, db, sel, reader)
	if err != nil {
		t.Fatal(err)
	}

	// One row per actor, not one per film: that is the whole point.
	if len(got) != 3 {
		t.Fatalf("got %d rows, want one per actor", len(got))
	}

	// And the trap. An aggregate over a LEFT JOIN with no match does not
	// produce an empty array; it produces an array holding one NULL, because
	// the join supplied one all-NULL row and the aggregate faithfully
	// collected it. A caller reading len(Films) sees 1 for an actor with no
	// films unless the aggregate filters the nulls out.
	names := make([]string, len(got))
	counts := make([]int, len(got))
	for i, a := range got {
		names[i], counts[i] = a.Name, len(a.Films)
	}
	t.Logf("names=%v lengths=%v", names, counts)

	if got[1].Name != "Bob" {
		t.Fatalf("expected Bob second, got %q", got[1].Name)
	}
	if len(got[1].Films) != 1 || got[1].Films[0] != nil {
		t.Fatalf("Bob's aggregate should be a single NULL, not %v; "+
			"if this changed, the documented trap is wrong", got[1].Films)
	}

	// Ada's two films arrive as real values, in order.
	if len(got[0].Films) != 2 || *got[0].Films[0] != "Alpha" || *got[0].Films[1] != "Beta" {
		t.Fatalf("Ada = %v", got[0].Films)
	}
}

// TestJSON_NullColumnIsTheZeroValue pins that an SQL NULL in a JSON column is
// not an error.
//
// A LEFT JOIN with no match produces NULL, which means "no children". Failing
// there would make the common case the error case.
func TestJSON_NullColumnIsTheZeroValue(t *testing.T) {
	ctx := context.Background()
	db, err := driversqlite.Memory()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	if _, err := db.ExecContext(ctx, `CREATE TABLE t (doc TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO t VALUES (NULL)`); err != nil {
		t.Fatal(err)
	}

	tbl := expr.Table{Name: "t", Alias: "t"}
	doc := expr.Col[json.RawMessage](tbl, "doc")

	got, err := scan.All(ctx, db, stmt.NewSelect(sqlite.Dialect{}).From(tbl), scan.JSON[[]string](doc))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != nil {
		t.Fatalf("got %v, want one nil slice", got)
	}
	_ = fmt.Sprint(got)
}
