package cookbook_test

import (
	"testing"

	"github.com/ubgo/sqlb/examples/cookbook/phone"
	"github.com/ubgo/sqlb/examples/cookbook/workspace"
)

// Exists is the one piece of plugin behaviour the recipes never show, because
// a recipe prints SQL and this is about what a scanned row means afterwards.
//
// It matters more than its one line suggests. Both plugins join LEFT, so a
// user with no phone and a user with an empty phone number scan into exactly
// the same zero struct. Without Exists the application cannot tell "no row"
// from "a row with nothing in it", and the distinction is usually the whole
// point of the join.
func TestPluginData_ExistsDistinguishesMissFromEmpty(t *testing.T) {
	var noRow phone.Data
	if noRow.Exists() {
		t.Error("a zero phone.Data must report that the join found nothing")
	}
	found := phone.Data{Number: "+61400000000"}
	if !found.Exists() {
		t.Error("a scanned phone number must report that the join found a row")
	}

	var noMembership workspace.Membership
	if noMembership.Exists() {
		t.Error("a zero workspace.Membership must report that the join found nothing")
	}
	member := workspace.Membership{WorkspaceID: "ws_1"}
	if !member.Exists() {
		t.Error("a scanned membership must report that the join found a row")
	}
}
