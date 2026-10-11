package pg_test

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/pg"
)

// The named string types, audited.
//
// A named string type with constants beside it is not a closed set: Go lets
// any caller write T("anything"), and where the value is concatenated into the
// statement rather than quoted, that string IS the statement. The SQLite
// package found exactly this in its OrAction on 2026-09-24, interpolated at
// three sites of which two had shipped long before the third was written.
//
// This file is the pg audit, and it does both halves at every site, because a
// validation that refuses everything passes the first half and is useless:
//
//   - hostile: every site is handed T(csInjection) and must fail the build.
//   - honest:  every site is handed every real constant and must build.
//
// The list of sites is the thing that matters and it came from grepping for
// each type, not from memory: TrimSide, NormalForm and JSONKind each had one
// site, DateField had four of which two quoted and two did not, and Bounds had
// three in two files.

// csInjection is what a caller can write today and what every site must
// refuse. It is the OrAction payload, adapted: a value that closes the
// construct it sits in and appends a statement.
const csInjection = `x'); DROP TABLE users; --`

// csCase is one render site of one closed type: a name, a build with a
// hostile value, and the same build with a real constant.
type csCase struct {
	name     string
	hostile  func() error
	constant func() error
}

// csScript turns a DDL build into a test result.
func csScript(_ ddl.Script, err error) error { return err }

// csQuery turns a query build into a test result.
func csQuery(_ sqlb.Query, err error) error { return err }

// csSelect builds a SELECT over one expression, which is how an expression
// construct reaches a statement.
func csSelect(e sqlb.SQLer) error {
	return csQuery(pg.Select().Exprs(e).From(tUsers).Build())
}

// csWhere builds a SELECT whose WHERE holds the predicate.
func csWhere(p sqlb.Predicate) error {
	return csQuery(pg.Select(tUserID).From(tUsers).Where(p).Build())
}

// csCases is every render site of every closed named string type in pg. A new
// site of one of these types belongs here; TestClosedSets_EverySiteIsCovered
// checks the count against the source.
func csCases() []csCase {
	hostileField := pg.DateField(csInjection)
	tbl := sqlb.Table{Name: "t"}
	return []csCase{
		{
			"TableLockMode/LockTable",
			func() error {
				return csQuery(pg.NewLockTable(pg.Dialect{}, tUsers).In(pg.TableLockMode(csInjection)).Build())
			},
			func() error { return csQuery(pg.NewLockTable(pg.Dialect{}, tUsers).In(pg.LockShare).Build()) },
		},
		{
			// Open but shaped: tsm_system_rows brings SYSTEM_ROWS, so the set
			// is the server's and a list here refused real code.
			"SampleMethod/TableSample",
			func() error {
				return csQuery(pg.Select(tUserID).From(tUsers).Clause(pg.TableSample(pg.SampleMethod(csInjection), 10, -1)).Build())
			},
			func() error {
				return csQuery(pg.Select(tUserID).From(tUsers).Clause(pg.TableSample(pg.SampleSystem, 10, -1)).Build())
			},
		},
		{
			"TrimSide/TrimChars",
			func() error { return csSelect(pg.TrimChars(pg.TrimSide(csInjection), " ", tEmail)) },
			func() error { return csSelect(pg.TrimChars(pg.TrimBoth, " ", tEmail)) },
		},
		{
			"NormalForm/Normalize",
			func() error { return csSelect(pg.Normalize(tEmail, pg.NormalForm(csInjection))) },
			func() error { return csSelect(pg.Normalize(tEmail, pg.NFC)) },
		},
		{
			"JSONKind/IsJSON",
			func() error { return csWhere(pg.IsJSON(tEmail, pg.JSONKind(csInjection), false)) },
			func() error { return csWhere(pg.IsJSON(tEmail, pg.JSONObj, false)) },
		},
		{
			"DateField/Extract",
			func() error { return csSelect(pg.Extract(hostileField, tCreated)) },
			func() error { return csSelect(pg.Extract(pg.FieldYear, tCreated)) },
		},
		{
			"DateField/ExtractFrom",
			func() error { return csSelect(pg.ExtractFrom(hostileField, tCreated)) },
			func() error { return csSelect(pg.ExtractFrom(pg.FieldYear, tCreated)) },
		},
		{
			"DateField/DateTrunc",
			func() error { return csSelect(pg.DateTrunc(hostileField, tCreated)) },
			func() error { return csSelect(pg.DateTrunc(pg.FieldMonth, tCreated)) },
		},
		{
			"DateField/DateTruncZone",
			func() error { return csSelect(pg.DateTruncZone(hostileField, tCreated, "UTC")) },
			func() error { return csSelect(pg.DateTruncZone(pg.FieldMonth, tCreated, "UTC")) },
		},
		{
			"Weight/SetWeight",
			func() error { return csSelect(pg.SetWeight(pg.ToTSVector("english", tEmail), pg.Weight(csInjection))) },
			func() error { return csSelect(pg.SetWeight(pg.ToTSVector("english", tEmail), pg.WeightA)) },
		},
		{
			"Bounds/TstzRange",
			func() error { return csSelect(pg.TstzRange(csTime, csTime, pg.Bounds(csInjection))) },
			func() error { return csSelect(pg.TstzRange(csTime, csTime, pg.BoundsIncExc)) },
		},
		{
			"Bounds/Int8Range",
			func() error { return csSelect(pg.Int8Range(1, 2, pg.Bounds(csInjection))) },
			func() error { return csSelect(pg.Int8Range(1, 2, pg.BoundsIncExc)) },
		},
		{
			"Bounds/RangeExpr",
			func() error {
				return csSelect(pg.RangeExpr[int64]("int8range", sqlb.Param(1), sqlb.Param(2), pg.Bounds(csInjection)))
			},
			func() error {
				return csSelect(pg.RangeExpr[int64]("int8range", sqlb.Param(1), sqlb.Param(2), pg.BoundsIncExc))
			},
		},
		{
			"ExcludeOp/Exclude",
			func() error {
				return csScript(pg.CreateTable(tbl).Column(ddl.Column(sqlb.Col[int64](tbl, "a"))).
					Clause(pg.Exclude(pg.TableConstraint{Table: tbl, Name: "c"}, pg.GiST,
						pg.ExcludeOn(sqlb.Col[int64](tbl, "a"), pg.ExcludeOp(csInjection)))).Build())
			},
			func() error {
				return csScript(pg.CreateTable(tbl).Column(ddl.Column(sqlb.Col[int64](tbl, "a"))).
					Clause(pg.Exclude(pg.TableConstraint{Table: tbl, Name: "c"}, pg.GiST,
						pg.ExcludeOn(sqlb.Col[int64](tbl, "a"), pg.OpEquals))).Build())
			},
		},
		{
			"CompareOp/AnyExpr",
			func() error {
				return csWhere(pg.AnyExpr(tUserID, pg.CompareOp(csInjection), sqlb.Param([]int64{1})))
			},
			func() error { return csWhere(pg.AnyExpr(tUserID, sqlb.OpEq, sqlb.Param([]int64{1}))) },
		},
		{
			"CompareOp/AllExpr",
			func() error {
				return csWhere(pg.AllExpr(tUserID, pg.CompareOp(csInjection), sqlb.Param([]int64{1})))
			},
			func() error { return csWhere(pg.AllExpr(tUserID, sqlb.OpNotEq, sqlb.Param([]int64{1}))) },
		},
		{
			"TypeCategory/CreateBaseType",
			func() error {
				return csScript(pg.CreateBaseType(pg.BaseType[int64]{Name: "b"}, pg.Function{Name: "i"}, pg.Function{Name: "o"}).
					Category(pg.TypeCategory(csInjection)).Build())
			},
			func() error {
				return csScript(pg.CreateBaseType(pg.BaseType[int64]{Name: "b"}, pg.Function{Name: "i"}, pg.Function{Name: "o"}).
					Category(pg.CategoryUser).Build())
			},
		},
		{
			"BitString/BitLit",
			func() error { return csSelect(pg.BitLit(csInjection)) },
			func() error { return csSelect(pg.BitLit("1011")) },
		},
	}
}

// TestClosedSets_EverySiteRefusesAHostileValueAndTakesARealOne is the audit.
// Both halves run at every site, because either one alone proves nothing: a
// validation that refuses everything passes the first, and no validation at
// all passes the second.
func TestClosedSets_EverySiteRefusesAHostileValueAndTakesARealOne(t *testing.T) {
	for _, c := range csCases() {
		t.Run(c.name, func(t *testing.T) {
			err := c.hostile()
			if err == nil {
				t.Fatalf("a hostile value built; %q would have reached the server", csInjection)
			}
			// Three typed refusals, because three mechanisms answer here: a
			// closed set reports pg.ErrUnknownValue, BitLit's own shape guard
			// reports pg.ErrNotBits, and a type that is open but shaped —
			// SampleMethod — goes through the core's keyword check and
			// reports sqlb.ErrUnknownValue.
			if !errors.Is(err, pg.ErrUnknownValue) && !errors.Is(err, pg.ErrNotBits) && !errors.Is(err, sqlb.ErrUnknownValue) {
				t.Errorf("refused with %v, which is not the typed refusal", err)
			}
			if err := c.constant(); err != nil {
				t.Errorf("a real constant was refused: %v", err)
			}
		})
	}
}

// TestClosedSets_EveryConstantBuilds is the honest half at full width: every
// constant of every closed type, not just the one each site's case uses. A
// validator that accepted only the first constant of each type would pass the
// table above.
func TestClosedSets_EveryConstantBuilds(t *testing.T) {
	for _, m := range pg.TableLockModeValues {
		if _, err := pg.NewLockTable(pg.Dialect{}, tUsers).In(m).Build(); err != nil {
			t.Errorf("lock mode %q: %v", m, err)
		}
	}
	for _, m := range pg.SampleMethodValues {
		if err := csQuery(pg.Select(tUserID).From(tUsers).Clause(pg.TableSample(m, 10, -1)).Build()); err != nil {
			t.Errorf("sampling method %q: %v", m, err)
		}
	}
	for _, s := range pg.TrimSideValues {
		if err := csSelect(pg.TrimChars(s, " ", tEmail)); err != nil {
			t.Errorf("trim side %q: %v", s, err)
		}
	}
	for _, f := range pg.NormalFormValues {
		if err := csSelect(pg.Normalize(tEmail, f)); err != nil {
			t.Errorf("normalization form %q: %v", f, err)
		}
	}
	for _, k := range pg.JSONKindValues {
		if err := csWhere(pg.IsJSON(tEmail, k, false)); err != nil {
			t.Errorf("IS JSON qualifier %q: %v", k, err)
		}
	}
	for _, f := range pg.DateFieldValues {
		if err := csSelect(pg.Extract(f, tCreated)); err != nil {
			t.Errorf("date field %q in extract: %v", f, err)
		}
		if err := csSelect(pg.DateTrunc(f, tCreated)); err != nil {
			t.Errorf("date field %q in date_trunc: %v", f, err)
		}
	}
	for _, w := range pg.WeightValues {
		if err := csSelect(pg.SetWeight(pg.ToTSVector("english", tEmail), w)); err != nil {
			t.Errorf("weight %q: %v", w, err)
		}
	}
	for _, b := range pg.BoundsValues {
		if err := csSelect(pg.Int8Range(1, 2, b)); err != nil {
			t.Errorf("bounds %q: %v", b, err)
		}
	}
	// The two open-but-shaped types: every operator this package names, and a
	// category of each documented class.
	for _, o := range []pg.ExcludeOp{pg.OpOverlaps, pg.OpEquals, pg.OpAdjacent} {
		tbl := sqlb.Table{Name: "t"}
		err := csScript(pg.CreateTable(tbl).Column(ddl.Column(sqlb.Col[int64](tbl, "a"))).
			Clause(pg.Exclude(pg.TableConstraint{Table: tbl, Name: "c"}, pg.GiST,
				pg.ExcludeOn(sqlb.Col[int64](tbl, "a"), o))).Build())
		if err != nil {
			t.Errorf("exclusion operator %q: %v", o, err)
		}
	}
	for _, c := range []pg.TypeCategory{pg.CategoryUser, pg.CategoryNumeric, pg.CategoryString, pg.CategoryDateTime,
		pg.CategoryBoolean, pg.CategoryGeometric, pg.CategoryNetwork, pg.CategoryRange, pg.CategoryTimespan} {
		err := csScript(pg.CreateBaseType(pg.BaseType[int64]{Name: "b"}, pg.Function{Name: "i"}, pg.Function{Name: "o"}).
			Category(c).Build())
		if err != nil {
			t.Errorf("type category %q: %v", c, err)
		}
	}
}

// TestClosedSets_OpenTypesAreQuotedRatherThanValidated pins the other half of
// the audit's answer: three of the named string types are genuinely open, so
// validating them would break the extension that brings a real value, and what
// makes them safe is the dialect's identifier quoting. A hostile value builds
// and comes out as one quoted identifier that does not exist, which is the
// server's problem rather than an injection.
func TestClosedSets_OpenTypesAreQuotedRatherThanValidated(t *testing.T) {
	hostile := `heap"; DROP TABLE users; --`
	quoted := `"heap""; DROP TABLE users; --"`

	got := build(t)(pg.AlterMaterializedView(pg.MaterializedView{Name: "m"}).SetAccessMethod(pg.AccessMethod(hostile)).Build())
	if !strings.Contains(got, quoted) {
		t.Errorf("AccessMethod was not quoted as one identifier: %s", got)
	}
	got = build(t)(pg.CreateIndex(pg.Index{Name: "i"}, tUsers).On(pg.IndexKey(tEmail)).Using(pg.IndexMethod(hostile)).Build())
	if !strings.Contains(got, quoted) {
		t.Errorf("IndexMethod was not quoted as one identifier: %s", got)
	}
	got = build(t)(pg.CreateFunction(pg.Function{Name: "f"}, pg.Language(hostile), "SELECT 1").
		Returns(pg.Returns(ddl.RawType[int64]("bigint"))).Build())
	if !strings.Contains(got, quoted) {
		t.Errorf("Language was not quoted as one identifier: %s", got)
	}
	// The same for the literal path: a hostile interval is one string.
	got = build(t)(pg.CreateTable(sqlb.Table{Name: "t"}).
		Column(ddl.Column(sqlb.Col[int64](sqlb.Table{Name: "t"}, "a")).
			DefaultExpr(sqlb.Cast[int64](pg.IntervalLit(`1 day'); DROP TABLE users; --`), "bigint"))).Build())
	if strings.Contains(got, "DROP TABLE users; --'") && !strings.Contains(got, `''`) {
		t.Errorf("IntervalLit did not quote its text: %s", got)
	}
}

// TestClosedSets_EveryConstantIsInItsValues reads the source and fails when a
// constant of a closed type is missing from that type's Values slice, which is
// what valid() consults. Without it, a constant added in another file — the
// four date fields in more.go were exactly that — would be refused at every
// site by the validation meant to protect it.
func TestClosedSets_EveryConstantIsInItsValues(t *testing.T) {
	declared := map[string][]string{}
	fset := token.NewFileSet()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			gen, ok := d.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok || vs.Type == nil {
					continue
				}
				id, ok := vs.Type.(*ast.Ident)
				if !ok {
					continue
				}
				for _, n := range vs.Names {
					declared[id.Name] = append(declared[id.Name], n.Name)
				}
			}
		}
	}
	if len(declared) == 0 {
		t.Fatal("found no typed constants at all; the scan is broken")
	}
	values := map[string][]string{
		"TableLockMode": names(pg.TableLockModeValues),
		"SampleMethod":  names(pg.SampleMethodValues),
		"TrimSide":      names(pg.TrimSideValues),
		"NormalForm":    names(pg.NormalFormValues),
		"JSONKind":      names(pg.JSONKindValues),
		"DateField":     names(pg.DateFieldValues),
		"Weight":        names(pg.WeightValues),
		"Bounds":        names(pg.BoundsValues),
	}
	for typ, want := range values {
		got, ok := declared[typ]
		if !ok {
			t.Errorf("%s has no constants in the source; the scan or the type is wrong", typ)
			continue
		}
		if len(got) != len(want) {
			t.Errorf("%s has %d constants in the source and %d in its Values slice: %v",
				typ, len(got), len(want), got)
		}
	}
}

// names renders a slice of a named string type as its values, for counting.
func names[T ~string](vs []T) []string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = string(v)
	}
	slices.Sort(out)
	return out
}

// TestClosedSets_EverySiteIsCovered fails when a render site of a closed type
// is added without a case above. It counts the calls to closedWord, closedLit
// and closedLiteral in the source and compares them with the table, because
// the gap OrAction fell through was a site nobody remembered.
func TestClosedSets_EverySiteIsCovered(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	sites := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") || name == "closedsets.go" {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, fn := range []string{"closedWord(", "closedLit(", "closedLiteral("} {
			sites += strings.Count(string(src), fn)
		}
	}
	if sites == 0 {
		t.Fatal("found no call to the closed-set writers; the scan is broken")
	}
	// Two cases in the table do not call one of the three writers: BitLit
	// guards its own shape, and SampleMethod is open-but-shaped and goes
	// through the core's keyword check. Both must still refuse a payload,
	// which is why they are in the table at all.
	if want := len(csCases()) - 2; sites != want {
		t.Errorf("the source has %d closed-set render sites and the table covers %d;"+
			" a site was added without a case, or a case without a site", sites, want)
	}
}

// tCreated is a timestamp column on the audit's table, for the date-field
// sites; csTime is a fixed instant for the range constructors, which bind
// their bounds, so the value is immaterial and the third argument is what is
// tested.
var tCreated = sqlb.Col[time.Time](tUsers, "created_at")

var csTime = time.Unix(0, 0).UTC()

// TestClosedSets_InjectionStringIsHostile pins that the payload this file
// tests with would in fact be an injection if it reached the statement, so the
// audit is not passing on a harmless string. It is the one assertion here that
// is about the test rather than about the code.
func TestClosedSets_InjectionStringIsHostile(t *testing.T) {
	if !strings.Contains(csInjection, "DROP TABLE") || !strings.Contains(csInjection, "--") {
		t.Fatalf("the payload %q does not close a construct and append a statement", csInjection)
	}
	if _, err := strconv.Unquote(`"` + csInjection + `"`); err == nil && csInjection == "" {
		t.Fatal("the payload is empty")
	}
}
