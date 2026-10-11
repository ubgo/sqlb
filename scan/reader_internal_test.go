package scan

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// An internal test, for the same reason the JSON one is: what a Reader does
// per row lives behind alloc, which is unexported because exporting it would
// make the contract in New's doc comment part of the API. Reaching it here is
// what lets the per-row behaviour be checked without a database.

// TestMap_AppliesItsFunctionPerRow pins the half of Map that the column test
// cannot see. Map keeps the columns of the Reader it wraps and converts the
// scanned value, so the conversion must run once per row and read that row's
// destination: a Map that captured the first row's value would return it for
// every row, and a query of one row would never show it.
func TestMap_AppliesItsFunctionPerRow(t *testing.T) {
	type name string
	r := Map(Field(internalCol), func(s string) name { return name(strings.ToUpper(s)) })
	if len(r.cols) != 1 {
		t.Fatalf("Map must keep the wrapped Reader's columns, got %d", len(r.cols))
	}
	var got []name
	for _, row := range []string{"ada", "grace"} {
		dests, value := r.alloc()
		dst, ok := dests[0].(*string)
		if !ok {
			t.Fatalf("the destination is %T, want *string", dests[0])
		}
		*dst = row
		got = append(got, value())
	}
	if len(got) != 2 || got[0] != "ADA" || got[1] != "GRACE" {
		t.Fatalf("got %v, want [ADA GRACE]", got)
	}
}

// TestMap2_ReadsEachHalfsOwnDestinations pins the pairing. Map2 hands back
// a's destinations followed by b's, and the value function must read each half
// from its own: crossing them would line every value up against the wrong
// column, which renders and executes cleanly and is wrong in the result.
func TestMap2_ReadsEachHalfsOwnDestinations(t *testing.T) {
	type pair struct {
		a, b string
	}
	r := Map2(Field(internalCol), Field(internalCol), func(a, b string) pair { return pair{a, b} })
	dests, value := r.alloc()
	if len(dests) != 2 {
		t.Fatalf("got %d destinations, want 2", len(dests))
	}
	*(dests[0].(*string)) = "first"
	*(dests[1].(*string)) = "second"
	if got := value(); got != (pair{"first", "second"}) {
		t.Fatalf("got %+v, want {first second}", got)
	}
}

// TestJSON_MalformedJSONPanicsWithTheColumnNamed pins the one place this
// package panics, which is documented at the construct: alloc has no error
// path, and giving it one would change every Reader's shape for this case
// alone. A column that is supposed to hold an aggregate and holds something
// else is a schema mistake, not a row's, so it must stop the program with a
// message naming the cause rather than return a zero value that reads as "no
// children".
func TestJSON_MalformedJSONPanicsWithTheColumnNamed(t *testing.T) {
	r := JSON[[]string](internalCol)
	dests, value := r.alloc()
	if err := dests[0].(interface{ Scan(any) error }).Scan([]byte(`{"not":`)); err != nil {
		t.Fatalf("the scanner takes any bytes; the JSON is checked when the value is read: %v", err)
	}
	defer func() {
		p := recover()
		if p == nil {
			t.Fatal("malformed JSON was read as a value")
		}
		err, ok := p.(error)
		if !ok {
			t.Fatalf("the panic carries %T, want an error", p)
		}
		if !strings.Contains(err.Error(), "JSON column") {
			t.Fatalf("the panic must name the cause, got %v", err)
		}
		var syntax *json.SyntaxError
		if !errors.As(err, &syntax) {
			t.Fatalf("the panic must carry the unmarshal error, got %v", err)
		}
	}()
	_ = value()
}

// TestCollect_OfNoRowsIsNil pins that Collect answers nil rather than an empty
// slice for an empty group. It is what a parent with no children gets from
// GroupByPtr, and nil is the value that marshals as null and compares equal to
// the zero value of the field it lands in.
func TestCollect_OfNoRowsIsNil(t *testing.T) {
	if got := Collect(nil, func(int) string { return "" }); got != nil {
		t.Fatalf("got %v, want nil", got)
	}
	if got := Collect([]int{}, func(int) string { return "" }); got != nil {
		t.Fatalf("got %v, want nil", got)
	}
}
