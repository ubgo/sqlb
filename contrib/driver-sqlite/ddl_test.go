package driversqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ubgo/sqlb"
	driversqlite "github.com/ubgo/sqlb/contrib/driver-sqlite"
	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/sqlite"
)

// These tasks execute the DDL builders against a real SQLite, and each one
// uses the same Col declarations to create a table and to query it, which is
// the one-declaration promise the ddl package exists for: a column cannot be
// created under one name or type and read under another.

type ddlStatus string

var (
	ddlPeople    = sqlb.Table{Name: "people", Alias: "p"}
	ddlID        = sqlb.Col[int64](ddlPeople, "id")
	ddlEmail     = sqlb.Col[string](ddlPeople, "email")
	ddlNick      = sqlb.Col[*string](ddlPeople, "nick")
	ddlHandle    = sqlb.Col[*string](ddlPeople, "handle")
	ddlStatusCol = sqlb.Col[ddlStatus](ddlPeople, "status")
	ddlPhone     = sqlb.Col[*string](ddlPeople, "phone")
)

func peopleTable() *sqlite.CreateTableReady {
	return sqlite.CreateTable(ddlPeople).IfNotExists().
		Column(ddl.Column(ddlID), ddl.Column(ddlEmail), ddl.Column(ddlNick), ddl.Column(ddlStatusCol).Default("active")).
		Constraint(ddl.OneOf(ddl.TableConstraint{Table: ddlStatusCol.Table(), Name: "people_status_known"}, ddlStatusCol, "active", "banned"))
}

func run(t *testing.T, db *sql.DB, s ddl.Script, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range s {
		if _, err := db.ExecContext(context.Background(), st.SQL); err != nil {
			t.Fatalf("%s: %v", st.SQL, err)
		}
	}
}

func openPeople(t *testing.T) *sql.DB {
	t.Helper()
	db, err := driversqlite.Memory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s, err := peopleTable().Build()
	run(t, db, s, err)
	return db
}

// TestTask_InstallALibrarysTableAndUseIt is the whole promise in one task:
// create the table from the column declarations, run the same CREATE again as
// a library does on every boot, then write and read a row through the query
// builder with the same declarations, the default and the CHECK both applied
// by the database.
func TestTask_InstallALibrarysTableAndUseIt(t *testing.T) {
	ctx := context.Background()
	db := openPeople(t)
	s, err := peopleTable().Build()
	run(t, db, s, err) // the second boot: IF NOT EXISTS makes it a no-op

	ins, err := sqlite.Insert(ddlPeople).Row(sqlb.Set(ddlID, int64(1)), sqlb.Set(ddlEmail, "ada@example.com")).Build()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, ins.SQL, ins.Args...); err != nil {
		t.Fatal(err)
	}

	var got ddlStatus
	var nick *string
	q, err := sqlite.Select().Columns(sqlb.Bind(ddlStatusCol, &got), sqlb.Bind(ddlNick, &nick)).
		From(ddlPeople).Where(sqlb.Eq(ddlID, int64(1))).Build()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlb.One(ctx, db, q); err != nil {
		t.Fatal(err)
	}
	if got != "active" {
		t.Errorf("status = %q, want the column's default", got)
	}
	if nick != nil {
		t.Errorf("nick = %q, want NULL from a *string column", *nick)
	}

	bad, err := sqlite.Insert(ddlPeople).Row(sqlb.Set(ddlID, int64(2)), sqlb.Set(ddlEmail, "b@example.com"), sqlb.Set(ddlStatusCol, "unknown")).Build()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, bad.SQL, bad.Args...); err == nil {
		t.Error("a status outside OneOf was stored; the CHECK did not reach the table")
	}
}

// TestTask_EvolveATableAcrossReleases is the next release of the library: add
// a column, rename one, drop one, each through its own declaration. SQLite
// takes one action per statement, so the builder writes three; the code is
// the same as on Postgres.
func TestTask_EvolveATableAcrossReleases(t *testing.T) {
	ctx := context.Background()
	db := openPeople(t)
	s, err := sqlite.AlterTable(ddlPeople).Action(
		ddl.AddColumn(ddl.Column(ddlPhone)),
		ddl.RenameColumn(ddlNick, ddlHandle),
		ddl.DropColumn(ddlPhone),
	).Build()
	if len(s) != 3 {
		t.Fatalf("SQLite needs one statement per action, got %v", s.SQL())
	}
	run(t, db, s, err)

	ins, err := sqlite.Insert(ddlPeople).Row(sqlb.Set(ddlID, int64(1)), sqlb.Set(ddlEmail, "ada@example.com"), sqlb.Set(ddlHandle, ptr("ada"))).Build()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, ins.SQL, ins.Args...); err != nil {
		t.Fatalf("the renamed column is not writable under its new declaration: %v", err)
	}
	var handle *string
	q, err := sqlite.Select().Columns(sqlb.Bind(ddlHandle, &handle)).From(ddlPeople).Build()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlb.One(ctx, db, q); err != nil || handle == nil || *handle != "ada" {
		t.Fatalf("read back %v, %v", handle, err)
	}
}

// TestTask_RemoveALibrarysTables drops two tables, one statement each, and
// again: IF EXISTS makes an uninstall safe to repeat.
func TestTask_RemoveALibrarysTables(t *testing.T) {
	db := openPeople(t)
	other := sqlb.Table{Name: "other"}
	s, err := sqlite.CreateTable(other).Column(ddl.Column(sqlb.Col[int64](other, "id"))).Build()
	run(t, db, s, err)
	for range 2 {
		s, err := sqlite.DropTable(other, ddlPeople).IfExists().Build()
		run(t, db, s, err)
	}
	if _, err := db.ExecContext(context.Background(), `SELECT 1 FROM "people"`); err == nil {
		t.Error("people still exists")
	}
}

// TestDDL_SQLiteRefusesATimeDefault pins the one value SQLite declines at
// Build. The modernc driver stores a bound time.Time as Go's String form, which
// no literal matches, so a time default would never compare equal to a time
// the application wrote.
func TestDDL_SQLiteRefusesATimeDefault(t *testing.T) {
	when := sqlb.Col[time.Time](ddlPeople, "created_at")
	_, err := sqlite.CreateTable(ddlPeople).Column(ddl.Column(when).Default(time.Now())).Build()
	if !errors.Is(err, sqlb.ErrNotInlinable) {
		t.Fatalf("got %v, want ErrNotInlinable", err)
	}
}

func ptr[T any](v T) *T { return &v }

// TestDDL_SQLiteKeysStrictAndGeneratedColumnsHold installs the spec's tables
// and has SQLite enforce each clause: STRICT refuses a mistyped value, NOCASE
// makes the unique email case-insensitive, the virtual column computes, ON
// CONFLICT IGNORE drops a duplicate silently, AUTOINCREMENT never reuses a
// rowid, and the foreign key cascades.
func TestDDL_SQLiteKeysStrictAndGeneratedColumnsHold(t *testing.T) {
	ctx := context.Background()
	db, err := driversqlite.Memory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	users := sqlb.Table{Name: "k_users", Alias: "u"}
	id := sqlb.Col[int64](users, "id")
	email := sqlb.Col[string](users, "email")
	domain := sqlb.Col[string](users, "domain")
	age := sqlb.Col[*int64](users, "age")
	for range 2 {
		s, err := sqlite.CreateTable(users).IfNotExists().
			Column(ddl.Column(id), ddl.Column(email), ddl.Column(domain), ddl.Column(age)).
			Clause(sqlite.PrimaryKey(id), sqlite.Unique(email), sqlite.ColumnCollate(email, sqlite.NoCase),
				sqlite.GeneratedVirtual(domain, sqlite.Substr(email, sqlb.Add(sqlite.Instr(email, "@"), sqlb.Literal[int32]("1")))),
				sqlite.Strict()).Build()
		run(t, db, s, err)
	}
	exec := func(q string) error { _, err := db.ExecContext(ctx, q); return err }
	if err := exec(`INSERT INTO k_users (id, email, age) VALUES (1, 'Ada@Example.com', 36)`); err != nil {
		t.Fatal(err)
	}
	if exec(`INSERT INTO k_users (id, email, age) VALUES (2, 'b@x.io', 'old')`) == nil {
		t.Error("STRICT accepted text in an INTEGER column")
	}
	if exec(`INSERT INTO k_users (id, email) VALUES (3, 'ada@example.COM')`) == nil {
		t.Error("NOCASE did not make the unique email case-insensitive")
	}
	var d string
	if err := db.QueryRowContext(ctx, `SELECT domain FROM k_users WHERE id = 1`).Scan(&d); err != nil || d != "Example.com" {
		t.Errorf("virtual column %q, %v", d, err)
	}

	tags := sqlb.Table{Name: "k_tags"}
	name := sqlb.Col[string](tags, "name")
	s, err := sqlite.CreateTable(tags).Column(ddl.Column(name)).Clause(sqlite.PrimaryKey(name).OnConflict(sqlite.OrIgnore), sqlite.WithoutRowID()).Build()
	run(t, db, s, err)
	if err := exec(`INSERT INTO k_tags VALUES ('go'); INSERT INTO k_tags VALUES ('go')`); err != nil {
		t.Errorf("ON CONFLICT IGNORE did not ignore the duplicate: %v", err)
	}
	var withoutRowID int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_list WHERE name = 'k_tags' AND wr = 1`).Scan(&withoutRowID); err != nil || withoutRowID != 1 {
		t.Errorf("WITHOUT ROWID not recorded: %d, %v", withoutRowID, err)
	}

	audit := sqlb.Table{Name: "k_audit"}
	aid := sqlb.Col[int64](audit, "id")
	s, err = sqlite.CreateTable(audit).Column(ddl.Column(aid)).Clause(sqlite.AutoincrementKey(aid)).Build()
	run(t, db, s, err)
	if err := exec(`INSERT INTO k_audit DEFAULT VALUES; INSERT INTO k_audit DEFAULT VALUES; DELETE FROM k_audit WHERE id = 2; INSERT INTO k_audit DEFAULT VALUES`); err != nil {
		t.Fatal(err)
	}
	var maxID int64
	if err := db.QueryRowContext(ctx, `SELECT max(id) FROM k_audit`).Scan(&maxID); err != nil || maxID != 3 {
		t.Errorf("AUTOINCREMENT reused a rowid: max id %d, %v", maxID, err)
	}

	sess := sqlb.Table{Name: "k_sessions"}
	uid := sqlb.Col[int64](sess, "user_id")
	s, err = sqlite.CreateTable(sess).Column(ddl.Column(uid)).Clause(sqlite.References(uid, id).OnDelete(sqlite.RefCascade)).Build()
	run(t, db, s, err)
	if err := exec(`INSERT INTO k_sessions VALUES (1)`); err != nil {
		t.Fatal(err)
	}
	if exec(`INSERT INTO k_sessions VALUES (99)`) == nil {
		t.Error("the foreign key accepted a missing user; is PRAGMA foreign_keys on?")
	}
	if err := exec(`DELETE FROM k_users WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM k_sessions`).Scan(&n); err != nil || n != 0 {
		t.Errorf("ON DELETE CASCADE left %d sessions, %v", n, err)
	}
}

// TestDDL_SQLiteRefusalsMatchTheEngine pins what each ADD COLUMN refusal is
// based on, measured rather than read. A key constraint SQLite always refuses.
// The other three it refuses only when the table has rows, and accepts on an
// empty one, which is exactly the state a test database is in: accepting them
// would let a migration pass in CI and fail on the first production database.
// Build therefore refuses all four unconditionally, and this test is what
// says the choice is deliberate rather than a mistake about the engine.
func TestDDL_SQLiteRefusalsMatchTheEngine(t *testing.T) {
	ctx := context.Background()
	rowDependent := map[string]string{
		"NOT NULL without default":  `ALTER TABLE "people" ADD COLUMN "x" TEXT NOT NULL`,
		"an expression default":     `ALTER TABLE "people" ADD COLUMN "x" TEXT DEFAULT (CURRENT_TIMESTAMP)`,
		"a stored generated column": `ALTER TABLE "people" ADD COLUMN "x" TEXT GENERATED ALWAYS AS (upper("email")) STORED`,
	}
	for name, q := range rowDependent {
		empty := openPeople(t)
		if _, err := empty.ExecContext(ctx, q); err != nil {
			t.Errorf("SQLite refused %s on an empty table; the measured rule changed: %v", name, err)
		}
	}
	db := openPeople(t)
	if _, err := db.ExecContext(ctx, `INSERT INTO "people" ("id", "email") VALUES (1, 'a@b.c')`); err != nil {
		t.Fatal(err)
	}
	for name, q := range rowDependent {
		if _, err := db.ExecContext(ctx, q); err == nil {
			t.Errorf("SQLite accepted %s on a table with rows, so the refusal protects nothing", name)
		}
	}
	for name, q := range map[string]string{
		"a unique column":      `ALTER TABLE "people" ADD COLUMN "x" TEXT UNIQUE`,
		"a primary key column": `ALTER TABLE "people" ADD COLUMN "x" INTEGER PRIMARY KEY`,
	} {
		if _, err := db.ExecContext(ctx, q); err == nil {
			t.Errorf("SQLite accepted %s", name)
		}
	}
	upper := sqlb.Col[string](ddlPeople, "email_upper")
	locale := sqlb.Col[string](ddlPeople, "locale")
	s, err := sqlite.AlterTable(ddlPeople).Clause(
		sqlite.AddColumn(ddl.Column(upper), sqlite.GeneratedVirtual(upper, sqlb.Upper(ddlEmail))),
	).Action(ddl.AddColumn(ddl.Column(locale).Default("en"))).Build()
	run(t, db, s, err)
	var u, l string
	if err := db.QueryRowContext(ctx, `SELECT email_upper, locale FROM people`).Scan(&u, &l); err != nil || u != "A@B.C" || l != "en" {
		t.Errorf("added columns read %q %q, %v", u, l, err)
	}
	s, err = sqlite.AlterTable(ddlPeople).Clause(sqlite.RenameTo(sqlb.Table{Name: "persons"})).Build()
	run(t, db, s, err)
	if _, err := db.ExecContext(ctx, `SELECT 1 FROM persons`); err != nil {
		t.Errorf("the renamed table: %v", err)
	}
}

// TestDDL_SQLiteIndexesViewsTriggersAndPragmas runs section 7.4 on SQLite: a
// NOCASE unique index refuses a case variant, a partial index is used by a
// query with its filter, a view over a query-builder body reads, a trigger
// whose body is an ordinary INSERT writes the audit row only when the status
// changes, and user_version reads back.
func TestDDL_SQLiteIndexesViewsTriggersAndPragmas(t *testing.T) {
	ctx := context.Background()
	db, err := driversqlite.Memory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	users := sqlb.Table{Name: "o_users", Alias: "u"}
	id := sqlb.Col[int64](users, "id")
	email := sqlb.Col[string](users, "email")
	status := sqlb.Col[string](users, "status")
	audit := sqlb.Table{Name: "o_audit", Alias: "a"}
	auditUser := sqlb.Col[int64](audit, "user_id")
	auditWhat := sqlb.Col[string](audit, "what")
	exec := func(q string) error { _, err := db.ExecContext(ctx, q); return err }
	for _, b := range []interface{ Build() (ddl.Script, error) }{
		sqlite.CreateTable(users).Column(ddl.Column(id), ddl.Column(email), ddl.Column(status).Default("active")).Clause(sqlite.PrimaryKey(id)),
		sqlite.CreateTable(audit).Column(ddl.Column(auditUser), ddl.Column(auditWhat)),
		sqlite.CreateIndex(sqlite.Index{Name: "o_users_email"}, users).Unique().IfNotExists().On(sqlite.IndexKey(email).Collate(sqlite.NoCase)),
		sqlite.CreateIndex(sqlite.Index{Name: "o_users_email"}, users).Unique().IfNotExists().On(sqlite.IndexKey(email).Collate(sqlite.NoCase)),
		sqlite.CreateIndex(sqlite.Index{Name: "o_users_banned"}, users).IfNotExists().On(sqlite.IndexKey(id)).Where(sqlb.Eq(status, "banned")),
		sqlite.CreateView(sqlite.View{Name: "o_active"}).IfNotExists().As(sqlite.Select(id, email).From(users).Where(sqlb.Eq(status, "active"))),
		sqlite.CreateView(sqlite.View{Name: "o_active"}).IfNotExists().As(sqlite.Select(id, email).From(users).Where(sqlb.Eq(status, "active"))),
		sqlite.CreateTrigger(sqlite.Trigger{Name: "o_users_audit"}).IfNotExists().After(sqlite.OnUpdateOf(status), users).
			When(sqlite.IsNot(sqlite.Old(status), sqlite.New(status))).
			Do(sqlite.StepInsert(sqlite.Insert(audit).Row(sqlb.SetExpr(auditUser, sqlite.New(id)), sqlb.SetExpr(auditWhat, sqlite.New(status))))),
		sqlite.SetUserVersion(7),
	} {
		s, err := b.Build()
		run(t, db, s, err)
	}
	if err := exec(`INSERT INTO o_users (id, email) VALUES (1, 'Ada@x.io'), (2, 'b@x.io')`); err != nil {
		t.Fatal(err)
	}
	if exec(`INSERT INTO o_users (id, email) VALUES (3, 'ada@X.IO')`) == nil {
		t.Error("the NOCASE unique index accepted a case variant")
	}
	for _, q := range []string{
		`UPDATE o_users SET status = 'banned' WHERE id = 1`,
		`UPDATE o_users SET status = 'banned' WHERE id = 1`,
		`UPDATE o_users SET email = 'c@x.io' WHERE id = 2`,
	} {
		if err := exec(q); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	var what string
	if err := db.QueryRowContext(ctx, `SELECT count(*), max(what) FROM o_audit`).Scan(&n, &what); err != nil || n != 1 || what != "banned" {
		t.Errorf("the trigger wrote %d audit rows (%q), want 1: %v", n, what, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM o_active`).Scan(&n); err != nil || n != 1 {
		t.Errorf("the view shows %d rows, %v", n, err)
	}
	rows, err := db.QueryContext(ctx, `EXPLAIN QUERY PLAN SELECT id FROM o_users WHERE status = 'banned'`)
	if err != nil {
		t.Fatal(err)
	}
	var plan []string
	for rows.Next() {
		var a, b, c int
		var detail string
		if err := rows.Scan(&a, &b, &c, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	_ = rows.Close()
	if !strings.Contains(strings.Join(plan, " "), "o_users_banned") {
		t.Errorf("the partial index was not used by a query with its filter: %v", plan)
	}
	var v int
	if err := db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&v); err != nil || v != 7 {
		t.Errorf("user_version %d, %v", v, err)
	}
	for _, b := range []interface{ Build() (ddl.Script, error) }{
		sqlite.DropTrigger(sqlite.Trigger{Name: "o_users_audit"}).IfExists(), sqlite.DropTrigger(sqlite.Trigger{Name: "o_users_audit"}).IfExists(),
		sqlite.DropView(sqlite.View{Name: "o_active"}).IfExists(), sqlite.DropView(sqlite.View{Name: "o_active"}).IfExists(),
		sqlite.DropIndex(sqlite.Index{Name: "o_users_email"}).IfExists(), sqlite.DropIndex(sqlite.Index{Name: "o_users_email"}).IfExists(),
	} {
		s, err := b.Build()
		run(t, db, s, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_schema WHERE name IN ('o_users_audit', 'o_active', 'o_users_email')`).Scan(&n); err != nil || n != 0 {
		t.Errorf("%d dropped objects remain, %v", n, err)
	}
}

// TestDDL_SQLiteForeignKeysPragmaIsANoOpInATransaction pins why ForeignKeys is
// marked OutsideTransaction: set inside BEGIN it changes nothing, silently.
func TestDDL_SQLiteForeignKeysPragmaIsANoOpInATransaction(t *testing.T) {
	ctx := context.Background()
	db, err := driversqlite.Memory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	off, err := sqlite.ForeignKeys(false).Build()
	if err != nil || !off[0].OutsideTransaction {
		t.Fatalf("not marked OutsideTransaction: %v", err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, off[0].SQL); err != nil {
		t.Fatal(err)
	}
	var on int
	if err := tx.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&on); err != nil || on != 1 {
		t.Errorf("inside a transaction foreign_keys read %d; the measured no-op changed: %v", on, err)
	}
	_ = tx.Rollback()
	if _, err := db.ExecContext(ctx, off[0].SQL); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&on); err != nil || on != 0 {
		t.Errorf("outside a transaction foreign_keys read %d, %v", on, err)
	}
}

// runScript is a runner that honours a Script's contract: statements marked
// OutsideTransaction run on their own, everything else between them in one
// transaction, and a MustReturnNoRows check fails on any row. It is what an
// application's runner has to do, written once so the test holds the rebuild
// to it.
func runScript(ctx context.Context, db *sql.DB, s ddl.Script) error {
	var tx *sql.Tx
	commit := func() error {
		if tx == nil {
			return nil
		}
		err := tx.Commit()
		tx = nil
		return err
	}
	for _, st := range s {
		if st.OutsideTransaction {
			if err := commit(); err != nil {
				return err
			}
			if _, err := db.ExecContext(ctx, st.SQL); err != nil {
				return err
			}
			continue
		}
		if tx == nil {
			var err error
			if tx, err = db.BeginTx(ctx, nil); err != nil {
				return err
			}
		}
		if st.MustReturnNoRows {
			rows, err := tx.QueryContext(ctx, st.SQL)
			if err != nil {
				_ = tx.Rollback()
				return err
			}
			found := rows.Next()
			_ = rows.Close()
			if found {
				_ = tx.Rollback()
				return errors.New("check failed: " + st.SQL)
			}
			continue
		}
		if _, err := tx.ExecContext(ctx, st.SQL); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return commit()
}

// TestTask_ChangeAColumnTypeOnSQLite is the table rebuild: sessions.expires_at
// goes from text to an integer, the rows are converted, the index on the old
// table comes back, and sqlite_schema holds the same objects afterwards.
func TestTask_ChangeAColumnTypeOnSQLite(t *testing.T) {
	ctx := context.Background()
	db, err := driversqlite.Memory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	users := sqlb.Table{Name: "r_users"}
	uid := sqlb.Col[int64](users, "id")
	sess := sqlb.Table{Name: "r_sessions", Alias: "s"}
	sid := sqlb.Col[int64](sess, "id")
	suser := sqlb.Col[int64](sess, "user_id")
	sexp := sqlb.Col[string](sess, "expires_at")
	byUser := sqlite.CreateIndex(sqlite.Index{Name: "r_sessions_user"}, sess).On(sqlite.IndexKey(suser))
	for _, b := range []interface{ Build() (ddl.Script, error) }{
		sqlite.CreateTable(users).Column(ddl.Column(uid)).Clause(sqlite.PrimaryKey(uid)),
		sqlite.CreateTable(sess).Column(ddl.Column(sid), ddl.Column(suser), ddl.Column(sexp)).
			Clause(sqlite.PrimaryKey(sid), sqlite.References(suser, uid).OnDelete(sqlite.RefCascade)),
		byUser,
	} {
		s, err := b.Build()
		run(t, db, s, err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO r_users VALUES (1); INSERT INTO r_sessions VALUES (10, 1, '2026-01-01 00:00:00'), (11, 1, '2026-01-02 00:00:00')`); err != nil {
		t.Fatal(err)
	}
	schema := func() string {
		var s string
		if err := db.QueryRowContext(ctx, `SELECT group_concat(type || ':' || name, ',') FROM (SELECT type, name FROM sqlite_schema ORDER BY name)`).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	before := schema()

	next := sqlb.Table{Name: "r_sessions_new"}
	nid := sqlb.Col[int64](next, "id")
	nuser := sqlb.Col[int64](next, "user_id")
	nexp := sqlb.Col[int64](next, "expires_at")
	rb := sqlite.Rebuild(sess).
		Into(sqlite.CreateTable(next).Column(ddl.Column(nid), ddl.Column(nuser), ddl.Column(nexp)).
			Clause(sqlite.PrimaryKey(nid), sqlite.References(nuser, uid).OnDelete(sqlite.RefCascade))).
		Copy(sqlite.CopyAs(nid, sid), sqlite.CopyAs(nuser, suser),
			sqlite.CopyAs(nexp, sqlb.Cast[int64](sqlite.Strftime("%s", sexp), "INTEGER"))).
		Recreate(byUser)
	script, err := rb.Build()
	if err != nil {
		t.Fatal(err)
	}
	if !script[0].OutsideTransaction || !script[len(script)-1].OutsideTransaction || !script[len(script)-2].MustReturnNoRows {
		t.Fatalf("the rebuild's contract flags are wrong: %+v", script)
	}
	if err := runScript(ctx, db, script); err != nil {
		t.Fatal(err)
	}
	if after := schema(); after != before {
		t.Errorf("sqlite_schema changed across the rebuild:\nbefore %s\nafter  %s", before, after)
	}
	var typ string
	var got int64
	if err := db.QueryRowContext(ctx, `SELECT typeof(expires_at), expires_at FROM r_sessions WHERE id = 10`).Scan(&typ, &got); err != nil || typ != "integer" || got != 1767225600 {
		t.Errorf("expires_at %s %d, %v", typ, got, err)
	}
	var fk int
	if err := db.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&fk); err != nil || fk != 1 {
		t.Errorf("foreign keys left %d after the rebuild, %v", fk, err)
	}

	// A rebuild that forgets the index loses it, which is what the schema
	// comparison exists to catch.
	s2, err := sqlite.Rebuild(sess).Into(sqlite.CreateTable(next).Column(ddl.Column(nid), ddl.Column(nuser), ddl.Column(nexp)).Clause(sqlite.PrimaryKey(nid))).
		Copy(sqlite.CopyAs(nid, sid), sqlite.CopyAs(nuser, suser), sqlite.CopyAs(nexp, sqlb.Literal[int64]("expires_at"))).Build()
	if err != nil {
		t.Fatal(err)
	}
	if err := runScript(ctx, db, s2); err != nil {
		t.Fatal(err)
	}
	if schema() == before {
		t.Error("a rebuild without Recreate kept the index; the schema comparison proves nothing")
	}

	// The foreign-key check fails a rebuild that breaks a reference.
	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys = OFF; INSERT INTO r_sessions VALUES (12, 99, 0); PRAGMA foreign_keys = ON`); err != nil {
		t.Fatal(err)
	}
	s3, err := sqlite.Rebuild(sess).Into(sqlite.CreateTable(next).Column(ddl.Column(nid), ddl.Column(nuser), ddl.Column(nexp)).
		Clause(sqlite.PrimaryKey(nid), sqlite.References(nuser, uid))).
		Copy(sqlite.CopyAs(nid, sid), sqlite.CopyAs(nuser, suser), sqlite.CopyAs(nexp, sqlb.Literal[int64]("expires_at"))).Build()
	if err != nil {
		t.Fatal(err)
	}
	if err := runScript(ctx, db, s3); err == nil {
		t.Error("the rebuild carried a dangling reference and the foreign-key check passed it")
	}
}

// TestDDL_SQLiteFTS5ExternalContent creates an FTS5 index over a content
// table, fills it with the 'rebuild' command, and searches it with Match.
func TestDDL_SQLiteFTS5ExternalContent(t *testing.T) {
	ctx := context.Background()
	db, err := driversqlite.Memory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	docs := sqlb.Table{Name: "f_docs"}
	did := sqlb.Col[int64](docs, "id")
	dtitle := sqlb.Col[string](docs, "title")
	fts := sqlb.Table{Name: "f_docs_fts"}
	ftitle := sqlb.Col[string](fts, "title")
	for _, b := range []interface{ Build() (ddl.Script, error) }{
		sqlite.CreateTable(docs).Column(ddl.Column(did), ddl.Column(dtitle)).Clause(sqlite.PrimaryKey(did)),
		sqlite.CreateFTS5(fts).IfNotExists().Columns(ftitle).ExternalContent(docs, did).Tokenize(sqlite.Unicode61(2)).Prefix(2),
		sqlite.CreateFTS5(fts).IfNotExists().Columns(ftitle).ExternalContent(docs, did).Tokenize(sqlite.Unicode61(2)).Prefix(2),
	} {
		s, err := b.Build()
		run(t, db, s, err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO f_docs VALUES (1, 'Café au lait'), (2, 'Tea'); INSERT INTO f_docs_fts(f_docs_fts) VALUES ('rebuild')`); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM f_docs_fts WHERE f_docs_fts MATCH 'cafe'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("remove_diacritics search found %d, %v", n, err)
	}
}
