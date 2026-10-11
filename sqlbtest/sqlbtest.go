// Package sqlbtest is the conformance suite every sqlb driver integration
// runs. It exists because rendering SQL and executing it are different
// claims: the builder's own tests prove the text, and only a real database
// proves that the placeholders, the argument order and the scan targets line
// up. A driver integration that passes Run is known to carry the builder end
// to end, not merely to compile against it.
//
// The suite has no third-party dependencies. It takes a sqlb.DBTX, so it runs
// against a *sql.DB, a *sql.Tx, or any wrapper a caller has of their own.
//
// sqlb generates no DDL, so the caller supplies the schema in its engine's
// own spelling. The table and column declarations the suite binds to are
// exported here, and Schema names the shape they expect.
package sqlbtest

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"testing"

	"github.com/ubgo/sqlb"
)

// The tables the suite works against. A caller creates them with its own DDL
// before calling Run. The names are prefixed so they cannot collide with an
// application's own tables in a shared test database.
var (
	Users = sqlb.Table{Name: "sqlb_conformance_user", Alias: "u"}
	Posts = sqlb.Table{Name: "sqlb_conformance_post", Alias: "p"}
)

// Columns of Users. Name is the nullable one, on purpose: a NULL that reaches
// a non-pointer target is the failure a text-only test never sees.
var (
	UserID     = sqlb.Col[int64](Users, "id")
	UserEmail  = sqlb.Col[string](Users, "email")
	UserName   = sqlb.Col[string](Users, "name")
	UserAge    = sqlb.Col[int64](Users, "age")
	UserActive = sqlb.Col[bool](Users, "active")
)

// Columns of Posts.
var (
	PostID     = sqlb.Col[int64](Posts, "id")
	PostUserID = sqlb.Col[int64](Posts, "user_id")
	PostTitle  = sqlb.Col[string](Posts, "title")
	PostViews  = sqlb.Col[int64](Posts, "views")
)

// Schema describes the tables Run needs, engine-neutral. A caller translates
// it into its own DDL; the types named here are the loosest that carry the
// suite, so any engine has a spelling for each.
//
// Text must compare by its bytes, as Postgres and SQLite compare it by
// default; on MySQL that means a utf8mb4_bin collation. The case_sensitive_eq
// case checks it, and an integration running MySQL's default collation skips
// that case with the reason, as contrib/driver-mysql does.
//
//	sqlb_conformance_user(id integer primary key, email text not null,
//	                      name text null, age integer not null,
//	                      active boolean not null)
//	sqlb_conformance_post(id integer primary key, user_id integer not null,
//	                      title text not null, views integer not null)
const Schema = `sqlb_conformance_user(id, email, name, age, active); sqlb_conformance_post(id, user_id, title, views)`

// Config is what a driver integration passes to Run.
type Config struct {
	// DB is the handle the suite executes against. It must already point at
	// a database where Setup has been applied.
	DB sqlb.DBTX

	// Dialect is the dialect the integration builds with. The suite asserts
	// nothing about its spelling; it only uses it to build.
	Dialect sqlb.Dialect

	// Setup runs once before the suite, and Teardown once after. Both are
	// raw DDL because sqlb generates none. Setup must create the two tables
	// Schema describes; Teardown usually drops them.
	Setup    []string
	Teardown []string

	// Skip excuses a case by name with a reason that is printed. Use it for
	// a genuine engine limitation, never to hide a failure: the reason ends
	// up in the test log, where a reviewer reads it.
	Skip map[string]string
}

// Case is one conformance case. Cases is the full list, in run order, so a
// caller can see what Run covers without reading the source.
type Case struct {
	Name string
	Why  string
	run  func(t *testing.T, c Config)
}

// Cases returns the suite, in run order.
func Cases() []Case {
	return []Case{
		{"insert_select_one", "a row written by the insert builder comes back through One with every target filled", caseInsertSelectOne},
		{"param_order", "several parameters in several clauses bind in the order the SQL names them", caseParamOrder},
		{"null_scan", "a NULL column reaches a *T target instead of failing the scan", caseNullScan},
		{"no_rows", "One reports ErrNoRows rather than leaving the targets untouched", caseNoRows},
		{"multiple_rows", "One reports ErrMultipleRows rather than silently taking the first", caseMultipleRows},
		{"each_order", "Each yields every row, in the ORDER BY order, reusing the same targets", caseEachOrder},
		{"empty_in", "IN with no values runs as FALSE instead of being a syntax error", caseEmptyIn},
		{"update", "the update builder writes, and only where the predicate says", caseUpdate},
		{"delete", "the delete builder removes, and only where the predicate says", caseDelete},
		{"join_group", "a join with an aggregate and GROUP BY scans both sides", caseJoinGroup},
		{"limit_offset", "LIMIT and OFFSET are bound parameters and page correctly", caseLimitOffset},
		{"search_escapes", "Contains, StartsWith and EndsWith match a value's %, _, ! and backslash as themselves", caseSearchEscapes},
		{"set_ops", "UNION, UNION ALL, INTERSECT and EXCEPT combine two selects with the counts each promises", caseSetOps},
		{"cte", "WITH and WITH RECURSIVE run, the recursive one counting to a bound", caseCTE},
		{"window", "ROW_NUMBER over a partition numbers each group from 1 in its order", caseWindow},
		{"expressions", "CASE, COALESCE, concat, upper and lower evaluate as the core renders them", caseExpressions},
		{"subqueries", "EXISTS, IN over a subquery and a row IN list select the same rows", caseSubqueries},
		{"case_fold", "EqFold, InFold, NotInFold, ContainsFold, StartsWithFold and EndsWithFold select the same ids on every engine, and filter an UPDATE and a DELETE", caseFold},
		{"crud_transcript", "one scripted create, read, update and delete workload produces CRUDTranscript line for line: every row, aggregate, page and rows-affected count", caseCRUDTranscript},
		{"case_sensitive_eq", "Eq and In compare exactly, so folding happens only where it is asked for; needs text compared by its bytes", caseCaseSensitiveEq},
	}
}

// Run executes the whole suite as subtests. A driver integration's test is
// one call to this.
func Run(t *testing.T, c Config) {
	t.Helper()
	if c.DB == nil {
		t.Fatal("sqlbtest: Config.DB is nil")
	}
	if c.Dialect == nil {
		t.Fatal("sqlbtest: Config.Dialect is nil")
	}
	ctx := context.Background()
	for _, stmt := range c.Setup {
		if _, err := c.DB.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("sqlbtest: setup %q: %v", stmt, err)
		}
	}
	if len(c.Teardown) > 0 {
		t.Cleanup(func() {
			for _, stmt := range c.Teardown {
				if _, err := c.DB.ExecContext(ctx, stmt); err != nil {
					t.Errorf("sqlbtest: teardown %q: %v", stmt, err)
				}
			}
		})
	}
	for _, cs := range Cases() {
		t.Run(cs.Name, func(t *testing.T) {
			if why, ok := c.Skip[cs.Name]; ok {
				t.Skipf("skipped by the integration: %s", why)
			}
			reset(t, c)
			cs.run(t, c)
		})
	}
}

// reset empties both tables, so each case starts from a known state and the
// order of the cases cannot matter.
func reset(t *testing.T, c Config) {
	t.Helper()
	ctx := context.Background()
	for _, tb := range []sqlb.Table{Posts, Users} {
		q, err := sqlb.NewDelete(c.Dialect).From(tb).All().Build()
		if err != nil {
			t.Fatalf("reset build: %v", err)
		}
		if _, err := c.DB.ExecContext(ctx, q.SQL, q.Args...); err != nil {
			t.Fatalf("reset exec: %v", err)
		}
	}
}

// seedUser inserts one user and returns its id, which the caller chose: the
// suite never relies on an engine's generated keys, because every engine
// spells that differently.
func seedUser(t *testing.T, c Config, id int64, email, name string, age int64, active bool) {
	t.Helper()
	ins := sqlb.NewInsert(c.Dialect).Into(Users).Row(
		sqlb.Set(UserID, id),
		sqlb.Set(UserEmail, email),
		sqlb.Set(UserName, name),
		sqlb.Set(UserAge, age),
		sqlb.Set(UserActive, active),
	)
	exec(t, c, ins)
}

func seedPost(t *testing.T, c Config, id, userID int64, title string, views int64) {
	t.Helper()
	ins := sqlb.NewInsert(c.Dialect).Into(Posts).Row(
		sqlb.Set(PostID, id),
		sqlb.Set(PostUserID, userID),
		sqlb.Set(PostTitle, title),
		sqlb.Set(PostViews, views),
	)
	exec(t, c, ins)
}

// statement is what every builder satisfies once it can Build.
type statement interface{ Build() (sqlb.Query, error) }

func exec(t *testing.T, c Config, s statement) sqlb.Query {
	t.Helper()
	q, err := s.Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if _, err := c.DB.ExecContext(context.Background(), q.SQL, q.Args...); err != nil {
		t.Fatalf("exec %s: %v", q.SQL, err)
	}
	return q
}

func one(t *testing.T, c Config, s statement) error {
	t.Helper()
	q, err := s.Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return sqlb.One(context.Background(), c.DB, q)
}

func caseInsertSelectOne(t *testing.T, c Config) {
	seedUser(t, c, 1, "a@example.com", "Ada", 36, true)

	var id, age int64
	var email string
	var active bool
	sel := sqlb.NewSelect(c.Dialect).
		Columns(sqlb.Bind(UserID, &id), sqlb.Bind(UserEmail, &email), sqlb.Bind(UserAge, &age), sqlb.Bind(UserActive, &active)).
		From(Users).Where(sqlb.Eq(UserEmail, "a@example.com"))
	if err := one(t, c, sel); err != nil {
		t.Fatalf("One: %v", err)
	}
	if id != 1 || email != "a@example.com" || age != 36 || !active {
		t.Fatalf("got id=%d email=%q age=%d active=%v", id, email, age, active)
	}
}

func caseParamOrder(t *testing.T, c Config) {
	seedUser(t, c, 1, "a@example.com", "Ada", 36, true)
	seedUser(t, c, 2, "b@example.com", "Bo", 41, true)

	// Four parameters across three clauses. A driver that binds them out of
	// order returns the wrong row rather than an error, which is why this
	// case asserts the value and not just the absence of a failure.
	var email string
	sel := sqlb.NewSelect(c.Dialect).
		Columns(sqlb.Bind(UserEmail, &email)).
		From(Users).
		Where(sqlb.Greater(UserAge, 30), sqlb.Less(UserAge, 40), sqlb.Eq(UserActive, true)).
		Order(sqlb.OrderBy(UserID, sqlb.SortAsc)).
		Limit(1)
	if err := one(t, c, sel); err != nil {
		t.Fatalf("One: %v", err)
	}
	if email != "a@example.com" {
		t.Fatalf("parameters bound out of order: got %q", email)
	}
}

func caseNullScan(t *testing.T, c Config) {
	// The NULL is written explicitly rather than through SetDefault, because
	// DEFAULT inside a VALUES list is not portable: Postgres has it, SQLite
	// does not. What this case is about is the read path.
	exec(t, c, sqlb.NewInsert(c.Dialect).Into(Users).Row(
		sqlb.Set(UserID, int64(1)),
		sqlb.Set(UserEmail, "a@example.com"),
		sqlb.Set(sqlb.Nullable(UserName), (*string)(nil)),
		sqlb.Set(UserAge, int64(36)),
		sqlb.Set(UserActive, true),
	))

	var name *string
	var email string
	sel := sqlb.NewSelect(c.Dialect).
		Columns(sqlb.Bind(sqlb.Nullable(UserName), &name), sqlb.Bind(UserEmail, &email)).
		From(Users).Where(sqlb.Eq(UserID, int64(1)))
	if err := one(t, c, sel); err != nil {
		t.Fatalf("One: %v", err)
	}
	if name != nil {
		t.Fatalf("want nil for a NULL column, got %q", *name)
	}
	if email != "a@example.com" {
		t.Fatalf("the non-null column beside it: got %q", email)
	}
}

func caseNoRows(t *testing.T, c Config) {
	var email string
	sel := sqlb.NewSelect(c.Dialect).
		Columns(sqlb.Bind(UserEmail, &email)).
		From(Users).Where(sqlb.Eq(UserID, int64(999)))
	if err := one(t, c, sel); !errors.Is(err, sqlb.ErrNoRows) {
		t.Fatalf("want ErrNoRows, got %v", err)
	}
}

func caseMultipleRows(t *testing.T, c Config) {
	seedUser(t, c, 1, "a@example.com", "Ada", 36, true)
	seedUser(t, c, 2, "b@example.com", "Bo", 41, true)

	var email string
	sel := sqlb.NewSelect(c.Dialect).
		Columns(sqlb.Bind(UserEmail, &email)).
		From(Users).Where(sqlb.Eq(UserActive, true))
	if err := one(t, c, sel); !errors.Is(err, sqlb.ErrMultipleRows) {
		t.Fatalf("want ErrMultipleRows, got %v", err)
	}
}

func caseEachOrder(t *testing.T, c Config) {
	seedUser(t, c, 1, "a@example.com", "Ada", 36, true)
	seedUser(t, c, 2, "b@example.com", "Bo", 41, true)
	seedUser(t, c, 3, "c@example.com", "Cy", 28, true)

	var age int64
	var email string
	sel := sqlb.NewSelect(c.Dialect).
		Columns(sqlb.Bind(UserEmail, &email), sqlb.Bind(UserAge, &age)).
		From(Users).
		Order(sqlb.OrderBy(UserAge, sqlb.SortAsc))
	q, err := sel.Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	var got []string
	err = sqlb.Each(context.Background(), c.DB, q, func() error {
		got = append(got, fmt.Sprintf("%s:%d", email, age))
		return nil
	})
	if err != nil {
		t.Fatalf("Each: %v", err)
	}
	want := []string{"c@example.com:28", "a@example.com:36", "b@example.com:41"}
	if len(got) != len(want) {
		t.Fatalf("got %d rows, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("row %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

func caseEmptyIn(t *testing.T, c Config) {
	seedUser(t, c, 1, "a@example.com", "Ada", 36, true)

	// IN () is a syntax error in every engine, so the builder renders the
	// constant instead. This case proves the rendered constant runs.
	var email string
	sel := sqlb.NewSelect(c.Dialect).
		Columns(sqlb.Bind(UserEmail, &email)).
		From(Users).Where(sqlb.In(UserID))
	if err := one(t, c, sel); !errors.Is(err, sqlb.ErrNoRows) {
		t.Fatalf("empty IN should match nothing: %v", err)
	}
	sel = sqlb.NewSelect(c.Dialect).
		Columns(sqlb.Bind(UserEmail, &email)).
		From(Users).Where(sqlb.NotIn(UserID))
	if err := one(t, c, sel); err != nil {
		t.Fatalf("empty NOT IN should match everything: %v", err)
	}
}

func caseUpdate(t *testing.T, c Config) {
	seedUser(t, c, 1, "a@example.com", "Ada", 36, true)
	seedUser(t, c, 2, "b@example.com", "Bo", 41, true)

	exec(t, c, sqlb.NewUpdate(c.Dialect).Table(Users).
		Set(sqlb.Set(UserAge, int64(37))).
		Where(sqlb.Eq(UserID, int64(1))))

	ages := collectAges(t, c)
	if len(ages) != 2 || ages[0] != 37 || ages[1] != 41 {
		t.Fatalf("update touched the wrong rows: %v", ages)
	}
}

func caseDelete(t *testing.T, c Config) {
	seedUser(t, c, 1, "a@example.com", "Ada", 36, true)
	seedUser(t, c, 2, "b@example.com", "Bo", 41, true)

	exec(t, c, sqlb.NewDelete(c.Dialect).From(Users).Where(sqlb.Eq(UserID, int64(1))))

	ages := collectAges(t, c)
	if len(ages) != 1 || ages[0] != 41 {
		t.Fatalf("delete touched the wrong rows: %v", ages)
	}
}

// collectAges reads every age, ordered, so a case can assert what a write did
// to the whole table rather than to one row.
func collectAges(t *testing.T, c Config) []int64 {
	t.Helper()
	var age int64
	q, err := sqlb.NewSelect(c.Dialect).
		Columns(sqlb.Bind(UserAge, &age)).
		From(Users).Order(sqlb.OrderBy(UserID, sqlb.SortAsc)).Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	var out []int64
	if err := sqlb.Each(context.Background(), c.DB, q, func() error {
		out = append(out, age)
		return nil
	}); err != nil {
		t.Fatalf("Each: %v", err)
	}
	return out
}

func caseJoinGroup(t *testing.T, c Config) {
	seedUser(t, c, 1, "a@example.com", "Ada", 36, true)
	seedUser(t, c, 2, "b@example.com", "Bo", 41, true)
	seedPost(t, c, 1, 1, "first", 10)
	seedPost(t, c, 2, 1, "second", 5)
	seedPost(t, c, 3, 2, "third", 7)

	var email string
	var posts, views int64
	q, err := sqlb.NewSelect(c.Dialect).
		Columns(
			sqlb.Bind(UserEmail, &email),
			sqlb.Bind(sqlb.Count(), &posts),
			sqlb.Bind(sqlb.Sum(PostViews), &views),
		).
		From(Users).
		Join(sqlb.Join{Type: sqlb.JoinInner, Table: Posts, On: []sqlb.Predicate{sqlb.EqExpr(UserID, PostUserID)}}).
		GroupBy(UserEmail).
		Order(sqlb.OrderBy(UserEmail, sqlb.SortAsc)).
		Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	got := map[string][2]int64{}
	if err := sqlb.Each(context.Background(), c.DB, q, func() error {
		got[email] = [2]int64{posts, views}
		return nil
	}); err != nil {
		t.Fatalf("Each: %v", err)
	}
	if len(got) != 2 {
		keys := make([]string, 0, len(got))
		for k := range got {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		t.Fatalf("want 2 groups, got %v", keys)
	}
	if got["a@example.com"] != [2]int64{2, 15} {
		t.Fatalf("group a: %v", got["a@example.com"])
	}
	if got["b@example.com"] != [2]int64{1, 7} {
		t.Fatalf("group b: %v", got["b@example.com"])
	}
}

func caseLimitOffset(t *testing.T, c Config) {
	seedUser(t, c, 1, "a@example.com", "Ada", 36, true)
	seedUser(t, c, 2, "b@example.com", "Bo", 41, true)
	seedUser(t, c, 3, "c@example.com", "Cy", 28, true)

	var email string
	sel := sqlb.NewSelect(c.Dialect).
		Columns(sqlb.Bind(UserEmail, &email)).
		From(Users).
		Order(sqlb.OrderBy(UserID, sqlb.SortAsc)).
		Limit(1).Offset(1)
	if err := one(t, c, sel); err != nil {
		t.Fatalf("One: %v", err)
	}
	if email != "b@example.com" {
		t.Fatalf("LIMIT/OFFSET paged wrong: got %q", email)
	}
}

// RunReturning executes the RETURNING case, which the main suite cannot hold.
//
// Run takes the dialect as a runtime value, so its statements are
// Select[render.Dialect] and their kin: universal SQL only, by construction.
// RETURNING is a capability, and InsertReturning is constrained to a dialect
// type that has it, so a statement whose dialect is only known at runtime
// cannot ask for it and the compiler says so. A driver whose dialect has
// RETURNING calls this with its concrete dialect; a driver whose dialect does
// not cannot write the call, which is the skip made into a type.
func RunReturning[D sqlb.ReturningDialect](t *testing.T, c Config, d D) {
	t.Helper()
	// Call it after Run, in the same test function: Run applied Setup and
	// registered Teardown, so this only resets the tables like every case.
	t.Run("returning", func(t *testing.T) {
		reset(t, c)
		var id int64
		var email string
		ins := sqlb.InsertReturning(sqlb.NewInsert(d).Into(Users).Row(
			sqlb.Set(UserID, int64(7)),
			sqlb.Set(UserEmail, "r@example.com"),
			sqlb.Set(UserName, "Ray"),
			sqlb.Set(UserAge, int64(50)),
			sqlb.Set(UserActive, true),
		), sqlb.Bind(UserID, &id), sqlb.Bind(UserEmail, &email))
		if err := one(t, c, ins); err != nil {
			t.Fatalf("One on INSERT RETURNING: %v", err)
		}
		if id != 7 || email != "r@example.com" {
			t.Fatalf("RETURNING filled wrong: id=%d email=%q", id, email)
		}
	})
}

// count runs a COUNT(*) over Users filtered by p.
func count(t *testing.T, c Config, p sqlb.Predicate) int64 {
	t.Helper()
	var n int64
	if err := one(t, c, sqlb.NewSelect(c.Dialect).Columns(sqlb.Bind(sqlb.Count(), &n)).From(Users).Where(p)); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

// rows collects one int64 column of a select.
func rows(t *testing.T, c Config, s *sqlb.Select[sqlb.Dialect], target *int64) []int64 {
	t.Helper()
	q, err := s.Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	var out []int64
	if err := sqlb.Each(context.Background(), c.DB, q, func() error { out = append(out, *target); return nil }); err != nil {
		t.Fatalf("Each %s: %v", q.SQL, err)
	}
	return out
}

func caseSearchEscapes(t *testing.T, c Config) {
	for i, e := range []string{"50%@x", "500@x", "a_b@x", "axb@x", "wow!@x", `back\slash@x`, "plain@x"} {
		seedUser(t, c, int64(i+1), e, "n", 30, true)
	}
	for _, tc := range []struct {
		name string
		p    sqlb.Predicate
		want int64
	}{
		{"a percent is a percent", sqlb.Contains(UserEmail, "50%"), 1},
		{"an underscore is an underscore", sqlb.Contains(UserEmail, "a_b"), 1},
		{"the escape character is itself", sqlb.Contains(UserEmail, "wow!"), 1},
		{"a backslash is itself", sqlb.Contains(UserEmail, `k\s`), 1},
		{"a prefix", sqlb.StartsWith(UserEmail, "50"), 2},
		{"a suffix", sqlb.EndsWith(UserEmail, "b@x"), 2},
		{"the negation", sqlb.NotContains(UserEmail, "50%"), 6},
	} {
		if got := count(t, c, tc.p); got != tc.want {
			t.Errorf("%s: %d rows, want %d", tc.name, got, tc.want)
		}
	}
}

func caseSetOps(t *testing.T, c Config) {
	for i := int64(1); i <= 3; i++ {
		seedUser(t, c, i, fmt.Sprintf("%d@x", i), "n", 30, true)
	}
	seedPost(t, c, 1, 1, "a", 1)
	seedPost(t, c, 2, 1, "b", 1)
	seedPost(t, c, 3, 2, "c", 1)
	var id int64
	users := func() *sqlb.Select[sqlb.Dialect] {
		return sqlb.NewSelect(c.Dialect).Columns(sqlb.Bind(UserID, &id)).From(Users)
	}
	posts := func() *sqlb.Select[sqlb.Dialect] { return sqlb.NewSelect(c.Dialect).Exprs(PostUserID).From(Posts) }
	for name, tc := range map[string]struct {
		s    *sqlb.Select[sqlb.Dialect]
		want int
	}{
		"UNION":     {users().Union(posts()), 3},
		"UNION ALL": {users().UnionAll(posts()), 6},
		"INTERSECT": {users().Intersect(posts()), 2},
		"EXCEPT":    {users().Except(posts()), 1},
	} {
		if got := rows(t, c, tc.s, &id); len(got) != tc.want {
			t.Errorf("%s: %d rows %v, want %d", name, len(got), got, tc.want)
		}
	}
}

func caseCTE(t *testing.T, c Config) {
	seedUser(t, c, 1, "a@x", "n", 30, true)
	seedUser(t, c, 2, "b@x", "n", 40, false)
	live := sqlb.Table{Name: "live", Alias: "l"}
	liveID := sqlb.Col[int64](live, "id")
	var id int64
	got := rows(t, c, sqlb.NewSelect(c.Dialect).
		With("live", sqlb.NewSelect(c.Dialect).Exprs(UserID).From(Users).Where(sqlb.Eq(UserActive, true))).
		Columns(sqlb.Bind(liveID, &id)).From(live), &id)
	if len(got) != 1 || got[0] != 1 {
		t.Errorf("WITH: %v, want [1]", got)
	}

	nums := sqlb.Table{Name: "nums", Alias: "n"}
	n := sqlb.Col[int64](nums, "n")
	seed := sqlb.NewSelect(c.Dialect).Exprs(sqlb.As(sqlb.Literal[int64]("1"), "n"))
	step := sqlb.NewSelect(c.Dialect).Exprs(sqlb.Add(n, sqlb.Literal[int64]("1"))).From(nums).Where(sqlb.Less(n, int64(5)))
	var sum int64
	if err := one(t, c, sqlb.NewSelect(c.Dialect).WithRecursive("nums", seed, step).
		Columns(sqlb.Bind(sqlb.Sum(n), &sum)).From(nums)); err != nil {
		t.Fatalf("WITH RECURSIVE: %v", err)
	}
	if sum != 15 {
		t.Errorf("WITH RECURSIVE summed %d, want 15", sum)
	}
}

func caseWindow(t *testing.T, c Config) {
	seedUser(t, c, 1, "a@x", "n", 30, true)
	seedUser(t, c, 2, "b@x", "n", 30, true)
	seedPost(t, c, 1, 1, "a", 10)
	seedPost(t, c, 2, 1, "b", 30)
	seedPost(t, c, 3, 1, "c", 20)
	seedPost(t, c, 4, 2, "d", 5)
	var id, rn int64
	w := sqlb.NewWindow().PartitionBy(PostUserID).OrderBy(sqlb.OrderBy(PostViews, sqlb.SortDesc))
	q, err := sqlb.NewSelect(c.Dialect).
		Columns(sqlb.Bind(PostID, &id), sqlb.Bind(sqlb.RowNumber().Over(w), &rn)).From(Posts).
		Order(sqlb.OrderBy(PostID, sqlb.SortAsc)).Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	got := map[int64]int64{}
	if err := sqlb.Each(context.Background(), c.DB, q, func() error { got[id] = rn; return nil }); err != nil {
		t.Fatalf("Each: %v", err)
	}
	want := map[int64]int64{1: 3, 2: 1, 3: 2, 4: 1}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("post %d row_number %d, want %d (all %v)", k, got[k], v, got)
		}
	}
}

func caseExpressions(t *testing.T, c Config) {
	seedUser(t, c, 1, "Ada@X", "n", 30, true)
	var label, email, lower, upper string
	q := sqlb.NewSelect(c.Dialect).Columns(
		sqlb.Bind(sqlb.Case[string]().When(sqlb.Greater(UserAge, int64(18)), sqlb.Param("adult")).Else(sqlb.Param("minor")), &label),
		sqlb.Bind(sqlb.Concat(sqlb.Coalesce(sqlb.Literal[string]("NULL"), UserEmail), sqlb.Param("!")), &email),
		sqlb.Bind(sqlb.Lower(UserEmail), &lower),
		sqlb.Bind(sqlb.Upper(UserEmail), &upper),
	).From(Users)
	if err := one(t, c, q); err != nil {
		t.Fatalf("select: %v", err)
	}
	if label != "adult" || email != "Ada@X!" || lower != "ada@x" || upper != "ADA@X" {
		t.Errorf("got %q %q %q %q", label, email, lower, upper)
	}
}

func caseSubqueries(t *testing.T, c Config) {
	for i := int64(1); i <= 3; i++ {
		seedUser(t, c, i, fmt.Sprintf("%d@x", i), "n", 30+i, true)
	}
	seedPost(t, c, 1, 1, "a", 1)
	seedPost(t, c, 2, 2, "b", 1)
	withPosts := sqlb.NewSelect(c.Dialect).Exprs(sqlb.Literal[int64]("1")).From(Posts).Where(sqlb.EqExpr(PostUserID, UserID))
	if got := count(t, c, sqlb.Exists(withPosts)); got != 2 {
		t.Errorf("EXISTS: %d, want 2", got)
	}
	if got := count(t, c, sqlb.InSubquery(UserID, sqlb.NewSelect(c.Dialect).Exprs(PostUserID).From(Posts))); got != 2 {
		t.Errorf("IN subquery: %d, want 2", got)
	}
	in := sqlb.RowIn(sqlb.Row(UserID, UserAge),
		sqlb.Row(sqlb.Param(int64(1)), sqlb.Param(int64(31))), sqlb.Row(sqlb.Param(int64(3)), sqlb.Param(int64(99))))
	if got := count(t, c, in); got != 1 {
		t.Errorf("row IN: %d, want 1", got)
	}
}
