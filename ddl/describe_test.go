package ddl_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/dialect/postgres"
	"github.com/ubgo/sqlb/dialect/sqlite"
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/render"
	"github.com/ubgo/sqlb/schema"
)

// TestDescribe_ColumnsComeFromTheDefinitions pins the column half of the
// description: an inferred type is portable, As with Portable is portable, a
// RawType is Raw, nullability follows the definition, and the default is the
// literal or expression the dialect writes.
func TestDescribe_ColumnsComeFromTheDefinitions(t *testing.T) {
	got, err := ddl.NewCreateTable(postgres.Dialect{}, users).Column(
		ddl.Column(id), ddl.Column(email).As(ddl.RawType[string]("citext")), ddl.Column(nick),
		ddl.Column(stat).Default("active"), ddl.Column(created).DefaultExpr(expr.Literal[time.Time]("now()")),
		ddl.Column(phone).As(ddl.Portable[*string](schema.TypeUUID)).NotNull(),
	).Describe()
	if err != nil {
		t.Fatal(err)
	}
	want := schema.Table{Name: "users", Alias: "u", Columns: []schema.Column{
		{Name: "id", Type: schema.TypeBigInt},
		{Name: "email", Raw: "citext"},
		{Name: "nick", Type: schema.TypeText, Nullable: true},
		{Name: "status", Type: schema.TypeText, Default: "'active'"},
		{Name: "created_at", Type: schema.TypeTimestamp, Default: "(now())"},
		{Name: "phone", Type: schema.TypeUUID},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
	if err := func() error { s, err := schema.New("t", got); _ = s; return err }(); err != nil {
		t.Fatalf("the description is not a valid schema.Table: %v", err)
	}
}

// keyClause is an engine clause that describes a primary key, as pg's does.
type keyClause struct {
	ddl.TableClause[postgres.Dialect]
}

func (keyClause) DescribeInto(t *schema.Table) { t.PrimaryKey = []string{"id"} }

// TestDescribe_ClausesThatDescribeThemselves pins the clause half: a clause
// implementing Describer contributes, one that does not is left out.
func TestDescribe_ClausesThatDescribeThemselves(t *testing.T) {
	plain := ddl.NewTableClause[postgres.Dialect](ddl.PositionModifier, func(*render.Args) string { return "UNLOGGED" })
	pk := keyClause{ddl.NewTableClause[postgres.Dialect](ddl.PositionElement, func(*render.Args) string { return `PRIMARY KEY ("id")` })}
	got, err := ddl.NewCreateTable(postgres.Dialect{}, users).Column(ddl.Column(id)).Clause(plain, pk).Describe()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.PrimaryKey, []string{"id"}) || len(got.Indexes) != 0 {
		t.Fatalf("got %+v", got)
	}
}

// TestDescribe_FailsWhereBuildWould pins that a column Build could not spell
// fails Describe too, so a description never exists for DDL that does not.
func TestDescribe_FailsWhereBuildWould(t *testing.T) {
	if _, err := ddl.NewCreateTable(postgres.Dialect{}, users).Column(ddl.Column(profile)).Describe(); err == nil {
		t.Error("a json.RawMessage with no type was described")
	}
	if _, err := ddl.NewCreateTable(sqlite.Dialect{}, users).Column(ddl.Column(created).Default(time.Now())).Describe(); err == nil {
		t.Error("a SQLite time default was described")
	}
	// Every other reason Build refuses a statement refuses its description.
	undefined := ddl.NewTableClauseOn[postgres.Dialect](ddl.PositionElement, []ddl.ColumnRef{email}, func(*render.Args) string { return `UNIQUE ("email")` })
	for name, stmt := range map[string]*ddl.CreateTableReady[postgres.Dialect]{
		"a column of another table":    ddl.NewCreateTable(postgres.Dialect{}, users).Column(ddl.Column(sUserID)),
		"a column twice":               ddl.NewCreateTable(postgres.Dialect{}, users).Column(ddl.Column(id), ddl.Column(id)),
		"a key on an undefined column": ddl.NewCreateTable(postgres.Dialect{}, users).Column(ddl.Column(id)).Clause(undefined),
		"an unknown position": ddl.NewCreateTable(postgres.Dialect{}, users).Column(ddl.Column(id)).
			Clause(ddl.NewTableClause[postgres.Dialect](ddl.Position(99), func(*render.Args) string { return "X" })),
	} {
		_, buildErr := stmt.Build()
		if buildErr == nil {
			t.Fatalf("%s: Build accepted it, so this case tests nothing", name)
		}
		if _, err := stmt.Describe(); !errors.Is(err, buildErr) && (err == nil || err.Error() != buildErr.Error()) {
			t.Errorf("%s: Describe returned %v, Build %v", name, err, buildErr)
		}
	}
}
