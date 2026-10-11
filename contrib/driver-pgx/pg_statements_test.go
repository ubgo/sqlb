package driverpgx_test

import (
	"testing"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/pg"
)

// TestPGConstructs_Aggregates executes every aggregate and window construct.
func TestPGConstructs_Aggregates(t *testing.T) {
	db := catalogue(t)
	agg := func(name string, e sqlb.SQLer) {
		executes(t, db, name, sel().Exprs(e).From(catUser))
	}
	agg("Filter", pg.ArrayAgg(uName, pg.Filter(sqlb.Greater(uAge, int32(30)))))
	agg("OrderWithin", pg.StringAgg(uName, ",", pg.OrderWithin(sqlb.OrderBy(uName, sqlb.SortDesc))))
	agg("BoolAnd", pg.BoolAnd(sqlb.BoolExpr(sqlb.Greater(uAge, int32(0)))))
	agg("BoolOr", pg.BoolOr(sqlb.BoolExpr(sqlb.Greater(uAge, int32(40)))))
	agg("Stddev", pg.Stddev(uScore))
	agg("Variance", pg.Variance(uScore))
	agg("PercentileCont", pg.PercentileCont(0.5, sqlb.OrderBy(uScore, sqlb.SortAsc)))
	agg("PercentileDisc", pg.PercentileDisc(0.5, uAge, sqlb.SortAsc))
	agg("Mode", pg.Mode(uAge))
	executes(t, db, "Grouping", sel().Exprs(uAge, pg.Grouping(uAge)).From(catUser).GroupBy(pg.Rollup(uAge)))
	win := sqlb.NewWindow().OrderBy(sqlb.OrderBy(uAge, sqlb.SortAsc))
	agg("PercentRank", pg.PercentRank().Over(win))
	agg("CumeDist", pg.CumeDist().Over(win))
	agg("Ntile", pg.Ntile(2).Over(win))
	agg("Lag", pg.Lag(uName, 1).Over(win))
	agg("Lead", pg.Lead(uName, 1).Over(win))
	agg("LagDefault", pg.LagDefault(uName, 1, sqlb.Param("none")).Over(win))
	agg("LeadDefault", pg.LeadDefault(uName, 1, sqlb.Param("none")).Over(win))
}

// TestPGConstructs_Statements executes the select-shape, write, lock,
// merge, truncate and recursive constructs that are not scalar expressions.
func TestPGConstructs_Statements(t *testing.T) {
	db := catalogue(t)
	executes(t, db, "OrderByNulls", sel().Exprs(uName).From(catUser).Order(pg.OrderByNulls(uName, sqlb.SortDesc, true)))
	executes(t, db, "OrderUsing", sel().Exprs(uName).From(catUser).Order(pg.OrderUsing(uAge, "<")))
	body := sel().Exprs(uID).From(catUser)
	cte := sqlb.Table{Name: "t", Alias: "t"}
	executes(t, db, "Materialized", sel().WithCTE(sqlb.CTE{Name: "t", Body: body, Prefix: pg.Materialized(true)}).Exprs(sqlb.Col[int64](cte, "id")).From(cte))
	executes(t, db, "Not materialized", sel().WithCTE(sqlb.CTE{Name: "t", Body: body, Prefix: pg.Materialized(false)}).Exprs(sqlb.Col[int64](cte, "id")).From(cte))
	fs := pg.FuncSource(pg.GenerateSeriesScalar(sqlb.Param[int64](1), sqlb.Param[int64](3)), "g", "n")
	executes(t, db, "FuncSource", sel().Exprs(sqlb.Col[int64](sqlb.Ref(fs), "n")).From(fs))
	executes(t, db, "ForShare Of SkipLocked", sel().Exprs(uID).From(catUser).Clause(pg.ForShare(pg.Of(catUser), pg.SkipLocked())))
	executes(t, db, "ForKeyShare NoWait", sel().Exprs(uID).From(catUser).Clause(pg.ForKeyShare(pg.NoWait())))
	executes(t, db, "ForNoKeyUpdate", sel().Exprs(uID).From(catUser).Clause(pg.ForNoKeyUpdate()))
	// writes
	ins := func() *sqlb.Insert[sqlb.PostgresDialect] { return sqlb.NewInsert(pgd) }
	executes(t, db, "OverridingUserValue", ins().Into(catUser).Row(sqlb.Set(uID, int64(90)), sqlb.Set(uName, "z"), sqlb.Set(uAge, int32(1))).Clause(pg.OverridingUserValue()))
	executes(t, db, "OnConstraint", ins().Into(catUser).Row(sqlb.Set(uID, int64(1)), sqlb.Set(uName, "z"), sqlb.Set(uAge, int32(1))).Clause(pg.OnConstraint(pg.TableConstraint{Table: catUser, Name: "cat_user_pkey"}).DoNothing()))
	executes(t, db, "OnConflict TargetWhere DoUpdate UpdateWhere Excluded", ins().Into(catUser).Row(sqlb.Set(uID, int64(1)), sqlb.Set(uName, "z"), sqlb.Set(uAge, int32(1))).
		Clause(pg.OnConflict(uID).TargetWhere(sqlb.Greater(uID, int64(0))).DoUpdate(sqlb.SetExpr(uName, pg.Excluded(uName))).UpdateWhere(pg.IsDistinctFrom(uName, pg.Excluded(uName))).Clause()))
	// lock table, merge, truncate
	executes(t, db, "LockTable", pg.NewLockTable(pgd, catUser).In(pg.LockShare).NoWait())
	executes(t, db, "LockTable every mode", pg.NewLockTable(pgd).Table(catUser, catOrder).In(pg.LockAccessShare))
	src := pg.Values("s", []string{"id", "name"}, []sqlb.SQLer{sqlb.Cast[int64](sqlb.Param[int64](1), "bigint"), sqlb.Cast[string](sqlb.Param("ada2"), "text")}, []sqlb.SQLer{sqlb.Param[int64](99), sqlb.Param("new")})
	sID := sqlb.Col[int64](sqlb.Ref(src), "id")
	sName := sqlb.Col[string](sqlb.Ref(src), "name")
	executes(t, db, "Merge", pg.NewMerge(pgd).Into(catUser).Using(src, sqlb.EqExpr(uID, sID)).
		WhenMatched(sqlb.Eq(uAge, int32(0))).ThenDelete().
		WhenMatched().ThenUpdate(sqlb.SetExpr(uName, sName)).
		WhenNotMatched().ThenInsert(sqlb.SetExpr(uID, sID), sqlb.SetExpr(uName, sName), sqlb.Set(uAge, int32(1))).
		WhenNotMatchedBySource().ThenDoNothing())
	var action, name string
	executes(t, db, "Merge Returning MergeAction", pg.NewMerge(pgd).Into(catUser).Using(src, sqlb.EqExpr(uID, sID)).
		WhenMatched().ThenUpdate(sqlb.SetExpr(uName, sName)).
		WhenNotMatched().ThenInsert(sqlb.SetExpr(uID, sID), sqlb.SetExpr(uName, sName), sqlb.Set(uAge, int32(1))).
		Returning(sqlb.Bind(pg.MergeAction(), &action), sqlb.Bind(uName, &name)))
	executes(t, db, "Truncate", pg.NewTruncate(pgd, catOrder).RestartIdentity().Cascade())
	executes(t, db, "Truncate Only ContinueIdentity Restrict", pg.NewTruncate(pgd).Table(catOrder).Only().ContinueIdentity().Restrict())
	// recursive CTE options
	tree := sqlb.Table{Name: "tree", Alias: "tr"}
	trID := sqlb.Col[int64](tree, "id")
	trUser := sqlb.Col[int64](tree, "user_id")
	seed := sel().Exprs(oID, oUser).From(catOrder).Where(sqlb.Eq(oID, int64(10)))
	step := sel().Exprs(oID, oUser).From(catOrder).Join(sqlb.Join{Type: sqlb.JoinInner, Table: tree, On: []sqlb.Predicate{sqlb.EqExpr(oUser, trUser)}}).Where(sqlb.GreaterExpr(oID, trID))
	executes(t, db, "SearchDepthFirst", sel().WithRecursiveCTE(sqlb.CTE{Name: "tree", Columns: []string{"id", "user_id"}, Body: seed.UnionAll(step), Suffix: pg.SearchDepthFirst("ord", "id")}).Exprs(trID).From(tree).Order(sqlb.OrderBy(sqlb.Col[string](tree, "ord"), sqlb.SortAsc)))
	executes(t, db, "SearchBreadthFirst", sel().WithRecursiveCTE(sqlb.CTE{Name: "tree", Columns: []string{"id", "user_id"}, Body: seed.UnionAll(step), Suffix: pg.SearchBreadthFirst("ord", "id")}).Exprs(trID).From(tree))
	executes(t, db, "Cycle", sel().WithRecursiveCTE(sqlb.CTE{Name: "tree", Columns: []string{"id", "user_id"}, Body: seed.UnionAll(step), Suffix: pg.Cycle("is_cycle", "path", "id")}).Exprs(trID).From(tree))
	executes(t, db, "Both", sel().WithRecursiveCTE(sqlb.CTE{Name: "tree", Columns: []string{"id", "user_id"}, Body: seed.UnionAll(step), Suffix: pg.Both(pg.SearchDepthFirst("ord", "id"), pg.Cycle("is_cycle", "path", "id"))}).Exprs(trID).From(tree))
	// misc sources and arguments
	rs := pg.RecordSet(pg.Func[string]("jsonb_to_record", "jsonb_to_record", mDoc), "r", pg.ColumnDef{Name: "n", Type: "text"})
	executes(t, db, "RecordSet", sel().Exprs(sqlb.Col[string](sqlb.Ref(rs), "n")).From(catMisc).Join(sqlb.Join{Type: sqlb.JoinCross, Source: rs}).Where(pg.HasKey(mDoc, "a")))
	expr(t, db, "Named", pg.Func[pg.Interval]("custom", "make_interval", pg.Named("days", sqlb.Cast[int32](sqlb.Param[int32](1), "int"))))
	// ext.go mechanisms, one custom construct each
	expr(t, db, "Expr", pg.Expr[int64]("custom", func(_ *sqlb.Args) string { return "1" }))
	pred(t, db, "Pred", pg.Pred("custom", func(_ *sqlb.Args) string { return "true" }))
	expr(t, db, "BinOp", pg.BinOp[int64]("custom", "+", mID, mID))
	pred(t, db, "BinPred", pg.BinPred("custom", "=", mID, mID))
	expr(t, db, "Func", pg.Func[string]("custom", "upper", mBody))
	executes(t, db, "ClauseIn", sel().Exprs(mID).From(catMisc).Clause(pg.ClauseIn[sqlb.SelectKind](sqlb.SlotAfterOrder, "custom", func(_ *sqlb.Args) string { return "LIMIT 1" })))
	expr(t, db, "QuoteLit", pg.Func[string]("custom", "upper", pg.QuoteLit("it's")))
	expr(t, db, "List", pg.Expr[string]("custom", func(a *sqlb.Args) string { return "concat(" + pg.List(a, []sqlb.SQLer{mBody, mBody}) + ")" }))
	executes(t, db, "Guard", sel().Exprs(sqlb.Raw(func(a *sqlb.Args) string { pg.Guard(a, "custom"); return "1" })))
}
