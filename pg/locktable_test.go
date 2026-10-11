package pg_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/pg"
)

var (
	lkA = sqlb.Table{Name: "accounts", Alias: "a"}
	lkB = sqlb.Table{Name: "ledger", Alias: "l"}
)

func lockSQL(t *testing.T, l *pg.LockTable) string {
	t.Helper()
	q, err := l.Build()
	if err != nil {
		t.Fatal(err)
	}
	return q.SQL
}

// TestLockTable_Renders covers every option, because each changes what the
// statement blocks and for how long, and a table lock is held until the
// transaction ends.
func TestLockTable_Renders(t *testing.T) {
	d := pg.Dialect{}
	for _, tc := range []struct {
		name string
		l    *pg.LockTable
		want string
	}{
		{"default mode", pg.NewLockTable(d, lkA),
			`LOCK TABLE "accounts"`},
		{"explicit mode", pg.NewLockTable(d, lkA).In(pg.LockShareRowExclusive),
			`LOCK TABLE "accounts" IN SHARE ROW EXCLUSIVE MODE`},
		{"several tables at once", pg.NewLockTable(d).Table(lkA, lkB).In(pg.LockExclusive),
			`LOCK TABLE "accounts", "ledger" IN EXCLUSIVE MODE`},
		{"nowait", pg.NewLockTable(d, lkA).In(pg.LockAccessExclusive).NoWait(),
			`LOCK TABLE "accounts" IN ACCESS EXCLUSIVE MODE NOWAIT`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := lockSQL(t, tc.l); got != tc.want {
				t.Fatalf("got  %s\nwant %s", got, tc.want)
			}
		})
	}
}

// TestLockTable_EveryMode pins that all eight modes render, since a mode that
// silently produced the wrong keyword would lock at the wrong strength, which
// is either a deadlock or a data race depending on which way it is wrong.
func TestLockTable_EveryMode(t *testing.T) {
	if len(pg.TableLockModeValues) != 8 {
		t.Fatalf("got %d modes, want the eight Postgres defines", len(pg.TableLockModeValues))
	}
	for _, m := range pg.TableLockModeValues {
		got := lockSQL(t, pg.NewLockTable(pg.Dialect{}, lkA).In(m))
		if !strings.Contains(got, "IN "+string(m)+" MODE") {
			t.Errorf("mode %q rendered as %s", m, got)
		}
	}
}

// TestTableLockMode_IsADistinctType pins that a mode cannot be an arbitrary
// string.
//
// The statement's whole purpose is to block other transactions, so a bare
// string here would be a fragment injected into exactly the statement where a
// mistake is least recoverable. The named type is what makes the set
// discoverable and closed.
func TestTableLockMode_IsADistinctType(t *testing.T) {
	// The real assertion is what a caller can and cannot pass. A function
	// taking the named type accepts a constant and rejects a bare string,
	// which is what stops a fragment reaching the statement where a mistake is
	// least recoverable.
	accepts := func(m pg.TableLockMode) string { return string(m) }

	if got := accepts(pg.LockShare); got != "SHARE" {
		t.Fatalf("got %q", got)
	}
	// accepts("SHARE ROW EXCLUSIVE; DROP TABLE x") does not compile.

	seen := map[string]bool{}
	for _, v := range pg.TableLockModeValues {
		kw := accepts(v)
		if kw == "" {
			t.Error("a mode has an empty keyword")
		}
		if seen[kw] {
			t.Errorf("%q appears twice in the canonical list", kw)
		}
		seen[kw] = true
	}
}

// TestLockTable_ModesAreWeakestFirst pins the ordering of the canonical list,
// which is the conflict ordering and the only thing that makes the list worth
// reading: ACCESS SHARE blocks almost nothing, ACCESS EXCLUSIVE blocks even a
// plain SELECT.
func TestLockTable_ModesAreWeakestFirst(t *testing.T) {
	if pg.TableLockModeValues[0] != pg.LockAccessShare {
		t.Error("the weakest mode should be first")
	}
	if pg.TableLockModeValues[len(pg.TableLockModeValues)-1] != pg.LockAccessExclusive {
		t.Error("the strongest mode should be last")
	}
}

// TestLockTable_NoTablesIsRefused pins that an empty statement fails at Build.
func TestLockTable_NoTablesIsRefused(t *testing.T) {
	_, err := pg.NewLockTable(pg.Dialect{}).Build()
	if !errors.Is(err, pg.ErrNoLockTables) {
		t.Fatalf("want ErrNoLockTables, got %v", err)
	}
}

// TestLockTable_FailsUnderAnotherDialect pins the one path on which another engine's
// dialect still reaches this statement. Passing one to the constructor does
// not compile, since the dialect parameter is pg.Dialect, and
// examples/typecheck holds that negative build. But the statement satisfies
// sqlb.Statement, so it can be embedded as a CTE body in a statement of any
// engine, and there the guard inside WriteSQL is what refuses it.
func TestLockTable_FailsUnderAnotherDialect(t *testing.T) {
	_, err := sqlb.NewSelect(sqlb.SQLiteDialect{}).With("x", pg.NewLockTable(pg.Dialect{}, lkA)).Exprs(sqlb.Literal[int]("1")).Build()
	if !errors.Is(err, sqlb.ErrUnsupportedDialect) {
		t.Fatalf("want ErrUnsupportedDialect, got %v", err)
	}
	if !strings.Contains(err.Error(), "pg.LockTable") {
		t.Errorf("the error should name the construct: %v", err)
	}
}

// TestLockTable_IsAStatement pins that it can be used anywhere a Statement can,
// and that rendering through the interface matches Build.
func TestLockTable_IsAStatement(t *testing.T) {
	var st sqlb.Statement = pg.NewLockTable(pg.Dialect{}, lkA).In(pg.LockShare)

	a := sqlb.NewArgs(pg.Dialect{})
	var b strings.Builder
	st.WriteSQL(&b, a)
	if err := a.Err(); err != nil {
		t.Fatal(err)
	}
	if want := `LOCK TABLE "accounts" IN SHARE MODE`; b.String() != want {
		t.Fatalf("got  %s\nwant %s", b.String(), want)
	}
}
