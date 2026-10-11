package pg_test

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/mysql"
	"github.com/ubgo/sqlb/pg"
	"github.com/ubgo/sqlb/sqlite"
)

// builder is one engine statement: something with Build.
type builder interface {
	Build() (sqlb.Query, error)
}

var (
	vsT     = sqlb.Table{Name: "t", Alias: "t"}
	vsU     = sqlb.Table{Name: "u", Alias: "u"}
	vsA     = sqlb.Col[int64](vsT, "a")
	vsB     = sqlb.Col[int64](vsU, "b")
	vsBound int64
)

// engineBuilders is every method, on every statement builder an engine package
// defines, that returns a derived statement. The name is the builder's type
// name, which TestEngineBuilders_AreAllCovered checks against the source.
var engineBuilders = map[string][]struct {
	method string
	base   func() builder
	add    func(builder) builder
}{
	"Truncate": {
		{"Table", func() builder { return pg.NewTruncate(pg.Dialect{}, vsT) }, func(b builder) builder { return b.(*pg.Truncate).Table(vsU) }},
		{"Only", func() builder { return pg.NewTruncate(pg.Dialect{}, vsT) }, func(b builder) builder { return b.(*pg.Truncate).Only() }},
		{"RestartIdentity", func() builder { return pg.NewTruncate(pg.Dialect{}, vsT) }, func(b builder) builder { return b.(*pg.Truncate).RestartIdentity() }},
		{"ContinueIdentity", func() builder { return pg.NewTruncate(pg.Dialect{}, vsT) }, func(b builder) builder { return b.(*pg.Truncate).ContinueIdentity() }},
		{"Cascade", func() builder { return pg.NewTruncate(pg.Dialect{}, vsT) }, func(b builder) builder { return b.(*pg.Truncate).Cascade() }},
		{"Restrict", func() builder { return pg.NewTruncate(pg.Dialect{}, vsT) }, func(b builder) builder { return b.(*pg.Truncate).Restrict() }},
	},
	"LockTable": {
		{"Table", func() builder { return pg.NewLockTable(pg.Dialect{}, vsT) }, func(b builder) builder { return b.(*pg.LockTable).Table(vsU) }},
		{"In", func() builder { return pg.NewLockTable(pg.Dialect{}, vsT) }, func(b builder) builder { return b.(*pg.LockTable).In(pg.LockShare) }},
		{"NoWait", func() builder { return pg.NewLockTable(pg.Dialect{}, vsT) }, func(b builder) builder { return b.(*pg.LockTable).NoWait() }},
	},
	"Merge": {
		{"Into", mergeBase, func(b builder) builder { return b.(*pg.Merge).Into(vsU) }},
		{"Using", mergeBase, func(b builder) builder { return b.(*pg.Merge).Using(vsU, sqlb.Eq(vsB, 5)) }},
		{"WhenMatched.ThenUpdate", mergeBase, func(b builder) builder {
			return b.(*pg.Merge).WhenMatched().ThenUpdate(sqlb.Set(vsA, 2))
		}},
		{"WhenNotMatched.ThenInsert", mergeBase, func(b builder) builder {
			return b.(*pg.Merge).WhenNotMatched().ThenInsert(sqlb.Set(vsA, 3))
		}},
		{"WhenMatched.ThenDelete", mergeBase, func(b builder) builder { return b.(*pg.Merge).WhenMatched().ThenDelete() }},
		{"WhenNotMatchedBySource.ThenDoNothing", mergeBase, func(b builder) builder {
			return b.(*pg.Merge).WhenNotMatchedBySource().ThenDoNothing()
		}},
		{"Returning", mergeBase, func(b builder) builder { return b.(*pg.Merge).Returning(sqlb.Bind(vsA, &vsBound)) }},
	},
}

// mergeBase is a complete MERGE, so every derivation of it builds.
func mergeBase() builder {
	return pg.NewMerge(pg.Dialect{}).Into(vsT).Using(vsU, sqlb.EqExpr(vsA, vsB)).
		WhenMatched().ThenUpdate(sqlb.Set(vsA, 1))
}

// TestEngineBuilders_MethodsDoNotWriteToTheirReceiver is
// TestBuilders_MethodsDoNotWriteToTheirReceiver for the statement kinds the
// engine packages define themselves.
//
// The core's builders stopped writing to their receiver in 2026-09, and the
// test that pins it lives in stmt, which cannot import pg. So pg.Truncate,
// pg.LockTable and pg.Merge kept mutating while "every builder method returns
// a derived statement" was true everywhere a test looked. The WHEN branches of
// a MERGE were the least visible case: they appended to the
// Merge they started from through a pointer the WhenBuilder held.
func TestEngineBuilders_MethodsDoNotWriteToTheirReceiver(t *testing.T) {
	for typ, cases := range engineBuilders {
		for _, tc := range cases {
			t.Run(typ+"."+tc.method, func(t *testing.T) {
				base := tc.base()
				before, err := base.Build()
				if err != nil {
					t.Fatal(err)
				}
				derived := tc.add(base)
				if derived == base {
					t.Error("returned the receiver, so the statement is shared")
				}
				after, err := base.Build()
				if err != nil {
					t.Fatal(err)
				}
				if after.SQL != before.SQL {
					t.Errorf("the method wrote to its receiver:\nbefore %s\nafter  %s", before.SQL, after.SQL)
				}
				got, err := derived.Build()
				if err != nil {
					t.Fatal(err)
				}
				if got.SQL == before.SQL {
					t.Errorf("the derived statement did not change: %s", got.SQL)
				}
			})
		}
	}
}

// TestMerge_DerivedStatementsShareTheGuard pins that Merge follows the core's
// other half of the rule: derived statements share their scan targets, so they
// share one execution guard, and a Query from each cannot run at once.
func TestMerge_DerivedStatementsShareTheGuard(t *testing.T) {
	base := mergeBase().(*pg.Merge).Returning(sqlb.Bind(vsA, &vsBound))
	derived := base.WhenNotMatched().ThenInsert(sqlb.Set(vsA, 9))

	q1, err := base.Build()
	if err != nil {
		t.Fatal(err)
	}
	q2, err := derived.Build()
	if err != nil {
		t.Fatal(err)
	}
	if !q1.Acquire() {
		t.Fatal("the first execution must be allowed")
	}
	defer q1.Release()
	if q2.Acquire() {
		q2.Release()
		t.Fatal("a Query from a derived MERGE ran alongside its base, which shares its targets")
	}
}

// buildMethod finds every type with a Build method in an engine package.
// A generic type's receiver is *Name[T], which the first version of this
// pattern did not match, so every generic builder escaped the gate.
var buildMethod = regexp.MustCompile(`(?m)^func \(\w+ \*(\w+)(?:\[\w+\])?\) Build\(\)`)

// TestEngineBuilders_AreAllCovered fails when an engine package gains a
// statement builder that engineBuilders does not walk. The earlier test could
// only protect the builders somebody remembered to list, which is exactly how
// three of them were missed. It fails closed on an empty scan.
func TestEngineBuilders_AreAllCovered(t *testing.T) {
	// Keys are package-qualified for the packages other than pg: sqlite has a
	// CreateIndexReady and a CreateViewStatement too, and a bare name would let
	// pg's entry cover them without either ever being walked.
	found := map[string]string{}
	prefix := map[string]string{".": "", "../sqlite": "sqlite.", "../ch": "ch.", "../mysql": "mysql."}
	for _, dir := range []string{".", "../sqlite", "../ch", "../mysql"} {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			for _, m := range buildMethod.FindAllStringSubmatch(string(src), -1) {
				found[prefix[dir]+m[1]] = f
			}
		}
	}
	if len(found) == 0 {
		t.Fatal(errors.New("found no statement builder in any engine package; the matcher is broken"))
	}
	var missing []string
	for typ, file := range found {
		_, query := engineBuilders[typ]
		_, script := ddlBuilders[typ]
		if !query && !script {
			missing = append(missing, typ+" ("+file+")")
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("statement builders with no receiver-write coverage: %v", missing)
	}
}

// scriptBuilder is an engine DDL statement: Build returns a Script.
type scriptBuilder interface {
	Build() (ddl.Script, error)
}

type vsStatus string

var (
	vsSchema = pg.Schema{Name: "s"}
	vsExt    = pg.Extension{Name: "e"}
	vsEnum   = pg.Enum[vsStatus]{Name: "st"}
	vsIndex  = pg.Index{Name: "ix"}
	vsIndex2 = pg.Index{Name: "ix2"}
)

var (
	vsSequence = pg.Sequence[int64]{Name: "seq"}
	vsView     = pg.View{Name: "v"}
	vsMat      = pg.MaterializedView{Name: "m"}
)

var (
	vsFn      = pg.Function{Name: "f"}
	vsTrigger = pg.Trigger{Name: "tr", Table: vsT}
)

var (
	vsPolicy = pg.Policy{Name: "p", Table: vsT}
	vsDomain = pg.Domain[int64]{Name: "d"}
	vsVsC    = sqlb.Col[int64](vsT, "c")
)

func vsPol() scriptBuilder { return pg.CreatePolicy(vsPolicy) }
func vsDom() scriptBuilder { return pg.CreateDomain(vsDomain, ddl.RawType[int64]("bigint")) }
func vsColl() scriptBuilder {
	return pg.CreateCollation(pg.Collation{Name: "c"}, "und-u-ks-level2")
}
func vsStats() scriptBuilder {
	return pg.CreateStatistics(pg.Statistics{Name: "s"}, vsT, vsA, vsVsC)
}

var (
	vsNewT = sqlb.Table{Name: "t_new"}
	vsNewA = sqlb.Col[int64](vsNewT, "a")
	vsNewB = sqlb.Col[int64](vsNewT, "b")
)

func vsFTS() scriptBuilder {
	return sqlite.CreateFTS5(vsT).Columns(sqlb.Col[string](vsT, "body"))
}
func vsRebuild() scriptBuilder {
	return sqlite.Rebuild(vsT).Into(sqlite.CreateTable(vsNewT).Column(ddl.Column(vsNewA), ddl.Column(vsNewB).Nullable())).Copy(sqlite.CopyAs(vsNewA, vsA))
}

func vsMyView() scriptBuilder {
	return mysql.CreateView(mysql.View{Name: "v"}).As(mysql.Select(vsA).From(vsT))
}

// The MySQL statement kinds added on 2026-09-24. Each base is a complete
// statement, so every derivation of one builds.
func vsMyAlterView() scriptBuilder {
	return mysql.AlterView(mysql.View{Name: "v"}).As(mysql.Select(vsA).From(vsT))
}

// vsMyAlterEvent starts from a change MySQL accepts on its own, because an
// ALTER EVENT naming nothing to change is not a statement.
func vsMyAlterEvent() scriptBuilder {
	return mysql.AlterEvent(mysql.Event{Name: "e"}).Status(mysql.EventEnable)
}

// vsMyAlterRoutine starts from a characteristic MySQL's ALTER accepts, which
// the derivation below does not set again.
func vsMyAlterRoutine() scriptBuilder {
	return mysql.AlterProcedure(mysql.Procedure{Name: "p"}).Characteristic(mysql.NoSQL)
}

// vsMyEventStep is the body statement an event and a trigger both take.
func vsMyEventStep() mysql.TriggerStep { return mysql.StepDelete(mysql.Delete(vsT).All()) }

// The MySQL statement kinds added on 2026-09-24. Each base is a complete
// statement, so every derivation of one builds.
func vsMyIndex() scriptBuilder {
	return mysql.CreateIndex(mysql.Index{Name: "i"}, vsT).On(mysql.IndexKey(vsA))
}

func vsMyDropIndex() scriptBuilder { return mysql.DropIndex(mysql.Index{Name: "i"}, vsT) }

func vsMyRename() scriptBuilder { return mysql.RenameTable(vsT, vsU) }

func vsMyLike() scriptBuilder { return mysql.CreateTableLike(vsU, vsT) }

func vsMyAs() scriptBuilder { return mysql.CreateTableAs(vsU, mysql.Select(vsA).From(vsT)) }

func vsMyDropTemp() scriptBuilder { return mysql.DropTemporaryTable(vsU) }

// vsLiteTableAs is a complete SQLite CREATE TABLE ... AS. It is vsTableAs
// upstream, renamed here because this file already uses that name for the
// Postgres statement of the same shape.
func vsLiteTableAs() scriptBuilder {
	return sqlite.CreateTableAs(vsNewT, sqlite.Select(vsA).From(vsT))
}

// vsVirt is a SQLite virtual table, the module form that takes a column.
func vsVirt() scriptBuilder { return sqlite.CreateGeopoly(vsT, vsA) }

func vsLiteIndex() scriptBuilder {
	return sqlite.CreateIndex(sqlite.Index{Name: "i"}, vsT).On(sqlite.IndexKey(vsA))
}

func vsPartition() scriptBuilder { return pg.CreatePartition(vsU, vsT).For(pg.DefaultPartition()) }

func vsSeq() scriptBuilder       { return pg.CreateSequence(vsSequence) }
func vsViewReady() scriptBuilder { return pg.CreateView(vsView).As(pg.Select(vsA).From(vsT)) }
func vsMatReady() scriptBuilder {
	return pg.CreateMaterializedView(vsMat).As(pg.Select(vsA).From(vsT))
}

func vsAlterSeq() scriptBuilder { return pg.AlterSequence(vsSequence).IncrementBy(1) }

// vsViewOpts is a complete ALTER VIEW ... SET, so every derivation of it
// builds; the option it starts with is one the derivations do not set again.
func vsViewOpts() scriptBuilder { return pg.AlterViewOptions(vsView).SecurityInvoker(false) }

// vsRange, vsBase and vsRoutine are complete statements of the three kinds
// added on 2026-09-24, so every derivation of one builds.
func vsRange() scriptBuilder {
	return pg.CreateRangeType(pg.RangeType[int64]{Name: "r"}, ddl.RawType[int64]("bigint"))
}

func vsBase() scriptBuilder {
	return pg.CreateBaseType(pg.BaseType[int64]{Name: "b"}, pg.Function{Name: "b_in"}, pg.Function{Name: "b_out"})
}

// vsTableAs is a complete CREATE TABLE ... AS, so every derivation builds.
func vsTableAs() scriptBuilder { return pg.CreateTableAs(vsU, pg.Select(vsA).From(vsT)) }

// vsAlterBase is an ALTER TYPE ... SET on a base type carrying one property,
// which the derivations below do not set again.
func vsAlterBase() scriptBuilder {
	return pg.AlterBaseType(pg.BaseType[int64]{Name: "b"}).SetProperties().Storage(pg.StoragePlain)
}

func vsRoutine() scriptBuilder {
	return pg.CreateFunction(vsFn, pg.LangSQL, "SELECT 1").Returns(pg.Returns(ddl.RawType[int64]("bigint")))
}

func vsIndexReady() scriptBuilder { return pg.CreateIndex(vsIndex, vsT).On(pg.IndexKey(vsA)) }

// ddlBuilders is the same walk for the engine packages' DDL statements. A type
// with no method that derives, CreateEnumStatement say, is listed with none,
// so the coverage check still sees it.
var ddlBuilders = map[string][]struct {
	method string
	base   func() scriptBuilder
	add    func(scriptBuilder) scriptBuilder
}{
	"CreateSchemaStatement": {
		{"IfNotExists", func() scriptBuilder { return pg.CreateSchema(vsSchema) }, func(b scriptBuilder) scriptBuilder { return b.(*pg.CreateSchemaStatement).IfNotExists() }},
	},
	"DropSchemaStatement": {
		{"IfExists", func() scriptBuilder { return pg.DropSchema(vsSchema) }, func(b scriptBuilder) scriptBuilder { return b.(*pg.DropSchemaStatement).IfExists() }},
		{"Cascade", func() scriptBuilder { return pg.DropSchema(vsSchema) }, func(b scriptBuilder) scriptBuilder { return b.(*pg.DropSchemaStatement).Cascade() }},
	},
	"CreateExtensionStatement": {
		{"IfNotExists", func() scriptBuilder { return pg.CreateExtension(vsExt) }, func(b scriptBuilder) scriptBuilder { return b.(*pg.CreateExtensionStatement).IfNotExists() }},
	},
	"DropExtensionStatement": {
		{"IfExists", func() scriptBuilder { return pg.DropExtension(vsExt) }, func(b scriptBuilder) scriptBuilder { return b.(*pg.DropExtensionStatement).IfExists() }},
		{"Cascade", func() scriptBuilder { return pg.DropExtension(vsExt) }, func(b scriptBuilder) scriptBuilder { return b.(*pg.DropExtensionStatement).Cascade() }},
	},
	"CreateEnumStatement": nil,
	"AddValueStatement": {
		{"Before", func() scriptBuilder { return pg.AlterEnum(vsEnum).AddValue("b") }, func(b scriptBuilder) scriptBuilder { return b.(*pg.AddValueStatement[vsStatus]).Before("a") }},
		{"After", func() scriptBuilder { return pg.AlterEnum(vsEnum).AddValue("b") }, func(b scriptBuilder) scriptBuilder { return b.(*pg.AddValueStatement[vsStatus]).After("a") }},
		{"IfNotExists", func() scriptBuilder { return pg.AlterEnum(vsEnum).AddValue("b") }, func(b scriptBuilder) scriptBuilder { return b.(*pg.AddValueStatement[vsStatus]).IfNotExists() }},
	},
	"AlterEnumStatement": nil,
	"DropTypeStatement": {
		{"IfExists", func() scriptBuilder { return pg.DropType(vsEnum) }, func(b scriptBuilder) scriptBuilder { return b.(*pg.DropTypeStatement).IfExists() }},
		{"Cascade", func() scriptBuilder { return pg.DropType(vsEnum) }, func(b scriptBuilder) scriptBuilder { return b.(*pg.DropTypeStatement).Cascade() }},
	},
	"GuardedStatement":    nil,
	"ConstraintStatement": nil,
	"CreateIndexReady": {
		{"Unique", vsIndexReady, func(b scriptBuilder) scriptBuilder { return b.(*pg.CreateIndexReady).Unique() }},
		{"IfNotExists", vsIndexReady, func(b scriptBuilder) scriptBuilder { return b.(*pg.CreateIndexReady).IfNotExists() }},
		{"Concurrently", vsIndexReady, func(b scriptBuilder) scriptBuilder { return b.(*pg.CreateIndexReady).Concurrently() }},
		{"Using", vsIndexReady, func(b scriptBuilder) scriptBuilder { return b.(*pg.CreateIndexReady).Using(pg.GIN) }},
		{"Include", vsIndexReady, func(b scriptBuilder) scriptBuilder { return b.(*pg.CreateIndexReady).Include(vsA) }},
		{"With", vsIndexReady, func(b scriptBuilder) scriptBuilder {
			return b.(*pg.CreateIndexReady).With(pg.NewIndexParam("fillfactor", 70))
		}},
		{"Where", vsIndexReady, func(b scriptBuilder) scriptBuilder { return b.(*pg.CreateIndexReady).Where(sqlb.Eq(vsA, 1)) }},
		{"NullsNotDistinct", vsIndexReady, func(b scriptBuilder) scriptBuilder { return b.(*pg.CreateIndexReady).NullsNotDistinct() }},
	},
	"RenameIndexStatement": {
		{"IfExists", func() scriptBuilder { return pg.RenameIndex(vsIndex, vsIndex2) }, func(b scriptBuilder) scriptBuilder { return b.(*pg.RenameIndexStatement).IfExists() }},
	},
	"DropIndexStatement": {
		{"IfExists", func() scriptBuilder { return pg.DropIndex(vsIndex) }, func(b scriptBuilder) scriptBuilder { return b.(*pg.DropIndexStatement).IfExists() }},
		{"Concurrently", func() scriptBuilder { return pg.DropIndex(vsIndex) }, func(b scriptBuilder) scriptBuilder { return b.(*pg.DropIndexStatement).Concurrently() }},
		{"Cascade", func() scriptBuilder { return pg.DropIndex(vsIndex) }, func(b scriptBuilder) scriptBuilder { return b.(*pg.DropIndexStatement).Cascade() }},
	},
	"CommentStatement":      nil,
	"RenameSchemaStatement": nil,
	"CreateSequenceStatement": {
		{"IfNotExists", vsSeq, func(b scriptBuilder) scriptBuilder { return b.(*pg.CreateSequenceStatement[int64]).IfNotExists() }},
		{"IncrementBy", vsSeq, func(b scriptBuilder) scriptBuilder { return b.(*pg.CreateSequenceStatement[int64]).IncrementBy(2) }},
		{"MinValue", vsSeq, func(b scriptBuilder) scriptBuilder { return b.(*pg.CreateSequenceStatement[int64]).MinValue(1) }},
		{"MaxValue", vsSeq, func(b scriptBuilder) scriptBuilder { return b.(*pg.CreateSequenceStatement[int64]).MaxValue(9) }},
		{"Start", vsSeq, func(b scriptBuilder) scriptBuilder { return b.(*pg.CreateSequenceStatement[int64]).Start(5) }},
		{"Cache", vsSeq, func(b scriptBuilder) scriptBuilder { return b.(*pg.CreateSequenceStatement[int64]).Cache(20) }},
	},
	"AlterSequenceStatement": {
		{"IfExists", func() scriptBuilder { return pg.AlterSequence(vsSequence).Restart(1) }, func(b scriptBuilder) scriptBuilder { return b.(*pg.AlterSequenceStatement[int64]).IfExists() }},
		{"IncrementBy", vsAlterSeq, func(b scriptBuilder) scriptBuilder { return b.(*pg.AlterSequenceStatement[int64]).IncrementBy(2) }},
		{"MinValue", vsAlterSeq, func(b scriptBuilder) scriptBuilder { return b.(*pg.AlterSequenceStatement[int64]).MinValue(1) }},
		{"MaxValue", vsAlterSeq, func(b scriptBuilder) scriptBuilder { return b.(*pg.AlterSequenceStatement[int64]).MaxValue(9) }},
		{"Cache", vsAlterSeq, func(b scriptBuilder) scriptBuilder { return b.(*pg.AlterSequenceStatement[int64]).Cache(5) }},
		{"Cycle", vsAlterSeq, func(b scriptBuilder) scriptBuilder { return b.(*pg.AlterSequenceStatement[int64]).Cycle() }},
		{"NoCycle", vsAlterSeq, func(b scriptBuilder) scriptBuilder { return b.(*pg.AlterSequenceStatement[int64]).NoCycle() }},
		{"Restart", vsAlterSeq, func(b scriptBuilder) scriptBuilder { return b.(*pg.AlterSequenceStatement[int64]).Restart(3) }},
		{"OwnedByNone", vsAlterSeq, func(b scriptBuilder) scriptBuilder { return b.(*pg.AlterSequenceStatement[int64]).OwnedByNone() }},
		{"SetType", vsAlterSeq, func(b scriptBuilder) scriptBuilder { return b.(*pg.AlterSequenceStatement[int64]).SetType() }},
		{"StartWith", vsAlterSeq, func(b scriptBuilder) scriptBuilder { return b.(*pg.AlterSequenceStatement[int64]).StartWith(7) }},
		{"SetLogged", vsAlterSeq, func(b scriptBuilder) scriptBuilder { return b.(*pg.AlterSequenceStatement[int64]).SetLogged() }},
		{"SetUnlogged", vsAlterSeq, func(b scriptBuilder) scriptBuilder { return b.(*pg.AlterSequenceStatement[int64]).SetUnlogged() }},
	},
	"SetSchemaStatement": nil,
	"AlterViewOptionsStatement": {
		{"CheckOption", vsViewOpts, func(b scriptBuilder) scriptBuilder { return b.(*pg.AlterViewOptionsStatement).CheckOption(pg.Cascaded) }},
		{"SecurityBarrier", vsViewOpts, func(b scriptBuilder) scriptBuilder { return b.(*pg.AlterViewOptionsStatement).SecurityBarrier(true) }},
		{"SecurityInvoker", vsViewOpts, func(b scriptBuilder) scriptBuilder { return b.(*pg.AlterViewOptionsStatement).SecurityInvoker(true) }},
		{"Reset", vsViewOpts, func(b scriptBuilder) scriptBuilder {
			return b.(*pg.AlterViewOptionsStatement).Reset(pg.ViewSecurityBarrier)
		}},
	},
	"AlterViewStatement":       nil,
	"CreateCompositeStatement": nil,
	"AlterCompositeStatement": {
		{"Cascade", func() scriptBuilder { return pg.AlterComposite(pg.Composite[vsStatus]{Name: "row"}).DropAttribute("a") }, func(b scriptBuilder) scriptBuilder {
			return b.(*pg.AlterCompositeStatement[vsStatus]).Cascade()
		}},
	},
	"DropObjectStatement": {
		{"IfExists", func() scriptBuilder { return pg.DropView(vsView) }, func(b scriptBuilder) scriptBuilder { return b.(*pg.DropObjectStatement).IfExists() }},
		{"Cascade", func() scriptBuilder { return pg.DropSequence(vsSequence) }, func(b scriptBuilder) scriptBuilder { return b.(*pg.DropObjectStatement).Cascade() }},
	},
	"CreateViewStatement": {
		{"OrReplace", vsViewReady, func(b scriptBuilder) scriptBuilder { return b.(*pg.CreateViewStatement).OrReplace() }},
		{"WithCheckOption", vsViewReady, func(b scriptBuilder) scriptBuilder { return b.(*pg.CreateViewStatement).WithCheckOption(pg.Local) }},
	},
	"RenameViewStatement": {
		{"IfExists", func() scriptBuilder { return pg.RenameView(vsView, pg.View{Name: "w"}) }, func(b scriptBuilder) scriptBuilder { return b.(*pg.RenameViewStatement).IfExists() }},
	},
	"CreateMaterializedViewStatement": {
		{"IfNotExists", vsMatReady, func(b scriptBuilder) scriptBuilder { return b.(*pg.CreateMaterializedViewStatement).IfNotExists() }},
		{"WithNoData", vsMatReady, func(b scriptBuilder) scriptBuilder { return b.(*pg.CreateMaterializedViewStatement).WithNoData() }},
	},
	"RefreshStatement": {
		{"Concurrently", func() scriptBuilder { return pg.RefreshMaterializedView(vsMat) }, func(b scriptBuilder) scriptBuilder { return b.(*pg.RefreshStatement).Concurrently() }},
	},
	"CreateFunctionStatement": {
		{"OrReplace", func() scriptBuilder { return pg.CreateTriggerFunction(vsFn, "BEGIN RETURN NEW; END") }, func(b scriptBuilder) scriptBuilder {
			return b.(*pg.CreateFunctionStatement).OrReplace()
		}},
	},
	"CreateTriggerStatement": nil,
	"RenameTriggerStatement": nil,
	"DropTriggerStatement": {
		{"IfExists", func() scriptBuilder { return pg.DropTrigger(vsTrigger) }, func(b scriptBuilder) scriptBuilder { return b.(*pg.DropTriggerStatement).IfExists() }},
	},
	"CreatePartitionStatement": {
		{"IfNotExists", vsPartition, func(b scriptBuilder) scriptBuilder { return b.(*pg.CreatePartitionStatement).IfNotExists() }},
	},
	"DetachStatement": {
		{"Concurrently", func() scriptBuilder { return pg.DetachPartition(vsT, vsU) }, func(b scriptBuilder) scriptBuilder { return b.(*pg.DetachStatement).Concurrently() }},
		{"Finalize", func() scriptBuilder { return pg.DetachPartition(vsT, vsU) }, func(b scriptBuilder) scriptBuilder { return b.(*pg.DetachStatement).Finalize() }},
	},
	"CreatePolicyStatement": {
		{"Permissive", func() scriptBuilder { return pg.CreatePolicy(vsPolicy).Restrictive() }, func(b scriptBuilder) scriptBuilder { return b.(*pg.CreatePolicyStatement).Permissive() }},
		{"Restrictive", vsPol, func(b scriptBuilder) scriptBuilder { return b.(*pg.CreatePolicyStatement).Restrictive() }},
		{"For", vsPol, func(b scriptBuilder) scriptBuilder { return b.(*pg.CreatePolicyStatement).For(pg.SelectCommand) }},
		{"To", vsPol, func(b scriptBuilder) scriptBuilder { return b.(*pg.CreatePolicyStatement).To(pg.RolePublic) }},
		{"Using", vsPol, func(b scriptBuilder) scriptBuilder { return b.(*pg.CreatePolicyStatement).Using(sqlb.Eq(vsA, 1)) }},
		{"WithCheck", vsPol, func(b scriptBuilder) scriptBuilder { return b.(*pg.CreatePolicyStatement).WithCheck(sqlb.Eq(vsA, 1)) }},
	},
	"AlterPolicyStatement": nil,
	"DropPolicyStatement": {
		{"IfExists", func() scriptBuilder { return pg.DropPolicy(vsPolicy) }, func(b scriptBuilder) scriptBuilder { return b.(*pg.DropPolicyStatement).IfExists() }},
	},
	"CreateDomainStatement": {
		{"Check", vsDom, func(b scriptBuilder) scriptBuilder {
			return b.(*pg.CreateDomainStatement[int64]).Check(pg.DomainConstraint[int64]{Domain: vsDomain, Name: "c"}, sqlb.Greater(pg.DomainValue[int64](), 0))
		}},
		{"NotNull", vsDom, func(b scriptBuilder) scriptBuilder { return b.(*pg.CreateDomainStatement[int64]).NotNull() }},
	},
	"AlterDomainStatement": {
		{"NotValid", func() scriptBuilder {
			return pg.AlterDomain(vsDomain).AddConstraint(pg.DomainConstraint[int64]{Domain: vsDomain, Name: "c"}, sqlb.Greater(pg.DomainValue[int64](), 0))
		}, func(b scriptBuilder) scriptBuilder { return b.(*pg.AlterDomainStatement[int64]).NotValid() }},
	},
	"CreateCollationStatement": {
		{"ICU", vsColl, func(b scriptBuilder) scriptBuilder { return b.(*pg.CreateCollationStatement).ICU() }},
		{"Nondeterministic", vsColl, func(b scriptBuilder) scriptBuilder { return b.(*pg.CreateCollationStatement).Nondeterministic() }},
		{"IfNotExists", vsColl, func(b scriptBuilder) scriptBuilder { return b.(*pg.CreateCollationStatement).IfNotExists() }},
	},
	"CreateStatisticsStatement": {
		{"Kinds", vsStats, func(b scriptBuilder) scriptBuilder { return b.(*pg.CreateStatisticsStatement).Kinds(pg.MCV) }},
		{"IfNotExists", vsStats, func(b scriptBuilder) scriptBuilder { return b.(*pg.CreateStatisticsStatement).IfNotExists() }},
	},
	"AlterExtensionStatement": {
		{"To", func() scriptBuilder { return pg.UpdateExtension(vsExt) }, func(b scriptBuilder) scriptBuilder { return b.(*pg.AlterExtensionStatement).To("0.8.0") }},
	},
	"RenameMaterializedViewStatement": {
		{"IfExists", func() scriptBuilder { return pg.RenameMaterializedView(vsMat, pg.MaterializedView{Name: "n"}) }, func(b scriptBuilder) scriptBuilder {
			return b.(*pg.RenameMaterializedViewStatement).IfExists()
		}},
	},
	"AlterIndexStatement":            nil,
	"AlterMaterializedViewStatement": nil,
	"CreateShellTypeStatement":       nil,
	"ExtensionMemberStatement":       nil,
	"SetExtensionSchemaStatement":    nil,
	"SetOwnerStatement":              nil,
	"RenameViewColumnStatement": {
		{"IfExists", func() scriptBuilder { return pg.RenameViewColumn(vsView, "a", "b") }, func(b scriptBuilder) scriptBuilder {
			return b.(*pg.RenameViewColumnStatement).IfExists()
		}},
	},
	"CreateRangeTypeStatement": {
		{"SubtypeOpClass", vsRange, func(b scriptBuilder) scriptBuilder {
			return b.(*pg.CreateRangeTypeStatement[int64]).SubtypeOpClass(pg.NewOpClass[int64]("int8_ops"))
		}},
		{"Collation", vsRange, func(b scriptBuilder) scriptBuilder {
			return b.(*pg.CreateRangeTypeStatement[int64]).Collation(pg.Collation{Name: "c"})
		}},
		{"Canonical", vsRange, func(b scriptBuilder) scriptBuilder { return b.(*pg.CreateRangeTypeStatement[int64]).Canonical(vsFn) }},
		{"SubtypeDiff", vsRange, func(b scriptBuilder) scriptBuilder { return b.(*pg.CreateRangeTypeStatement[int64]).SubtypeDiff(vsFn) }},
		{"MultirangeTypeName", vsRange, func(b scriptBuilder) scriptBuilder {
			return b.(*pg.CreateRangeTypeStatement[int64]).MultirangeTypeName("mr")
		}},
	},
	"CreateBaseTypeStatement": {
		{"Receive", vsBase, func(b scriptBuilder) scriptBuilder { return b.(*pg.CreateBaseTypeStatement[int64]).Receive(vsFn) }},
		{"Send", vsBase, func(b scriptBuilder) scriptBuilder { return b.(*pg.CreateBaseTypeStatement[int64]).Send(vsFn) }},
		{"TypmodIn", vsBase, func(b scriptBuilder) scriptBuilder { return b.(*pg.CreateBaseTypeStatement[int64]).TypmodIn(vsFn) }},
		{"TypmodOut", vsBase, func(b scriptBuilder) scriptBuilder { return b.(*pg.CreateBaseTypeStatement[int64]).TypmodOut(vsFn) }},
		{"Analyze", vsBase, func(b scriptBuilder) scriptBuilder { return b.(*pg.CreateBaseTypeStatement[int64]).Analyze(vsFn) }},
		{"Subscript", vsBase, func(b scriptBuilder) scriptBuilder { return b.(*pg.CreateBaseTypeStatement[int64]).Subscript(vsFn) }},
		{"InternalLength", vsBase, func(b scriptBuilder) scriptBuilder { return b.(*pg.CreateBaseTypeStatement[int64]).InternalLength(8) }},
		{"VariableLength", vsBase, func(b scriptBuilder) scriptBuilder { return b.(*pg.CreateBaseTypeStatement[int64]).VariableLength() }},
		{"PassedByValue", vsBase, func(b scriptBuilder) scriptBuilder { return b.(*pg.CreateBaseTypeStatement[int64]).PassedByValue() }},
		{"Alignment", vsBase, func(b scriptBuilder) scriptBuilder {
			return b.(*pg.CreateBaseTypeStatement[int64]).Alignment(pg.AlignDouble)
		}},
		{"Storage", vsBase, func(b scriptBuilder) scriptBuilder {
			return b.(*pg.CreateBaseTypeStatement[int64]).Storage(pg.StoragePlain)
		}},
		{"Like", vsBase, func(b scriptBuilder) scriptBuilder {
			return b.(*pg.CreateBaseTypeStatement[int64]).Like(pg.NamedType(ddl.RawType[int64]("bigint")))
		}},
		{"Category", vsBase, func(b scriptBuilder) scriptBuilder {
			return b.(*pg.CreateBaseTypeStatement[int64]).Category(pg.CategoryUser)
		}},
		{"Preferred", vsBase, func(b scriptBuilder) scriptBuilder { return b.(*pg.CreateBaseTypeStatement[int64]).Preferred(true) }},
		{"Default", vsBase, func(b scriptBuilder) scriptBuilder { return b.(*pg.CreateBaseTypeStatement[int64]).Default(7) }},
		{"Element", vsBase, func(b scriptBuilder) scriptBuilder {
			return b.(*pg.CreateBaseTypeStatement[int64]).Element(pg.NamedType(ddl.RawType[int64]("bigint")))
		}},
		{"Delimiter", vsBase, func(b scriptBuilder) scriptBuilder { return b.(*pg.CreateBaseTypeStatement[int64]).Delimiter(";") }},
		{"Collatable", vsBase, func(b scriptBuilder) scriptBuilder { return b.(*pg.CreateBaseTypeStatement[int64]).Collatable(true) }},
	},
	"CreateRoutineStatement": {
		{"OrReplace", vsRoutine, func(b scriptBuilder) scriptBuilder { return b.(*pg.CreateRoutineStatement).OrReplace() }},
		{"Returns", vsRoutine, func(b scriptBuilder) scriptBuilder {
			return b.(*pg.CreateRoutineStatement).Returns(pg.Returns(ddl.RawType[int32]("integer")))
		}},
		{"Volatility", vsRoutine, func(b scriptBuilder) scriptBuilder { return b.(*pg.CreateRoutineStatement).Volatility(pg.Immutable) }},
		{"NullInput", vsRoutine, func(b scriptBuilder) scriptBuilder { return b.(*pg.CreateRoutineStatement).NullInput(pg.Strict) }},
		{"Security", vsRoutine, func(b scriptBuilder) scriptBuilder { return b.(*pg.CreateRoutineStatement).Security(pg.Definer) }},
		{"Parallel", vsRoutine, func(b scriptBuilder) scriptBuilder { return b.(*pg.CreateRoutineStatement).Parallel(pg.ParallelSafe) }},
		{"Leakproof", vsRoutine, func(b scriptBuilder) scriptBuilder { return b.(*pg.CreateRoutineStatement).Leakproof(true) }},
		{"Cost", vsRoutine, func(b scriptBuilder) scriptBuilder { return b.(*pg.CreateRoutineStatement).Cost(10) }},
		{"Rows", vsRoutine, func(b scriptBuilder) scriptBuilder { return b.(*pg.CreateRoutineStatement).Rows(100) }},
		{"Window", vsRoutine, func(b scriptBuilder) scriptBuilder { return b.(*pg.CreateRoutineStatement).Window() }},
		{"Support", vsRoutine, func(b scriptBuilder) scriptBuilder { return b.(*pg.CreateRoutineStatement).Support(vsFn) }},
		{"Transform", vsRoutine, func(b scriptBuilder) scriptBuilder {
			return b.(*pg.CreateRoutineStatement).Transform(pg.NamedType(ddl.RawType[int64]("bigint")))
		}},
		{"Set", vsRoutine, func(b scriptBuilder) scriptBuilder {
			return b.(*pg.CreateRoutineStatement).Set("search_path", "public")
		}},
		{"SetFromCurrent", vsRoutine, func(b scriptBuilder) scriptBuilder {
			return b.(*pg.CreateRoutineStatement).SetFromCurrent("search_path")
		}},
	},
	"CreateTableAsStatement": {
		{"IfNotExists", vsTableAs, func(b scriptBuilder) scriptBuilder { return b.(*pg.CreateTableAsStatement).IfNotExists() }},
		{"Columns", vsTableAs, func(b scriptBuilder) scriptBuilder { return b.(*pg.CreateTableAsStatement).Columns("a") }},
		{"Using", vsTableAs, func(b scriptBuilder) scriptBuilder { return b.(*pg.CreateTableAsStatement).Using(pg.Heap) }},
		{"WithStorage", vsTableAs, func(b scriptBuilder) scriptBuilder {
			return b.(*pg.CreateTableAsStatement).WithStorage(pg.IntParameter("fillfactor", 70))
		}},
		{"Temporary", vsTableAs, func(b scriptBuilder) scriptBuilder { return b.(*pg.CreateTableAsStatement).Temporary() }},
		{"Unlogged", vsTableAs, func(b scriptBuilder) scriptBuilder { return b.(*pg.CreateTableAsStatement).Unlogged() }},
		{"OnCommit", vsTableAs, func(b scriptBuilder) scriptBuilder {
			return b.(*pg.CreateTableAsStatement).OnCommit(pg.DeleteRows)
		}},
		{"WithNoData", vsTableAs, func(b scriptBuilder) scriptBuilder { return b.(*pg.CreateTableAsStatement).WithNoData() }},
	},
	"AlterTypeNameStatement":      nil,
	"DependsOnExtensionStatement": nil,
	"AlterBaseTypeStatement": {
		{"Receive", vsAlterBase, func(b scriptBuilder) scriptBuilder { return b.(*pg.AlterBaseTypeStatement[int64]).Receive(vsFn) }},
		{"Send", vsAlterBase, func(b scriptBuilder) scriptBuilder { return b.(*pg.AlterBaseTypeStatement[int64]).Send(vsFn) }},
		{"TypmodIn", vsAlterBase, func(b scriptBuilder) scriptBuilder { return b.(*pg.AlterBaseTypeStatement[int64]).TypmodIn(vsFn) }},
		{"TypmodOut", vsAlterBase, func(b scriptBuilder) scriptBuilder { return b.(*pg.AlterBaseTypeStatement[int64]).TypmodOut(vsFn) }},
		{"Analyze", vsAlterBase, func(b scriptBuilder) scriptBuilder { return b.(*pg.AlterBaseTypeStatement[int64]).Analyze(vsFn) }},
		{"Subscript", vsAlterBase, func(b scriptBuilder) scriptBuilder { return b.(*pg.AlterBaseTypeStatement[int64]).Subscript(vsFn) }},
		{"Storage", vsAlterBase, func(b scriptBuilder) scriptBuilder {
			return b.(*pg.AlterBaseTypeStatement[int64]).Storage(pg.StorageExtended)
		}},
	},
	"sqlite.CreateRTreeStatement": nil,
	"sqlite.CreateIndexReady": {
		{"Unique", vsLiteIndex, func(b scriptBuilder) scriptBuilder { return b.(*sqlite.CreateIndexReady).Unique() }},
		{"IfNotExists", vsLiteIndex, func(b scriptBuilder) scriptBuilder { return b.(*sqlite.CreateIndexReady).IfNotExists() }},
		{"Where", vsLiteIndex, func(b scriptBuilder) scriptBuilder { return b.(*sqlite.CreateIndexReady).Where(sqlb.Eq(vsA, 1)) }},
	},
	"sqlite.DropStatement": {
		{"IfExists", func() scriptBuilder { return sqlite.DropIndex(sqlite.Index{Name: "i"}) }, func(b scriptBuilder) scriptBuilder { return b.(*sqlite.DropStatement).IfExists() }},
	},
	"sqlite.CreateViewStatement": {
		{"IfNotExists", func() scriptBuilder {
			return sqlite.CreateView(sqlite.View{Name: "v"}).As(sqlite.Select(vsA).From(vsT))
		}, func(b scriptBuilder) scriptBuilder {
			return b.(*sqlite.CreateViewStatement).IfNotExists()
		}},
	},
	"sqlite.CreateTriggerStatement": nil,

	// UNCHANGED — genuinely nil: the type has only Build.
	"mysql.CreateTriggerStatement": nil,
	// UNCHANGED — genuinely nil: the type has only Build.
	"mysql.TruncateStatement": nil,
	// UNCHANGED — carried over so the region is a drop-in replacement.
	"mysql.CreateViewStatement": {
		{"OrReplace", vsMyView, func(b scriptBuilder) scriptBuilder { return b.(*mysql.CreateViewStatement).OrReplace() }},
		{"WithCheckOption", vsMyView, func(b scriptBuilder) scriptBuilder {
			return b.(*mysql.CreateViewStatement).WithCheckOption(mysql.Local)
		}},
	},
	// UNCHANGED — carried over so the region is a drop-in replacement.
	"mysql.DropStatement": {
		{"IfExists", func() scriptBuilder { return mysql.DropView(mysql.View{Name: "v"}) }, func(b scriptBuilder) scriptBuilder { return b.(*mysql.DropStatement).IfExists() }},
	},
	// REPLACES a bare nil that was ALREADY VACUOUS before this work.
	"mysql.CreateIndexReady": {
		{"With", vsMyIndex, func(b scriptBuilder) scriptBuilder {
			return b.(*mysql.CreateIndexReady).With(mysql.IndexComment("why"))
		}},
		{"Algorithm", vsMyIndex, func(b scriptBuilder) scriptBuilder {
			return b.(*mysql.CreateIndexReady).Algorithm(mysql.AlgorithmInplace)
		}},
		{"Lock", vsMyIndex, func(b scriptBuilder) scriptBuilder {
			return b.(*mysql.CreateIndexReady).Lock(mysql.LockNone)
		}},
	},
	// REPLACES a bare nil that was ALREADY VACUOUS before this work.
	"mysql.DropIndexStatement": {
		{"Algorithm", vsMyDropIndex, func(b scriptBuilder) scriptBuilder {
			return b.(*mysql.DropIndexStatement).Algorithm(mysql.AlgorithmInplace)
		}},
		{"Lock", vsMyDropIndex, func(b scriptBuilder) scriptBuilder {
			return b.(*mysql.DropIndexStatement).Lock(mysql.LockNone)
		}},
	},
	// REPLACES a bare nil, vacuous since And was added on 2026-09-24.
	"mysql.RenameTableStatement": {
		{"And", vsMyRename, func(b scriptBuilder) scriptBuilder {
			return b.(*mysql.RenameTableStatement).And(vsU, vsT)
		}},
	},
	// REPLACES the pass-one entry; unchanged in content.
	"mysql.AlterViewStatement": {
		{"WithCheckOption", vsMyAlterView, func(b scriptBuilder) scriptBuilder {
			return b.(*mysql.AlterViewStatement).WithCheckOption(mysql.Local)
		}},
	},
	// REPLACES the pass-one entry; still correctly nil.
	"mysql.CreateEventStatement": nil,
	// REPLACES the pass-one entry; still correctly nil.
	"mysql.CreateRoutineStatement": nil,
	// REPLACES the pass-one entry; unchanged in content.
	"mysql.AlterEventStatement": {
		{"On", vsMyAlterEvent, func(b scriptBuilder) scriptBuilder {
			return b.(*mysql.AlterEventStatement).On(mysql.EventEvery(2, mysql.UnitHour))
		}},
		{"OnCompletion", vsMyAlterEvent, func(b scriptBuilder) scriptBuilder {
			return b.(*mysql.AlterEventStatement).OnCompletion(mysql.Preserve)
		}},
		{"Status", vsMyAlterEvent, func(b scriptBuilder) scriptBuilder {
			return b.(*mysql.AlterEventStatement).Status(mysql.EventDisable)
		}},
		{"Comment", vsMyAlterEvent, func(b scriptBuilder) scriptBuilder {
			return b.(*mysql.AlterEventStatement).Comment("c")
		}},
		{"Rename", vsMyAlterEvent, func(b scriptBuilder) scriptBuilder {
			return b.(*mysql.AlterEventStatement).Rename(mysql.Event{Name: "e2"})
		}},
		{"Do", vsMyAlterEvent, func(b scriptBuilder) scriptBuilder {
			return b.(*mysql.AlterEventStatement).Do(vsMyEventStep())
		}},
	},
	// REPLACES the pass-one entry — Comment was added in pass two.
	"mysql.AlterRoutineStatement": {
		{"Characteristic", vsMyAlterRoutine, func(b scriptBuilder) scriptBuilder {
			return b.(*mysql.AlterRoutineStatement).Characteristic(mysql.SecurityInvoker)
		}},
		{"Comment", vsMyAlterRoutine, func(b scriptBuilder) scriptBuilder {
			return b.(*mysql.AlterRoutineStatement).Comment("c")
		}},
	},
	// NEW in pass two.
	"mysql.CreateTableLikeStatement": {
		{"IfNotExists", vsMyLike, func(b scriptBuilder) scriptBuilder {
			return b.(*mysql.CreateTableLikeStatement).IfNotExists()
		}},
		{"Temporary", vsMyLike, func(b scriptBuilder) scriptBuilder {
			return b.(*mysql.CreateTableLikeStatement).Temporary()
		}},
	},
	// NEW in pass two.
	"mysql.CreateTableAsStatement": {
		{"IfNotExists", vsMyAs, func(b scriptBuilder) scriptBuilder {
			return b.(*mysql.CreateTableAsStatement).IfNotExists()
		}},
		{"Temporary", vsMyAs, func(b scriptBuilder) scriptBuilder {
			return b.(*mysql.CreateTableAsStatement).Temporary()
		}},
		{"OnConflict", vsMyAs, func(b scriptBuilder) scriptBuilder {
			return b.(*mysql.CreateTableAsStatement).OnConflict(mysql.ConflictIgnore)
		}},
	},
	// NEW in pass two.
	"mysql.DropTemporaryTableStatement": {
		{"IfExists", vsMyDropTemp, func(b scriptBuilder) scriptBuilder {
			return b.(*mysql.DropTemporaryTableStatement).IfExists()
		}},
	},
	"sqlite.CreateTableAsStatement": {
		{"IfNotExists", vsLiteTableAs, func(b scriptBuilder) scriptBuilder { return b.(*sqlite.CreateTableAsStatement).IfNotExists() }},
		{"Temporary", vsLiteTableAs, func(b scriptBuilder) scriptBuilder { return b.(*sqlite.CreateTableAsStatement).Temporary() }},
	},
	"sqlite.CreateVirtualTableStatement": {
		{"IfNotExists", vsVirt, func(b scriptBuilder) scriptBuilder { return b.(*sqlite.CreateVirtualTableStatement).IfNotExists() }},
	},
	"sqlite.CreateFTS5Statement": {
		{"ExternalContent", vsFTS, func(b scriptBuilder) scriptBuilder { return b.(*sqlite.CreateFTS5Statement).ExternalContent(vsT, vsA) }},
		{"Tokenize", vsFTS, func(b scriptBuilder) scriptBuilder { return b.(*sqlite.CreateFTS5Statement).Tokenize(sqlite.Trigram()) }},
		{"Prefix", vsFTS, func(b scriptBuilder) scriptBuilder { return b.(*sqlite.CreateFTS5Statement).Prefix(2) }},
		{"IfNotExists", vsFTS, func(b scriptBuilder) scriptBuilder { return b.(*sqlite.CreateFTS5Statement).IfNotExists() }},
	},
	"sqlite.RebuildStatement": {
		{"Copy", vsRebuild, func(b scriptBuilder) scriptBuilder {
			return b.(*sqlite.RebuildStatement).Copy(sqlite.CopyAs(vsNewB, vsA))
		}},
		{"Recreate", vsRebuild, func(b scriptBuilder) scriptBuilder {
			return b.(*sqlite.RebuildStatement).Recreate(sqlite.SetUserVersion(1))
		}},
	},
	"sqlite.PragmaStatement": nil,
	"CreateTableLikeStatement": {
		{"IfNotExists", func() scriptBuilder { return pg.CreateTableLike(vsT, vsU) }, func(b scriptBuilder) scriptBuilder { return b.(*pg.CreateTableLikeStatement).IfNotExists() }},
	},
}

func scriptSQL(t *testing.T, b scriptBuilder) string {
	t.Helper()
	s, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(s.SQL(), ";")
}

// TestEngineDDLBuilders_MethodsDoNotWriteToTheirReceiver is the receiver-write
// walk for the engine packages' DDL statements.
func TestEngineDDLBuilders_MethodsDoNotWriteToTheirReceiver(t *testing.T) {
	for typ, cases := range ddlBuilders {
		for _, tc := range cases {
			t.Run(typ+"."+tc.method, func(t *testing.T) {
				base := tc.base()
				before := scriptSQL(t, base)
				derived := tc.add(base)
				if derived == base {
					t.Error("returned the receiver, so the statement is shared")
				}
				if after := scriptSQL(t, base); after != before {
					t.Errorf("the method wrote to its receiver:\nbefore %s\nafter  %s", before, after)
				}
				if got := scriptSQL(t, derived); got == before {
					t.Errorf("the derived statement did not change: %s", got)
				}
			})
		}
	}
}

// TestIncompleteViews_ModifiersDoNotWriteToTheirReceiver walks the view
// statements' incomplete forms, which have no Build.
func TestIncompleteViews_ModifiersDoNotWriteToTheirReceiver(t *testing.T) {
	body := pg.Select(vsA).From(vsT)
	v := pg.CreateView(vsView)
	before := scriptSQL(t, v.As(body))
	d := v.OrReplace()
	if scriptSQL(t, v.As(body)) != before || scriptSQL(t, d.As(body)) == before {
		t.Error("CreateViewQuery.OrReplace wrote to its receiver or changed nothing")
	}
	m := pg.CreateMaterializedView(vsMat)
	before = scriptSQL(t, m.As(body))
	dm := m.IfNotExists()
	if scriptSQL(t, m.As(body)) != before || scriptSQL(t, dm.As(body)) == before {
		t.Error("CreateMaterializedViewQuery.IfNotExists wrote to its receiver or changed nothing")
	}
}

// TestIncompleteTriggers_ModifiersDoNotWriteToTheirReceiver walks the trigger
// and partition statements' incomplete stages, which have no Build.
func TestIncompleteTriggers_ModifiersDoNotWriteToTheirReceiver(t *testing.T) {
	q := pg.CreateTrigger(vsTrigger)
	build := func(q *pg.CreateTriggerQuery) string { return scriptSQL(t, q.After(pg.OnInsert()).Execute(vsFn)) }
	before := build(q)
	if d := q.OrReplace(); build(q) != before || build(d) == before {
		t.Error("CreateTriggerQuery.OrReplace wrote to its receiver or changed nothing")
	}
	timed := q.After(pg.OnInsert())
	before = scriptSQL(t, timed.Execute(vsFn))
	for name, m := range map[string]func(*pg.TimedTriggerQuery) *pg.TimedTriggerQuery{
		"ForEachRow": (*pg.TimedTriggerQuery).ForEachRow,
		"When":       func(x *pg.TimedTriggerQuery) *pg.TimedTriggerQuery { return x.When(sqlb.Eq(pg.New(vsA), 1)) },
	} {
		d := m(timed)
		if scriptSQL(t, timed.Execute(vsFn)) != before || scriptSQL(t, d.Execute(vsFn)) == before {
			t.Errorf("TimedTriggerQuery.%s wrote to its receiver or changed nothing", name)
		}
	}
	p := pg.CreatePartition(vsU, vsT)
	before = scriptSQL(t, p.For(pg.DefaultPartition()))
	if d := p.IfNotExists(); scriptSQL(t, p.For(pg.DefaultPartition())) != before || scriptSQL(t, d.For(pg.DefaultPartition())) == before {
		t.Error("CreatePartitionQuery.IfNotExists wrote to its receiver or changed nothing")
	}
}

// TestCreateIndexQuery_ModifiersDoNotWriteToTheirReceiver walks the incomplete
// CREATE INDEX, which has no Build and so is not in ddlBuilders: each modifier
// must leave the receiver rendering what it did.
func TestCreateIndexQuery_ModifiersDoNotWriteToTheirReceiver(t *testing.T) {
	for name, m := range map[string]func(*pg.CreateIndexQuery) *pg.CreateIndexQuery{
		"Unique":       (*pg.CreateIndexQuery).Unique,
		"IfNotExists":  (*pg.CreateIndexQuery).IfNotExists,
		"Concurrently": (*pg.CreateIndexQuery).Concurrently,
		"Using":        func(q *pg.CreateIndexQuery) *pg.CreateIndexQuery { return q.Using(pg.GIN) },
	} {
		q := pg.CreateIndex(vsIndex, vsT)
		before := scriptSQL(t, q.On(pg.IndexKey(vsA)))
		d := m(q)
		if after := scriptSQL(t, q.On(pg.IndexKey(vsA))); after != before {
			t.Errorf("%s wrote to its receiver: %s", name, after)
		}
		if scriptSQL(t, d.On(pg.IndexKey(vsA))) == before {
			t.Errorf("%s did not change the derived statement", name)
		}
	}
}
