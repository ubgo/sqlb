package sqlbtest

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/ubgo/sqlb"
)

// The CRUD transcript: one scripted workload of the writes and reads a library
// does, built from c.Dialect alone, whose every observable result is written
// to a transcript and compared line by line with CRUDTranscript.
//
// It exists because the other cases each assert one property, and a library
// switching engines does not care about one property; it cares that the same
// program gives the same answers. Every row read back, every aggregate, every
// page and every rows-affected count goes into the transcript, so an engine
// that passes produced exactly the text every other passing engine produced.
// A count that differs by engine, a NULL that comes back as an empty string, a
// boolean that scans as 1, an ORDER BY that ties differently: each changes a
// line and fails with that line named.
//
// Rows affected is part of the transcript on purpose. It is what a library
// uses to report "not found" from an UPDATE or a DELETE, and it is the number
// engines are most likely to disagree on.

// nullText is how the transcript writes a NULL column, so a NULL and an empty
// string, which a non-pointer target would conflate, read differently.
const nullText = "NULL"

// crudStep is one statement of the workload and how its result is recorded.
type crudStep struct {
	name string
	run  func(t *testing.T, c Config) []string
}

// affected executes a write and records its rows-affected count.
func affected(name string, build func(d sqlb.Dialect) statement) crudStep {
	return crudStep{name, func(t *testing.T, c Config) []string {
		t.Helper()
		q, err := build(c.Dialect).Build()
		if err != nil {
			t.Fatalf("%s: build: %v", name, err)
		}
		res, err := c.DB.ExecContext(context.Background(), q.SQL, q.Args...)
		if err != nil {
			t.Fatalf("%s: %s: %v", name, q.SQL, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			t.Fatalf("%s: RowsAffected: %v", name, err)
		}
		return []string{fmt.Sprintf("%s: %d affected", name, n)}
	}}
}

// dumpUsers records every user, every column, in id order. A NULL name is
// written as NULL, never as the empty string it would scan to without a
// pointer target.
func dumpUsers(name string, where sqlb.Predicate) crudStep {
	return crudStep{name, func(t *testing.T, c Config) []string {
		t.Helper()
		var (
			id, age   int64
			email     string
			nameVal   *string
			active    bool
			collected []string
		)
		q, err := sqlb.NewSelect(c.Dialect).Columns(
			sqlb.Bind(UserID, &id), sqlb.Bind(UserEmail, &email), sqlb.Bind(sqlb.Nullable(UserName), &nameVal),
			sqlb.Bind(UserAge, &age), sqlb.Bind(UserActive, &active),
		).From(Users).Where(where).Order(sqlb.OrderBy(UserID, sqlb.SortAsc)).Build()
		if err != nil {
			t.Fatalf("%s: build: %v", name, err)
		}
		if err := sqlb.Each(context.Background(), c.DB, q, func() error {
			n := nullText
			if nameVal != nil {
				n = *nameVal
			}
			collected = append(collected, fmt.Sprintf("%s: user %d|%s|%s|%d|%t", name, id, email, n, age, active))
			return nil
		}); err != nil {
			t.Fatalf("%s: %s: %v", name, q.SQL, err)
		}
		if len(collected) == 0 {
			return []string{name + ": no rows"}
		}
		return collected
	}}
}

// dumpPosts records every post, every column, in id order.
func dumpPosts(name string) crudStep {
	return crudStep{name, func(t *testing.T, c Config) []string {
		t.Helper()
		var (
			id, userID, views int64
			title             string
			collected         []string
		)
		q, err := sqlb.NewSelect(c.Dialect).Columns(
			sqlb.Bind(PostID, &id), sqlb.Bind(PostUserID, &userID), sqlb.Bind(PostTitle, &title), sqlb.Bind(PostViews, &views),
		).From(Posts).Order(sqlb.OrderBy(PostID, sqlb.SortAsc)).Build()
		if err != nil {
			t.Fatalf("%s: build: %v", name, err)
		}
		if err := sqlb.Each(context.Background(), c.DB, q, func() error {
			collected = append(collected, fmt.Sprintf("%s: post %d|%d|%s|%d", name, id, userID, title, views))
			return nil
		}); err != nil {
			t.Fatalf("%s: %s: %v", name, q.SQL, err)
		}
		if len(collected) == 0 {
			return []string{name + ": no rows"}
		}
		return collected
	}}
}

// ids records the ids a predicate selects, in id order.
func ids(name string, p sqlb.Predicate) crudStep {
	return crudStep{name, func(t *testing.T, c Config) []string {
		t.Helper()
		return []string{fmt.Sprintf("%s: %v", name, idsWhere(t, c, p))}
	}}
}

// userRow is the full set of assignments for one user.
func userRow(id int64, email string, name *string, age int64, active bool) []sqlb.Assignment {
	return []sqlb.Assignment{
		sqlb.Set(UserID, id), sqlb.Set(UserEmail, email), sqlb.Set(sqlb.Nullable(UserName), name),
		sqlb.Set(UserAge, age), sqlb.Set(UserActive, active),
	}
}

func postRow(id, userID int64, title string, views int64) []sqlb.Assignment {
	return []sqlb.Assignment{sqlb.Set(PostID, id), sqlb.Set(PostUserID, userID), sqlb.Set(PostTitle, title), sqlb.Set(PostViews, views)}
}

// crudWorkload is the script. Its order is the transcript's order.
func crudWorkload() []crudStep {
	return []crudStep{
		// Create: one row per statement, then several rows in one statement.
		affected("insert one", func(d sqlb.Dialect) statement {
			return sqlb.NewInsert(d).Into(Users).Row(userRow(1, "Ada@Example.com", strPtr("Ada"), 36, true)...)
		}),
		affected("insert many", func(d sqlb.Dialect) statement {
			return sqlb.NewInsert(d).Into(Users).
				Row(userRow(2, "bo@example.com", strPtr("Bo"), 41, true)...).
				Row(userRow(3, "CY@example.com", nil, 29, false)...).
				Row(userRow(4, "di@Example.COM", strPtr("Di"), 52, true)...).
				Row(userRow(5, "50%off@shop", strPtr("Deal"), 18, false)...)
		}),
		affected("insert posts", func(d sqlb.Dialect) statement {
			return sqlb.NewInsert(d).Into(Posts).
				Row(postRow(1, 1, "hello", 10)...).
				Row(postRow(2, 1, "again", 30)...).
				Row(postRow(3, 2, "first", 5)...).
				Row(postRow(4, 4, "only", 70)...)
		}),
		dumpUsers("after create", sqlb.True()),
		dumpPosts("after create"),

		// Read: the predicates a library filters with.
		ids("Eq exact", sqlb.Eq(UserEmail, "bo@example.com")),
		ids("EqFold", sqlb.EqFold(UserEmail, "ADA@EXAMPLE.COM")),
		ids("In", sqlb.In(UserID, int64(2), int64(4), int64(99))),
		ids("InFold", sqlb.InFold(UserEmail, "cy@EXAMPLE.com", "DI@example.com")),
		ids("NotIn", sqlb.NotIn(UserID, int64(1), int64(2))),
		ids("Greater", sqlb.Greater(UserAge, int64(36))),
		ids("Between", sqlb.Between(UserAge, int64(29), int64(41))),
		ids("IsNull", sqlb.IsNull(sqlb.Nullable(UserName))),
		ids("active", sqlb.Eq(UserActive, true)),
		ids("ContainsFold", sqlb.ContainsFold(UserEmail, "EXAMPLE")),
		ids("Contains escapes %", sqlb.Contains(UserEmail, "50%")),
		ids("StartsWithFold", sqlb.StartsWithFold(UserEmail, "D")),
		ids("EndsWithFold", sqlb.EndsWithFold(UserEmail, ".com")),
		ids("And Or Not", sqlb.And(sqlb.Eq(UserActive, true), sqlb.Or(sqlb.Greater(UserAge, int64(50)), sqlb.Not(sqlb.EqFold(UserEmail, "bo@EXAMPLE.com"))))),
		ids("empty In", sqlb.In[int64](UserID)),
		pageStep("page 1", 2, 0),
		pageStep("page 2", 2, 2),
		pageStep("page 3", 2, 4),
		aggregateStep(),
		joinStep(),

		// Update: a change to every matched row, a change to none, a NULL,
		// an expression, and a filter by a fold.
		affected("update age+1 where active", func(d sqlb.Dialect) statement {
			return sqlb.NewUpdate(d).Table(Users).Set(sqlb.SetExpr(UserAge, sqlb.Add(UserAge, sqlb.Param(int64(1))))).Where(sqlb.Eq(UserActive, true))
		}),
		affected("update matching nothing", func(d sqlb.Dialect) statement {
			return sqlb.NewUpdate(d).Table(Users).Set(sqlb.Set(UserAge, int64(1))).Where(sqlb.Eq(UserID, int64(99)))
		}),
		affected("update to the value it already has", func(d sqlb.Dialect) statement {
			return sqlb.NewUpdate(d).Table(Users).Set(sqlb.Set(UserActive, true)).Where(sqlb.Eq(UserID, int64(1)))
		}),
		affected("update name to NULL", func(d sqlb.Dialect) statement {
			return sqlb.NewUpdate(d).Table(Users).Set(sqlb.Set(sqlb.Nullable(UserName), (*string)(nil))).Where(sqlb.Eq(UserID, int64(4)))
		}),
		affected("update by EqFold", func(d sqlb.Dialect) statement {
			return sqlb.NewUpdate(d).Table(Users).Set(sqlb.Set(UserActive, true), sqlb.Set(sqlb.Nullable(UserName), strPtr("Cy"))).
				Where(sqlb.EqFold(UserEmail, "cy@example.COM"))
		}),
		affected("update posts views", func(d sqlb.Dialect) statement {
			return sqlb.NewUpdate(d).Table(Posts).Set(sqlb.SetExpr(PostViews, sqlb.Add(PostViews, PostViews))).Where(sqlb.Eq(PostUserID, int64(1)))
		}),
		dumpUsers("after update", sqlb.True()),
		dumpPosts("after update"),

		// Delete: by a list, by a fold, and matching nothing.
		affected("delete posts of user 1", func(d sqlb.Dialect) statement {
			return sqlb.NewDelete(d).From(Posts).Where(sqlb.Eq(PostUserID, int64(1)))
		}),
		affected("delete by InFold", func(d sqlb.Dialect) statement {
			return sqlb.NewDelete(d).From(Users).Where(sqlb.InFold(UserEmail, "ADA@example.com", "50%OFF@SHOP"))
		}),
		affected("delete matching nothing", func(d sqlb.Dialect) statement {
			return sqlb.NewDelete(d).From(Users).Where(sqlb.Eq(UserID, int64(99)))
		}),
		dumpUsers("after delete", sqlb.True()),
		dumpPosts("after delete"),
	}
}

// pageStep records one page of users ordered by age descending, id breaking
// ties, which is how a library pages without an engine-chosen order.
func pageStep(name string, limit, offset int) crudStep {
	return crudStep{name, func(t *testing.T, c Config) []string {
		t.Helper()
		var id int64
		got := rows(t, c, sqlb.NewSelect(c.Dialect).Columns(sqlb.Bind(UserID, &id)).From(Users).
			Order(sqlb.OrderBy(UserAge, sqlb.SortDesc), sqlb.OrderBy(UserID, sqlb.SortAsc)).Limit(limit).Offset(offset), &id)
		return []string{fmt.Sprintf("%s: %v", name, got)}
	}}
}

// aggregateStep records count, sum, min and max over the whole table.
func aggregateStep() crudStep {
	return crudStep{"aggregates", func(t *testing.T, c Config) []string {
		t.Helper()
		var n, sum, lo, hi int64
		if err := one(t, c, sqlb.NewSelect(c.Dialect).Columns(
			sqlb.Bind(sqlb.Count(), &n), sqlb.Bind(sqlb.Sum(UserAge), &sum),
			sqlb.Bind(sqlb.Min(UserAge), &lo), sqlb.Bind(sqlb.Max(UserAge), &hi),
		).From(Users)); err != nil {
			t.Fatalf("aggregates: %v", err)
		}
		return []string{fmt.Sprintf("aggregates: count=%d sum=%d min=%d max=%d", n, sum, lo, hi)}
	}}
}

// joinStep records each user's post count and total views, LEFT joined so a
// user with no posts is reported with zeros rather than dropped.
func joinStep() crudStep {
	return crudStep{"join", func(t *testing.T, c Config) []string {
		t.Helper()
		var id, posts, views int64
		q, err := sqlb.NewSelect(c.Dialect).Columns(
			sqlb.Bind(UserID, &id), sqlb.Bind(sqlb.CountOf(PostID), &posts),
			sqlb.Bind(sqlb.Coalesce(sqlb.Sum(PostViews), sqlb.Param(int64(0))), &views),
		).From(Users).Join(sqlb.Join{Type: sqlb.JoinLeft, Table: Posts, On: []sqlb.Predicate{sqlb.EqExpr(PostUserID, UserID)}}).
			GroupBy(UserID).Order(sqlb.OrderBy(UserID, sqlb.SortAsc)).Build()
		if err != nil {
			t.Fatalf("join: build: %v", err)
		}
		var out []string
		if err := sqlb.Each(context.Background(), c.DB, q, func() error {
			out = append(out, fmt.Sprintf("join: user %d posts=%d views=%d", id, posts, views))
			return nil
		}); err != nil {
			t.Fatalf("join: %s: %v", q.SQL, err)
		}
		return out
	}}
}

// CRUDTranscript is what the workload must produce, line for line, on every
// engine. It is the golden: an engine that differs anywhere fails naming the
// line, and changing a line here is changing what every engine is held to.
var CRUDTranscript = []string{
	"insert one: 1 affected",
	"insert many: 4 affected",
	"insert posts: 4 affected",
	"after create: user 1|Ada@Example.com|Ada|36|true",
	"after create: user 2|bo@example.com|Bo|41|true",
	"after create: user 3|CY@example.com|NULL|29|false",
	"after create: user 4|di@Example.COM|Di|52|true",
	"after create: user 5|50%off@shop|Deal|18|false",
	"after create: post 1|1|hello|10",
	"after create: post 2|1|again|30",
	"after create: post 3|2|first|5",
	"after create: post 4|4|only|70",
	"Eq exact: [2]",
	"EqFold: [1]",
	"In: [2 4]",
	"InFold: [3 4]",
	"NotIn: [3 4 5]",
	"Greater: [2 4]",
	"Between: [1 2 3]",
	"IsNull: [3]",
	"active: [1 2 4]",
	"ContainsFold: [1 2 3 4]",
	"Contains escapes %: [5]",
	"StartsWithFold: [4]",
	"EndsWithFold: [1 2 3 4]",
	"And Or Not: [1 4]",
	"empty In: []",
	"page 1: [4 2]",
	"page 2: [1 3]",
	"page 3: [5]",
	"aggregates: count=5 sum=176 min=18 max=52",
	"join: user 1 posts=2 views=40",
	"join: user 2 posts=1 views=5",
	"join: user 3 posts=0 views=0",
	"join: user 4 posts=1 views=70",
	"join: user 5 posts=0 views=0",
	"update age+1 where active: 3 affected",
	"update matching nothing: 0 affected",
	"update to the value it already has: 1 affected",
	"update name to NULL: 1 affected",
	"update by EqFold: 1 affected",
	"update posts views: 2 affected",
	"after update: user 1|Ada@Example.com|Ada|37|true",
	"after update: user 2|bo@example.com|Bo|42|true",
	"after update: user 3|CY@example.com|Cy|29|true",
	"after update: user 4|di@Example.COM|NULL|53|true",
	"after update: user 5|50%off@shop|Deal|18|false",
	"after update: post 1|1|hello|20",
	"after update: post 2|1|again|60",
	"after update: post 3|2|first|5",
	"after update: post 4|4|only|70",
	"delete posts of user 1: 2 affected",
	"delete by InFold: 2 affected",
	"delete matching nothing: 0 affected",
	"after delete: user 2|bo@example.com|Bo|42|true",
	"after delete: user 3|CY@example.com|Cy|29|true",
	"after delete: user 4|di@Example.COM|NULL|53|true",
	"after delete: post 3|2|first|5",
	"after delete: post 4|4|only|70",
}

// caseCRUDTranscript runs the workload and compares its transcript with
// CRUDTranscript, reporting every differing line rather than the first.
func caseCRUDTranscript(t *testing.T, c Config) {
	var got []string
	for _, s := range crudWorkload() {
		got = append(got, s.run(t, c)...)
	}
	if diff := transcriptDiff(got, CRUDTranscript); diff != "" {
		t.Errorf("the transcript differs from CRUDTranscript:\n%s", diff)
	}
}

// transcriptDiff lists every line where got and want differ, by line number,
// or returns "" when they are identical.
func transcriptDiff(got, want []string) string {
	var b strings.Builder
	for i := 0; i < len(got) || i < len(want); i++ {
		var g, w string
		if i < len(got) {
			g = got[i]
		}
		if i < len(want) {
			w = want[i]
		}
		if g != w {
			fmt.Fprintf(&b, "  line %d:\n    got  %q\n    want %q\n", i+1, g, w)
		}
	}
	return b.String()
}
