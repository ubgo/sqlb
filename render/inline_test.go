package render_test

import (
	"errors"
	"testing"

	"github.com/ubgo/sqlb/render"
)

// quoting is a dialect that writes literals, and says so by implementing
// LiteralQuoting. It writes only strings, so every other value exercises the
// refusal path.
type quoting struct{}

func (quoting) Placeholder(int) string         { return "?" }
func (quoting) QuoteIdent(ident string) string { return `"` + ident + `"` }
func (quoting) Name() string                   { return "quoting" }
func (quoting) QuoteLiteral(v any) (string, error) {
	s, ok := v.(string)
	if !ok {
		return "", render.ErrNotInlinable
	}
	return "'" + s + "'", nil
}

// TestInlineArgs_NextWritesALiteral pins the mode DDL depends on: Next writes
// the value into the text and binds nothing, so the statement has no
// parameters for a driver to be handed.
func TestInlineArgs_NextWritesALiteral(t *testing.T) {
	a := render.NewInlineArgs(quoting{})
	if got := a.Next("active"); got != "'active'" {
		t.Fatalf("Next = %q, want a literal", got)
	}
	if len(a.Values()) != 0 {
		t.Fatalf("an inline Args bound %v; it must bind nothing", a.Values())
	}
	if a.Err() != nil {
		t.Fatal(a.Err())
	}
}

// TestArgs_BindingIsUnchanged pins that the ordinary mode still binds: the
// inline mode is a second behaviour behind a second constructor, not a change
// to the first.
func TestArgs_BindingIsUnchanged(t *testing.T) {
	a := render.NewArgs(quoting{})
	if got := a.Next("active"); got != "?" {
		t.Fatalf("Next = %q, want a placeholder", got)
	}
	if len(a.Values()) != 1 {
		t.Fatalf("bound %v, want one value", a.Values())
	}
}

// TestArgs_LiteralIgnoresTheMode pins that Literal always writes a literal,
// which is what a position that never takes a parameter needs even inside an
// ordinary query.
func TestArgs_LiteralIgnoresTheMode(t *testing.T) {
	a := render.NewArgs(quoting{})
	if got := a.Literal("english"); got != "'english'" {
		t.Fatalf("Literal = %q", got)
	}
	if len(a.Values()) != 0 {
		t.Fatalf("Literal bound %v; it must not", a.Values())
	}
}

// TestInlineArgs_FailsClosed pins the two refusals. A dialect without the
// capability cannot be given a literal, and a value the dialect will not write
// fails with the dialect's own error; in both cases the Args records it, so
// Build fails rather than the statement carrying an empty hole.
func TestInlineArgs_FailsClosed(t *testing.T) {
	a := render.NewInlineArgs(gremlinish{})
	_ = a.Next("x")
	if !errors.Is(a.Err(), render.ErrNoLiteralQuoting) {
		t.Fatalf("got %v, want ErrNoLiteralQuoting", a.Err())
	}

	b := render.NewInlineArgs(quoting{})
	_ = b.Next(42)
	if !errors.Is(b.Err(), render.ErrNotInlinable) {
		t.Fatalf("got %v, want ErrNotInlinable", b.Err())
	}
}

// TestArgs_TableNamedRestoresItsMode pins that TableNamed sets ByTableName only
// while fn runs, as Bare does for Unqualified.
func TestArgs_TableNamedRestoresItsMode(t *testing.T) {
	a := render.NewArgs(quoting{})
	var inside bool
	_ = a.TableNamed(func() string { inside = a.ByTableName(); return "" })
	if !inside || a.ByTableName() {
		t.Fatalf("inside %v, after %v", inside, a.ByTableName())
	}
}
