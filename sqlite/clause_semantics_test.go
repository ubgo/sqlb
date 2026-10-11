package sqlite_test

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/sqlite"
)

// The value-semantics gate for the builders the statement gate cannot see.
//
// pg's TestEngineBuilders_AreAllCovered walks every type with a Build method,
// which is every statement. A clause builder has no Build — it ends in a
// Clause, a TableClause or the next typestate — so every one of them was
// unchecked, and sqlite.Conflict spent its whole life appending to its
// receiver and returning it: a *Conflict held and used twice accumulated both
// uses, silently. That is the same bug the statement builders were fixed for
// in 2026-09, in the half of the package nothing was looking at.
//
// So this walks the other half, and finds the methods by scanning the
// package's own source rather than from a list somebody types, because a list
// only protects the builders someone remembered. A clause builder added next
// month is covered the day it lands: its type needs a base here, and any
// parameter type not in the sample registry fails by name.
//
// It is deliberately not written as a table of forty closures. Each method is
// called through reflection with a sample argument for its parameter type, and
// the receiver is compared to a structural snapshot of itself taken before the
// call, so the check is the property — "the method did not write to its
// receiver" — rather than a rendering somebody chose.

// derivingMethod matches an exported method whose result is a type declared in
// this package: a builder method, as opposed to WriteSQL or Position.
//
// The type parameter groups are optional on both the receiver and the result,
// because a generic builder such as IndexKeyElem[T] is exactly the kind a
// matcher without them silently skips. The parameter list is greedy so the
// split between parameters and result is the LAST ") ", which keeps a
// func-typed parameter from ending it early.
var derivingMethod = regexp.MustCompile(
	`(?m)^func \((\w+) (\*?)(\w+)(?:\[[^\]]*\])?\) ([A-Z]\w*)\((.*)\) (\*?)(\w+)(?:\[[^\]]*\])?\s*\{`)

// localType matches a type this package declares, including one declared
// inside a grouped declaration, which is where the clause aliases live. A
// matcher that missed those read Conflict.DoNothing's InsertClause result as
// somebody else's type and skipped the method.
var localType = regexp.MustCompile(`(?m)^type ([A-Z]\w*)|^\t([A-Z]\w*)\s+=`)

// buildMethod finds the types the statement gate already walks.
var buildMethod = regexp.MustCompile(`(?m)^func \(\w+ \*?(\w+)(?:\[[^\]]*\])?\) Build\(`)

// clauseBuilderBases is one starting value per clause builder in the package.
// A type the scan finds with no base here fails the test by name.
var clauseBuilderBases = map[string]func() any{
	"Conflict":           func() any { return sqlite.OnConflict(csName) },
	"CreateFTS5Query":    func() any { return sqlite.CreateFTS5(csTable) },
	"CreateIndexQuery":   func() any { return sqlite.CreateIndex(sqlite.Index{Name: "ix"}, csTable) },
	"CreateRTreeQuery":   func() any { return sqlite.CreateRTree(csTable) },
	"CreateTriggerQuery": func() any { return sqlite.CreateTrigger(sqlite.Trigger{Name: "tr"}) },
	"CreateViewQuery":    func() any { return sqlite.CreateView(sqlite.View{Name: "v"}) },
	"TimedTriggerQuery": func() any {
		return sqlite.CreateTrigger(sqlite.Trigger{Name: "tr"}).After(sqlite.OnInsert(), csTable)
	},
	"RebuildQuery":     func() any { return sqlite.Rebuild(csTable) },
	"KeyClause":        func() any { return sqlite.Unique(csName) },
	"KeyColumn":        func() any { return sqlite.Key(csName) },
	"ColumnKeyClause":  func() any { return sqlite.ColumnKey(csID) },
	"ForeignKeyClause": func() any { return sqlite.References(csRef, csOtherID) },
	"IndexKeyElem":     func() any { return sqlite.IndexKey(csID) },
}

// clauseBuilderSamples is one plausible argument per parameter type. A method
// whose parameter type is absent fails by name rather than being skipped,
// which is what stops this gate quietly shrinking as the package grows.
func clauseBuilderSamples() map[reflect.Type]reflect.Value {
	step := sqlite.StepSelect(sqlite.Select(csID).From(csTable))
	newTable := sqlb.Table{Name: "cs_new"}
	newID := sqlb.Col[int64](newTable, "id")
	out := map[reflect.Type]reflect.Value{}
	// Every sample is stored under its CONCRETE type, because reflect.ValueOf
	// unwraps an interface; sampleArgs finds an interface parameter's value
	// through AssignableTo rather than by an exact match.
	for _, v := range []any{
		ddl.ColumnRef(csName),
		sqlb.Eq(csID, int64(1)),
		sqlb.Set(csName, "x"),
		sqlite.NoCase,
		sqlite.OrIgnore,
		sqlite.TableConstraint{Table: csTable, Name: "c"},
		sqlite.RefCascade,
		sqlite.DeferrableInitiallyDeferred,
		sqlite.OnInsert(),
		csTable,
		sqlite.View{Name: "v"},
		step,
		sqlite.Select(csID).From(csTable),
		sqlite.CreateTable(newTable).Column(ddl.Column(newID)).Clause(sqlite.PrimaryKey(newID)),
		csID,
		sqlite.IndexElement(sqlite.IndexKey(csID)),
		sqlite.Dimension(csLo, csHi),
		sqlite.CopyAs(newID, csID),
		sqlite.Trigram(),
		0, // Prefix takes lengths, and an int is an int
	} {
		rv := reflect.ValueOf(v)
		out[rv.Type()] = rv
	}
	return out
}

var (
	csTable   = sqlb.Table{Name: "cs", Alias: "c"}
	csID      = sqlb.Col[int64](csTable, "id")
	csName    = sqlb.Col[string](csTable, "name")
	csRef     = sqlb.Col[int64](csTable, "other_id")
	csLo      = sqlb.Col[float64](csTable, "lo")
	csHi      = sqlb.Col[float64](csTable, "hi")
	csOther   = sqlb.Table{Name: "cs_other"}
	csOtherID = sqlb.Col[int64](csOther, "id")
)

// TestClauseBuilders_MethodsDoNotWriteToTheirReceiver is the gate. It fails
// closed: a scan that matches nothing is a broken matcher, not a clean result,
// which this repository has shipped before.
func TestClauseBuilders_MethodsDoNotWriteToTheirReceiver(t *testing.T) {
	methods := scanClauseBuilders(t)
	if len(methods) == 0 {
		t.Fatal("found no clause builder method in this package; the matcher is broken, not the package empty")
	}
	samples := clauseBuilderSamples()

	var uncovered []string
	for _, m := range methods {
		base, ok := clauseBuilderBases[m.typ]
		if !ok {
			uncovered = append(uncovered, m.typ+"."+m.method+" (no base in clauseBuilderBases)")
			continue
		}
		t.Run(m.typ+"."+m.method, func(t *testing.T) {
			recv := reflect.ValueOf(base())
			method := recv.MethodByName(m.method)
			if !method.IsValid() {
				t.Fatalf("the scan found %s.%s but the value has no such method", m.typ, m.method)
			}
			args, err := sampleArgs(method.Type(), samples)
			if err != nil {
				t.Fatalf("%s.%s: %v", m.typ, m.method, err)
			}
			before := snapshot(recv)
			results := method.Call(args)
			if after := snapshot(recv); after != before {
				t.Errorf("the method wrote to its receiver:\nbefore %s\nafter  %s", before, after)
			}
			// Where the result is the same type, the derived value must also
			// have changed, or the method did nothing and the check above
			// passed for the wrong reason.
			if len(results) == 1 && results[0].Type() == recv.Type() {
				if got := snapshot(results[0]); got == before {
					t.Errorf("the derived value is identical to its base, so the method changed nothing: %s", got)
				}
			}
		})
	}
	sort.Strings(uncovered)
	if len(uncovered) > 0 {
		t.Errorf("clause builders with no value-semantics coverage: %v", uncovered)
	}
}

type clauseMethod struct{ typ, method string }

// scanClauseBuilders reads the package's source for every exported method on a
// type with no Build method whose result is a type declared here.
func scanClauseBuilders(t *testing.T) []clauseMethod {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var all strings.Builder
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		all.Write(src)
		all.WriteString("\n")
	}
	src := all.String()
	local := map[string]bool{}
	for _, m := range localType.FindAllStringSubmatch(src, -1) {
		local[m[1]+m[2]] = true
	}
	if len(local) == 0 {
		t.Fatal("found no type declaration in this package; the matcher is broken")
	}
	hasBuild := map[string]bool{}
	for _, m := range buildMethod.FindAllStringSubmatch(src, -1) {
		hasBuild[m[1]] = true
	}
	if len(hasBuild) == 0 {
		t.Fatal("found no Build method in this package; the matcher is broken")
	}
	var out []clauseMethod
	for _, m := range derivingMethod.FindAllStringSubmatch(src, -1) {
		typ, name, result := m[3], m[4], m[7]
		if hasBuild[typ] || !local[result] {
			continue
		}
		out = append(out, clauseMethod{typ: typ, method: name})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].typ != out[j].typ {
			return out[i].typ < out[j].typ
		}
		return out[i].method < out[j].method
	})
	return out
}

// sampleArgs builds one call's arguments from the registry, giving a variadic
// parameter exactly one element, which is enough to make an appending method
// change something.
func sampleArgs(ft reflect.Type, samples map[reflect.Type]reflect.Value) ([]reflect.Value, error) {
	var args []reflect.Value
	for i := range ft.NumIn() {
		pt := ft.In(i)
		if ft.IsVariadic() && i == ft.NumIn()-1 {
			pt = pt.Elem()
		}
		v, ok := samples[pt]
		if !ok {
			// An assignable sample is good enough: a concrete type standing in
			// for an interface parameter.
			for st, sv := range samples {
				if st.AssignableTo(pt) {
					v, ok = sv, true
					break
				}
			}
		}
		if !ok {
			return nil, fmt.Errorf("no sample value for parameter type %s; add one to clauseBuilderSamples", pt)
		}
		args = append(args, v)
	}
	return args, nil
}

// snapshot renders a value's structure as text, following pointers, slices and
// interfaces, so two snapshots of one value compare equal and a write through
// the receiver makes them differ.
//
// It reads unexported fields, which is the point: a builder keeps everything
// it accumulates in them. That means it never calls Interface, which panics on
// a value obtained from an unexported field, and works from Kind alone.
func snapshot(v reflect.Value) string {
	var b strings.Builder
	writeSnapshot(&b, v, 0)
	return b.String()
}

// snapshotDepth stops a cycle through a self-referential value; nothing here
// has one, and a guard costs less than the hang would.
const snapshotDepth = 12

func writeSnapshot(b *strings.Builder, v reflect.Value, depth int) {
	if depth > snapshotDepth {
		b.WriteString("...")
		return
	}
	switch v.Kind() {
	case reflect.Invalid:
		b.WriteString("nil")
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			b.WriteString("nil")
			return
		}
		writeSnapshot(b, v.Elem(), depth+1)
	case reflect.Slice, reflect.Array:
		if v.Kind() == reflect.Slice && v.IsNil() {
			b.WriteString("nil")
			return
		}
		b.WriteString("[")
		for i := range v.Len() {
			if i > 0 {
				b.WriteString(" ")
			}
			writeSnapshot(b, v.Index(i), depth+1)
		}
		b.WriteString("]")
	case reflect.Struct:
		b.WriteString("{")
		for i := range v.NumField() {
			if i > 0 {
				b.WriteString(" ")
			}
			b.WriteString(v.Type().Field(i).Name)
			b.WriteString(":")
			writeSnapshot(b, v.Field(i), depth+1)
		}
		b.WriteString("}")
	case reflect.Map:
		keys := make([]string, 0, v.Len())
		for _, k := range v.MapKeys() {
			var kb strings.Builder
			writeSnapshot(&kb, k, depth+1)
			var vb strings.Builder
			writeSnapshot(&vb, v.MapIndex(k), depth+1)
			keys = append(keys, kb.String()+"="+vb.String())
		}
		sort.Strings(keys)
		b.WriteString("{" + strings.Join(keys, " ") + "}")
	case reflect.Func:
		// A function cannot be compared, so only its presence is recorded;
		// every builder field this gate is about is data rather than a
		// closure.
		if v.IsNil() {
			b.WriteString("func:nil")
		} else {
			b.WriteString("func:set")
		}
	case reflect.String:
		b.WriteString(strconv(v.String()))
	case reflect.Bool:
		fmt.Fprintf(b, "%v", v.Bool())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		fmt.Fprintf(b, "%d", v.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		fmt.Fprintf(b, "%d", v.Uint())
	case reflect.Float32, reflect.Float64:
		fmt.Fprintf(b, "%g", v.Float())
	default:
		b.WriteString(v.Kind().String())
	}
}

// strconv quotes a string so an empty one is visible in a failure message.
func strconv(s string) string { return "\"" + s + "\"" }
