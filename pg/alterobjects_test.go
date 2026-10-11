package pg_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/pg"
)

// The ALTER forms of a declared object added on 2026-09-24, rendered. Each one
// closes an entry the DDL detector had recorded as partial, and each is
// executed against a real Postgres in contrib/driver-pgx, because a rendering
// test proves the text and only execution proves the SQL.

var (
	aoIndex   = pg.Index{Schema: "auth", Name: "users_email_ix"}
	aoPart    = pg.Index{Schema: "auth", Name: "users_email_ix_p1"}
	aoView    = pg.View{Schema: "auth", Name: "active_users"}
	aoMatView = pg.MaterializedView{Schema: "auth", Name: "user_counts"}
	aoExt     = pg.Extension{Name: "btree_gist"}
	aoApp     = pg.Role{Name: "app"}
	aoDomain  = pg.Domain[int64]{Schema: "auth", Name: "positive"}
)

// TestAlterObjects_IndexStorageAndPartition renders the three ALTER INDEX
// forms that were the operator's until the detector asked for them: the
// storage parameters of a library's own index, the reset that puts them back,
// and the attach that finishes a partitioned index built one partition at a
// time.
func TestAlterObjects_IndexStorageAndPartition(t *testing.T) {
	got := buildAll(t,
		pg.AlterIndex(aoIndex).SetStorage(pg.IntParameter("fillfactor", 70), pg.BoolParameter("deduplicate_items", false)),
		pg.AlterIndex(aoIndex).ResetStorage("fillfactor", "deduplicate_items"),
		pg.AlterIndex(aoIndex).AttachPartition(aoPart),
	)
	want := `ALTER INDEX "auth"."users_email_ix" SET ("fillfactor" = 70, "deduplicate_items" = false)
ALTER INDEX "auth"."users_email_ix" RESET ("fillfactor", "deduplicate_items")
ALTER INDEX "auth"."users_email_ix" ATTACH PARTITION "auth"."users_email_ix_p1"`
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

// TestAlterObjects_MaterializedViewForms renders the ALTER MATERIALIZED VIEW
// actions: a renamed output column, the view's own storage parameters, and the
// access method its rows are kept in.
func TestAlterObjects_MaterializedViewForms(t *testing.T) {
	got := buildAll(t,
		pg.AlterMaterializedView(aoMatView).RenameColumn("n", "user_count"),
		pg.AlterMaterializedView(aoMatView).SetStorage(pg.FloatParameter("autovacuum_vacuum_scale_factor", 0.05)),
		pg.AlterMaterializedView(aoMatView).ResetStorage("autovacuum_vacuum_scale_factor"),
		pg.AlterMaterializedView(aoMatView).SetAccessMethod(pg.Heap),
	)
	want := `ALTER MATERIALIZED VIEW "auth"."user_counts" RENAME COLUMN "n" TO "user_count"
ALTER MATERIALIZED VIEW "auth"."user_counts" SET ("autovacuum_vacuum_scale_factor" = 0.05)
ALTER MATERIALIZED VIEW "auth"."user_counts" RESET ("autovacuum_vacuum_scale_factor")
ALTER MATERIALIZED VIEW "auth"."user_counts" SET ACCESS METHOD "heap"`
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

// TestAlterObjects_ViewColumnRename renders ALTER VIEW ... RENAME COLUMN, in
// both its forms.
func TestAlterObjects_ViewColumnRename(t *testing.T) {
	got := buildAll(t,
		pg.RenameViewColumn(aoView, "email", "address"),
		pg.RenameViewColumn(aoView, "email", "address").IfExists(),
	)
	want := `ALTER VIEW "auth"."active_users" RENAME COLUMN "email" TO "address"
ALTER VIEW IF EXISTS "auth"."active_users" RENAME COLUMN "email" TO "address"`
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

// TestAlterObjects_DomainDefaults renders the domain's own default, which was
// left out on the reading that a default belongs on the column. It does, when
// it differs per column; a default every column of the type should start at
// belongs to the type, and there was no way to write one.
func TestAlterObjects_DomainDefaults(t *testing.T) {
	got := buildAll(t,
		pg.AlterDomain(aoDomain).SetDefault(1),
		pg.AlterDomain(aoDomain).SetDefaultExpr(sqlb.Mul(sqlb.Param(int64(2)), sqlb.Param(int64(3)))),
		pg.AlterDomain(aoDomain).DropDefault(),
	)
	want := `ALTER DOMAIN "auth"."positive" SET DEFAULT 1
ALTER DOMAIN "auth"."positive" SET DEFAULT ((2 * 3))
ALTER DOMAIN "auth"."positive" DROP DEFAULT`
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

// TestAlterObjects_ExtensionMembersAndSchema renders the membership statements
// an extension's own upgrade script runs, with the object kind coming from the
// Go type of the value, and the move into another schema.
func TestAlterObjects_ExtensionMembersAndSchema(t *testing.T) {
	got := buildAll(t,
		pg.AddExtensionMember(aoExt, pg.Function{Schema: "auth", Name: "touch"}),
		pg.AddExtensionMember(aoExt, pg.MemberTable(tUsers)),
		pg.AddExtensionMember(aoExt, aoView),
		pg.DropExtensionMember(aoExt, pg.Sequence[int64]{Schema: "auth", Name: "user_seq"}),
		pg.SetExtensionSchema(aoExt, pg.Schema{Name: "ext"}),
	)
	want := `ALTER EXTENSION "btree_gist" ADD FUNCTION "auth"."touch"()
ALTER EXTENSION "btree_gist" ADD TABLE "auth"."users"
ALTER EXTENSION "btree_gist" ADD VIEW "auth"."active_users"
ALTER EXTENSION "btree_gist" DROP SEQUENCE "auth"."user_seq"
ALTER EXTENSION "btree_gist" SET SCHEMA "ext"`
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

// TestAlterObjects_OwnerTo renders OWNER TO for every ownable kind, which is
// what the detector recorded against four commands at once, and pins that a
// named role and a keyword role are written the same way.
func TestAlterObjects_OwnerTo(t *testing.T) {
	got := buildAll(t,
		pg.SetOwner(aoView, aoApp),
		pg.SetOwner(aoMatView, aoApp),
		pg.SetOwner(pg.Sequence[int64]{Schema: "auth", Name: "user_seq"}, aoApp),
		pg.SetOwner(pg.Enum[oStatus]{Schema: "auth", Name: "user_status"}, aoApp),
		pg.SetOwner(aoDomain, pg.RoleCurrentUser),
	)
	want := `ALTER VIEW "auth"."active_users" OWNER TO "app"
ALTER MATERIALIZED VIEW "auth"."user_counts" OWNER TO "app"
ALTER SEQUENCE "auth"."user_seq" OWNER TO "app"
ALTER TYPE "auth"."user_status" OWNER TO "app"
ALTER DOMAIN "auth"."positive" OWNER TO CURRENT_USER`
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

// TestAlterObjects_PolicyNamesRoles pins that a policy can now name a declared
// role, which is the whole of what ALTER POLICY ... TO was partial for, and
// that both spellings of a role compose in one list.
func TestAlterObjects_PolicyNamesRoles(t *testing.T) {
	p := pg.Policy{Name: "tenant_isolation", Table: tUsers}
	got := buildAll(t,
		pg.CreatePolicy(p).To(aoApp, pg.RoleCurrentUser).Using(sqlb.Eq(tTenantID, 1)),
		pg.AlterPolicy(p).To(aoApp),
	)
	want := `CREATE POLICY "tenant_isolation" ON "auth"."users" AS PERMISSIVE TO "app", CURRENT_USER USING ("tenant_id" = 1)
ALTER POLICY "tenant_isolation" ON "auth"."users" TO "app"`
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

// TestAlterObjects_RefuseIncompleteValues pins that each form fails Build
// rather than rendering half a statement. A role with no name is the one worth
// keeping: it is the only value here that would otherwise render an empty
// quoted identifier, which Postgres accepts as a role that cannot exist.
func TestAlterObjects_RefuseIncompleteValues(t *testing.T) {
	cases := []struct {
		name string
		run  func() (ddl.Script, error)
	}{
		{"unnamed role", func() (ddl.Script, error) { return pg.SetOwner(aoView, pg.Role{}).Build() }},
		{"unnamed index", func() (ddl.Script, error) { return pg.AlterIndex(aoIndex).AttachPartition(pg.Index{}).Build() }},
		{"unnamed reset", func() (ddl.Script, error) { return pg.AlterIndex(aoIndex).ResetStorage("").Build() }},
		{"unnamed access method", func() (ddl.Script, error) {
			return pg.AlterMaterializedView(aoMatView).SetAccessMethod("").Build()
		}},
		{"unnamed column", func() (ddl.Script, error) { return pg.RenameViewColumn(aoView, "a", "").Build() }},
		{"unnamed schema", func() (ddl.Script, error) { return pg.SetExtensionSchema(aoExt, pg.Schema{}).Build() }},
		{"no member", func() (ddl.Script, error) { return pg.AddExtensionMember(aoExt, nil).Build() }},
	}
	for _, c := range cases {
		if _, err := c.run(); err == nil {
			t.Errorf("%s: built with no error", c.name)
		}
	}
}

// TestAlterObjects_MemberSignatureIsChecked pins that a function's declared
// parameters reach every statement that names it, and that a parameter that
// cannot be rendered fails the statement rather than being dropped. The second
// half is why commentTarget reports an error at all.
func TestAlterObjects_MemberSignatureIsChecked(t *testing.T) {
	f := pg.Function{Schema: "auth", Name: "add", Params: []pg.FunctionArg{
		pg.Arg("a", ddl.RawType[int64]("bigint")),
		pg.OutArg("sum", ddl.RawType[int64]("bigint")),
	}}
	got := buildAll(t, pg.AddExtensionMember(aoExt, f), pg.DropFunction(f), pg.CommentOn(f).Is("adds"))
	want := `ALTER EXTENSION "btree_gist" ADD FUNCTION "auth"."add"(IN "a" bigint, OUT "sum" bigint)
DROP FUNCTION "auth"."add"(IN "a" bigint, OUT "sum" bigint)
COMMENT ON FUNCTION "auth"."add"(IN "a" bigint, OUT "sum" bigint) IS 'adds'`
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	broken := pg.Function{Name: "add", Params: []pg.FunctionArg{{}}}
	for _, run := range []func() (ddl.Script, error){
		func() (ddl.Script, error) { return pg.AddExtensionMember(aoExt, broken).Build() },
		func() (ddl.Script, error) { return pg.DropFunction(broken).Build() },
		func() (ddl.Script, error) { return pg.CommentOn(broken).Is("x").Build() },
	} {
		if _, err := run(); !errors.Is(err, ddl.ErrZeroValue) {
			t.Errorf("a malformed parameter was not refused: %v", err)
		}
	}
}

// TestAlterObjects_MethodsDoNotWriteToTheirReceiver pins the builder invariant
// for the statements whose options accumulate, which the engine-builder gate
// walks by type name and cannot walk for a query type with no Build.
func TestAlterObjects_MethodsDoNotWriteToTheirReceiver(t *testing.T) {
	base := pg.AlterIndex(aoIndex)
	first := build(t)(base.SetStorage(pg.IntParameter("fillfactor", 70)).Build())
	second := build(t)(base.ResetStorage("fillfactor").Build())
	if strings.Contains(second, "70") {
		t.Errorf("AlterIndexQuery kept the first derivation's option: %s", second)
	}
	if !strings.Contains(first, "70") {
		t.Errorf("the first derivation lost its option: %s", first)
	}
}
