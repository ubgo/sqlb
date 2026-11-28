package pg_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/ubgo/sqlb"
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

// TestDescribe_PostgresCreateIndex pins what a CREATE INDEX describes: its
// columns, uniqueness, every key's order as the builder was given it, and a
// partial index's predicate exactly as Build writes it; an expression key is
// refused rather than described as a column list, and a statement Build
// refuses is not described.
func TestDescribe_PostgresCreateIndex(t *testing.T) {
	stmt := pg.CreateIndex(pg.Index{Name: "users_recent"}, tUsers).Unique().
		On(pg.IndexKey(tTenantID), pg.IndexKey(tUserID).Desc().NullsLast(), pg.IndexKey(tEmail).Asc()).
		Where(sqlb.IsNull(tEmail))
	got, err := stmt.Describe()
	if err != nil {
		t.Fatal(err)
	}
	want := schema.Index{
		Name: "users_recent", Columns: []string{"tenant_id", "id", "email"}, Unique: true,
		Orders: []schema.KeyOrder{{}, {Direction: schema.Descending, Nulls: schema.NullsLast}, {Direction: schema.Ascending}},
		Where:  `"email" IS NULL`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
	s, err := stmt.Build()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(s[0].SQL, " WHERE "+want.Where) {
		t.Fatalf("the description's predicate is not the one Build writes: %s", s[0].SQL)
	}
	if err := (schema.Table{Name: "users", Columns: []schema.Column{{Name: "id", Type: schema.TypeBigInt}, {Name: "tenant_id", Type: schema.TypeBigInt}, {Name: "email", Type: schema.TypeText}}, Indexes: []schema.Index{got}}).Validate(); err != nil {
		t.Fatalf("a described index does not validate: %v", err)
	}
	plain, err := pg.CreateIndex(pg.Index{Name: "users_email"}, tUsers).On(pg.IndexKey(tEmail)).Describe()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plain, schema.Index{Name: "users_email", Columns: []string{"email"}, Orders: []schema.KeyOrder{{}}}) {
		t.Fatalf("plain: %+v", plain)
	}
	if _, err := pg.CreateIndex(pg.Index{Name: "users_lower"}, tUsers).On(pg.IndexExpr(sqlb.Lower(tEmail))).Describe(); !errors.Is(err, ddl.ErrNotDescribable) {
		t.Fatalf("an expression key: %v", err)
	}
	if _, err := pg.CreateIndex(pg.Index{Name: "x"}, tUsers).On(pg.IndexKey(sqlb.Col[int64](tSessions, "id"))).Describe(); !errors.Is(err, ddl.ErrForeignColumn) {
		t.Fatalf("a statement Build refuses: %v", err)
	}
}
