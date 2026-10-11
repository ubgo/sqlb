package mysql

import (
	"errors"
	"fmt"
	"math/big"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/schema"
)

// MySQL partitioning: PARTITION BY in CREATE TABLE, and the ALTER TABLE
// actions that maintain partitions. Each rule below was measured on MySQL
// 8.4, with the server's error number, and Build enforces it so a table that
// cannot exist fails before any statement is sent:
//
//   - every PRIMARY KEY and UNIQUE key must include every partition column
//     (1503);
//   - a partitioned InnoDB table may have no foreign key (1506);
//   - RANGE bounds must strictly increase (1493), and MAXVALUE may only be
//     the last (1481);
//   - partition names must be unique (1517).
//
// A partitioning function is one of the few MySQL allows, and only those that
// work on the columns this module writes: UNIX_TIMESTAMP is refused on a
// DATETIME as timezone-dependent (1486), and DATETIME(6) is how a timestamp is
// written here, so it is not offered.

const (
	kwPartitionBy        = "PARTITION BY"
	kwRange              = "RANGE"
	kwList               = "LIST"
	kwHash               = "HASH"
	kwKey                = "KEY"
	kwColumns            = "COLUMNS"
	kwPartitions         = "PARTITIONS"
	kwPartition          = "PARTITION"
	kwValuesLessThan     = "VALUES LESS THAN"
	kwValuesIn           = "VALUES IN"
	kwMaxValue           = "MAXVALUE"
	kwAddPartition       = "ADD PARTITION"
	kwDropPartition      = "DROP PARTITION"
	kwTruncatePartition  = "TRUNCATE PARTITION"
	kwReorganize         = "REORGANIZE PARTITION"
	kwInto               = "INTO"
	kwCoalescePartition  = "COALESCE PARTITION"
	kwRemovePartitioning = "REMOVE PARTITIONING"
	fnYear               = "YEAR"
	fnToDays             = "TO_DAYS"
	fnToSeconds          = "TO_SECONDS"
)

// The partitioning errors, each a table MySQL would refuse.
var (
	// ErrPartitionKeyNotInKey is a primary or unique key that does not
	// include every partition column.
	ErrPartitionKeyNotInKey = errors.New("sqlb/mysql: every primary and unique key must include every partition column")
	// ErrPartitionForeignKey is a foreign key on a partitioned table, which
	// InnoDB does not support.
	ErrPartitionForeignKey = errors.New("sqlb/mysql: a partitioned table cannot have a foreign key")
	// ErrPartitionBounds is RANGE bounds that do not strictly increase, a
	// MAXVALUE that is not last, or a bound with the wrong number of values.
	ErrPartitionBounds = errors.New("sqlb/mysql: partition bounds are not strictly increasing, or MAXVALUE is not last")
	// ErrPartitionDefinition is a partition with no name, a name used twice,
	// a definition of the wrong kind for the partitioning, or none at all
	// where RANGE and LIST need at least one.
	ErrPartitionDefinition = errors.New("sqlb/mysql: a partition definition is missing, unnamed, duplicated or of the wrong kind")
)

// PartitionKey is what a RANGE, LIST or HASH partitioning is computed from: an
// integer column, or one of the functions MySQL allows over a date column.
// It names its column, so the statement checks the column exists and the
// keys include it.
type PartitionKey struct {
	col ddl.ColumnRef
	fn  string
}

// PartitionOn partitions by an integer column's own value.
func PartitionOn[T integer](col sqlb.Expr[T]) PartitionKey { return PartitionKey{col: col} }

// PartitionYear partitions by YEAR(col).
func PartitionYear(col sqlb.Expr[time.Time]) PartitionKey { return PartitionKey{col: col, fn: fnYear} }

// PartitionToDays partitions by TO_DAYS(col), the day number.
func PartitionToDays(col sqlb.Expr[time.Time]) PartitionKey {
	return PartitionKey{col: col, fn: fnToDays}
}

// PartitionToSeconds partitions by TO_SECONDS(col).
func PartitionToSeconds(col sqlb.Expr[time.Time]) PartitionKey {
	return PartitionKey{col: col, fn: fnToSeconds}
}

func (k PartitionKey) sql(a *sqlb.Args) string {
	name := a.Dialect().QuoteIdent(k.col.Name())
	if k.fn == "" {
		return name
	}
	return k.fn + "(" + name + ")"
}

// partitionKind is which PARTITION BY a Partitioning is.
type partitionKind int

const (
	partRange partitionKind = iota + 1
	partList
	partHash
	partKey
)

// PartitionDef is one partition: a name and its bound. LessThan and
// LessThanMax make RANGE partitions, In makes LIST ones. A bound's values are
// written as literals, since no engine takes a placeholder in DDL; one value
// per partition column.
type PartitionDef struct {
	name   string
	kind   partitionKind
	values []any
	max    bool
}

// LessThan is a RANGE partition holding rows below values, one per partition
// column.
func LessThan(name string, values ...any) PartitionDef {
	return PartitionDef{name: name, kind: partRange, values: values}
}

// LessThanMax is the last RANGE partition, holding every row the others do
// not.
func LessThanMax(name string) PartitionDef {
	return PartitionDef{name: name, kind: partRange, max: true}
}

// In is a LIST partition holding rows whose key is one of values.
func In(name string, values ...any) PartitionDef {
	return PartitionDef{name: name, kind: partList, values: values}
}

// Partitioning is a PARTITION BY clause for CREATE TABLE. It goes at the
// option position and checks the whole table it belongs to; see the rules at
// the top of this file.
type Partitioning struct {
	kind    partitionKind
	key     PartitionKey    // RANGE, LIST, HASH over one key
	cols    []ddl.ColumnRef // the COLUMNS forms and KEY
	columns bool            // RANGE COLUMNS, LIST COLUMNS
	count   int             // HASH, KEY: PARTITIONS n
	defs    []PartitionDef
}

// PartitionByRange partitions by ranges of a key. Give the partitions with
// With.
func PartitionByRange(key PartitionKey) Partitioning {
	return Partitioning{kind: partRange, key: key}
}

// PartitionByRangeColumns partitions by ranges of one or more columns
// compared in order, which also takes dates and strings. Give the partitions
// with With, one value per column in each bound.
func PartitionByRangeColumns(first ddl.ColumnRef, more ...ddl.ColumnRef) Partitioning {
	return Partitioning{kind: partRange, columns: true, cols: append([]ddl.ColumnRef{first}, more...)}
}

// PartitionByList partitions by a key's value in a list.
func PartitionByList(key PartitionKey) Partitioning {
	return Partitioning{kind: partList, key: key}
}

// PartitionByListColumns partitions by one or more columns' values in a
// list.
func PartitionByListColumns(first ddl.ColumnRef, more ...ddl.ColumnRef) Partitioning {
	return Partitioning{kind: partList, columns: true, cols: append([]ddl.ColumnRef{first}, more...)}
}

// PartitionByHash spreads rows over n partitions by a key's hash.
func PartitionByHash(key PartitionKey, n int) Partitioning {
	return Partitioning{kind: partHash, key: key, count: n}
}

// PartitionByKey spreads rows over n partitions by MySQL's own hash of the
// columns, which takes any column type.
func PartitionByKey(n int, first ddl.ColumnRef, more ...ddl.ColumnRef) Partitioning {
	return Partitioning{kind: partKey, cols: append([]ddl.ColumnRef{first}, more...), count: n}
}

// With gives a RANGE or LIST partitioning its partitions, in order.
func (p Partitioning) With(defs ...PartitionDef) Partitioning {
	p.defs = append(slices.Clone(p.defs), defs...)
	return p
}

// Position places the clause after the table's options.
func (Partitioning) Position() ddl.Position { return ddl.PositionOption }

// OptionRank places PARTITION BY after every table option, which MySQL
// requires and which is a syntax error the other way round; the statement
// sorts it there whatever order the caller passed. See rankPartitioning.
func (Partitioning) OptionRank() ddl.OptionRank { return rankPartitioning }

// NamedColumns is the partition columns, which the statement checks it
// defines.
func (p Partitioning) NamedColumns() []ddl.ColumnRef {
	if p.key.col != nil {
		return []ddl.ColumnRef{p.key.col}
	}
	return p.cols
}

// TableClauseOf marks the clause as MySQL's.
func (Partitioning) TableClauseOf(Dialect) {}

// CheckTable enforces MySQL's rules on the table the partitioning belongs to:
// every primary and unique key includes every partition column, and there is
// no foreign key.
func (p Partitioning) CheckTable(t schema.Table) error {
	if len(t.ForeignKeys) > 0 {
		return fmt.Errorf("%w: %s references %s", ErrPartitionForeignKey, t.Name, t.ForeignKeys[0].RefTable)
	}
	need := make([]string, 0, len(p.NamedColumns()))
	for _, c := range p.NamedColumns() {
		if c != nil {
			need = append(need, c.Name())
		}
	}
	check := func(what string, key []string) error {
		for _, n := range need {
			if !slices.Contains(key, n) {
				return fmt.Errorf("%w: the %s (%s) lacks %s", ErrPartitionKeyNotInKey, what, strings.Join(key, ", "), n)
			}
		}
		return nil
	}
	if len(t.PrimaryKey) > 0 {
		if err := check("primary key", t.PrimaryKey); err != nil {
			return err
		}
	}
	for _, ix := range t.Indexes {
		if ix.Unique {
			if err := check("unique key "+ix.Name, ix.Columns); err != nil {
				return err
			}
		}
	}
	return nil
}

// arity is how many values each bound takes.
func (p Partitioning) arity() int {
	if p.columns {
		return len(p.cols)
	}
	return 1
}

// WriteSQL renders PARTITION BY and the partition definitions.
func (p Partitioning) WriteSQL(b *strings.Builder, a *sqlb.Args) {
	if p.kind == 0 || (p.key.col == nil && len(p.cols) == 0) {
		a.Fail(ddl.ErrZeroValue)
		return
	}
	var head string
	switch p.kind {
	case partRange, partList:
		kw := kwRange
		if p.kind == partList {
			kw = kwList
		}
		if p.columns {
			head = kw + " " + kwColumns + " " + quoteCols(a, p.cols)
		} else {
			head = kw + " (" + p.key.sql(a) + ")"
		}
	case partHash:
		head = kwHash + " (" + p.key.sql(a) + ")"
	case partKey:
		head = kwKey + " " + quoteCols(a, p.cols)
	}
	b.WriteString(kwPartitionBy + " " + head)
	switch p.kind {
	case partHash, partKey:
		if p.count < 1 || len(p.defs) > 0 {
			a.Fail(fmt.Errorf("%w: HASH and KEY take a partition count of at least 1 and no definitions", ErrPartitionDefinition))
			return
		}
		b.WriteString(" " + kwPartitions + " " + strconv.Itoa(p.count))
		return
	}
	defs, ok := p.renderDefs(a)
	if !ok {
		return
	}
	b.WriteString(" (" + defs + ")")
}

// renderDefs checks and writes a RANGE or LIST partitioning's definitions.
func (p Partitioning) renderDefs(a *sqlb.Args) (string, bool) {
	if len(p.defs) == 0 {
		a.Fail(fmt.Errorf("%w: %s needs at least one partition", ErrPartitionDefinition, kindName(p.kind)))
		return "", false
	}
	if err := checkDefs(p.kind, p.arity(), p.defs); err != nil {
		a.Fail(err)
		return "", false
	}
	parts := make([]string, len(p.defs))
	for i, def := range p.defs {
		parts[i] = writeDef(a, def, p.arity())
	}
	return strings.Join(parts, ", "), a.Err() == nil
}

func kindName(k partitionKind) string {
	if k == partList {
		return kwList
	}
	return kwRange
}

// checkDefs enforces the definition rules: names present and unique, every
// definition of the partitioning's kind with one value per column, RANGE
// bounds strictly increasing and MAXVALUE only last.
func checkDefs(kind partitionKind, arity int, defs []PartitionDef) error {
	seen := map[string]bool{}
	var prev []any
	for i, def := range defs {
		switch {
		case def.name == "":
			return fmt.Errorf("%w: partition %d has no name", ErrPartitionDefinition, i+1)
		case seen[strings.ToLower(def.name)]:
			return fmt.Errorf("%w: %s is used twice", ErrPartitionDefinition, def.name)
		case def.kind != kind:
			return fmt.Errorf("%w: %s is not a %s partition", ErrPartitionDefinition, def.name, kindName(kind))
		}
		seen[strings.ToLower(def.name)] = true
		if def.max {
			if i != len(defs)-1 {
				return fmt.Errorf("%w: MAXVALUE in %s, which is not the last partition", ErrPartitionBounds, def.name)
			}
			continue
		}
		if kind == partRange && len(def.values) != arity {
			return fmt.Errorf("%w: %s has %d values for %d partition columns", ErrPartitionBounds, def.name, len(def.values), arity)
		}
		if kind == partList && len(def.values) == 0 {
			return fmt.Errorf("%w: %s lists no values", ErrPartitionDefinition, def.name)
		}
		if kind == partRange {
			if prev != nil && !increasing(prev, def.values) {
				return fmt.Errorf("%w: %s does not lie above the partition before it", ErrPartitionBounds, def.name)
			}
			prev = def.values
		}
	}
	return nil
}

// increasing reports whether b lies strictly above a, comparing position by
// position as MySQL compares a RANGE COLUMNS tuple. A pair it cannot order,
// values of different kinds, is left to the server.
func increasing(a, b []any) bool {
	for i := range a {
		c, ok := compareBound(a[i], b[i])
		if !ok {
			return true
		}
		if c != 0 {
			return c < 0
		}
	}
	return false
}

// compareBound orders two bound values of the same kind: numbers
// numerically, strings and times by value.
func compareBound(x, y any) (int, bool) {
	if xs, ok := x.(string); ok {
		if ys, ok := y.(string); ok {
			return strings.Compare(xs, ys), true
		}
		return 0, false
	}
	if xt, ok := x.(time.Time); ok {
		if yt, ok := y.(time.Time); ok {
			return xt.Compare(yt), true
		}
		return 0, false
	}
	xr, ok1 := asRat(x)
	yr, ok2 := asRat(y)
	if !ok1 || !ok2 {
		return 0, false
	}
	return xr.Cmp(yr), true
}

func asRat(v any) (*big.Rat, bool) {
	switch n := v.(type) {
	case int:
		return new(big.Rat).SetInt64(int64(n)), true
	case int8:
		return new(big.Rat).SetInt64(int64(n)), true
	case int16:
		return new(big.Rat).SetInt64(int64(n)), true
	case int32:
		return new(big.Rat).SetInt64(int64(n)), true
	case int64:
		return new(big.Rat).SetInt64(n), true
	case uint8:
		return new(big.Rat).SetInt64(int64(n)), true
	case uint16:
		return new(big.Rat).SetInt64(int64(n)), true
	case uint32:
		return new(big.Rat).SetInt64(int64(n)), true
	case uint64:
		return new(big.Rat).SetUint64(n), true
	case float64:
		r := new(big.Rat)
		if r.SetFloat64(n) == nil {
			return nil, false
		}
		return r, true
	}
	return nil, false
}

// writeDef renders one partition definition, its values as literals.
// MAXVALUE is written once per partition column in parentheses, which both
// forms accept, measured: RANGE takes (MAXVALUE), and RANGE COLUMNS refuses
// the bare keyword and needs (MAXVALUE, MAXVALUE) for two columns. arity is
// how many partition columns there are.
func writeDef(a *sqlb.Args, def PartitionDef, arity int) string {
	d := a.Dialect()
	s := kwPartition + " " + d.QuoteIdent(def.name) + " "
	if def.max {
		maxes := make([]string, max(arity, 1))
		for i := range maxes {
			maxes[i] = kwMaxValue
		}
		return s + kwValuesLessThan + " (" + strings.Join(maxes, ", ") + ")"
	}
	vals := make([]string, len(def.values))
	for i, v := range def.values {
		vals[i] = a.Literal(v)
	}
	if def.kind == partRange {
		return s + kwValuesLessThan + " (" + strings.Join(vals, ", ") + ")"
	}
	return s + kwValuesIn + " (" + strings.Join(vals, ", ") + ")"
}

// partitionAction is an ALTER TABLE partition action. MySQL takes each as the
// only action of its statement, so every one is standalone.
func partitionAction(render func(a *sqlb.Args) string) AlterClause {
	return alterAction{standalone: true, sql: render}
}

// defsArity is how many partition columns a set of definitions implies, for
// an ALTER action, which does not know the table's partitioning: the value
// count of its first bounded definition, or 1 when every one is MAXVALUE.
func defsArity(defs []PartitionDef) int {
	for _, d := range defs {
		if !d.max && d.kind == partRange {
			return len(d.values)
		}
	}
	return 1
}

// AddPartitions adds RANGE or LIST partitions at the end. A new RANGE
// partition must lie above every existing one; the server checks that,
// since the existing bounds are not in the statement. A MAXVALUE partition is
// written for as many columns as the other partitions given bound; alone, it
// is written for one.
func AddPartitions(first PartitionDef, more ...PartitionDef) AlterClause {
	defs := append([]PartitionDef{first}, more...)
	return partitionAction(func(a *sqlb.Args) string {
		arity := defsArity(defs)
		if err := checkDefs(defs[0].kind, arity, defs); err != nil {
			a.Fail(err)
			return ""
		}
		parts := make([]string, len(defs))
		for i, def := range defs {
			parts[i] = writeDef(a, def, arity)
		}
		return kwAddPartition + " (" + strings.Join(parts, ", ") + ")"
	})
}

// AddHashPartitions adds n partitions to a HASH or KEY partitioning.
func AddHashPartitions(n int) AlterClause {
	return partitionAction(func(a *sqlb.Args) string {
		if n < 1 {
			a.Fail(fmt.Errorf("%w: add at least one partition", ErrPartitionDefinition))
			return ""
		}
		return kwAddPartition + " " + kwPartitions + " " + strconv.Itoa(n)
	})
}

func partitionNames(a *sqlb.Args, names []string) (string, bool) {
	if len(names) == 0 {
		a.Fail(fmt.Errorf("%w: name at least one partition", ErrPartitionDefinition))
		return "", false
	}
	out := make([]string, len(names))
	for i, n := range names {
		if n == "" {
			a.Fail(fmt.Errorf("%w: an empty partition name", ErrPartitionDefinition))
			return "", false
		}
		out[i] = a.Dialect().QuoteIdent(n)
	}
	return strings.Join(out, ", "), true
}

// DropPartitions drops RANGE or LIST partitions and every row in them.
func DropPartitions(names ...string) AlterClause {
	return partitionAction(func(a *sqlb.Args) string {
		list, ok := partitionNames(a, names)
		if !ok {
			return ""
		}
		return kwDropPartition + " " + list
	})
}

// TruncatePartitions deletes every row of the named partitions and keeps
// them.
func TruncatePartitions(names ...string) AlterClause {
	return partitionAction(func(a *sqlb.Args) string {
		list, ok := partitionNames(a, names)
		if !ok {
			return ""
		}
		return kwTruncatePartition + " " + list
	})
}

// ReorganizePartitions replaces the named partitions with new definitions,
// moving their rows: the way to split MAXVALUE's partition when a new year
// arrives.
func ReorganizePartitions(from []string, first PartitionDef, more ...PartitionDef) AlterClause {
	defs := append([]PartitionDef{first}, more...)
	return partitionAction(func(a *sqlb.Args) string {
		list, ok := partitionNames(a, from)
		if !ok {
			return ""
		}
		arity := defsArity(defs)
		if err := checkDefs(defs[0].kind, arity, defs); err != nil {
			a.Fail(err)
			return ""
		}
		parts := make([]string, len(defs))
		for i, def := range defs {
			parts[i] = writeDef(a, def, arity)
		}
		return kwReorganize + " " + list + " " + kwInto + " (" + strings.Join(parts, ", ") + ")"
	})
}

// CoalescePartitions removes n partitions from a HASH or KEY partitioning,
// redistributing their rows.
func CoalescePartitions(n int) AlterClause {
	return partitionAction(func(a *sqlb.Args) string {
		if n < 1 {
			a.Fail(fmt.Errorf("%w: coalesce at least one partition", ErrPartitionDefinition))
			return ""
		}
		return kwCoalescePartition + " " + strconv.Itoa(n)
	})
}

// RemovePartitioning makes the table unpartitioned, keeping every row.
func RemovePartitioning() AlterClause {
	return partitionAction(func(*sqlb.Args) string { return kwRemovePartitioning })
}

// The partition maintenance actions. Each is an ALTER TABLE action MySQL
// takes as the only action of its statement, and each names partitions of the
// table rather than columns of it.
//
// Four of them — ANALYZE, CHECK, OPTIMIZE and REPAIR PARTITION — report their
// result by returning a result set, one row per partition, exactly as CHECK
// TABLE does, and they do not fail when they find a problem. A runner that
// only executes them reports success whatever they found, so a runner must
// query them and read the Msg_type column; contrib/driver-mysql's
// TestDDL_MySQLPartitionMaintenance is the worked example. That is why they
// are not ddl.Statement.MustReturnNoRows, which means the opposite: those
// four always return rows.
const (
	kwExchangePartition = "EXCHANGE PARTITION"
	kwWithTable         = "WITH TABLE"
	kwWithoutValidation = "WITHOUT VALIDATION"
	kwRebuildPartition  = "REBUILD PARTITION"
	kwAnalyzePartition  = "ANALYZE PARTITION"
	kwCheckPartition    = "CHECK PARTITION"
	kwOptimizePartition = "OPTIMIZE PARTITION"
	kwRepairPartition   = "REPAIR PARTITION"
	kwAllPartitionsBare = "ALL"
)

// AllPartitions names every partition of the table in a maintenance action,
// MySQL's ALL keyword. It is a partition name a table cannot have, because a
// bare ALL is a keyword in that position and an identifier there is quoted,
// so the two cannot be confused.
const AllPartitions = kwAllPartitionsBare

// partitionList renders the names of a maintenance action's partitions,
// writing ALL bare for AllPartitions and quoting every other name.
func partitionList(a *sqlb.Args, names []string) (string, bool) {
	if len(names) == 1 && names[0] == AllPartitions {
		return kwAllPartitionsBare, true
	}
	return partitionNames(a, names)
}

// maintenance is an ALTER TABLE action over named partitions.
func maintenance(kw string, names []string) AlterClause {
	return partitionAction(func(a *sqlb.Args) string {
		list, ok := partitionList(a, names)
		if !ok {
			return ""
		}
		return kw + " " + list
	})
}

// RebuildPartitions rewrites the named partitions, which is how a partition
// left fragmented by a bulk delete is compacted without touching the rest of
// the table. Pass AllPartitions for every one.
func RebuildPartitions(names ...string) AlterClause { return maintenance(kwRebuildPartition, names) }

// AnalyzePartitions recomputes the key distribution of the named partitions,
// so the optimizer plans against current statistics after a bulk load. It
// returns a result set rather than failing; see the note above.
func AnalyzePartitions(names ...string) AlterClause { return maintenance(kwAnalyzePartition, names) }

// CheckPartitions looks for corruption in the named partitions. It reports
// what it finds in a result set rather than failing, which is the whole
// reason a runner must query it; see the note above.
func CheckPartitions(names ...string) AlterClause { return maintenance(kwCheckPartition, names) }

// OptimizePartitions rebuilds the named partitions and recomputes their
// statistics: REBUILD and ANALYZE in one pass. It returns a result set; see
// the note above.
func OptimizePartitions(names ...string) AlterClause { return maintenance(kwOptimizePartition, names) }

// RepairPartitions repairs corruption CheckPartitions found. It returns a
// result set; see the note above.
func RepairPartitions(names ...string) AlterClause { return maintenance(kwRepairPartition, names) }

// ExchangePartitionClause is EXCHANGE PARTITION ... WITH TABLE, which swaps a
// partition's rows with an unpartitioned table's in constant time, by
// exchanging the two tablespaces rather than moving rows.
//
// It is the statement behind archiving: load a month into a plain table, then
// exchange it into the partitioned one. MySQL requires the two to have the
// identical structure, and it validates, row by row, that every row of the
// table belongs in the partition — which is linear in the rows and is what
// WithoutValidation turns off.
type ExchangePartitionClause struct {
	name     string
	with     sqlb.Table
	validate bool
}

// ExchangePartition swaps the named partition with the unpartitioned table
// with, validating every row. The zero value of the clause has no partition
// name and fails Build with ErrEmptyName.
func ExchangePartition(name string, with sqlb.Table) ExchangePartitionClause {
	return ExchangePartitionClause{name: name, with: with, validate: true}
}

// WithoutValidation skips the per-row check that every row of the table
// belongs in the partition, which makes the exchange constant time on a large
// table. MySQL then stores rows that do not match the partition's bounds and
// no query will ever find them again, so this is for a table whose rows the
// caller has already constrained — the WHERE of the SELECT that built it.
func (x ExchangePartitionClause) WithoutValidation() ExchangePartitionClause {
	x.validate = false
	return x
}

// Standalone is true: MySQL takes an EXCHANGE PARTITION as the only action of
// its ALTER TABLE.
func (ExchangePartitionClause) Standalone() bool { return true }

// AlterClauseOf marks the action as MySQL's.
func (ExchangePartitionClause) AlterClauseOf(Dialect) {}

// NamedColumns is none: the action names partitions and a table, not columns.
func (ExchangePartitionClause) NamedColumns() []ddl.ColumnRef { return nil }

// WriteSQL renders the action.
func (x ExchangePartitionClause) WriteSQL(b *strings.Builder, a *sqlb.Args) {
	if x.name == "" || x.with.Name == "" {
		a.Fail(ErrEmptyName)
		return
	}
	d := a.Dialect()
	b.WriteString(kwExchangePartition + " " + d.QuoteIdent(x.name) + " " + kwWithTable + " " + x.with.Qualified(d))
	if !x.validate {
		b.WriteString(" " + kwWithoutValidation)
	}
}
