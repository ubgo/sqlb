package expr_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// A family is a group of constructors that answer the same question about
// different shapes of operand. This test enumerates them.
//
// It exists because of how RowIn went missing. RowEq, RowLess and RowGreater
// had been here since keyset pagination, so the row family looked finished,
// and asking "is this pair one of these pairs" had no answer for a year. Every
// test in this package asked whether a constructor renders correctly, which
// can only confirm what somebody already wrote; nothing enumerated the shapes
// a family ought to cover, so the hole was invisible rather than known.
//
// The table below is that enumeration. A shape with no constructor is written
// down as absent, with where it lives instead or why it does not exist, so the
// next person reads a decision rather than a silence.
type shape struct {
	name string // the question this shape asks
	fn   string // the constructor, or "" when deliberately absent
	why  string // required when fn is empty
}

var families = map[string][]shape{
	// Everything comparing a row constructor, (a, b), against something.
	"row": {
		{name: "construct", fn: "Row"},
		{name: "equality", fn: "RowEq"},
		{name: "ordering", fn: "RowLess"},
		{name: "ordering (other direction)", fn: "RowGreater"},
		{name: "membership in a list", fn: "RowIn"},
		{name: "absence from a list", fn: "RowNotIn"},
		{name: "membership in a subquery", fn: "",
			why: "stmt.SubqueryCompare takes a SQLer, so a row goes on its left like any other expression; naming it again here would be a second spelling of one mechanism"},
		{name: "inequality", fn: "",
			why: "NOT (RowEq(...)) is the whole of it, and a dedicated RowNotEq would differ from it only in NULL handling, which is the database's rule rather than ours to restate"},
	},
	// Everything asking whether a single value is one of a set.
	"membership": {
		{name: "against bound values", fn: "In"},
		{name: "against bound values, negated", fn: "NotIn"},
		{name: "against other expressions", fn: "InExprs"},
		{name: "against a subquery", fn: "",
			why: "stmt.InSubquery and NotInSubquery, because a subquery is a statement and this package must not import one"},
	},
	// Everything matching text, split by where the pattern comes from.
	"text match": {
		{name: "author-written pattern", fn: "Like"},
		{name: "author-written pattern, negated", fn: "NotLike"},
		{name: "text a person typed, anywhere", fn: "Contains"},
		{name: "text a person typed, anywhere, negated", fn: "NotContains"},
		{name: "text a person typed, at the start", fn: "StartsWith"},
		{name: "text a person typed, at the end", fn: "EndsWith"},
		{name: "escaping for a pattern you assemble", fn: "EscapeLike"},
		{name: "case-insensitive match", fn: "",
			why: "its own family, \"case-insensitive\", below: the six questions this family asks of a value, asked regardless of case"},
	},
	// The questions Eq, In and the value-taking text matches ask, answered
	// regardless of case, with the spelling chosen by the dialect. Filled on
	// 2026-09-28; until then the text-match family recorded the absence and a
	// caller wrote Lower on both sides by hand, which is not the same query
	// on Postgres, where ILIKE is the idiom and a trigram index serves it.
	"case-insensitive": {
		{name: "equality", fn: "EqFold"},
		{name: "membership in a list", fn: "InFold"},
		{name: "absence from a list", fn: "NotInFold"},
		{name: "text a person typed, anywhere", fn: "ContainsFold"},
		{name: "text a person typed, at the start", fn: "StartsWithFold"},
		{name: "text a person typed, at the end", fn: "EndsWithFold"},
		{name: "inequality", fn: "",
			why: "Not(EqFold(...)) is NOT (lower(e) = lower(v)), which is lower(e) <> lower(v) with the same NULL answer, so a NeFold would be a second spelling"},
		{name: "text a person typed, anywhere, negated", fn: "",
			why: "Not(ContainsFold(...)) renders NOT (e ILIKE ...) or NOT (lower(e) LIKE ...), identical in meaning to a NOT ILIKE, NULL included, so a NotContainsFold would be a second spelling"},
		{name: "author-written pattern", fn: "",
			why: "a pattern the author wrote is a literal they can write in lower case, so Like(Lower(e), pattern) says it; pg.ILike is there for Postgres code that wants the operator itself"},
	},
}

// prefixes maps a family to the exported names that must be classified by it.
// Scanning by prefix is what makes the table a detector rather than a list: a
// new RowSomething has to be named above or excluded below, so it cannot be
// added without someone deciding which question it answers.
var prefixes = map[string][]string{
	"row":              {"Row"},
	"membership":       {"In", "NotIn"},
	"text match":       {"Like", "NotLike", "Contains", "NotContains", "StartsWith", "EndsWith", "EscapeLike"},
	"case-insensitive": {"EqFold", "InFold", "NotInFold", "ContainsFold", "StartsWithFold", "EndsWithFold"},
}

// excluded are exported names a prefix catches that belong to another family.
var excluded = map[string]string{
	"RowNumber": "a window function, not a row comparison; it happens to share the prefix",
}

func TestFamilies_EveryShapeIsAnsweredOrRecorded(t *testing.T) {
	declared := map[string]bool{}
	for family, shapes := range families {
		for _, s := range shapes {
			if s.fn == "" {
				if s.why == "" {
					t.Errorf("%s: shape %q is absent with no reason; an absence nobody wrote down is indistinguishable from an oversight", family, s.name)
				}
				continue
			}
			if declared[s.fn] {
				t.Errorf("%s: %s listed twice", family, s.fn)
			}
			declared[s.fn] = true
		}
	}

	exported := exportedFuncs(t)
	for _, name := range exported {
		if declared[name] || excluded[name] != "" {
			continue
		}
		for family, ps := range prefixes {
			for _, p := range ps {
				if !strings.HasPrefix(name, p) {
					continue
				}
				t.Errorf("%s is exported and matches the %q family's prefix %q, but no shape claims it. Add it to the table or to excluded.", name, family, p)
			}
		}
	}

	for name := range declared {
		found := false
		for _, e := range exported {
			if e == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("the table names %s, which this package does not export", name)
		}
	}
}

// exportedFuncs reads this package's own source for exported function names.
// Reading the source rather than a hand-kept list is the point: a list is
// something to forget to update.
func exportedFuncs(t *testing.T) []string {
	t.Helper()
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || !fn.Name.IsExported() {
				continue
			}
			out = append(out, fn.Name.Name)
		}
	}
	return out
}
