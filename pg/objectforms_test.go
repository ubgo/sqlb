package pg_test

import (
	"errors"
	"testing"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/pg"
)

// The forms inside a statement that already had a constructor, rendered. Each
// is executed in contrib/driver-pgx/objectforms_test.go, and the one that
// could only have been found there is ALTER EXTENSION's member list: an Index
// renders perfectly and the server refuses it.

var (
	obExt    = pg.Extension{Name: "btree_gist"}
	obSchema = pg.Schema{Name: "auth"}
	obRole   = pg.Role{Name: "app"}
	obDomain = pg.Domain[string]{Schema: "auth", Name: "code"}
	obColl   = pg.Collation{Schema: "auth", Name: "ci"}
	obSeq    = pg.Sequence[int64]{Schema: "auth", Name: "user_seq"}
	obStats  = pg.Statistics{Schema: "auth", Name: "users_stats"}
	obIndex  = pg.Index{Schema: "auth", Name: "users_email_ix"}
)

// TestObjectForms_ExtensionAndSchema renders the CREATE EXTENSION options and
// the two ways a schema is given to a role.
func TestObjectForms_ExtensionAndSchema(t *testing.T) {
	got := buildAll(t,
		pg.CreateExtension(obExt).IfNotExists().Schema(obSchema).Version("1.7").Cascade(),
		pg.CreateSchema(obSchema).IfNotExists().Authorization(obRole),
		pg.CreateSchema(obSchema).Authorization(pg.RoleCurrentUser),
		pg.SetOwner(obSchema, obRole),
	)
	want := `CREATE EXTENSION IF NOT EXISTS "btree_gist" SCHEMA "auth" VERSION '1.7' CASCADE
CREATE SCHEMA IF NOT EXISTS "auth" AUTHORIZATION "app"
CREATE SCHEMA "auth" AUTHORIZATION CURRENT_USER
ALTER SCHEMA "auth" OWNER TO "app"`
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

// TestObjectForms_ExtensionMembership pins the member kinds and the two that
// are deliberately absent. An Index and a Statistics render as well as
// anything else and the server refuses both, which is why the absence is a
// compile error here rather than a runtime one; the refusal itself is measured
// in the driver's execution test.
func TestObjectForms_ExtensionMembership(t *testing.T) {
	got := buildAll(t,
		pg.AddExtensionMember(obExt, obDomain),
		pg.AddExtensionMember(obExt, obColl),
		pg.DependsOnExtension(obIndex, obExt),
		pg.NoDependsOnExtension(obIndex, obExt),
		pg.DependsOnExtension(pg.Trigger{Name: "touch", Table: tUsers}, obExt),
		pg.SetExtensionSchema(obExt, obSchema),
	)
	want := `ALTER EXTENSION "btree_gist" ADD DOMAIN "auth"."code"
ALTER EXTENSION "btree_gist" ADD COLLATION "auth"."ci"
ALTER INDEX "auth"."users_email_ix" DEPENDS ON EXTENSION "btree_gist"
ALTER INDEX "auth"."users_email_ix" NO DEPENDS ON EXTENSION "btree_gist"
ALTER TRIGGER "touch" ON "auth"."users" DEPENDS ON EXTENSION "btree_gist"
ALTER EXTENSION "btree_gist" SET SCHEMA "auth"`
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

// TestObjectForms_DomainDeclaration renders the CREATE DOMAIN options and
// every ALTER DOMAIN form, including the comment on a domain's constraint,
// which Postgres spells ON DOMAIN rather than ON.
func TestObjectForms_DomainDeclaration(t *testing.T) {
	con := pg.DomainConstraint[string]{Domain: obDomain, Name: "code_len"}
	con2 := pg.DomainConstraint[string]{Domain: obDomain, Name: "code_length"}
	got := buildAll(t,
		pg.CreateDomain(obDomain, ddl.RawType[string]("text")).Collate(obColl).Default("none").NotNull(),
		pg.CreateDomain(obDomain, ddl.RawType[string]("text")).DefaultExpr(sqlb.Lower(pg.DomainValue[string]())),
		pg.AlterDomain(obDomain).SetNotNull(),
		pg.AlterDomain(obDomain).DropNotNull(),
		pg.AlterDomain(obDomain).RenameConstraint(con, con2),
		pg.AlterDomain(obDomain).DropConstraint(con2).Cascade(),
		pg.AlterDomain(obDomain).RenameTo(pg.Domain[string]{Schema: "auth", Name: "code_v2"}),
		pg.CommentOnDomainConstraint(con).Is("not empty"),
	)
	want := `CREATE DOMAIN "auth"."code" AS text COLLATE "auth"."ci" DEFAULT 'none' NOT NULL
CREATE DOMAIN "auth"."code" AS text DEFAULT (lower(VALUE))
ALTER DOMAIN "auth"."code" SET NOT NULL
ALTER DOMAIN "auth"."code" DROP NOT NULL
ALTER DOMAIN "auth"."code" RENAME CONSTRAINT "code_len" TO "code_length"
ALTER DOMAIN "auth"."code" DROP CONSTRAINT IF EXISTS "code_length" CASCADE
ALTER DOMAIN "auth"."code" RENAME TO "code_v2"
COMMENT ON CONSTRAINT "code_len" ON DOMAIN "auth"."code" IS 'not empty'`
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

// TestObjectForms_CascadeBelongsToDropConstraint pins that CASCADE fails Build
// on any ALTER DOMAIN action but a drop, rather than rendering a statement the
// server would reject.
func TestObjectForms_CascadeBelongsToDropConstraint(t *testing.T) {
	if _, err := pg.AlterDomain(obDomain).SetNotNull().Cascade().Build(); !errors.Is(err, pg.ErrCascadeNeedsDrop) {
		t.Errorf("CASCADE on SET NOT NULL: %v", err)
	}
}

// TestObjectForms_Collation renders the collation forms that were missing: the
// libc provider's two halves, ICU tailoring rules, a recorded version, and the
// copy that takes no options at all.
func TestObjectForms_Collation(t *testing.T) {
	got := buildAll(t,
		pg.CreateCollation(obColl, "und-u-ks-level2").ICU().Nondeterministic(),
		pg.CreateCollation(obColl, "").LcCollate("C").LcCtype("C"),
		pg.CreateCollation(obColl, "und").ICU().Rules("&a < g").Version("2.36"),
		pg.CreateCollationFrom(obColl, pg.Collation{Name: "C"}).IfNotExists(),
	)
	want := `CREATE COLLATION "auth"."ci" (provider = icu, locale = 'und-u-ks-level2', deterministic = false)
CREATE COLLATION "auth"."ci" (provider = libc, lc_collate = 'C', lc_ctype = 'C')
CREATE COLLATION "auth"."ci" (provider = icu, locale = 'und', rules = '&a < g', version = '2.36')
CREATE COLLATION IF NOT EXISTS "auth"."ci" FROM "C"`
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

// TestObjectForms_StatisticsOverExpressions renders both statistics forms.
// The single-expression form is separate because Postgres's grammar separates
// them, and mixing them is refused rather than rendered.
func TestObjectForms_StatisticsOverExpressions(t *testing.T) {
	got := buildAll(t,
		pg.CreateStatisticsExpr(obStats, tUsers, sqlb.Lower(tEmail)),
		pg.CreateStatistics(obStats, tUsers, tUserID, tTenantID).Expression(sqlb.Lower(tEmail)).Kinds(pg.NDistinct),
	)
	want := `CREATE STATISTICS "auth"."users_stats" ON (lower("email")) FROM "auth"."users"
CREATE STATISTICS "auth"."users_stats" (ndistinct) ON "id", "tenant_id", (lower("email")) FROM "auth"."users"`
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	if _, err := pg.CreateStatisticsExpr(obStats, tUsers, sqlb.Lower(tEmail)).Kinds(pg.NDistinct).Build(); !errors.Is(err, pg.ErrSingleExpression) {
		t.Errorf("a kind on the single-expression form: %v", err)
	}
}

// TestObjectForms_SequenceDeclaration renders the CREATE SEQUENCE options that
// were missing and the two ALTER SEQUENCE bounds.
func TestObjectForms_SequenceDeclaration(t *testing.T) {
	got := buildAll(t,
		pg.CreateSequence(obSeq).IncrementBy(5).MinValue(10).MaxValue(50).Start(10).Cache(1).Cycle().OwnedBy(tUserID),
		pg.CreateSequence(obSeq).NoMinValue().NoMaxValue().NoCycle().OwnedByNone(),
		pg.CreateSequence(obSeq).Unlogged(),
		pg.CreateSequence(obSeq).Temporary(),
		pg.AlterSequence(obSeq).NoMinValue(),
		pg.AlterSequence(obSeq).NoMaxValue(),
	)
	want := `CREATE SEQUENCE "auth"."user_seq" AS bigint INCREMENT BY 5 MINVALUE 10 MAXVALUE 50 START WITH 10 CACHE 1 CYCLE OWNED BY "auth"."users"."id"
CREATE SEQUENCE "auth"."user_seq" AS bigint NO MINVALUE NO MAXVALUE NO CYCLE OWNED BY NONE
CREATE UNLOGGED SEQUENCE "auth"."user_seq" AS bigint
CREATE TEMPORARY SEQUENCE "auth"."user_seq" AS bigint
ALTER SEQUENCE "auth"."user_seq" NO MINVALUE
ALTER SEQUENCE "auth"."user_seq" NO MAXVALUE`
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

// TestObjectForms_PolicyChangesInOneStatement pins the shape that matters:
// three changes render as one statement, and a rename cannot join them.
func TestObjectForms_PolicyChangesInOneStatement(t *testing.T) {
	p := pg.Policy{Name: "tenant_isolation", Table: tUsers}
	got := build(t)(pg.AlterPolicy(p).
		To(obRole, pg.RolePublic).
		Using(sqlb.Eq(tTenantID, 1)).
		WithCheck(sqlb.Eq(tTenantID, 1)).Build())
	want := `ALTER POLICY "tenant_isolation" ON "auth"."users" TO "app", PUBLIC USING ("tenant_id" = 1) WITH CHECK ("tenant_id" = 1)`
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	if _, err := pg.AlterPolicy(p).RenameTo(pg.Policy{Name: "x", Table: tUsers}).Using(sqlb.Eq(tTenantID, 1)).Build(); !errors.Is(err, pg.ErrRenamePolicyAlone) {
		t.Errorf("a rename beside a change: %v", err)
	}
	if _, err := (&pg.AlterPolicyStatement{}).Build(); !errors.Is(err, ddl.ErrZeroValue) {
		t.Errorf("an ALTER POLICY changing nothing: %v", err)
	}
}

// TestObjectForms_DropKeywords renders CASCADE and RESTRICT on the drops that
// had one or neither.
func TestObjectForms_DropKeywords(t *testing.T) {
	tr := pg.Trigger{Name: "touch", Table: tUsers}
	p := pg.Policy{Name: "iso", Table: tUsers}
	got := buildAll(t,
		pg.DropView(pg.View{Name: "v"}).Restrict(),
		pg.DropSequence(obSeq).Cascade(),
		pg.DropTrigger(tr).IfExists().Cascade(),
		pg.DropTrigger(tr).Restrict(),
		pg.DropPolicy(p).IfExists().Cascade(),
		pg.DropPolicy(p).Restrict(),
	)
	want := `DROP VIEW "v" RESTRICT
DROP SEQUENCE "auth"."user_seq" CASCADE
DROP TRIGGER IF EXISTS "touch" ON "auth"."users" CASCADE
DROP TRIGGER "touch" ON "auth"."users" RESTRICT
DROP POLICY IF EXISTS "iso" ON "auth"."users" CASCADE
DROP POLICY "iso" ON "auth"."users" RESTRICT`
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

// TestObjectForms_TypeRenamesAndProperties renders the ALTER TYPE forms the
// range and base types had none of, and the properties a base type can only
// gain after it exists.
func TestObjectForms_TypeRenamesAndProperties(t *testing.T) {
	rt := pg.RangeType[int64]{Schema: "auth", Name: "span"}
	bt := pg.BaseType[int64]{Schema: "auth", Name: "myint"}
	ct := pg.Composite[string]{Schema: "auth", Name: "row"}
	got := buildAll(t,
		pg.AlterRangeType(rt).RenameTo(pg.RangeType[int64]{Schema: "auth", Name: "span2"}),
		pg.AlterBaseType(bt).RenameTo(pg.BaseType[int64]{Schema: "auth", Name: "myint2"}),
		pg.AlterComposite(ct).RenameTo(pg.Composite[string]{Schema: "auth", Name: "row2"}),
		pg.AlterBaseType(bt).SetProperties().
			Receive(pg.Function{Name: "myint_recv"}).
			Send(pg.Function{Name: "myint_send"}).
			TypmodIn(pg.Function{Name: "ti"}).
			TypmodOut(pg.Function{Name: "to"}).
			Analyze(pg.Function{Name: "an"}).
			Subscript(pg.Function{Name: "sub"}).
			Storage(pg.StorageExtended),
	)
	want := `ALTER TYPE "auth"."span" RENAME TO "span2"
ALTER TYPE "auth"."myint" RENAME TO "myint2"
ALTER TYPE "auth"."row" RENAME TO "row2"
ALTER TYPE "auth"."myint" SET (RECEIVE = "myint_recv", SEND = "myint_send", TYPMOD_IN = "ti", TYPMOD_OUT = "to", ANALYZE = "an", SUBSCRIPT = "sub", STORAGE = extended)`
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

// TestObjectForms_CompositeAttributeCascade renders the CASCADE that is the
// only way an attribute change reaches a typed table built on the type.
func TestObjectForms_CompositeAttributeCascade(t *testing.T) {
	c := pg.Composite[string]{Schema: "auth", Name: "row"}
	got := build(t)(pg.AlterComposite(c).DropAttribute("old").Cascade().Build())
	if want := `ALTER TYPE "auth"."row" DROP ATTRIBUTE IF EXISTS "old" CASCADE`; got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}
