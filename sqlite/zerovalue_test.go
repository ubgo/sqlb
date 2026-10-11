package sqlite_test

import (
	"testing"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/sqlbtest"
	"github.com/ubgo/sqlb/sqlite"
)

// TestZeroValues_AreRefused builds the zero value of every exported sqlite type
// and fails on a panic or a clean build; see sqlbtest.ZeroValues.
func TestZeroValues_AreRefused(t *testing.T) {
	sqlbtest.ZeroValues(t, sqlbtest.ZeroConfig{
		Dir:     ".",
		Dialect: sqlite.Dialect{},
		Values: []any{
			sqlite.CreateRTreeStatement{},
			sqlite.CreateFTS5Statement{},
			sqlite.CreateVirtualTableStatement{},
			sqlite.CreateTableAsStatement{},
			sqlite.IndexKeyElem[int64]{},
			sqlite.CreateIndexReady{},
			sqlite.DropStatement{},
			sqlite.CreateViewStatement{},
			sqlite.CreateTriggerStatement{},
			sqlite.PragmaStatement{},
			sqlite.RebuildStatement{},
			sqlite.KeyClause{},
			sqlite.ColumnKeyClause{},
			sqlite.NotNullConflictClause{},
			sqlite.ForeignKeyClause{},
		},
		Probes: map[string]func() error{
			"CreateRTreeQuery": func() error {
				return fails((&sqlite.CreateRTreeQuery{}).Columns(zID, sqlite.Dimension(zMinX, zMaxX)).Build())
			},
			"Dim":              func() error { return fails(sqlite.CreateRTree(zt).Columns(zID, sqlite.Dim{}).Build()) },
			"Index":            func() error { return fails(sqlite.CreateIndex(sqlite.Index{}, zt).On(sqlite.IndexKey(zID)).Build()) },
			"View":             func() error { return fails(sqlite.CreateView(sqlite.View{}).As(zSelect()).Build()) },
			"Trigger":          func() error { return fails(zTrigger(sqlite.Trigger{}, sqlite.OnInsert(), zStep()).Build()) },
			"CreateIndexQuery": func() error { return fails((&sqlite.CreateIndexQuery{}).On(sqlite.IndexKey(zID)).Build()) },
			"CreateViewQuery":  func() error { return fails((&sqlite.CreateViewQuery{}).As(zSelect()).Build()) },
			"CreateFTS5Query":  func() error { return fails((&sqlite.CreateFTS5Query{}).Columns(zName).Build()) },
			"CreateTriggerQuery": func() error {
				return fails((&sqlite.CreateTriggerQuery{}).After(sqlite.OnInsert(), zt).Do(zStep()).Build())
			},
			"TimedTriggerQuery": func() error { return fails((&sqlite.TimedTriggerQuery{}).Do(zStep()).Build()) },
			"RebuildQuery": func() error {
				return fails((&sqlite.RebuildQuery{}).Into(zNewTable()).Copy(sqlite.CopyAs(zNewID, zID)).Build())
			},
			"TriggerEvent": func() error {
				return fails(zTrigger(sqlite.Trigger{Name: "t"}, sqlite.TriggerEvent{}, zStep()).Build())
			},
			"TriggerStep": func() error {
				return fails(zTrigger(sqlite.Trigger{Name: "t"}, sqlite.OnInsert(), sqlite.TriggerStep{}).Build())
			},
			"Tokenizer": func() error { return fails(sqlite.CreateFTS5(zt).Columns(zName).Tokenize(sqlite.Tokenizer{}).Build()) },
			"CopyPair":  func() error { return fails(sqlite.Rebuild(zt).Into(zNewTable()).Copy(sqlite.CopyPair{}).Build()) },
			"ColumnPair": func() error {
				return fails(sqlite.CreateTable(zt).Column(ddl.Column(zID)).Clause(sqlite.ForeignKey(sqlite.TableConstraint{Table: zt, Name: "fk"}, sqlite.ColumnPair{})).Build())
			},
			"Pair": func() error {
				return failsQ(sqlite.Select().Columns(sqlb.Bind(sqlite.JSONObject(sqlite.Pair{}), new(string))).Build())
			},
			"KeyColumn": func() error {
				return fails(sqlite.CreateTable(zt).Column(ddl.Column(zID)).Clause(sqlite.UniqueOn(sqlite.KeyColumn{})).Build())
			},
		},
		Controls: map[string]func() error{
			"CreateRTreeQuery": func() error {
				return fails(sqlite.CreateRTree(zt).Columns(zID, sqlite.Dimension(zMinX, zMaxX)).Build())
			},
			"Dim": func() error {
				return fails(sqlite.CreateRTree(zt).Columns(zID, sqlite.Dimension(zMinX, zMaxX)).Build())
			},
			"Index": func() error {
				return fails(sqlite.CreateIndex(sqlite.Index{Name: "i"}, zt).On(sqlite.IndexKey(zID)).Build())
			},
			"View":    func() error { return fails(sqlite.CreateView(sqlite.View{Name: "v"}).As(zSelect()).Build()) },
			"Trigger": func() error { return fails(zTrigger(sqlite.Trigger{Name: "t"}, sqlite.OnInsert(), zStep()).Build()) },
			"CreateIndexQuery": func() error {
				return fails(sqlite.CreateIndex(sqlite.Index{Name: "i"}, zt).On(sqlite.IndexKey(zID)).Build())
			},
			"CreateViewQuery": func() error { return fails(sqlite.CreateView(sqlite.View{Name: "v"}).As(zSelect()).Build()) },
			"CreateFTS5Query": func() error { return fails(sqlite.CreateFTS5(zt).Columns(zName).Build()) },
			"CreateTriggerQuery": func() error {
				return fails(zTrigger(sqlite.Trigger{Name: "t"}, sqlite.OnInsert(), zStep()).Build())
			},
			"TimedTriggerQuery": func() error { return fails(zTrigger(sqlite.Trigger{Name: "t"}, sqlite.OnInsert(), zStep()).Build()) },
			"RebuildQuery": func() error {
				return fails(sqlite.Rebuild(zt).Into(zNewTable()).Copy(sqlite.CopyAs(zNewID, zID)).Build())
			},
			"TriggerEvent": func() error { return fails(zTrigger(sqlite.Trigger{Name: "t"}, sqlite.OnDelete(), zStep()).Build()) },
			"TriggerStep":  func() error { return fails(zTrigger(sqlite.Trigger{Name: "t"}, sqlite.OnInsert(), zStep()).Build()) },
			"Tokenizer":    func() error { return fails(sqlite.CreateFTS5(zt).Columns(zName).Tokenize(sqlite.Trigram()).Build()) },
			"CopyPair": func() error {
				return fails(sqlite.Rebuild(zt).Into(zNewTable()).Copy(sqlite.CopyAs(zNewID, zID)).Build())
			},
			"ColumnPair": func() error {
				return fails(sqlite.CreateTable(zt).Column(ddl.Column(zID)).Clause(sqlite.ForeignKey(sqlite.TableConstraint{Table: zID.Table(), Name: "fk"}, sqlite.RefPair(zID, z2ID))).Build())
			},
			"Pair": func() error {
				return failsQ(sqlite.Select().Columns(sqlb.Bind(sqlite.JSONObject(sqlite.KV("k", zName)), new(string))).Build())
			},
			"KeyColumn": func() error {
				return fails(sqlite.CreateTable(zt).Column(ddl.Column(zID)).Clause(sqlite.UniqueOn(sqlite.Key(zID))).Build())
			},
		},
		Exempt: map[string]string{
			"Conflict": "the zero value is ON CONFLICT with no target, which SQLite 3.35 and later accepts on the last clause; TestUpsert_NoTargetOnTheLastClause in contrib/driver-sqlite executes it",
		},
	})
}

var (
	zt     = sqlb.Table{Name: "z", Alias: "z"}
	zID    = sqlb.Col[int64](zt, "id")
	zName  = sqlb.Col[string](zt, "name")
	zt2    = sqlb.Table{Name: "z2", Alias: "y"}
	z2ID   = sqlb.Col[int64](zt2, "id")
	zNew   = sqlb.Table{Name: "z_new"}
	zNewID = sqlb.Col[int64](zNew, "id")
	zMinX  = sqlb.Col[float64](zt, "min_x")
	zMaxX  = sqlb.Col[float64](zt, "max_x")
)

func zSelect() *sqlite.SelectQuery {
	return sqlite.Select().Columns(sqlb.Bind(zID, new(int64))).From(zt)
}

func zStep() sqlite.TriggerStep {
	return sqlite.StepDelete(sqlite.Delete(zt2).Where(sqlb.EqExpr(z2ID, sqlite.New(zID))))
}

func zTrigger(tr sqlite.Trigger, e sqlite.TriggerEvent, step sqlite.TriggerStep) *sqlite.CreateTriggerStatement {
	return sqlite.CreateTrigger(tr).After(e, zt).Do(step)
}

func zNewTable() *sqlite.CreateTableReady {
	return sqlite.CreateTable(zNew).Column(ddl.Column(zNewID)).Clause(sqlite.PrimaryKey(zNewID))
}

func fails(_ ddl.Script, err error) error { return err }

func failsQ(_ sqlb.Query, err error) error { return err }
