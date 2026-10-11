package pg_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/pg"
)

var (
	tUsers     = sqlb.Table{Name: "users", Schema: "auth", Alias: "u"}
	tUserID    = sqlb.Col[int64](tUsers, "id")
	tTenantID  = sqlb.Col[int64](tUsers, "tenant_id")
	tEmail     = sqlb.Col[string](tUsers, "email")
	tEmailLow  = sqlb.Col[string](tUsers, "email_lower")
	tNick      = sqlb.Col[*string](tUsers, "nick")
	tSessions  = sqlb.Table{Name: "sessions", Schema: "auth", Alias: "s"}
	tSessID    = sqlb.Col[int64](tSessions, "id")
	tSessUser  = sqlb.Col[int64](tSessions, "user_id")
	tSessTen   = sqlb.Col[int64](tSessions, "tenant_id")
	tOtherID   = sqlb.Col[int64](sqlb.Table{Name: "other"}, "id")
	tOtherText = sqlb.Col[int64](sqlb.Table{Name: "else"}, "id")
)

// build returns a checker for a Build that must succeed, curried so the two
// results pass straight through: build(t)(x.Build()).
func build(t *testing.T) func(ddl.Script, error) string {
	return func(s ddl.Script, err error) string {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return strings.Join(s.SQL(), ";\n")
	}
}

// TestTable_KeysIdentityAndGenerated renders the spec's users table: an
// identity key, a generated column, a collation, a named unique constraint
// with NULLS NOT DISTINCT and a check, every column named once in Go.
func TestTable_KeysIdentityAndGenerated(t *testing.T) {
	got := build(t)(pg.CreateTable(tUsers).IfNotExists().
		Column(ddl.Column(tUserID), ddl.Column(tEmail), ddl.Column(tEmailLow), ddl.Column(tNick)).
		Clause(
			pg.Identity(tUserID, pg.Always),
			pg.ColumnCollate(tEmail, "C"),
			pg.GeneratedStored(tEmailLow, sqlb.Lower(tEmail)),
			pg.PrimaryKey(tUserID),
			pg.Unique(pg.TableConstraint{Table: tEmailLow.Table(), Name: "users_email_lower_key"}, tEmailLow).NullsNotDistinct(),
			pg.Check(pg.TableConstraint{Table: tNick.Table(), Name: "users_nick_len"}, sqlb.LessEq(pg.Length(sqlb.AssertNotNull(tNick)), 80)),
		).Build())
	want := `CREATE TABLE IF NOT EXISTS "auth"."users" (` +
		`"id" BIGINT NOT NULL GENERATED ALWAYS AS IDENTITY, ` +
		`"email" TEXT COLLATE "C" NOT NULL, ` +
		`"email_lower" TEXT GENERATED ALWAYS AS (lower("email")) STORED, ` +
		`"nick" TEXT, ` +
		`PRIMARY KEY ("id"), ` +
		`CONSTRAINT "users_email_lower_key" UNIQUE NULLS NOT DISTINCT ("email_lower"), ` +
		`CONSTRAINT "users_nick_len" CHECK (length("nick") <= 80))`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

// TestTable_ForeignKeys renders the single and composite forms with every
// option, and the named primary key.
func TestTable_ForeignKeys(t *testing.T) {
	got := build(t)(pg.CreateTable(tSessions).
		Column(ddl.Column(tSessID), ddl.Column(tSessUser), ddl.Column(tSessTen)).
		Clause(
			pg.PrimaryKey(tSessID).Named(pg.TableConstraint{Table: tSessID.Table(), Name: "sessions_pk"}),
			pg.References(tSessUser, tUserID).OnDelete(pg.RefCascade),
			pg.ForeignKey(pg.TableConstraint{Table: tSessTen.Table(), Name: "sessions_tenant_user_fk"}, pg.RefPair(tSessTen, tTenantID), pg.RefPair(tSessUser, tUserID)).
				OnDelete(pg.RefRestrict).OnUpdate(pg.RefCascade).Deferrable(pg.InitiallyDeferred),
		).Build())
	want := `CREATE TABLE "auth"."sessions" ("id" BIGINT NOT NULL, "user_id" BIGINT NOT NULL, "tenant_id" BIGINT NOT NULL, ` +
		`CONSTRAINT "sessions_pk" PRIMARY KEY ("id"), ` +
		`FOREIGN KEY ("user_id") REFERENCES "auth"."users" ("id") ON DELETE CASCADE, ` +
		`CONSTRAINT "sessions_tenant_user_fk" FOREIGN KEY ("tenant_id", "user_id") REFERENCES "auth"."users" ("tenant_id", "id") ` +
		`ON DELETE RESTRICT ON UPDATE CASCADE DEFERRABLE INITIALLY DEFERRED)`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

// TestTable_EveryClosedSetValueRenders walks each closed set, so a constant
// added without a keyword fails here rather than at Build in someone's code.
func TestTable_EveryClosedSetValueRenders(t *testing.T) {
	for _, r := range []pg.RefAction{pg.RefNoAction, pg.RefRestrict, pg.RefCascade, pg.RefSetNull, pg.RefSetDefault} {
		if _, err := pg.CreateTable(tSessions).Column(ddl.Column(tSessUser)).Clause(pg.References(tSessUser, tUserID).OnDelete(r)).Build(); err != nil {
			t.Errorf("RefAction %d: %v", r, err)
		}
	}
	for _, d := range []pg.Deferral{pg.InitiallyImmediate, pg.InitiallyDeferred} {
		if _, err := pg.CreateTable(tSessions).Column(ddl.Column(tSessUser)).Clause(pg.References(tSessUser, tUserID).Deferrable(d)).Build(); err != nil {
			t.Errorf("Deferral %d: %v", d, err)
		}
	}
	for _, k := range []pg.IdentityKind{pg.Always, pg.ByDefault} {
		if _, err := pg.CreateTable(tSessions).Column(ddl.Column(tSessID)).Clause(pg.Identity(tSessID, k)).Build(); err != nil {
			t.Errorf("IdentityKind %d: %v", k, err)
		}
	}
}

// TestTable_Refusals pins what Build refuses, each before any SQL exists.
func TestTable_Refusals(t *testing.T) {
	for _, tc := range []struct {
		name  string
		build func() (ddl.Script, error)
		want  error
	}{
		{"a key on a column the table does not define", func() (ddl.Script, error) {
			return pg.CreateTable(tUsers).Column(ddl.Column(tUserID)).Clause(pg.PrimaryKey(tEmail)).Build()
		}, ddl.ErrUnknownColumn},
		{"a unique constraint on another table's column", func() (ddl.Script, error) {
			return pg.CreateTable(tUsers).Column(ddl.Column(tUserID)).Clause(pg.Unique(pg.TableConstraint{Table: tSessID.Table(), Name: "k"}, tSessID)).Build()
		}, ddl.ErrForeignColumn},
		{"a foreign key referencing two tables", func() (ddl.Script, error) {
			return pg.CreateTable(tSessions).Column(ddl.Column(tSessUser), ddl.Column(tSessTen)).
				Clause(pg.ForeignKey(pg.TableConstraint{Table: tSessUser.Table(), Name: "fk"}, pg.RefPair(tSessUser, tOtherID), pg.RefPair(tSessTen, tOtherText))).Build()
		}, pg.ErrMixedReferences},
		{"an identity with a default", func() (ddl.Script, error) {
			return pg.CreateTable(tUsers).Column(ddl.Column(tUserID).Default(1)).Clause(pg.Identity(tUserID, pg.Always)).Build()
		}, ddl.ErrDefaultOnGenerated},
		{"an identity kind outside the constants", func() (ddl.Script, error) {
			return pg.CreateTable(tUsers).Column(ddl.Column(tUserID)).Clause(pg.Identity(tUserID, pg.IdentityKind(0))).Build()
		}, pg.ErrUnknownKeyword},
		{"a referential action outside the constants", func() (ddl.Script, error) {
			return pg.CreateTable(tSessions).Column(ddl.Column(tSessUser)).Clause(pg.References(tSessUser, tUserID).OnDelete(pg.RefAction(42))).Build()
		}, pg.ErrUnknownKeyword},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := tc.build()
			if !errors.Is(err, tc.want) || s != nil {
				t.Fatalf("got %v %v, want %v", s, err, tc.want)
			}
		})
	}
}

// TestTable_UnloggedAndCascade renders the modifier and the drop option.
func TestTable_UnloggedAndCascade(t *testing.T) {
	if got := build(t)(pg.CreateTable(tUsers).Clause(pg.Unlogged()).Column(ddl.Column(tUserID)).Build()); got != `CREATE UNLOGGED TABLE "auth"."users" ("id" BIGINT NOT NULL)` {
		t.Fatal(got)
	}
	if got := build(t)(pg.DropTable(tSessions, tUsers).IfExists().Clause(pg.Cascade()).Build()); got != "DROP TABLE IF EXISTS \"auth\".\"sessions\" CASCADE;\nDROP TABLE IF EXISTS \"auth\".\"users\" CASCADE" {
		t.Fatal(got)
	}
}
