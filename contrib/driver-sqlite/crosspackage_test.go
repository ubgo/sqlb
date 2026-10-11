package driversqlite_test

import (
	"context"
	"database/sql"
	"testing"

	driversqlite "github.com/ubgo/sqlb/contrib/driver-sqlite"
	"github.com/ubgo/sqlb/dialect/sqlite"
	"github.com/ubgo/sqlb/examples/cookbook/app"
	"github.com/ubgo/sqlb/examples/cookbook/authlite"
	"github.com/ubgo/sqlb/examples/cookbook/workspace"
	"github.com/ubgo/sqlb/exec"
)

// This file executes the claim the whole library is for: four packages, three
// of which have never heard of each other, composed into one statement that a
// real database runs and that scans into one struct.
//
// It lives here because this is the only module that can execute anything, and
// the distinction is the one this repository keeps making: a rendering test
// proves the text, and only execution proves the SQL. examples/cookbook prints
// the statement; nothing there can tell you whether SQLite accepts it, whether
// the LEFT joins fan out, or whether the bindings land in the right fields.
//
// The composite under test is app.User, which embeds authlite.User, nests
// phone.Data and workspace.Membership, and adds a column of the application's
// own. The packages import in one direction only: app imports the three, phone
// imports authlite, and workspace imports nothing but sqlb.

// crossSchema is the four tables the four packages own between them, spelled
// through the exported name constants rather than as literals, so a rename in
// a library fails to compile here instead of failing at runtime.
func crossDB(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()
	db, err := driversqlite.Memory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	for _, ddl := range []string{
		`CREATE TABLE ` + authlite.TableNameUser + ` (
			` + authlite.ColID + ` TEXT PRIMARY KEY,
			` + authlite.ColEmail + ` TEXT NOT NULL,
			` + authlite.ColBannedAt + ` TIMESTAMP,
			` + authlite.ColCreatedAt + ` TIMESTAMP NOT NULL)`,
		`CREATE TABLE ` + authlite.TableNameIdentity + ` (
			` + authlite.ColIdentUserID + ` TEXT NOT NULL,
			` + authlite.ColIdentKind + ` TEXT NOT NULL,
			` + authlite.ColIdentValue + ` TEXT NOT NULL)`,
		`CREATE TABLE ` + workspace.TableNameMember + ` (
			` + workspace.ColWorkspaceID + ` TEXT NOT NULL,
			` + workspace.ColUserID + ` TEXT NOT NULL,
			` + workspace.ColRole + ` TEXT NOT NULL)`,
		`CREATE TABLE ` + app.TableNameProfile + ` (
			` + app.ColProfileUserID + ` TEXT NOT NULL,
			` + app.ColProfileCompany + ` TEXT NOT NULL)`,
	} {
		if _, err := db.ExecContext(ctx, ddl); err != nil {
			t.Fatalf("create: %v", err)
		}
	}

	// usr_full has every join satisfied. usr_bare has a user row and nothing
	// else, which is the case that tells a LEFT-join miss from an empty value.
	for _, stmtSQL := range []string{
		`INSERT INTO auth_user (id, primary_email, banned_at, created_at)
			VALUES ('usr_full', 'ada@example.com', NULL, '2026-01-01T00:00:00Z'),
			       ('usr_bare', 'bob@example.com', NULL, '2026-01-02T00:00:00Z')`,
		`INSERT INTO auth_identity (user_id, kind, value)
			VALUES ('usr_full', 'phone', '+61400000000'),
			       ('usr_full', 'email', 'ada@example.com')`,
		`INSERT INTO ws_member (workspace_id, user_id, role)
			VALUES ('ws_42', 'usr_full', 'owner'),
			       ('ws_99', 'usr_full', 'member')`,
		`INSERT INTO app_profile (user_id, company) VALUES ('usr_full', 'Analytical Engines')`,
	} {
		if _, err := db.ExecContext(ctx, stmtSQL); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	return db
}

// TestTask_LoadOneUserAcrossFourPackages is the task: given a user id and a
// workspace, load everything the application knows about that user in one
// round trip, where "everything" is spread across four packages.
func TestTask_LoadOneUserAcrossFourPackages(t *testing.T) {
	ctx := context.Background()
	db := crossDB(t)

	a := authlite.New(authlite.WithDialect(sqlite.Dialect{}))
	u := app.NewUser("ws_42")

	q, err := a.LoadUserQuery("usr_full", u)
	if err != nil {
		t.Fatal(err)
	}
	if err := exec.One(ctx, db, q); err != nil {
		t.Fatalf("one statement, four tables, three owners: %v", err)
	}

	// authlite's own columns, scanned through authlite's own bindings.
	if u.ID != "usr_full" {
		t.Errorf("auth user id = %q, want usr_full", u.ID)
	}
	if u.Email != "ada@example.com" {
		t.Errorf("auth email = %q, want ada@example.com", u.Email)
	}
	if u.BannedAt != nil {
		t.Errorf("banned_at = %v, want nil for a NULL", u.BannedAt)
	}

	// The plugin's column, reached through the core's table under the
	// plugin's own alias. Getting the alias wrong is what this proves does
	// not happen: auth_identity is joined once by the plugin, and the core's
	// own alias for that table is untouched.
	if u.Phone.Number != "+61400000000" {
		t.Errorf("phone = %q, want +61400000000", u.Phone.Number)
	}
	if !u.Phone.Exists() {
		t.Error("phone must report that the join found a row")
	}

	// The second library's columns. workspace imports nothing; the
	// application handed it authlite.UserID as the column to join on.
	if u.Membership.WorkspaceID != "ws_42" {
		t.Errorf("workspace = %q, want ws_42", u.Membership.WorkspaceID)
	}
	if u.Membership.Role != workspace.RoleOwner {
		t.Errorf("role = %q, want %q", u.Membership.Role, workspace.RoleOwner)
	}

	// The application's own column, which no library can see.
	if u.Company != "Analytical Engines" {
		t.Errorf("company = %q, want Analytical Engines", u.Company)
	}
}

// TestTask_CompositeDistinguishesAMissFromAnEmptyValue is the half a rendering
// test cannot reach at all.
//
// Every join in the composite is LEFT, so a user with no phone row and a user
// whose phone number is the empty string scan into the same zero struct. The
// bindings COALESCE a miss to ” precisely so the scan cannot fail on a NULL,
// which means the SQL alone can no longer tell the two apart — Exists is what
// carries that, and this is the only place it is exercised against a database
// that actually produced the miss.
func TestTask_CompositeDistinguishesAMissFromAnEmptyValue(t *testing.T) {
	ctx := context.Background()
	db := crossDB(t)

	a := authlite.New(authlite.WithDialect(sqlite.Dialect{}))
	u := app.NewUser("ws_42")

	q, err := a.LoadUserQuery("usr_bare", u)
	if err != nil {
		t.Fatal(err)
	}
	if err := exec.One(ctx, db, q); err != nil {
		t.Fatalf("a user with no joined rows must still load: %v", err)
	}

	if u.ID != "usr_bare" {
		t.Errorf("auth user id = %q, want usr_bare", u.ID)
	}
	if u.Phone.Exists() {
		t.Error("a user with no phone identity must report no row")
	}
	if u.Membership.Exists() {
		t.Error("a user with no membership must report no row")
	}
	if u.Company != "" {
		t.Errorf("company = %q, want the COALESCE fallback", u.Company)
	}
}

// TestTask_TheSameUserInADifferentWorkspace pins that the membership join is
// scoped by the value the application supplied, not by the user.
//
// usr_full is a member of both ws_42 and ws_99 with different roles. If the
// workspace predicate were dropped the LEFT join would match two rows and the
// statement would return two, which One reports rather than silently taking
// the first.
func TestTask_TheSameUserInADifferentWorkspace(t *testing.T) {
	ctx := context.Background()
	db := crossDB(t)

	a := authlite.New(authlite.WithDialect(sqlite.Dialect{}))

	for _, tc := range []struct {
		ws   string
		want workspace.Role
	}{
		{"ws_42", workspace.RoleOwner},
		{"ws_99", workspace.RoleMember},
	} {
		u := app.NewUser(tc.ws)
		q, err := a.LoadUserQuery("usr_full", u)
		if err != nil {
			t.Fatal(err)
		}
		if err := exec.One(ctx, db, q); err != nil {
			t.Fatalf("workspace %s: %v", tc.ws, err)
		}
		if u.Membership.Role != tc.want {
			t.Errorf("workspace %s: role = %q, want %q", tc.ws, u.Membership.Role, tc.want)
		}
		if got := u.WorkspaceID(); got != tc.ws {
			t.Errorf("composite scope = %q, want %q", got, tc.ws)
		}
	}
}
