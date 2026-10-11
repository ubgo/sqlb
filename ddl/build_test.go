package ddl_test

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/dialect/clickhouse"
	"github.com/ubgo/sqlb/dialect/postgres"
	"github.com/ubgo/sqlb/dialect/sqlite"
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/render"
	"github.com/ubgo/sqlb/schema"
)

type status string

var (
	users    = expr.Table{Name: "users", Alias: "u"}
	id       = expr.Col[int64](users, "id")
	email    = expr.Col[string](users, "email")
	nick     = expr.Col[*string](users, "nick")
	nickname = expr.Col[*string](users, "nickname")
	stat     = expr.Col[status](users, "status")
	created  = expr.Col[time.Time](users, "created_at")
	phone    = expr.Col[*string](users, "phone")
	profile  = expr.Col[json.RawMessage](users, "profile")

	sessions = expr.Table{Name: "sessions", Alias: "s"}
	sUserID  = expr.Col[int64](sessions, "user_id")
)

// one returns a checker for a Build that must produce exactly one statement,
// curried so a Build's two results pass straight through: one(t)(x.Build()).
func one(t *testing.T) func(ddl.Script, error) string {
	return func(s ddl.Script, err error) string {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		if len(s) != 1 {
			t.Fatalf("want one statement, got %d: %v", len(s), s.SQL())
		}
		return s[0].SQL
	}
}

// TestCreateTable_OneDeclarationBothEngines pins the core: the same column
// declarations, the same builder, and each engine's own spelling of every
// type, with nullability read off the Go type and every value a literal.
func TestCreateTable_OneDeclarationBothEngines(t *testing.T) {
	build := func(d render.Dialect) (ddl.Script, error) {
		c := ddl.NewCreateTable(d, users).IfNotExists().
			Column(ddl.Column(id), ddl.Column(email), ddl.Column(nick), ddl.Column(stat).Default("active")).
			Constraint(ddl.OneOf(ddl.TableConstraint{Table: stat.Table(), Name: "status_known"}, stat, "active", "banned"))
		return c.Build()
	}
	for _, tc := range []struct {
		name string
		d    render.Dialect
		want string
	}{
		{"postgres", postgres.Dialect{}, `CREATE TABLE IF NOT EXISTS "users" ("id" BIGINT NOT NULL, "email" TEXT NOT NULL, "nick" TEXT, "status" TEXT NOT NULL DEFAULT 'active', CONSTRAINT "status_known" CHECK ("status" IN ('active', 'banned')))`},
		{"sqlite", sqlite.Dialect{}, `CREATE TABLE IF NOT EXISTS "users" ("id" INTEGER NOT NULL, "email" TEXT NOT NULL, "nick" TEXT, "status" TEXT NOT NULL DEFAULT 'active', CONSTRAINT "status_known" CHECK ("status" IN ('active', 'banned')))`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := build(tc.d)
			if got := one(t)(s, err); got != tc.want {
				t.Fatalf("got  %s\nwant %s", got, tc.want)
			}
			if !s.Transactional() {
				t.Error("a CREATE TABLE is transactional on both engines")
			}
		})
	}
}

// TestColumn_TypesAndDefaults pins inference, the typed overrides, the
// parenthesised expression default and the NULL default.
func TestColumn_TypesAndDefaults(t *testing.T) {
	for _, tc := range []struct {
		name string
		col  ddl.ColumnSpec
		want string
	}{
		{"time.Time is a timestamp", ddl.Column(created), `"created_at" TIMESTAMPTZ NOT NULL`},
		{"a pointer is nullable", ddl.Column(nick), `"nick" TEXT`},
		{"NotNull overrides a pointer", ddl.Column(nick).NotNull(), `"nick" TEXT NOT NULL`},
		{"Nullable overrides a value", ddl.Column(email).Nullable(), `"email" TEXT`},
		{"a portable type", ddl.Column(profile).As(ddl.Portable[json.RawMessage](schema.TypeJSON)), `"profile" JSONB NOT NULL`},
		{"a raw type", ddl.Column(email).As(ddl.RawType[string]("citext")), `"email" citext NOT NULL`},
		{"an expression default is parenthesised", ddl.Column(created).DefaultExpr(expr.Literal[time.Time]("now()")), `"created_at" TIMESTAMPTZ NOT NULL DEFAULT (now())`},
		{"a NULL default", ddl.Column(nick).Default(nil), `"nick" TEXT DEFAULT NULL`},
		{"a backslash default takes the E form", ddl.Column(email).Default(`C:\tmp`), `"email" TEXT NOT NULL DEFAULT E'C:\\tmp'`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sql := one(t)(ddl.NewCreateTable(postgres.Dialect{}, users).Column(tc.col).Build())
			if want := `CREATE TABLE "users" (` + tc.want + `)`; sql != want {
				t.Fatalf("got  %s\nwant %s", sql, want)
			}
		})
	}
}

// TestCreateTable_Refusals pins every Build-time refusal, each with its own
// error, and that a refused statement carries no text.
func TestCreateTable_Refusals(t *testing.T) {
	for _, tc := range []struct {
		name  string
		build func() (ddl.Script, error)
		want  error
	}{
		{"a column from another table", func() (ddl.Script, error) {
			return ddl.NewCreateTable(postgres.Dialect{}, users).Column(ddl.Column(sUserID)).Build()
		}, ddl.ErrForeignColumn},
		{"a column twice", func() (ddl.Script, error) {
			return ddl.NewCreateTable(postgres.Dialect{}, users).Column(ddl.Column(id), ddl.Column(id)).Build()
		}, ddl.ErrDuplicateColumn},
		{"a named []byte with no type", func() (ddl.Script, error) {
			return ddl.NewCreateTable(postgres.Dialect{}, users).Column(ddl.Column(profile)).Build()
		}, ddl.ErrNoDefaultType},
		{"a portable type the Go type cannot hold", func() (ddl.Script, error) {
			return ddl.NewCreateTable(postgres.Dialect{}, users).Column(ddl.Column(id).As(ddl.Portable[int64](schema.TypeText))).Build()
		}, schema.ErrTypeMismatch},
		{"an empty OneOf", func() (ddl.Script, error) {
			return ddl.NewCreateTable(postgres.Dialect{}, users).Column(ddl.Column(stat)).Constraint(ddl.OneOf[status](ddl.TableConstraint{Table: users, Name: "x"}, stat)).Build()
		}, ddl.ErrEmptyOneOf},
		{"a time default on SQLite", func() (ddl.Script, error) {
			return ddl.NewCreateTable(sqlite.Dialect{}, users).Column(ddl.Column(created).Default(time.Now())).Build()
		}, render.ErrNotInlinable},
		{"ClickHouse, which writes no literals", func() (ddl.Script, error) {
			return ddl.NewCreateTable(clickhouse.Dialect{}, users).Column(ddl.Column(id)).Build()
		}, render.ErrNoLiteralQuoting},
		{"a column clause for a column the table does not define", func() (ddl.Script, error) {
			return ddl.NewCreateTable(postgres.Dialect{}, users).Column(ddl.Column(id)).
				Clause(ddl.NewColumnClause[postgres.Dialect](email, ddl.PartConstraint, func(*render.Args) string { return "UNIQUE" })).Build()
		}, ddl.ErrUnknownColumn},
		{"a column clause for another table's column", func() (ddl.Script, error) {
			return ddl.NewCreateTable(postgres.Dialect{}, users).Column(ddl.Column(id)).
				Clause(ddl.NewColumnClause[postgres.Dialect](sUserID, ddl.PartConstraint, func(*render.Args) string { return "UNIQUE" })).Build()
		}, ddl.ErrForeignColumn},
		{"a column clause for an expression that is not a column", func() (ddl.Script, error) {
			return ddl.NewCreateTable(postgres.Dialect{}, users).Column(ddl.Column(id)).
				Clause(ddl.NewColumnClause[postgres.Dialect](expr.Literal[int64]("1"), ddl.PartConstraint, func(*render.Args) string { return "UNIQUE" })).Build()
		}, ddl.ErrNotAColumn},
		{"a table clause at the column position with no column", func() (ddl.Script, error) {
			return ddl.NewCreateTable(postgres.Dialect{}, users).Column(ddl.Column(id)).
				Clause(ddl.NewTableClause[postgres.Dialect](ddl.PositionColumn, func(*render.Args) string { return "X" })).Build()
		}, ddl.ErrNoColumnTarget},
		{"a column clause at an unknown part", func() (ddl.Script, error) {
			return ddl.NewCreateTable(postgres.Dialect{}, users).Column(ddl.Column(id)).
				Clause(ddl.NewColumnClause[postgres.Dialect](id, ddl.ColumnPart(99), func(*render.Args) string { return "X" })).Build()
		}, ddl.ErrUnknownPosition},
		{"a table clause naming an undefined column", func() (ddl.Script, error) {
			return ddl.NewCreateTable(postgres.Dialect{}, users).Column(ddl.Column(id)).
				Clause(ddl.NewTableClauseOn[postgres.Dialect](ddl.PositionElement, []ddl.ColumnRef{id, email}, func(*render.Args) string { return "X" })).Build()
		}, ddl.ErrUnknownColumn},
		{"an option clause naming an undefined column", func() (ddl.Script, error) {
			return ddl.NewCreateTable(postgres.Dialect{}, users).Column(ddl.Column(id)).
				Clause(ddl.NewTableClauseOn[postgres.Dialect](ddl.PositionOption, []ddl.ColumnRef{email}, func(*render.Args) string { return "X" })).Build()
		}, ddl.ErrUnknownColumn},
		{"a default on a generated column", func() (ddl.Script, error) {
			return ddl.NewCreateTable(postgres.Dialect{}, users).Column(ddl.Column(email).Default("x")).
				Clause(ddl.NewColumnClause[postgres.Dialect](email, ddl.PartComputed, func(*render.Args) string { return "GENERATED" })).Build()
		}, ddl.ErrDefaultOnGenerated},
		{"a default on an identity column", func() (ddl.Script, error) {
			return ddl.NewCreateTable(postgres.Dialect{}, users).Column(ddl.Column(id).Default(1)).
				Clause(ddl.NewColumnClause[postgres.Dialect](id, ddl.PartValueSource, func(*render.Args) string { return "IDENTITY" })).Build()
		}, ddl.ErrDefaultOnGenerated},
		{"a clause at no position", func() (ddl.Script, error) {
			return ddl.NewCreateTable(postgres.Dialect{}, users).Column(ddl.Column(id)).
				Clause(ddl.NewTableClause[postgres.Dialect](ddl.Position(99), func(*render.Args) string { return "X" })).Build()
		}, ddl.ErrUnknownPosition},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := tc.build()
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
			if s != nil {
				t.Fatalf("a refused statement must carry no SQL, got %v", s.SQL())
			}
		})
	}
}

// TestCreateTable_ClausesRenderAtTheirPositions pins that an engine clause
// lands where its position says, and that every position is rendered.
func TestCreateTable_ClausesRenderAtTheirPositions(t *testing.T) {
	cl := func(p ddl.Position, s string) ddl.TableClause[postgres.Dialect] {
		return ddl.NewTableClause[postgres.Dialect](p, func(*render.Args) string { return s })
	}
	col := ddl.NewColumnClause[postgres.Dialect](id, ddl.PartConstraint, func(*render.Args) string { return "UNIQUE" })
	sql := one(t)(ddl.NewCreateTable(postgres.Dialect{}, users).Column(ddl.Column(id)).
		Clause(cl(ddl.PositionModifier, "UNLOGGED"), cl(ddl.PositionElement, `PRIMARY KEY ("id")`), cl(ddl.PositionOption, "WITH (fillfactor = 70)"), col).Build())
	if want := `CREATE UNLOGGED TABLE "users" ("id" BIGINT NOT NULL UNIQUE, PRIMARY KEY ("id")) WITH (fillfactor = 70)`; sql != want {
		t.Fatalf("got  %s\nwant %s", sql, want)
	}
	if len(ddl.PositionValues) != 4 {
		t.Fatalf("a new position must be rendered by CREATE TABLE and added to this test: %v", ddl.PositionValues)
	}
}

// TestRawClauses_RenderColumnsBare pins that a raw clause naming a column
// writes it bare at every CREATE TABLE position and in an ALTER action. The
// statement declares no alias, so "u"."email" there fails at the server; the
// raw SQLite recipe hit exactly that before the statement took the rule over
// from each clause.
func TestRawClauses_RenderColumnsBare(t *testing.T) {
	check := func(p ddl.Position) ddl.TableClause[postgres.Dialect] {
		return ddl.NewTableClause[postgres.Dialect](p, func(a *render.Args) string { return "X(" + email.SQL(a) + ")" })
	}
	sql := one(t)(ddl.NewCreateTable(postgres.Dialect{}, users).Column(ddl.Column(email)).
		Clause(check(ddl.PositionModifier), check(ddl.PositionElement), check(ddl.PositionOption)).Build())
	if want := `CREATE X("email") TABLE "users" ("email" TEXT NOT NULL, X("email")) X("email")`; sql != want {
		t.Fatalf("got  %s\nwant %s", sql, want)
	}
	action := ddl.NewAlterClause[postgres.Dialect](func(a *render.Args) string { return "ADD CHECK (" + email.SQL(a) + " <> '')" })
	sql = one(t)(ddl.NewAlterTable(postgres.Dialect{}, users).Clause(action).Build())
	if want := `ALTER TABLE "users" ADD CHECK ("email" <> '')`; sql != want {
		t.Fatalf("got  %s\nwant %s", sql, want)
	}
}

// TestAlterTable_GroupsWhereTheEngineCan pins the grouping. Postgres joins
// consecutive actions under one lock but never a RENAME, which is a top-level
// form in its grammar; SQLite takes one action per statement. Order holds.
func TestAlterTable_GroupsWhereTheEngineCan(t *testing.T) {
	actions := []ddl.Action{
		ddl.AddColumn(ddl.Column(phone)),
		ddl.DropColumn(email),
		ddl.RenameColumn(nick, nickname),
		ddl.AddColumn(ddl.Column(stat).Default("active")),
	}
	for _, tc := range []struct {
		name string
		d    render.Dialect
		want []string
	}{
		{"postgres", postgres.Dialect{}, []string{
			`ALTER TABLE "users" ADD COLUMN "phone" TEXT, DROP COLUMN "email"`,
			`ALTER TABLE "users" RENAME COLUMN "nick" TO "nickname"`,
			`ALTER TABLE "users" ADD COLUMN "status" TEXT NOT NULL DEFAULT 'active'`,
		}},
		{"sqlite", sqlite.Dialect{}, []string{
			`ALTER TABLE "users" ADD COLUMN "phone" TEXT`,
			`ALTER TABLE "users" DROP COLUMN "email"`,
			`ALTER TABLE "users" RENAME COLUMN "nick" TO "nickname"`,
			`ALTER TABLE "users" ADD COLUMN "status" TEXT NOT NULL DEFAULT 'active'`,
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := ddl.NewAlterTable(tc.d, users).Action(actions[0], actions[1:]...).Build()
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Join(s.SQL(), "\n"); got != strings.Join(tc.want, "\n") {
				t.Fatalf("got\n%s\nwant\n%s", got, strings.Join(tc.want, "\n"))
			}
		})
	}
}

// TestAlterTable_Refusals pins the table check on every action and on a
// rename's target.
func TestAlterTable_Refusals(t *testing.T) {
	sessionsNick := expr.Col[*string](sessions, "nick")
	for name, act := range map[string]ddl.Action{
		"adding another table's column":      ddl.AddColumn(ddl.Column(sUserID)),
		"dropping another table's column":    ddl.DropColumn(sUserID),
		"renaming to another table's column": ddl.RenameColumn(nick, sessionsNick),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ddl.NewAlterTable(postgres.Dialect{}, users).Action(act).Build(); !errors.Is(err, ddl.ErrForeignColumn) {
				t.Fatalf("got %v, want ErrForeignColumn", err)
			}
		})
	}
}

// TestAlterTable_EngineClausesStandAloneWhenTheySay pins the standalone flag
// for an engine action, the shape pg's RENAME TO and SET SCHEMA will take.
func TestAlterTable_EngineClausesStandAloneWhenTheySay(t *testing.T) {
	joinable := ddl.NewAlterClause[postgres.Dialect](func(*render.Args) string { return `ALTER COLUMN "email" SET NOT NULL` })
	alone := ddl.NewStandaloneAlterClause[postgres.Dialect](func(*render.Args) string { return `RENAME TO "people"` })
	s, err := ddl.NewAlterTable(postgres.Dialect{}, users).Action(ddl.AddColumn(ddl.Column(phone))).Clause(joinable, alone).Build()
	if err != nil {
		t.Fatal(err)
	}
	want := `ALTER TABLE "users" ADD COLUMN "phone" TEXT, ALTER COLUMN "email" SET NOT NULL` + "\n" + `ALTER TABLE "users" RENAME TO "people"`
	if got := strings.Join(s.SQL(), "\n"); got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

// TestDropTable_OneStatementPerTable pins the one-per-table rule, which is
// what SQLite accepts, and that an engine option follows every table.
func TestDropTable_OneStatementPerTable(t *testing.T) {
	cascade := ddl.NewDropClause[postgres.Dialect](func(*render.Args) string { return "CASCADE" })
	s, err := ddl.NewDropTable(postgres.Dialect{}, sessions, users).IfExists().Clause(cascade).Build()
	if err != nil {
		t.Fatal(err)
	}
	want := `DROP TABLE IF EXISTS "sessions" CASCADE` + "\n" + `DROP TABLE IF EXISTS "users" CASCADE`
	if got := strings.Join(s.SQL(), "\n"); got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

// TestCreateTable_SchemaQualifiedWithoutAlias pins that a DDL statement writes
// the table by its qualified name and never its alias, which does not exist in
// a statement's own definition.
func TestCreateTable_SchemaQualifiedWithoutAlias(t *testing.T) {
	authUsers := expr.Table{Schema: "auth", Name: "users", Alias: "u"}
	uid := expr.Col[int64](authUsers, "id")
	sql := one(t)(ddl.NewCreateTable(postgres.Dialect{}, authUsers).Column(ddl.Column(uid)).
		Constraint(ddl.Check(ddl.TableConstraint{Table: uid.Table(), Name: "id_positive"}, expr.Greater(uid, int64(0)))).Build())
	if want := `CREATE TABLE "auth"."users" ("id" BIGINT NOT NULL, CONSTRAINT "id_positive" CHECK ("id" > 0))`; sql != want {
		t.Fatalf("got  %s\nwant %s", sql, want)
	}
}

// TestColumnClauses_RenderInTheirPart pins where each part lands in a column
// definition: after the type, after the default, and that a computed column
// writes no NOT NULL. Every part in ColumnPartValues is covered.
func TestColumnClauses_RenderInTheirPart(t *testing.T) {
	cc := func(c ddl.ColumnRef, p ddl.ColumnPart, s string) ddl.TableClause[postgres.Dialect] {
		return ddl.NewColumnClause[postgres.Dialect](c, p, func(*render.Args) string { return s })
	}
	sql := one(t)(ddl.NewCreateTable(postgres.Dialect{}, users).
		Column(ddl.Column(id), ddl.Column(email).Default("x"), ddl.Column(stat)).
		Clause(cc(id, ddl.PartValueSource, "IDENTITY"), cc(email, ddl.PartAfterType, "COLLATE C"), cc(email, ddl.PartConstraint, "UNIQUE"),
			cc(id, ddl.PartAfterNotNull, "ON CONFLICT REPLACE"),
			cc(stat, ddl.PartComputed, "GENERATED")).Build())
	want := `CREATE TABLE "users" ("id" BIGINT NOT NULL ON CONFLICT REPLACE IDENTITY, "email" TEXT COLLATE C NOT NULL DEFAULT 'x' UNIQUE, "status" TEXT GENERATED)`
	if sql != want {
		t.Fatalf("got  %s\nwant %s", sql, want)
	}
	if len(ddl.ColumnPartValues) != 5 {
		t.Fatalf("a new column part must be rendered and added to this test: %v", ddl.ColumnPartValues)
	}
}

// TestPartAfterNotNull_NeedsANotNull pins the refusal that makes the part
// honest: the clause modifies a NOT NULL, so a definition that writes none —
// a nullable column, or a generated one, where the expression decides
// nullability — has nothing for it to attach to. Rendering it anyway would put
// a bare ON CONFLICT where the engine reads it as a constraint of its own.
func TestPartAfterNotNull_NeedsANotNull(t *testing.T) {
	cc := func(c ddl.ColumnRef, p ddl.ColumnPart, s string) ddl.TableClause[postgres.Dialect] {
		return ddl.NewColumnClause[postgres.Dialect](c, p, func(*render.Args) string { return s })
	}
	// The control: the same clause on a NOT NULL column builds.
	if _, err := ddl.NewCreateTable(postgres.Dialect{}, users).Column(ddl.Column(id)).
		Clause(cc(id, ddl.PartAfterNotNull, "ON CONFLICT ABORT")).Build(); err != nil {
		t.Fatalf("a clause on a NOT NULL column must build: %v", err)
	}
	cases := map[string]struct {
		col      ddl.ColumnRef
		computed bool
	}{
		"a nullable column":  {email, false},
		"a generated column": {stat, true},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			b := ddl.NewCreateTable(postgres.Dialect{}, users).
				Column(ddl.Column(id), ddl.Column(email).Nullable(), ddl.Column(stat)).
				Clause(cc(c.col, ddl.PartAfterNotNull, "ON CONFLICT ABORT"))
			if c.computed {
				b = b.Clause(cc(stat, ddl.PartComputed, "GENERATED"))
			}
			if _, err := b.Build(); !errors.Is(err, ddl.ErrNoNotNull) {
				t.Fatalf("want ErrNoNotNull, got %v", err)
			}
		})
	}
}

// TestColumnClauses_RenderBare pins that a column clause renders its columns
// without the table's alias, as a generated expression must.
func TestColumnClauses_RenderBare(t *testing.T) {
	gen := ddl.NewColumnClause[postgres.Dialect](nick, ddl.PartComputed, func(a *render.Args) string {
		return "GENERATED ALWAYS AS (" + email.SQL(a) + ") STORED"
	})
	sql := one(t)(ddl.NewCreateTable(postgres.Dialect{}, users).Column(ddl.Column(email), ddl.Column(nick)).Clause(gen).Build())
	if !strings.Contains(sql, `GENERATED ALWAYS AS ("email") STORED`) {
		t.Fatalf("the generated expression was not bare: %s", sql)
	}
}

// TestAlterTable_KeepsTheOrderActionsWereAdded pins that engine and universal
// actions run in the order written. They were once rendered universal first,
// so an engine action added before a universal one ran after it.
func TestAlterTable_KeepsTheOrderActionsWereAdded(t *testing.T) {
	x := ddl.NewAlterClause[sqlite.Dialect](func(*render.Args) string { return "X" })
	s, err := ddl.NewAlterTable(sqlite.Dialect{}, users).Clause(x).Action(ddl.DropColumn(email)).Clause(x).Build()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{`ALTER TABLE "users" X`, `ALTER TABLE "users" DROP COLUMN "email"`, `ALTER TABLE "users" X`}
	if got := s.SQL(); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// onTable is an engine action that says which table it belongs to.
type onTable struct {
	ddl.AlterClause[postgres.Dialect]
	t expr.Table
}

func (o onTable) ClauseTable() expr.Table { return o.t }

// TestAlterTable_RefusesAnEngineActionOnAnotherTable pins the table check for
// engine actions, by TableNamer and by ColumnNamer.
func TestAlterTable_RefusesAnEngineActionOnAnotherTable(t *testing.T) {
	x := ddl.NewAlterClause[postgres.Dialect](func(*render.Args) string { return "X" })
	if _, err := ddl.NewAlterTable(postgres.Dialect{}, users).Clause(onTable{x, sessions}).Build(); !errors.Is(err, ddl.ErrForeignColumn) {
		t.Errorf("TableNamer: got %v", err)
	}
	if _, err := ddl.NewAlterTable(postgres.Dialect{}, users).Clause(onTable{x, users}).Build(); err != nil {
		t.Errorf("TableNamer on its own table: %v", err)
	}
}

// TestWriteColumnWith_OnlyTheColumnBeingAdded pins that an added column's
// clauses must name it.
func TestWriteColumnWith_OnlyTheColumnBeingAdded(t *testing.T) {
	var b strings.Builder
	a := render.NewInlineArgs(postgres.Dialect{})
	c := ddl.NewColumnClause[postgres.Dialect](email, ddl.PartAfterType, func(*render.Args) string { return `COLLATE "C"` })
	ddl.WriteColumnWith(&b, a, ddl.Column(email), []ddl.ColumnClause[postgres.Dialect]{c})
	if a.Err() != nil || b.String() != `"email" TEXT COLLATE "C" NOT NULL` {
		t.Fatalf("got %q, %v", b.String(), a.Err())
	}
	a = render.NewInlineArgs(postgres.Dialect{})
	ddl.WriteColumnWith(&b, a, ddl.Column(id), []ddl.ColumnClause[postgres.Dialect]{c})
	if !errors.Is(a.Err(), ddl.ErrUnknownColumn) {
		t.Fatalf("a clause for another column: %v", a.Err())
	}
}

// TestAddColumn_RefusesWhatSQLiteCannotAdd pins the restricted ADD COLUMN: each
// case is refused under SQLite with the rule named, and all of them are
// accepted under Postgres, which implements render.UnrestrictedAddColumn.
func TestAddColumn_RefusesWhatSQLiteCannotAdd(t *testing.T) {
	uniq := ddl.NewColumnClause[sqlite.Dialect](email, ddl.PartConstraint, func(*render.Args) string { return "UNIQUE" })
	for _, tc := range []struct {
		name string
		lite func() (ddl.Script, error)
		pg   func() (ddl.Script, error)
		want error
	}{
		{"NOT NULL with no default",
			func() (ddl.Script, error) {
				return ddl.NewAlterTable(sqlite.Dialect{}, users).Action(ddl.AddColumn(ddl.Column(email))).Build()
			},
			func() (ddl.Script, error) {
				return ddl.NewAlterTable(postgres.Dialect{}, users).Action(ddl.AddColumn(ddl.Column(email))).Build()
			}, ddl.ErrAddNotNull},
		{"an expression default",
			func() (ddl.Script, error) {
				return ddl.NewAlterTable(sqlite.Dialect{}, users).Action(ddl.AddColumn(ddl.Column(created).DefaultExpr(expr.Literal[time.Time]("CURRENT_TIMESTAMP")))).Build()
			},
			func() (ddl.Script, error) {
				return ddl.NewAlterTable(postgres.Dialect{}, users).Action(ddl.AddColumn(ddl.Column(created).DefaultExpr(expr.Literal[time.Time]("now()")))).Build()
			}, ddl.ErrAddNonConstantDefault},
		{"a key constraint",
			func() (ddl.Script, error) {
				return ddl.NewAlterTable(sqlite.Dialect{}, users).Clause(ddl.NewAlterClause[sqlite.Dialect](func(a *render.Args) string {
					var b strings.Builder
					ddl.WriteAddColumn(&b, a, ddl.Column(email).Default("x"), []ddl.ColumnClause[sqlite.Dialect]{uniq})
					return b.String()
				})).Build()
			},
			nil, ddl.ErrAddConstraint},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tc.lite(); !errors.Is(err, tc.want) {
				t.Errorf("SQLite: got %v, want %v", err, tc.want)
			}
			if tc.pg != nil {
				if _, err := tc.pg(); err != nil {
					t.Errorf("Postgres refused it: %v", err)
				}
			}
		})
	}
	for name, c := range map[string]ddl.ColumnSpec{
		"a nullable column":              ddl.Column(nick),
		"a literal default":              ddl.Column(email).Default("x"),
		"NotNull overridden to nullable": ddl.Column(email).Nullable(),
	} {
		if _, err := ddl.NewAlterTable(sqlite.Dialect{}, users).Action(ddl.AddColumn(c)).Build(); err != nil {
			t.Errorf("%s was refused: %v", name, err)
		}
	}
}

// listed is an option that belongs to a comma-separated list.
type listed struct {
	ddl.TableClause[sqlite.Dialect]
}

func (listed) ListedOption() {}

// TestCreateTable_ListedOptionsJoinWithCommas pins SQLite's option list.
func TestCreateTable_ListedOptionsJoinWithCommas(t *testing.T) {
	opt := func(s string) ddl.TableClause[sqlite.Dialect] {
		return ddl.NewTableClause[sqlite.Dialect](ddl.PositionOption, func(*render.Args) string { return s })
	}
	got := one(t)(ddl.NewCreateTable(sqlite.Dialect{}, users).Column(ddl.Column(id)).
		Clause(listed{opt("STRICT")}, listed{opt("WITHOUT ROWID")}, opt("X")).Build())
	if want := `CREATE TABLE "users" ("id" INTEGER NOT NULL) STRICT, WITHOUT ROWID X`; got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

// jsonValuer is a driver.Valuer, as pg.JSON[T] is, for the default path that
// asks a value for its JSON.
type jsonValuer struct{ v string }

func (j jsonValuer) Value() (driver.Value, error) { return j.v, nil }

// TestDefault_JSONColumn pins how a JSON column's default is written: as the
// JSON text in the expression form ('...'), which MySQL requires of a JSON
// column and Postgres and SQLite accept; whatever order As and Default come
// in; from a raw message, a Valuer or a marshalled value; and refused when it
// is not JSON. A byte column that is not JSON keeps its byte literal.
func TestDefault_JSONColumn(t *testing.T) {
	js := ddl.Portable[json.RawMessage](schema.TypeJSON)
	d := postgres.Dialect{}
	render := func(c ddl.ColumnSpec) (string, error) {
		s, err := ddl.NewCreateTable(d, users).Column(c).Build()
		if err != nil {
			return "", err
		}
		return s[0].SQL, nil
	}
	for name, c := range map[string]struct {
		spec ddl.ColumnSpec
		want string
	}{
		"type then default": {ddl.Column(profile).As(js).Default(json.RawMessage(`{"a":1}`)), `"profile" JSONB NOT NULL DEFAULT ('{"a":1}')`},
		"default then type": {ddl.Column(profile).Default(json.RawMessage(`[]`)).As(js), `"profile" JSONB NOT NULL DEFAULT ('[]')`},
		"a quote inside":    {ddl.Column(profile).As(js).Default(json.RawMessage(`{"a":"it's"}`)), `"profile" JSONB NOT NULL DEFAULT ('{"a":"it''s"}')`},
		"a valuer": {ddl.Column(expr.Col[jsonValuer](users, "v")).As(ddl.RawJSONType[jsonValuer]("jsonb")).Default(jsonValuer{`{"k":true}`}),
			`"v" jsonb NOT NULL DEFAULT ('{"k":true}')`},
		"marshalled": {ddl.Column(expr.Col[map[string]int](users, "m")).As(ddl.RawJSONType[map[string]int]("jsonb")).Default(map[string]int{"n": 2}),
			`"m" jsonb NOT NULL DEFAULT ('{"n":2}')`},
	} {
		got, err := render(c.spec)
		if err != nil || !strings.Contains(got, c.want) {
			t.Errorf("%s: got %s, %v; want it to contain %s", name, got, err, c.want)
		}
	}
	if _, err := render(ddl.Column(profile).As(js).Default(json.RawMessage(`{bad`))); !errors.Is(err, ddl.ErrInvalidJSON) {
		t.Errorf("invalid JSON: %v", err)
	}
	if _, err := render(ddl.Column(expr.Col[jsonValuer](users, "v")).As(ddl.RawJSONType[jsonValuer]("jsonb")).Default(jsonValuer{`nope`})); !errors.Is(err, ddl.ErrInvalidJSON) {
		t.Errorf("a valuer returning non-JSON: %v", err)
	}
	if _, err := render(ddl.Column(expr.Col[func()](users, "f")).As(ddl.RawJSONType[func()]("jsonb")).Default(func() {})); !errors.Is(err, ddl.ErrInvalidJSON) {
		t.Errorf("a value json.Marshal refuses: %v", err)
	}
	blob := expr.Col[[]byte](users, "blob")
	got, err := render(ddl.Column(blob).Default([]byte("ab")))
	if err != nil || strings.Contains(got, `DEFAULT ('`) {
		t.Errorf("a byte column that is not JSON must keep its byte literal: %s, %v", got, err)
	}
}
