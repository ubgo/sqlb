package pg_test

import (
	"errors"
	"testing"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/pg"
)

// TestTier5_Renders pins the forms built after the four tiers.
func TestTier5_Renders(t *testing.T) {
	seq := pg.Sequence[int64]{Schema: "auth", Name: "ids"}
	view := pg.View{Schema: "auth", Name: "active_users"}
	money := pg.Composite[string]{Schema: "billing", Name: "money"}
	amount := pg.Attr("amount", ddl.RawType[string]("numeric"))
	cases := map[string]struct {
		got  string
		want string
	}{
		"sequence options": {build(t)(pg.AlterSequence(seq).IncrementBy(5).MinValue(1).MaxValue(1000).Cache(10).Cycle().Build()),
			`ALTER SEQUENCE "auth"."ids" INCREMENT BY 5 MINVALUE 1 MAXVALUE 1000 CACHE 10 CYCLE`},
		"sequence restart and no cycle": {build(t)(pg.AlterSequence(seq).NoCycle().Restart(7).Build()),
			`ALTER SEQUENCE "auth"."ids" NO CYCLE RESTART WITH 7`},
		"sequence rename": {build(t)(pg.AlterSequence(seq).RenameTo(pg.Sequence[int64]{Name: "user_ids"}).Build()),
			`ALTER SEQUENCE "auth"."ids" RENAME TO "user_ids"`},
		"view default": {build(t)(pg.SetViewColumnDefault(view, tEmail, "none@example.com").Build()),
			`ALTER VIEW "auth"."active_users" ALTER COLUMN "email" SET DEFAULT 'none@example.com'`},
		"view drop default": {build(t)(pg.DropViewColumnDefault(view, tEmail).Build()),
			`ALTER VIEW "auth"."active_users" ALTER COLUMN "email" DROP DEFAULT`},
		"row security": {build(t)(pg.AlterTable(tUsers).Clause(pg.ForceRowLevelSecurity(), pg.NoForceRowLevelSecurity(), pg.DisableRowLevelSecurity()).Build()),
			`ALTER TABLE "auth"."users" FORCE ROW LEVEL SECURITY, NO FORCE ROW LEVEL SECURITY, DISABLE ROW LEVEL SECURITY`},
		"composite": {build(t)(pg.CreateComposite(money, amount, pg.Attr("currency", ddl.RawType[string]("text"))).Build()),
			`CREATE TYPE "billing"."money" AS ("amount" numeric, "currency" text)`},
		"add attribute": {build(t)(pg.AlterComposite(money).AddAttribute(pg.Attr("note", ddl.RawType[string]("text"))).Build()),
			`ALTER TYPE "billing"."money" ADD ATTRIBUTE "note" text`},
		"alter attribute type, a name with a space": {build(t)(pg.AlterComposite(money).AlterAttributeType(pg.Attr("due date", ddl.RawType[string]("date"))).Build()),
			`ALTER TYPE "billing"."money" ALTER ATTRIBUTE "due date" TYPE date`},
		"rename attribute": {build(t)(pg.AlterComposite(money).RenameAttribute("note", "memo").Build()),
			`ALTER TYPE "billing"."money" RENAME ATTRIBUTE "note" TO "memo"`},
		"drop attribute": {build(t)(pg.AlterComposite(money).DropAttribute("memo").Build()),
			`ALTER TYPE "billing"."money" DROP ATTRIBUTE IF EXISTS "memo"`},
		"drop composite": {build(t)(pg.DropComposite(money).IfExists().Cascade().Build()),
			`DROP TYPE IF EXISTS "billing"."money" CASCADE`},
		"composite as a column type": {build(t)(pg.CreateTable(tUsers).Column(ddl.Column(tEmail).As(money)).Build()),
			`CREATE TABLE "auth"."users" ("email" "billing"."money" NOT NULL)`},
		"guarded composite": {build(t)(pg.Guarded(pg.CreateComposite(money, amount)).Build()),
			`DO $sqlb$ BEGIN CREATE TYPE "billing"."money" AS ("amount" numeric); EXCEPTION WHEN duplicate_object THEN NULL; END $sqlb$`},
		"comment on a composite": {build(t)(pg.CommentOn(money).Is("an amount and its currency").Build()),
			`COMMENT ON TYPE "billing"."money" IS 'an amount and its currency'`},
	}
	for name, c := range cases {
		if c.got != c.want {
			t.Errorf("%s:\ngot  %s\nwant %s", name, c.got, c.want)
		}
	}
	for name, b := range map[string]func() (ddl.Script, error){
		"rename with an option": pg.AlterSequence(seq).RenameTo(pg.Sequence[int64]{Name: "x"}).IncrementBy(2).Build,
		"cache of zero":         pg.AlterSequence(seq).Cache(0).Build,
		"rename to no name":     pg.AlterSequence(seq).RenameTo(pg.Sequence[int64]{}).Build,
		"view column not a col": pg.SetViewColumnDefault(view, sqlb.Expr[string]{}, "x").Build,
		"an unnamed attribute":  pg.CreateComposite(money, pg.Attr("", ddl.RawType[string]("text"))).Build,
		"drop no attribute":     pg.AlterComposite(money).DropAttribute("").Build,
		"rename no attribute":   pg.AlterComposite(money).RenameAttribute("a", "").Build,
	} {
		if _, err := b(); err == nil {
			t.Errorf("%s: built", name)
		}
	}
	if _, err := pg.AlterSequence(seq).RenameTo(pg.Sequence[int64]{Name: "x"}).IncrementBy(2).Build(); !errors.Is(err, pg.ErrRenameAlone) {
		t.Errorf("rename with an option: %v", err)
	}
	if _, err := pg.AlterSequence(seq).Cache(0).Build(); !errors.Is(err, pg.ErrBadCache) {
		t.Errorf("cache of zero: %v", err)
	}
}
