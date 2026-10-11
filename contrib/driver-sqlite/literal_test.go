package driversqlite_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	driversqlite "github.com/ubgo/sqlb/contrib/driver-sqlite"
	"github.com/ubgo/sqlb/dialect/sqlite"
)

// TestLiteral_HostileStringsRoundTrip pins the SQLite quoter against a real
// database: every hostile string, written as a literal, reads back as exactly
// the same string. SQLite has no escape character in a string literal, so a
// backslash is ordinary text and only the quote is doubled; this is what
// proves that rule rather than asserting it.
func TestLiteral_HostileStringsRoundTrip(t *testing.T) {
	db, err := driversqlite.Memory()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()

	values := []string{
		`it's`, `'`, `''`, `\`, `\\`, `a\b`, `trailing\`, `\'`, `$$`, `--`, `/*`,
		`x'); DROP TABLE t; --`, "héllo ☃", "",
	}
	var ctrl strings.Builder
	for b := byte(1); b < 32; b++ {
		ctrl.WriteByte(b)
	}
	values = append(values, ctrl.String())

	for _, s := range values {
		lit, err := sqlite.Dialect{}.QuoteLiteral(s)
		if err != nil {
			t.Fatalf("%q: %v", s, err)
		}
		var got string
		if err := db.QueryRowContext(ctx, "SELECT "+lit).Scan(&got); err != nil {
			t.Fatalf("%q as %s: %v", s, lit, err)
		}
		if got != s {
			t.Errorf("wrote %q as %s, read back %q", s, lit, got)
		}
	}

	blob := []byte{0x00, 0x01, 0x5c, 0x27, 0xff}
	lit, err := sqlite.Dialect{}.QuoteLiteral(blob)
	if err != nil {
		t.Fatal(err)
	}
	var gotBlob []byte
	if err := db.QueryRowContext(ctx, "SELECT "+lit).Scan(&gotBlob); err != nil {
		t.Fatalf("%s: %v", lit, err)
	}
	if !bytes.Equal(gotBlob, blob) {
		t.Errorf("blob %x read back as %x", blob, gotBlob)
	}
}
