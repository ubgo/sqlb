package pg

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
)

const (
	kwCreateIndex  = "CREATE INDEX"
	kwCreateUnique = "CREATE UNIQUE INDEX"
	kwConcurrently = "CONCURRENTLY"
	kwInclude      = "INCLUDE"
	kwWith         = "WITH"
	kwAlterIndex   = "ALTER INDEX"
	kwDropIndex    = "DROP INDEX"
	kwDesc         = "DESC"
	kwAsc          = "ASC"
	kwComment      = "COMMENT ON"
	kwIs           = "IS"
	kwNull         = "NULL"
)

// Index is a Postgres index, declared once as a value and named by every
// statement that creates, renames, drops or comments on it.
type Index struct{ Schema, Name string }

// IndexMethod is an index access method. It is an open set on purpose: an
// extension adds one, vector.HNSW, without editing this package.
type IndexMethod string

// The access methods Postgres ships.
const (
	BTree  IndexMethod = "btree"
	Hash   IndexMethod = "hash"
	GiST   IndexMethod = "gist"
	SPGiST IndexMethod = "spgist"
	GIN    IndexMethod = "gin"
	BRIN   IndexMethod = "brin"
)

// OpClass is an operator class, typed to the Go type of the column it indexes,
// so jsonb_path_ops on a text column does not compile. An extension makes its
// own with NewOpClass.
type OpClass[T any] struct{ name string }

// NewOpClass makes an operator class for columns of Go type T.
func NewOpClass[T any](name string) OpClass[T] { return OpClass[T]{name: name} }

// NullableOps is the same operator class for a nullable column of the same
// type, a *string where the class was made for string. It is a function rather
// than a method because a method on OpClass[T] returning OpClass[*T] is an
// instantiation cycle Go refuses.
func NullableOps[T any](o OpClass[T]) OpClass[*T] { return OpClass[*T](o) }

// The jsonb operator classes. JSONBPathOps is smaller and faster and serves
// only @>; JSONBOps, the default, also serves the key-existence operators.
var (
	JSONBPathOps = NewOpClass[json.RawMessage]("jsonb_path_ops")
	JSONBOps     = NewOpClass[json.RawMessage]("jsonb_ops")
)

// IndexParam is one storage parameter in WITH (...), an extension's tuning
// knob such as HNSW's m.
type IndexParam struct {
	name  string
	value int64
}

// NewIndexParam makes a storage parameter with an integer value, which is what
// every index parameter a library tunes takes.
func NewIndexParam(name string, value int64) IndexParam { return IndexParam{name: name, value: value} }

// IndexElement is one key of an index: a column or an expression, with its
// sort order and operator class. IndexKey and IndexExpr make them.
type IndexElement interface {
	WriteIndexElement(b *strings.Builder, a *sqlb.Args)
	// IndexColumns is the table's columns the element names, which the
	// statement checks belong to the indexed table.
	IndexColumns() []ddl.ColumnRef
}

// IndexKeyElem is a column or expression key.
type IndexKeyElem[T any] struct {
	e       sqlb.Expr[T]
	isExpr  bool
	opclass string
	// opclassSet is true once Opclass was called, so an OpClass[T]{} with no
	// name is refused rather than read as "no operator class".
	opclassSet bool
	// opclassParams are the operator class's own parameters, which an
	// extension's index method takes and nothing else can pass.
	opclassParams []StorageParameter
	dir           string
	nulls         string
}

// IndexKey indexes a column.
func IndexKey[T any](col sqlb.Expr[T]) IndexKeyElem[T] { return IndexKeyElem[T]{e: col} }

// IndexExpr indexes an expression, lower(email) say; a query uses the index
// only when it filters on the same expression.
func IndexExpr[T any](e sqlb.Expr[T]) IndexKeyElem[T] { return IndexKeyElem[T]{e: e, isExpr: true} }

// Desc sorts the key descending.
func (k IndexKeyElem[T]) Desc() IndexKeyElem[T] { k.dir = kwDesc; return k }

// Asc sorts the key ascending, the default, written out.
func (k IndexKeyElem[T]) Asc() IndexKeyElem[T] { k.dir = kwAsc; return k }

// NullsFirst sorts NULLs first.
func (k IndexKeyElem[T]) NullsFirst() IndexKeyElem[T] { k.nulls = kwNullsFirst; return k }

// NullsLast sorts NULLs last.
func (k IndexKeyElem[T]) NullsLast() IndexKeyElem[T] { k.nulls = kwNullsLast; return k }

// Opclass gives the key an operator class for its Go type.
func (k IndexKeyElem[T]) Opclass(o OpClass[T]) IndexKeyElem[T] {
	k.opclass, k.opclassSet = o.name, true
	return k
}

// WriteIndexElement renders the key, its columns bare.
func (k IndexKeyElem[T]) WriteIndexElement(b *strings.Builder, a *sqlb.Args) {
	sql := a.Bare(func() string { return k.e.SQL(a) })
	if k.isExpr {
		sql = "(" + sql + ")"
	} else if k.e.Name() == "" {
		a.Fail(ddl.ErrNotAColumn)
		return
	}
	b.WriteString(sql)
	if k.opclassSet && k.opclass == "" {
		a.Fail(ddl.ErrZeroValue)
		return
	}
	opclass := quoteOpt(a, k.opclass)
	if opclass != "" && len(k.opclassParams) > 0 {
		opclass += " (" + strings.Join(parameterList(a, k.opclassParams), ", ") + ")"
	}
	for _, s := range []string{opclass, k.dir, k.nulls} {
		if s != "" {
			b.WriteString(" " + s)
		}
	}
}

func quoteOpt(a *sqlb.Args, name string) string {
	if name == "" {
		return ""
	}
	return a.Dialect().QuoteIdent(name)
}

// IndexColumns is the column a column key names; an expression key names
// none that can be checked.
func (k IndexKeyElem[T]) IndexColumns() []ddl.ColumnRef {
	if k.isExpr {
		return nil
	}
	return []ddl.ColumnRef{k.e}
}

// indexCore is what CREATE INDEX and its complete form share.
type indexCore struct {
	ix                    Index
	t                     sqlb.Table
	unique, ifNotExists   bool
	concurrently, nullsND bool
	// onlyTable writes ON ONLY, which creates a partitioned table's index
	// without building it on the partitions.
	onlyTable bool
	method    IndexMethod
	keys      []IndexElement
	include   []ddl.ColumnRef
	params    []IndexParam
	where     sqlb.Predicate
	hasWhere  bool
}

func (c indexCore) derive() indexCore {
	c.keys = slices.Clone(c.keys)
	c.include = slices.Clone(c.include)
	c.params = slices.Clone(c.params)
	return c
}

// CreateIndexQuery is a CREATE INDEX with no key yet, which has no Build.
type CreateIndexQuery struct{ core indexCore }

// CreateIndexReady is a CREATE INDEX with at least one key.
type CreateIndexReady struct{ core indexCore }

// CreateIndex starts an index ix on table t.
func CreateIndex(ix Index, t sqlb.Table) *CreateIndexQuery {
	return &CreateIndexQuery{core: indexCore{ix: ix, t: t}}
}

// Unique makes the index refuse duplicate keys.
func (x *CreateIndexQuery) Unique() *CreateIndexQuery {
	c := x.core.derive()
	c.unique = true
	return &CreateIndexQuery{core: c}
}

// IfNotExists makes creating an existing index a no-op.
func (x *CreateIndexQuery) IfNotExists() *CreateIndexQuery {
	c := x.core.derive()
	c.ifNotExists = true
	return &CreateIndexQuery{core: c}
}

// Concurrently builds without blocking writes; see CreateIndexReady.Concurrently.
func (x *CreateIndexQuery) Concurrently() *CreateIndexQuery {
	c := x.core.derive()
	c.concurrently = true
	return &CreateIndexQuery{core: c}
}

// Using picks the access method; btree when not given.
func (x *CreateIndexQuery) Using(m IndexMethod) *CreateIndexQuery {
	c := x.core.derive()
	c.method = m
	return &CreateIndexQuery{core: c}
}

// On gives the keys, at least one, and so completes the statement.
func (x *CreateIndexQuery) On(first IndexElement, more ...IndexElement) *CreateIndexReady {
	c := x.core.derive()
	c.keys = append(append(c.keys, first), more...)
	return &CreateIndexReady{core: c}
}

// Unique makes the index refuse duplicate keys.
func (x *CreateIndexReady) Unique() *CreateIndexReady {
	c := x.core.derive()
	c.unique = true
	return &CreateIndexReady{core: c}
}

// IfNotExists makes creating an existing index a no-op. It checks the name
// only: an index of that name on other columns is left as it is.
func (x *CreateIndexReady) IfNotExists() *CreateIndexReady {
	c := x.core.derive()
	c.ifNotExists = true
	return &CreateIndexReady{core: c}
}

// Concurrently builds the index without blocking writes. Postgres refuses it
// inside a transaction, so the Statement is marked non-transactional and a
// runner must execute it on its own; a failed concurrent build leaves an
// invalid index that must be dropped before it is tried again.
func (x *CreateIndexReady) Concurrently() *CreateIndexReady {
	c := x.core.derive()
	c.concurrently = true
	return &CreateIndexReady{core: c}
}

// Using picks the access method; btree when not given.
func (x *CreateIndexReady) Using(m IndexMethod) *CreateIndexReady {
	c := x.core.derive()
	c.method = m
	return &CreateIndexReady{core: c}
}

// Include adds non-key columns, so a query reading only those is answered from
// the index alone.
func (x *CreateIndexReady) Include(cols ...ddl.ColumnRef) *CreateIndexReady {
	c := x.core.derive()
	c.include = append(c.include, cols...)
	return &CreateIndexReady{core: c}
}

// With sets storage parameters.
func (x *CreateIndexReady) With(ps ...IndexParam) *CreateIndexReady {
	c := x.core.derive()
	c.params = append(c.params, ps...)
	return &CreateIndexReady{core: c}
}

// Where makes the index partial: only rows matching p are indexed, and a query
// uses it only when its own filter implies p.
func (x *CreateIndexReady) Where(p sqlb.Predicate) *CreateIndexReady {
	c := x.core.derive()
	c.where, c.hasWhere = p, true
	return &CreateIndexReady{core: c}
}

// NullsNotDistinct makes a unique index treat NULLs as equal. Postgres 15 or
// later.
func (x *CreateIndexReady) NullsNotDistinct() *CreateIndexReady {
	c := x.core.derive()
	c.nullsND = true
	return &CreateIndexReady{core: c}
}

// Build renders the statement.
func (x *CreateIndexReady) Build() (ddl.Script, error) {
	c := x.core
	for _, k := range c.keys {
		for _, r := range k.IndexColumns() {
			if !onTable(r, c.t) {
				return nil, ddl.ErrForeignColumn
			}
		}
	}
	for _, r := range c.include {
		if r.Name() == "" {
			return nil, ddl.ErrNotAColumn
		}
		if !onTable(r, c.t) {
			return nil, ddl.ErrForeignColumn
		}
	}
	s, err := buildOne(c.ix.Name, !c.concurrently, func(a *sqlb.Args) string {
		d := a.Dialect()
		var b strings.Builder
		b.WriteString(kwCreateIndex)
		if c.unique {
			b.Reset()
			b.WriteString(kwCreateUnique)
		}
		if c.concurrently {
			b.WriteString(" " + kwConcurrently)
		}
		b.WriteString(ifNotExists(c.ifNotExists) + " " + d.QuoteIdent(c.ix.Name) + " " + kwOn)
		if c.onlyTable {
			b.WriteString(" " + kwOnly)
		}
		b.WriteString(" " + c.t.Qualified(d))
		if c.method != "" {
			b.WriteString(" " + kwUsing + " " + d.QuoteIdent(string(c.method)))
		}
		b.WriteString(" (")
		for i, k := range c.keys {
			if i > 0 {
				b.WriteString(", ")
			}
			k.WriteIndexElement(&b, a)
		}
		b.WriteString(")")
		if len(c.include) > 0 {
			b.WriteString(" " + kwInclude + " " + quoteCols(a, c.include))
		}
		if c.nullsND {
			b.WriteString(" " + kwNullsNotDistinct)
		}
		if len(c.params) > 0 {
			ps := make([]string, len(c.params))
			for i, p := range c.params {
				if p.name == "" {
					a.Fail(ddl.ErrZeroValue)
				}
				ps[i] = d.QuoteIdent(p.name) + " = " + a.Literal(p.value)
			}
			b.WriteString(" " + kwWith + " (" + strings.Join(ps, ", ") + ")")
		}
		if c.hasWhere {
			b.WriteString(" " + kwWhere + " " + a.Bare(func() string {
				var w strings.Builder
				c.where.WriteSQL(&w, a)
				return w.String()
			}))
		}
		return b.String()
	})
	return s, err
}

// onTable reports whether a column belongs to t, by schema and name.
func onTable(r ddl.ColumnRef, t sqlb.Table) bool {
	return r.Table().Name == t.Name && r.Table().Schema == t.Schema
}

// RenameIndexStatement is ALTER INDEX ... RENAME TO.
type RenameIndexStatement struct {
	from, to Index
	ifExists bool
}

// RenameIndex renames an index; its schema is unchanged.
func RenameIndex(from, to Index) *RenameIndexStatement {
	return &RenameIndexStatement{from: from, to: to}
}

// IfExists makes renaming a missing index a no-op.
func (x *RenameIndexStatement) IfExists() *RenameIndexStatement {
	c := *x
	c.ifExists = true
	return &c
}

// Build renders the statement.
func (x *RenameIndexStatement) Build() (ddl.Script, error) {
	if x.to.Name == "" {
		return nil, ErrEmptyName
	}
	return buildOne(x.from.Name, true, func(a *sqlb.Args) string {
		d := a.Dialect()
		return kwAlterIndex + ifExists(x.ifExists) + " " + qualified(d, x.from.Schema, x.from.Name) + " " + kwRenameTo + " " + d.QuoteIdent(x.to.Name)
	})
}

// DropIndexStatement is DROP INDEX.
type DropIndexStatement struct {
	ixs                             []Index
	ifExists, concurrently, cascade bool
}

// DropIndex drops one or more indexes, one statement each, since DROP INDEX
// CONCURRENTLY takes one index.
func DropIndex(first Index, more ...Index) *DropIndexStatement {
	return &DropIndexStatement{ixs: append([]Index{first}, more...)}
}

// IfExists makes dropping a missing index a no-op.
func (x *DropIndexStatement) IfExists() *DropIndexStatement {
	c := *x
	c.ixs = slices.Clone(x.ixs)
	c.ifExists = true
	return &c
}

// Concurrently drops without blocking reads and writes; not in a transaction.
func (x *DropIndexStatement) Concurrently() *DropIndexStatement {
	c := *x
	c.ixs = slices.Clone(x.ixs)
	c.concurrently = true
	return &c
}

// Cascade also drops what depends on the index, a constraint using it.
func (x *DropIndexStatement) Cascade() *DropIndexStatement {
	c := *x
	c.ixs = slices.Clone(x.ixs)
	c.cascade = true
	return &c
}

// Build renders one statement per index.
func (x *DropIndexStatement) Build() (ddl.Script, error) {
	if len(x.ixs) == 0 {
		return nil, ddl.ErrZeroValue
	}
	var out ddl.Script
	for _, ix := range x.ixs {
		s, err := buildOne(ix.Name, !x.concurrently, func(a *sqlb.Args) string {
			kw := kwDropIndex
			if x.concurrently {
				kw += " " + kwConcurrently
			}
			return kw + ifExists(x.ifExists) + " " + qualified(a.Dialect(), ix.Schema, ix.Name) + cascade(x.cascade)
		})
		if err != nil {
			return nil, err
		}
		out = append(out, s...)
	}
	return out, nil
}

// The object kinds COMMENT ON writes, one per Commentable. A closed set, so
// each is a constant rather than a literal at its commentTarget: the kind is
// grammar, and a misspelling here would reach the server as a syntax error in
// SQL the caller never wrote.
const (
	kwObjIndex            = "INDEX"
	kwObjSchema           = "SCHEMA"
	kwObjExtension        = "EXTENSION"
	kwObjType             = "TYPE"
	kwObjTable            = "TABLE"
	kwObjColumn           = "COLUMN"
	kwObjConstraint       = "CONSTRAINT"
	kwObjPolicy           = "POLICY"
	kwObjDomain           = "DOMAIN"
	kwObjCollation        = "COLLATION"
	kwObjStatistics       = "STATISTICS"
	kwObjFunction         = "FUNCTION"
	kwObjTrigger          = "TRIGGER"
	kwObjView             = "VIEW"
	kwObjMaterializedView = "MATERIALIZED VIEW"
	kwObjSequence         = "SEQUENCE"
)

// Commentable is a declared object COMMENT ON can name. The object kind comes
// from the Go type, so the statement cannot say INDEX of a table; the set is
// closed to this package's object values and the two constructors below.
type Commentable interface {
	commentTarget(d sqlb.Dialect) (kind, qualified, bare string, err error)
}

func (ix Index) commentTarget(d sqlb.Dialect) (string, string, string, error) {
	return kwObjIndex, qualified(d, ix.Schema, ix.Name), ix.Name, nil
}
func (s Schema) commentTarget(d sqlb.Dialect) (string, string, string, error) {
	return kwObjSchema, d.QuoteIdent(s.Name), s.Name, nil
}
func (e Extension) commentTarget(d sqlb.Dialect) (string, string, string, error) {
	return kwObjExtension, d.QuoteIdent(e.Name), e.Name, nil
}
func (e Enum[T]) commentTarget(d sqlb.Dialect) (string, string, string, error) {
	return kwObjType, qualified(d, e.Schema, e.Name), e.Name, nil
}

type tableTarget struct{ t sqlb.Table }

func (x tableTarget) commentTarget(d sqlb.Dialect) (string, string, string, error) {
	return kwObjTable, x.t.Qualified(d), x.t.Name, nil
}

type columnTarget struct{ c ddl.ColumnRef }

func (x columnTarget) commentTarget(d sqlb.Dialect) (string, string, string, error) {
	return kwObjColumn, x.c.Table().Qualified(d) + "." + d.QuoteIdent(x.c.Name()), x.c.Name(), nil
}

// CommentQuery is COMMENT ON an object with no text yet, which has no Build.
type CommentQuery struct{ target Commentable }

// CommentOn starts a comment on a declared object: an Index, a Schema, an
// Extension or an Enum.
func CommentOn(o Commentable) *CommentQuery { return &CommentQuery{target: o} }

// CommentOnConstraint starts a comment on a declared table constraint:
// COMMENT ON CONSTRAINT name ON table.
func CommentOnConstraint(c TableConstraint) *CommentQuery {
	return &CommentQuery{target: constraintTarget{c}}
}

type constraintTarget struct{ c TableConstraint }

func (x constraintTarget) commentTarget(d sqlb.Dialect) (string, string, string, error) {
	if !x.c.Valid() {
		return kwObjConstraint, "", "", nil
	}
	return kwObjConstraint, d.QuoteIdent(x.c.Name) + " " + kwOn + " " + x.c.Table.Qualified(d), x.c.Name, nil
}

// CommentOnTable starts a comment on a table.
func CommentOnTable(t sqlb.Table) *CommentQuery { return &CommentQuery{target: tableTarget{t}} }

// CommentOnColumn starts a comment on a column.
func CommentOnColumn[T any](c sqlb.Expr[T]) *CommentQuery {
	return &CommentQuery{target: columnTarget{c}}
}

// CommentStatement is a complete COMMENT ON.
type CommentStatement struct {
	target Commentable
	text   *string
}

// Is sets the comment. The text is written as a literal by the dialect, so
// quotes and backslashes are stored exactly.
func (x *CommentQuery) Is(text string) *CommentStatement {
	return &CommentStatement{target: x.target, text: &text}
}

// IsNull removes the comment.
func (x *CommentQuery) IsNull() *CommentStatement { return &CommentStatement{target: x.target} }

// Build renders the statement.
func (x *CommentStatement) Build() (ddl.Script, error) {
	if x.target == nil {
		return nil, ddl.ErrZeroValue
	}
	kind, name, bare, err := x.target.commentTarget(Dialect{})
	if err != nil {
		return nil, err
	}
	if _, ok := x.target.(columnTarget); ok && bare == "" {
		return nil, ddl.ErrNotAColumn
	}
	return buildOne(bare, true, func(a *sqlb.Args) string {
		v := kwNull
		if x.text != nil {
			v = a.Literal(*x.text)
		}
		return kwComment + " " + kind + " " + name + " " + kwIs + " " + v
	})
}
