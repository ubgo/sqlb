package pg_test

import (
	"errors"
	"testing"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/pg"
)

// Constraints declared once, as a library would.
var (
	usersEmailKey  = pg.TableConstraint{Table: tUsers, Name: "users_email_key"}
	sessionsUserFK = pg.TableConstraint{Table: tSessions, Name: "sessions_user_fk"}
	sessionsOwner  = pg.TableConstraint{Table: tSessions, Name: "sessions_owner_fk"}
)

// TestTableConstraint_OneDeclarationEverywhere pins the point of the value:
// the same TableConstraint creates, validates, renames, comments on, targets
// and drops the constraint, and every statement renders its name.
func TestTableConstraint_OneDeclarationEverywhere(t *testing.T) {
	cases := map[string]struct {
		got  string
		want string
	}{
		"create": {build(t)(pg.CreateTable(tUsers).Column(ddl.Column(tUserID), ddl.Column(tEmail)).Clause(pg.Unique(usersEmailKey, tEmail)).Build()),
			`CREATE TABLE "auth"."users" ("id" BIGINT NOT NULL, "email" TEXT NOT NULL, CONSTRAINT "users_email_key" UNIQUE ("email"))`},
		"validate": {build(t)(pg.AlterTable(tSessions).Clause(pg.ValidateConstraint(sessionsUserFK)).Build()),
			`ALTER TABLE "auth"."sessions" VALIDATE CONSTRAINT "sessions_user_fk"`},
		"rename": {build(t)(pg.AlterTable(tSessions).Clause(pg.RenameConstraint(sessionsUserFK, sessionsOwner)).Build()),
			`ALTER TABLE "auth"."sessions" RENAME CONSTRAINT "sessions_user_fk" TO "sessions_owner_fk"`},
		"drop": {build(t)(pg.AlterTable(tSessions).Clause(pg.DropConstraint(sessionsOwner).IfExists()).Build()),
			`ALTER TABLE "auth"."sessions" DROP CONSTRAINT IF EXISTS "sessions_owner_fk"`},
		"comment": {build(t)(pg.CommentOnConstraint(usersEmailKey).Is("one address per user").Build()),
			`COMMENT ON CONSTRAINT "users_email_key" ON "auth"."users" IS 'one address per user'`},
		"comment on statistics": {build(t)(pg.CommentOn(pg.Statistics{Schema: "auth", Name: "users_stats"}).Is("x").Build()),
			`COMMENT ON STATISTICS "auth"."users_stats" IS 'x'`},
		"guarded add": {build(t)(pg.AddConstraintStatement(pg.Unique(usersEmailKey, tEmail)).Build()),
			`ALTER TABLE "auth"."users" ADD CONSTRAINT "users_email_key" UNIQUE ("email")`},
	}
	for name, c := range cases {
		if c.got != c.want {
			t.Errorf("%s:\ngot  %s\nwant %s", name, c.got, c.want)
		}
	}
	q, err := pg.Insert(tUsers).Row(sqlb.Set(tEmail, "a")).Clause(pg.OnConstraint(usersEmailKey).DoNothing()).Build()
	if err != nil || q.SQL != `INSERT INTO "auth"."users" AS "u" ("email") VALUES ($1) ON CONFLICT ON CONSTRAINT "users_email_key" DO NOTHING` {
		t.Errorf("upsert target: %s, %v", q.SQL, err)
	}
}

// TestTableConstraint_RefusesAnotherTable pins the safety the value buys:
// a constraint of one table used in a statement about another fails Build,
// in CREATE TABLE and in every ALTER action that names one.
func TestTableConstraint_RefusesAnotherTable(t *testing.T) {
	for name, build := range map[string]func() (ddl.Script, error){
		"create unique":  pg.CreateTable(tSessions).Column(ddl.Column(tSessID)).Clause(pg.Unique(usersEmailKey, tSessID)).Build,
		"create check":   pg.CreateTable(tSessions).Column(ddl.Column(tSessID)).Clause(pg.Check(usersEmailKey, sqlb.Greater(tSessID, 0))).Build,
		"core check":     pg.CreateTable(tSessions).Column(ddl.Column(tSessID)).Constraint(ddl.Check(usersEmailKey, sqlb.Greater(tSessID, 0))).Build,
		"add constraint": pg.AlterTable(tSessions).Clause(pg.AddConstraint(pg.Check(usersEmailKey, sqlb.Greater(tSessID, 0)))).Build,
		"validate":       pg.AlterTable(tSessions).Clause(pg.ValidateConstraint(usersEmailKey)).Build,
		"drop":           pg.AlterTable(tSessions).Clause(pg.DropConstraint(usersEmailKey)).Build,
		"rename":         pg.AlterTable(tSessions).Clause(pg.RenameConstraint(usersEmailKey, pg.TableConstraint{Table: tUsers, Name: "x"})).Build,
	} {
		if _, err := build(); !errors.Is(err, ddl.ErrForeignColumn) {
			t.Errorf("%s: %v, want ErrForeignColumn", name, err)
		}
	}
	if _, err := pg.AlterTable(tSessions).Clause(pg.RenameConstraint(sessionsUserFK, usersEmailKey)).Build(); !errors.Is(err, pg.ErrRenameAcrossTables) {
		t.Errorf("rename to another table's constraint: %v", err)
	}
	for name, build := range map[string]func() (ddl.Script, error){
		"no name":   pg.AlterTable(tSessions).Clause(pg.DropConstraint(pg.TableConstraint{Table: tSessions})).Build,
		"no table":  pg.CreateTable(tUsers).Column(ddl.Column(tEmail)).Clause(pg.Unique(pg.TableConstraint{Name: "k"}, tEmail)).Build,
		"validate":  pg.AlterTable(tSessions).Clause(pg.ValidateConstraint(pg.TableConstraint{Table: tSessions})).Build,
		"rename to": pg.AlterTable(tSessions).Clause(pg.RenameConstraint(sessionsUserFK, pg.TableConstraint{Table: tSessions})).Build,
		"comment":   pg.CommentOnConstraint(pg.TableConstraint{}).Is("x").Build,
	} {
		if _, err := build(); err == nil {
			t.Errorf("%s: a half-declared constraint built", name)
		}
	}
	if _, err := pg.Insert(tUsers).Row(sqlb.Set(tEmail, "a")).Clause(pg.OnConstraint(pg.TableConstraint{}).DoNothing()).Build(); !errors.Is(err, ddl.ErrZeroValue) {
		t.Errorf("a zero upsert target: %v", err)
	}
}

// TestDomainConstraint_RefusesAnotherDomain pins the domain counterpart.
func TestDomainConstraint_RefusesAnotherDomain(t *testing.T) {
	email := pg.Domain[string]{Schema: "auth", Name: "email"}
	other := pg.Domain[string]{Schema: "auth", Name: "handle"}
	emailAt := pg.DomainConstraint[string]{Domain: email, Name: "email_at"}
	got := build(t)(pg.AlterDomain(email).DropConstraint(emailAt).Build())
	if want := `ALTER DOMAIN "auth"."email" DROP CONSTRAINT IF EXISTS "email_at"`; got != want {
		t.Errorf("got %s", got)
	}
	for name, build := range map[string]func() (ddl.Script, error){
		"create": pg.CreateDomain(other, ddl.RawType[string]("text")).Check(emailAt, sqlb.IsNotNull(pg.DomainValue[string]())).Build,
		"add":    pg.AlterDomain(other).AddConstraint(emailAt, sqlb.IsNotNull(pg.DomainValue[string]())).Build,
		"valid":  pg.AlterDomain(other).ValidateConstraint(emailAt).Build,
		"drop":   pg.AlterDomain(other).DropConstraint(emailAt).Build,
	} {
		if _, err := build(); !errors.Is(err, pg.ErrForeignDomain) {
			t.Errorf("%s: %v, want ErrForeignDomain", name, err)
		}
	}
	if _, err := pg.CreateDomain(email, ddl.RawType[string]("text")).Check(pg.DomainConstraint[string]{Domain: email}, sqlb.IsNotNull(pg.DomainValue[string]())).Build(); !errors.Is(err, ddl.ErrZeroValue) {
		t.Errorf("an unnamed domain check: %v", err)
	}
}
