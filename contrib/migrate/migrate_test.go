package migrate_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/ubgo/sqlb/contrib/migrate"
	"github.com/ubgo/sqlb/dialect/clickhouse"
	"github.com/ubgo/sqlb/dialect/postgres"
	"github.com/ubgo/sqlb/dialect/sqlite"
	"github.com/ubgo/sqlb/render"
	"github.com/ubgo/sqlb/schema"
)

// authTables is what a library exports. It never hears of the application.
func authTables() []schema.Table {
	return []schema.Table{{
		Name:       "auth_user",
		PrimaryKey: []string{"id"},
		Columns: []schema.Column{
			{Name: "id", Type: schema.TypeUUID},
			{Name: "email", Type: schema.TypeText},
			{Name: "created_at", Type: schema.TypeTimestamp, Default: "CURRENT_TIMESTAMP"},
		},
		Indexes: []schema.Index{{Name: "auth_user_email_key", Columns: []string{"email"}, Unique: true}},
	}}
}

// appTables is the application's, referencing the library's table by name.
func appTables() []schema.Table {
	return []schema.Table{{
		Name:       "invoice",
		PrimaryKey: []string{"id"},
		Columns: []schema.Column{
			{Name: "id", Type: schema.TypeUUID},
			{Name: "user_id", Type: schema.TypeUUID},
			{Name: "total", Type: schema.TypeDecimal},
			{Name: "note", Type: schema.TypeText, Nullable: true},
		},
		ForeignKeys: []schema.ForeignKey{{
			Name: "invoice_user_fk", Columns: []string{"user_id"},
			RefTable: "auth_user", RefColumns: []string{"id"}, OnDelete: schema.Cascade,
		}},
	}}
}

func assembled(t *testing.T) schema.Set {
	t.Helper()
	set, err := schema.New("ubgo/auth", authTables()...)
	if err != nil {
		t.Fatal(err)
	}
	if err := set.Add("the application", appTables()...); err != nil {
		t.Fatal(err)
	}
	return *set
}

// TestPlan_OrdersAcrossContributors is the point of the whole schema layer,
// end to end. Two packages that never imported each other are migrated by a
// third that imported neither, and the dependent table comes second because
// the ordering was computed from both declarations at once.
func TestPlan_OrdersAcrossContributors(t *testing.T) {
	plan, err := migrate.Plan(postgres.Dialect{}, assembled(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 3 {
		t.Fatalf("got %d statements, want 3 (two tables and one index):\n%s", len(plan), strings.Join(plan, "\n"))
	}
	if !strings.Contains(plan[0], `"auth_user"`) {
		t.Fatalf("the referenced table must be created first, got:\n%s", plan[0])
	}
	if !strings.Contains(plan[1], `"invoice"`) || !strings.Contains(plan[1], "REFERENCES") {
		t.Fatalf("the referring table must come second with its constraint, got:\n%s", plan[1])
	}
	if !strings.Contains(plan[2], "CREATE UNIQUE INDEX") {
		t.Fatalf("indexes come after every table, got:\n%s", plan[2])
	}
}

// TestSQLType_IsPerEngine pins that one declaration serves several engines, and
// that the differences are real rather than cosmetic.
func TestSQLType_IsPerEngine(t *testing.T) {
	col := schema.Column{Name: "at", Type: schema.TypeTimestamp}
	for _, c := range []struct {
		d    render.Dialect
		want string
	}{
		{postgres.Dialect{}, "TIMESTAMPTZ"},
		{sqlite.Dialect{}, "TEXT"},
		{clickhouse.Dialect{}, "DateTime64(3)"},
	} {
		got, err := migrate.SQLType(c.d, col)
		if err != nil {
			t.Fatal(err)
		}
		if got != c.want {
			t.Errorf("%s: got %q, want %q", render.DialectName(c.d), got, c.want)
		}
	}
}

// TestSQLType_RawWins pins the escape hatch: the schema layer cannot express
// every engine's type, and a visible per-column override beats pretending.
func TestSQLType_RawWins(t *testing.T) {
	got, err := migrate.SQLType(postgres.Dialect{}, schema.Column{Name: "v", Raw: "vector(3)"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "vector(3)" {
		t.Fatalf("got %q, want the raw spelling", got)
	}
}

// TestSQLType_UnknownEngineIsAnError pins that an unmapped engine fails loudly
// rather than falling back to TEXT. A timestamp that silently became text is
// found months later in the data, not at migration time.
func TestSQLType_UnknownEngineIsAnError(t *testing.T) {
	_, err := migrate.SQLType(unknownDialect{}, schema.Column{Name: "at", Type: schema.TypeTimestamp})
	if !errors.Is(err, migrate.ErrNoTypeMapping) {
		t.Fatalf("want ErrNoTypeMapping, got %v", err)
	}
	for _, want := range migrate.Engines() {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error should list the engines it does support, missing %q: %v", want, err)
		}
	}
}

type unknownDialect struct{}

func (unknownDialect) Placeholder(int) string         { return "?" }
func (unknownDialect) QuoteIdent(ident string) string { return ident }
func (unknownDialect) Name() string                   { return "gremlinish" }

// TestEveryPortableTypeIsMapped is the coverage check that matters here. A type
// added to the schema layer and forgotten in one engine's table would surface
// as ErrNoTypeMapping at migration time on that engine only, which is the
// worst place to find it.
func TestEveryPortableTypeIsMapped(t *testing.T) {
	for _, d := range []render.Dialect{postgres.Dialect{}, sqlite.Dialect{}, clickhouse.Dialect{}} {
		for _, typ := range schema.TypeValues {
			if _, err := migrate.SQLType(d, schema.Column{Name: "c", Type: typ}); err != nil {
				t.Errorf("%s has no mapping for %s", render.DialectName(d), typ)
			}
		}
	}
}

// TestPlan_RefusesAnUnorderableSchema pins that a broken schema fails before
// any DDL is produced, rather than halfway through applying it.
func TestPlan_RefusesAnUnorderableSchema(t *testing.T) {
	set, err := schema.New("the application", appTables()...) // auth_user absent
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrate.Plan(postgres.Dialect{}, *set); !errors.Is(err, schema.ErrMissingTable) {
		t.Fatalf("want ErrMissingTable, got %v", err)
	}
}
