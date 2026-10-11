package driverpgx_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ubgo/sqlb"
	driverpgx "github.com/ubgo/sqlb/contrib/driver-pgx"
	"github.com/ubgo/sqlb/dialect/postgres"
	"github.com/ubgo/sqlb/pg"
)

// stringSettings are both values of the Postgres setting that decides whether
// a backslash in a plain string literal is an escape. Every literal test runs
// under each, because a literal that is right under the default and wrong
// under the other is the bug these tests exist for.
var stringSettings = []string{"on", "off"}

// withSetting runs fn against a database whose connections all start with
// standard_conforming_strings set, each setting in a pool of its own.
//
// A pool per setting is not tidiness, it is what makes the second setting
// real. pgx caches a prepared statement per connection, keyed by its text,
// and Postgres parses a literal when the statement is prepared. With one pool,
// SET on and SET off on a reused connection, the second run executed the plan
// the first had prepared and never parsed the literal under its own setting,
// so the "off" half of every test here passed against a quoter that was wrong
// under "off". Found by breaking the quoter and watching these tests pass.
func withSetting(t *testing.T, setting string, fn func(db *sql.DB)) {
	t.Helper()
	dsn, ok := driverpgx.DSNFromEnv()
	if !ok {
		t.Skipf("%s not set: the Postgres proof did not run", driverpgx.EnvDSN)
	}
	db, err := driverpgx.Open(withRuntimeParam(dsn, "standard_conforming_strings", setting))
	if err != nil {
		t.Fatalf("Open with standard_conforming_strings=%s: %v", setting, err)
	}
	defer func() { _ = db.Close() }()
	var got string
	if err := db.QueryRowContext(context.Background(), "SHOW standard_conforming_strings").Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != setting {
		t.Fatalf("asked for standard_conforming_strings=%s, the server reports %s", setting, got)
	}
	fn(db)
}

// withRuntimeParam adds a startup parameter to a connection string in either
// of the two forms pgx accepts: a URL, or keyword=value pairs.
func withRuntimeParam(dsn, key, value string) string {
	if u, err := url.Parse(dsn); err == nil && (u.Scheme == "postgres" || u.Scheme == "postgresql") {
		q := u.Query()
		q.Set(key, value)
		u.RawQuery = q.Encode()
		return u.String()
	}
	return dsn + " " + key + "=" + value
}

// TestLiteral_JSONPathMeansTheSameUnderBothStringSettings is the bug report,
// executed.
//
// A jsonpath is a literal, because JSON_EXISTS takes its path as a constant.
// This one holds a backslash, in the like_regex "^\\d$". Written as a plain
// '...' literal, which is what pg did before 2026-09-21, it matched with the
// setting on and silently did not with it off. The E'...' form the Postgres
// quoter now writes matches under both.
func TestLiteral_JSONPathMeansTheSameUnderBothStringSettings(t *testing.T) {
	doc := sqlb.Literal[json.RawMessage](`'{"a":"5"}'::jsonb`)
	path := `$.a ? (@ like_regex "^\\d$")`
	q, err := pg.Select(sqlb.Literal[int]("1")).Where(pg.JSONExists(doc, path)).Build()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(q.SQL, `E'`) {
		t.Fatalf("a path holding a backslash must be written in the E'...' form: %s", q.SQL)
	}
	for _, setting := range stringSettings {
		withSetting(t, setting, func(conn *sql.DB) {
			rows, err := conn.QueryContext(context.Background(), q.SQL, q.Args...)
			if err != nil {
				t.Fatalf("setting %s: %v", setting, err)
			}
			defer func() { _ = rows.Close() }()
			if !rows.Next() {
				t.Errorf("standard_conforming_strings=%s: the path did not match; the literal was read differently", setting)
			}
		})
	}
}

// hostileStrings are the values a literal renderer gets wrong: quotes,
// backslashes in every position, the start of a dollar quote and of a comment,
// every control byte except NUL, and multi-byte text.
func hostileStrings() []string {
	out := []string{
		`it's`, `'`, `''`, `\`, `\\`, `a\b`, `trailing\`, `\'`, `\x41`, `E'x'`,
		`$$`, `$tag$`, `--`, `/*`, `*/`, `x'); DROP TABLE t; --`, "héllo ☃", "",
	}
	var ctrl strings.Builder
	for b := byte(1); b < 32; b++ {
		ctrl.WriteByte(b)
	}
	return append(out, ctrl.String())
}

// TestLiteral_HostileStringsRoundTripUnderBothSettings pins the quoter against
// a live server: every hostile string, written as a literal, reads back as
// exactly the same string with the setting on and with it off.
func TestLiteral_HostileStringsRoundTripUnderBothSettings(t *testing.T) {
	for _, setting := range stringSettings {
		withSetting(t, setting, func(conn *sql.DB) {
			for _, s := range hostileStrings() {
				lit, err := postgres.Dialect{}.QuoteLiteral(s)
				if err != nil {
					t.Fatalf("%q: %v", s, err)
				}
				var got string
				if err := conn.QueryRowContext(context.Background(), "SELECT "+lit+"::text").Scan(&got); err != nil {
					t.Fatalf("setting %s, %q as %s: %v", setting, s, lit, err)
				}
				if got != s {
					t.Errorf("setting %s: wrote %q as %s, read back %q", setting, s, lit, got)
				}
			}
		})
	}
}

// TestLiteral_BytesAndTimesRoundTrip pins the two literals with a cast: a bytea
// in hex form, which starts with a backslash and so depends on the E'...'
// form as much as any string, and a timestamptz written in UTC.
func TestLiteral_BytesAndTimesRoundTrip(t *testing.T) {
	blob := []byte{0x00, 0x01, 0x5c, 0x27, 0xff}
	when := time.Date(2026, 9, 1, 12, 30, 45, 123456000, time.UTC)
	for _, setting := range stringSettings {
		withSetting(t, setting, func(conn *sql.DB) {
			lit, err := postgres.Dialect{}.QuoteLiteral(blob)
			if err != nil {
				t.Fatal(err)
			}
			var gotBlob []byte
			if err := conn.QueryRowContext(context.Background(), "SELECT "+lit).Scan(&gotBlob); err != nil {
				t.Fatalf("setting %s, %s: %v", setting, lit, err)
			}
			if !bytes.Equal(gotBlob, blob) {
				t.Errorf("setting %s: bytea %x read back as %x", setting, blob, gotBlob)
			}
			lit, err = postgres.Dialect{}.QuoteLiteral(when)
			if err != nil {
				t.Fatal(err)
			}
			var gotTime time.Time
			if err := conn.QueryRowContext(context.Background(), "SELECT "+lit).Scan(&gotTime); err != nil {
				t.Fatalf("setting %s, %s: %v", setting, lit, err)
			}
			if !gotTime.Equal(when) {
				t.Errorf("setting %s: time %v read back as %v", setting, when, gotTime)
			}
		})
	}
}
