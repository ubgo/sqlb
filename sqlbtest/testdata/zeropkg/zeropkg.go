// Package zeropkg is the fixture sqlbtest's own tests scan: three exported
// structs, one of each kind ZeroValues classifies.
package zeropkg

// Refused is a statement whose zero value fails to build.
type Refused struct{ Name string }

// Probed is a value its probe passes to a statement.
type Probed struct{ Name string }

// Plain is a value with nothing to render.
type Plain struct{ N int }
