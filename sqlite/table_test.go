package sqlite_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/schema"
	"github.com/ubgo/sqlb/sqlite"
)

type tStatus string

var (
	tUsers   = sqlb.Table{Name: "users", Alias: "u"}
	tID      = sqlb.Col[int64](tUsers, "id")
	tEmail   = sqlb.Col[string](tUsers, "email")
	tDomain  = sqlb.Col[string](tUsers, "email_domain")
	tNick    = sqlb.Col[*string](tUsers, "nick")
	tStat    = sqlb.Col[tStatus](tUsers, "status")
	tSess    = sqlb.Table{Name: "sessions", Alias: "s"}
	tSessID  = sqlb.Col[string](tSess, "id")
	tSessUID = sqlb.Col[int64](tSess, "user_id")
)

func built(t *testing.T) func(ddl.Script, error) string {
	return func(s ddl.Script, err error) string {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return strings.Join(s.SQL(), ";\n")
	}
}

// TestTable_TheSpecTables renders section 7.1.
func TestTable_TheSpecTables(t *testing.T) {
	got := built(t)(sqlite.CreateTable(tUsers).IfNotExists().
		Column(ddl.Column(tID), ddl.Column(tEmail), ddl.Column(tStat).Default("active"), ddl.Column(tNick), ddl.Column(tDomain)).
		Clause(
			sqlite.PrimaryKey(tID),
			sqlite.Unique(tEmail).Named(sqlite.TableConstraint{Table: tEmail.Table(), Name: "users_email_key"}),
			sqlite.ColumnCollate(tEmail, sqlite.NoCase),
			sqlite.GeneratedVirtual(tDomain, sqlite.Substr(tEmail, sqlb.Add(sqlite.Instr(tEmail, "@"), sqlb.Literal[int32]("1")))),
			sqlite.Strict(),
		).
		Constraint(ddl.OneOf[tStatus](ddl.TableConstraint{Table: tStat.Table(), Name: "users_status_known"}, tStat, "active", "banned"),
			ddl.Check(ddl.TableConstraint{Table: tNick.Table(), Name: "users_nick_len"}, sqlb.LessEq(sqlite.Length(sqlb.AssertNotNull(tNick)), 80))).Build())
	want := `CREATE TABLE IF NOT EXISTS "users" ("id" INTEGER NOT NULL, "email" TEXT COLLATE NOCASE NOT NULL, "status" TEXT NOT NULL DEFAULT 'active', "nick" TEXT, ` +
		`"email_domain" TEXT GENERATED ALWAYS AS (substr("email", (instr("email", '@') + 1))) VIRTUAL, ` +
		`CONSTRAINT "users_status_known" CHECK ("status" IN ('active', 'banned')), CONSTRAINT "users_nick_len" CHECK (length("nick") <= 80), ` +
		`PRIMARY KEY ("id"), CONSTRAINT "users_email_key" UNIQUE ("email")) STRICT`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}

	got = built(t)(sqlite.CreateTable(tSess).IfNotExists().
		Column(ddl.Column(tSessID), ddl.Column(tSessUID)).
		Clause(sqlite.PrimaryKey(tSessID).OnConflict(sqlite.OrIgnore), sqlite.References(tSessUID, tID).OnDelete(sqlite.RefCascade).OnUpdate(sqlite.RefRestrict).Deferred(),
			sqlite.Strict(), sqlite.WithoutRowID()).Build())
	want = `CREATE TABLE IF NOT EXISTS "sessions" ("id" TEXT NOT NULL, "user_id" INTEGER NOT NULL, PRIMARY KEY ("id") ON CONFLICT IGNORE, ` +
		`FOREIGN KEY ("user_id") REFERENCES "users" ("id") ON DELETE CASCADE ON UPDATE RESTRICT DEFERRABLE INITIALLY DEFERRED) STRICT, WITHOUT ROWID`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}

	audit := sqlb.Table{Name: "audit"}
	aid := sqlb.Col[int64](audit, "id")
	if got := built(t)(sqlite.CreateTable(audit).Column(ddl.Column(aid)).Clause(sqlite.AutoincrementKey(aid)).Build()); got != `CREATE TABLE "audit" ("id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT)` {
		t.Fatal(got)
	}
}

// TestAlter_SQLiteActionsAndRefusals renders section 7.2 and its refusals.
func TestAlter_SQLiteActionsAndRefusals(t *testing.T) {
	upper := sqlb.Col[string](tUsers, "email_upper")
	renamed := sqlb.Table{Name: "people"}
	got := built(t)(sqlite.AlterTable(tUsers).Clause(
		sqlite.AddColumn(ddl.Column(upper), sqlite.GeneratedVirtual(upper, sqlb.Upper(tEmail))),
		sqlite.AddColumn(ddl.Column(tNick), sqlite.ColumnCollate(tNick, sqlite.NoCase)),
		sqlite.RenameTo(renamed),
	).Build())
	want := `ALTER TABLE "users" ADD COLUMN "email_upper" TEXT GENERATED ALWAYS AS (upper("email")) VIRTUAL;
ALTER TABLE "users" ADD COLUMN "nick" TEXT COLLATE NOCASE;
ALTER TABLE "users" RENAME TO "people"`
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	for name, tc := range map[string]struct {
		c    sqlite.AlterClause
		want error
	}{
		"a stored generated column":   {sqlite.AddColumn(ddl.Column(upper), sqlite.GeneratedStored(upper, sqlb.Upper(tEmail))), sqlite.ErrAddStored},
		"NOT NULL without default":    {sqlite.AddColumn(ddl.Column(tEmail)), ddl.ErrAddNotNull},
		"a clause for another column": {sqlite.AddColumn(ddl.Column(tNick), sqlite.AutoincrementKey(tID)), ddl.ErrUnknownColumn},
		"a key on the added column":   {sqlite.AddColumn(ddl.Column(sqlb.Col[int64](tUsers, "n")), sqlite.AutoincrementKey(sqlb.Col[int64](tUsers, "n"))), ddl.ErrAddConstraint},
		"another table's column":      {sqlite.AddColumn(ddl.Column(tSessUID)), ddl.ErrForeignColumn},
	} {
		if _, err := sqlite.AlterTable(tUsers).Clause(tc.c).Build(); !errors.Is(err, tc.want) {
			t.Errorf("%s: got %v, want %v", name, err, tc.want)
		}
	}
}

// TestTable_SQLiteRefusals pins the closed sets and the column checks.
func TestTable_SQLiteRefusals(t *testing.T) {
	for name, tc := range map[string]struct {
		b    func() (ddl.Script, error)
		want error
	}{
		"a key on an undefined column": {func() (ddl.Script, error) {
			return sqlite.CreateTable(tUsers).Column(ddl.Column(tID)).Clause(sqlite.PrimaryKey(tEmail)).Build()
		}, ddl.ErrUnknownColumn},
		"a collation outside the constants": {func() (ddl.Script, error) {
			return sqlite.CreateTable(tUsers).Column(ddl.Column(tEmail)).Clause(sqlite.ColumnCollate(tEmail, sqlite.Collation(0))).Build()
		}, sqlite.ErrUnknownKeyword},
		"a referential action outside the constants": {func() (ddl.Script, error) {
			return sqlite.CreateTable(tSess).Column(ddl.Column(tSessUID)).Clause(sqlite.References(tSessUID, tID).OnDelete(sqlite.RefAction(9))).Build()
		}, sqlite.ErrUnknownKeyword},
		"a default on a generated column": {func() (ddl.Script, error) {
			return sqlite.CreateTable(tUsers).Column(ddl.Column(tEmail), ddl.Column(tDomain).Default("x")).Clause(sqlite.GeneratedVirtual(tDomain, tEmail)).Build()
		}, ddl.ErrDefaultOnGenerated},
	} {
		if _, err := tc.b(); !errors.Is(err, tc.want) {
			t.Errorf("%s: got %v, want %v", name, err, tc.want)
		}
	}
}

// TestIncompleteStatements_ModifiersDoNotWriteToTheirReceiver walks the SQLite
// DDL statements' incomplete stages, which have no Build and so are not in the
// engine walk.
func TestIncompleteStatements_ModifiersDoNotWriteToTheirReceiver(t *testing.T) {
	ix := sqlite.CreateIndex(sqlite.Index{Name: "i"}, tUsers)
	b := func(q *sqlite.CreateIndexQuery) string { return built(t)(q.On(sqlite.IndexKey(tEmail)).Build()) }
	before := b(ix)
	for name, m := range map[string]func(*sqlite.CreateIndexQuery) *sqlite.CreateIndexQuery{
		"Unique": (*sqlite.CreateIndexQuery).Unique, "IfNotExists": (*sqlite.CreateIndexQuery).IfNotExists,
	} {
		if d := m(ix); b(ix) != before || b(d) == before {
			t.Errorf("CreateIndexQuery.%s wrote to its receiver or changed nothing", name)
		}
	}
	v := sqlite.CreateView(sqlite.View{Name: "v"})
	body := sqlite.Select(tID).From(tUsers)
	before = built(t)(v.As(body).Build())
	if d := v.IfNotExists(); built(t)(v.As(body).Build()) != before || built(t)(d.As(body).Build()) == before {
		t.Error("CreateViewQuery.IfNotExists wrote to its receiver or changed nothing")
	}
	step := sqlite.StepDelete(sqlite.Delete(tSess).Where(sqlb.EqExpr(tSessUID, sqlite.Old(tID))))
	tr := sqlite.CreateTrigger(sqlite.Trigger{Name: "tr"})
	bt := func(q *sqlite.CreateTriggerQuery) string {
		return built(t)(q.After(sqlite.OnDelete(), tUsers).Do(step).Build())
	}
	before = bt(tr)
	if d := tr.IfNotExists(); bt(tr) != before || bt(d) == before {
		t.Error("CreateTriggerQuery.IfNotExists wrote to its receiver or changed nothing")
	}
	timed := tr.After(sqlite.OnDelete(), tUsers)
	before = built(t)(timed.Do(step).Build())
	if d := timed.When(sqlite.IsNot(sqlite.Old(tEmail), sqlite.New(tEmail))); built(t)(timed.Do(step).Build()) != before || built(t)(d.Do(step).Build()) == before {
		t.Error("TimedTriggerQuery.When wrote to its receiver or changed nothing")
	}
}

// TestObjects_SQLiteIndexesViewsTriggersPragmas renders section 7.4 and 7.5.
func TestObjects_SQLiteIndexesViewsTriggersPragmas(t *testing.T) {
	audit := sqlb.Table{Name: "audit", Alias: "a"}
	auditUser := sqlb.Col[int64](audit, "user_id")
	auditWhat := sqlb.Col[string](audit, "what")
	var out []string
	for _, b := range []interface{ Build() (ddl.Script, error) }{
		sqlite.CreateIndex(sqlite.Index{Name: "users_email"}, tUsers).Unique().IfNotExists().On(sqlite.IndexKey(tEmail).Collate(sqlite.NoCase)),
		sqlite.CreateIndex(sqlite.Index{Name: "users_lower"}, tUsers).On(sqlite.IndexExpr(sqlb.Lower(tEmail)), sqlite.IndexKey(tID).Desc()).Where(sqlb.IsNotNull(tNick)),
		sqlite.DropIndex(sqlite.Index{Name: "users_email"}).IfExists(),
		sqlite.CreateView(sqlite.View{Name: "active"}).IfNotExists().As(sqlite.Select(tID, tEmail).From(tUsers).Where(sqlb.Eq(tStat, "active"))),
		sqlite.DropView(sqlite.View{Name: "active"}).IfExists(),
		sqlite.CreateTrigger(sqlite.Trigger{Name: "users_audit"}).IfNotExists().After(sqlite.OnUpdateOf(tStat), tUsers).
			When(sqlite.IsNot(sqlite.Old(tStat), sqlite.New(tStat))).
			Do(sqlite.StepInsert(sqlite.Insert(audit).Row(sqlb.SetExpr(auditUser, sqlite.New(tID)), sqlb.Set(auditWhat, "status")))),
		sqlite.DropTrigger(sqlite.Trigger{Name: "users_audit"}).IfExists(),
		sqlite.SetUserVersion(3),
		sqlite.ForeignKeys(false),
	} {
		out = append(out, built(t)(b.Build()))
	}
	want := `CREATE UNIQUE INDEX IF NOT EXISTS "users_email" ON "users" ("email" COLLATE NOCASE)
CREATE INDEX "users_lower" ON "users" ((lower("email")), "id" DESC) WHERE "nick" IS NOT NULL
DROP INDEX IF EXISTS "users_email"
CREATE VIEW IF NOT EXISTS "active" AS SELECT "u"."id", "u"."email" FROM "users" AS "u" WHERE ("u"."status" = 'active')
DROP VIEW IF EXISTS "active"
CREATE TRIGGER IF NOT EXISTS "users_audit" AFTER UPDATE OF "status" ON "users" FOR EACH ROW WHEN (OLD."status" IS NOT NEW."status") BEGIN INSERT INTO "audit" ("user_id", "what") VALUES (NEW."id", 'status'); END
DROP TRIGGER IF EXISTS "users_audit"
PRAGMA user_version = 3
PRAGMA foreign_keys = OFF`
	if got := strings.Join(out, "\n"); got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	s, _ := sqlite.ForeignKeys(true).Build()
	if !s[0].OutsideTransaction {
		t.Error("PRAGMA foreign_keys must be marked OutsideTransaction")
	}
}

// TestFTS5AndRebuild_Render pins the text of an FTS5 table and a rebuild.
func TestFTS5AndRebuild_Render(t *testing.T) {
	fts := sqlb.Table{Name: "users_fts"}
	ftsEmail := sqlb.Col[string](fts, "email")
	got := built(t)(sqlite.CreateFTS5(fts).IfNotExists().Columns(ftsEmail).ExternalContent(tUsers, tID).
		Tokenize(sqlite.Porter(sqlite.Unicode61(2))).Prefix(2, 3).Build())
	want := `CREATE VIRTUAL TABLE IF NOT EXISTS "users_fts" USING fts5("email", content = 'users', content_rowid = 'id', tokenize = 'porter unicode61 remove_diacritics 2', prefix = '2 3')`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	next := sqlb.Table{Name: "users_new"}
	nid := sqlb.Col[int64](next, "id")
	nemail := sqlb.Col[string](next, "email")
	got = built(t)(sqlite.Rebuild(tUsers).Into(sqlite.CreateTable(next).Column(ddl.Column(nid), ddl.Column(nemail)).Clause(sqlite.PrimaryKey(nid))).
		Copy(sqlite.CopyAs(nid, tID), sqlite.CopyAs(nemail, sqlb.Lower(tEmail))).
		Recreate(sqlite.CreateIndex(sqlite.Index{Name: "users_email"}, tUsers).On(sqlite.IndexKey(tEmail))).Build())
	want = `PRAGMA foreign_keys = OFF;
CREATE TABLE "users_new" ("id" INTEGER NOT NULL, "email" TEXT NOT NULL, PRIMARY KEY ("id"));
INSERT INTO "users_new" ("id", "email") SELECT "u"."id", lower("u"."email") FROM "users" AS "u";
DROP TABLE "users";
ALTER TABLE "users_new" RENAME TO "users";
CREATE INDEX "users_email" ON "users" ("email");
PRAGMA foreign_key_check;
PRAGMA foreign_keys = ON`
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	for name, tc := range map[string]struct {
		b    func() (ddl.Script, error)
		want error
	}{
		"no copy": {func() (ddl.Script, error) {
			return sqlite.Rebuild(tUsers).Into(sqlite.CreateTable(next).Column(ddl.Column(nid))).Build()
		}, sqlite.ErrRebuildIncomplete},
		"the same name": {func() (ddl.Script, error) {
			return sqlite.Rebuild(tUsers).Into(sqlite.CreateTable(tUsers).Column(ddl.Column(tID))).Copy(sqlite.CopyAs(tID, tID)).Build()
		}, sqlite.ErrRebuildSameName},
		"a copy into another table": {func() (ddl.Script, error) {
			return sqlite.Rebuild(tUsers).Into(sqlite.CreateTable(next).Column(ddl.Column(nid))).Copy(sqlite.CopyAs(tID, tID)).Build()
		}, ddl.ErrForeignColumn},
		"an FTS5 rowid from another table": {func() (ddl.Script, error) {
			return sqlite.CreateFTS5(fts).Columns(ftsEmail).ExternalContent(tUsers, tSessUID).Build()
		}, ddl.ErrForeignColumn},
	} {
		if _, err := tc.b(); !errors.Is(err, tc.want) {
			t.Errorf("%s: got %v, want %v", name, err, tc.want)
		}
	}
}

// TestDescribe_SQLiteKeysAndReferences pins the SQLite clauses' descriptions,
// AutoincrementKey among them, and that the set orders by the reference.
func TestDescribe_SQLiteKeysAndReferences(t *testing.T) {
	users, err := sqlite.CreateTable(tUsers).Column(ddl.Column(tID), ddl.Column(tEmail)).
		Clause(sqlite.AutoincrementKey(tID), sqlite.Unique(tEmail).Named(sqlite.TableConstraint{Table: tEmail.Table(), Name: "users_email"}), sqlite.Strict()).Describe()
	if err != nil {
		t.Fatal(err)
	}
	if len(users.PrimaryKey) != 1 || users.PrimaryKey[0] != "id" || len(users.Indexes) != 1 || !users.Indexes[0].Unique {
		t.Fatalf("users: %+v", users)
	}
	sess, err := sqlite.CreateTable(tSess).Column(ddl.Column(tSessID), ddl.Column(tSessUID)).
		Clause(sqlite.PrimaryKey(tSessID), sqlite.References(tSessUID, tID).OnDelete(sqlite.RefCascade)).Describe()
	if err != nil {
		t.Fatal(err)
	}
	if len(sess.ForeignKeys) != 1 || sess.ForeignKeys[0].RefTable != "users" || sess.ForeignKeys[0].OnDelete != schema.Cascade {
		t.Fatalf("sessions: %+v", sess.ForeignKeys)
	}
	set, err := schema.New("app", sess, users)
	if err != nil {
		t.Fatal(err)
	}
	sorted, err := set.Sorted()
	if err != nil || sorted[0].Name != "users" {
		t.Fatalf("order %v, %v", sorted, err)
	}
}

// TestTableConstraint_SQLiteRefusesAnotherTable pins the declared-constraint
// check on SQLite's two named clauses, and that an unnamed key stays legal.
func TestTableConstraint_SQLiteRefusesAnotherTable(t *testing.T) {
	key := sqlite.TableConstraint{Table: tUsers, Name: "users_email_key"}
	for name, build := range map[string]func() (ddl.Script, error){
		"named key":   sqlite.CreateTable(tSess).Column(ddl.Column(tSessID)).Clause(sqlite.Unique(tSessID).Named(key)).Build,
		"foreign key": sqlite.CreateTable(tSess).Column(ddl.Column(tSessID)).Clause(sqlite.ForeignKey(key, sqlite.RefPair(tSessID, tSessID))).Build,
	} {
		if _, err := build(); !errors.Is(err, ddl.ErrForeignColumn) {
			t.Errorf("%s: %v, want ErrForeignColumn", name, err)
		}
	}
	if _, err := sqlite.CreateTable(tUsers).Column(ddl.Column(tEmail)).Clause(sqlite.Unique(tEmail).Named(sqlite.TableConstraint{Table: tUsers})).Build(); !errors.Is(err, ddl.ErrZeroValue) {
		t.Errorf("a half-declared name: %v", err)
	}
	if _, err := sqlite.CreateTable(tUsers).Column(ddl.Column(tEmail)).Clause(sqlite.Unique(tEmail)).Build(); err != nil {
		t.Errorf("an unnamed unique key: %v", err)
	}
}
