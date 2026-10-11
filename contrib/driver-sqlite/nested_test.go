package driversqlite_test

import (
	"context"
	"fmt"
	"testing"

	driversqlite "github.com/ubgo/sqlb/contrib/driver-sqlite"
	"github.com/ubgo/sqlb/dialect/sqlite"
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/scan"
	"github.com/ubgo/sqlb/stmt"
)

// TestNestedResults_FromOneLeftJoin is the end-to-end answer to "can a join be
// folded into a tree of structs".
//
// One query, one pass over the rows, no N+1. The parent repeats once per child
// in the rowset and comes back holding its children; a parent with no children
// comes back with an empty slice rather than one zero-valued child, which is
// the case that separates a correct mapping from a plausible one.
//
// Rendering proves the SQL; only executing proves the fold, because the shape
// that breaks it is a real LEFT JOIN producing real NULLs.
func TestNestedResults_FromOneLeftJoin(t *testing.T) {
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
		// Bob deliberately has none, which is what the LEFT JOIN is for.
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
	// Nullable, because the LEFT JOIN may not match. The Go type says so, and
	// that is what makes GroupByPtr the obvious call rather than a guess.
	fID, fTitle := expr.Col[*int64](films, "id"), expr.Col[*string](films, "title")

	type row struct {
		ActorID   int64
		ActorName string
		FilmID    *int64
		FilmTitle *string
	}

	reader := scan.Map2(
		scan.Map2(scan.Field(aID), scan.Field(aName),
			func(id int64, n string) row { return row{ActorID: id, ActorName: n} }),
		scan.Map2(scan.Field(fID), scan.Field(fTitle),
			func(id *int64, t *string) row { return row{FilmID: id, FilmTitle: t} }),
		func(a, f row) row { a.FilmID, a.FilmTitle = f.FilmID, f.FilmTitle; return a },
	)

	sel := stmt.NewSelect(sqlite.Dialect{}).
		From(actors).
		Join(stmt.Join{
			Type: stmt.JoinLeft, Table: films, Source: films,
			On: []expr.Predicate{expr.EqExpr(aID, fActor)},
		}).
		Order(expr.OrderBy(aID, expr.SortAsc), expr.OrderBy(fID, expr.SortAsc))

	rows, err := scan.All(ctx, db, sel, reader)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 {
		t.Fatalf("the join returned %d flat rows, want 4", len(rows))
	}

	type actorOut struct {
		Name  string
		Films []string
	}
	got := scan.GroupBy(rows,
		func(r row) int64 { return r.ActorID },
		func(first row, g []row) actorOut {
			return actorOut{first.ActorName, scan.CollectPtr(g, func(r row) *string { return r.FilmTitle })}
		})

	want := "[{Ada [Alpha Beta]} {Bob []} {Cyd [Gamma]}]"
	if fmt.Sprint(got) != want {
		t.Fatalf("got  %v\nwant %s", got, want)
	}
}
