package pg

import (
	"strings"

	"github.com/ubgo/sqlb"
)

const (
	constructLock = "pg.lock"

	kwForUpdate      = "FOR UPDATE"
	kwForNoKeyUpdate = "FOR NO KEY UPDATE"
	kwForShare       = "FOR SHARE"
	kwForKeyShare    = "FOR KEY SHARE"
	kwOf             = "OF"
	kwNoWait         = "NOWAIT"
	kwSkipLocked     = "SKIP LOCKED"
)

// LockOption modifies a row-locking clause.
type LockOption func(*lockSpec)

type lockSpec struct {
	of         []sqlb.Table
	noWait     bool
	skipLocked bool
}

// Of restricts the lock to the named tables.
func Of(tables ...sqlb.Table) LockOption { return func(s *lockSpec) { s.of = append(s.of, tables...) } }

// NoWait errors instead of waiting for a locked row.
func NoWait() LockOption { return func(s *lockSpec) { s.noWait = true } }

// SkipLocked skips locked rows: the job-queue idiom, with LIMIT.
func SkipLocked() LockOption { return func(s *lockSpec) { s.skipLocked = true } }

func lock(kw string, opts []LockOption) SelectClause {
	var spec lockSpec
	for _, o := range opts {
		o(&spec)
	}
	return selectClause(sqlb.SlotTail, constructLock, func(a *sqlb.Args) string {
		var b strings.Builder
		b.WriteString(kw)
		if len(spec.of) > 0 {
			b.WriteString(" ")
			b.WriteString(kwOf)
			b.WriteString(" ")
			for i, t := range spec.of {
				if i > 0 {
					b.WriteString(", ")
				}
				b.WriteString(t.Qualifier(a.Dialect()))
			}
		}
		if spec.noWait {
			b.WriteString(" ")
			b.WriteString(kwNoWait)
		}
		if spec.skipLocked {
			b.WriteString(" ")
			b.WriteString(kwSkipLocked)
		}
		return b.String()
	})
}

// ForUpdate renders FOR UPDATE, the strongest row lock: it blocks another
// FOR UPDATE, a FOR SHARE, and any update or delete of the row.
//
// The four here are ordered from strongest to weakest. Take the weakest that
// holds the invariant you need, because each stronger one blocks more writers.
func ForUpdate(opts ...LockOption) SelectClause { return lock(kwForUpdate, opts) }

// ForNoKeyUpdate renders FOR NO KEY UPDATE, which still allows another
// transaction to reference the row with a foreign key. The right lock when
// you are updating a non-key column of a row other tables point at.
func ForNoKeyUpdate(opts ...LockOption) SelectClause { return lock(kwForNoKeyUpdate, opts) }

// ForShare renders FOR SHARE: several readers may hold it, and it blocks
// writers.
func ForShare(opts ...LockOption) SelectClause { return lock(kwForShare, opts) }

// ForKeyShare renders FOR KEY SHARE, the weakest: it blocks only a change to
// the key. It is the lock a foreign-key check takes.
func ForKeyShare(opts ...LockOption) SelectClause { return lock(kwForKeyShare, opts) }

// ForUpdateSkipLocked is the queue idiom in one call.
func ForUpdateSkipLocked() SelectClause { return ForUpdate(SkipLocked()) }
