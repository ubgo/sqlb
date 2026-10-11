package pg

import (
	"errors"
	"strconv"
	"strings"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
)

const (
	kwPartitionBy     = "PARTITION BY"
	kwRange           = "RANGE"
	kwList            = "LIST"
	kwHash            = "HASH"
	kwPartitionOf     = "PARTITION OF"
	kwForValues       = "FOR VALUES"
	kwModulus         = "MODULUS"
	kwRemainder       = "REMAINDER"
	kwAttachPartition = "ATTACH PARTITION"
	kwDetachPartition = "DETACH PARTITION"
	kwFinalize        = "FINALIZE"
	kwAlterTable      = "ALTER TABLE"
)

// ErrNoBound is returned for a partition with neither bounds nor Default.
var ErrNoBound = errors.New("sqlb/pg: a partition needs bounds or Default")

// partitionBy renders PARTITION BY strategy (cols) as an option clause that
// names its columns, so a key the table does not define fails Build.
func partitionBy(strategy string, cols []ddl.ColumnRef) TableClause {
	return ddl.NewOptionClauseOn[Dialect](rankPartitionBy, cols, func(a *sqlb.Args) string {
		return kwPartitionBy + " " + strategy + " " + quoteCols(a, cols)
	})
}

// PartitionByRange partitions the table by ranges of its key, time most often.
// A primary or unique key on a partitioned table must include the key.
func PartitionByRange(first ddl.ColumnRef, more ...ddl.ColumnRef) TableClause {
	return partitionBy(kwRange, append([]ddl.ColumnRef{first}, more...))
}

// PartitionByList partitions by listed values of one column, a tenant or a
// region.
func PartitionByList(col ddl.ColumnRef) TableClause {
	return partitionBy(kwList, []ddl.ColumnRef{col})
}

// PartitionByHash spreads rows over a fixed number of partitions by hash.
func PartitionByHash(first ddl.ColumnRef, more ...ddl.ColumnRef) TableClause {
	return partitionBy(kwHash, append([]ddl.ColumnRef{first}, more...))
}

// PartitionBound is FOR VALUES ... or DEFAULT: FromTo, ListValues, HashOf and
// DefaultPartition make one.
type PartitionBound interface {
	writeBound(a *sqlb.Args) string
}

type bound func(a *sqlb.Args) string

func (b bound) writeBound(a *sqlb.Args) string { return b(a) }

// FromTo is a range partition's bounds, from inclusive to exclusive, typed to
// the key, so a partition of a timestamp key cannot be bounded by numbers. The
// key is not rendered, since a partition's bound names no column; it is taken
// only so its Go type fixes T.
func FromTo[T any](_ sqlb.Expr[T], from, to T) PartitionBound {
	return bound(func(a *sqlb.Args) string {
		return kwForValues + " " + kwFrom + " (" + a.Literal(from) + ") " + kwTo + " (" + a.Literal(to) + ")"
	})
}

// ListValues is a list partition's values, typed to the key, which is taken
// only so its Go type fixes T.
func ListValues[T any](_ sqlb.Expr[T], first T, more ...T) PartitionBound {
	vals := append([]T{first}, more...)
	return bound(func(a *sqlb.Args) string {
		lits := make([]string, len(vals))
		for i, v := range vals {
			lits[i] = a.Literal(v)
		}
		return kwForValues + " " + kwIn + " (" + strings.Join(lits, ", ") + ")"
	})
}

// HashOf is a hash partition: the rows whose key hashes to remainder modulo
// modulus.
func HashOf(modulus, remainder int) PartitionBound {
	return bound(func(*sqlb.Args) string {
		return kwForValues + " " + kwWith + " (" + kwModulus + " " + strconv.Itoa(modulus) + ", " + kwRemainder + " " + strconv.Itoa(remainder) + ")"
	})
}

// DefaultPartition takes every row no other partition does. While one exists,
// attaching a new partition scans it, and DETACH ... CONCURRENTLY is refused.
func DefaultPartition() PartitionBound {
	return bound(func(*sqlb.Args) string { return kwDefault })
}

// CreatePartitionQuery is CREATE TABLE ... PARTITION OF with no bounds yet,
// which has no Build.
type CreatePartitionQuery struct {
	child, parent sqlb.Table
	ifNotExists   bool
}

// CreatePartition starts a partition child of parent. It has no column list:
// the partition takes the parent's.
func CreatePartition(child, parent sqlb.Table) *CreatePartitionQuery {
	return &CreatePartitionQuery{child: child, parent: parent}
}

// IfNotExists makes creating an existing partition a no-op.
func (x *CreatePartitionQuery) IfNotExists() *CreatePartitionQuery {
	c := *x
	c.ifNotExists = true
	return &c
}

// For gives the bounds, and so completes the statement.
func (x *CreatePartitionQuery) For(b PartitionBound) *CreatePartitionStatement {
	return &CreatePartitionStatement{q: *x, bound: b}
}

// CreatePartitionStatement is a complete CREATE TABLE ... PARTITION OF.
type CreatePartitionStatement struct {
	q     CreatePartitionQuery
	bound PartitionBound
}

// IfNotExists makes creating an existing partition a no-op.
func (x *CreatePartitionStatement) IfNotExists() *CreatePartitionStatement {
	c := *x
	c.q.ifNotExists = true
	return &c
}

// Build renders the statement.
func (x *CreatePartitionStatement) Build() (ddl.Script, error) {
	if x.bound == nil {
		return nil, ErrNoBound
	}
	if x.q.parent.Name == "" {
		return nil, ErrEmptyName
	}
	return buildOne(x.q.child.Name, true, func(a *sqlb.Args) string {
		d := a.Dialect()
		return kwCreateTable + ifNotExists(x.q.ifNotExists) + " " + x.q.child.Qualified(d) + " " + kwPartitionOf + " " + x.q.parent.Qualified(d) + " " + x.bound.writeBound(a)
	})
}

// AttachPartition attaches an existing table as a partition, scanning it to
// check the bounds; a CHECK constraint matching them lets Postgres skip the
// scan. A statement of its own in Postgres's grammar.
func AttachPartition(child sqlb.Table, b PartitionBound) AlterClause {
	return alterAction{standalone: true, sql: func(a *sqlb.Args) string {
		if b == nil {
			a.Fail(ErrNoBound)
			return ""
		}
		return kwAttachPartition + " " + child.Qualified(a.Dialect()) + " " + b.writeBound(a)
	}}
}

// DetachStatement is ALTER TABLE ... DETACH PARTITION, its own builder because
// CONCURRENTLY makes it non-transactional, which an ALTER TABLE action cannot
// say.
type DetachStatement struct {
	parent, child          sqlb.Table
	concurrently, finalize bool
}

// DetachPartition detaches child from parent, leaving it an ordinary table.
func DetachPartition(parent, child sqlb.Table) *DetachStatement {
	return &DetachStatement{parent: parent, child: child}
}

// Concurrently detaches without blocking queries on the parent. Postgres
// refuses it inside a transaction and while a default partition exists, so
// the Statement is non-transactional.
func (x *DetachStatement) Concurrently() *DetachStatement { c := *x; c.concurrently = true; return &c }

// Finalize completes a concurrent detach that was interrupted.
func (x *DetachStatement) Finalize() *DetachStatement { c := *x; c.finalize = true; return &c }

// Build renders the statement.
func (x *DetachStatement) Build() (ddl.Script, error) {
	if x.child.Name == "" {
		return nil, ErrEmptyName
	}
	return buildOne(x.parent.Name, !x.concurrently, func(a *sqlb.Args) string {
		d := a.Dialect()
		s := kwAlterTable + " " + x.parent.Qualified(d) + " " + kwDetachPartition + " " + x.child.Qualified(d)
		switch {
		case x.finalize:
			s += " " + kwFinalize
		case x.concurrently:
			s += " " + kwConcurrently
		}
		return s
	})
}
