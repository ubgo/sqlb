package schema_test

import (
	"strings"
	"testing"

	"github.com/ubgo/sqlb/schema"
)

// TestTable_ValidateReportsTheStructuralProblemsToo extends the one-sitting
// rule to the problems a declaration has before its columns are looked at: a
// table with no name, a table with no columns, a column with no name, an index
// over no columns, a foreign key naming no table, and a foreign key naming a
// column of its own table that does not exist. Each is reported in the same
// pass, so a declaration written by hand is corrected once rather than six
// times, and each has its own sentence, so the report says which one it found.
func TestTable_ValidateReportsTheStructuralProblemsToo(t *testing.T) {
	err := schema.Table{
		Columns: []schema.Column{{Type: schema.TypeText}},
		Indexes: []schema.Index{{Name: "empty"}},
		ForeignKeys: []schema.ForeignKey{
			{Name: "nowhere", Columns: []string{"missing"}, RefColumns: []string{"id"}},
		},
	}.Validate()
	if err == nil {
		t.Fatal("expected errors")
	}
	for _, want := range []string{
		"table has no name",
		"has a column with no name",
		`index "empty"`,
		"names no columns",
		`foreign key "nowhere"`,
		"names no referenced table",
		`names column "missing" that the table does not declare`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the report does not mention %q:\n%v", want, err)
		}
	}
	// A table with no columns at all is the other half: the case above has one
	// column, so it cannot say this.
	empty := schema.Table{Name: "empty"}.Validate()
	if empty == nil || !strings.Contains(empty.Error(), "declares no columns") {
		t.Errorf("a table with no columns: got %v", empty)
	}
}

// TestCol_DoesNotCheckWhatItCannotKnow pins the two Go types Col accepts
// without checking. An interface target, `any` among them, has no concrete
// type to compare, so the caller has opted out and the type table is not
// consulted; and a column declared with a portable type this package does not
// constrain has no list of Go types to check against. Both must be accepted:
// refusing them would make Raw and an untyped scan target unusable, and both
// are documented escape hatches.
func TestCol_DoesNotCheckWhatItCannotKnow(t *testing.T) {
	// An interface target against a column whose type does constrain.
	text := schema.Table{Name: "t", Columns: []schema.Column{{Name: "c", Type: schema.TypeText}}}
	if _, err := schema.Col[any](text, "c"); err != nil {
		t.Errorf("an interface target must be accepted: %v", err)
	}
	// A concrete target against a portable type with no Go types listed. A
	// dialect package may declare one this module's table does not cover.
	exotic := schema.Table{Name: "t", Columns: []schema.Column{{Name: "c", Type: schema.Type("citext")}}}
	if _, err := schema.Col[int64](exotic, "c"); err != nil {
		t.Errorf("an unconstrained column type must be accepted: %v", err)
	}
	// The control: a type that is constrained still refuses the wrong target,
	// so the two acceptances above are about the absence of a rule rather than
	// about a check that never runs.
	if _, err := schema.Col[int64](text, "c"); err == nil {
		t.Error("a text column accepted an int64 target")
	}
}

// TestSet_ZeroValueTakesItsFirstTables pins that a Set declared rather than
// made by New is usable: Add creates its maps on the first call. A migration
// runner holds a Set as a field, and a zero value that panicked on the first
// contribution would make `var s schema.Set` a trap with no compiler warning.
func TestSet_ZeroValueTakesItsFirstTables(t *testing.T) {
	var s schema.Set
	if err := s.Add("first", schema.Table{Name: "a", Columns: []schema.Column{{Name: "id", Type: schema.TypeBigInt}}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Add("second", schema.Table{Name: "b", Columns: []schema.Column{{Name: "id", Type: schema.TypeBigInt}}}); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tbl := range s.Tables() {
		names = append(names, tbl.Name)
	}
	if strings.Join(names, " ") != "a b" {
		t.Fatalf("got %v, want a b", names)
	}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
}

// TestSet_ValidateNamesTheOwnerOfABrokenTable pins that a Set's report says
// who contributed the table that is wrong. A Set exists because no contributor
// can see the others, so "column has neither Type nor Raw" without an owner
// sends every team to read every declaration.
func TestSet_ValidateNamesTheOwnerOfABrokenTable(t *testing.T) {
	var s schema.Set
	if err := s.Add("ubgo/auth", schema.Table{Name: "broken", Columns: []schema.Column{{Name: "id"}}}); err != nil {
		t.Fatal(err)
	}
	err := s.Validate()
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "ubgo/auth") {
		t.Errorf("the report must name the owner, got %v", err)
	}
	if !strings.Contains(err.Error(), "neither Type nor Raw") {
		t.Errorf("the report must carry the table's own error, got %v", err)
	}
}

// TestSet_AReferenceToAColumnTheOtherTableDoesNotDeclare pins the other half
// of the cross-table check. A foreign key naming a table nobody contributed is
// ErrMissingTable; one naming a table that exists and a column it does not
// declare is the mistake that survives a rename, and it is caught here rather
// than by the server halfway through a migration.
func TestSet_AReferenceToAColumnTheOtherTableDoesNotDeclare(t *testing.T) {
	var s schema.Set
	err := s.Add("app",
		schema.Table{Name: "invoice", Columns: []schema.Column{{Name: "id", Type: schema.TypeBigInt}}},
		schema.Table{
			Name:    "line",
			Columns: []schema.Column{{Name: "invoice_id", Type: schema.TypeBigInt}},
			ForeignKeys: []schema.ForeignKey{{
				Name: "line_invoice_fk", Columns: []string{"invoice_id"},
				RefTable: "invoice", RefColumns: []string{"invoice_number"},
			}},
		})
	if err != nil {
		t.Fatal(err)
	}
	got := s.Validate()
	if got == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"line_invoice_fk", "invoice.invoice_number", "not declared"} {
		if !strings.Contains(got.Error(), want) {
			t.Errorf("the report does not mention %q:\n%v", want, got)
		}
	}
}
