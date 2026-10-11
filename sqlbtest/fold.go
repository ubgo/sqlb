package sqlbtest

import (
	"reflect"
	"testing"

	"github.com/ubgo/sqlb"
)

// The case-folding cases. They are the proof behind the claim that a library
// can switch engines by changing its dialect and get the same rows: every
// statement below is built from c.Dialect, and every expected result is a
// fixed list of ids written here, so an engine that passes answers exactly
// what every other passing engine answers. Comparing against one golden list
// is what "identical" means across driver modules that cannot import each
// other.
//
// The data is ASCII on purpose. Beyond ASCII the answer belongs to the
// engine's collation, which is measured and pinned in each driver module
// rather than asserted here, because a conformance case that one engine fails
// by design is a case every integration would have to skip.

// foldUser is one row of the fold data.
type foldUser struct {
	id    int64
	email string
	name  *string // nil is a NULL name
}

func strPtr(s string) *string { return &s }

// foldUsers mixes case in the local part and the domain, and holds the three
// characters LIKE treats as syntax, so an escaping or folding mistake changes
// an answer rather than hiding in one. User 8's name is NULL, so every name
// query also folds a NULL.
var foldUsers = []foldUser{
	{1, "Ada@Example.com", strPtr("ADA")},
	{2, "ada@example.com", strPtr("ada")},
	{3, "BOB@x.io", strPtr("Bob")},
	{4, "carol@X.IO", strPtr("carol")},
	{5, "50%OFF@shop", strPtr("Deal")},
	{6, "a_b@X", strPtr("Eve")},
	{7, "axb@x", strPtr("eve")},
	{8, "Wow!@Y", nil},
}

func seedFold(t *testing.T, c Config) {
	t.Helper()
	for _, u := range foldUsers {
		exec(t, c, sqlb.NewInsert(c.Dialect).Into(Users).Row(
			sqlb.Set(UserID, u.id),
			sqlb.Set(UserEmail, u.email),
			sqlb.Set(sqlb.Nullable(UserName), u.name),
			sqlb.Set(UserAge, int64(30)),
			sqlb.Set(UserActive, true),
		))
	}
}

// idsWhere returns the ids matching p, in id order.
func idsWhere(t *testing.T, c Config, p sqlb.Predicate) []int64 {
	t.Helper()
	var id int64
	got := rows(t, c, sqlb.NewSelect(c.Dialect).Columns(sqlb.Bind(UserID, &id)).From(Users).
		Where(p).Order(sqlb.OrderBy(UserID, sqlb.SortAsc)), &id)
	if got == nil {
		got = []int64{}
	}
	return got
}

// FoldQuery is one query of the case_fold case: a predicate and the ids it
// must select from the fold data on every engine.
type FoldQuery struct {
	Name string
	Pred sqlb.Predicate
	Want []int64
}

// FoldQueries is the query set case_fold runs. Exported so an integration can
// run the same set under a configuration Run does not cover, a second
// collation say, and hold it to the same answers.
func FoldQueries() []FoldQuery {
	all := []int64{1, 2, 3, 4, 5, 6, 7, 8}
	return []FoldQuery{
		{"EqFold, value in another case", sqlb.EqFold(UserEmail, "ADA@EXAMPLE.COM"), []int64{1, 2}},
		{"EqFold, exact", sqlb.EqFold(UserEmail, "axb@x"), []int64{7}},
		{"EqFold, no match", sqlb.EqFold(UserEmail, "nobody@x"), []int64{}},
		{"EqFold, a NULL column matches nothing", sqlb.EqFold(UserName, "EVE"), []int64{6, 7}},
		{"InFold", sqlb.InFold(UserEmail, "bob@X.IO", "CAROL@x.io"), []int64{3, 4}},
		{"InFold, empty", sqlb.InFold(UserEmail), []int64{}},
		{"NotInFold", sqlb.NotInFold(UserEmail, "ADA@example.COM", "bob@x.io"), []int64{4, 5, 6, 7, 8}},
		{"NotInFold, empty", sqlb.NotInFold(UserEmail), all},
		{"NotInFold, a NULL column matches nothing", sqlb.NotInFold(UserName, "ada"), []int64{3, 4, 5, 6, 7}},
		{"ContainsFold", sqlb.ContainsFold(UserEmail, "EXAMPLE"), []int64{1, 2}},
		{"ContainsFold, a typed percent is a percent", sqlb.ContainsFold(UserEmail, "50%off"), []int64{5}},
		{"ContainsFold, the escape character is itself", sqlb.ContainsFold(UserEmail, "WOW!"), []int64{8}},
		{"StartsWithFold, a typed underscore is an underscore", sqlb.StartsWithFold(UserEmail, "A_B"), []int64{6}},
		{"StartsWithFold", sqlb.StartsWithFold(UserEmail, "ADA@"), []int64{1, 2}},
		{"EndsWithFold", sqlb.EndsWithFold(UserEmail, "@X.io"), []int64{3, 4}},
		{"NOT ContainsFold", sqlb.Not(sqlb.ContainsFold(UserEmail, "x")), []int64{5, 8}},
		{"folds combined with AND and OR", sqlb.Or(
			sqlb.And(sqlb.StartsWithFold(UserEmail, "a"), sqlb.EqFold(UserName, "ADA")),
			sqlb.EndsWithFold(UserEmail, "@y"),
		), []int64{1, 2, 8}},
	}
}

// caseFold runs FoldQueries and then the writes a library does with the same
// predicates, an UPDATE and a DELETE filtered by a fold.
func caseFold(t *testing.T, c Config) {
	seedFold(t, c)
	for _, q := range FoldQueries() {
		if got := idsWhere(t, c, q.Pred); !reflect.DeepEqual(got, q.Want) {
			t.Errorf("%s: ids %v, want %v", q.Name, got, q.Want)
		}
	}

	exec(t, c, sqlb.NewUpdate(c.Dialect).Table(Users).Set(sqlb.Set(UserAge, int64(99))).
		Where(sqlb.EqFold(UserEmail, "ADA@EXAMPLE.COM")))
	if got := idsWhere(t, c, sqlb.Eq(UserAge, int64(99))); !reflect.DeepEqual(got, []int64{1, 2}) {
		t.Errorf("UPDATE ... WHERE EqFold touched %v, want [1 2]", got)
	}
	exec(t, c, sqlb.NewDelete(c.Dialect).From(Users).Where(sqlb.InFold(UserEmail, "BOB@X.IO", "Carol@x.io")))
	if got := idsWhere(t, c, sqlb.In(UserID, int64(3), int64(4))); len(got) != 0 {
		t.Errorf("DELETE ... WHERE InFold left %v", got)
	}
}

// caseCaseSensitiveEq pins the other half: Eq, In and every comparison stay
// exact, so a library that means "exactly this value" gets it on every engine
// and asks for folding by name. It needs the engine to compare text by its
// bytes, which Postgres and SQLite do and MySQL does only under a binary or
// case-sensitive collation; an integration that runs MySQL on its default
// collation skips this case with that reason.
func caseCaseSensitiveEq(t *testing.T, c Config) {
	seedFold(t, c)
	for _, q := range []FoldQuery{
		{"Eq is exact", sqlb.Eq(UserEmail, "ADA@EXAMPLE.COM"), []int64{}},
		{"Eq matches its own case", sqlb.Eq(UserEmail, "Ada@Example.com"), []int64{1}},
		{"In is exact", sqlb.In(UserEmail, "bob@x.io", "carol@X.IO"), []int64{4}},
	} {
		if got := idsWhere(t, c, q.Pred); !reflect.DeepEqual(got, q.Want) {
			t.Errorf("%s: ids %v, want %v", q.Name, got, q.Want)
		}
	}
}
