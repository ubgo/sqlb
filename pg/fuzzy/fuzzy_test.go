package fuzzy

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
	q, err := sqlb.NewSelect(sqlb.PostgresDialect{}).Exprs(name).From(users).
		Where(sqlb.EqExpr(Dmetaphone(name), Dmetaphone(sqlb.Param("Katherine"))),
			sqlb.LessEq(LevenshteinLessEqual(sqlb.Lower(name), sqlb.Param("jonathon"), 2), 2)).Build()
	if err != nil {
		t.Fatal(err)
	}
	want := `SELECT "u"."name" FROM "users" AS "u" WHERE (dmetaphone("u"."name") = dmetaphone($1) AND levenshtein_less_equal(lower("u"."name"), $2, $3) <= $4)`
	if q.SQL != want {
		t.Fatalf("got\n%s\nwant\n%s", q.SQL, want)
	}
}

func TestGuard(t *testing.T) {
	for k, e := range map[string]sqlb.SQLer{
		"Dmetaphone": Dmetaphone(name), "DmetaphoneAlt": DmetaphoneAlt(name), "Soundex": Soundex(name),
		"Metaphone": Metaphone(name, 4), "Levenshtein": Levenshtein(name, name),
		"LevenshteinLessEqual": LevenshteinLessEqual(name, name, 2), "Difference": Difference(name, name),
	} {
		if _, err := sqlb.NewSelect(sqlb.SQLiteDialect{}).Exprs(e).From(users).Build(); !errors.Is(err, sqlb.ErrUnsupportedDialect) {
			t.Errorf("%s under sqlite: got %v", k, err)
		}
	}
}
