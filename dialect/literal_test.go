package dialect_test

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/ubgo/sqlb/dialect/clickhouse"
	"github.com/ubgo/sqlb/dialect/mysql"
	"github.com/ubgo/sqlb/dialect/postgres"
	"github.com/ubgo/sqlb/dialect/sqlite"
	"github.com/ubgo/sqlb/render"
)

// status is a named string type, the shape a typed enum's labels take.
type status string

// cents is a named integer type.
type cents int64

// TestQuoteLiteral_Postgres pins the exact literal for every kind of value.
//
// The rule the cases exist for: a string with no backslash is written plainly
// and one with a backslash in the E'...' form, because with
// standard_conforming_strings off Postgres reads a backslash in a plain
// literal as an escape. Both forms were executed under both settings on
// Postgres 17 and read back identically; contrib/driver-pgx repeats that
// against a live server.
func TestQuoteLiteral_Postgres(t *testing.T) {
	s := "active"
	for _, tc := range []struct {
		name string
		v    any
		want string
	}{
		{"plain string", "active", `'active'`},
		{"quote doubled", "it's", `'it''s'`},
		{"backslash takes the E form", `a\b`, `E'a\\b'`},
		{"backslash and quote together", `a\b'c`, `E'a\\b''c'`},
		{"a hostile string stays one literal", `x'); DROP TABLE t; --`, `'x''); DROP TABLE t; --'`},
		{"dollar quoting is inert inside a literal", `$$ $tag$`, `'$$ $tag$'`},
		{"multi-byte text is untouched", "héllo ☃", `'héllo ☃'`},
		{"empty string", "", `''`},
		{"true", true, `true`},
		{"false", false, `false`},
		{"int64", int64(-42), `-42`},
		{"uint8", uint8(7), `7`},
		{"float64", 1.5, `1.5`},
		{"float32 keeps its own precision", float32(0.1), `0.1`},
		{"bytes are hex in the E form", []byte{0x00, 0xff}, `E'\\x00ff'::bytea`},
		{"time is UTC with microseconds", time.Date(2026, 9, 1, 12, 30, 45, 123456789, time.FixedZone("x", 3600)), `'2026-09-01 11:30:45.123456Z'::timestamptz`},
		{"nil", nil, `NULL`},
		{"nil pointer", (*string)(nil), `NULL`},
		{"pointer is dereferenced", &s, `'active'`},
		{"named string", status("banned"), `'banned'`},
		{"named int", cents(100), `100`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := postgres.Dialect{}.QuoteLiteral(tc.v)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("got  %s\nwant %s", got, tc.want)
			}
		})
	}
}

// TestQuoteLiteral_SQLite pins the SQLite spellings. SQLite has no escape
// character in a string literal, so a backslash is ordinary text and the
// quote is the only thing doubled.
func TestQuoteLiteral_SQLite(t *testing.T) {
	for _, tc := range []struct {
		name string
		v    any
		want string
	}{
		{"plain string", "active", `'active'`},
		{"quote doubled", "it's", `'it''s'`},
		{"backslash is ordinary", `a\b`, `'a\b'`},
		{"a hostile string stays one literal", `x'); DROP TABLE t; --`, `'x''); DROP TABLE t; --'`},
		{"true is 1", true, `1`},
		{"false is 0", false, `0`},
		{"int", 42, `42`},
		{"float", 2.25, `2.25`},
		{"bytes are a blob literal", []byte{0xde, 0xad}, `X'dead'`},
		{"nil", nil, `NULL`},
		{"named string", status("active"), `'active'`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := sqlite.Dialect{}.QuoteLiteral(tc.v)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("got  %s\nwant %s", got, tc.want)
			}
		})
	}
}

// TestQuoteLiteral_MySQL pins MySQL's rule: quotes doubled, a backslash
// refused, bytes and times in forms that read the same under every sql_mode.
func TestQuoteLiteral_MySQL(t *testing.T) {
	for _, tc := range []struct {
		name string
		v    any
		want string
	}{
		{"plain string", "active", `'active'`},
		{"quote doubled", "it's", `'it''s'`},
		{"a hostile string stays one literal", `x'); DROP TABLE t; --`, `'x''); DROP TABLE t; --'`},
		{"true", true, `TRUE`},
		{"false", false, `FALSE`},
		{"int", 42, `42`},
		{"float", 2.25, `2.25`},
		{"bytes are a hex literal", []byte{0xde, 0xad}, `X'dead'`},
		{"time in UTC with no zone", time.Date(2026, 1, 2, 3, 4, 5, 6000, time.FixedZone("x", 3600)), `'2026-01-02 02:04:05.000006'`},
		{"nil", nil, `NULL`},
		{"named string", status("active"), `'active'`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := mysql.Dialect{}.QuoteLiteral(tc.v)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("got  %s\nwant %s", got, tc.want)
			}
		})
	}
	if _, err := (mysql.Dialect{}).QuoteLiteral(`a\b`); !errors.Is(err, render.ErrNotInlinable) {
		t.Fatalf("a backslash: got %v, want ErrNotInlinable; no spelling reads the same under NO_BACKSLASH_ESCAPES", err)
	}
}

// TestQuoteLiteral_RefusesWhatItCannotWriteFaithfully pins the closed set. A
// value outside it is an error naming the problem, never a best guess, since
// a guessed literal is either an injection or a value the engine stores
// differently from what was written.
func TestQuoteLiteral_RefusesWhatItCannotWriteFaithfully(t *testing.T) {
	quoters := map[string]render.LiteralQuoting{"postgres": postgres.Dialect{}, "sqlite": sqlite.Dialect{}, "mysql": mysql.Dialect{}}
	refused := map[string]any{
		"a string holding NUL": "a\x00b",
		"NaN":                  math.NaN(),
		"infinity":             math.Inf(1),
		"a struct":             struct{}{},
		"a map":                map[string]int{},
		"a slice of strings":   []string{"a"},
	}
	for engine, q := range quoters {
		for name, v := range refused {
			t.Run(engine+"/"+name, func(t *testing.T) {
				if _, err := q.QuoteLiteral(v); !errors.Is(err, render.ErrNotInlinable) {
					t.Fatalf("got %v, want ErrNotInlinable", err)
				}
			})
		}
	}
	t.Run("sqlite declines time.Time", func(t *testing.T) {
		if _, err := (sqlite.Dialect{}).QuoteLiteral(time.Now()); !errors.Is(err, render.ErrNotInlinable) {
			t.Fatalf("got %v, want ErrNotInlinable: SQLite has no faithful time spelling", err)
		}
	})
}

// TestQuoteLiteral_ClickHouseDeclines pins that ClickHouse does not claim the
// capability. Its string literals take backslash escapes, and nothing in this
// module has ever run against a ClickHouse server, so a quoter written from its
// documentation would be an unverified claim in the one place a wrong rule is
// an injection. A dialect declines by not implementing the interface.
func TestQuoteLiteral_ClickHouseDeclines(t *testing.T) {
	var d render.Dialect = clickhouse.Dialect{}
	if _, ok := d.(render.LiteralQuoting); ok {
		t.Fatal("clickhouse.Dialect implements LiteralQuoting without an execution test behind it")
	}
}
