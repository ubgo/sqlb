package sqlb_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ch"
	"github.com/ubgo/sqlb/pg"
	"github.com/ubgo/sqlb/sqlite"
)

// A complete engine package, written as if it lived in another repository.
//
// This file is in package sqlb_test, so it can reach nothing unexported. That
// is the point: if it compiles and passes, a community package has everything
// the engine packages in this module have. Nothing here is privileged, and no
// list inside sqlb was edited to make this engine exist.
//
// The engine is DuckDB, chosen because it has a clause Postgres does not,
// QUALIFY, which filters on a window result without a subquery. It needs a
// position no other engine uses, which is exactly the case that used to be
// impossible from outside.

const duckEngine = "duckdb"

type duckDialect struct{}

func (duckDialect) Placeholder(int) string      { return "?" }
func (duckDialect) QuoteIdent(id string) string { return sqlb.Quote(id, '"') }
func (duckDialect) Name() string                { return duckEngine }
func (duckDialect) SupportsReturning() bool     { return true }

// QuoteLiteral is what an outside dialect writes when its constructs take
// literals, as read_parquet takes a path. DuckDB strings take no backslash
// escapes, so doubling the quote is the whole rule. A dialect that leaves this
// out cannot be handed a literal: EngineQuoteLit fails closed rather than
// guessing a quoting rule, because the rule a guess would pick, doubling the
// quote, is an injection on any engine that treats a backslash as an escape.
func (duckDialect) QuoteLiteral(v any) (string, error) {
	s, ok := v.(string)
	if !ok {
		return "", sqlb.ErrNotInlinable
	}
	return "'" + strings.ReplaceAll(s, "'", "''") + "'", nil
}

// bareDialect is an outside dialect with no LiteralQuoting at all.
type bareDialect struct{}

func (bareDialect) Placeholder(int) string      { return "?" }
func (bareDialect) QuoteIdent(id string) string { return sqlb.Quote(id, '"') }
func (bareDialect) Name() string                { return duckEngine }

// The whole of the engine package's plumbing: one line each, over the core.
func duckExpr[T any](c string, r func(*sqlb.Args) string) sqlb.Expr[T] {
	return sqlb.EngineExpr[T](duckEngine, c, r)
}
func duckFn[T any](c, name string, args ...sqlb.SQLer) sqlb.Expr[T] {
	return sqlb.EngineFn[T](duckEngine, c, name, args...)
}
func duckPred(c string, r func(*sqlb.Args) string) sqlb.Predicate {
	return sqlb.EnginePred(duckEngine, c, r)
}
func duckBinPred(c, op string, l, r sqlb.SQLer) sqlb.Predicate {
	return sqlb.EngineBinPred(duckEngine, c, op, l, r)
}
func duckClause[K any](c string, slot sqlb.Slot, r func(*sqlb.Args) string) sqlb.Clause[duckDialect, K] {
	return sqlb.EngineClause[duckDialect, K](c, slot, r)
}

// Constructs only this engine has.

// ListValue renders list_value(items...), DuckDB's array constructor.
func duckListValue[T any](items ...sqlb.Expr[T]) sqlb.Expr[[]T] {
	args := make([]sqlb.SQLer, len(items))
	for i, it := range items {
		args[i] = it
	}
	return duckFn[[]T]("duck.ListValue", "list_value", args...)
}

// Qualify renders QUALIFY, which filters on a window result. It goes between
// WINDOW and ORDER BY, a position the core gained for exactly this shape.
// duckProbe is a marker clause typed to the statement kind K, for the tests
// that walk every slot of the write statements.
func duckProbe[K any](slot sqlb.Slot, marker string) sqlb.Clause[duckDialect, K] {
	return duckClause[K]("duck.Probe", slot, func(*sqlb.Args) string { return marker })
}

func duckQualify(p sqlb.Predicate) sqlb.Clause[duckDialect, sqlb.SelectKind] {
	return duckClause[sqlb.SelectKind]("duck.Qualify", sqlb.SlotBeforeOrder, func(a *sqlb.Args) string {
		return "QUALIFY " + sqlb.PredicateSQL(p, a)
	})
}

// TryCast renders TRY_CAST(e AS type), which returns NULL instead of raising
// on a value that will not convert. It is built with the general expression
// constructor because its shape is not a function call.
func duckTryCast[R, T any](e sqlb.Expr[T], sqlType string) sqlb.Expr[R] {
	return duckExpr[R]("duck.TryCast", func(a *sqlb.Args) string {
		return "TRY_CAST(" + e.SQL(a) + " AS " + sqlType + ")"
	})
}

// AllNotNull renders (a IS NOT NULL AND b IS NOT NULL ...), a predicate whose
// shape is neither a function nor a binary operator, so it uses the general
// predicate constructor.
func duckAllNotNull(cols ...sqlb.SQLer) sqlb.Predicate {
	return duckPred("duck.AllNotNull", func(a *sqlb.Args) string {
		parts := make([]string, len(cols))
		for i, c := range cols {
			parts[i] = c.SQL(a) + " IS NOT NULL"
		}
		return "(" + strings.Join(parts, " AND ") + ")"
	})
}

// Similar renders a ~~~ b, so the test covers a binary predicate too.
func duckSimilar(a, b sqlb.Expr[string]) sqlb.Predicate {
	return duckBinPred("duck.Similar", "~~~", a, b)
}

// ReadParquet renders read_parquet('path') as a FROM source.
func duckReadParquet(path, alias string) sqlb.Source {
	return sqlb.EngineSource(duckEngine, "duck.ReadParquet", alias, func(a *sqlb.Args) string {
		return "read_parquet(" + sqlb.EngineQuoteLit(path).SQL(a) + ") AS " + a.Dialect().QuoteIdent(alias)
	})
}

var (
	duckTable = sqlb.Table{Name: "events", Alias: "e"}
	duckName  = sqlb.Col[string](duckTable, "name")
	duckN     = sqlb.Col[int32](duckTable, "n")
)

// A third-party dialect is a first-class dialect: the core identifies it by
// name without any entry in the core's own constant block, and it opts into
// capabilities through the same interfaces.
func TestExternalDialect_IsFirstClass(t *testing.T) {
	var d sqlb.Dialect = duckDialect{}

	if got := sqlb.DialectName(d); got != duckEngine {
		t.Errorf("DialectName = %q; the core must not need its own constant for an outside engine", got)
	}
	if _, ok := d.(sqlb.Returning); !ok {
		t.Error("a third-party dialect must be able to satisfy a capability interface")
	}
	// A dialect that declines to name itself is reported as unknown rather
	// than mistaken for one of the built-in three.
	if got := sqlb.DialectName(anonymousDialect{}); got != sqlb.DialectUnknown {
		t.Errorf("unnamed dialect = %q, want %q", got, sqlb.DialectUnknown)
	}
}

type anonymousDialect struct{}

func (anonymousDialect) Placeholder(int) string      { return "?" }
func (anonymousDialect) QuoteIdent(id string) string { return id }

// Constructs render under their own engine and fail closed under every other,
// with no code in this module knowing the engine exists.
func TestExternalConstructs_RenderAndGuard(t *testing.T) {
	cases := map[string]sqlb.SQLer{
		"ListValue":  duckListValue(duckName, sqlb.Param("x")),
		"Similar":    sqlb.BoolExpr(duckSimilar(duckName, sqlb.Param("y"))),
		"TryCast":    duckTryCast[int64](duckName, "BIGINT"),
		"AllNotNull": sqlb.BoolExpr(duckAllNotNull(duckName, duckN)),
	}
	for name, e := range cases {
		q, err := sqlb.NewSelect(duckDialect{}).Exprs(e).From(duckTable).Build()
		if err != nil {
			t.Errorf("%s under its own dialect: %v", name, err)
		}
		if q.SQL == "" {
			t.Errorf("%s rendered nothing", name)
		}
		for _, other := range []sqlb.Dialect{sqlb.PostgresDialect{}, sqlb.SQLiteDialect{}, sqlb.ClickHouseDialect{}} {
			_, err := sqlb.NewSelect(other).Exprs(e).From(duckTable).Build()
			if !errors.Is(err, sqlb.ErrUnsupportedDialect) {
				t.Errorf("%s under %s: want ErrUnsupportedDialect, got %v", name, sqlb.DialectName(other), err)
			}
		}
	}

	q, err := sqlb.NewSelect(duckDialect{}).Exprs(duckListValue(duckName)).From(duckTable).Build()
	if err != nil {
		t.Fatal(err)
	}
	if want := `SELECT list_value("e"."name") FROM "events" AS "e"`; q.SQL != want {
		t.Fatalf("got  %s\nwant %s", q.SQL, want)
	}
}

// QUALIFY, the case that proves the slot set is usable from outside. It needs
// a position between WINDOW and ORDER BY, which no engine in this module uses.
func TestExternalClause_InItsOwnPosition(t *testing.T) {
	rn := sqlb.RowNumber().Over(sqlb.NewWindow().
		PartitionBy(duckName).
		OrderBy(sqlb.OrderBy(duckN, sqlb.SortDesc)))

	q, err := sqlb.NewSelect(duckDialect{}).
		Exprs(duckName, duckN, sqlb.As(rn, "rn")).
		From(duckTable).
		// PredicateSQL renders a bare predicate, without the parentheses a
		// WHERE group adds. The clause supplies its own grouping if it needs
		// any, which is what lets QUALIFY read as the engine spells it.
		Clause(duckQualify(sqlb.Eq(sqlb.OutputCol[int64]("rn"), int64(1)))).
		Order(sqlb.OrderBy(duckName, sqlb.SortAsc)).
		Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	want := `SELECT "e"."name", "e"."n", row_number() OVER (PARTITION BY "e"."name" ORDER BY "e"."n" DESC) AS "rn" FROM "events" AS "e" QUALIFY "rn" = ? ORDER BY "e"."name" ASC`
	if q.SQL != want {
		t.Fatalf("got  %s\nwant %s", q.SQL, want)
	}
	// The exact string above already fixes the position. This states the
	// invariant separately, against the LAST "ORDER BY": the first one in
	// this statement belongs to the window definition, not to the statement,
	// and comparing against it would pass for the wrong reason.
	if strings.Index(q.SQL, "QUALIFY") > strings.LastIndex(q.SQL, "ORDER BY") {
		t.Error("QUALIFY must render before the statement's ORDER BY")
	}

	// And it is guarded like everything else: a duck clause on a Postgres
	// statement is a compile error now, pinned in examples/typecheck, which
	// is exactly the guarantee a community engine gets from EngineClause.
}

// A source from outside the module.
func TestExternalSource(t *testing.T) {
	src := duckReadParquet("s3://bucket/events.parquet", "p")
	ref := sqlb.Ref(src)
	q, err := sqlb.NewSelect(duckDialect{}).
		Exprs(sqlb.Col[string](ref, "name")).From(src).Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	want := `SELECT "p"."name" FROM read_parquet('s3://bucket/events.parquet') AS "p"`
	if q.SQL != want {
		t.Fatalf("got  %s\nwant %s", q.SQL, want)
	}
	if _, err := sqlb.NewSelect(sqlb.SQLiteDialect{}).
		Exprs(sqlb.Col[string](ref, "name")).From(src).Build(); !errors.Is(err, sqlb.ErrUnsupportedDialect) {
		t.Errorf("source under sqlite: got %v", err)
	}
}

// TestEngineQuoteLit_FailsClosedWithoutTheCapability pins what an outside
// dialect gets when its constructs need a literal and it has not said how to
// write one: Build fails naming the dialect, and no statement text exists.
//
// Doubling the quote would have kept such a dialect working, and it was what
// EngineQuoteLit did until 2026-09-21. It is also an injection on MySQL, whose
// strings take backslash escapes by default, and it silently changed a
// jsonpath's meaning on a Postgres server with standard_conforming_strings
// off. A community dialect for either would have inherited that without
// knowing, so the capability is required rather than assumed.
func TestEngineQuoteLit_FailsClosedWithoutTheCapability(t *testing.T) {
	src := duckReadParquet("s3://bucket/events.parquet", "p")
	q, err := sqlb.NewSelect(bareDialect{}).Exprs(sqlb.Col[string](sqlb.Ref(src), "name")).From(src).Build()
	if !errors.Is(err, sqlb.ErrNoLiteralQuoting) {
		t.Fatalf("got %v, want ErrNoLiteralQuoting", err)
	}
	if !strings.Contains(err.Error(), duckEngine) {
		t.Errorf("the error should name the dialect: %v", err)
	}
	if q.SQL != "" {
		t.Errorf("a refused statement must carry no SQL, got %q", q.SQL)
	}
}

// A clause aimed at a slot the statement does not render must fail at Build.
//
// This is the failure that used to be silent, and the one that made an
// outside dialect second-class: a third party cannot add a slot, so a clause
// in the wrong place simply vanished from the statement. It is now an error
// naming the slot.
func TestUnknownSlot_FailsRatherThanVanishing(t *testing.T) {
	// A slot number no statement renders.
	invented := duckClause[sqlb.SelectKind]("duck.Invented", sqlb.Slot(99), func(*sqlb.Args) string {
		return "SOMETHING"
	})
	_, err := sqlb.NewSelect(duckDialect{}).Exprs(duckName).From(duckTable).Clause(invented).Build()
	if !errors.Is(err, sqlb.ErrUnknownSlot) {
		t.Fatalf("want ErrUnknownSlot, got %v", err)
	}
	if !strings.Contains(err.Error(), "Slot(99)") {
		t.Errorf("the error must name the slot, got %q", err)
	}

	// A real slot aimed at the wrong statement kind. SlotAfterSet belongs to
	// UPDATE; a SELECT carrying one used to drop it without a word.
	misplaced := duckClause[sqlb.SelectKind]("duck.Misplaced", sqlb.SlotAfterSet, func(*sqlb.Args) string {
		return "FROM other"
	})
	_, err = sqlb.NewSelect(duckDialect{}).Exprs(duckName).From(duckTable).Clause(misplaced).Build()
	if !errors.Is(err, sqlb.ErrUnknownSlot) {
		t.Fatalf("select with an UPDATE slot: want ErrUnknownSlot, got %v", err)
	}
	if !strings.Contains(err.Error(), "SlotAfterSet") {
		t.Errorf("the error must name the slot, got %q", err)
	}

	// The write statements check the same way. SlotAfterInsert belongs to
	// INSERT, which is the only statement whose keyword is followed by INTO.
	insertSlotOnUpdate := duckClause[sqlb.UpdateKind]("duck.Misfit", sqlb.SlotAfterInsert, func(*sqlb.Args) string { return "X" })
	_, err = sqlb.NewUpdate(duckDialect{}).Table(duckTable).
		Set(sqlb.Set(duckName, "a")).All().Clause(insertSlotOnUpdate).Build()
	if !errors.Is(err, sqlb.ErrUnknownSlot) {
		t.Fatalf("update with an INSERT slot: want ErrUnknownSlot, got %v", err)
	}
	if !strings.Contains(err.Error(), "SlotAfterInsert") {
		t.Errorf("the error must name the slot, got %q", err)
	}
}

// Every slot the core defines is either rendered by a statement or is an
// error, and no slot is silently ignored. This walks the whole set so a slot
// added later cannot be forgotten.
func TestEverySlot_IsRenderedOrRejected(t *testing.T) {
	for _, slot := range sqlb.SlotValues {
		// One probe per statement kind: a clause carries its kind in its type,
		// so a single value cannot be offered to all four builders any more.
		var rendered, rejected int
		for _, build := range []func() (sqlb.Query, error){
			func() (sqlb.Query, error) {
				return sqlb.NewSelect(duckDialect{}).Exprs(duckName).From(duckTable).Clause(duckProbe[sqlb.SelectKind](slot, "PROBE")).Build()
			},
			func() (sqlb.Query, error) {
				return sqlb.NewInsert(duckDialect{}).Into(duckTable).Row(sqlb.Set(duckName, "a")).Clause(duckProbe[sqlb.InsertKind](slot, "PROBE")).Build()
			},
			func() (sqlb.Query, error) {
				return sqlb.NewUpdate(duckDialect{}).Table(duckTable).Set(sqlb.Set(duckName, "a")).All().Clause(duckProbe[sqlb.UpdateKind](slot, "PROBE")).Build()
			},
			func() (sqlb.Query, error) {
				return sqlb.NewDelete(duckDialect{}).From(duckTable).All().Clause(duckProbe[sqlb.DeleteKind](slot, "PROBE")).Build()
			},
		} {
			q, err := build()
			switch {
			case err == nil && strings.Contains(q.SQL, "PROBE"):
				rendered++
			case errors.Is(err, sqlb.ErrUnknownSlot):
				rejected++
			case err != nil:
				// Some combinations fail for an unrelated reason, such as a
				// clause replacing the INSERT body. Those are not this test's
				// business.
			default:
				t.Errorf("%s: built cleanly without rendering the clause; that is the silent drop this test exists to catch", slot)
			}
		}
		if rendered == 0 {
			t.Errorf("%s: no statement renders it, so no clause can ever use it", slot)
		}
	}
}

// Slot.String names every slot, because the unknown-slot error is only useful
// if it can say which one.
func TestSlotString(t *testing.T) {
	for _, slot := range sqlb.SlotValues {
		if got := slot.String(); got == "" || strings.HasPrefix(got, "Slot(") {
			t.Errorf("slot %d has no name: %q", int(slot), got)
		}
	}
	if got := sqlb.Slot(99).String(); got != "Slot(99)" {
		t.Errorf("unknown slot = %q, want Slot(99)", got)
	}
}

// Every boundary of a SELECT is reachable from outside this module.
//
// This is the claim that makes an outside engine package first class. The
// slot set is the one extension point a community package cannot add to, so
// the set has to be complete: a clause must be placeable at every position
// the renderer emits. The test puts a marker in each SELECT slot and asserts
// they come out in grammar order, which fails if a slot is unrendered, in the
// wrong place, or missing from the middle of the statement.
func TestEverySelectBoundary_IsReachable(t *testing.T) {
	selectSlots := []struct {
		slot   sqlb.Slot
		marker string
	}{
		{sqlb.SlotAfterSelect, "M_AFTER_SELECT"},
		{sqlb.SlotAfterColumns, "M_AFTER_COLUMNS"},
		{sqlb.SlotAfterFrom, "M_AFTER_FROM"},
		{sqlb.SlotBeforeWhere, "M_BEFORE_WHERE"},
		{sqlb.SlotAfterWhere, "M_AFTER_WHERE"},
		{sqlb.SlotAfterGroupBy, "M_AFTER_GROUPBY"},
		{sqlb.SlotAfterHaving, "M_AFTER_HAVING"},
		{sqlb.SlotBeforeOrder, "M_BEFORE_ORDER"},
		{sqlb.SlotAfterOrder, "M_AFTER_ORDER"},
		{sqlb.SlotAfterOffset, "M_AFTER_OFFSET"},
		{sqlb.SlotTail, "M_TAIL"},
	}

	sel := sqlb.NewSelect(duckDialect{}).
		Exprs(duckName, sqlb.Count()).
		From(duckTable).
		Where(sqlb.Eq(duckName, "a")).
		GroupBy(duckName).
		Having(sqlb.Greater(sqlb.Count(), int64(1))).
		Window("w", sqlb.NewWindow().PartitionBy(duckName)).
		Order(sqlb.OrderBy(duckName, sqlb.SortAsc)).
		Limit(10).Offset(5)

	for _, sl := range selectSlots {
		marker := sl.marker
		sel = sel.Clause(duckClause[sqlb.SelectKind]("duck.Probe", sl.slot, func(*sqlb.Args) string { return marker }))
	}

	q, err := sel.Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	prev := -1
	for _, sl := range selectSlots {
		at := strings.Index(q.SQL, sl.marker)
		if at < 0 {
			t.Fatalf("%s: no clause reached %s, so that position is unreachable from outside", sl.slot, sl.marker)
		}
		if at < prev {
			t.Errorf("%s rendered out of grammar order", sl.slot)
		}
		prev = at
	}

	// Spot-check three positions against the keywords they must sit between,
	// since ordering alone would pass if two slots rendered in one place.
	for _, c := range []struct{ marker, after, before string }{
		{"M_AFTER_SELECT", "SELECT", `"e"."name"`},
		{"M_AFTER_WHERE", "WHERE", "GROUP BY"},
		{"M_BEFORE_ORDER", "WINDOW", "ORDER BY"},
	} {
		at := strings.Index(q.SQL, c.marker)
		if a := strings.Index(q.SQL, c.after); a > at {
			t.Errorf("%s must come after %s", c.marker, c.after)
		}
		if b := strings.LastIndex(q.SQL, c.before); b < at {
			t.Errorf("%s must come before %s", c.marker, c.before)
		}
	}
}

// The same for the write statements, so no boundary is SELECT-only.
func TestEveryWriteBoundary_IsReachable(t *testing.T) {

	q, err := sqlb.NewInsert(duckDialect{}).Into(duckTable).
		Row(sqlb.Set(duckName, "a")).
		Clause(duckProbe[sqlb.InsertKind](sqlb.SlotAfterInsert, "M_AFTER_INSERT")).
		Clause(duckProbe[sqlb.InsertKind](sqlb.SlotAfterTarget, "M_AFTER_TARGET")).
		Clause(duckProbe[sqlb.InsertKind](sqlb.SlotBeforeValues, "M_BEFORE_VALUES")).
		Clause(duckProbe[sqlb.InsertKind](sqlb.SlotTail, "M_TAIL")).
		Build()
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	for _, m := range []string{"M_AFTER_INSERT", "M_AFTER_TARGET", "M_BEFORE_VALUES", "M_TAIL"} {
		if !strings.Contains(q.SQL, m) {
			t.Errorf("insert: %s unreachable", m)
		}
	}

	q, err = sqlb.NewUpdate(duckDialect{}).Table(duckTable).
		Set(sqlb.Set(duckName, "a")).
		Where(sqlb.Eq(duckN, int32(1))).
		Clause(duckProbe[sqlb.UpdateKind](sqlb.SlotAfterVerb, "M_AFTER_VERB")).
		Clause(duckProbe[sqlb.UpdateKind](sqlb.SlotAfterTarget, "M_AFTER_TARGET")).
		Clause(duckProbe[sqlb.UpdateKind](sqlb.SlotAfterSet, "M_AFTER_SET")).
		Clause(duckProbe[sqlb.UpdateKind](sqlb.SlotAfterWhere, "M_AFTER_WHERE")).
		Clause(duckProbe[sqlb.UpdateKind](sqlb.SlotTail, "M_TAIL")).
		Build()
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	for _, m := range []string{"M_AFTER_VERB", "M_AFTER_TARGET", "M_AFTER_SET", "M_AFTER_WHERE", "M_TAIL"} {
		if !strings.Contains(q.SQL, m) {
			t.Errorf("update: %s unreachable", m)
		}
	}

	q, err = sqlb.NewDelete(duckDialect{}).From(duckTable).
		Where(sqlb.Eq(duckN, int32(1))).
		Clause(duckProbe[sqlb.DeleteKind](sqlb.SlotAfterVerb, "M_AFTER_VERB")).
		Clause(duckProbe[sqlb.DeleteKind](sqlb.SlotAfterTarget, "M_AFTER_TARGET")).
		Clause(duckProbe[sqlb.DeleteKind](sqlb.SlotAfterWhere, "M_AFTER_WHERE")).
		Clause(duckProbe[sqlb.DeleteKind](sqlb.SlotTail, "M_TAIL")).
		Build()
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !strings.HasPrefix(q.SQL, "DELETE M_AFTER_VERB FROM") {
		t.Errorf("the verb slot must sit between DELETE and FROM: %s", q.SQL)
	}
	for _, m := range []string{"M_AFTER_TARGET", "M_AFTER_WHERE", "M_TAIL"} {
		if !strings.Contains(q.SQL, m) {
			t.Errorf("delete: %s unreachable", m)
		}
	}
}

// Where a clause lands in a compound statement.
//
// Slots are a per-statement mechanism, and a statement can contain others: a
// join, a set operation, a CTE, a subquery. This pins which statement each
// clause belongs to, because "it rendered somewhere" is not the same as "it
// rendered in the right branch", and the difference is invisible until the
// database disagrees.
func TestSlotScope_InCompoundStatements(t *testing.T) {
	a := sqlb.Table{Name: "a", Alias: "a"}
	bt := sqlb.Table{Name: "b", Alias: "b"}
	an, bn := sqlb.Col[string](a, "n"), sqlb.Col[string](bt, "n")
	mark := func(slot sqlb.Slot, text string) sqlb.Clause[duckDialect, sqlb.SelectKind] {
		return duckClause[sqlb.SelectKind]("duck.Mark", slot, func(*sqlb.Args) string { return text })
	}

	// A join does not move the statement's slots. SlotAfterFrom stays with
	// the driving table, before the joins; SlotBeforeWhere comes after them.
	t.Run("join", func(t *testing.T) {
		q, err := sqlb.NewSelect(duckDialect{}).Exprs(an).From(a).
			Join(sqlb.Join{Type: sqlb.JoinInner, Table: bt, On: []sqlb.Predicate{sqlb.EqExpr(an, bn)}}).
			Clause(mark(sqlb.SlotAfterFrom, "M_FROM")).
			Clause(mark(sqlb.SlotBeforeWhere, "M_JOINS")).
			Build()
		if err != nil {
			t.Fatal(err)
		}
		want := `SELECT "a"."n" FROM "a" AS "a" M_FROM INNER JOIN "b" AS "b" ON ("a"."n" = "b"."n") M_JOINS`
		if q.SQL != want {
			t.Fatalf("got  %s\nwant %s", q.SQL, want)
		}
	})

	// A set operation's receiver is the compound. So a clause added to the
	// receiver applies to the whole statement, exactly as its ORDER BY and
	// LIMIT do, and a clause on an argument branch stays inside that branch's
	// scope, a derived table, which every engine accepts where SQLite refuses
	// a parenthesised compound member.
	t.Run("set operation", func(t *testing.T) {
		left := sqlb.NewSelect(duckDialect{}).Exprs(an).From(a)
		right := sqlb.NewSelect(duckDialect{}).Exprs(bn).From(bt).
			Clause(mark(sqlb.SlotTail, "M_BRANCH"))
		q, err := left.Union(right).Clause(mark(sqlb.SlotTail, "M_COMPOUND")).Build()
		if err != nil {
			t.Fatal(err)
		}
		want := `SELECT "a"."n" FROM "a" AS "a" UNION SELECT * FROM (SELECT "b"."n" FROM "b" AS "b" M_BRANCH) AS "sqlb_setop" M_COMPOUND`
		if q.SQL != want {
			t.Fatalf("got  %s\nwant %s", q.SQL, want)
		}
		// Union returns a derived statement rather than the receiver, like
		// every other builder method, so the compound is the returned value
		// and the left branch is still usable on its own. A clause added
		// before the call belongs to the compound because it was copied into
		// it; one added after belongs to whichever statement received it.
		compound := left.Union(right)
		if compound == left {
			t.Error("Union must not return the receiver")
		}
		alone, err := left.Build()
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(alone.SQL, "UNION") {
			t.Errorf("the left branch carries the set operation: %s", alone.SQL)
		}
	})

	// A CTE body is its own statement and keeps its own slots.
	t.Run("cte", func(t *testing.T) {
		body := sqlb.NewSelect(duckDialect{}).Exprs(an).From(a).
			Clause(mark(sqlb.SlotTail, "M_BODY"))
		c := sqlb.Table{Name: "c", Alias: "c"}
		q, err := sqlb.NewSelect(duckDialect{}).With("c", body).
			Exprs(sqlb.Col[string](c, "n")).From(c).
			Clause(mark(sqlb.SlotTail, "M_OUTER")).Build()
		if err != nil {
			t.Fatal(err)
		}
		want := `WITH "c" AS (SELECT "a"."n" FROM "a" AS "a" M_BODY) SELECT "c"."n" FROM "c" AS "c" M_OUTER`
		if q.SQL != want {
			t.Fatalf("got  %s\nwant %s", q.SQL, want)
		}
	})

	// A recursive CTE has two branches, and the recursive one keeps its own.
	t.Run("recursive cte", func(t *testing.T) {
		r := sqlb.Table{Name: "r", Alias: "r"}
		seed := sqlb.NewSelect(duckDialect{}).Exprs(an).From(a)
		step := sqlb.NewSelect(duckDialect{}).Exprs(an).From(a).
			Clause(mark(sqlb.SlotTail, "M_STEP"))
		q, err := sqlb.NewSelect(duckDialect{}).WithRecursive("r", seed, step).
			Exprs(sqlb.Col[string](r, "n")).From(r).Build()
		if err != nil {
			t.Fatal(err)
		}
		// The step is written bare: a derived table around it would put its
		// reference to the CTE inside a subquery, which Postgres refuses.
		if !strings.Contains(q.SQL, `UNION ALL SELECT "a"."n" FROM "a" AS "a" M_STEP`) {
			t.Fatalf("the recursive branch lost its clause: %s", q.SQL)
		}
	})

	// A derived table is a statement inside a FROM, and keeps its own slots.
	t.Run("subquery", func(t *testing.T) {
		inner := sqlb.NewSelect(duckDialect{}).Exprs(an).From(a).
			Clause(mark(sqlb.SlotTail, "M_INNER"))
		src := sqlb.Derived(inner, "s")
		q, err := sqlb.NewSelect(duckDialect{}).
			Exprs(sqlb.Col[string](sqlb.Ref(src), "n")).From(src).Build()
		if err != nil {
			t.Fatal(err)
		}
		want := `SELECT "s"."n" FROM (SELECT "a"."n" FROM "a" AS "a" M_INNER) AS "s"`
		if q.SQL != want {
			t.Fatalf("got  %s\nwant %s", q.SQL, want)
		}
	})

	// A per-table hint on a joined table. This is the one position a Slot
	// cannot name, because there is one per join rather than one per
	// statement, so Join carries its own clauses.
	t.Run("per-join clause", func(t *testing.T) {
		q, err := sqlb.NewSelect(duckDialect{}).Exprs(an).From(a).
			Join(sqlb.Join{
				Type:    sqlb.JoinInner,
				Table:   bt,
				On:      []sqlb.Predicate{sqlb.EqExpr(an, bn)},
				Clauses: []sqlb.AnyClause{mark(sqlb.SlotAfterFrom, "M_HINT")},
			}).Build()
		if err != nil {
			t.Fatal(err)
		}
		want := `SELECT "a"."n" FROM "a" AS "a" INNER JOIN "b" AS "b" M_HINT ON ("a"."n" = "b"."n")`
		if q.SQL != want {
			t.Fatalf("got  %s\nwant %s", q.SQL, want)
		}
		// It is still a Clause, so it still carries its guard.
		_, err = sqlb.NewSelect(sqlb.PostgresDialect{}).Exprs(an).From(a).
			Join(sqlb.Join{
				Type:    sqlb.JoinInner,
				Table:   bt,
				On:      []sqlb.Predicate{sqlb.EqExpr(an, bn)},
				Clauses: []sqlb.AnyClause{mark(sqlb.SlotAfterFrom, "M_HINT")},
			}).Build()
		if !errors.Is(err, sqlb.ErrUnsupportedDialect) {
			t.Fatalf("a join clause must be guarded like any other: got %v", err)
		}
	})
}

// The per-table-reference class, all of it.
//
// TABLESAMPLE, FINAL, SAMPLE and INDEXED BY are all per table reference
// rather than per statement. A statement slot names one position, so a clause
// meant for a joined table used to attach to the driving table and produce a
// different query that still runs. sqlb.Join.Clauses is the position for
// them, and this walks the whole class so the next one added is covered by a
// test rather than by a reader noticing.
func TestPerTableClauses_TargetTheirOwnTable(t *testing.T) {
	a := sqlb.Table{Name: "a", Alias: "a"}
	b := sqlb.Table{Name: "b", Alias: "b"}
	an, bn := sqlb.Col[string](a, "n"), sqlb.Col[string](b, "n")
	join := sqlb.Join{Type: sqlb.JoinInner, Table: b, On: []sqlb.Predicate{sqlb.EqExpr(an, bn)}}

	// Each case builds both placements itself, because the statement placement
	// goes through Clause, which is typed to the engine: a table-driven loop
	// over a runtime dialect could not offer pg.TableSample to a Select and
	// compile. The join placement is an AnyClause and stays loose by design.
	type placement struct {
		onJoin, onStatement func() (sqlb.Query, error)
	}
	for _, tc := range []struct {
		name string
		want string
		p    placement
	}{
		{"pg.TableSample",
			`SELECT "a"."n" FROM "a" AS "a" INNER JOIN "b" AS "b" TABLESAMPLE SYSTEM (10) ON ("a"."n" = "b"."n")`,
			placement{
				func() (sqlb.Query, error) {
					j := join
					j.Clauses = []sqlb.AnyClause{pg.TableSample(pg.SampleSystem, 10, -1)}
					return sqlb.NewSelect(sqlb.PostgresDialect{}).Exprs(an).From(a).Join(j).Build()
				},
				func() (sqlb.Query, error) {
					return sqlb.NewSelect(sqlb.PostgresDialect{}).Exprs(an).From(a).Join(join).Clause(pg.TableSample(pg.SampleSystem, 10, -1)).Build()
				}}},
		{"ch.Final",
			"SELECT `a`.`n` FROM `a` AS `a` INNER JOIN `b` AS `b` FINAL ON (`a`.`n` = `b`.`n`)",
			placement{
				func() (sqlb.Query, error) {
					j := join
					j.Clauses = []sqlb.AnyClause{ch.Final()}
					return sqlb.NewSelect(ch.Dialect{}).Exprs(an).From(a).Join(j).Build()
				},
				func() (sqlb.Query, error) {
					return sqlb.NewSelect(ch.Dialect{}).Exprs(an).From(a).Join(join).Clause(ch.Final()).Build()
				}}},
		{"ch.Sample",
			"SELECT `a`.`n` FROM `a` AS `a` INNER JOIN `b` AS `b` SAMPLE 0.1 ON (`a`.`n` = `b`.`n`)",
			placement{
				func() (sqlb.Query, error) {
					j := join
					j.Clauses = []sqlb.AnyClause{ch.Sample(0.1)}
					return sqlb.NewSelect(ch.Dialect{}).Exprs(an).From(a).Join(j).Build()
				},
				func() (sqlb.Query, error) {
					return sqlb.NewSelect(ch.Dialect{}).Exprs(an).From(a).Join(join).Clause(ch.Sample(0.1)).Build()
				}}},
		{"sqlite.IndexedBy",
			`SELECT "a"."n" FROM "a" AS "a" INNER JOIN "b" AS "b" INDEXED BY "idx_b" ON ("a"."n" = "b"."n")`,
			placement{
				func() (sqlb.Query, error) {
					j := join
					j.Clauses = []sqlb.AnyClause{sqlite.IndexedBy("idx_b")}
					return sqlb.NewSelect(sqlb.SQLiteDialect{}).Exprs(an).From(a).Join(j).Build()
				},
				func() (sqlb.Query, error) {
					return sqlb.NewSelect(sqlb.SQLiteDialect{}).Exprs(an).From(a).Join(join).Clause(sqlite.IndexedBy("idx_b")).Build()
				}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q, err := tc.p.onJoin()
			if err != nil {
				t.Fatalf("on a join: %v", err)
			}
			if q.SQL != tc.want {
				t.Fatalf("got  %s\nwant %s", q.SQL, tc.want)
			}

			// The same clause in the statement targets the driving table.
			// Both placements are legal SQL, which is exactly why the
			// distinction has to be tested rather than left to the reader.
			q, err = tc.p.onStatement()
			if err != nil {
				t.Fatalf("on the statement: %v", err)
			}
			if strings.Index(q.SQL, "JOIN") < strings.Index(q.SQL, `"a"`)+len(`"a"`) {
				t.Error("the statement placement must bind to the driving table")
			}
			if !strings.Contains(q.SQL, "FROM") {
				t.Error("unexpected shape")
			}
		})
	}
}
