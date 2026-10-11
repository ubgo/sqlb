package driverpgx_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/pg"
	"github.com/ubgo/sqlb/pg/trgm"
	"github.com/ubgo/sqlb/pg/vector"
)

// The DDL builders against a real Postgres. Every test runs in a transaction
// it rolls back: Postgres DDL is transactional, so the database is left as it
// was, and that is itself a property the Script's Transactional flag claims.

type ddlStatus string

var (
	ddlPeople    = sqlb.Table{Name: "ddl_people", Alias: "p"}
	ddlID        = sqlb.Col[int64](ddlPeople, "id")
	ddlEmail     = sqlb.Col[string](ddlPeople, "email")
	ddlNick      = sqlb.Col[*string](ddlPeople, "nick")
	ddlHandle    = sqlb.Col[*string](ddlPeople, "handle")
	ddlStatusCol = sqlb.Col[ddlStatus](ddlPeople, "status")
	ddlCreated   = sqlb.Col[time.Time](ddlPeople, "created_at")
	ddlPhone     = sqlb.Col[*string](ddlPeople, "phone")
	ddlLocale    = sqlb.Col[string](ddlPeople, "locale")
)

func peopleTable() *pg.CreateTableReady {
	return pg.CreateTable(ddlPeople).IfNotExists().
		Column(ddl.Column(ddlID), ddl.Column(ddlEmail), ddl.Column(ddlNick),
			ddl.Column(ddlStatusCol).Default("active"), ddl.Column(ddlCreated).DefaultExpr(pg.Now())).
		Constraint(ddl.OneOf(ddl.TableConstraint{Table: ddlStatusCol.Table(), Name: "ddl_people_status_known"}, ddlStatusCol, "active", "banned"))
}

// inTx runs fn in a transaction and rolls it back.
func inTx(t *testing.T, fn func(tx *sql.Tx)) {
	t.Helper()
	db := openOrSkip(t)
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	fn(tx)
}

func runTx(t *testing.T, tx *sql.Tx, s ddl.Script, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	if !s.Transactional() {
		t.Fatalf("a script claimed to be non-transactional: %v", s.SQL())
	}
	for _, st := range s {
		if _, err := tx.ExecContext(context.Background(), st.SQL); err != nil {
			t.Fatalf("%s: %v", st.SQL, err)
		}
	}
}

// TestDDL_InstallAndUseATableOnPostgres creates the table twice from its
// declarations and writes and reads a row through the same ones, with the
// literal default, the expression default and the CHECK applied by Postgres.
func TestDDL_InstallAndUseATableOnPostgres(t *testing.T) {
	inTx(t, func(tx *sql.Tx) {
		ctx := context.Background()
		s, err := peopleTable().Build()
		runTx(t, tx, s, err)
		s, err = peopleTable().Build()
		runTx(t, tx, s, err)

		ins, err := pg.Insert(ddlPeople).Row(sqlb.Set(ddlID, int64(1)), sqlb.Set(ddlEmail, "ada@example.com")).Build()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx, ins.SQL, ins.Args...); err != nil {
			t.Fatal(err)
		}
		var got ddlStatus
		var created time.Time
		q, err := pg.Select().Columns(sqlb.Bind(ddlStatusCol, &got), sqlb.Bind(ddlCreated, &created)).
			From(ddlPeople).Where(sqlb.Eq(ddlID, int64(1))).Build()
		if err != nil {
			t.Fatal(err)
		}
		if err := sqlb.One(ctx, tx, q); err != nil {
			t.Fatal(err)
		}
		if got != "active" || created.IsZero() {
			t.Errorf("status %q, created %v: the defaults did not apply", got, created)
		}

		bad, err := pg.Insert(ddlPeople).Row(sqlb.Set(ddlID, int64(2)), sqlb.Set(ddlEmail, "b@example.com"), sqlb.Set(ddlStatusCol, "unknown")).Build()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx, "SAVEPOINT bad"); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx, bad.SQL, bad.Args...); err == nil {
			t.Error("a status outside OneOf was stored; the CHECK did not reach the table")
		}
		_, _ = tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT bad")
	})
}

// TestDDL_PostgresGroupsActionsButNeverARename executes the grouping. Postgres
// applies joinable actions in one statement, and RENAME is a top-level form in
// its grammar, so combining it is a syntax error; the builder must keep it
// apart, and the server is what says whether it did.
func TestDDL_PostgresGroupsActionsButNeverARename(t *testing.T) {
	inTx(t, func(tx *sql.Tx) {
		s, err := peopleTable().Build()
		runTx(t, tx, s, err)
		s, err = pg.AlterTable(ddlPeople).Action(
			ddl.AddColumn(ddl.Column(ddlPhone)),
			ddl.RenameColumn(ddlNick, ddlHandle),
			ddl.AddColumn(ddl.Column(ddlLocale).Default("en")),
			ddl.DropColumn(ddlPhone),
		).Build()
		if len(s) != 3 {
			t.Fatalf("want add, rename, then add+drop grouped: got %v", s.SQL())
		}
		runTx(t, tx, s, err)

		var locale string
		var handle *string
		ins, err := pg.Insert(ddlPeople).Row(sqlb.Set(ddlID, int64(1)), sqlb.Set(ddlEmail, "ada@example.com"), sqlb.Set(ddlHandle, strPtr("ada"))).Build()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(context.Background(), ins.SQL, ins.Args...); err != nil {
			t.Fatal(err)
		}
		q, err := pg.Select().Columns(sqlb.Bind(ddlLocale, &locale), sqlb.Bind(ddlHandle, &handle)).From(ddlPeople).Build()
		if err != nil {
			t.Fatal(err)
		}
		if err := sqlb.One(context.Background(), tx, q); err != nil {
			t.Fatal(err)
		}
		if locale != "en" || handle == nil || *handle != "ada" {
			t.Errorf("locale %q, handle %v", locale, handle)
		}
	})
}

// TestDDL_DropTablesOnPostgres drops two tables one statement each, twice.
func TestDDL_DropTablesOnPostgres(t *testing.T) {
	inTx(t, func(tx *sql.Tx) {
		other := sqlb.Table{Name: "ddl_other"}
		s, err := peopleTable().Build()
		runTx(t, tx, s, err)
		s, err = pg.CreateTable(other).Column(ddl.Column(sqlb.Col[int64](other, "id"))).Build()
		runTx(t, tx, s, err)
		for range 2 {
			s, err := pg.DropTable(other, ddlPeople).IfExists().Build()
			runTx(t, tx, s, err)
		}
	})
}

func strPtr(s string) *string { return &s }

var (
	kUsers    = sqlb.Table{Name: "ddl_k_users", Alias: "u"}
	kUserID   = sqlb.Col[int64](kUsers, "id")
	kTenant   = sqlb.Col[int64](kUsers, "tenant_id")
	kEmail    = sqlb.Col[string](kUsers, "email")
	kEmailLow = sqlb.Col[string](kUsers, "email_lower")
	kNick     = sqlb.Col[*string](kUsers, "nick")

	kSessions = sqlb.Table{Name: "ddl_k_sessions", Alias: "s"}
	kSessID   = sqlb.Col[int64](kSessions, "id")
	kSessUser = sqlb.Col[int64](kSessions, "user_id")
	kSessTen  = sqlb.Col[int64](kSessions, "tenant_id")
)

func kUsersTable() *pg.CreateTableReady {
	return pg.CreateTable(kUsers).IfNotExists().
		Column(ddl.Column(kUserID), ddl.Column(kTenant), ddl.Column(kEmail), ddl.Column(kEmailLow), ddl.Column(kNick)).
		Clause(
			pg.Identity(kUserID, pg.Always),
			pg.ColumnCollate(kEmail, "C"),
			pg.GeneratedStored(kEmailLow, sqlb.Lower(kEmail)),
			pg.PrimaryKey(kUserID),
			pg.Unique(pg.TableConstraint{Table: kTenant.Table(), Name: "ddl_k_users_tenant_id_key"}, kTenant, kUserID),
			pg.Unique(pg.TableConstraint{Table: kNick.Table(), Name: "ddl_k_users_nick_key"}, kNick).NullsNotDistinct(),
			pg.Check(pg.TableConstraint{Table: kNick.Table(), Name: "ddl_k_users_nick_len"}, sqlb.LessEq(pg.Length(sqlb.AssertNotNull(kNick)), 8)),
		)
}

func kSessionsTable() *pg.CreateTableReady {
	return pg.CreateTable(kSessions).IfNotExists().
		Column(ddl.Column(kSessID), ddl.Column(kSessUser), ddl.Column(kSessTen)).
		Clause(
			pg.Identity(kSessID, pg.ByDefault),
			pg.PrimaryKey(kSessID).Named(pg.TableConstraint{Table: kSessID.Table(), Name: "ddl_k_sessions_pk"}),
			pg.References(kSessUser, kUserID).OnDelete(pg.RefCascade),
			pg.ForeignKey(pg.TableConstraint{Table: kSessTen.Table(), Name: "ddl_k_sessions_tenant_fk"}, pg.RefPair(kSessTen, kTenant), pg.RefPair(kSessUser, kUserID)).
				OnDelete(pg.RefCascade).Deferrable(pg.InitiallyDeferred),
		)
}

// failsAt runs one statement under a savepoint and reports whether Postgres
// refused it, rolling back so the transaction stays usable.
func failsAt(t *testing.T, tx *sql.Tx, q string, args ...any) bool {
	t.Helper()
	ctx := context.Background()
	if _, err := tx.ExecContext(ctx, "SAVEPOINT probe"); err != nil {
		t.Fatal(err)
	}
	_, err := tx.ExecContext(ctx, q, args...)
	if err != nil {
		_, _ = tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT probe")
		return true
	}
	_, _ = tx.ExecContext(ctx, "RELEASE SAVEPOINT probe")
	return false
}

// TestDDL_PostgresKeysAndGeneratedColumnsHold installs both tables twice and
// has Postgres enforce every clause: the identity refuses an explicit id, the
// generated column is computed, the composite unique key and NULLS NOT
// DISTINCT collide, the CHECK refuses, and the cascade removes sessions.
func TestDDL_PostgresKeysAndGeneratedColumnsHold(t *testing.T) {
	inTx(t, func(tx *sql.Tx) {
		ctx := context.Background()
		for range 2 {
			s, err := kUsersTable().Build()
			runTx(t, tx, s, err)
			s, err = kSessionsTable().Build()
			runTx(t, tx, s, err)
		}
		insUser := func(tenant int64, email string, nick *string) (int64, error) {
			var id int64
			q, err := pg.InsertReturning(pg.Insert(kUsers).Row(sqlb.Set(kTenant, tenant), sqlb.Set(kEmail, email), sqlb.Set(kNick, nick)), sqlb.Bind(kUserID, &id)).Build()
			if err != nil {
				t.Fatal(err)
			}
			return id, sqlb.One(ctx, tx, q)
		}
		ada, err := insUser(1, "Ada@Example.com", nil)
		if err != nil {
			t.Fatal(err)
		}
		var low string
		q, err := pg.Select().Columns(sqlb.Bind(kEmailLow, &low)).From(kUsers).Where(sqlb.Eq(kUserID, ada)).Build()
		if err != nil {
			t.Fatal(err)
		}
		if err := sqlb.One(ctx, tx, q); err != nil || low != "ada@example.com" {
			t.Fatalf("generated column read %q, %v", low, err)
		}

		if !failsAt(t, tx, `INSERT INTO "ddl_k_users" ("id", "tenant_id", "email") VALUES (999, 1, 'x')`) {
			t.Error("GENERATED ALWAYS accepted an explicit id")
		}
		if !failsAt(t, tx, `INSERT INTO "ddl_k_users" ("tenant_id", "email", "nick") VALUES (1, 'x', 'much too long')`) {
			t.Error("the CHECK did not reach the table")
		}
		if _, err := insUser(1, "b@example.com", strPtr("bo")); err != nil {
			t.Fatal(err)
		}
		if !failsAt(t, tx, `INSERT INTO "ddl_k_users" ("tenant_id", "email", "nick") VALUES (1, 'c', 'bo')`) {
			t.Error("the unique nick accepted a duplicate")
		}
		// Ada's nick is NULL; under NULLS NOT DISTINCT a second NULL collides,
		// where a plain UNIQUE would accept any number of them.
		if !failsAt(t, tx, `INSERT INTO "ddl_k_users" ("tenant_id", "email") VALUES (1, 'd')`) {
			t.Error("a second NULL nick was accepted; NULLS NOT DISTINCT did not reach the table")
		}

		// The composite foreign key is deferred, so it is checked at the end;
		// SET CONSTRAINTS ALL IMMEDIATE brings the check forward to prove it.
		if code := deferredViolation(t, tx, ada, 2); code != "23503" {
			t.Errorf("a session in the wrong tenant: SQLSTATE %q, want 23503 foreign_key_violation", code)
		}
		if code := deferredViolation(t, tx, ada, 1); code != "" {
			t.Errorf("a session in the right tenant was refused: SQLSTATE %q", code)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO "ddl_k_sessions" ("user_id", "tenant_id") VALUES ($1, 1)`, ada); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM "ddl_k_users" WHERE "id" = $1`, ada); err != nil {
			t.Fatal(err)
		}
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM "ddl_k_sessions"`).Scan(&n); err != nil || n != 0 {
			t.Errorf("ON DELETE CASCADE left %d sessions, %v", n, err)
		}

		s, err := pg.DropTable(kUsers).Clause(pg.Cascade()).Build()
		runTx(t, tx, s, err)
		if !failsAt(t, tx, `SELECT 1 FROM "ddl_k_users"`) {
			t.Error("the table survived DROP ... CASCADE")
		}
	})
}

// TestDDL_PostgresUnloggedTable checks Postgres records the table as unlogged.
func TestDDL_PostgresUnloggedTable(t *testing.T) {
	inTx(t, func(tx *sql.Tx) {
		s, err := pg.CreateTable(kUsers).Clause(pg.Unlogged()).Column(ddl.Column(kUserID)).Build()
		runTx(t, tx, s, err)
		var p string
		if err := tx.QueryRowContext(context.Background(), `SELECT relpersistence FROM pg_class WHERE relname = 'ddl_k_users'`).Scan(&p); err != nil || p != "u" {
			t.Fatalf("relpersistence %q, %v", p, err)
		}
	})
}

// deferredViolation inserts a session under a savepoint, forces the deferred
// constraints to be checked, and returns the SQLSTATE of the failure or "" if
// there was none. The insert itself succeeds either way, which is what
// INITIALLY DEFERRED means, so the insert succeeding proves nothing alone.
func deferredViolation(t *testing.T, tx *sql.Tx, user, tenant int64) string {
	t.Helper()
	ctx := context.Background()
	if _, err := tx.ExecContext(ctx, "SAVEPOINT deferred"); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT deferred") }()
	if _, err := tx.ExecContext(ctx, `INSERT INTO "ddl_k_sessions" ("user_id", "tenant_id") VALUES ($1, $2)`, user, tenant); err != nil {
		t.Fatalf("a deferred key refused the insert itself: %v", err)
	}
	_, err := tx.ExecContext(ctx, "SET CONSTRAINTS ALL IMMEDIATE")
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	if err != nil {
		t.Fatal(err)
	}
	return ""
}

// TestDDL_PostgresRecordsEveryReferentialAction creates one foreign key per
// action and deferral and reads back what Postgres stored in pg_constraint, so
// every keyword the closed sets write is proven to mean what its name says.
func TestDDL_PostgresRecordsEveryReferentialAction(t *testing.T) {
	cases := []struct {
		name     string
		fk       pg.ForeignKeyClause
		del, upd string
		deferred bool
		deferral bool
	}{
		{"no action", pg.References(kSessUser, kUserID).OnDelete(pg.RefNoAction), "a", "a", false, false},
		{"restrict", pg.References(kSessUser, kUserID).OnDelete(pg.RefRestrict).OnUpdate(pg.RefRestrict), "r", "r", false, false},
		{"cascade", pg.References(kSessUser, kUserID).OnDelete(pg.RefCascade).OnUpdate(pg.RefCascade), "c", "c", false, false},
		{"set null", pg.References(kSessUser, kUserID).OnDelete(pg.RefSetNull), "n", "a", false, false},
		{"set default", pg.References(kSessUser, kUserID).OnDelete(pg.RefSetDefault), "d", "a", false, false},
		{"immediate", pg.References(kSessUser, kUserID).Deferrable(pg.InitiallyImmediate), "a", "a", true, false},
		{"deferred", pg.References(kSessUser, kUserID).Deferrable(pg.InitiallyDeferred), "a", "a", true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inTx(t, func(tx *sql.Tx) {
				s, err := pg.CreateTable(kUsers).Column(ddl.Column(kUserID)).Clause(pg.PrimaryKey(kUserID)).Build()
				runTx(t, tx, s, err)
				s, err = pg.CreateTable(kSessions).Column(ddl.Column(kSessUser)).Clause(tc.fk).Build()
				runTx(t, tx, s, err)
				var del, upd string
				var deferrable, deferred bool
				err = tx.QueryRowContext(context.Background(),
					`SELECT confdeltype, confupdtype, condeferrable, condeferred FROM pg_constraint WHERE conrelid = 'ddl_k_sessions'::regclass AND contype = 'f'`).
					Scan(&del, &upd, &deferrable, &deferred)
				if err != nil {
					t.Fatal(err)
				}
				if del != tc.del || upd != tc.upd || deferrable != tc.deferred || deferred != tc.deferral {
					t.Errorf("stored delete %q update %q deferrable %v deferred %v", del, upd, deferrable, deferred)
				}
			})
		})
	}
}

// TestDDL_PostgresAltersATableInPlace runs every Postgres ALTER TABLE action
// against a table with rows and reads the result back from the catalog: the
// three-step NOT NULL, a type change with USING, an identity added to an
// existing column, a foreign key added NOT VALID and then validated, and the
// table renamed and moved. Postgres is what says each one did what it claims.
func TestDDL_PostgresAltersATableInPlace(t *testing.T) {
	inTx(t, func(tx *sql.Tx) {
		ctx := context.Background()
		s, err := pg.CreateTable(kUsers).Column(ddl.Column(kUserID), ddl.Column(kEmail), ddl.Column(kNick)).
			Clause(pg.PrimaryKey(kUserID)).Build()
		runTx(t, tx, s, err)
		if _, err := tx.ExecContext(ctx, `INSERT INTO "ddl_k_users" ("id", "email") VALUES (1, 'a'), (2, 'b')`); err != nil {
			t.Fatal(err)
		}
		tz := sqlb.Col[*string](kUsers, "tz")
		tzSet := sqlb.Col[string](kUsers, "tz")
		// 1. add nullable, twice to prove IF NOT EXISTS; 2. backfill; 3. tighten.
		for range 2 {
			s, err = pg.AlterTable(kUsers).Clause(pg.AddColumnIfNotExists(ddl.Column(tz), pg.ColumnCollate(tz, "C"))).Build()
			runTx(t, tx, s, err)
		}
		if !failsAt(t, tx, `ALTER TABLE "ddl_k_users" ALTER COLUMN "tz" SET NOT NULL`) {
			t.Fatal("SET NOT NULL succeeded over NULL rows; the three steps are not needed and the test is wrong")
		}
		up, err := pg.Update(kUsers).Set(sqlb.Set(tzSet, "UTC")).All().Build()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx, up.SQL, up.Args...); err != nil {
			t.Fatal(err)
		}
		s, err = pg.AlterTable(kUsers).Clause(
			pg.SetNotNull(tz),
			pg.SetDefault(kNick, strPtr("anon")),
			pg.AlterType(kEmail, ddl.RawType[string]("varchar(8)")).Collate("C").Using(pg.Left(kEmail, 8)),
			pg.AddIdentity(kUserID, pg.ByDefault),
			pg.EnableRowLevelSecurity(),
		).Build()
		if len(s) != 1 {
			t.Fatalf("joinable actions should share one statement: %v", s.SQL())
		}
		runTx(t, tx, s, err)

		var nullable, typ, coll string
		var def sql.NullString
		if err := tx.QueryRowContext(ctx, `SELECT is_nullable, collation_name FROM information_schema.columns WHERE table_name = 'ddl_k_users' AND column_name = 'tz'`).Scan(&nullable, &coll); err != nil || nullable != "NO" || coll != "C" {
			t.Errorf("tz nullable %q collation %q, %v", nullable, coll, err)
		}
		if err := tx.QueryRowContext(ctx, `SELECT data_type, column_default FROM information_schema.columns WHERE table_name = 'ddl_k_users' AND column_name = 'nick'`).Scan(&typ, &def); err != nil || def.String != "'anon'::text" {
			t.Errorf("nick default %q, %v", def.String, err)
		}
		var emailColl sql.NullString
		if err := tx.QueryRowContext(ctx, `SELECT data_type, collation_name FROM information_schema.columns WHERE table_name = 'ddl_k_users' AND column_name = 'email'`).Scan(&typ, &emailColl); err != nil || typ != "character varying" || emailColl.String != "C" {
			t.Errorf("email type %q collation %q, %v", typ, emailColl.String, err)
		}
		var ident string
		var rls bool
		if err := tx.QueryRowContext(ctx, `SELECT is_identity FROM information_schema.columns WHERE table_name = 'ddl_k_users' AND column_name = 'id'`).Scan(&ident); err != nil || ident != "YES" {
			t.Errorf("id identity %q, %v", ident, err)
		}
		if err := tx.QueryRowContext(ctx, `SELECT relrowsecurity FROM pg_class WHERE relname = 'ddl_k_users'`).Scan(&rls); err != nil || !rls {
			t.Errorf("row security %v, %v", rls, err)
		}

		// A foreign key over existing rows, added NOT VALID then validated.
		s, err = pg.CreateTable(kSessions).Column(ddl.Column(kSessUser)).Build()
		runTx(t, tx, s, err)
		if _, err := tx.ExecContext(ctx, `INSERT INTO "ddl_k_sessions" ("user_id") VALUES (1)`); err != nil {
			t.Fatal(err)
		}
		fk := pg.ForeignKey(pg.TableConstraint{Table: kSessUser.Table(), Name: "ddl_k_sessions_user_fk"}, pg.RefPair(kSessUser, kUserID))
		s, err = pg.AlterTable(kSessions).Clause(pg.AddConstraintNotValid(fk)).Build()
		runTx(t, tx, s, err)
		var validated bool
		q := `SELECT convalidated FROM pg_constraint WHERE conname = 'ddl_k_sessions_user_fk'`
		if err := tx.QueryRowContext(ctx, q).Scan(&validated); err != nil || validated {
			t.Errorf("after NOT VALID convalidated = %v, %v", validated, err)
		}
		s, err = pg.AlterTable(kSessions).Clause(pg.ValidateConstraint(pg.TableConstraint{Table: kSessions, Name: "ddl_k_sessions_user_fk"}), pg.RenameConstraint(pg.TableConstraint{Table: kSessions, Name: "ddl_k_sessions_user_fk"}, pg.TableConstraint{Table: kSessions, Name: "ddl_k_sessions_owner_fk"})).Build()
		runTx(t, tx, s, err)
		if err := tx.QueryRowContext(ctx, `SELECT convalidated FROM pg_constraint WHERE conname = 'ddl_k_sessions_owner_fk'`).Scan(&validated); err != nil || !validated {
			t.Errorf("after VALIDATE and RENAME convalidated = %v, %v", validated, err)
		}
		for range 2 {
			s, err = pg.AlterTable(kSessions).Clause(pg.DropConstraint(pg.TableConstraint{Table: kSessions, Name: "ddl_k_sessions_owner_fk"}).IfExists(), pg.DropColumnIfExists(kSessUser)).Build()
			runTx(t, tx, s, err)
		}

		// Rename and move the table.
		if _, err := tx.ExecContext(ctx, `CREATE SCHEMA ddl_k_audit`); err != nil {
			t.Fatal(err)
		}
		s, err = pg.AlterTable(kUsers).Clause(pg.RenameTo(sqlb.Table{Name: "ddl_k_people"}), pg.SetSchema("ddl_k_audit")).Build()
		if err == nil && len(s) != 2 {
			t.Fatalf("RENAME TO and SET SCHEMA are standalone: %v", s.SQL())
		}
		// SET SCHEMA runs against the old name, so the rename must come after it.
		s, err = pg.AlterTable(kUsers).Clause(pg.SetSchema("ddl_k_audit")).Build()
		runTx(t, tx, s, err)
		s, err = pg.AlterTable(sqlb.Table{Name: "ddl_k_users", Schema: "ddl_k_audit"}).Clause(pg.RenameTo(sqlb.Table{Name: "ddl_k_people"})).Build()
		runTx(t, tx, s, err)
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM ddl_k_audit.ddl_k_people`).Scan(&n); err != nil || n != 2 {
			t.Errorf("moved and renamed table holds %d rows, %v", n, err)
		}
	})
}

type obStatus string

const (
	obActive    obStatus = "active"
	obBanned    obStatus = "banned"
	obSuspended obStatus = "suspended"
	obPaused    obStatus = "paused"
)

var (
	obSchema  = pg.Schema{Name: "ddl_o"}
	obEnum    = pg.Enum[obStatus]{Schema: "ddl_o", Name: "status"}
	obUsers   = sqlb.Table{Name: "users", Schema: "ddl_o", Alias: "u"}
	obID      = sqlb.Col[int64](obUsers, "id")
	obEmail   = sqlb.Col[string](obUsers, "email")
	obNick    = sqlb.Col[*string](obUsers, "nick")
	obStatCol = sqlb.Col[obStatus](obUsers, "status")
	obProfile = sqlb.Col[json.RawMessage](obUsers, "profile")
	obEmbed   = sqlb.Col[vector.Vector](obUsers, "embedding")
)

// build is a Build that must succeed, curried so its results pass through.
func build(t *testing.T) func(ddl.Script, error) ddl.Script {
	return func(s ddl.Script, err error) ddl.Script {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
}

// extAvailable reports whether the server ships an extension, so a test that
// installs one can skip it rather than fail on a server built without it; the
// catalogue's requireExts makes the same distinction for the whole suite.
func extAvailable(t *testing.T, tx *sql.Tx, name string) bool {
	t.Helper()
	var n int
	if err := tx.QueryRowContext(context.Background(), `SELECT count(*) FROM pg_available_extensions WHERE name = $1`, name).Scan(&n); err != nil {
		t.Fatalf("pg_available_extensions: %v", err)
	}
	return n > 0
}

// TestDDL_PostgresSchemasEnumsIndexesAndComments installs a schema, two
// extensions, an enum guarded against a second run, a table using the enum,
// every index form, and comments, all twice where the statement claims to be
// idempotent, then reads each back from the catalog.
func TestDDL_PostgresSchemasEnumsIndexesAndComments(t *testing.T) {
	inTx(t, func(tx *sql.Tx) {
		ctx := context.Background()
		// The table below has a vector column, so the whole case needs the
		// extension; a server without it skips loudly rather than failing.
		if !extAvailable(t, tx, "vector") {
			t.Skip("SKIPPED, NOT PASSED: this server does not ship the vector extension")
		}
		for range 2 {
			runB(t, tx, pg.CreateSchema(obSchema).IfNotExists())
			runB(t, tx, pg.CreateExtension(vector.Extension).IfNotExists())
			runB(t, tx, pg.CreateExtension(trgm.Extension).IfNotExists())
			runB(t, tx, pg.Guarded(pg.CreateEnum(obEnum, obActive, obBanned)))
		}
		if !failsAt(t, tx, build(t)(pg.CreateEnum(obEnum, obActive).Build()).SQL()[0]) {
			t.Fatal("an unguarded second CREATE TYPE succeeded; Guarded is not needed and the test is wrong")
		}
		runB(t, tx, pg.CreateTable(obUsers).IfNotExists().
			Column(ddl.Column(obID), ddl.Column(obEmail), ddl.Column(obNick), ddl.Column(obStatCol).As(obEnum).Default(obActive),
				ddl.Column(obProfile).As(ddl.RawType[json.RawMessage]("jsonb")).Nullable(), ddl.Column(obEmbed).As(ddl.RawType[vector.Vector]("vector(3)")).Nullable()).
			Clause(pg.PrimaryKey(obID)))

		ix := func(n string) pg.Index { return pg.Index{Schema: "ddl_o", Name: n} }
		for range 2 {
			for _, b := range []interface{ Build() (ddl.Script, error) }{
				pg.CreateIndex(ix("users_nick_idx"), obUsers).IfNotExists().On(pg.IndexKey(obNick)).Where(sqlb.IsNotNull(obNick)),
				pg.CreateIndex(ix("users_email_ci"), obUsers).Unique().IfNotExists().On(pg.IndexExpr(sqlb.Lower(obEmail))).Include(obStatCol),
				pg.CreateIndex(ix("users_status_id"), obUsers).IfNotExists().On(pg.IndexKey(obStatCol), pg.IndexKey(obID).Desc().NullsLast()),
				pg.CreateIndex(ix("users_profile_gin"), obUsers).IfNotExists().Using(pg.GIN).On(pg.IndexKey(obProfile).Opclass(pg.JSONBPathOps)),
				pg.CreateIndex(ix("users_nick_trgm"), obUsers).IfNotExists().Using(pg.GIN).On(pg.IndexKey(obNick).Opclass(pg.NullableOps(trgm.GinOps))),
				pg.CreateIndex(ix("users_embed_hnsw"), obUsers).IfNotExists().Using(vector.HNSW).On(pg.IndexKey(obEmbed).Opclass(vector.CosineOps)).With(vector.M(8), vector.EfConstruction(32)),
				pg.CreateIndex(ix("users_email_key"), obUsers).Unique().IfNotExists().On(pg.IndexKey(obEmail)).NullsNotDistinct(),
			} {
				runB(t, tx, b)
			}
		}
		var defs []string
		rows, err := tx.QueryContext(ctx, `SELECT indexdef FROM pg_indexes WHERE schemaname = 'ddl_o' ORDER BY indexname`)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var d string
			if err := rows.Scan(&d); err != nil {
				t.Fatal(err)
			}
			defs = append(defs, d)
		}
		all := strings.Join(defs, "\n")
		for _, want := range []string{
			"WHERE (nick IS NOT NULL)", "INCLUDE (status)", "(lower(email))", "status, id DESC NULLS LAST",
			"USING gin (profile jsonb_path_ops)", "USING gin (nick gin_trgm_ops)",
			"USING hnsw (embedding vector_cosine_ops) WITH (m='8', ef_construction='32')", "NULLS NOT DISTINCT",
		} {
			if !strings.Contains(all, want) {
				t.Errorf("no index definition contains %q:\n%s", want, all)
			}
		}

		// The enum: a label added inside the transaction cannot be used in it.
		runB(t, tx, pg.AlterEnum(obEnum).AddValue(obSuspended).Before(obBanned).IfNotExists())
		runB(t, tx, pg.AlterEnum(obEnum).AddValue(obSuspended).Before(obBanned).IfNotExists())
		if !failsAt(t, tx, `INSERT INTO ddl_o.users (id, email, status) VALUES (9, 'x', 'suspended')`) {
			t.Error("a label added in this transaction was usable in it; the documented caveat is wrong")
		}
		runB(t, tx, pg.AlterEnum(obEnum).RenameValue(obSuspended, obPaused))
		var labels string
		if err := tx.QueryRowContext(ctx, `SELECT string_agg(enumlabel, ',' ORDER BY enumsortorder) FROM pg_enum WHERE enumtypid = 'ddl_o.status'::regtype`).Scan(&labels); err != nil || labels != "active,paused,banned" {
			t.Errorf("labels %q, %v", labels, err)
		}

		// Comments, a hostile one stored exactly.
		hostile := "It's the owner's table; DROP TABLE x; -- \\ $$ /* \t"
		runB(t, tx, pg.CommentOnTable(obUsers).Is(hostile))
		runB(t, tx, pg.CommentOnColumn(obEmail).Is("login"))
		runB(t, tx, pg.CommentOn(ix("users_email_key")).Is("unique"))
		runB(t, tx, pg.CommentOn(obEnum).Is("lifecycle"))
		runB(t, tx, pg.CommentOn(obSchema).Is("auth"))
		var tc, cc, ic, ec, sc string
		err = tx.QueryRowContext(ctx, `SELECT obj_description('ddl_o.users'::regclass, 'pg_class'), col_description('ddl_o.users'::regclass, 2),
			obj_description('ddl_o.users_email_key'::regclass, 'pg_class'), obj_description('ddl_o.status'::regtype, 'pg_type'),
			obj_description('ddl_o'::regnamespace, 'pg_namespace')`).Scan(&tc, &cc, &ic, &ec, &sc)
		if err != nil || tc != hostile || cc != "login" || ic != "unique" || ec != "lifecycle" || sc != "auth" {
			t.Errorf("comments %q %q %q %q %q, %v", tc, cc, ic, ec, sc, err)
		}
		runB(t, tx, pg.CommentOnTable(obUsers).IsNull())
		var gone sql.NullString
		if err := tx.QueryRowContext(ctx, `SELECT obj_description('ddl_o.users'::regclass, 'pg_class')`).Scan(&gone); err != nil || gone.Valid {
			t.Errorf("IS NULL left %v, %v", gone, err)
		}

		// A guarded named constraint twice; renaming and dropping indexes.
		// Each constraint kind twice: a unique key raises duplicate_table on
		// the second run and a check duplicate_object, and each is caught.
		for range 2 {
			runB(t, tx, pg.Guarded(pg.AddConstraintStatement(pg.Unique(pg.TableConstraint{Table: obNick.Table(), Name: "users_nick_key"}, obNick))))
			runB(t, tx, pg.Guarded(pg.AddConstraintStatement(pg.Check(pg.TableConstraint{Table: obUsers, Name: "users_id_positive"}, sqlb.Greater(obID, 0)))))
			runB(t, tx, pg.Guarded(pg.AddConstraintStatement(pg.ForeignKey(pg.TableConstraint{Table: obID.Table(), Name: "users_self_fk"}, pg.RefPair(obID, obID)))))
		}
		runB(t, tx, pg.RenameIndex(ix("users_nick_idx"), ix("users_by_nick")).IfExists())
		for range 2 {
			runB(t, tx, pg.DropIndex(ix("users_by_nick"), ix("users_nick_trgm")).IfExists())
		}
		renamed := pg.Schema{Name: "ddl_o_renamed"}
		runB(t, tx, pg.RenameSchema(obSchema, renamed))
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM ddl_o_renamed.users`).Scan(&n); err != nil {
			t.Errorf("the table did not move with its schema: %v", err)
		}
		for range 2 {
			runB(t, tx, pg.DropType(pg.Enum[obStatus]{Schema: renamed.Name, Name: "status"}).IfExists().Cascade())
			runB(t, tx, pg.DropSchema(renamed).IfExists().Cascade())
		}
	})
}

// TestDDL_PostgresConcurrentIndexOutsideATransaction runs the two statements
// marked non-transactional the only way Postgres accepts them, and checks that
// inside a transaction it refuses, which is what the flag claims.
func TestDDL_PostgresConcurrentIndexOutsideATransaction(t *testing.T) {
	db := openOrSkip(t)
	ctx := context.Background()
	tbl := sqlb.Table{Name: "ddl_conc"}
	col := sqlb.Col[int64](tbl, "id")
	ix := pg.Index{Name: "ddl_conc_id_idx"}
	t.Cleanup(func() { _, _ = db.ExecContext(ctx, `DROP TABLE IF EXISTS "ddl_conc"`) })
	run := func(s ddl.Script, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		for _, st := range s {
			if _, err := db.ExecContext(ctx, st.SQL); err != nil {
				t.Fatalf("%s: %v", st.SQL, err)
			}
		}
	}
	run(pg.CreateTable(tbl).IfNotExists().Column(ddl.Column(col)).Build())
	create := build(t)(pg.CreateIndex(ix, tbl).Concurrently().IfNotExists().On(pg.IndexKey(col)).Build())
	if create.Transactional() {
		t.Fatal("CREATE INDEX CONCURRENTLY claims to be transactional")
	}
	inTx(t, func(tx *sql.Tx) {
		if !failsAt(t, tx, create[0].SQL) {
			t.Error("Postgres accepted CREATE INDEX CONCURRENTLY in a transaction; the flag is wrong")
		}
	})
	run(create, nil)
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM pg_indexes WHERE indexname = 'ddl_conc_id_idx'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("index count %d, %v", n, err)
	}
	run(pg.DropIndex(ix).Concurrently().IfExists().Build())
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM pg_indexes WHERE indexname = 'ddl_conc_id_idx'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("index count after drop %d, %v", n, err)
	}
}

// runB builds b and runs it in tx, as runTx does.
func runB(t *testing.T, tx *sql.Tx, b interface{ Build() (ddl.Script, error) }) {
	t.Helper()
	s, err := b.Build()
	runTx(t, tx, s, err)
}

// TestDDL_PostgresExclusionAndLike has Postgres enforce an exclusion
// constraint, a room booked for two overlapping ranges, scoped to live rows,
// guards adding it twice, and copies the table with LIKE, checking that the
// generated column stays generated only when INCLUDING GENERATED is written.
func TestDDL_PostgresExclusionAndLike(t *testing.T) {
	inTx(t, func(tx *sql.Tx) {
		ctx := context.Background()
		runB(t, tx, pg.CreateExtension(pg.Extension{Name: "btree_gist"}).IfNotExists())
		rooms := sqlb.Table{Name: "ddl_bookings", Alias: "b"}
		room := sqlb.Col[int64](rooms, "room")
		during := sqlb.Col[pg.Range[time.Time]](rooms, "during")
		cancelled := sqlb.Col[bool](rooms, "cancelled")
		label := sqlb.Col[string](rooms, "label")
		ex := pg.Exclude(pg.TableConstraint{Table: room.Table(), Name: "ddl_bookings_no_overlap"}, pg.GiST, pg.ExcludeOn(room, pg.OpEquals), pg.ExcludeOn(during, pg.OpOverlaps)).
			Where(sqlb.Eq(cancelled, false))
		runB(t, tx, pg.CreateTable(rooms).
			Column(ddl.Column(room), ddl.Column(during).As(ddl.RawType[pg.Range[time.Time]]("tstzrange")), ddl.Column(cancelled).Default(false), ddl.Column(label)).
			Clause(pg.GeneratedStored(label, sqlb.Literal[string]("'booking'")), ex))
		for range 2 {
			runB(t, tx, pg.Guarded(pg.AddConstraintStatement(ex)))
		}
		book := func(r int64, from, to string, c bool) string {
			return fmt.Sprintf(`INSERT INTO "ddl_bookings" ("room", "during", "cancelled") VALUES (%d, tstzrange('%s', '%s'), %v)`, r, from, to, c)
		}
		if failsAt(t, tx, book(1, "2026-01-01 10:00Z", "2026-01-01 11:00Z", false)) {
			t.Fatal("the first booking was refused")
		}
		if !failsAt(t, tx, book(1, "2026-01-01 10:30Z", "2026-01-01 11:30Z", false)) {
			t.Error("an overlapping booking of the same room was accepted")
		}
		if failsAt(t, tx, book(2, "2026-01-01 10:30Z", "2026-01-01 11:30Z", false)) {
			t.Error("another room's booking was refused; the equality element is missing")
		}
		if failsAt(t, tx, book(1, "2026-01-01 10:30Z", "2026-01-01 11:30Z", true)) {
			t.Error("a cancelled overlapping booking was refused; the WHERE did not reach the constraint")
		}

		plain, full := sqlb.Table{Name: "ddl_bookings_plain"}, sqlb.Table{Name: "ddl_bookings_full"}
		runB(t, tx, pg.CreateTableLike(plain, rooms).IfNotExists())
		runB(t, tx, pg.CreateTableLike(full, rooms, pg.IncludingDefaults, pg.IncludingConstraints, pg.IncludingGenerated, pg.IncludingIndexes).IfNotExists())
		gen := func(tbl string) string {
			var g string
			if err := tx.QueryRowContext(ctx, `SELECT is_generated FROM information_schema.columns WHERE table_name = $1 AND column_name = 'label'`, tbl).Scan(&g); err != nil {
				t.Fatal(err)
			}
			return g
		}
		if gen("ddl_bookings_plain") != "NEVER" || gen("ddl_bookings_full") != "ALWAYS" {
			t.Errorf("LIKE copied generated columns as plain %q, full %q", gen("ddl_bookings_plain"), gen("ddl_bookings_full"))
		}
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM pg_constraint WHERE conrelid = 'ddl_bookings_full'::regclass AND contype = 'x'`).Scan(&n); err != nil || n != 1 {
			t.Errorf("LIKE INCLUDING INDEXES copied %d exclusion constraints, %v", n, err)
		}
	})
}

type vInvoiceNo int32

// TestDDL_PostgresSequencesAndViews creates a sequence used as a default, a
// view over a query-builder body whose bound values became literals, a check
// option that refuses a write, and a materialized view refreshed concurrently
// through a unique index, all read back from the server.
func TestDDL_PostgresSequencesAndViews(t *testing.T) {
	inTx(t, func(tx *sql.Tx) {
		ctx := context.Background()
		seq := pg.Sequence[vInvoiceNo]{Name: "ddl_v_invoice_no"}
		inv := sqlb.Table{Name: "ddl_v_invoices", Alias: "i"}
		num := sqlb.Col[vInvoiceNo](inv, "number")
		status := sqlb.Col[string](inv, "status")
		for range 2 {
			runB(t, tx, pg.CreateSequence(seq).IfNotExists().Start(1000).IncrementBy(10).Cache(5))
		}
		runB(t, tx, pg.CreateTable(inv).Column(ddl.Column(num).As(ddl.RawType[vInvoiceNo]("integer")).DefaultExpr(pg.NextVal(seq)), ddl.Column(status)))
		runB(t, tx, pg.AlterSequence(seq).OwnedBy(num))
		if _, err := tx.ExecContext(ctx, `INSERT INTO ddl_v_invoices (status) VALUES ('open'), ('paid'), ('open')`); err != nil {
			t.Fatal(err)
		}
		var typ string
		if err := tx.QueryRowContext(ctx, `SELECT data_type FROM information_schema.sequences WHERE sequence_name = 'ddl_v_invoice_no'`).Scan(&typ); err != nil || typ != "integer" {
			t.Errorf("sequence type %q, %v", typ, err)
		}
		var nums string
		if err := tx.QueryRowContext(ctx, `SELECT string_agg(number::text, ',' ORDER BY number) FROM ddl_v_invoices`).Scan(&nums); err != nil || nums != "1000,1010,1020" {
			t.Errorf("numbers %q, %v", nums, err)
		}
		runB(t, tx, pg.AlterSequence(seq).Restart(5000).IfExists())
		var next int64
		if err := tx.QueryRowContext(ctx, `SELECT nextval('ddl_v_invoice_no')`).Scan(&next); err != nil || next != 5000 {
			t.Errorf("after RESTART nextval %d, %v", next, err)
		}

		open := pg.View{Name: "ddl_v_open"}
		body := pg.Select(num, status).From(inv).Where(sqlb.Eq(status, "open"))
		for range 2 {
			runB(t, tx, pg.CreateView(open).OrReplace().As(body).WithCheckOption(pg.Local))
		}
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM ddl_v_open`).Scan(&n); err != nil || n != 2 {
			t.Errorf("view rows %d, %v", n, err)
		}
		if !failsAt(t, tx, `INSERT INTO ddl_v_open (number, status) VALUES (1, 'paid')`) {
			t.Error("WITH CHECK OPTION accepted a row the view does not show")
		}
		runB(t, tx, pg.RenameView(open, pg.View{Name: "ddl_v_unpaid"}).IfExists())
		for range 2 {
			runB(t, tx, pg.DropView(pg.View{Name: "ddl_v_unpaid"}).IfExists())
		}

		counts := pg.MaterializedView{Name: "ddl_v_counts"}
		countsT := sqlb.Table{Name: "ddl_v_counts"}
		cst := sqlb.Col[string](countsT, "status")
		for range 2 {
			runB(t, tx, pg.CreateMaterializedView(counts).IfNotExists().As(pg.Select(status).From(inv).GroupBy(status)).WithNoData())
		}
		if !failsAt(t, tx, `SELECT count(*) FROM ddl_v_counts`) {
			t.Error("a WITH NO DATA view was readable before its first refresh")
		}
		runB(t, tx, pg.CreateIndex(pg.Index{Name: "ddl_v_counts_status"}, countsT).Unique().On(pg.IndexKey(cst)))
		runB(t, tx, pg.RefreshMaterializedView(counts))
		runB(t, tx, pg.RefreshMaterializedView(counts).Concurrently())
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM ddl_v_counts`).Scan(&n); err != nil || n != 2 {
			t.Errorf("materialized rows %d, %v", n, err)
		}
		runB(t, tx, pg.CommentOn(counts).Is("per status"))
		runB(t, tx, pg.DropMaterializedView(counts).IfExists())
		runB(t, tx, pg.DropSequence(seq).IfExists().Cascade())
	})
}

// TestDDL_PostgresTriggersAndPartitions installs a trigger function and a
// trigger that maintains updated_at only when the email changes, then a range
// partitioned table whose rows Postgres routes by the bound, a partition
// attached and detached, and reads each effect back.
func TestDDL_PostgresTriggersAndPartitions(t *testing.T) {
	inTx(t, func(tx *sql.Tx) {
		ctx := context.Background()
		users := sqlb.Table{Name: "ddl_t_users", Alias: "u"}
		id := sqlb.Col[int64](users, "id")
		email := sqlb.Col[string](users, "email")
		nick := sqlb.Col[*string](users, "nick")
		touched := sqlb.Col[int64](users, "touched")
		runB(t, tx, pg.CreateTable(users).Column(ddl.Column(id), ddl.Column(email), ddl.Column(nick), ddl.Column(touched).Default(0)))
		fn := pg.Function{Name: "ddl_t_touch"}
		tr := pg.Trigger{Name: "ddl_t_users_touch", Table: users}
		for range 2 {
			runB(t, tx, pg.CreateTriggerFunction(fn, "\nBEGIN NEW.touched := OLD.touched + 1; RETURN NEW; END\n").OrReplace())
			runB(t, tx, pg.CreateTrigger(tr).OrReplace().Before(pg.OnUpdateOf(email, nick)).ForEachRow().
				When(pg.IsDistinctFrom(pg.Old(email), pg.New(email))).Execute(fn))
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO ddl_t_users (id, email) VALUES (1, 'a')`); err != nil {
			t.Fatal(err)
		}
		for _, q := range []string{
			`UPDATE ddl_t_users SET email = 'b'`,       // fires
			`UPDATE ddl_t_users SET email = 'b'`,       // WHEN false: same email
			`UPDATE ddl_t_users SET nick = 'x'`,        // UPDATE OF nick, WHEN false
			`UPDATE ddl_t_users SET touched = touched`, // not UPDATE OF email or nick
		} {
			if _, err := tx.ExecContext(ctx, q); err != nil {
				t.Fatal(err)
			}
		}
		var n int64
		if err := tx.QueryRowContext(ctx, `SELECT touched FROM ddl_t_users`).Scan(&n); err != nil || n != 1 {
			t.Errorf("the trigger fired %d times, want 1: %v", n, err)
		}
		runB(t, tx, pg.CommentOn(tr).Is("keeps touched"))
		renamed := pg.Trigger{Name: "ddl_t_users_touched", Table: users}
		runB(t, tx, pg.RenameTrigger(tr, renamed))
		for range 2 {
			runB(t, tx, pg.DropTrigger(renamed).IfExists())
		}
		runB(t, tx, pg.DropFunction(fn).IfExists())

		events := sqlb.Table{Name: "ddl_t_events", Alias: "e"}
		eid := sqlb.Col[int64](events, "id")
		at := sqlb.Col[time.Time](events, "at")
		runB(t, tx, pg.CreateTable(events).Column(ddl.Column(eid), ddl.Column(at)).Clause(pg.PrimaryKey(eid, at), pg.PartitionByRange(at)))
		sep, oct, aug := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
		sepT := sqlb.Table{Name: "ddl_t_events_2026_09"}
		for range 2 {
			runB(t, tx, pg.CreatePartition(sepT, events).IfNotExists().For(pg.FromTo(at, sep, oct)))
		}
		runB(t, tx, pg.CreatePartition(sqlb.Table{Name: "ddl_t_events_other"}, events).For(pg.DefaultPartition()))
		if _, err := tx.ExecContext(ctx, `INSERT INTO ddl_t_events VALUES (1, '2026-09-15Z'), (2, '2026-11-15Z')`); err != nil {
			t.Fatal(err)
		}
		var inSep int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM ddl_t_events_2026_09`).Scan(&inSep); err != nil || inSep != 1 {
			t.Errorf("September partition holds %d rows, %v", inSep, err)
		}
		augT := sqlb.Table{Name: "ddl_t_events_2026_08"}
		runB(t, tx, pg.CreateTableLike(augT, events, pg.IncludingDefaults))
		runB(t, tx, pg.AlterTable(events).Clause(pg.AttachPartition(augT, pg.FromTo(at, aug, sep))))
		var parts int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM pg_inherits WHERE inhparent = 'ddl_t_events'::regclass`).Scan(&parts); err != nil || parts != 3 {
			t.Errorf("partitions after ATTACH %d, %v", parts, err)
		}
		runB(t, tx, pg.DetachPartition(events, augT))
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM pg_inherits WHERE inhparent = 'ddl_t_events'::regclass`).Scan(&parts); err != nil || parts != 2 {
			t.Errorf("partitions after DETACH %d, %v", parts, err)
		}
		conc := build(t)(pg.DetachPartition(events, sepT).Concurrently().Build())
		if !failsAt(t, tx, conc[0].SQL) {
			t.Error("Postgres accepted DETACH ... CONCURRENTLY in a transaction; the flag is wrong")
		}
	})
}

// TestDDL_PostgresPoliciesAndTier4 has Postgres enforce a row-level security
// policy for a role that does not own the table, a domain's checks, and a
// nondeterministic collation that makes a unique email case-insensitive, then
// runs the statistics, extension and rename forms and reads each back.
func TestDDL_PostgresPoliciesAndTier4(t *testing.T) {
	inTx(t, func(tx *sql.Tx) {
		ctx := context.Background()
		sess := sqlb.Table{Name: "ddl_p_sessions", Alias: "s"}
		owner := sqlb.Col[int64](sess, "user_id")
		runB(t, tx, pg.CreateTable(sess).Column(ddl.Column(owner)))
		runB(t, tx, pg.AlterTable(sess).Clause(pg.EnableRowLevelSecurity()))
		me := sqlb.Cast[int64](pg.CurrentSetting("app.user_id"), "bigint")
		pol := pg.Policy{Name: "ddl_p_owner", Table: sess}
		for range 2 {
			runB(t, tx, pg.Guarded(pg.CreatePolicy(pol).For(pg.AllCommands).To(pg.RolePublic).
				Using(sqlb.EqExpr(owner, me)).WithCheck(sqlb.EqExpr(owner, me))))
		}
		for _, q := range []string{
			`INSERT INTO ddl_p_sessions VALUES (1), (2), (2)`,
			`CREATE ROLE ddl_p_reader NOLOGIN`,
			`GRANT SELECT, INSERT ON ddl_p_sessions TO ddl_p_reader`,
			`SET LOCAL ROLE ddl_p_reader`,
			`SET LOCAL app.user_id = '2'`,
		} {
			if _, err := tx.ExecContext(ctx, q); err != nil {
				t.Fatalf("%s: %v", q, err)
			}
		}
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM ddl_p_sessions`).Scan(&n); err != nil || n != 2 {
			t.Errorf("the policy showed %d rows to user 2, want 2: %v", n, err)
		}
		if !failsAt(t, tx, `INSERT INTO ddl_p_sessions VALUES (1)`) {
			t.Error("WITH CHECK accepted a row for another user")
		}
		if _, err := tx.ExecContext(ctx, `RESET ROLE`); err != nil {
			t.Fatal(err)
		}
		runB(t, tx, pg.AlterPolicy(pol).Using(sqlb.Greater(owner, int64(0))))
		runB(t, tx, pg.AlterPolicy(pol).RenameTo(pg.Policy{Name: "ddl_p_self", Table: sess}))
		runB(t, tx, pg.CommentOn(pg.Policy{Name: "ddl_p_self", Table: sess}).Is("own rows"))
		for range 2 {
			runB(t, tx, pg.DropPolicy(pg.Policy{Name: "ddl_p_self", Table: sess}).IfExists())
		}

		dom := pg.Domain[string]{Name: "ddl_p_email"}
		for range 2 {
			runB(t, tx, pg.Guarded(pg.CreateDomain(dom, ddl.RawType[string]("text")).Check(pg.DomainConstraint[string]{Domain: dom, Name: "ddl_p_email_at"}, pg.Regex(pg.DomainValue[string](), "^[^@]+@[^@]+$"))))
		}
		runB(t, tx, pg.AlterDomain(dom).AddConstraint(pg.DomainConstraint[string]{Domain: dom, Name: "ddl_p_email_len"}, sqlb.LessEq(pg.Length(pg.DomainValue[string]()), 12)).NotValid())
		runB(t, tx, pg.AlterDomain(dom).ValidateConstraint(pg.DomainConstraint[string]{Domain: dom, Name: "ddl_p_email_len"}))
		ci := pg.Collation{Name: "ddl_p_ci"}
		for range 2 {
			runB(t, tx, pg.CreateCollation(ci, "und-u-ks-level2").ICU().Nondeterministic().IfNotExists())
		}
		acc := sqlb.Table{Name: "ddl_p_accounts"}
		email := sqlb.Col[string](acc, "email")
		tenant := sqlb.Col[int64](acc, "tenant")
		runB(t, tx, pg.CreateTable(acc).Column(ddl.Column(email).As(dom), ddl.Column(tenant)).
			Clause(pg.ColumnCollation(email, ci), pg.Unique(pg.TableConstraint{Table: email.Table(), Name: "ddl_p_accounts_email"}, email)))
		if !failsAt(t, tx, `INSERT INTO ddl_p_accounts VALUES ('no-at-sign', 1)`) {
			t.Error("the domain's CHECK accepted a value without @")
		}
		if !failsAt(t, tx, `INSERT INTO ddl_p_accounts VALUES ('much@too-long.example', 1)`) {
			t.Error("the domain constraint added later did not apply")
		}
		if failsAt(t, tx, `INSERT INTO ddl_p_accounts VALUES ('Ada@x.io', 1)`) {
			t.Fatal("a valid email was refused")
		}
		if !failsAt(t, tx, `INSERT INTO ddl_p_accounts VALUES ('ada@X.IO', 1)`) {
			t.Error("the nondeterministic collation did not make the unique email case-insensitive")
		}
		runB(t, tx, pg.AlterDomain(dom).DropConstraint(pg.DomainConstraint[string]{Domain: dom, Name: "ddl_p_email_len"}))
		st := pg.Statistics{Name: "ddl_p_tenant_email"}
		for range 2 {
			runB(t, tx, pg.CreateStatistics(st, acc, tenant, email).IfNotExists().Kinds(pg.Dependencies, pg.NDistinct, pg.MCV))
		}
		var kinds string
		if err := tx.QueryRowContext(ctx, `SELECT stxkind::text FROM pg_statistic_ext WHERE stxname = 'ddl_p_tenant_email'`).Scan(&kinds); err != nil || kinds != "{d,f,m}" {
			t.Errorf("statistics kinds %q, %v", kinds, err)
		}
		runB(t, tx, pg.DropStatistics(st).IfExists())
		if extAvailable(t, tx, "vector") {
			runB(t, tx, pg.CreateExtension(vector.Extension).IfNotExists())
			runB(t, tx, pg.UpdateExtension(vector.Extension))
		} else {
			t.Log("SKIPPED, NOT PASSED: CREATE and ALTER EXTENSION; this server does not ship vector")
		}
		mv := pg.MaterializedView{Name: "ddl_p_mv"}
		runB(t, tx, pg.CreateMaterializedView(mv).As(pg.Select(tenant).From(acc)))
		runB(t, tx, pg.RenameMaterializedView(mv, pg.MaterializedView{Name: "ddl_p_mv2"}).IfExists())
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM ddl_p_mv2`).Scan(&n); err != nil {
			t.Errorf("the renamed materialized view: %v", err)
		}
		runB(t, tx, pg.CommentOn(dom).Is("email"))
		runB(t, tx, pg.CommentOn(ci).Is("ci"))
		runB(t, tx, pg.DropTable(acc).Clause(pg.Cascade()))
		for range 2 {
			runB(t, tx, pg.DropDomain(dom).IfExists().Cascade())
			runB(t, tx, pg.DropCollation(ci).IfExists())
		}
	})
}
