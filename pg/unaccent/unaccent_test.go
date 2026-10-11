package unaccent

import (
	"errors"
	"testing"

	"github.com/ubgo/sqlb"
)

var (
	users = sqlb.Table{Name: "users", Alias: "u"}
	name  = sqlb.Col[string](users, "name")
)

func TestRender(t *testing.T) {
	q, err := sqlb.NewSelect(sqlb.PostgresDialect{}).Exprs(Unaccent(name), WithDictionary("my_rules", name)).From(users).Build()
	if err != nil {
		t.Fatal(err)
	}
	want := `SELECT unaccent("u"."name"), unaccent('my_rules'::regdictionary, "u"."name") FROM "users" AS "u"`
	if q.SQL != want {
		t.Fatalf("got\n%s\nwant\n%s", q.SQL, want)
	}
}

func TestGuard(t *testing.T) {
	for k, e := range map[string]sqlb.SQLer{"Unaccent": Unaccent(name), "WithDictionary": WithDictionary("d", name)} {
		if _, err := sqlb.NewSelect(sqlb.SQLiteDialect{}).Exprs(e).From(users).Build(); !errors.Is(err, sqlb.ErrUnsupportedDialect) {
			t.Errorf("%s under sqlite: got %v", k, err)
		}
	}
}
