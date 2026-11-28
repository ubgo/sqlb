package enginetest

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/contrib/migrate"
	"github.com/ubgo/sqlb/contrib/vet"
	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/examples/prefixed/widgets"
	"github.com/ubgo/sqlb/pg"
	"github.com/ubgo/sqlb/render"
	"github.com/ubgo/sqlb/schema"
	"github.com/ubgo/sqlb/sqlite"
)

// The declare-once path for a library with a run-time table prefix, end to
// end on a real database: tables written once (examples/prefixed/widgets),
// numbered migrations written with the ddl builders and applied by the
// contrib/migrate ledger, sqlb vet's check of the result against the
// declaration's Describe, and the library's queries (ON CONFLICT, RETURNING,
// FOR UPDATE) under two prefixes in one database.

type engineCase struct {
	engine  widgets.Engine
	dialect render.Dialect
	name    string
	open    func(t *testing.T) *sql.DB
	// legacy is what an earlier release created with hand-written
	// CREATE TABLE IF NOT EXISTS strings, for prefix "legacy_".
	legacy []string
}

func engineCases(t *testing.T) []engineCase {
	cases := []engineCase{{
		engine: widgets.SQLite, dialect: sqlite.Dialect{}, name: render.DialectSQLite,
		open: func(t *testing.T) *sql.DB {
			// A file, not :memory:, so every pooled connection sees one
			// database; foreign keys on, as contrib/driver-sqlite sets them.
			db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "w.db")+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			return db
		},
		legacy: []string{
			`CREATE TABLE IF NOT EXISTS legacy_accounts (id INTEGER NOT NULL, email TEXT NOT NULL, active INTEGER NOT NULL DEFAULT 1, created_at TIMESTAMP NOT NULL, PRIMARY KEY (id))`,
			`CREATE UNIQUE INDEX IF NOT EXISTS legacy_accounts_email_key ON legacy_accounts (email)`,
			`CREATE TABLE IF NOT EXISTS legacy_members (account_id INTEGER NOT NULL, team TEXT NOT NULL, role TEXT NOT NULL DEFAULT 'member', PRIMARY KEY (account_id, team), FOREIGN KEY (account_id) REFERENCES legacy_accounts (id) ON DELETE CASCADE)`,
		},
	}}
	if dsn := os.Getenv("SQLB_PG_DSN"); dsn != "" {
		cases = append(cases, engineCase{
			engine: widgets.Postgres, dialect: pg.Dialect{}, name: render.DialectPostgres,
			open: func(t *testing.T) *sql.DB {
				db, err := sql.Open("pgx", dsn)
				if err != nil {
					t.Fatal(err)
				}
				drop := func() {
					for _, p := range []string{"", "acme_", "legacy_"} {
						_, _ = db.Exec(`DROP TABLE IF EXISTS ` + p + `members, ` + p + `accounts, ` + p + `sqlb_migrations`)
					}
				}
				drop()
				t.Cleanup(func() { drop(); _ = db.Close() })
				return db
			},
			legacy: []string{
				`CREATE TABLE IF NOT EXISTS legacy_accounts (id bigint NOT NULL PRIMARY KEY, email text NOT NULL, active boolean NOT NULL DEFAULT true, created_at timestamptz NOT NULL)`,
				`CREATE UNIQUE INDEX IF NOT EXISTS legacy_accounts_email_key ON legacy_accounts (email)`,
				`CREATE TABLE IF NOT EXISTS legacy_members (account_id bigint NOT NULL REFERENCES legacy_accounts (id) ON DELETE CASCADE, team text NOT NULL, role text NOT NULL DEFAULT 'member', PRIMARY KEY (account_id, team))`,
			},
		})
	} else {
		t.Log("SKIPPED, NOT PASSED: the Postgres half needs SQLB_PG_DSN")
	}
	return cases
}

// install applies the library's numbered migrations through the ledger, the
// way the library's own Migrate function would, and returns what ran.
func install(t *testing.T, ctx context.Context, db *sql.DB, c engineCase, tables widgets.Tables, prefix string) []string {
	t.Helper()
	ms, err := tables.Migrations(c.engine)
	if err != nil {
		t.Fatal(err)
	}
	led, err := migrate.NewLedger(c.dialect, prefix+"sqlb_migrations", migrate.EngineLock(c.dialect, prefix+widgets.Owner))
	if err != nil {
		t.Fatal(err)
	}
	ran, err := led.RefuseUnknown().ApplyDB(ctx, db, toLedger(ms)...)
	if err != nil {
		t.Fatal(err)
	}
	return ran
}

func toLedger(ms []widgets.Migration) []migrate.Migration {
	out := make([]migrate.Migration, len(ms))
	for i, m := range ms {
		out[i] = migrate.Migration{Owner: widgets.Owner, Name: m.Name, Script: m.Script}
	}
	return out
}

// selfCheck is the test check: the live tables against the
// declaration's description, through sqlb vet's library function. Only the
// library's own tables are passed, so another prefix's tables in the same
// database are not judged.
func selfCheck(t *testing.T, ctx context.Context, db *sql.DB, c engineCase, tables widgets.Tables) {
	t.Helper()
	desc, err := tables.Describe(c.engine)
	if err != nil {
		t.Fatal(err)
	}
	fs, err := vet.CheckTables(ctx, db, c.name, desc)
	if err != nil {
		t.Fatal(err)
	}
	// A test asserts no finding at all, warnings included: a column the
	// migrations create and the declaration lacks is only a warning, since
	// at start-up it may be another release's, but in a test it is the drift
	// this check exists to find.
	if len(fs) != 0 {
		t.Fatalf("the live schema differs from the declaration: %v", fs)
	}
}

func TestTask_DeclareOnceWithAPrefix(t *testing.T) {
	for _, c := range engineCases(t) {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			db := c.open(t)
			for _, prefix := range []string{"acme_", ""} {
				tables, err := widgets.NewTables(prefix)
				if err != nil {
					t.Fatal(err)
				}
				if ran := install(t, ctx, db, c, tables, prefix); !slices.Equal(ran, []string{widgets.Owner + "/0001", widgets.Owner + "/0002", widgets.Owner + "/0003"}) {
					t.Fatalf("%q: ran %v", prefix, ran)
				}
				if ran := install(t, ctx, db, c, tables, prefix); len(ran) != 0 {
					t.Fatalf("%q: a second deploy ran %v", prefix, ran)
				}
				selfCheck(t, ctx, db, c, tables)
				exercise(t, ctx, db, c, tables)
			}
		})
	}
}

// exercise runs every query the library has: an upsert that inserts and
// then conflicts, with RETURNING; an insert that ignores a duplicate; a
// locking read in a transaction; and a listing.
func exercise(t *testing.T, ctx context.Context, db *sql.DB, c engineCase, tables widgets.Tables) {
	t.Helper()
	s := widgets.Store{T: tables, Engine: c.engine, DB: db}
	born := time.Date(1990, 5, 17, 0, 0, 0, 0, time.UTC)
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	in := widgets.Account{ID: 1, Email: "alice@example.com", Active: true, CreatedAt: created, Birthday: &born, Profile: json.RawMessage(`{"plan":"free"}`), Avatar: []byte{0, 1, 2}}
	var got widgets.Account
	if err := s.Upsert(ctx, in, &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != 1 || got.Email != in.Email || !got.Active || !got.CreatedAt.Equal(created) || got.Birthday == nil || !got.Birthday.Equal(born) || len(got.Avatar) != 3 {
		t.Fatalf("returned %+v", got)
	}
	in.ID, in.Active, in.Profile = 99, false, json.RawMessage(`{"plan":"pro"}`)
	if err := s.Upsert(ctx, in, &got); err != nil {
		t.Fatal(err)
	}
	var profile map[string]string
	if err := json.Unmarshal(got.Profile, &profile); err != nil {
		t.Fatal(err)
	}
	if got.ID != 1 || got.Active || profile["plan"] != "pro" {
		t.Fatalf("the conflict did not update the existing row: %+v", got)
	}
	for range 2 {
		if err := s.Join(ctx, 1, "blue"); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Join(ctx, 1, "amber"); err != nil {
		t.Fatal(err)
	}
	teams, err := s.Teams(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(teams, []string{"amber", "blue"}) {
		t.Fatalf("teams %v", teams)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	var locked widgets.Account
	if err := s.Lock(ctx, tx, 1, &locked); err != nil {
		t.Fatal(err)
	}
	if locked.Email != in.Email {
		t.Fatalf("locked %+v", locked)
	}
}

// TestTask_AdoptTablesAnEarlierReleaseCreated is the upgrade of a database an
// earlier release created with hand-written CREATE TABLE IF NOT EXISTS
// strings and no ledger: migration 0001 finds every table and index already
// there and changes nothing, 0002 adds the new columns, 0003 the partial
// owner index, the row survives and the live schema matches the declaration.
func TestTask_AdoptTablesAnEarlierReleaseCreated(t *testing.T) {
	for _, c := range engineCases(t) {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			db := c.open(t)
			for _, q := range c.legacy {
				if _, err := db.ExecContext(ctx, q); err != nil {
					t.Fatalf("%s: %v", q, err)
				}
			}
			ts := "2025-06-01 10:00:00+00:00"
			if _, err := db.ExecContext(ctx, `INSERT INTO legacy_accounts (id, email, created_at) VALUES (7, 'bob@example.com', '`+ts+`')`); err != nil {
				t.Fatal(err)
			}
			tables, err := widgets.NewTables("legacy_")
			if err != nil {
				t.Fatal(err)
			}
			if ran := install(t, ctx, db, c, tables, "legacy_"); !slices.Equal(ran, []string{widgets.Owner + "/0001", widgets.Owner + "/0002", widgets.Owner + "/0003"}) {
				t.Fatalf("ran %v", ran)
			}
			selfCheck(t, ctx, db, c, tables)
			var email string
			var active bool
			if err := db.QueryRowContext(ctx, `SELECT email, active FROM legacy_accounts WHERE id = 7`).Scan(&email, &active); err != nil {
				t.Fatal(err)
			}
			if email != "bob@example.com" || !active {
				t.Fatalf("the earlier release's row changed: %q %v", email, active)
			}
		})
	}
}

// TestTask_ABorrowingPackageGetsTypedColumnsFromTheDescription is the other
// direction of the one-declaration path: a package that has only the
// library's schema.Table, not its Go values, gets typed columns with
// schema.MustCol, checked against the declared types.
func TestTask_ABorrowingPackageGetsTypedColumnsFromTheDescription(t *testing.T) {
	c := engineCases(t)[0]
	ctx := context.Background()
	db := c.open(t)
	tables, err := widgets.NewTables("acme_")
	if err != nil {
		t.Fatal(err)
	}
	install(t, ctx, db, c, tables, "acme_")
	if _, err := db.ExecContext(ctx, `INSERT INTO acme_accounts (id, email, created_at) VALUES (3, 'carol@example.com', '2026-01-01 00:00:00+00:00')`); err != nil {
		t.Fatal(err)
	}
	desc, err := tables.Describe(c.engine)
	if err != nil {
		t.Fatal(err)
	}
	accounts := desc[0]
	accounts.Alias = "ba"
	email := schema.MustCol[string](accounts, "email")
	id := schema.MustCol[int64](accounts, "id")
	var got string
	q, err := sqlite.Select().Columns(sqlb.Bind(email, &got)).From(accounts.Ref()).Where(sqlb.Eq(id, int64(3))).Build()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlb.One(ctx, db, q); err != nil || got != "carol@example.com" {
		t.Fatalf("%q %v", got, err)
	}
	if _, err := schema.Col[int64](accounts, "email"); !errors.Is(err, schema.ErrTypeMismatch) {
		t.Fatalf("a wrong Go type was accepted: %v", err)
	}
}

// TestTask_AnEditedReleasedMigrationIsRefused pins what keeps the numbered
// migrations honest: a released migration edited after a database applied
// it, here a column's type changed in 0001, fails the next deploy with
// ErrChecksumMismatch instead of being skipped.
func TestTask_AnEditedReleasedMigrationIsRefused(t *testing.T) {
	c := engineCases(t)[0]
	ctx := context.Background()
	db := c.open(t)
	tables, err := widgets.NewTables("")
	if err != nil {
		t.Fatal(err)
	}
	install(t, ctx, db, c, tables, "")
	ms, err := tables.Migrations(c.engine)
	if err != nil {
		t.Fatal(err)
	}
	edited := sqlite.CreateTable(tables.Accounts).IfNotExists().
		Column(ddl.Column(tables.AccountID), ddl.Column(tables.AccountEmail).Nullable()).Clause(sqlite.PrimaryKey(tables.AccountID))
	s, err := edited.Build()
	if err != nil {
		t.Fatal(err)
	}
	ms[0].Script = append(s, ms[0].Script[1:]...)
	led, err := migrate.NewLedger(c.dialect, "sqlb_migrations", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := led.ApplyDB(ctx, db, toLedger(ms)...); !errors.Is(err, migrate.ErrChecksumMismatch) {
		t.Fatalf("an edited migration was not refused: %v", err)
	}
}
