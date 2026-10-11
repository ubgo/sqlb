package scan

import (
	"bytes"
	"testing"
)

// TestAnyScanner_CopiesBytes pins the one conversion this scanner makes, and
// the reason it exists at all.
//
// A driver is allowed to hand back a slice into its own read buffer and
// overwrite it on the next row. Scanning into a plain *any keeps that slice,
// so every row but the last would silently hold the last row's bytes. The
// database-backed test proves the behaviour end to end; this one proves the
// mechanism in isolation, by mutating the source the way a driver would.
func TestAnyScanner_CopiesBytes(t *testing.T) {
	var dst any
	s := &anyScanner{&dst}

	src := []byte("first")
	if err := s.Scan(src); err != nil {
		t.Fatal(err)
	}
	copy(src, "XXXXX") // the driver reuses its buffer for the next row

	got, ok := dst.([]byte)
	if !ok {
		t.Fatalf("got %T, want []byte", dst)
	}
	if !bytes.Equal(got, []byte("first")) {
		t.Fatalf("got %q, want %q; the value aliases the driver's buffer", got, "first")
	}
}

// TestAnyScanner_PassesEverythingElseThrough pins that nothing but []byte is
// touched.
//
// Converting further would have to guess — is an int64 an ID or a count, is a
// string a date — and a guess here is invisible to the caller, so the driver's
// own answer is what lands in the map. NULL in particular stays nil rather
// than becoming a zero value, because a caller cannot tell those apart
// afterwards.
func TestAnyScanner_PassesEverythingElseThrough(t *testing.T) {
	for _, src := range []any{nil, int64(7), "ada", 1.5, true} {
		var dst any
		if err := (&anyScanner{&dst}).Scan(src); err != nil {
			t.Fatal(err)
		}
		if dst != src {
			t.Errorf("Scan(%#v) stored %#v", src, dst)
		}
	}
}
