package migrate_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/ubgo/sqlb/contrib/migrate"
	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/sqlite"
)

// TestExecute_PinningIsWhatMakesAConnectionSettingHold demonstrates, rather
// than argues, why Execute takes one connection. SQLite's PRAGMA foreign_keys
// applies to the connection that ran it. A script that turns enforcement off,
// writes a row the foreign key would refuse, and turns it on again is run two
// ways against the same pool of two connections, each while the rest of the
// application holds the other connection busy, which is what a pool under load
// does:
//
//   - statement by statement through the pool, the PRAGMA lands on one
//     connection and the INSERT on the other, where enforcement is still on,
//     and SQLite refuses the row;
//   - through ExecuteDB, every statement runs on the connection it pinned, and
//     the row is written.
func TestExecute_PinningIsWhatMakesAConnectionSettingHold(t *testing.T) {
	ctx := context.Background()
	script := func(t *testing.T) ddl.Script {
		off, err := sqlite.ForeignKeys(false).Build()
		if err != nil {
			t.Fatal(err)
		}
		on, err := sqlite.ForeignKeys(true).Build()
		if err != nil {
			t.Fatal(err)
		}
		s := append(ddl.Script{}, off...)
		s = append(s, ddl.Statement{SQL: `INSERT INTO pin_child VALUES (1, 99)`, Transactional: true})
		return append(s, on...)
	}
	setup := func(t *testing.T) *sql.DB {
		db := fileDB(t)
		db.SetMaxOpenConns(2)
		for _, q := range []string{
			`CREATE TABLE pin_parent (id INTEGER PRIMARY KEY)`,
			`CREATE TABLE pin_child (id INTEGER PRIMARY KEY, parent_id INTEGER REFERENCES pin_parent (id))`,
		} {
			if _, err := db.ExecContext(ctx, q); err != nil {
				t.Fatal(err)
			}
		}
		return db
	}
	// busy takes one connection from the pool and holds it, so the next pool
	// statement is forced onto the other one.
	busy := func(t *testing.T, db *sql.DB) *sql.Conn {
		c, err := db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}

	t.Run("through the pool the setting is lost", func(t *testing.T) {
		db := setup(t)
		s := script(t)
		// Two connections, A and B. Hold A, so the PRAGMA runs on B.
		a := busy(t, db)
		if _, err := db.ExecContext(ctx, s[0].SQL); err != nil {
			t.Fatal(err)
		}
		// Now the application holds B, and the INSERT gets A, which still
		// enforces foreign keys.
		b := busy(t, db)
		_ = a.Close()
		_, err := db.ExecContext(ctx, s[1].SQL)
		_ = b.Close()
		if err == nil || !strings.Contains(err.Error(), "FOREIGN KEY") {
			t.Fatalf("the row was written across connections, so this does not show what pinning prevents: %v", err)
		}
	})

	t.Run("ExecuteDB pins one connection and the setting holds", func(t *testing.T) {
		db := setup(t)
		other := busy(t, db)
		defer func() { _ = other.Close() }()
		if err := migrate.ExecuteDB(ctx, db, script(t)); err != nil {
			t.Fatalf("ExecuteDB: %v", err)
		}
		var n int
		if err := other.QueryRowContext(ctx, `SELECT count(*) FROM pin_child`).Scan(&n); err != nil || n != 1 {
			t.Fatalf("rows %d, %v", n, err)
		}
	})
}
