package enginetest

import (
	"context"
	"database/sql"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ubgo/sqlb/contrib/vet"
	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/pg"
	"github.com/ubgo/sqlb/render"
	"github.com/ubgo/sqlb/schema"
	"github.com/ubgo/sqlb/sqlite"
)

// A table with a partial index, a descending index and a partial index whose
// predicate Postgres re-prints with a cast, each described by its own CREATE
// INDEX, plus one index described the old way, by name and columns only.
var (
	ixRuns       = expr.Table{Name: "ix_runs", Alias: "r"}
	ixID         = expr.Col[int64](ixRuns, "id")
	ixAccount    = expr.Col[int64](ixRuns, "account_id")
	ixStarted    = expr.Col[time.Time](ixRuns, "started_at")
	ixStatus     = expr.Col[string](ixRuns, "status")
	ixRecordPath = expr.Col[*string](ixRuns, "record_path")
)

// indexStmt is an engine's CREATE INDEX: what it builds and what it describes.
type indexStmt interface {
	Build() (ddl.Script, error)
	Describe() (schema.Index, error)
}

// indexFixture is one engine's statements for the table and its indexes.
type indexFixture struct {
	dialect string
	table   tableStmt
	indexes []indexStmt
	// plain is the old-style index: created by hand, described by name and
	// columns only, so neither its order nor anything else new is judged.
	plain string
}

func sqliteIndexFixture() indexFixture {
	return indexFixture{
		dialect: render.DialectSQLite,
		table: sqlite.CreateTable(ixRuns).Column(ddl.Column(ixID), ddl.Column(ixAccount), ddl.Column(ixStarted), ddl.Column(ixStatus), ddl.Column(ixRecordPath)).
			Clause(sqlite.PrimaryKey(ixID)),
		indexes: []indexStmt{
			sqlite.CreateIndex(sqlite.Index{Name: "ix_runs_open"}, ixRuns).On(sqlite.IndexKey(ixAccount)).Where(expr.IsNull(ixRecordPath)),
			sqlite.CreateIndex(sqlite.Index{Name: "ix_runs_recent"}, ixRuns).On(sqlite.IndexKey(ixAccount), sqlite.IndexKey(ixStarted).Desc()),
			sqlite.CreateIndex(sqlite.Index{Name: "ix_runs_active"}, ixRuns).On(sqlite.IndexKey(ixAccount)).Where(expr.Eq(ixStatus, "open")),
		},
		plain: "ix_runs_plain",
	}
}

func pgIndexFixture() indexFixture {
	return indexFixture{
		dialect: render.DialectPostgres,
		table: pg.CreateTable(ixRuns).Column(ddl.Column(ixID), ddl.Column(ixAccount), ddl.Column(ixStarted), ddl.Column(ixStatus), ddl.Column(ixRecordPath)).
			Clause(pg.PrimaryKey(ixID)),
		indexes: []indexStmt{
			pg.CreateIndex(pg.Index{Name: "ix_runs_open"}, ixRuns).On(pg.IndexKey(ixAccount)).Where(expr.IsNull(ixRecordPath)),
			pg.CreateIndex(pg.Index{Name: "ix_runs_recent"}, ixRuns).On(pg.IndexKey(ixAccount), pg.IndexKey(ixStarted).Desc()),
			pg.CreateIndex(pg.Index{Name: "ix_runs_active"}, ixRuns).On(pg.IndexKey(ixAccount)).Where(expr.Eq(ixStatus, "open")),
		},
		plain: "ix_runs_plain",
	}
}

// create runs the table's and every index's DDL, plus the old-style index,
// and returns the description: CREATE TABLE's, with each index's own.
func (f indexFixture) create(t *testing.T, ctx context.Context, db *sql.DB) schema.Table {
	t.Helper()
	exec := func(s ddl.Script, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		for _, x := range s {
			if _, err := db.ExecContext(ctx, x.SQL); err != nil {
				t.Fatalf("%s: %v", x.SQL, err)
			}
		}
	}
	exec(f.table.Build())
	desc, err := f.table.Describe()
	if err != nil {
		t.Fatal(err)
	}
	for _, ix := range f.indexes {
		exec(ix.Build())
		d, err := ix.Describe()
		if err != nil {
			t.Fatal(err)
		}
		desc.Indexes = append(desc.Indexes, d)
	}
	if _, err := db.ExecContext(ctx, "CREATE INDEX "+f.plain+" ON ix_runs (status)"); err != nil {
		t.Fatal(err)
	}
	desc.Indexes = append(desc.Indexes, schema.Index{Name: f.plain, Columns: []string{"status"}})
	return desc
}

// kinds is each finding as "kind warning|error: message", sorted.
func kinds(fs []vet.Finding) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		sev := "error"
		if f.Warning {
			sev = "warning"
		}
		out[i] = string(f.Kind) + " " + sev + ": " + f.Message
	}
	sort.Strings(out)
	return out
}

// requireFindings requires exactly one finding per want, each want a prefix
// "kind severity" followed by a substring of the message.
func requireFindings(t *testing.T, got []vet.Finding, want ...[3]string) {
	t.Helper()
	lines := kinds(got)
	used := make([]bool, len(lines))
	for _, w := range want {
		found := false
		for i, l := range lines {
			if !used[i] && strings.HasPrefix(l, w[0]+" "+w[1]+": ") && strings.Contains(l, w[2]) {
				used[i], found = true, true
				break
			}
		}
		if !found {
			t.Errorf("no %s %s finding containing %q in %q", w[0], w[1], w[2], lines)
		}
	}
	for i, l := range lines {
		if !used[i] {
			t.Errorf("unexpected finding: %s", l)
		}
	}
}

// indexDrift runs the shared cases on one engine: the indexes as declared
// pass; then each is recreated differently and exactly the typed findings
// follow, while the old-style declaration stays silent about its order.
func indexDrift(t *testing.T, db *sql.DB, f indexFixture, extra func(ctx context.Context, desc schema.Table)) {
	ctx := context.Background()
	desc := f.create(t, ctx, db)
	got, err := vet.CheckTables(ctx, db, f.dialect, []schema.Table{desc})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("the indexes the DDL just created differ from it: %q", kinds(got))
	}
	if extra != nil {
		extra(ctx, desc)
	}
	for _, q := range []string{
		// Declared partial, created without the predicate.
		`DROP INDEX ix_runs_open`, `CREATE INDEX ix_runs_open ON ix_runs (account_id)`,
		// Declared DESC, created ascending.
		`DROP INDEX ix_runs_recent`, `CREATE INDEX ix_runs_recent ON ix_runs (account_id, started_at)`,
		// Another predicate.
		`DROP INDEX ix_runs_active`, `CREATE INDEX ix_runs_active ON ix_runs (account_id) WHERE status = 'closed'`,
		// The old-style declaration states no order: DESC in the database is
		// not judged, exactly as before.
		`DROP INDEX ix_runs_plain`, `CREATE INDEX ix_runs_plain ON ix_runs (status DESC)`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	got, err = vet.CheckTables(ctx, db, f.dialect, []schema.Table{desc})
	if err != nil {
		t.Fatal(err)
	}
	requireFindings(t, got,
		[3]string{string(vet.FindingIndexPredicate), "error", "the DDL's index on (account_id) is partial (WHERE "},
		[3]string{string(vet.FindingIndexOrder), "error", "the DDL's index on (account_id, started_at) sorts started_at DESC"},
		[3]string{string(vet.FindingIndexPredicate), "error", "is WHERE \"status\" = 'open', the database's is WHERE "},
	)
}

func TestCheckTables_IndexOrderAndPredicate_SQLite(t *testing.T) {
	db := memorySQLite(t)
	indexDrift(t, db, sqliteIndexFixture(), func(ctx context.Context, desc schema.Table) {
		// SQLite keeps the text as written, so even CompareKeys, with no
		// engine to ask, finds the builder's predicates equal.
		keys, err := vet.ReadKeys(ctx, db, render.DialectSQLite)
		if err != nil {
			t.Fatal(err)
		}
		if got := vet.CompareKeys([]schema.Table{desc}, keys); len(got) != 0 {
			t.Fatalf("CompareKeys on SQLite: %q", kinds(got))
		}
	})
}

func TestCheckTables_IndexOrderAndPredicate_Postgres(t *testing.T) {
	dsn := os.Getenv("SQLB_PG_DSN")
	if dsn == "" {
		t.Skip("SKIPPED, NOT PASSED: set SQLB_PG_DSN to run the Postgres index check")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = db.Exec(`DROP TABLE IF EXISTS ix_runs`)
	t.Cleanup(func() { _, _ = db.Exec(`DROP TABLE IF EXISTS ix_runs`); _ = db.Close() })
	indexDrift(t, db, pgIndexFixture(), func(ctx context.Context, desc schema.Table) {
		keys, err := vet.ReadKeys(ctx, db, render.DialectPostgres)
		if err != nil {
			t.Fatal(err)
		}
		// Postgres re-prints "status" = 'open' as (status = 'open'::text).
		// Without the engine the two cannot be told apart, so CompareKeys
		// says so and claims nothing; CheckTables, above, found them equal.
		// "record_path" IS NULL re-printed as (record_path IS NULL) is the
		// same normal form and needs no engine.
		requireFindings(t, vet.CompareKeys([]schema.Table{desc}, keys),
			[3]string{string(vet.FindingIndexPredicateNotCompared), "warning", "the database's is WHERE (status = 'open'::text)"})
		// NULLS placement: DESC defaults to NULLS FIRST, so DESC NULLS LAST
		// in the database differs from the declared DESC.
		for _, q := range []string{`DROP INDEX ix_runs_recent`, `CREATE INDEX ix_runs_recent ON ix_runs (account_id, started_at DESC NULLS LAST)`} {
			if _, err := db.ExecContext(ctx, q); err != nil {
				t.Fatal(err)
			}
		}
		got, err := vet.CheckTables(ctx, db, render.DialectPostgres, []schema.Table{desc})
		if err != nil {
			t.Fatal(err)
		}
		requireFindings(t, got, [3]string{string(vet.FindingIndexOrder), "error", "sorts started_at DESC NULLS FIRST, the database's sorts started_at DESC NULLS LAST"})
		for _, q := range []string{`DROP INDEX ix_runs_recent`, `CREATE INDEX ix_runs_recent ON ix_runs (account_id, started_at DESC)`} {
			if _, err := db.ExecContext(ctx, q); err != nil {
				t.Fatal(err)
			}
		}
		// A predicate the engine cannot parse is not compared, never equal:
		// the description names a column the table does not have.
		broken := desc
		broken.Indexes = append([]schema.Index(nil), desc.Indexes...)
		for i := range broken.Indexes {
			if broken.Indexes[i].Name == "ix_runs_active" {
				broken.Indexes[i].Where = `"no_such_column" = 'open'`
			}
		}
		got, err = vet.CheckTables(ctx, db, render.DialectPostgres, []schema.Table{broken})
		if err != nil {
			t.Fatal(err)
		}
		requireFindings(t, got, [3]string{string(vet.FindingIndexPredicateNotCompared), "warning", "no_such_column"})
	})
}
