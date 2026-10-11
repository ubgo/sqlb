package pg_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/pg"
)

// CREATE FUNCTION for functions other than trigger functions, rendered.

var (
	fnBigint = ddl.RawType[int64]("bigint")
	fnText   = ddl.RawType[string]("text")
	fnAdd    = pg.Function{Schema: "auth", Name: "add", Params: []pg.FunctionArg{
		pg.Arg("a", fnBigint),
		pg.Arg("b", fnBigint),
	}}
)

// TestFunction_SignatureAndCharacteristics renders a whole function: a typed
// signature, what it returns, the language, and every characteristic, in the
// order Postgres's grammar takes them.
func TestFunction_SignatureAndCharacteristics(t *testing.T) {
	got := build(t)(pg.CreateFunction(fnAdd, pg.LangSQL, "SELECT a + b").
		OrReplace().
		Returns(pg.Returns(fnBigint)).
		Volatility(pg.Immutable).
		Leakproof(true).
		NullInput(pg.Strict).
		Security(pg.Definer).
		Parallel(pg.ParallelSafe).
		Cost(1.5).
		Set("search_path", "auth, pg_catalog").
		Build())
	want := `CREATE OR REPLACE FUNCTION "auth"."add"(IN "a" bigint, IN "b" bigint) RETURNS bigint ` +
		`LANGUAGE "sql" IMMUTABLE LEAKPROOF STRICT SECURITY DEFINER PARALLEL SAFE COST 1.5 ` +
		`SET "search_path" TO 'auth, pg_catalog' AS $sqlb$SELECT a + b$sqlb$`
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

// TestFunction_ResultShapes renders the three things a function can return and
// the case where it returns none, which an output parameter makes legal.
func TestFunction_ResultShapes(t *testing.T) {
	setOf := pg.Function{Name: "ids"}
	table := pg.Function{Name: "pairs"}
	out := pg.Function{Name: "split", Params: []pg.FunctionArg{
		pg.Arg("s", fnText),
		pg.OutArg("head", fnText),
		pg.InOutArg("n", fnBigint),
	}}
	variadic := pg.Function{Name: "total", Params: []pg.FunctionArg{pg.VariadicArg("xs", ddl.RawType[[]int64]("bigint[]"))}}
	got := buildAll(t,
		pg.CreateFunction(setOf, pg.LangSQL, "SELECT 1").Returns(pg.ReturnsSetOf(fnBigint)),
		pg.CreateFunction(table, pg.LangSQL, "SELECT 1, 'a'").Returns(pg.ReturnsTable(pg.Attr("n", fnBigint), pg.Attr("s", fnText))),
		pg.CreateFunction(out, pg.LangPLpgSQL, "BEGIN head := s; END"),
		pg.CreateFunction(variadic, pg.LangSQL, "SELECT 0").Returns(pg.Returns(fnBigint)),
	)
	want := `CREATE FUNCTION "ids"() RETURNS SETOF bigint LANGUAGE "sql" AS $sqlb$SELECT 1$sqlb$
CREATE FUNCTION "pairs"() RETURNS TABLE ("n" bigint, "s" text) LANGUAGE "sql" AS $sqlb$SELECT 1, 'a'$sqlb$
CREATE FUNCTION "split"(IN "s" text, OUT "head" text, INOUT "n" bigint) LANGUAGE "plpgsql" AS $sqlb$BEGIN head := s; END$sqlb$
CREATE FUNCTION "total"(VARIADIC "xs" bigint[]) RETURNS bigint LANGUAGE "sql" AS $sqlb$SELECT 0$sqlb$`
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

// TestFunction_PlannerAndCForms renders the parts a C function needs: the
// two-string body, the window declaration, a support function, a transform,
// and the row estimate a set-returning function carries.
func TestFunction_PlannerAndCForms(t *testing.T) {
	c := pg.Function{Name: "cume", Params: []pg.FunctionArg{pg.Arg("x", fnBigint)}}
	got := buildAll(t,
		pg.CreateCFunction(c, "$libdir/mylib", "cume_dist_c").Returns(pg.Returns(fnBigint)).Window(),
		pg.CreateFunction(pg.Function{Name: "rows"}, pg.LangSQL, "SELECT 1").
			Returns(pg.ReturnsSetOf(fnBigint)).Rows(42).Support(pg.Function{Name: "rows_support"}).
			Transform(pg.NamedType(fnText)).SetFromCurrent("search_path"),
	)
	want := `CREATE FUNCTION "cume"(IN "x" bigint) RETURNS bigint LANGUAGE "c" WINDOW AS '$libdir/mylib', 'cume_dist_c'
CREATE FUNCTION "rows"() RETURNS SETOF bigint LANGUAGE "sql" TRANSFORM FOR TYPE text ROWS 42 ` +
		`SUPPORT "rows_support" SET "search_path" FROM CURRENT AS $sqlb$SELECT 1$sqlb$`
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

// TestFunction_BodyIsDollarQuotedAwayFromItself pins that a body containing
// the tag is quoted with one it does not contain, which is what keeps a body
// from ending its own quote.
func TestFunction_BodyIsDollarQuotedAwayFromItself(t *testing.T) {
	got := build(t)(pg.CreateFunction(pg.Function{Name: "f"}, pg.LangSQL, "SELECT '$sqlb$'").Returns(pg.Returns(fnBigint)).Build())
	if !strings.Contains(got, "$sqlb1$SELECT '$sqlb$'$sqlb1$") {
		t.Fatalf("the body ended its own quote: %s", got)
	}
}

// TestFunction_RefuseIncompleteValues pins what fails Build. The result one is
// the case worth having: a function that neither returns anything nor has an
// output parameter is refused here rather than by the server, because the
// statement it would render is meaningless rather than wrong.
func TestFunction_RefuseIncompleteValues(t *testing.T) {
	cases := []struct {
		name string
		want error
		run  func() (ddl.Script, error)
	}{
		{"no result", pg.ErrNoResult, func() (ddl.Script, error) {
			return pg.CreateFunction(pg.Function{Name: "f"}, pg.LangSQL, "SELECT 1").Build()
		}},
		{"no body", pg.ErrEmptyBody, func() (ddl.Script, error) {
			return pg.CreateFunction(pg.Function{Name: "f"}, pg.LangSQL, "  ").Returns(pg.Returns(fnBigint)).Build()
		}},
		{"no language", ddl.ErrZeroValue, func() (ddl.Script, error) {
			return pg.CreateFunction(pg.Function{Name: "f"}, "", "SELECT 1").Returns(pg.Returns(fnBigint)).Build()
		}},
		{"no name", pg.ErrEmptyName, func() (ddl.Script, error) {
			return pg.CreateFunction(pg.Function{}, pg.LangSQL, "SELECT 1").Returns(pg.Returns(fnBigint)).Build()
		}},
		{"no link symbol", pg.ErrEmptyName, func() (ddl.Script, error) {
			return pg.CreateCFunction(pg.Function{Name: "f"}, "lib", "").Returns(pg.Returns(fnBigint)).Build()
		}},
		{"unknown volatility", pg.ErrUnknownKeyword, func() (ddl.Script, error) {
			return pg.CreateFunction(pg.Function{Name: "f"}, pg.LangSQL, "SELECT 1").Returns(pg.Returns(fnBigint)).Volatility(pg.Volatility(9)).Build()
		}},
		{"unknown parallel", pg.ErrUnknownKeyword, func() (ddl.Script, error) {
			return pg.CreateFunction(pg.Function{Name: "f"}, pg.LangSQL, "SELECT 1").Returns(pg.Returns(fnBigint)).Parallel(pg.Parallel(9)).Build()
		}},
		{"unnamed support", pg.ErrEmptyName, func() (ddl.Script, error) {
			return pg.CreateFunction(pg.Function{Name: "f"}, pg.LangSQL, "SELECT 1").Returns(pg.Returns(fnBigint)).Support(pg.Function{}).Build()
		}},
	}
	for _, c := range cases {
		if _, err := c.run(); !errors.Is(err, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, err, c.want)
		}
	}
}

// TestFunction_TriggerFunctionTakesNoParameters pins that a Function declaring
// parameters is refused by every trigger statement rather than having them
// dropped, which is the one way the shared Function value could lie.
func TestFunction_TriggerFunctionTakesNoParameters(t *testing.T) {
	f := pg.Function{Name: "touch", Params: []pg.FunctionArg{pg.Arg("a", fnBigint)}}
	if _, err := pg.CreateTriggerFunction(f, "BEGIN RETURN NEW; END").Build(); !errors.Is(err, pg.ErrTriggerSignature) {
		t.Errorf("CreateTriggerFunction: %v", err)
	}
	tr := pg.Trigger{Name: "t", Table: tUsers}
	if _, err := pg.CreateTrigger(tr).Before(pg.OnInsert()).ForEachRow().Execute(f).Build(); !errors.Is(err, pg.ErrTriggerSignature) {
		t.Errorf("CreateTrigger: %v", err)
	}
}
