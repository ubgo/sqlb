package pg_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/pg"
)

// The CREATE TABLE, CREATE INDEX, CREATE VIEW, CREATE MATERIALIZED VIEW and
// CREATE TRIGGER forms of the second pass, rendered. Every one is executed in
// contrib/driver-pgx/tableshape_test.go.

var (
	tsParent = sqlb.Table{Name: "people", Schema: "auth"}
	tsRow    = pg.Composite[string]{Schema: "auth", Name: "user_row"}
)

// TestTableShape_OptionsRenderInGrammarOrder is the test the file comment
// promises: Postgres fixes the order of a table's options and the statement
// renders them in the order the caller gave, so the documented sequence is
// pinned here rather than asserted in prose. The server takes this exact
// statement in the driver's execution test.
//
// Since 2026-09-24 the options are passed in REVERSE of the grammar's order,
// because passing them in the right order proved nothing about a caller who
// did not: the statement used to write them in caller order, and Postgres
// answers every out-of-order pair with a syntax error. They now render in the
// order the rank table in tableshape.go gives, whatever order they arrive in.
func TestTableShape_OptionsRenderInGrammarOrder(t *testing.T) {
	got := build(t)(pg.CreateTable(tUsers).
		Column(ddl.Column(tUserID), ddl.Column(tEmail)).
		Clause(
			pg.OnCommit(pg.DeleteRows),
			pg.TableStorage(pg.IntParameter("fillfactor", 70)),
			pg.TableAccessMethod(pg.Heap),
			pg.Inherits(tsParent),
			pg.Temporary(),
			pg.ColumnCollate(tEmail, "C"),
			pg.ColumnCompression(tEmail, "pglz"),
			pg.ColumnStorage(tEmail, pg.StorageExternal),
		).Build())
	want := `CREATE TEMPORARY TABLE "auth"."users" ("id" BIGINT NOT NULL, ` +
		`"email" TEXT STORAGE external COMPRESSION "pglz" COLLATE "C" NOT NULL) ` +
		`INHERITS ("auth"."people") USING "heap" WITH ("fillfactor" = 70) ON COMMIT DELETE ROWS`
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

// TestTableShape_KeyIndexParameters renders the INCLUDE and WITH a key
// constraint's own index takes, on all three constraints that have one, and
// the MATCH and NO INHERIT that were missing beside them.
func TestTableShape_KeyIndexParameters(t *testing.T) {
	got := build(t)(pg.CreateTable(tUsers).
		Column(ddl.Column(tUserID), ddl.Column(tTenantID), ddl.Column(tEmail)).
		Clause(
			pg.PrimaryKey(tUserID).Include(tEmail).With(pg.IntParameter("fillfactor", 80)),
			pg.Unique(pg.TableConstraint{Table: tUsers, Name: "users_email_key"}, tEmail).
				Include(tTenantID).With(pg.IntParameter("fillfactor", 90)),
			pg.Check(pg.TableConstraint{Table: tUsers, Name: "users_tenant_positive"}, sqlb.Greater(tTenantID, 0)).NoInherit(),
			pg.References(tTenantID, tSessTen).Match(pg.MatchFull),
		).Build())
	want := `CREATE TABLE "auth"."users" ("id" BIGINT NOT NULL, "tenant_id" BIGINT NOT NULL, "email" TEXT NOT NULL, ` +
		`PRIMARY KEY ("id") INCLUDE ("email") WITH ("fillfactor" = 80), ` +
		`CONSTRAINT "users_email_key" UNIQUE ("email") INCLUDE ("tenant_id") WITH ("fillfactor" = 90), ` +
		`CONSTRAINT "users_tenant_positive" CHECK ("tenant_id" > 0) NO INHERIT, ` +
		`FOREIGN KEY ("tenant_id") REFERENCES "auth"."sessions" ("tenant_id") MATCH FULL)`
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

// TestTableShape_ExclusionIndexParameters renders the exclusion constraint's
// index parameters, which go before its WHERE.
func TestTableShape_ExclusionIndexParameters(t *testing.T) {
	c := pg.TableConstraint{Table: tUsers, Name: "users_no_overlap"}
	got := build(t)(pg.CreateTable(tUsers).
		Column(ddl.Column(tUserID), ddl.Column(tTenantID)).
		Clause(pg.Exclude(c, pg.GiST, pg.ExcludeOn(tUserID, pg.OpEquals)).
			Include(tTenantID).
			With(pg.IntParameter("fillfactor", 70)).
			Where(sqlb.Greater(tTenantID, 0))).Build())
	want := `CREATE TABLE "auth"."users" ("id" BIGINT NOT NULL, "tenant_id" BIGINT NOT NULL, ` +
		`CONSTRAINT "users_no_overlap" EXCLUDE USING "gist" ("id" WITH =) INCLUDE ("tenant_id") ` +
		`WITH ("fillfactor" = 70) WHERE ("tenant_id" > 0))`
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

// TestTableShape_IndexOnOnlyAndOpclassParameters renders the two CREATE INDEX
// forms that were missing: the partitioned build and an operator class's own
// parameters, which is the only way an extension's index method is tuned.
func TestTableShape_IndexOnOnlyAndOpclassParameters(t *testing.T) {
	got := buildAll(t,
		pg.CreateIndex(pg.Index{Name: "users_tenant_ix"}, tUsers).On(pg.IndexKey(tTenantID)).OnOnly(),
		pg.CreateIndex(pg.Index{Name: "users_email_ix"}, tUsers).
			On(pg.IndexKey(tEmail).OpclassWith(pg.NewOpClass[string]("text_ops"), pg.IntParameter("m", 16))),
	)
	want := `CREATE INDEX "users_tenant_ix" ON ONLY "auth"."users" ("tenant_id")
CREATE INDEX "users_email_ix" ON "auth"."users" ("email" "text_ops" ("m" = 16))`
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

// TestTableShape_ViewDeclaration renders the view forms: the column list, the
// options set at creation rather than after, and the two modifiers.
func TestTableShape_ViewDeclaration(t *testing.T) {
	v := pg.View{Schema: "auth", Name: "active"}
	mv := pg.MaterializedView{Schema: "auth", Name: "counts"}
	got := buildAll(t,
		pg.CreateView(v).As(pg.Select(tEmail).From(tUsers)).
			Columns("address").
			WithOptions(pg.SecurityInvokerSetting(true), pg.CheckOptionSetting(pg.Local)),
		pg.CreateView(v).As(pg.Select(tEmail).From(tUsers)).Temporary(),
		pg.CreateView(v).As(pg.Select(tEmail).From(tUsers)).Recursive().Columns("n"),
		pg.CreateMaterializedView(mv).As(pg.Select(tEmail).From(tUsers)).
			Columns("address").Using(pg.Heap).WithStorage(pg.IntParameter("fillfactor", 60)).WithNoData(),
	)
	want := `CREATE VIEW "auth"."active" ("address") WITH (security_invoker = true, check_option = local) AS SELECT "u"."email" FROM "auth"."users" AS "u"
CREATE TEMPORARY VIEW "auth"."active" AS SELECT "u"."email" FROM "auth"."users" AS "u"
CREATE RECURSIVE VIEW "auth"."active" ("n") AS SELECT "u"."email" FROM "auth"."users" AS "u"
CREATE MATERIALIZED VIEW "auth"."counts" ("address") USING "heap" WITH ("fillfactor" = 60) AS SELECT "u"."email" FROM "auth"."users" AS "u" WITH NO DATA`
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	if _, err := pg.CreateView(v).As(pg.Select(tEmail).From(tUsers)).Recursive().Build(); !errors.Is(err, pg.ErrRecursiveColumns) {
		t.Errorf("a recursive view with no column list: %v", err)
	}
}

// TestTableShape_TriggerForms renders the CREATE TRIGGER forms that were
// missing: transition tables, TRUNCATE, a deferred constraint trigger, INSTEAD
// OF, and the constant arguments a function reads out of TG_ARGV.
func TestTableShape_TriggerForms(t *testing.T) {
	f := pg.Function{Schema: "auth", Name: "touch"}
	got := buildAll(t,
		pg.CreateTrigger(pg.Trigger{Name: "after_insert", Table: tUsers}).
			After(pg.OnInsert()).Referencing("", "new_rows").ExecuteWith(f, "insert", "users"),
		pg.CreateTrigger(pg.Trigger{Name: "after_update", Table: tUsers}).
			After(pg.OnUpdate()).Referencing("old_rows", "new_rows").Execute(f),
		pg.CreateTrigger(pg.Trigger{Name: "after_truncate", Table: tUsers}).
			After(pg.OnTruncate()).Execute(f),
		pg.CreateTrigger(pg.Trigger{Name: "pairs", Table: tUsers}).Constraint().
			After(pg.OnInsert()).From(tSessions).Deferrable(pg.InitiallyDeferred).ForEachRow().Execute(f),
		pg.CreateTrigger(pg.Trigger{Name: "instead", Table: tUsers}).
			InsteadOf(pg.OnInsert()).ForEachRow().Execute(f),
	)
	want := `CREATE TRIGGER "after_insert" AFTER INSERT ON "auth"."users" REFERENCING NEW TABLE AS "new_rows" FOR EACH STATEMENT EXECUTE FUNCTION "auth"."touch"('insert', 'users')
CREATE TRIGGER "after_update" AFTER UPDATE ON "auth"."users" REFERENCING OLD TABLE AS "old_rows" NEW TABLE AS "new_rows" FOR EACH STATEMENT EXECUTE FUNCTION "auth"."touch"()
CREATE TRIGGER "after_truncate" AFTER TRUNCATE ON "auth"."users" FOR EACH STATEMENT EXECUTE FUNCTION "auth"."touch"()
CREATE CONSTRAINT TRIGGER "pairs" AFTER INSERT ON "auth"."users" FROM "auth"."sessions" DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION "auth"."touch"()
CREATE TRIGGER "instead" INSTEAD OF INSERT ON "auth"."users" FOR EACH ROW EXECUTE FUNCTION "auth"."touch"()`
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

// TestTableShape_TypedTableIsAlterOnly records the one form of CREATE TABLE
// the second pass did not build, as a test rather than as a note nobody reads:
// a typed table takes its columns from a composite type instead of declaring
// any, and ddl.CreateTable is a column list. AlterTable's OfType reaches the
// same state from a table whose columns match, which is what this pins.
func TestTableShape_TypedTableIsAlterOnly(t *testing.T) {
	got := build(t)(pg.AlterTable(tUsers).Clause(pg.OfType(tsRow)).Build())
	if want := `ALTER TABLE "auth"."users" OF "auth"."user_row"`; got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

// TestTableShape_RefuseIncompleteValues pins that each new clause fails Build
// rather than rendering a statement with a name or a keyword missing.
func TestTableShape_RefuseIncompleteValues(t *testing.T) {
	table := func(c ...ddl.TableClause[pg.Dialect]) (ddl.Script, error) {
		return pg.CreateTable(tUsers).Column(ddl.Column(tUserID)).Clause(c...).Build()
	}
	cases := []struct {
		name string
		want error
		run  func() (ddl.Script, error)
	}{
		{"unnamed parent", pg.ErrEmptyName, func() (ddl.Script, error) { return table(pg.Inherits(sqlb.Table{})) }},
		{"unnamed access method", pg.ErrEmptyName, func() (ddl.Script, error) { return table(pg.TableAccessMethod("")) }},
		{"unknown commit action", pg.ErrUnknownKeyword, func() (ddl.Script, error) { return table(pg.OnCommit(pg.OnCommitAction(9))) }},
		{"a zero storage parameter", ddl.ErrZeroValue, func() (ddl.Script, error) { return table(pg.TableStorage(pg.StorageParameter{})) }},
		{"unknown match type", pg.ErrUnknownKeyword, func() (ddl.Script, error) {
			return table(pg.References(tUserID, tSessUser).Match(pg.MatchType(9)))
		}},
		{"unknown column storage", pg.ErrUnknownKeyword, func() (ddl.Script, error) {
			return pg.CreateTable(tUsers).Column(ddl.Column(tEmail)).Clause(pg.ColumnStorage(tEmail, pg.TypeStorage(9))).Build()
		}},
		{"empty compression", pg.ErrEmptyName, func() (ddl.Script, error) {
			return pg.CreateTable(tUsers).Column(ddl.Column(tEmail)).Clause(pg.ColumnCompression(tEmail, "")).Build()
		}},
		{"an empty view column", pg.ErrEmptyName, func() (ddl.Script, error) {
			return pg.CreateView(pg.View{Name: "v"}).As(pg.Select(tEmail).From(tUsers)).Columns("").Build()
		}},
	}
	for _, c := range cases {
		if _, err := c.run(); !errors.Is(err, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, err, c.want)
		}
	}
}

// TestTableShape_MethodsDoNotWriteToTheirReceiver pins the builder invariant
// for the constraint clauses, whose new methods accumulate; the statement
// builders are walked by the engine-builder gate and these are not.
func TestTableShape_MethodsDoNotWriteToTheirReceiver(t *testing.T) {
	base := pg.Unique(pg.TableConstraint{Table: tUsers, Name: "k"}, tEmail)
	derived := base.Include(tTenantID)
	render := func(c ddl.TableClause[pg.Dialect]) string {
		return build(t)(pg.CreateTable(tUsers).Column(ddl.Column(tUserID), ddl.Column(tTenantID), ddl.Column(tEmail)).Clause(c).Build())
	}
	if !strings.Contains(render(derived), "INCLUDE") {
		t.Error("the derived constraint lost its covering column")
	}
	if strings.Contains(render(base), "INCLUDE") {
		t.Error("UniqueClause.Include wrote to its receiver")
	}
}

// TestTableShape_CreateTableAs renders the statement that makes a table from a
// query, which was classified as the operator's until the second pass found
// SQLite and MySQL both building it.
func TestTableShape_CreateTableAs(t *testing.T) {
	summary := sqlb.Table{Name: "summary", Schema: "auth"}
	got := buildAll(t,
		pg.CreateTableAs(summary, pg.Select(tEmail).From(tUsers)),
		pg.CreateTableAs(summary, pg.Select(tEmail).From(tUsers)).
			IfNotExists().Columns("address").Unlogged().
			Using(pg.Heap).WithStorage(pg.IntParameter("fillfactor", 70)).WithNoData(),
		pg.CreateTableAs(summary, pg.Select(tEmail).From(tUsers)).Temporary().OnCommit(pg.DropAtCommit),
	)
	want := `CREATE TABLE "auth"."summary" AS SELECT "u"."email" FROM "auth"."users" AS "u"
CREATE UNLOGGED TABLE IF NOT EXISTS "auth"."summary" ("address") USING "heap" WITH ("fillfactor" = 70) AS SELECT "u"."email" FROM "auth"."users" AS "u" WITH NO DATA
CREATE TEMPORARY TABLE "auth"."summary" ON COMMIT DROP AS SELECT "u"."email" FROM "auth"."users" AS "u"`
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	if _, err := (&pg.CreateTableAsStatement{}).Build(); !errors.Is(err, ddl.ErrZeroValue) {
		t.Errorf("a statement with no query: %v", err)
	}
}

// TestTableShape_ColumnOptionsHaveAnAlterHalf renders the ALTER side of the
// two column options the create side had, which is the asymmetry the second
// pass was looking for: a property that can be declared and not changed is a
// declaration no migration can follow.
func TestTableShape_ColumnOptionsHaveAnAlterHalf(t *testing.T) {
	got := build(t)(pg.AlterTable(tUsers).Clause(
		pg.SetColumnStorage(tEmail, pg.StorageExternal),
		pg.SetColumnCompression(tEmail, "pglz"),
	).Build())
	want := `ALTER TABLE "auth"."users" ALTER COLUMN "email" SET STORAGE external, ALTER COLUMN "email" SET COMPRESSION "pglz"`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}
