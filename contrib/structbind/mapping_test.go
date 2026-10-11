package structbind_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ubgo/sqlb/contrib/structbind"
	"github.com/ubgo/sqlb/expr"
)

var tbl = expr.Table{Name: "users", Alias: "u"}

type namedUser struct {
	ID         string
	CreatedAt  time.Time
	HTTPStatus int
	Note       string `db:"user_note"`
	Skipped    string `db:"-"`
	unexported string //nolint:unused // present to prove it is skipped
}

// TestNew_DefaultsToSnake pins the default, which is the whole point of the
// change: an untagged CreatedAt was createdat, a column almost no schema has.
func TestNew_DefaultsToSnake(t *testing.T) {
	m := structbind.MustNew[namedUser](tbl)
	want := []string{"id", "created_at", "http_status", "user_note"}
	if got := m.Columns(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// TestNew_NamerIsChosenPerMapping pins that the convention is an argument.
//
// Handle-wide mappers put the equivalent on the handle, so the meaning of a
// struct depends on
// mutable state elsewhere in the process. Two mappings of the same struct
// disagreeing here is the property that makes that impossible.
func TestNew_NamerIsChosenPerMapping(t *testing.T) {
	for _, tc := range []struct {
		name  string
		namer structbind.Namer
		want  string
	}{
		{"default", nil, "id,created_at,http_status,user_note"},
		{"Camel", structbind.Camel, "id,createdAt,httpStatus,user_note"},
		{"Pascal", structbind.Pascal, "Id,CreatedAt,HttpStatus,user_note"},
		{"Lower", structbind.Lower, "id,createdat,httpstatus,user_note"},
		{"Exact", structbind.Exact, "ID,CreatedAt,HTTPStatus,user_note"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var m *structbind.Mapping[namedUser]
			if tc.namer == nil {
				m = structbind.MustNew[namedUser](tbl)
			} else {
				m = structbind.MustNew[namedUser](tbl, tc.namer)
			}
			if got := strings.Join(m.Columns(), ","); got != tc.want {
				t.Fatalf("got %s\nwant %s", got, tc.want)
			}
		})
	}
}

// TestNew_TagAlwaysBeatsTheNamer pins the precedence in every direction.
//
// A tag is what somebody wrote down deliberately, so no Namer may override it.
// Asserting it once per namer rather than once is the point: a namer that
// happened to be applied to the tag would pass a single-case test.
func TestNew_TagAlwaysBeatsTheNamer(t *testing.T) {
	for _, n := range []structbind.Namer{
		structbind.Snake, structbind.Camel, structbind.Pascal,
		structbind.Lower, structbind.Exact,
		// A namer that would rewrite the tagged column if it were ever
		// consulted for a tagged field.
		func(s string) string { return "namer_" + s },
	} {
		m := structbind.MustNew[namedUser](tbl, n)
		if got := m.Columns()[3]; got != "user_note" {
			t.Errorf("tagged field became %q, want user_note", got)
		}
	}
}

// TestNew_NilNamerFallsBackToTheDefault pins that an explicit nil is the same
// as passing nothing, because a caller threading an optional namer through
// their own constructor will pass nil rather than branch.
func TestNew_NilNamerFallsBackToTheDefault(t *testing.T) {
	m := structbind.MustNew[namedUser](tbl, nil)
	if got := m.Columns()[1]; got != "created_at" {
		t.Fatalf("got %q, want created_at", got)
	}
}

type auditBase struct {
	ID        string
	CreatedAt time.Time
}

type embedded struct {
	auditBase
	UserEmail string
}

// TestNew_NamerAppliesThroughEmbedding pins that a shared base struct is named
// by the same rule as the outer one, which is what makes an embedded audit
// struct usable at all.
func TestNew_NamerAppliesThroughEmbedding(t *testing.T) {
	m := structbind.MustNew[embedded](tbl)
	if got := strings.Join(m.Columns(), ","); got != "id,created_at,user_email" {
		t.Fatalf("got %s", got)
	}
}

// twoFields collides only under a Namer that collapses both names, which is
// what makes it the right fixture for the mechanism: the Go field names are
// unremarkable, so any collision is the derivation's doing.
type twoFields struct {
	Alpha string
	Beta  string
}

type clashWithTag struct {
	Email    string
	UserMail string `db:"email"`
}

type clashAcrossEmbedding struct {
	auditBase
	ID string
}

// TestNew_DuplicateColumnIsAnError pins the failure that used to be silent.
//
// Two fields mapping to one column renders SELECT "id", "id", which every
// engine accepts. Both scan targets then receive the same value and one field
// holds the wrong data with no error at any layer, which is the shape of bug
// this library exists to turn into a loud one.
func TestNew_DuplicateColumnIsAnError(t *testing.T) {
	t.Run("two derived names", func(t *testing.T) {
		_, err := structbind.New[twoFields](tbl, func(string) string { return "col" })
		assertDuplicate(t, err, "Alpha", "Beta", `"col"`)
	})
	t.Run("a tag colliding with a derived name", func(t *testing.T) {
		_, err := structbind.New[clashWithTag](tbl)
		assertDuplicate(t, err, "Email", "UserMail", `"email"`)
	})
	t.Run("across embedding, naming the path", func(t *testing.T) {
		_, err := structbind.New[clashAcrossEmbedding](tbl)
		// The embedded field must be spelled auditBase.ID, because "ID" alone does
		// not say which of the two it is.
		assertDuplicate(t, err, "auditBase.ID", "ID", `"id"`)
	})
}

// TestNew_DuplicateDependsOnTheNamer pins that the check runs against the names
// actually derived, not against the Go field names.
//
// This is what lets one struct be legal under one convention and rejected under
// another, which is correct: whether two columns are the same is a fact about
// the schema the Namer describes, not about the Go source.
func TestNew_DuplicateDependsOnTheNamer(t *testing.T) {
	// The same struct, two Namers: one derives distinct columns and one does
	// not. A check written against Go field names would see no difference.
	if _, err := structbind.New[twoFields](tbl, structbind.Snake); err != nil {
		t.Fatalf("Alpha and Beta derive distinct columns, so this must succeed: %v", err)
	}
	collapse := func(string) string { return "col" }
	if _, err := structbind.New[twoFields](tbl, collapse); !errors.Is(err, structbind.ErrDuplicateColumn) {
		t.Fatalf("got %v, want ErrDuplicateColumn", err)
	}
}

// TestNew_EmptyColumnFromACustomNamer pins the one way a column name can come
// out empty, which would otherwise render "u"."" and fail at the engine with an
// error pointing nowhere near the cause.
func TestNew_EmptyColumnFromACustomNamer(t *testing.T) {
	_, err := structbind.New[namedUser](tbl, func(string) string { return "" })
	if !errors.Is(err, structbind.ErrEmptyColumn) {
		t.Fatalf("got %v, want ErrEmptyColumn", err)
	}
	if !strings.Contains(err.Error(), "ID") {
		t.Errorf("error does not name the field: %v", err)
	}
}

func assertDuplicate(t *testing.T, err error, first, second, col string) {
	t.Helper()
	if !errors.Is(err, structbind.ErrDuplicateColumn) {
		t.Fatalf("got %v, want ErrDuplicateColumn", err)
	}
	for _, want := range []string{first, second, col} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %s: %v", want, err)
		}
	}
}
