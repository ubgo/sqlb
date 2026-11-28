package migrate_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/contrib/migrate"
	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/render"
)

// fixed is the clock the script examples are written with, so their text is
// the same on every run.
func fixed() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }

// widgets0001 is the one migration the script tests render.
var widgets0001 = migrate.Migration{
	Owner:  "acme",
	Name:   "0001_widgets",
	Script: ddl.Script{{SQL: "CREATE TABLE widgets (id BIGINT PRIMARY KEY)", Transactional: true}},
}

// ExampleLedger_Script shows the offline script for one migration on
// Postgres: the ledger table, the migration, and the row that makes a later
// Apply skip it.
func ExampleLedger_Script() {
	l, err := migrate.NewLedger(sqlb.PostgresDialect{}, "acme_migrations", nil)
	if err != nil {
		panic(err)
	}
	script, err := l.WithClock(fixed).Script(widgets0001)
	if err != nil {
		panic(err)
	}
	for _, st := range script {
		fmt.Println(st.SQL + ";")
	}
	// Output:
	// CREATE TABLE IF NOT EXISTS "acme_migrations" ("owner" TEXT NOT NULL, "name" TEXT NOT NULL, "checksum" TEXT NOT NULL, "applied_at" TEXT NOT NULL, PRIMARY KEY ("owner", "name"));
	// CREATE TABLE widgets (id BIGINT PRIMARY KEY);
	// INSERT INTO "acme_migrations" ("owner", "name", "checksum", "applied_at") VALUES ('acme', '0001_widgets', 'b902bef554a5858858da611211a11b382ce0f250972676ab6ccff0fd4f99acda', '2026-01-02T03:04:05Z');
}

// TestScript_IsTableScriptThenEachMigrationAndItsRecord pins Script's shape
// on every ledger engine: TableScript first, then each migration's own
// statements, flags kept, then its RecordStatement, which carries
// Checksum(script) and the injected time.
func TestScript_IsTableScriptThenEachMigrationAndItsRecord(t *testing.T) {
	second := migrate.Migration{Owner: "acme", Name: "0002_color", Script: ddl.Script{
		{SQL: "ALTER TABLE widgets ADD COLUMN color TEXT", Transactional: true},
		{SQL: "CREATE INDEX CONCURRENTLY widgets_color ON widgets (color)"},
	}}
	for _, d := range []render.Dialect{sqlb.PostgresDialect{}, sqlb.MySQLDialect{}, sqlb.SQLiteDialect{}} {
		l, err := migrate.NewLedger(d, "acme_migrations", nil)
		if err != nil {
			t.Fatal(err)
		}
		l = l.WithClock(fixed)
		table, err := l.TableScript()
		if err != nil || len(table) != 1 || !strings.HasPrefix(table[0].SQL, "CREATE TABLE IF NOT EXISTS "+d.QuoteIdent("acme_migrations")) {
			t.Fatalf("%s: TableScript %v, %v", render.DialectName(d), table, err)
		}
		script, err := l.Script(widgets0001, second)
		if err != nil {
			t.Fatal(err)
		}
		r1, err := l.RecordStatement(widgets0001)
		if err != nil {
			t.Fatal(err)
		}
		r2, err := l.RecordStatement(second)
		if err != nil {
			t.Fatal(err)
		}
		want := append(append(append(append(append(ddl.Script{}, table...), widgets0001.Script...), r1), second.Script...), r2)
		if fmt.Sprint(script) != fmt.Sprint(want) {
			t.Errorf("%s: Script\n%v\nwant\n%v", render.DialectName(d), script, want)
		}
		if !r2.Transactional || !strings.Contains(r2.SQL, "'"+migrate.Checksum(second.Script)+"'") || !strings.Contains(r2.SQL, "'2026-01-02T03:04:05Z'") {
			t.Errorf("%s: record %+v lacks the checksum or the injected time", render.DialectName(d), r2)
		}
	}
}

// TestScript_Refusals pins what Script, RecordStatement and TableScript
// refuse: what Apply refuses, and a zero Ledger.
func TestScript_Refusals(t *testing.T) {
	l, err := migrate.NewLedger(sqlb.SQLiteDialect{}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Script(widgets0001, widgets0001); !errors.Is(err, migrate.ErrBadMigration) {
		t.Errorf("a name twice: %v", err)
	}
	if _, err := l.RecordStatement(migrate.Migration{Owner: "acme", Name: "x"}); !errors.Is(err, migrate.ErrBadMigration) {
		t.Errorf("no script: %v", err)
	}
	var zero migrate.Ledger
	if _, err := zero.TableScript(); !errors.Is(err, migrate.ErrNoLedgerDialect) {
		t.Errorf("zero TableScript: %v", err)
	}
	if _, err := zero.RecordStatement(widgets0001); !errors.Is(err, migrate.ErrNoLedgerDialect) {
		t.Errorf("zero RecordStatement: %v", err)
	}
	if _, err := zero.Script(widgets0001); !errors.Is(err, migrate.ErrNoLedgerDialect) {
		t.Errorf("zero Script: %v", err)
	}
	if _, err := zero.Adopt(context.Background(), nil, nil, widgets0001); !errors.Is(err, migrate.ErrNoLedgerDialect) {
		t.Errorf("zero Adopt: %v", err)
	}
	if _, err := l.Adopt(context.Background(), nil, nil, migrate.Migration{Name: "x", Script: widgets0001.Script}); !errors.Is(err, migrate.ErrBadMigration) {
		t.Errorf("Adopt without an owner: %v", err)
	}
}

// TestWithClock_DefaultsToNowAndLeavesTheLedgerComparable pins the clock: a
// nil source is time.Now, the receiver is unchanged, and a Ledger can still
// be compared with ==, as it could before the clock existed.
func TestWithClock_DefaultsToNowAndLeavesTheLedgerComparable(t *testing.T) {
	l, err := migrate.NewLedger(sqlb.SQLiteDialect{}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	clocked := l.WithClock(fixed)
	if clocked == l || clocked.WithClock(nil) != l {
		t.Error("WithClock changed the receiver, or WithClock(nil) did not restore the default")
	}
	before := time.Now().UTC().Truncate(time.Second)
	st, err := l.RecordStatement(widgets0001)
	if err != nil {
		t.Fatal(err)
	}
	i := strings.LastIndex(st.SQL, ", '")
	at, err := time.Parse(time.RFC3339Nano, strings.TrimSuffix(st.SQL[i+3:], "')"))
	if err != nil || at.Before(before) || at.After(time.Now()) {
		t.Errorf("the default clock recorded %v (%v), not now", at, err)
	}
}
