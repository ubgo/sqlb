package pg_test

import (
	"reflect"
	"testing"

	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/pg"
	"github.com/ubgo/sqlb/schema"
)

// TestDescribe_PostgresKeysAndReferences pins what the Postgres clauses add to
// a description, and that two described tables order by their foreign key in
// a schema.Set, which is what a migration planner needs from them.
func TestDescribe_PostgresKeysAndReferences(t *testing.T) {
	users, err := pg.CreateTable(tUsers).Column(ddl.Column(tUserID), ddl.Column(tTenantID), ddl.Column(tEmail)).
		Clause(pg.Identity(tUserID, pg.Always), pg.PrimaryKey(tUserID), pg.Unique(pg.TableConstraint{Table: tEmail.Table(), Name: "users_email"}, tEmail), pg.Unlogged()).Describe()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(users.PrimaryKey, []string{"id"}) ||
		!reflect.DeepEqual(users.Indexes, []schema.Index{{Name: "users_email", Columns: []string{"email"}, Unique: true}}) {
		t.Fatalf("users: %+v", users)
	}
	sessions, err := pg.CreateTable(tSessions).Column(ddl.Column(tSessID), ddl.Column(tSessUser)).
		Clause(pg.PrimaryKey(tSessID), pg.ForeignKey(pg.TableConstraint{Table: tSessUser.Table(), Name: "sessions_user_fk"}, pg.RefPair(tSessUser, tUserID)).OnDelete(pg.RefCascade)).Describe()
	if err != nil {
		t.Fatal(err)
	}
	want := []schema.ForeignKey{{Name: "sessions_user_fk", Columns: []string{"user_id"}, RefTable: "auth.users", RefColumns: []string{"id"}, OnDelete: schema.Cascade}}
	if !reflect.DeepEqual(sessions.ForeignKeys, want) {
		t.Fatalf("sessions: %+v", sessions.ForeignKeys)
	}
	set, err := schema.New("app", sessions, users)
	if err != nil {
		t.Fatal(err)
	}
	sorted, err := set.Sorted()
	if err != nil {
		t.Fatal(err)
	}
	if sorted[0].Name != "users" || sorted[1].Name != "sessions" {
		t.Fatalf("order %s, %s", sorted[0].Name, sorted[1].Name)
	}
	if err := set.Validate(); err != nil {
		t.Fatalf("the described set is invalid: %v", err)
	}
}
