package pg_test

import (
	"errors"
	"testing"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/pg"
)

// CREATE TYPE for the kinds that are neither an enum nor a composite.

var (
	tdRange  = pg.RangeType[int64]{Schema: "auth", Name: "int8range_local"}
	tdBase   = pg.BaseType[int64]{Schema: "auth", Name: "myint"}
	tdIn     = pg.Function{Schema: "auth", Name: "myint_in"}
	tdOut    = pg.Function{Schema: "auth", Name: "myint_out"}
	tdBigint = ddl.RawType[int64]("bigint")
)

// TestTypedef_RangeType renders a range type, bare and with every option, and
// pins that the type is usable as a column's SQL type, which is the point of
// typing it to Range[T]: declaring the type and declaring the column that
// holds one are the same act.
func TestTypedef_RangeType(t *testing.T) {
	got := buildAll(t,
		pg.CreateRangeType(tdRange, tdBigint),
		pg.CreateRangeType(tdRange, tdBigint).
			SubtypeOpClass(pg.NewOpClass[int64]("int8_ops")).
			Collation(pg.Collation{Schema: "auth", Name: "c"}).
			Canonical(pg.Function{Name: "canon"}).
			SubtypeDiff(pg.Function{Name: "diff"}).
			MultirangeTypeName("int8multirange_local"),
	)
	want := `CREATE TYPE "auth"."int8range_local" AS RANGE (SUBTYPE = bigint)
CREATE TYPE "auth"."int8range_local" AS RANGE (SUBTYPE = bigint, SUBTYPE_OPCLASS = "int8_ops", ` +
		`COLLATION = "auth"."c", CANONICAL = "canon", SUBTYPE_DIFF = "diff", MULTIRANGE_TYPE_NAME = "int8multirange_local")`
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}

	span := sqlb.Col[pg.Range[int64]](tUsers, "span")
	col := build(t)(pg.CreateTable(tUsers).Column(ddl.Column(span).As(tdRange)).Build())
	if want := `CREATE TABLE "auth"."users" ("span" "auth"."int8range_local" NOT NULL)`; col != want {
		t.Fatalf("as a column type:\ngot  %s\nwant %s", col, want)
	}
}

// TestTypedef_ShellAndBaseType renders the pair that has to be run in order: a
// shell so the input and output functions can name the type, then the type
// itself.
func TestTypedef_ShellAndBaseType(t *testing.T) {
	got := buildAll(t,
		pg.CreateShellType(tdBase),
		pg.CreateBaseType(tdBase, tdIn, tdOut).
			Receive(pg.Function{Name: "r"}).
			Send(pg.Function{Name: "s"}).
			TypmodIn(pg.Function{Name: "ti"}).
			TypmodOut(pg.Function{Name: "to"}).
			Analyze(pg.Function{Name: "an"}).
			Subscript(pg.Function{Name: "sub"}).
			InternalLength(8).
			PassedByValue().
			Alignment(pg.AlignDouble).
			Storage(pg.StoragePlain).
			Category(pg.CategoryNumeric).
			Preferred(false).
			Default(0).
			Delimiter(",").
			Collatable(false),
		pg.CreateBaseType(tdBase, tdIn, tdOut).VariableLength().
			Like(pg.NamedType(ddl.RawType[string]("text"))).
			Element(pg.NamedType(tdBigint)),
	)
	want := `CREATE TYPE "auth"."myint"
CREATE TYPE "auth"."myint" (INPUT = "auth"."myint_in", OUTPUT = "auth"."myint_out", RECEIVE = "r", SEND = "s", ` +
		`TYPMOD_IN = "ti", TYPMOD_OUT = "to", ANALYZE = "an", SUBSCRIPT = "sub", INTERNALLENGTH = 8, PASSEDBYVALUE, ` +
		`ALIGNMENT = double, STORAGE = plain, CATEGORY = 'N', PREFERRED = false, DEFAULT = 0, DELIMITER = ',', COLLATABLE = false)
CREATE TYPE "auth"."myint" (INPUT = "auth"."myint_in", OUTPUT = "auth"."myint_out", INTERNALLENGTH = VARIABLE, ` +
		`LIKE = text, ELEMENT = bigint)`
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

// TestTypedef_GuardedAndMovable pins that the new types take the two things
// every other declared object here takes: the guard that makes a create with
// no IF NOT EXISTS safe to repeat, and the move into another schema.
func TestTypedef_GuardedAndMovable(t *testing.T) {
	got := buildAll(t,
		pg.Guarded(pg.CreateRangeType(tdRange, tdBigint)),
		pg.SetObjectSchema(tdRange, pg.Schema{Name: "other"}),
		pg.SetOwner(tdBase, pg.Role{Name: "app"}),
	)
	want := `DO $sqlb$ BEGIN CREATE TYPE "auth"."int8range_local" AS RANGE (SUBTYPE = bigint); EXCEPTION WHEN duplicate_object THEN NULL; END $sqlb$
ALTER TYPE "auth"."int8range_local" SET SCHEMA "other"
ALTER TYPE "auth"."myint" OWNER TO "app"`
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

// TestTypedef_RefuseIncompleteValues pins that a half-declared type fails
// Build rather than rendering a statement the server would take differently.
func TestTypedef_RefuseIncompleteValues(t *testing.T) {
	cases := []struct {
		name string
		want error
		run  func() (ddl.Script, error)
	}{
		{"no subtype", ddl.ErrZeroValue, func() (ddl.Script, error) {
			return pg.CreateRangeType(tdRange, nil).Build()
		}},
		{"no range name", pg.ErrEmptyName, func() (ddl.Script, error) {
			return pg.CreateRangeType(pg.RangeType[int64]{}, tdBigint).Build()
		}},
		{"no opclass name", pg.ErrEmptyName, func() (ddl.Script, error) {
			return pg.CreateRangeType(tdRange, tdBigint).SubtypeOpClass(pg.NewOpClass[int64]("")).Build()
		}},
		{"no canonical name", pg.ErrEmptyName, func() (ddl.Script, error) {
			return pg.CreateRangeType(tdRange, tdBigint).Canonical(pg.Function{}).Build()
		}},
		{"no input function", pg.ErrEmptyName, func() (ddl.Script, error) {
			return pg.CreateBaseType(tdBase, pg.Function{}, tdOut).Build()
		}},
		{"unknown alignment", pg.ErrUnknownKeyword, func() (ddl.Script, error) {
			return pg.CreateBaseType(tdBase, tdIn, tdOut).Alignment(pg.TypeAlignment(9)).Build()
		}},
		{"unknown storage", pg.ErrUnknownKeyword, func() (ddl.Script, error) {
			return pg.CreateBaseType(tdBase, tdIn, tdOut).Storage(pg.TypeStorage(9)).Build()
		}},
		{"empty category", pg.ErrEmptyName, func() (ddl.Script, error) {
			return pg.CreateBaseType(tdBase, tdIn, tdOut).Category("").Build()
		}},
		{"empty delimiter", pg.ErrEmptyName, func() (ddl.Script, error) {
			return pg.CreateBaseType(tdBase, tdIn, tdOut).Delimiter("").Build()
		}},
	}
	for _, c := range cases {
		if _, err := c.run(); !errors.Is(err, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, err, c.want)
		}
	}
}
