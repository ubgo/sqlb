package driversqlite_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/ubgo/sqlb"
	driversqlite "github.com/ubgo/sqlb/contrib/driver-sqlite"
	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/schema"
	"github.com/ubgo/sqlb/sqlite"
)

// The raw recipes in docs/ddl/not-built.md, executed. Each SQLite entry the
// DDL detector marks partial has a recipe there, and so does each escape
// hatch, and each one is here, run against a real SQLite and read back.

var (
	rrItems = sqlb.Table{Name: "rr_items", Alias: "i"}
	rrID    = sqlb.Col[int64](rrItems, "id")
	rrName  = sqlb.Col[string](rrItems, "name")
	rrBlob  = sqlb.Col[[]byte](rrItems, "payload")
)

func rrScalar(t *testing.T, db *sql.DB, q string) string {
	t.Helper()
	var s sql.NullString
	if err := db.QueryRowContext(context.Background(), q).Scan(&s); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return s.String
}

func rrMemory(t *testing.T) *sql.DB {
	t.Helper()
	db, err := driversqlite.Memory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestRawRecipes_SQLite(t *testing.T) {
	d := sqlite.Dialect{}
	ctx := context.Background()

	t.Run("CREATE_VIRTUAL_TABLE", func(t *testing.T) {
		db := rrMemory(t)
		// geopoly: a module the builders do not write, as a Script.
		run(t, db, ddl.Script{{SQL: `CREATE VIRTUAL TABLE IF NOT EXISTS ` + d.QuoteIdent("rr_shapes") + ` USING geopoly()`, Transactional: true}}, nil)
		if _, err := db.ExecContext(ctx, `INSERT INTO rr_shapes (_shape) VALUES ('[[0,0],[10,0],[10,10],[0,10],[0,0]]')`); err != nil {
			t.Fatal(err)
		}
		if got := rrScalar(t, db, `SELECT count(*) FROM rr_shapes WHERE geopoly_contains_point(_shape, 5, 5)`); got != "1" {
			t.Errorf("geopoly hit %q", got)
		}
	})

	t.Run("PRAGMA", func(t *testing.T) {
		db := rrMemory(t)
		run(t, db, ddl.Script{{SQL: `PRAGMA auto_vacuum = INCREMENTAL`}}, nil)
		if got := rrScalar(t, db, `PRAGMA auto_vacuum`); got != "2" {
			t.Errorf("auto_vacuum %q, want 2 (incremental)", got)
		}
	})

	t.Run("hatches", func(t *testing.T) {
		db := rrMemory(t)
		// A column clause in the column's own definition, a table element, a
		// table option, and a raw type, all on one built CREATE TABLE.
		nocase := ddl.NewColumnClause[sqlite.Dialect](rrName, ddl.PartAfterType, func(*sqlb.Args) string { return `COLLATE NOCASE` })
		element := ddl.NewTableClause[sqlite.Dialect](ddl.PositionElement, func(a *sqlb.Args) string {
			return `CHECK (length(` + rrName.SQL(a) + `) > 0)`
		})
		option := ddl.NewTableClause[sqlite.Dialect](ddl.PositionOption, func(*sqlb.Args) string { return `WITHOUT ROWID` })
		create := sqlite.CreateTable(rrItems).
			Column(ddl.Column(rrID), ddl.Column(rrName), ddl.Column(rrBlob).As(ddl.RawType[[]byte]("BLOB"))).
			Clause(sqlite.PrimaryKey(rrID), nocase, element, option)
		s, err := create.Build()
		run(t, db, s, err)
		if _, err := db.ExecContext(ctx, `INSERT INTO rr_items VALUES (1, 'Ada', x'00')`); err != nil {
			t.Fatal(err)
		}
		if got := rrScalar(t, db, `SELECT count(*) FROM rr_items WHERE name = 'ADA'`); got != "1" {
			t.Errorf("NOCASE match %q", got)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO rr_items VALUES (2, '', x'00')`); err == nil {
			t.Error("the raw CHECK did not refuse an empty name")
		}
		if got := rrScalar(t, db, `SELECT "wr" FROM pragma_table_list WHERE name = 'rr_items'`); got != "1" {
			t.Errorf("WITHOUT ROWID %q", got)
		}

		// An ALTER action the builders do not write, and a literal quoted by
		// the dialect for a position that takes no parameter.
		lit, err := d.QuoteLiteral("it's")
		if err != nil {
			t.Fatal(err)
		}
		action := ddl.NewAlterClause[sqlite.Dialect](func(*sqlb.Args) string { return `ADD COLUMN "note" TEXT NOT NULL DEFAULT ` + lit })
		s, err = sqlite.AlterTable(rrItems).Clause(action).Build()
		run(t, db, s, err)
		if got := rrScalar(t, db, `SELECT note FROM rr_items`); got != "it's" {
			t.Errorf("note %q", got)
		}
	})
}

// TestUpsert_NoTargetOnTheLastClause backs sqlite's zero-value exemption for
// Conflict: a Conflict{} is ON CONFLICT with no conflict target, which SQLite
// 3.35 and later accepts on the last ON CONFLICT clause, DO UPDATE included
// (https://www.sqlite.org/lang_upsert.html). Executed, not read: the second
// insert of the same key updates the row.
func TestUpsert_NoTargetOnTheLastClause(t *testing.T) {
	db := rrMemory(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `CREATE TABLE rr_items (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"first", "second"} {
		q, err := sqlite.Insert(rrItems).Row(sqlb.Set(rrID, int64(1)), sqlb.Set(rrName, name)).
			Clause((&sqlite.Conflict{}).DoUpdate(sqlb.SetExpr(rrName, sqlite.Excluded(rrName))).Clause()).Build()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, q.SQL, q.Args...); err != nil {
			t.Fatalf("%s: %v", q.SQL, err)
		}
	}
	if got := rrScalar(t, db, `SELECT name FROM rr_items WHERE id = 1`); got != "second" {
		t.Fatalf("the targetless DO UPDATE did not update: %q", got)
	}
}

// TestDDL_SQLiteJSONDefaultExecutes inserts a row that takes a JSON column's
// default, and adds a second JSON column to a table with rows, which SQLite
// allows for this constant expression, and reads both back as JSON.
func TestDDL_SQLiteJSONDefaultExecutes(t *testing.T) {
	db := rrMemory(t)
	tb := sqlb.Table{Name: "jd", Alias: "t"}
	id := sqlb.Col[int64](tb, "id")
	doc := sqlb.Col[json.RawMessage](tb, "doc")
	more := sqlb.Col[json.RawMessage](tb, "more")
	js := ddl.Portable[json.RawMessage](schema.TypeJSON)
	s, err := sqlite.CreateTable(tb).Column(ddl.Column(id), ddl.Column(doc).As(js).Default(json.RawMessage(`{"a":1}`))).Build()
	run(t, db, s, err)
	if _, err := db.Exec(`INSERT INTO jd (id) VALUES (1)`); err != nil {
		t.Fatal(err)
	}
	s, err = sqlite.AlterTable(tb).Action(ddl.AddColumn(ddl.Column(more).As(js).Default(json.RawMessage(`[]`)))).Build()
	run(t, db, s, err)
	if got := rrScalar(t, db, `SELECT json_extract(doc, '$.a') || '|' || more FROM jd`); got != "1|[]" {
		t.Errorf("defaults read back %q", got)
	}
}

// TestDDL_SQLiteRTreeAndApplicationID creates an R*Tree through the builder,
// answers an overlap query from it, does the same with integer coordinates,
// and sets and reads back the application id.
func TestDDL_SQLiteRTreeAndApplicationID(t *testing.T) {
	db := rrMemory(t)
	ctx := context.Background()
	boxes := sqlb.Table{Name: "rt_boxes", Alias: "b"}
	id := sqlb.Col[int64](boxes, "id")
	minX, maxX := sqlb.Col[float64](boxes, "min_x"), sqlb.Col[float64](boxes, "max_x")
	minY, maxY := sqlb.Col[float64](boxes, "min_y"), sqlb.Col[float64](boxes, "max_y")
	s, err := sqlite.CreateRTree(boxes).IfNotExists().Columns(id, sqlite.Dimension(minX, maxX), sqlite.Dimension(minY, maxY)).Build()
	run(t, db, s, err)
	run(t, db, s, err) // IF NOT EXISTS: a second run is a no-op
	if _, err := db.ExecContext(ctx, `INSERT INTO rt_boxes VALUES (1, 0, 10, 0, 10), (2, 20, 30, 20, 30)`); err != nil {
		t.Fatal(err)
	}
	var hit int64
	q, err := sqlite.Select().Columns(sqlb.Bind(id, &hit)).From(boxes).
		Where(sqlb.LessEq(minX, 5.0), sqlb.GreaterEq(maxX, 5.0), sqlb.LessEq(minY, 5.0), sqlb.GreaterEq(maxY, 5.0)).Build()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlb.One(ctx, db, q); err != nil || hit != 1 {
		t.Fatalf("the box containing (5, 5): %d, %v", hit, err)
	}
	grid := sqlb.Table{Name: "rt_grid"}
	gid := sqlb.Col[int64](grid, "id")
	lo, hi := sqlb.Col[int32](grid, "lo"), sqlb.Col[int32](grid, "hi")
	s, err = sqlite.CreateRTree(grid).Integer().Columns(gid, sqlite.Dimension(lo, hi)).Build()
	run(t, db, s, err)
	if _, err := db.ExecContext(ctx, `INSERT INTO rt_grid VALUES (1, 3, 7)`); err != nil {
		t.Fatal(err)
	}
	if got := rrScalar(t, db, `SELECT typeof(lo) FROM rt_grid`); got != "integer" {
		t.Errorf("rtree_i32 coordinate stored as %q", got)
	}
	s, err = sqlite.SetApplicationID(0x53514C42).Build()
	run(t, db, s, err)
	if got := rrScalar(t, db, `PRAGMA application_id`); got != "1397836866" {
		t.Errorf("application id %q", got)
	}
}
