package ddl_test

import (
	"database/sql/driver"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/dialect/clickhouse"
	"github.com/ubgo/sqlb/dialect/postgres"
	"github.com/ubgo/sqlb/dialect/sqlite"
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/render"
	"github.com/ubgo/sqlb/schema"
)

// outsideDialect is an engine from outside this module: it writes literals, so
// a DDL statement gets as far as the type table, and it reports no name, so
// schema has no type table for it. It is how the tests reach the arms the
// built-in three never take, and it is exactly what a dialect written in
// another repository looks like.
type outsideDialect struct{}

func (outsideDialect) Placeholder(int) string             { return "?" }
func (outsideDialect) QuoteIdent(s string) string         { return render.Quote(s, '"') }
func (outsideDialect) QuoteLiteral(v any) (string, error) { return "'" + v.(string) + "'", nil }

// noneOfThem is a portable type name schema does not know, so it constrains no
// Go type and no engine spells it.
const noneOfThem = schema.Type("citext")

var small = expr.Col[int32](users, "small")

// TestPortable_NeedsTheDialectsTypeTable pins the two ways Portable has no
// spelling to write: an engine with no type table at all, which is every
// dialect from outside this module until it adds one, and a portable type this
// module's own table does not hold. Both fail Build with ErrNoTypeTable rather
// than writing a guessed column type, which would be wrong silently and for
// the life of the table. The second case also pins that a type schema does not
// constrain reaches the table rather than being rejected as a mismatch.
func TestPortable_NeedsTheDialectsTypeTable(t *testing.T) {
	t.Run("a dialect with no type table", func(t *testing.T) {
		_, err := ddl.NewCreateTable(outsideDialect{}, users).Column(ddl.Column(email)).Build()
		if !errors.Is(err, ddl.ErrNoTypeTable) {
			t.Fatalf("got %v, want ErrNoTypeTable", err)
		}
	})
	t.Run("a portable type the table does not hold", func(t *testing.T) {
		_, err := ddl.NewCreateTable(postgres.Dialect{}, users).
			Column(ddl.Column(email).As(ddl.Portable[string](noneOfThem))).Build()
		if !errors.Is(err, ddl.ErrNoTypeTable) {
			t.Fatalf("got %v, want ErrNoTypeTable", err)
		}
		if errors.Is(err, schema.ErrTypeMismatch) {
			t.Fatal("a type schema does not constrain must not be reported as a mismatch")
		}
	})
}

// TestInferType_AnInt32IsAnInt pins the narrow integer arm of the inference
// table, which is the one a Col[int32] takes: int32 is stored as the portable
// int and spelled INTEGER, not as the bigint a Col[int64] gets.
func TestInferType_AnInt32IsAnInt(t *testing.T) {
	sql := one(t)(ddl.NewCreateTable(postgres.Dialect{}, users).Column(ddl.Column(small)).Build())
	if want := `CREATE TABLE "users" ("small" INTEGER NOT NULL)`; sql != want {
		t.Fatalf("got  %s\nwant %s", sql, want)
	}
}

// TestColumnDef_RefusesAColumnWithNoName pins that a definition built from an
// expression with no name is refused at Build with ErrNotAColumn rather than
// rendering a definition whose name is empty, which every engine reads as a
// syntax error far from its cause.
func TestColumnDef_RefusesAColumnWithNoName(t *testing.T) {
	nameless := expr.Col[int64](users, "")
	_, err := ddl.NewCreateTable(postgres.Dialect{}, users).Column(ddl.Column(nameless)).Build()
	if !errors.Is(err, ddl.ErrNotAColumn) {
		t.Fatalf("got %v, want ErrNotAColumn", err)
	}
}

// bytesValuer is a driver.Valuer whose Value returns JSON as bytes, the form a
// pg.JSON[T] over a []byte payload takes.
type bytesValuer struct{ v []byte }

func (b bytesValuer) Value() (driver.Value, error) { return b.v, nil }

// errValuer is a driver.Valuer that cannot produce a value, as a JSON payload
// holding an unmarshalable field does.
type errValuer struct{ err error }

func (e errValuer) Value() (driver.Value, error) { return nil, e.err }

// intValuer is a driver.Valuer that returns a legal driver.Value which is not
// JSON text, the one shape jsonLiteral cannot use.
type intValuer struct{}

func (intValuer) Value() (driver.Value, error) { return int64(7), nil }

// TestDefault_JSONColumnTakesItsValueInEveryForm pins the remaining forms a
// JSON column's default arrives in. The value is JSON text whatever its Go
// type, so bytes and a string go in as they are, and a driver.Valuer is asked
// for its value and then read in either of the two forms a driver.Value can
// carry text in. A Valuer that fails, and one that answers with something that
// is not text, fail Build with ErrInvalidJSON: an unreadable default must not
// reach the server as a column definition nobody wrote.
func TestDefault_JSONColumnTakesItsValueInEveryForm(t *testing.T) {
	render := func(c ddl.ColumnSpec) (string, error) {
		s, err := ddl.NewCreateTable(postgres.Dialect{}, users).Column(c).Build()
		if err != nil {
			return "", err
		}
		return s[0].SQL, nil
	}
	for name, tc := range map[string]struct {
		col  ddl.ColumnSpec
		want string
	}{
		"plain bytes": {
			ddl.Column(expr.Col[[]byte](users, "b")).As(ddl.RawJSONType[[]byte]("jsonb")).Default([]byte(`{"a":1}`)),
			`"b" jsonb NOT NULL DEFAULT ('{"a":1}')`,
		},
		"a string": {
			ddl.Column(expr.Col[string](users, "s")).As(ddl.RawJSONType[string]("jsonb")).Default(`{"a":1}`),
			`"s" jsonb NOT NULL DEFAULT ('{"a":1}')`,
		},
		"a valuer answering with bytes": {
			ddl.Column(expr.Col[bytesValuer](users, "v")).As(ddl.RawJSONType[bytesValuer]("jsonb")).Default(bytesValuer{[]byte(`[1,2]`)}),
			`"v" jsonb NOT NULL DEFAULT ('[1,2]')`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := render(tc.col)
			if err != nil {
				t.Fatal(err)
			}
			if want := `CREATE TABLE "users" (` + tc.want + `)`; got != want {
				t.Fatalf("got  %s\nwant %s", got, want)
			}
		})
	}
	boom := errors.New("no value")
	t.Run("a valuer that fails", func(t *testing.T) {
		_, err := render(ddl.Column(expr.Col[errValuer](users, "v")).As(ddl.RawJSONType[errValuer]("jsonb")).Default(errValuer{boom}))
		if !errors.Is(err, ddl.ErrInvalidJSON) || !errors.Is(err, boom) {
			t.Fatalf("got %v, want ErrInvalidJSON wrapping the Valuer's own error", err)
		}
	})
	t.Run("a valuer answering with a number", func(t *testing.T) {
		_, err := render(ddl.Column(expr.Col[intValuer](users, "v")).As(ddl.RawJSONType[intValuer]("jsonb")).Default(intValuer{}))
		if !errors.Is(err, ddl.ErrInvalidJSON) {
			t.Fatalf("got %v, want ErrInvalidJSON", err)
		}
		if !strings.Contains(err.Error(), "int64") {
			t.Errorf("the error must name the type Value returned, got %v", err)
		}
	})
}

// errNoSpelling is what an engine type returns when it cannot be written under
// the dialect it was given, as an enum declared in another schema would.
var errNoSpelling = errors.New("no spelling under this dialect")

// unspellable is an SQLType from an engine package whose Spelling fails. It is
// the only shape that reaches DescribeColumn's spelling arm: the two types
// this package ships either carry a portable type, which is described without
// being spelled, or are written exactly as given and cannot fail.
type unspellable[T any] struct{}

func (unspellable[T]) TypeOf(T)                                {}
func (unspellable[T]) Spelling(render.Dialect) (string, error) { return "", errNoSpelling }

// TestDescribeColumn_FailsWhereTheDefinitionCannotBeWritten pins that
// DescribeColumn refuses everything WriteColumn refuses, each with its own
// error. It is the entry point contrib/migrate and contrib/vet reach a column
// through, so a definition that cannot be rendered must not come back as a
// schema.Column either: an ordering and a drift check would then be made
// against a column no CREATE TABLE could produce.
func TestDescribeColumn_FailsWhereTheDefinitionCannotBeWritten(t *testing.T) {
	for name, tc := range map[string]struct {
		describe func() (schema.Column, error)
		want     error
	}{
		"a Go type with no default SQL type": {
			func() (schema.Column, error) { return ddl.Column(profile).DescribeColumn(postgres.Dialect{}) },
			ddl.ErrNoDefaultType,
		},
		"a type with no spelling under this dialect": {
			func() (schema.Column, error) {
				return ddl.Column(id).As(unspellable[int64]{}).DescribeColumn(postgres.Dialect{})
			},
			errNoSpelling,
		},
		"a dialect that writes no literals": {
			func() (schema.Column, error) {
				return ddl.Column(id).Default(1).DescribeColumn(clickhouse.Dialect{})
			},
			render.ErrNoLiteralQuoting,
		},
		"a default the dialect cannot write": {
			func() (schema.Column, error) {
				return ddl.Column(created).Default(time.Now()).DescribeColumn(sqlite.Dialect{})
			},
			render.ErrNotInlinable,
		},
	} {
		t.Run(name, func(t *testing.T) {
			col, err := tc.describe()
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
			if !reflect.DeepEqual(col, schema.Column{}) {
				t.Fatalf("a refused column must be described as nothing, got %+v", col)
			}
		})
	}
}

// wholeTableClause is a clause whose validity depends on the rest of the
// table, as MySQL's PARTITION BY is: CREATE TABLE describes itself and hands
// the description over. called records whether it was asked.
type wholeTableClause struct {
	ddl.TableClause[postgres.Dialect]
	called *bool
	err    error
}

func (c wholeTableClause) CheckTable(schema.Table) error {
	*c.called = true
	return c.err
}

// TestCheckTable_IsNotAskedAboutATableThatCannotBeDescribed pins the order
// between the two. A TableChecker is handed the statement's own description,
// so when a column cannot be described there is nothing to hand it: Build
// fails with the column's error and the clause is never asked, rather than
// being given a half-built table and answering about it.
func TestCheckTable_IsNotAskedAboutATableThatCannotBeDescribed(t *testing.T) {
	called := false
	clause := wholeTableClause{
		TableClause: ddl.NewTableClause[postgres.Dialect](ddl.PositionOption, func(*render.Args) string { return "PARTITION BY HASH (id)" }),
		called:      &called,
	}
	// profile is a json.RawMessage with no type given, which cannot be
	// described or written.
	_, err := ddl.NewCreateTable(postgres.Dialect{}, users).Column(ddl.Column(profile)).Clause(clause).Build()
	if !errors.Is(err, ddl.ErrNoDefaultType) {
		t.Fatalf("got %v, want ErrNoDefaultType", err)
	}
	if called {
		t.Fatal("a TableChecker was asked about a table that could not be described")
	}
	// The control: with a column that describes, the same clause is asked and
	// its refusal is Build's.
	refused := errors.New("the key must include the partition column")
	clause.err = refused
	_, err = ddl.NewCreateTable(postgres.Dialect{}, users).Column(ddl.Column(id)).Clause(clause).Build()
	if !called {
		t.Fatal("a TableChecker was never asked about a table that describes")
	}
	if !errors.Is(err, refused) {
		t.Fatalf("got %v, want the clause's own refusal", err)
	}
}

// bareSpec is a ColumnSpec written outside this package that knows only how to
// render itself: it describes nothing and reports no facts. Both absences are
// legal, and each takes a branch of its own.
type bareSpec struct{ name string }

func (s bareSpec) ColumnName() string    { return s.name }
func (bareSpec) ColumnTable() expr.Table { return users }
func (s bareSpec) WriteColumn(b *strings.Builder, a *render.Args, _ ddl.ColumnExtras) {
	b.WriteString(a.Dialect().QuoteIdent(s.name))
	b.WriteString(" TEXT")
}

// TestDescribe_AColumnThatCannotDescribeItselfIsNamedOnly pins what a
// ColumnSpec from outside this package contributes to the description when it
// does not implement ColumnDescriber: its name and nothing more. Guessing a
// type from the rendered text would put a type into a migration plan that the
// spec never claimed.
func TestDescribe_AColumnThatCannotDescribeItselfIsNamedOnly(t *testing.T) {
	got, err := ddl.NewCreateTable(postgres.Dialect{}, users).
		Column(ddl.Column(id), bareSpec{"note"}).Describe()
	if err != nil {
		t.Fatal(err)
	}
	want := []schema.Column{{Name: "id", Type: schema.TypeBigInt}, {Name: "note"}}
	if !reflect.DeepEqual(got.Columns, want) {
		t.Fatalf("got  %+v\nwant %+v", got.Columns, want)
	}
}

// TestAddColumn_ASpecWithNoFactsIsAddedAsItRenders pins the restricted
// dialect's answer for a ColumnSpec that cannot report its facts: SQLite's
// ADD COLUMN checks what it can see, and a spec that says nothing about NULL
// or its default is added as it renders rather than being refused for facts
// nobody stated.
func TestAddColumn_ASpecWithNoFactsIsAddedAsItRenders(t *testing.T) {
	sql := one(t)(ddl.NewAlterTable(sqlite.Dialect{}, users).Action(ddl.AddColumn(bareSpec{"note"})).Build())
	if want := `ALTER TABLE "users" ADD COLUMN "note" TEXT`; sql != want {
		t.Fatalf("got  %s\nwant %s", sql, want)
	}
}

// namingAlterClause is an engine ALTER TABLE action that names the columns it
// acts on, as a Postgres ALTER COLUMN does, so the statement checks each one
// belongs to the table being altered.
type namingAlterClause struct {
	ddl.AlterClause[postgres.Dialect]
	cols []ddl.ColumnRef
}

func (c namingAlterClause) NamedColumns() []ddl.ColumnRef { return c.cols }

// constraintAlterClause is an engine ALTER TABLE action over a named
// constraint, the shape pg.ValidateConstraint and pg.DropConstraint take: the
// constraint is a value that carries its own table, and the statement checks
// it against the table being altered.
type constraintAlterClause struct {
	ddl.AlterClause[postgres.Dialect]
	ddl.TableConstraint
}

// TestAlterTable_MoreRefusals pins the refusals of ALTER TABLE that the
// grouping and foreign-column tests do not reach: an action naming an
// expression that is not a column, a dialect that cannot write the literals a
// DDL statement is made of, and a clause naming a column that does not exist.
func TestAlterTable_MoreRefusals(t *testing.T) {
	nameless := expr.Col[int64](users, "")
	namelessText := expr.Col[*string](users, "")
	for name, tc := range map[string]struct {
		build func() (ddl.Script, error)
		want  error
	}{
		"dropping an expression that is not a column": {
			func() (ddl.Script, error) {
				return ddl.NewAlterTable(postgres.Dialect{}, users).Action(ddl.DropColumn(nameless)).Build()
			},
			ddl.ErrNotAColumn,
		},
		"renaming from an expression that is not a column": {
			func() (ddl.Script, error) {
				return ddl.NewAlterTable(postgres.Dialect{}, users).Action(ddl.RenameColumn(namelessText, nickname)).Build()
			},
			ddl.ErrNotAColumn,
		},
		"renaming to an expression that is not a column": {
			func() (ddl.Script, error) {
				return ddl.NewAlterTable(postgres.Dialect{}, users).Action(ddl.RenameColumn(nick, namelessText)).Build()
			},
			ddl.ErrNotAColumn,
		},
		"a dialect that writes no literals": {
			func() (ddl.Script, error) {
				return ddl.NewAlterTable(clickhouse.Dialect{}, users).Action(ddl.DropColumn(email)).Build()
			},
			render.ErrNoLiteralQuoting,
		},
		"a clause naming an expression that is not a column": {
			func() (ddl.Script, error) {
				cl := namingAlterClause{
					AlterClause: ddl.NewAlterClause[postgres.Dialect](func(*render.Args) string { return `ALTER COLUMN "x" SET NOT NULL` }),
					cols:        []ddl.ColumnRef{nameless},
				}
				return ddl.NewAlterTable(postgres.Dialect{}, users).Clause(cl).Build()
			},
			ddl.ErrNotAColumn,
		},
		"a clause naming no column at all": {
			func() (ddl.Script, error) {
				cl := namingAlterClause{
					AlterClause: ddl.NewAlterClause[postgres.Dialect](func(*render.Args) string { return `ALTER COLUMN "x" SET NOT NULL` }),
					cols:        []ddl.ColumnRef{nil},
				}
				return ddl.NewAlterTable(postgres.Dialect{}, users).Clause(cl).Build()
			},
			ddl.ErrNotAColumn,
		},
		"a constraint of another table": {
			func() (ddl.Script, error) {
				cl := constraintAlterClause{
					AlterClause:     ddl.NewAlterClause[postgres.Dialect](func(*render.Args) string { return `VALIDATE CONSTRAINT "c"` }),
					TableConstraint: ddl.TableConstraint{Table: sessions, Name: "c"},
				}
				return ddl.NewAlterTable(postgres.Dialect{}, users).Clause(cl).Build()
			},
			ddl.ErrForeignColumn,
		},
	} {
		t.Run(name, func(t *testing.T) {
			s, err := tc.build()
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
			if s != nil {
				t.Fatalf("a refused statement must carry no SQL, got %v", s.SQL())
			}
		})
	}
}

// TestAlterTable_AConstraintOfThisTableIsAccepted is the control for the
// refusal above: the same clause naming this statement's own table builds, so
// the refusal is evidence about the table check rather than about the clause.
func TestAlterTable_AConstraintOfThisTableIsAccepted(t *testing.T) {
	cl := constraintAlterClause{
		AlterClause:     ddl.NewAlterClause[postgres.Dialect](func(*render.Args) string { return `VALIDATE CONSTRAINT "c"` }),
		TableConstraint: ddl.TableConstraint{Table: users, Name: "c"},
	}
	sql := one(t)(ddl.NewAlterTable(postgres.Dialect{}, users).Clause(cl).Build())
	if want := `ALTER TABLE "users" VALIDATE CONSTRAINT "c"`; sql != want {
		t.Fatalf("got  %s\nwant %s", sql, want)
	}
}

// TestDropTable_Refusals pins DROP TABLE's three refusals: a table with no
// name, which is a zero value that skipped the constructor; a dialect with no
// literal quoting; and a clause whose own render fails, which must fail Build
// rather than come back as a statement with a hole in it.
func TestDropTable_Refusals(t *testing.T) {
	for name, tc := range map[string]struct {
		build func() (ddl.Script, error)
		want  error
	}{
		"a table with no name": {
			func() (ddl.Script, error) {
				return ddl.NewDropTable(postgres.Dialect{}, users, expr.Table{}).Build()
			},
			ddl.ErrZeroValue,
		},
		"a dialect that writes no literals": {
			func() (ddl.Script, error) { return ddl.NewDropTable(clickhouse.Dialect{}, users).Build() },
			render.ErrNoLiteralQuoting,
		},
		"a clause whose render fails": {
			func() (ddl.Script, error) {
				cl := ddl.NewDropClause[sqlite.Dialect](func(a *render.Args) string { return a.Literal(time.Now()) })
				return ddl.NewDropTable(sqlite.Dialect{}, users).Clause(cl).Build()
			},
			render.ErrNotInlinable,
		},
	} {
		t.Run(name, func(t *testing.T) {
			s, err := tc.build()
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
			if s != nil {
				t.Fatalf("a refused statement must carry no SQL, got %v", s.SQL())
			}
		})
	}
}

// TestWriteColumnHelpers_RefuseAMistargetedClause pins the two helpers an
// engine package renders an added column with. Both take the clauses the
// caller collected, and both must refuse a clause that does not name the
// column being written: one that names nothing would vanish, and one that
// names another column would be written onto the wrong definition. The two
// helpers exist so an engine's own ADD COLUMN and this package's refuse the
// same things, so they are checked together.
func TestWriteColumnHelpers_RefuseAMistargetedClause(t *testing.T) {
	write := map[string]func(*strings.Builder, *render.Args, ddl.ColumnSpec, []ddl.ColumnClause[postgres.Dialect]){
		"WriteAddColumn":  ddl.WriteAddColumn[postgres.Dialect],
		"WriteColumnWith": ddl.WriteColumnWith[postgres.Dialect],
	}
	clause := func(target ddl.ColumnRef, part ddl.ColumnPart) ddl.ColumnClause[postgres.Dialect] {
		return ddl.NewColumnClause[postgres.Dialect](target, part, func(*render.Args) string { return "COLLATE \"C\"" })
	}
	for name, tc := range map[string]struct {
		clauses []ddl.ColumnClause[postgres.Dialect]
		want    error
	}{
		"a clause naming no column":              {[]ddl.ColumnClause[postgres.Dialect]{clause(nil, ddl.PartAfterType)}, ddl.ErrNoColumnTarget},
		"a clause whose column has no name":      {[]ddl.ColumnClause[postgres.Dialect]{clause(expr.Col[string](users, ""), ddl.PartAfterType)}, ddl.ErrNoColumnTarget},
		"a clause naming another column":         {[]ddl.ColumnClause[postgres.Dialect]{clause(id, ddl.PartAfterType)}, ddl.ErrUnknownColumn},
		"a clause naming another table's column": {[]ddl.ColumnClause[postgres.Dialect]{clause(sUserID, ddl.PartAfterType)}, ddl.ErrUnknownColumn},
		"a clause at a part no column renders":   {[]ddl.ColumnClause[postgres.Dialect]{clause(email, ddl.ColumnPart(99))}, ddl.ErrUnknownPosition},
	} {
		for helper, fn := range write {
			t.Run(helper+": "+name, func(t *testing.T) {
				a := render.NewInlineArgs(postgres.Dialect{})
				var b strings.Builder
				fn(&b, a, ddl.Column(email), tc.clauses)
				if err := a.Err(); !errors.Is(err, tc.want) {
					t.Fatalf("got %v, want %v", err, tc.want)
				}
				if b.String() != "" {
					t.Fatalf("a refused column must write nothing, got %q", b.String())
				}
			})
		}
	}
}

// TestWriteColumnHelpers_WriteTheClausesTheyAccept is the control: the same
// two helpers with a clause that does name the column write it, so the
// refusals above are evidence about the target check rather than about a
// helper that refuses everything.
func TestWriteColumnHelpers_WriteTheClausesTheyAccept(t *testing.T) {
	cl := ddl.NewColumnClause[postgres.Dialect](email, ddl.PartAfterType, func(*render.Args) string { return `COLLATE "C"` })
	for helper, fn := range map[string]func(*strings.Builder, *render.Args, ddl.ColumnSpec, []ddl.ColumnClause[postgres.Dialect]){
		"WriteAddColumn":  ddl.WriteAddColumn[postgres.Dialect],
		"WriteColumnWith": ddl.WriteColumnWith[postgres.Dialect],
	} {
		t.Run(helper, func(t *testing.T) {
			a := render.NewInlineArgs(postgres.Dialect{})
			var b strings.Builder
			fn(&b, a, ddl.Column(email), []ddl.ColumnClause[postgres.Dialect]{cl})
			if err := a.Err(); err != nil {
				t.Fatal(err)
			}
			if want := `"email" TEXT COLLATE "C" NOT NULL`; b.String() != want {
				t.Fatalf("got  %s\nwant %s", b.String(), want)
			}
		})
	}
}

// TestDescribeColumn_LeavesTheGoTypeCheckToBuild pins an asymmetry worth
// knowing about. A portable type is described without being spelled, so
// DescribeColumn alone does not notice that the Go type cannot hold it; the
// check lives in Spelling, which only the render calls. It is not a hole,
// because the two callers that matter go through Build: Describe builds
// first, and CREATE TABLE renders every column before it hands the
// description to a TableChecker. The test pins both halves, so a change that
// moved the check would have to say which half it changed.
func TestDescribeColumn_LeavesTheGoTypeCheckToBuild(t *testing.T) {
	def := ddl.Column(id).As(ddl.Portable[int64](schema.TypeText))
	col, err := def.DescribeColumn(postgres.Dialect{})
	if err != nil {
		t.Fatalf("DescribeColumn refused a portable type: %v", err)
	}
	if want := (schema.Column{Name: "id", Type: schema.TypeText}); col != want {
		t.Fatalf("got %+v, want %+v", col, want)
	}
	stmt := ddl.NewCreateTable(postgres.Dialect{}, users).Column(def)
	if _, err := stmt.Build(); !errors.Is(err, schema.ErrTypeMismatch) {
		t.Fatalf("Build got %v, want ErrTypeMismatch", err)
	}
	if _, err := stmt.Describe(); !errors.Is(err, schema.ErrTypeMismatch) {
		t.Fatalf("Describe got %v, want ErrTypeMismatch", err)
	}
}
