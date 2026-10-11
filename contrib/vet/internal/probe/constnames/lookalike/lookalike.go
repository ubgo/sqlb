// Package lookalike has a function named Col with Col's signature, so the
// test can check that Declarations reads only sqlb's Col and not every Col.
package lookalike

import "github.com/ubgo/sqlb/expr"

// Col looks like expr.Col and is not it.
func Col[T any](t expr.Table, name string) string { return t.Name + name }

// Pair is a two-argument generic call that is not a column at all.
func Pair[T any](a, b T) [2]T { return [2]T{a, b} }
