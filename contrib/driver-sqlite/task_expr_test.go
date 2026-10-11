package driversqlite_test

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	driversqlite "github.com/ubgo/sqlb/contrib/driver-sqlite"
	"github.com/ubgo/sqlb/dialect/sqlite"
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/scan"
	"github.com/ubgo/sqlb/stmt"
)

// The expr layer's end-to-end tasks: the jobs a filter panel and a search box
// do, executed rather than rendered.
//
// Every predicate in expr has a rendering test, and a rendering test cannot
// see the failure this layer actually has. Contains, IsNull and Case all
// produce SQL an engine accepts whichever way they are used; what separates a
// correct filter from a plausible one is which rows come back, and only a
// database can say. Each task below therefore asserts the row set, and two of
// them assert the wrong row set the obvious alternative returns, because the
// point of the construct is the difference between the two.

type article struct {
	ID    int64
	Title string
}

var (
	artTbl    = expr.Table{Name: "articles", Alias: "a"}
	artID     = expr.Col[int64](artTbl, "id")
	artTitle  = expr.Col[string](artTbl, "title")
	artBody   = expr.Col[string](artTbl, "body")
	artAuthor = expr.Col[string](artTbl, "author")
	artStatus = expr.Col[string](artTbl, "status")
	artViews  = expr.Col[int64](artTbl, "views")
)

// artReader reads the two columns every task below displays. The id is what
// the assertions name a row by, because a title is what the search is about
// and quoting it in a failure message twice reads worse than an id.
var artReader = scan.Map2(scan.Field(artID), scan.Field(artTitle),
	func(id int64, title string) article { return article{id, title} })

// artDB is the content table a search box and a filter panel sit on top of.
//
// The rows are chosen so that the careless spelling of each task returns a
// different set from the careful one: "50%" and "a_b" are both what a person
// types and both LIKE syntax, one author is NULL, and the statuses do not sort
// into their workflow order alphabetically.
func artDB(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()
	db, err := driversqlite.Memory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.ExecContext(ctx, `CREATE TABLE articles (
		id INTEGER PRIMARY KEY, title TEXT NOT NULL, body TEXT NOT NULL,
		author TEXT, status TEXT NOT NULL, views INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		id          int64
		title, body string
		author      *string
		status      string
		views       int64
	}{
		{1, "Discount 50% off", "seasonal sale", strptr("ada"), "published", 10},
		{2, "A_B testing", "split traffic", strptr("bob"), "published", 40},
		{3, "Fifty percent", "spelled out, no digits", nil, "draft", 5},
		{4, "ADA writes about caches", "warm the cache", strptr("ada"), "archived", 7},
		{5, "Plain article", "everything is 50% faster", strptr("cyd"), "published", 100},
		{6, "50 shades of caching", "not a discount", strptr("cyd"), "published", 3},
		{7, "AxB rollout", "one letter apart", strptr("bob"), "published", 1},
	} {
		if _, err := db.ExecContext(ctx, `INSERT INTO articles VALUES (?, ?, ?, ?, ?, ?)`,
			row.id, row.title, row.body, row.author, row.status, row.views); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func strptr(s string) *string { return &s }

// artIDs runs a filtered list and returns the ids it matched, newest-first by
// views, which is the order every one of these screens is sorted in.
func artIDs(t *testing.T, db *sql.DB, where ...expr.Predicate) []int64 {
	t.Helper()
	rows, err := scan.All(context.Background(), db,
		stmt.NewSelect(sqlite.Dialect{}).
			From(artTbl).
			Where(where...).
			Order(expr.OrderBy(artViews, expr.SortDesc)),
		artReader)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]int64, len(rows))
	for i, r := range rows {
		out[i] = r.ID
	}
	return out
}

// TestTask_FilterPanelOverUserTypedText is the job a search box does: take
// whatever a person typed, match it against two columns, narrow by a status
// the panel offers, and sort by popularity.
//
// The interesting part is that what a person types is not a pattern. "50%" and
// "a_b" are ordinary text to the person and LIKE syntax to the engine, so the
// careless spelling returns rows nobody asked for and returns them silently —
// a search that matches too much looks exactly like a search that worked. So
// each half asserts both answers: the one Contains gives and the wrong one a
// hand-written Like gives, side by side, which is the only form in which the
// difference is visible at all.
func TestTask_FilterPanelOverUserTypedText(t *testing.T) {
	db := artDB(t)

	// The panel lowercases the column and the term rather than relying on
	// LIKE's case rules, which differ per engine: SQLite folds ASCII case by
	// default and Postgres does not, so a search written the other way answers
	// differently depending on where it runs.
	anywhere := func(term string) expr.Predicate {
		term = strings.ToLower(term)
		return expr.Or(
			expr.Contains(expr.Lower(artTitle), term),
			expr.Contains(expr.Lower(artBody), term),
		)
	}
	// The same search spelled with a raw pattern, which is what a caller
	// writes before discovering that Like takes a pattern and not a value.
	anywhereUnescaped := func(term string) expr.Predicate {
		pattern := "%" + strings.ToLower(term) + "%"
		return expr.Or(
			expr.Like(expr.Lower(artTitle), pattern),
			expr.Like(expr.Lower(artBody), pattern),
		)
	}

	published := expr.In(artStatus, "published")

	// A percent sign in the term. Article 1 is the discount, article 5 says
	// "50% faster" in its body; article 6 is "50 shades", which only matches
	// if the % was taken as a wildcard.
	if got, want := artIDs(t, db, anywhere("50%"), published), []int64{5, 1}; !sameIDs(got, want) {
		t.Errorf("searching for 50%% matched %v, want %v", got, want)
	}
	if got, want := artIDs(t, db, anywhereUnescaped("50%"), published), []int64{5, 1, 6}; !sameIDs(got, want) {
		t.Errorf("the unescaped search matched %v, want %v; if this changed, the "+
			"contrast the escaped search exists for has moved", got, want)
	}

	// An underscore in the term. Article 2 is "A_B testing"; article 7 is
	// "AxB rollout", which only matches if the _ was taken as "any character".
	if got, want := artIDs(t, db, anywhere("a_b")), []int64{2}; !sameIDs(got, want) {
		t.Errorf("searching for a_b matched %v, want %v", got, want)
	}
	if got, want := artIDs(t, db, anywhereUnescaped("a_b")), []int64{2, 7}; !sameIDs(got, want) {
		t.Errorf("the unescaped search matched %v, want %v", got, want)
	}

	// A term with no syntax in it at all still has to work, which is the case
	// that hides the other two: everything looks right until somebody types a
	// punctuation mark.
	if got, want := artIDs(t, db, anywhere("CACHING"), published), []int64{6}; !sameIDs(got, want) {
		t.Errorf("searching for CACHING matched %v, want %v", got, want)
	}
}

// TestTask_ExcludeAnAuthorWithoutLosingTheUnattributed is the three-way logic
// every nullable column has, in the one place it costs a person real rows.
//
// The job is "show me everything not written by ada". The obvious predicate is
// author <> 'ada', and in SQL that is unknown rather than true for a row with
// no author, so an unattributed article disappears from a list nobody filtered
// it out of. There is no error and no warning; the row count is simply lower.
// This asserts both answers so the difference is a number rather than a
// paragraph, and then reads the column back through Nullable to show the same
// NULL arriving in Go as the nil the predicate had to account for.
func TestTask_ExcludeAnAuthorWithoutLosingTheUnattributed(t *testing.T) {
	db := artDB(t)

	// What a person writes first. Articles 1 and 4 are ada's and 3 has no
	// author, so this drops three rows where only two were excluded.
	naive := artIDs(t, db, expr.NotEq(artAuthor, "ada"))
	if want := []int64{5, 2, 6, 7}; !sameIDs(naive, want) {
		t.Fatalf("author <> 'ada' matched %v, want %v", naive, want)
	}

	// What the list was asked for: not ada's, including the ones nobody
	// signed. IS NULL is the only predicate that is true for a NULL.
	full := artIDs(t, db, expr.Or(expr.IsNull(artAuthor), expr.NotEq(artAuthor, "ada")))
	if want := []int64{5, 2, 3, 6, 7}; !sameIDs(full, want) {
		t.Fatalf("the corrected filter matched %v, want %v", full, want)
	}

	// And the row the naive filter lost is the one whose author is nil in Go
	// too, which is what makes the missing row findable from either end.
	rows, err := scan.All(context.Background(), db,
		stmt.NewSelect(sqlite.Dialect{}).
			From(artTbl).
			Where(expr.IsNull(artAuthor)),
		scan.Map2(scan.Field(artID), scan.Field(expr.Nullable(artAuthor)),
			func(id int64, author *string) string { return fmt.Sprint(id, author == nil) }))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0] != "3 true" {
		t.Errorf("the unattributed rows are %v, want [3 true]", rows)
	}
}

// TestTask_OrderByWorkflowOrderNotAlphabetically sorts a list by a state
// column in the order the states happen rather than the order they spell.
//
// This is the job every status column has and no column can answer for
// itself: draft, published and archived sort as archived, draft, published,
// which is an order no reader expects. Keeping a rank column beside the status
// is the usual fix and it is a second place for the truth to live; a CASE in
// the ORDER BY keeps one. It is worth executing because ordering by an
// expression that appears nowhere in the select list, with bound values inside
// it, is precisely the shape that renders cleanly and can still be refused.
func TestTask_OrderByWorkflowOrderNotAlphabetically(t *testing.T) {
	db := artDB(t)

	// The workflow order, written once. Else is the bucket anything new falls
	// into, which is deliberate: a status added later sorts last rather than
	// making the query fail.
	rank := expr.Case[int64]().
		When(expr.Eq(artStatus, "draft"), expr.Param[int64](0)).
		When(expr.Eq(artStatus, "published"), expr.Param[int64](1)).
		Else(expr.Param[int64](2))

	rows, err := scan.All(context.Background(), db,
		stmt.NewSelect(sqlite.Dialect{}).
			From(artTbl).
			Order(expr.OrderBy(rank, expr.SortAsc), expr.OrderBy(artViews, expr.SortDesc)),
		artReader)
	if err != nil {
		t.Fatal(err)
	}

	got := make([]int64, len(rows))
	for i, r := range rows {
		got[i] = r.ID
	}
	// Draft first, then the published ones by popularity, then archived.
	if want := []int64{3, 5, 2, 1, 6, 7, 4}; !sameIDs(got, want) {
		t.Fatalf("workflow order is %v, want %v", got, want)
	}
}

// sameIDs compares two id lists including their order, which is half of what
// each task asserts: the rows and the sequence are one answer, not two.
func sameIDs(got, want []int64) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
