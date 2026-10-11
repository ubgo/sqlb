package pg_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/pg"
	"github.com/ubgo/sqlb/sqlbtest"
)

// TestZeroValues_AreRefused builds the zero value of every exported pg type
// and fails on a panic or a clean build; see sqlbtest.ZeroValues.
func TestZeroValues_AreRefused(t *testing.T) {
	sqlbtest.ZeroValues(t, sqlbtest.ZeroConfig{
		Dir:     ".",
		Dialect: pg.Dialect{},
		Values: []any{
			pg.AlterConstraintClause{},
			pg.AlterViewStatement{},
			pg.CreateCompositeStatement[string]{},
			pg.AlterCompositeStatement[string]{},
			pg.DropColumnClause{},
			pg.AlterTypeClause[string]{},
			pg.DropConstraintClause{},
			pg.ExcludeClause{},
			pg.CreateTableLikeStatement{},
			pg.IndexKeyElem[int64]{},
			pg.CreateIndexReady{},
			pg.RenameIndexStatement{},
			pg.DropIndexStatement{},
			pg.CommentStatement{},
			pg.LockTable{},
			pg.Merge{},
			pg.CreateSchemaStatement{},
			pg.DropSchemaStatement{},
			pg.CreateExtensionStatement{},
			pg.DropExtensionStatement{},
			pg.CreateEnumStatement[string]{},
			pg.AddValueStatement[string]{},
			pg.AlterEnumStatement[string]{},
			pg.DropTypeStatement{},
			pg.GuardedStatement{},
			pg.ConstraintStatement{},
			pg.RenameSchemaStatement{},
			pg.CreatePartitionStatement{},
			pg.DetachStatement{},
			pg.CreatePolicyStatement{},
			pg.AlterPolicyStatement{},
			pg.DropPolicyStatement{},
			pg.CreateSequenceStatement[int64]{},
			pg.AlterSequenceStatement[int64]{},
			pg.DropObjectStatement{},
			pg.PrimaryKeyClause{},
			pg.UniqueClause{},
			pg.ForeignKeyClause{},
			pg.CheckClause{},
			pg.CreateDomainStatement[string]{},
			pg.AlterDomainStatement[string]{},
			pg.CreateCollationStatement{},
			pg.CreateStatisticsStatement{},
			pg.AlterExtensionStatement{},
			pg.RenameMaterializedViewStatement{},
			pg.CreateFunctionStatement{},
			pg.CreateTriggerStatement{},
			pg.DropTriggerStatement{},
			pg.RenameTriggerStatement{},
			pg.Truncate{},
			pg.CreateViewStatement{},
			pg.RenameViewStatement{},
			pg.CreateMaterializedViewStatement{},
			pg.RefreshStatement{},
			pg.AlterViewOptionsStatement{},
			pg.SetSchemaStatement{},
			pg.DropColumnPartClause{},
			pg.RestartIdentityClause[int64]{},
			pg.AlterIndexStatement{},
			pg.AlterMaterializedViewStatement{},
			pg.RenameViewColumnStatement{},
			pg.ExtensionMemberStatement{},
			pg.SetExtensionSchemaStatement{},
			pg.SetOwnerStatement{},
			pg.CreateRoutineStatement{},
			pg.CreateRangeTypeStatement[int64]{},
			pg.CreateShellTypeStatement[int64]{},
			pg.CreateBaseTypeStatement[int64]{},
			pg.AlterTypeNameStatement{},
			pg.AlterBaseTypeStatement[int64]{},
			pg.DependsOnExtensionStatement{},
			pg.CreateTableAsStatement{},
		},
		Probes: map[string]func() error{
			"AlterBaseTypeQuery": func() error {
				return fails((&pg.AlterBaseTypeQuery[int64]{}).SetProperties().Storage(pg.StoragePlain).Build())
			},
			"ViewSetting": func() error {
				return fails(pg.CreateView(pg.View{Name: "v"}).As(pg.Select(zID).From(zt)).WithOptions(pg.ViewSetting{}).Build())
			},
			"AlterRangeTypeQuery": func() error {
				return fails((&pg.AlterRangeTypeQuery[int64]{}).RenameTo(pg.RangeType[int64]{Name: "r2"}).Build())
			},
			// A query type has no Build of its own, so its zero value is
			// probed through the statement it starts.
			"AlterIndexQuery": func() error { return fails((&pg.AlterIndexQuery{}).ResetStorage("fillfactor").Build()) },
			"AlterMaterializedViewQuery": func() error {
				return fails((&pg.AlterMaterializedViewQuery{}).RenameColumn("a", "b").Build())
			},
			"RangeType": func() error {
				return fails(pg.CreateRangeType(pg.RangeType[int64]{}, ddl.RawType[int64]("bigint")).Build())
			},
			"BaseType": func() error { return fails(pg.CreateShellType(pg.BaseType[int64]{}).Build()) },
			"Role":     func() error { return fails(pg.SetOwner(pg.View{Name: "v"}, pg.Role{}).Build()) },
			"Rule":     func() error { return fails(pg.AlterTable(zt).Clause(pg.EnableRule(pg.Rule{})).Build()) },
			"StorageParameter": func() error {
				return fails(pg.AlterIndex(pg.Index{Name: "ix"}).SetStorage(pg.StorageParameter{}).Build())
			},
			"TypeRef": func() error {
				return fails(pg.CreateBaseType(pg.BaseType[int64]{Name: "b"}, pg.Function{Name: "i"}, pg.Function{Name: "o"}).Like(pg.TypeRef{}).Build())
			},
			"FunctionArg": func() error {
				f := pg.Function{Name: "f", Params: []pg.FunctionArg{{}}}
				return fails(pg.CreateFunction(f, pg.LangSQL, "SELECT 1").Returns(pg.Returns(ddl.RawType[int64]("bigint"))).Build())
			},
			"FunctionResult": func() error {
				return fails(pg.CreateFunction(pg.Function{Name: "f"}, pg.LangSQL, "SELECT 1").Returns(pg.FunctionResult{}).Build())
			},
			"IdentitySequenceClause": func() error {
				return fails(pg.AlterTable(zt).Clause(pg.IdentitySequenceClause[int64]{}).Build())
			},
			"PeriodColumn": func() error {
				return fails(pg.CreateTable(zt).Column(ddl.Column(zID)).Clause(pg.PrimaryKey(zID).WithoutOverlaps(pg.PeriodColumn{})).Build())
			},
			"Composite":           func() error { return fails(pg.CreateComposite(pg.Composite[string]{}, zAttr).Build()) },
			"Attribute":           func() error { return fails(pg.CreateComposite(zComposite, pg.Attribute{}).Build()) },
			"AlterCompositeQuery": func() error { return fails((&pg.AlterCompositeQuery[string]{}).AddAttribute(zAttr).Build()) },
			"DomainConstraint": func() error {
				return fails(pg.AlterDomain(pg.Domain[string]{Name: "d"}).DropConstraint(pg.DomainConstraint[string]{}).Build())
			},
			// An object value is passed to its constructor.
			"Schema":     func() error { return fails(pg.CreateSchema(pg.Schema{}).Build()) },
			"Extension":  func() error { return fails(pg.CreateExtension(pg.Extension{}).Build()) },
			"Enum":       func() error { return fails(pg.CreateEnum(pg.Enum[string]{}, "a").Build()) },
			"Index":      func() error { return fails(pg.CreateIndex(pg.Index{}, zt).On(pg.IndexKey(zID)).Build()) },
			"Policy":     func() error { return fails(pg.CreatePolicy(pg.Policy{}).Build()) },
			"Sequence":   func() error { return fails(pg.CreateSequence(pg.Sequence[int64]{}).Build()) },
			"Domain":     func() error { return fails(pg.CreateDomain(pg.Domain[string]{}, ddl.RawType[string]("text")).Build()) },
			"Collation":  func() error { return fails(pg.CreateCollation(pg.Collation{}, "und-x-icu").Build()) },
			"Statistics": func() error { return fails(pg.CreateStatistics(pg.Statistics{}, zt, zID, zName).Build()) },
			"Function":   func() error { return fails(pg.CreateTriggerFunction(pg.Function{}, "BEGIN RETURN NEW; END").Build()) },
			"Trigger":    func() error { return fails(pg.DropTrigger(pg.Trigger{}).Build()) },
			"View": func() error {
				return fails(pg.CreateView(pg.View{}).As(pg.Select().Columns(sqlb.Bind(zID, new(int64))).From(zt)).Build())
			},
			"MaterializedView": func() error { return fails(pg.RefreshMaterializedView(pg.MaterializedView{}).Build()) },
			// A half-built builder is completed through its method.
			"AlterDomainQuery": func() error {
				return fails((&pg.AlterDomainQuery[string]{}).DropConstraint(pg.DomainConstraint[string]{Domain: pg.Domain[string]{Name: "d"}, Name: "c"}).Build())
			},
			"AlterEnumQuery":     func() error { return fails((&pg.AlterEnumQuery[string]{}).AddValue("a").Build()) },
			"AlterPolicyQuery":   func() error { return fails((&pg.AlterPolicyQuery{}).Using(sqlb.Eq(zID, int64(1))).Build()) },
			"AlterSequenceQuery": func() error { return fails((&pg.AlterSequenceQuery[int64]{}).Restart(1).Build()) },
			"CommentQuery":       func() error { return fails((&pg.CommentQuery{}).Is("x").Build()) },
			"CreateIndexQuery":   func() error { return fails((&pg.CreateIndexQuery{}).On(pg.IndexKey(zID)).Build()) },
			"CreateMaterializedViewQuery": func() error {
				return fails((&pg.CreateMaterializedViewQuery{}).As(pg.Select().Columns(sqlb.Bind(zID, new(int64))).From(zt)).Build())
			},
			"CreatePartitionQuery": func() error { return fails((&pg.CreatePartitionQuery{}).For(pg.DefaultPartition()).Build()) },
			"CreateTriggerQuery": func() error {
				return fails((&pg.CreateTriggerQuery{}).After(pg.OnInsert()).Execute(pg.Function{Name: "f"}).Build())
			},
			"TimedTriggerQuery": func() error { return fails((&pg.TimedTriggerQuery{}).Execute(pg.Function{Name: "f"}).Build()) },
			"CreateViewQuery": func() error {
				return fails((&pg.CreateViewQuery{}).As(pg.Select().Columns(sqlb.Bind(zID, new(int64))).From(zt)).Build())
			},
			"WhenBuilder": func() error { return failsQ(pg.WhenBuilder{}.ThenDelete().Build()) },
			// A part is placed in the construct that consumes it.
			"ColumnPair": func() error {
				return fails(pg.CreateTable(zt).Column(ddl.Column(zID)).Clause(pg.ForeignKey(pg.TableConstraint{Table: zt, Name: "fk"}, pg.ColumnPair{})).Build())
			},
			"ExcludeElement": func() error {
				return fails(pg.CreateTable(zt).Column(ddl.Column(zID)).Clause(pg.Exclude(pg.TableConstraint{Table: zt, Name: "x"}, pg.GiST, pg.ExcludeElement{})).Build())
			},
			"IndexParam": func() error {
				return fails(pg.CreateIndex(pg.Index{Name: "i"}, zt).On(pg.IndexKey(zID)).With(pg.IndexParam{}).Build())
			},
			"OpClass": func() error {
				return fails(pg.CreateIndex(pg.Index{Name: "i"}, zt).On(pg.IndexKey(zID).Opclass(pg.OpClass[int64]{})).Build())
			},
			"TriggerEvent": func() error {
				return fails(pg.CreateTrigger(pg.Trigger{Name: "t"}).After(pg.TriggerEvent{}).Execute(pg.Function{Name: "f"}).Build())
			},
			"Conflict": func() error {
				return failsQ(pg.Insert(zt).Row(sqlb.Set(zID, int64(1))).Clause((&pg.Conflict{}).DoUpdate(sqlb.Set(zName, "a")).Clause()).Build())
			},
			"ColumnDef": func() error {
				return failsQ(pg.Select().Columns(sqlb.Bind(zID, new(int64))).From(pg.RecordSet(sqlb.Raw(func(*sqlb.Args) string { return "f()" }), "r", pg.ColumnDef{})).Build())
			},
			"JSONPair": func() error {
				return failsQ(pg.Select().Columns(sqlb.Bind(pg.JSONObject([]pg.JSONPair{{}}), new(json.RawMessage))).Build())
			},
			"JSONTableColumn": func() error {
				return failsQ(pg.Select().Columns(sqlb.Bind(zID, new(int64))).From(pg.JSONTable(zName, "$", "j", pg.JSONTableColumn{})).Build())
			},
			"JSONTableNested": func() error {
				return failsQ(pg.Select().Columns(sqlb.Bind(zID, new(int64))).From(pg.JSONTable(zName, "$", "j", pg.JSONTableColumn{Nested: &pg.JSONTableNested{}})).Build())
			},
		},
		Controls: map[string]func() error{
			"AlterBaseTypeQuery": func() error {
				return fails(pg.AlterBaseType(pg.BaseType[int64]{Name: "b"}).SetProperties().Storage(pg.StoragePlain).Build())
			},
			"ViewSetting": func() error {
				return fails(pg.CreateView(pg.View{Name: "v"}).As(pg.Select(zID).From(zt)).WithOptions(pg.SecurityInvokerSetting(true)).Build())
			},
			"AlterRangeTypeQuery": func() error {
				return fails(pg.AlterRangeType(pg.RangeType[int64]{Name: "r"}).RenameTo(pg.RangeType[int64]{Name: "r2"}).Build())
			},
			"AlterIndexQuery": func() error {
				return fails(pg.AlterIndex(pg.Index{Name: "ix"}).ResetStorage("fillfactor").Build())
			},
			"AlterMaterializedViewQuery": func() error {
				return fails(pg.AlterMaterializedView(pg.MaterializedView{Name: "m"}).RenameColumn("a", "b").Build())
			},
			"RangeType": func() error {
				return fails(pg.CreateRangeType(pg.RangeType[int64]{Name: "r"}, ddl.RawType[int64]("bigint")).Build())
			},
			"BaseType": func() error { return fails(pg.CreateShellType(pg.BaseType[int64]{Name: "b"}).Build()) },
			"Role": func() error {
				return fails(pg.SetOwner(pg.View{Name: "v"}, pg.Role{Name: "r"}).Build())
			},
			"Rule": func() error {
				return fails(pg.AlterTable(zt).Clause(pg.EnableRule(pg.Rule{Name: "r", Table: zt})).Build())
			},
			"StorageParameter": func() error {
				return fails(pg.AlterIndex(pg.Index{Name: "ix"}).SetStorage(pg.IntParameter("fillfactor", 70)).Build())
			},
			"TypeRef": func() error {
				return fails(pg.CreateBaseType(pg.BaseType[int64]{Name: "b"}, pg.Function{Name: "i"}, pg.Function{Name: "o"}).Like(pg.NamedType(ddl.RawType[int64]("bigint"))).Build())
			},
			"FunctionArg": func() error {
				f := pg.Function{Name: "f", Params: []pg.FunctionArg{pg.Arg("a", ddl.RawType[int64]("bigint"))}}
				return fails(pg.CreateFunction(f, pg.LangSQL, "SELECT 1").Returns(pg.Returns(ddl.RawType[int64]("bigint"))).Build())
			},
			"FunctionResult": func() error {
				return fails(pg.CreateFunction(pg.Function{Name: "f"}, pg.LangSQL, "SELECT 1").Returns(pg.Returns(ddl.RawType[int64]("bigint"))).Build())
			},
			"IdentitySequenceClause": func() error {
				return fails(pg.AlterTable(zt).Clause(pg.AlterIdentitySequence(zID).IncrementBy(2)).Build())
			},
			"PeriodColumn": func() error {
				return fails(pg.CreateTable(zt).Column(ddl.Column(zID), ddl.Column(zValid)).Clause(pg.PrimaryKey(zID).WithoutOverlaps(pg.Period(zValid))).Build())
			},
			"Composite":           func() error { return fails(pg.CreateComposite(zComposite, zAttr).Build()) },
			"Attribute":           func() error { return fails(pg.CreateComposite(zComposite, zAttr).Build()) },
			"AlterCompositeQuery": func() error { return fails(pg.AlterComposite(zComposite).AddAttribute(zAttr).Build()) },
			"DomainConstraint": func() error {
				return fails(pg.AlterDomain(pg.Domain[string]{Name: "d"}).DropConstraint(pg.DomainConstraint[string]{Domain: pg.Domain[string]{Name: "d"}, Name: "c"}).Build())
			},
			"Schema":    func() error { return fails(pg.CreateSchema(pg.Schema{Name: "s"}).Build()) },
			"Extension": func() error { return fails(pg.CreateExtension(pg.Extension{Name: "e"}).Build()) },
			"Enum":      func() error { return fails(pg.CreateEnum(pg.Enum[string]{Name: "e"}, "a").Build()) },
			"Index":     func() error { return fails(pg.CreateIndex(pg.Index{Name: "i"}, zt).On(pg.IndexKey(zID)).Build()) },
			"Policy":    func() error { return fails(pg.CreatePolicy(pg.Policy{Name: "p", Table: zt}).Build()) },
			"Sequence":  func() error { return fails(pg.CreateSequence(pg.Sequence[int64]{Name: "s"}).Build()) },
			"Domain": func() error {
				return fails(pg.CreateDomain(pg.Domain[string]{Name: "d"}, ddl.RawType[string]("text")).Build())
			},
			"Collation":  func() error { return fails(pg.CreateCollation(pg.Collation{Name: "c"}, "und-x-icu").Build()) },
			"Statistics": func() error { return fails(pg.CreateStatistics(pg.Statistics{Name: "s"}, zt, zID, zName).Build()) },
			"Function": func() error {
				return fails(pg.CreateTriggerFunction(pg.Function{Name: "f"}, "BEGIN RETURN NEW; END").Build())
			},
			"Trigger": func() error { return fails(pg.DropTrigger(pg.Trigger{Name: "t", Table: zt}).Build()) },
			"View": func() error {
				return fails(pg.CreateView(pg.View{Name: "v"}).As(pg.Select().Columns(sqlb.Bind(zID, new(int64))).From(zt)).Build())
			},
			"MaterializedView": func() error { return fails(pg.RefreshMaterializedView(pg.MaterializedView{Name: "m"}).Build()) },
			"AlterDomainQuery": func() error {
				return fails(pg.AlterDomain(pg.Domain[string]{Name: "d"}).DropConstraint(pg.DomainConstraint[string]{Domain: pg.Domain[string]{Name: "d"}, Name: "c"}).Build())
			},
			"AlterEnumQuery": func() error { return fails(pg.AlterEnum(pg.Enum[string]{Name: "e"}).AddValue("a").Build()) },
			"AlterPolicyQuery": func() error {
				return fails(pg.AlterPolicy(pg.Policy{Name: "p", Table: zt}).Using(sqlb.Eq(zID, int64(1))).Build())
			},
			"AlterSequenceQuery": func() error { return fails(pg.AlterSequence(pg.Sequence[int64]{Name: "s"}).Restart(1).Build()) },
			"CommentQuery":       func() error { return fails(pg.CommentOnTable(zt).Is("x").Build()) },
			"CreateIndexQuery":   func() error { return fails(pg.CreateIndex(pg.Index{Name: "i"}, zt).On(pg.IndexKey(zID)).Build()) },
			"CreateMaterializedViewQuery": func() error {
				return fails(pg.CreateMaterializedView(pg.MaterializedView{Name: "m"}).As(pg.Select().Columns(sqlb.Bind(zID, new(int64))).From(zt)).Build())
			},
			"CreatePartitionQuery": func() error {
				return fails(pg.CreatePartition(sqlb.Table{Name: "z1"}, zt).For(pg.DefaultPartition()).Build())
			},
			"CreateTriggerQuery": func() error {
				return fails(pg.CreateTrigger(pg.Trigger{Name: "t", Table: zt}).After(pg.OnInsert()).Execute(pg.Function{Name: "f"}).Build())
			},
			"TimedTriggerQuery": func() error {
				return fails(pg.CreateTrigger(pg.Trigger{Name: "t", Table: zt}).After(pg.OnInsert()).Execute(pg.Function{Name: "f"}).Build())
			},
			"CreateViewQuery": func() error {
				return fails(pg.CreateView(pg.View{Name: "v"}).As(pg.Select().Columns(sqlb.Bind(zID, new(int64))).From(zt)).Build())
			},
			"WhenBuilder": func() error {
				return failsQ(pg.NewMerge(pg.Dialect{}).Into(zt).Using(zt2, sqlb.EqExpr(zID, z2ID)).WhenMatched().ThenDelete().Build())
			},
			"ColumnPair": func() error {
				return fails(pg.CreateTable(zt).Column(ddl.Column(zID)).Clause(pg.ForeignKey(pg.TableConstraint{Table: zID.Table(), Name: "fk"}, pg.RefPair(zID, z2ID))).Build())
			},
			"ExcludeElement": func() error {
				return fails(pg.CreateTable(zt).Column(ddl.Column(zID)).Clause(pg.Exclude(pg.TableConstraint{Table: zID.Table(), Name: "x"}, pg.GiST, pg.ExcludeOn(zID, pg.OpEquals))).Build())
			},
			"IndexParam": func() error {
				return fails(pg.CreateIndex(pg.Index{Name: "i"}, zt).On(pg.IndexKey(zID)).With(pg.NewIndexParam("fillfactor", 70)).Build())
			},
			"OpClass": func() error {
				return fails(pg.CreateIndex(pg.Index{Name: "i"}, zt).On(pg.IndexKey(zID).Opclass(pg.NewOpClass[int64]("int8_ops"))).Build())
			},
			"TriggerEvent": func() error {
				return fails(pg.CreateTrigger(pg.Trigger{Name: "t", Table: zt}).After(pg.OnInsert()).Execute(pg.Function{Name: "f"}).Build())
			},
			"Conflict": func() error {
				return failsQ(pg.Insert(zt).Row(sqlb.Set(zID, int64(1))).Clause(pg.OnConflict(zID).DoUpdate(sqlb.Set(zName, "a")).Clause()).Build())
			},
			"ColumnDef": func() error {
				return failsQ(pg.Select().Columns(sqlb.Bind(zID, new(int64))).From(pg.RecordSet(sqlb.Raw(func(*sqlb.Args) string { return "f()" }), "r", pg.ColumnDef{Name: "a", Type: "int"})).Build())
			},
			"JSONPair": func() error {
				return failsQ(pg.Select().Columns(sqlb.Bind(pg.JSONObject([]pg.JSONPair{pg.Pair("k", zName)}), new(json.RawMessage))).Build())
			},
			"JSONTableColumn": func() error {
				return failsQ(pg.Select().Columns(sqlb.Bind(zID, new(int64))).From(pg.JSONTable(zName, "$", "j", pg.JSONTableColumn{Name: "a", Type: "int", Path: "$.a"})).Build())
			},
			"JSONTableNested": func() error {
				return failsQ(pg.Select().Columns(sqlb.Bind(zID, new(int64))).From(pg.JSONTable(zName, "$", "j",
					pg.JSONTableColumn{Nested: &pg.JSONTableNested{Path: "$.x", Columns: []pg.JSONTableColumn{{Name: "a", Type: "int", Path: "$.a"}}}})).Build())
			},
		},
		Exempt: map[string]string{
			"JSON":        "a scan and value wrapper; its zero value is a valid empty document and renders nothing",
			"DomainCheck": "no function or method takes one, so a literal cannot reach a statement",
		},
	})
}

var (
	zt    = sqlb.Table{Name: "z", Alias: "z"}
	zID   = sqlb.Col[int64](zt, "id")
	zName = sqlb.Col[string](zt, "name")
	zt2   = sqlb.Table{Name: "z2", Alias: "y"}
	// zValid is a range column, a temporal key's period.
	zValid = sqlb.Col[pg.Range[time.Time]](zt, "valid")
	// zComposite and zAttr are a composite type and one attribute of it.
	zComposite = pg.Composite[string]{Name: "money"}
	zAttr      = pg.Attr("amount", ddl.RawType[string]("numeric"))
	z2ID       = sqlb.Col[int64](zt2, "id")
)

// fails turns a DDL Build's result into the probe's error.
func fails(_ ddl.Script, err error) error { return err }

// failsQ turns a query Build's result into the probe's error.
func failsQ(_ sqlb.Query, err error) error { return err }
