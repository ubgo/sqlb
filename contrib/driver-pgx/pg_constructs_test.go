package driverpgx_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/pg"
	"github.com/ubgo/sqlb/render"
)

// The construct catalogue: pg constructs built and executed against a real
// Postgres, the way contrib/driver-sqlite executes every sqlite helper.
//
// Every case here is a statement that was, until this file, checked as
// rendered text only. Executing it is what proves the text is Postgres, and
// the fixture is chosen to include the class of mistake SQLite tolerates and
// Postgres does not: tables declared without an alias, as a CTE reference is.
//
// Each case runs inside a transaction that is rolled back, so a lock, a write
// or a DDL statement leaves nothing behind and the cases cannot depend on one
// another. The count of constructs this file covers is stated in
// docs/status.md rather than implied; it is not all five hundred.

var (
	catUser  = sqlb.Table{Name: "cat_user", Alias: "u"}
	uID      = sqlb.Col[int64](catUser, "id")
	uName    = sqlb.Col[string](catUser, "name")
	uTags    = sqlb.Col[[]string](catUser, "tags")
	uMeta    = sqlb.Col[json.RawMessage](catUser, "meta")
	uAge     = sqlb.Col[int32](catUser, "age")
	uCreated = sqlb.Col[time.Time](catUser, "created")
	uScore   = sqlb.Col[float64](catUser, "score")

	catOrder = sqlb.Table{Name: "cat_order", Alias: "o"}
	oID      = sqlb.Col[int64](catOrder, "id")
	oUser    = sqlb.Col[int64](catOrder, "user_id")
	oTotal   = sqlb.Col[int64](catOrder, "total")

	// Deliberately unaliased: the shape that found the empty-alias bug.
	bare     = sqlb.Table{Name: "cat_user"}
	bareID   = sqlb.Col[int64](bare, "id")
	bareName = sqlb.Col[string](bare, "name")
)

var catalogueDDL = []string{
	`DROP TABLE IF EXISTS cat_order`,
	`DROP TABLE IF EXISTS cat_user`,
	`CREATE TABLE cat_user (
		id BIGINT PRIMARY KEY, name TEXT NOT NULL, tags TEXT[] NOT NULL DEFAULT '{}',
		meta JSONB NOT NULL DEFAULT '{}', age INT NOT NULL, created TIMESTAMPTZ NOT NULL DEFAULT now(),
		score DOUBLE PRECISION NOT NULL DEFAULT 0)`,
	`CREATE TABLE cat_order (id BIGINT PRIMARY KEY, user_id BIGINT NOT NULL REFERENCES cat_user(id), total BIGINT NOT NULL)`,
	`DROP TABLE IF EXISTS cat_defaults`,
	`CREATE TABLE cat_defaults (id BIGSERIAL PRIMARY KEY, note TEXT NOT NULL DEFAULT 'x')`,
	`INSERT INTO cat_user (id, name, tags, meta, age, score) VALUES
		(1, 'ada', '{go,sql}', '{"plan":"pro","n":3}', 36, 1.5),
		(2, 'bob', '{go}', '{"plan":"free"}', 41, 2.5),
		(3, 'cyd', '{}', '{"plan":"pro","n":7}', 29, 3.5)`,
	`INSERT INTO cat_order (id, user_id, total) VALUES (10, 1, 120), (11, 1, 80), (12, 2, 300)`,
	`DROP TABLE IF EXISTS cat_misc`,
	`CREATE TABLE cat_misc (
		id BIGINT PRIMARY KEY, nums INT[] NOT NULL, body TEXT NOT NULL, doc JSONB NOT NULL,
		blob BYTEA NOT NULL, uid UUID NOT NULL, flag BOOLEAN NOT NULL, bits BIT(8) NOT NULL,
		span INT8RANGE NOT NULL, period TSTZRANGE NOT NULL, iv INTERVAL NOT NULL, day DATE NOT NULL,
		fts TSVECTOR NOT NULL, pct REAL NOT NULL)`,
	`INSERT INTO cat_misc VALUES
		(1, '{3,1,2}', 'The quick brown fox', '{"a":{"b":[1,2,3]},"tags":["x","y"],"n":null}', '\x00ff', '0192e8a0-7b6a-7000-8000-000000000001', true, B'10110000', '[1,10)', '[2026-01-01,2026-02-01)', '1 day 2 hours', '2026-03-04', to_tsvector('english', 'The quick brown fox'), 0.5),
		(2, '{}', 'lazy dogs', '[1,"two",{"three":3}]', '\x', '0192e8a0-7b6a-7000-8000-000000000002', false, B'00000001', 'empty', '(,)', '3 weeks', '2025-12-31', to_tsvector('english', 'lazy dogs'), 1.5)`,
}

var (
	catMisc = sqlb.Table{Name: "cat_misc", Alias: "m"}
	mID     = sqlb.Col[int64](catMisc, "id")
	mNums   = sqlb.Col[[]int32](catMisc, "nums")
	mBody   = sqlb.Col[string](catMisc, "body")
	mDoc    = sqlb.Col[json.RawMessage](catMisc, "doc")
	mBlob   = sqlb.Col[[]byte](catMisc, "blob")
	mUID    = sqlb.Col[string](catMisc, "uid")
	mFlag   = sqlb.Col[bool](catMisc, "flag")
	mBits   = sqlb.Col[pg.BitString](catMisc, "bits")
	mSpan   = sqlb.Col[pg.Range[int64]](catMisc, "span")
	mPeriod = sqlb.Col[pg.Range[time.Time]](catMisc, "period")
	mIv     = sqlb.Col[pg.Interval](catMisc, "iv")
	mDay    = sqlb.Col[time.Time](catMisc, "day")
	mFts    = sqlb.Col[pg.TSVector](catMisc, "fts")
	mPct    = sqlb.Col[float32](catMisc, "pct")
)

// expr executes SELECT e FROM the misc table; pred executes a WHERE on it.
// Every catalogue entry is one of the two unless it needs a source.
func expr(t *testing.T, db *sql.DB, name string, e sqlb.SQLer) {
	t.Helper()
	executes(t, db, name, sel().Exprs(e).From(catMisc))
}

func pred(t *testing.T, db *sql.DB, name string, p sqlb.Predicate) {
	t.Helper()
	executes(t, db, name, sel().Exprs(mID).From(catMisc).Where(p))
}

// serverVersion is the server's version number, read once per process; a
// construct that needs a newer server than this skips loudly.
var serverVersion int

func versionOf(t *testing.T, db *sql.DB) int {
	t.Helper()
	if serverVersion == 0 {
		if err := db.QueryRowContext(context.Background(), `SHOW server_version_num`).Scan(&serverVersion); err != nil {
			t.Fatalf("server_version_num: %v", err)
		}
	}
	return serverVersion
}

// since is executes for a construct that exists only from a given major
// version: on an older server it skips and says so, rather than passing.
func since(t *testing.T, db *sql.DB, major int, name string, b built) {
	t.Helper()
	if v := versionOf(t, db); v < major*10000 {
		t.Run(name, func(t *testing.T) {
			t.Skipf("SKIPPED, NOT PASSED: %s needs Postgres %d, server is %d", name, major, v/10000)
		})
		return
	}
	executes(t, db, name, b)
}

// objExpr is expr restricted to the row whose doc is an object, for the
// path functions that reject an array document.
func objExpr(t *testing.T, db *sql.DB, name string, e sqlb.SQLer) {
	t.Helper()
	executes(t, db, name, sel().Exprs(e).From(catMisc).Where(pg.HasKey(mDoc, "a")))
}

// fromSource executes SELECT e FROM the misc table CROSS JOIN a function
// source, which is how a correlated set-returning call is written.
func fromSource(t *testing.T, db *sql.DB, name string, src sqlb.Source, e sqlb.SQLer) {
	t.Helper()
	executes(t, db, name, sel().Exprs(e).From(catMisc).Join(sqlb.Join{Type: sqlb.JoinCross, Source: src}))
}

var pgd = sqlb.PostgresDialect{}

func sel() *sqlb.Select[sqlb.PostgresDialect] { return sqlb.NewSelect(pgd) }

type built interface{ Build() (sqlb.Query, error) }

// executes runs one built statement inside a transaction that is rolled back,
// and requires it to succeed. A statement that returns rows is drained so a
// bad scan or a late error surfaces too.
func executes(t *testing.T, db *sql.DB, name string, b built) {
	t.Helper()
	t.Run(name, func(t *testing.T) {
		q, err := b.Build()
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		ctx := context.Background()
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback() }()
		rows, err := tx.QueryContext(ctx, q.SQL, q.Args...)
		if err != nil {
			t.Fatalf("execute %s: %v\nSQL: %s\nargs: %v", name, err, q.SQL, q.Args)
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("rows: %v", err)
		}
	})
}

// catalogue opens the database or skips, creates the fixture tables and
// drops them when the test ends. Every catalogue file starts with it.
func catalogue(t *testing.T) *sql.DB {
	t.Helper()
	db := openOrSkip(t)
	ctx := context.Background()
	for _, ddl := range catalogueDDL {
		if _, err := db.ExecContext(ctx, ddl); err != nil {
			t.Fatalf("setup %q: %v", ddl, err)
		}
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx, `DROP TABLE IF EXISTS cat_misc`)
		_, _ = db.ExecContext(ctx, `DROP TABLE IF EXISTS cat_defaults`)
		_, _ = db.ExecContext(ctx, `DROP TABLE IF EXISTS cat_order`)
		_, _ = db.ExecContext(ctx, `DROP TABLE IF EXISTS cat_user`)
	})
	return db
}

func TestPGConstructs_Execute(t *testing.T) {
	db := catalogue(t)

	// --- the empty-alias class, on Postgres specifically ---
	executes(t, db, "unaliased table", sel().Exprs(bareID, bareName).From(bare).Where(sqlb.Eq(bareID, int64(1))))
	regional := sqlb.Table{Name: "regional"}
	rName := sqlb.Col[string](regional, "name")
	rTotal := sqlb.Col[int64](regional, "revenue")
	executes(t, db, "unaliased CTE reference",
		sel().With("regional", sel().Exprs(uName, sqlb.As(sqlb.Sum(oTotal), "revenue")).From(catUser).
			Join(sqlb.Join{Type: sqlb.JoinInner, Table: catOrder, On: []sqlb.Predicate{sqlb.EqExpr(oUser, uID)}}).
			GroupBy(uName)).
			Exprs(rName, rTotal).From(regional).Where(sqlb.Greater(rTotal, 100)))

	// --- select clauses ---
	executes(t, db, "ForUpdate", sel().Exprs(uID).From(catUser).Where(sqlb.Eq(uID, int64(1))).Clause(pg.ForUpdate()))
	executes(t, db, "ForUpdate NoWait", sel().Exprs(uID).From(catUser).Clause(pg.ForUpdate(pg.NoWait())))
	executes(t, db, "ForUpdate SkipLocked", sel().Exprs(uID).From(catUser).Clause(pg.ForUpdateSkipLocked()))
	executes(t, db, "ForShare", sel().Exprs(uID).From(catUser).Clause(pg.ForShare()))
	executes(t, db, "ForNoKeyUpdate", sel().Exprs(uID).From(catUser).Clause(pg.ForNoKeyUpdate()))
	executes(t, db, "ForKeyShare", sel().Exprs(uID).From(catUser).Clause(pg.ForKeyShare()))
	executes(t, db, "DistinctOn", sel().Clause(pg.DistinctOn(uAge)).Exprs(uAge, uName).From(catUser).Order(sqlb.OrderBy(uAge, sqlb.SortAsc), sqlb.OrderBy(uName, sqlb.SortAsc)))
	executes(t, db, "FetchFirst with ties", sel().Exprs(uName).From(catUser).Order(sqlb.OrderBy(uAge, sqlb.SortAsc)).Clause(pg.FetchFirst(2, true)))
	executes(t, db, "TableSample", sel().Exprs(uID).From(catUser).Clause(pg.TableSample(pg.SampleSystem, 50, 7)))
	executes(t, db, "LimitAll", sel().Exprs(uID).From(catUser).Clause(pg.LimitAll()))
	executes(t, db, "IntersectAll", pg.IntersectAll(sel().Exprs(uID).From(catUser), sel().Exprs(oUser).From(catOrder)))
	executes(t, db, "ExceptAll", pg.ExceptAll(sel().Exprs(uID).From(catUser), sel().Exprs(oUser).From(catOrder)))

	// --- sources ---
	// A bare bound parameter in a VALUES list has no type context on Postgres
	// and is read as text; the first row casts, and the rest follow it.
	vals := pg.Values("v", []string{"id", "label"},
		[]render.SQLer{sqlb.Cast[int64](sqlb.Param(int64(1)), "bigint"), sqlb.Cast[string](sqlb.Param("one"), "text")},
		[]render.SQLer{sqlb.Param(int64(2)), sqlb.Param("two")})
	v := sqlb.Ref(vals)
	vID, vLabel := sqlb.Col[int64](v, "id"), sqlb.Col[string](v, "label")
	executes(t, db, "Values source", sel().Exprs(vID, vLabel).From(vals))
	gs, n := pg.GenerateSeries(1, 5, 1, "g")
	executes(t, db, "GenerateSeries", sel().Exprs(n).From(gs))
	un, tag := pg.Unnest(uTags, "t")
	executes(t, db, "Unnest as source", sel().Exprs(tag).From(catUser).Join(sqlb.Join{Type: sqlb.JoinCross, Source: un}))
	executes(t, db, "WithOrdinality", sel().Exprs(uID).From(catUser).Join(sqlb.Join{Type: sqlb.JoinCross, Source: pg.WithOrdinality(un)}))
	lat := sqlb.Derived(sel().Exprs(sqlb.As(sqlb.Sum(oTotal), "spent")).From(catOrder).Where(sqlb.EqExpr(oUser, uID)), "l")
	executes(t, db, "Lateral", sel().Exprs(uName, sqlb.Col[int64](sqlb.Ref(lat), "spent")).From(catUser).
		Join(sqlb.Join{Type: sqlb.JoinLeft, Source: pg.Lateral(lat), On: []sqlb.Predicate{sqlb.True()}}))

	// --- writes ---
	executes(t, db, "OnConflict DoUpdate Excluded",
		sqlb.NewInsert(pgd).Into(catUser).Row(sqlb.Set(uID, int64(1)), sqlb.Set(uName, "ada2"), sqlb.Set(uAge, int32(37))).
			Clause(pg.OnConflict(uID).DoUpdate(sqlb.SetExpr(uName, pg.Excluded(uName))).Clause()))
	executes(t, db, "OnConflict DoNothing",
		sqlb.NewInsert(pgd).Into(catUser).Row(sqlb.Set(uID, int64(1)), sqlb.Set(uName, "x"), sqlb.Set(uAge, int32(1))).
			Clause(pg.OnConflict(uID).DoNothing()))
	executes(t, db, "OnConflict UpdateWhere",
		sqlb.NewInsert(pgd).Into(catUser).Row(sqlb.Set(uID, int64(2)), sqlb.Set(uName, "bob2"), sqlb.Set(uAge, int32(42))).
			Clause(pg.OnConflict(uID).DoUpdate(sqlb.SetExpr(uAge, pg.Excluded(uAge))).UpdateWhere(sqlb.Less(uAge, int32(100))).Clause()))
	executes(t, db, "OverridingSystemValue",
		sqlb.NewInsert(pgd).Into(catUser).Row(sqlb.Set(uID, int64(99)), sqlb.Set(uName, "n"), sqlb.Set(uAge, int32(1))).Clause(pg.OverridingSystemValue()))
	executes(t, db, "DefaultValues via Body", sqlb.NewInsert(pgd).Into(sqlb.Table{Name: "cat_defaults", Alias: "d"}).Body(pg.DefaultValues()))
	incoming := pg.Values("inc", []string{"id", "name"}, []render.SQLer{sqlb.Cast[int64](sqlb.Param(int64(1)), "bigint"), sqlb.Cast[string](sqlb.Param("ada3"), "text")})
	inc := sqlb.Ref(incoming)
	executes(t, db, "UpdateFrom Values",
		sqlb.NewUpdate(pgd).Table(catUser).Set(sqlb.SetExpr(uName, sqlb.Col[string](inc, "name"))).
			Clause(pg.UpdateFrom(incoming)).Where(sqlb.EqExpr(uID, sqlb.Col[int64](inc, "id"))))
	executes(t, db, "DeleteUsing",
		sqlb.NewDelete(pgd).From(catOrder).Clause(pg.DeleteUsing(catUser)).Where(sqlb.EqExpr(oUser, uID), sqlb.Eq(uName, "cyd")))
	var gotID int64
	executes(t, db, "InsertReturning",
		sqlb.InsertReturning(sqlb.NewInsert(pgd).Into(catUser).Row(sqlb.Set(uID, int64(50)), sqlb.Set(uName, "r"), sqlb.Set(uAge, int32(5))), sqlb.Bind(uID, &gotID)))
	executes(t, db, "UpdateReturning",
		sqlb.UpdateReturning(sqlb.NewUpdate(pgd).Table(catUser).Set(sqlb.Set(uAge, int32(1))).Where(sqlb.Eq(uID, int64(1))), sqlb.Bind(uID, &gotID)))
	executes(t, db, "DeleteReturning",
		sqlb.DeleteReturning(sqlb.NewDelete(pgd).From(catOrder).Where(sqlb.Eq(oID, int64(12))), sqlb.Bind(oID, &gotID)))

	// --- arrays ---
	executes(t, db, "ArrayAgg", sel().Exprs(pg.ArrayAgg(uName)).From(catUser))
	executes(t, db, "ArrayAgg distinct", sel().Exprs(pg.ArrayAgg(uAge, sqlb.Distinct())).From(catUser))
	executes(t, db, "InArray", sel().Exprs(uID).From(catUser).Where(pg.InArray(uName, []string{"ada", "bob"})))
	executes(t, db, "Cardinality", sel().Exprs(pg.Cardinality(uTags)).From(catUser))
	executes(t, db, "ArrayLength", sel().Exprs(pg.ArrayLength(uTags, 1)).From(catUser))
	executes(t, db, "ArrayAppend", sel().Exprs(pg.ArrayAppend(uTags, "new")).From(catUser))
	executes(t, db, "ArrayCat", sel().Exprs(pg.ArrayCat(uTags, uTags)).From(catUser))
	executes(t, db, "ArrayPosition", sel().Exprs(pg.ArrayPosition(uTags, "go")).From(catUser))
	executes(t, db, "ArrayDims", sel().Exprs(pg.ArrayDims(uTags)).From(catUser))
	executes(t, db, "Overlaps", sel().Exprs(uID).From(catUser).Where(pg.Overlaps(uTags, []string{"go"})))
	executes(t, db, "UnnestScalar", sel().Exprs(pg.UnnestScalar(uTags)).From(catUser))
	executes(t, db, "ArraySubquery", sel().Exprs(pg.ArraySubquery[string](sel().Exprs(uName).From(catUser))))
	executes(t, db, "AnyOf", sel().Exprs(uID).From(catUser).Where(pg.AnyOf(uID, sqlb.OpEq, sel().Exprs(oUser).From(catOrder))))
	executes(t, db, "AllOf", sel().Exprs(uID).From(catUser).Where(pg.AllOf(uID, sqlb.OpLessEq, sel().Exprs(oUser).From(catOrder))))

	// --- json ---
	executes(t, db, "RowToJSON", sel().Exprs(pg.RowToJSON(catUser)).From(catUser))
	executes(t, db, "HasKey", sel().Exprs(uID).From(catUser).Where(pg.HasKey(uMeta, "plan")))
	executes(t, db, "HasAnyKey", sel().Exprs(uID).From(catUser).Where(pg.HasAnyKey(uMeta, []string{"plan", "n"})))
	executes(t, db, "JSON Contains", sel().Exprs(uID).From(catUser).Where(pg.Contains(uMeta, json.RawMessage(`{"plan":"pro"}`))))
	executes(t, db, "JSONBSet", sel().Exprs(pg.JSONBSet(uMeta, []string{"plan"}, json.RawMessage(`"gold"`), true)).From(catUser))
	executes(t, db, "JSONAgg", sel().Exprs(pg.JSONAgg(uMeta)).From(catUser))
	executes(t, db, "JSONValue", sel().Exprs(pg.JSONValue[int32](uMeta, "$.n")).From(catUser))

	// --- time, text, math ---
	executes(t, db, "Now DateTrunc Extract", sel().Exprs(pg.DateTrunc(pg.FieldMonth, pg.Now()), pg.Extract(pg.FieldYear, uCreated)).From(catUser))
	executes(t, db, "Age IntervalLit", sel().Exprs(pg.Age(pg.Now(), uCreated), pg.IntervalLit("1 day")).From(catUser))
	executes(t, db, "ILike", sel().Exprs(uID).From(catUser).Where(pg.ILike(uName, "A%")))
	executes(t, db, "Upper Left Right", sel().Exprs(pg.Upper(uName), pg.Left(uName, 1), pg.Right(uName, 1)).From(catUser))
	executes(t, db, "StringAgg", sel().Exprs(pg.StringAgg(uName, ",")).From(catUser))
	executes(t, db, "RegexpMatch", sel().Exprs(pg.RegexpMatch(uName, "^a")).From(catUser))
	executes(t, db, "Round", sel().Exprs(pg.Round(uScore, 1)).From(catUser))
	executes(t, db, "Int4Range", sel().Exprs(pg.Int4Range(1, 10, pg.BoundsIncExc)))

	// --- aggregates and grouping ---
	executes(t, db, "Filter", sel().Exprs(sqlb.CountOf(uID, pg.Filter(sqlb.Greater(uAge, int32(30))))).From(catUser))
	executes(t, db, "WithinGroup", sel().Exprs(pg.WithinGroup[float64]("percentile_cont", []render.SQLer{sqlb.Param(0.5)}, sqlb.OrderBy(uAge, sqlb.SortAsc))).From(catUser))
	executes(t, db, "Cube", sel().Exprs(uAge, sqlb.Count()).From(catUser).GroupBy(pg.Cube(uAge)))
	executes(t, db, "Rollup", sel().Exprs(uAge, sqlb.Count()).From(catUser).GroupBy(pg.Rollup(uAge)))
	executes(t, db, "GroupingSets", sel().Exprs(uAge, sqlb.Count()).From(catUser).GroupBy(pg.GroupingSets([]render.SQLer{uAge}, []render.SQLer{})))

	// --- core constructs that are universal but were only ever executed on SQLite ---
	executes(t, db, "RowIn", sel().Exprs(uID).From(catUser).Where(sqlb.RowIn(sqlb.Row(uName, uAge), sqlb.Row(sqlb.Param("ada"), sqlb.Param(int32(36))))))
	executes(t, db, "Contains with ESCAPE", sel().Exprs(uID).From(catUser).Where(sqlb.Contains(uName, "50%")))
	executes(t, db, "WithRecursive", sel().WithRecursive("nums",
		sel().Exprs(sqlb.As(sqlb.Cast[int64](sqlb.Param(int64(1)), "bigint"), "n")),
		sel().Exprs(sqlb.Add(sqlb.Col[int64](sqlb.Table{Name: "nums"}, "n"), sqlb.Param(int64(1)))).From(sqlb.Table{Name: "nums"}).Where(sqlb.Less(sqlb.Col[int64](sqlb.Table{Name: "nums"}, "n"), int64(5)))).
		Exprs(sqlb.Col[int64](sqlb.Table{Name: "nums"}, "n")).From(sqlb.Table{Name: "nums"}))
	executes(t, db, "window count over", sel().Exprs(uName, sqlb.Over(sqlb.Count(), sqlb.Window{})).From(catUser).Limit(2))
	executes(t, db, "keyset RowLess", sel().Exprs(uID).From(catUser).Where(sqlb.RowLess(sqlb.Row(uName, uID), sqlb.Row(sqlb.Param("bob"), sqlb.Param(int64(2))))).Order(sqlb.OrderBy(uName, sqlb.SortAsc), sqlb.OrderBy(uID, sqlb.SortAsc)))

	if strings.TrimSpace(t.Name()) == "" {
		t.Fatal("unreachable")
	}
}
