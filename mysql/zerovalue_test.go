package mysql_test

import (
	"testing"
	"time"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/mysql"
	"github.com/ubgo/sqlb/sqlbtest"
)

// TestZeroValues_AreRefused builds the zero value of every exported mysql type
// and fails on a panic or a clean build; see sqlbtest.ZeroValues.
func TestZeroValues_AreRefused(t *testing.T) {
	sqlbtest.ZeroValues(t, sqlbtest.ZeroConfig{
		Dir:     ".",
		Dialect: mysql.Dialect{},
		Values: []any{
			mysql.Partitioning{},
			mysql.IndexKeyElem[int64]{},
			mysql.CreateIndexReady{},
			mysql.DropIndexStatement{},
			mysql.CreateViewStatement{},
			mysql.DropStatement{},
			mysql.CreateTriggerStatement{},
			mysql.TruncateStatement{},
			mysql.RenameTableStatement{},
			mysql.KeyClause{},
			mysql.ForeignKeyClause{},
			mysql.CheckClause{},
			mysql.ExchangePartitionClause{},
			mysql.AlterViewStatement{},
			mysql.CreateEventStatement{},
			mysql.AlterEventStatement{},
			mysql.CreateRoutineStatement{},
			mysql.AlterRoutineStatement{},
			mysql.ColumnAction{},
			mysql.CreateTableLikeStatement{},
			mysql.CreateTableAsStatement{},
			mysql.DropTemporaryTableStatement{},
		},
		Probes: map[string]func() error{
			"PartitionKey": func() error {
				return fails(mysql.CreateTable(zt).Column(ddl.Column(zID)).Clause(mysql.PartitionByHash(mysql.PartitionKey{}, 2)).Build())
			},
			"PartitionDef": func() error {
				return fails(mysql.CreateTable(zt).Column(ddl.Column(zID)).Clause(mysql.PrimaryKey(zID), mysql.PartitionByRange(mysql.PartitionOn(zID)).With(mysql.PartitionDef{})).Build())
			},
			"Index":            func() error { return fails(mysql.CreateIndex(mysql.Index{}, zt).On(mysql.IndexKey(zID)).Build()) },
			"CreateIndexQuery": func() error { return fails((&mysql.CreateIndexQuery{}).On(mysql.IndexKey(zID)).Build()) },
			"View":             func() error { return fails(mysql.CreateView(mysql.View{}).As(zSelect()).Build()) },
			"CreateViewQuery":  func() error { return fails((&mysql.CreateViewQuery{}).As(zSelect()).Build()) },
			"Trigger": func() error {
				return fails(mysql.CreateTrigger(mysql.Trigger{}).After(mysql.OnDelete, zt).Do(zStep()).Build())
			},
			"CreateTriggerQuery": func() error {
				return fails((&mysql.CreateTriggerQuery{}).After(mysql.OnDelete, zt).Do(zStep()).Build())
			},
			"TimedTriggerQuery": func() error { return fails((&mysql.TimedTriggerQuery{}).Do(zStep()).Build()) },
			"TriggerStep": func() error {
				return fails(mysql.CreateTrigger(mysql.Trigger{Name: "t"}).After(mysql.OnDelete, zt).Do(mysql.TriggerStep{}).Build())
			},
			"ColumnPair": func() error {
				return fails(mysql.CreateTable(zt).Column(ddl.Column(zID)).Clause(mysql.ForeignKey(mysql.TableConstraint{Table: zt, Name: "fk"}, mysql.ColumnPair{})).Build())
			},
			"AlterViewQuery": func() error { return fails((&mysql.AlterViewQuery{}).As(zSelect()).Build()) },
			"Event":          func() error { return fails(zEvent(mysql.Event{}).Build()) },
			"Schedule": func() error {
				return fails(mysql.CreateEvent(mysql.Event{Name: "e"}).On(mysql.Schedule{}).Do(zStep()).Build())
			},
			"CreateEventQuery": func() error {
				return fails((&mysql.CreateEventQuery{}).On(mysql.EventAt(zTime)).Do(zStep()).Build())
			},
			"ScheduledEventQuery": func() error { return fails((&mysql.ScheduledEventQuery{}).Do(zStep()).Build()) },
			"AlterEventQuery": func() error {
				return fails((&mysql.AlterEventQuery{}).Status(mysql.EventDisable).Build())
			},
			"Function":  func() error { return fails(zFunc(mysql.Function{}).Build()) },
			"Procedure": func() error { return fails(zProc(mysql.Procedure{}).Build()) },
			"Param": func() error {
				return fails(mysql.CreateProcedure(mysql.Procedure{Name: "p"}).Param(mysql.Param{}).Body(zBody).Build())
			},
			"CreateRoutineQuery": func() error { return fails((&mysql.CreateRoutineQuery{}).Body(zBody).Build()) },
			"AlterRoutineQuery": func() error {
				return fails((&mysql.AlterRoutineQuery{}).Characteristic(mysql.LanguageSQL).Build())
			},
			"IndexOption": func() error {
				return fails(mysql.CreateIndex(mysql.Index{Name: "i"}, zt).On(mysql.IndexKey(zID)).With(mysql.IndexOption{}).Build())
			},
		},
		Controls: map[string]func() error{
			"PartitionKey": func() error {
				return fails(mysql.CreateTable(zt).Column(ddl.Column(zID)).Clause(mysql.PrimaryKey(zID), mysql.PartitionByHash(mysql.PartitionOn(zID), 2)).Build())
			},
			"PartitionDef": func() error {
				return fails(mysql.CreateTable(zt).Column(ddl.Column(zID)).Clause(mysql.PrimaryKey(zID), mysql.PartitionByRange(mysql.PartitionOn(zID)).With(mysql.LessThan("a", 1))).Build())
			},
			"Index": func() error {
				return fails(mysql.CreateIndex(mysql.Index{Name: "i"}, zt).On(mysql.IndexKey(zID)).Build())
			},
			"CreateIndexQuery": func() error {
				return fails(mysql.CreateIndex(mysql.Index{Name: "i"}, zt).On(mysql.IndexKey(zID)).Build())
			},
			"View":            func() error { return fails(mysql.CreateView(mysql.View{Name: "v"}).As(zSelect()).Build()) },
			"CreateViewQuery": func() error { return fails(mysql.CreateView(mysql.View{Name: "v"}).As(zSelect()).Build()) },
			"Trigger": func() error {
				return fails(mysql.CreateTrigger(mysql.Trigger{Name: "t"}).After(mysql.OnDelete, zt).Do(zStep()).Build())
			},
			"CreateTriggerQuery": func() error {
				return fails(mysql.CreateTrigger(mysql.Trigger{Name: "t"}).After(mysql.OnDelete, zt).Do(zStep()).Build())
			},
			"TimedTriggerQuery": func() error {
				return fails(mysql.CreateTrigger(mysql.Trigger{Name: "t"}).After(mysql.OnDelete, zt).Do(zStep()).Build())
			},
			"TriggerStep": func() error {
				return fails(mysql.CreateTrigger(mysql.Trigger{Name: "t"}).After(mysql.OnDelete, zt).Do(zStep()).Build())
			},
			"ColumnPair": func() error {
				return fails(mysql.CreateTable(zt).Column(ddl.Column(zID)).Clause(mysql.ForeignKey(mysql.TableConstraint{Table: zID.Table(), Name: "fk"}, mysql.RefPair(zID, z2ID))).Build())
			},
			"AlterViewQuery": func() error {
				return fails(mysql.AlterView(mysql.View{Name: "v"}).As(zSelect()).Build())
			},
			"Event":    func() error { return fails(zEvent(mysql.Event{Name: "e"}).Build()) },
			"Schedule": func() error { return fails(zEvent(mysql.Event{Name: "e"}).Build()) },
			"CreateEventQuery": func() error {
				return fails(mysql.CreateEvent(mysql.Event{Name: "e"}).On(mysql.EventAt(zTime)).Do(zStep()).Build())
			},
			"ScheduledEventQuery": func() error {
				return fails(mysql.CreateEvent(mysql.Event{Name: "e"}).On(mysql.EventAt(zTime)).Do(zStep()).Build())
			},
			"AlterEventQuery": func() error {
				return fails(mysql.AlterEvent(mysql.Event{Name: "e"}).Status(mysql.EventDisable).Build())
			},
			"Function":  func() error { return fails(zFunc(mysql.Function{Name: "f"}).Build()) },
			"Procedure": func() error { return fails(zProc(mysql.Procedure{Name: "p"}).Build()) },
			"Param": func() error {
				return fails(mysql.CreateProcedure(mysql.Procedure{Name: "p"}).Param(mysql.RoutineParam("n", zBigint)).Body(zBody).Build())
			},
			"CreateRoutineQuery": func() error { return fails(zProc(mysql.Procedure{Name: "p"}).Build()) },
			"AlterRoutineQuery": func() error {
				return fails(mysql.AlterProcedure(mysql.Procedure{Name: "p"}).Characteristic(mysql.LanguageSQL).Build())
			},
			"IndexOption": func() error {
				return fails(mysql.CreateIndex(mysql.Index{Name: "i"}, zt).On(mysql.IndexKey(zID)).With(mysql.IndexComment("c")).Build())
			},
		},
		Exempt: map[string]string{
			"Placement": "a zero Placement means no placement, which is exactly what a column action without one renders; it adds no text and cannot make a statement wrong, so there is nothing for it to be refused for. After's own bad input, an expression that is not a column, is refused by ErrNotAColumn.",
		},
	})
}

// TestTriggerEvent_OutsideTheConstantsIsRefused pins the closed set: a
// TriggerEvent is a string type, so TriggerEvent("TRUNCATE") compiles, and
// MySQL has no such trigger.
func TestTriggerEvent_OutsideTheConstantsIsRefused(t *testing.T) {
	for _, e := range []mysql.TriggerEvent{"", "TRUNCATE"} {
		if _, err := mysql.CreateTrigger(mysql.Trigger{Name: "t"}).After(e, zt).Do(zStep()).Build(); err == nil {
			t.Errorf("event %q built", e)
		}
	}
}

var (
	zt   = sqlb.Table{Name: "z", Alias: "z"}
	zID  = sqlb.Col[int64](zt, "id")
	zt2  = sqlb.Table{Name: "z2", Alias: "y"}
	z2ID = sqlb.Col[int64](zt2, "id")
)

func zSelect() *mysql.SelectQuery { return mysql.Select().Columns(sqlb.Bind(zID, new(int64))).From(zt) }

func zStep() mysql.TriggerStep {
	return mysql.StepDelete(mysql.Delete(zt2).Where(sqlb.EqExpr(z2ID, mysql.OldRow(zID))))
}

func fails(_ ddl.Script, err error) error { return err }

// zBody is a routine body that is syntactically complete and does nothing, so
// a zero-value probe fails on the value under test rather than on the body.
const zBody = "BEGIN END"

var (
	zTime   = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	zBigint = ddl.RawType[int64]("BIGINT")
)

func zEvent(e mysql.Event) *mysql.CreateEventStatement {
	return mysql.CreateEvent(e).On(mysql.EventAt(zTime)).Do(zStep())
}

func zFunc(f mysql.Function) *mysql.CreateRoutineStatement {
	return mysql.CreateFunction(f, zBigint).Characteristic(mysql.NoSQL).Body("RETURN 1")
}

func zProc(p mysql.Procedure) *mysql.CreateRoutineStatement {
	return mysql.CreateProcedure(p).Body(zBody)
}
