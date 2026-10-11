package migrate_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ubgo/sqlb/contrib/migrate"
	"github.com/ubgo/sqlb/ddl"
)

var errBoom = errors.New("boom")

// TestExecute_FailsOnEveryDatabaseError drives each error return of Execute,
// run and ExecuteDB with a driver that fails on cue, and checks what each
// failure left behind: a transaction that fails is rolled back, and nothing
// after the failure runs.
func TestExecute_FailsOnEveryDatabaseError(t *testing.T) {
	ctx := context.Background()
	tx := ddl.Statement{SQL: "CREATE A", Transactional: true}
	tx2 := ddl.Statement{SQL: "CREATE B", Transactional: true}
	alone := ddl.Statement{SQL: "VACUUM", Transactional: false}
	check := ddl.Statement{SQL: "CHECK", Transactional: true, MustReturnNoRows: true}
	ok := map[string]error{"CREATE": nil, "VACUUM": nil}
	cases := map[string]struct {
		db     *faultDB
		script ddl.Script
		want   error
		log    string // what ran, joined by " | "
	}{
		"the connection is refused": {&faultDB{connectErr: errBoom}, ddl.Script{tx}, errBoom, ""},
		"BEGIN fails":               {&faultDB{beginErr: errBoom, exec: ok}, ddl.Script{tx}, errBoom, ""},
		"a statement in a transaction fails": {
			&faultDB{exec: map[string]error{"CREATE A": nil, "CREATE B": errBoom}}, ddl.Script{tx, tx2}, errBoom,
			"BEGIN | CREATE A | ROLLBACK"},
		"a statement alone fails": {
			&faultDB{exec: map[string]error{"CREATE": nil, "VACUUM": errBoom}}, ddl.Script{tx, alone, tx2}, errBoom,
			"BEGIN | CREATE A | COMMIT"},
		"the COMMIT before a statement alone fails": {
			&faultDB{commitErr: errBoom, exec: ok}, ddl.Script{tx, alone}, errBoom, "BEGIN | CREATE A"},
		"the final COMMIT fails": {&faultDB{commitErr: errBoom, exec: ok}, ddl.Script{tx}, errBoom, "BEGIN | CREATE A"},
		"a check's query fails": {
			&faultDB{exec: ok, query: map[string]fakeQuery{"CHECK": {err: errBoom}}}, ddl.Script{tx, check}, errBoom,
			"BEGIN | CREATE A | ROLLBACK"},
		"a check's row fails": {
			&faultDB{exec: ok, query: map[string]fakeQuery{"CHECK": {rows: 1, failAt: 1, rowErr: errBoom}}}, ddl.Script{check}, errBoom,
			"BEGIN | CHECK | ROLLBACK"},
		"a check's rows fail to close": {
			&faultDB{exec: ok, query: map[string]fakeQuery{"CHECK": {closeErr: errBoom}}}, ddl.Script{check}, errBoom,
			"BEGIN | CHECK | ROLLBACK"},
		"a check finds a row": {
			&faultDB{exec: ok, query: map[string]fakeQuery{"CHECK": {rows: 1}}}, ddl.Script{tx, check, tx2}, migrate.ErrCheckFailed,
			"BEGIN | CREATE A | CHECK | ROLLBACK"},
	}
	for name, c := range cases {
		err := migrate.ExecuteDB(ctx, c.db.open(), c.script)
		if errors.Is(err, errUnexpected) || !errors.Is(err, c.want) {
			t.Errorf("%s: got %v, want %v", name, err, c.want)
			continue
		}
		if got := strings.Join(c.db.log, " | "); got != c.log {
			t.Errorf("%s: ran %q, want %q", name, got, c.log)
		}
	}
	control := &faultDB{exec: ok, query: map[string]fakeQuery{"CHECK": {}}}
	if err := migrate.ExecuteDB(ctx, control.open(), ddl.Script{tx, alone, check, tx2}); err != nil {
		t.Fatalf("the control failed, so the cases above test nothing: %v", err)
	}
	if got := strings.Join(control.log, " | "); got != "BEGIN | CREATE A | COMMIT | VACUUM | BEGIN | CHECK | CREATE B | COMMIT" {
		t.Errorf("the control ran %q", got)
	}
}
