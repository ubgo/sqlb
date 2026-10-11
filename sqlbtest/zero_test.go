package sqlbtest_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/sqlbtest"
)

// recorder is a testing.TB that keeps what ZeroValues reports instead of
// failing, so the runner itself can be tested.
type recorder struct {
	testing.TB
	msgs []string
}

func (r *recorder) Errorf(f string, args ...any) { r.msgs = append(r.msgs, fmt.Sprintf(f, args...)) }
func (r *recorder) Error(args ...any)            { r.msgs = append(r.msgs, fmt.Sprint(args...)) }
func (r *recorder) Fatalf(f string, args ...any) { r.msgs = append(r.msgs, fmt.Sprintf(f, args...)) }
func (r *recorder) Fatal(args ...any)            { r.msgs = append(r.msgs, fmt.Sprint(args...)) }
func (r *recorder) Helper()                      {}

// Mirrors of the fixture's types, so a Values entry has the scanned name.
type Refused struct{ Name string }

func (Refused) Build() (string, error) { return "", errors.New("refused") }

type builds struct{}

func (builds) Build() (string, error) { return "SQL", nil }

type panics struct{}

func (panics) Build() (string, error) { panic("boom") }

type clause struct{}

func (clause) WriteSQL(b *strings.Builder, _ *sqlb.Args) { b.WriteString("X") }

var errProbe = errors.New("probe refused")

func run(cfg sqlbtest.ZeroConfig) []string {
	r := &recorder{TB: &testing.T{}}
	cfg.Dir = "testdata/zeropkg"
	cfg.Dialect = sqlb.PostgresDialect{}
	sqlbtest.ZeroValues(r, cfg)
	return r.msgs
}

func base() sqlbtest.ZeroConfig {
	return sqlbtest.ZeroConfig{
		Values:   []any{Refused{}},
		Probes:   map[string]func() error{"Probed": func() error { return errProbe }},
		Controls: map[string]func() error{"Probed": func() error { return nil }},
		Exempt:   map[string]string{"Plain": "renders nothing"},
	}
}

// TestZeroValues_PassesAClassifiedPackage is the control: every fixture type
// in its right place reports nothing.
func TestZeroValues_PassesAClassifiedPackage(t *testing.T) {
	if msgs := run(base()); len(msgs) != 0 {
		t.Fatalf("a clean configuration reported: %v", msgs)
	}
}

// TestZeroValues_ReportsEachWayAZeroValueGoesWrong pins every finding the
// runner makes, one broken configuration at a time.
func TestZeroValues_ReportsEachWayAZeroValueGoesWrong(t *testing.T) {
	cases := map[string]struct {
		edit func(*sqlbtest.ZeroConfig)
		want string
	}{
		"a zero value that builds": {func(c *sqlbtest.ZeroConfig) { c.Values = []any{builds{}} }, "built without an error"},
		"a zero value that panics": {func(c *sqlbtest.ZeroConfig) { c.Values = []any{panics{}} }, "panicked: boom"},
		"a clause that renders":    {func(c *sqlbtest.ZeroConfig) { c.Values = []any{clause{}} }, `rendered "X" without an error`},
		"a type with no method":    {func(c *sqlbtest.ZeroConfig) { c.Values = []any{struct{}{}} }, "has no Build or render method"},
		"a probe that succeeds": {func(c *sqlbtest.ZeroConfig) {
			c.Probes["Probed"] = func() error { return nil }
		}, "a zero value must be refused"},
		"a probe that panics": {func(c *sqlbtest.ZeroConfig) {
			c.Probes["Probed"] = func() error { panic("probe") }
		}, "the probe for Probed panicked"},
		"a probe with no control": {func(c *sqlbtest.ZeroConfig) { c.Controls = nil }, "has no control"},
		"a control that fails": {func(c *sqlbtest.ZeroConfig) {
			c.Controls["Probed"] = func() error { return errProbe }
		}, "its probe tests nothing"},
		"a control with no probe": {func(c *sqlbtest.ZeroConfig) {
			c.Controls["Other"] = func() error { return nil }
		}, "a control for Other has no probe"},
		"an exemption with no reason": {func(c *sqlbtest.ZeroConfig) { c.Exempt["Plain"] = " " }, "exempt with no reason"},
		"an unclassified type":        {func(c *sqlbtest.ZeroConfig) { delete(c.Exempt, "Plain") }, "Plain is an exported struct with no zero-value check"},
		"a type listed twice": {func(c *sqlbtest.ZeroConfig) {
			c.Exempt["Refused"] = "twice"
		}, "Refused is listed twice"},
		"a stale name": {func(c *sqlbtest.ZeroConfig) { c.Exempt["Gone"] = "was here" }, "Gone in Exempt is not an exported struct"},
	}
	for name, tc := range cases {
		cfg := base()
		tc.edit(&cfg)
		msgs := strings.Join(run(cfg), "\n")
		if !strings.Contains(msgs, tc.want) {
			t.Errorf("%s: want a message containing %q, got:\n%s", name, tc.want, msgs)
		}
	}
}

// TestZeroValues_FailsClosedOnAnEmptyScan pins that a Dir with no exported
// structs is an error rather than a pass over nothing.
func TestZeroValues_FailsClosedOnAnEmptyScan(t *testing.T) {
	r := &recorder{TB: &testing.T{}}
	sqlbtest.ZeroValues(r, sqlbtest.ZeroConfig{Dir: t.TempDir(), Dialect: sqlb.PostgresDialect{}})
	if len(r.msgs) == 0 || !strings.Contains(r.msgs[0], "no exported struct types") {
		t.Fatalf("an empty directory passed: %v", r.msgs)
	}
}
