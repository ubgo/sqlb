package pg

import (
	"strings"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
)

const (
	kwCreateView      = "CREATE VIEW"
	kwCreateOrReplace = "CREATE OR REPLACE VIEW"
	kwAlterView       = "ALTER VIEW"
	kwDropView        = "DROP VIEW"
	kwCreateMatView   = "CREATE MATERIALIZED VIEW"
	kwRefreshMatView  = "REFRESH MATERIALIZED VIEW"
	kwDropMatView     = "DROP MATERIALIZED VIEW"
	kwWithNoData      = "WITH NO DATA"
	kwWithCheckOption = "CHECK OPTION"
	kwWithLocal       = "WITH LOCAL"
	kwWithCascaded    = "WITH CASCADED"
)

// View is a Postgres view.
type View struct{ Schema, Name string }

// MaterializedView is a Postgres materialized view: a view whose rows are
// stored, and read stale until refreshed.
type MaterializedView struct{ Schema, Name string }

func (v View) commentTarget(d sqlb.Dialect) (string, string, string, error) {
	return kwObjView, qualified(d, v.Schema, v.Name), v.Name, nil
}

func (v MaterializedView) commentTarget(d sqlb.Dialect) (string, string, string, error) {
	return kwObjMaterializedView, qualified(d, v.Schema, v.Name), v.Name, nil
}

func (s Sequence[T]) commentTarget(d sqlb.Dialect) (string, string, string, error) {
	return kwObjSequence, qualified(d, s.Schema, s.Name), s.Name, nil
}

// CheckOption is what a view's WITH CHECK OPTION checks: an INSERT or UPDATE
// through the view must produce a row the view shows.
type CheckOption int

// The check options. Local checks this view's condition; Cascaded also checks
// every view it is built on.
const (
	Local CheckOption = iota + 1
	Cascaded
)

func (o CheckOption) keyword() (string, bool) {
	switch o {
	case Local:
		return kwWithLocal, true
	case Cascaded:
		return kwWithCascaded, true
	}
	return "", false
}

// renderBody writes a view's query through the statement's inline Args, so
// every value it bound in the query builder is written as a literal.
func renderBody(a *sqlb.Args, body *SelectQuery) string {
	var b strings.Builder
	body.WriteSQL(&b, a)
	return b.String()
}

// CreateViewQuery is CREATE VIEW with no body yet, which has no Build.
type CreateViewQuery struct {
	v         View
	orReplace bool
}

// CreateView starts a view.
func CreateView(v View) *CreateViewQuery { return &CreateViewQuery{v: v} }

// OrReplace replaces an existing view of the name, which Postgres allows only
// when the new body keeps every existing column, in order, with its type. It
// is the idempotent form: Postgres has no CREATE VIEW IF NOT EXISTS.
func (x *CreateViewQuery) OrReplace() *CreateViewQuery { c := *x; c.orReplace = true; return &c }

// As gives the view's body, a Postgres SELECT built with the query builder,
// so a view is typed like any other query. Its bound values become literals.
func (x *CreateViewQuery) As(body *SelectQuery) *CreateViewStatement {
	return &CreateViewStatement{v: x.v, orReplace: x.orReplace, body: body}
}

// CreateViewStatement is a complete CREATE VIEW.
type CreateViewStatement struct {
	// columns names the view's output columns, options its reloptions, and
	// the two flags its modifiers; a recursive view must have columns, which
	// Build checks.
	columns              []string
	options              []ViewSetting
	temporary, recursive bool
	v                    View
	orReplace            bool
	body                 *SelectQuery
	check                CheckOption
}

// OrReplace replaces an existing view of the name.
func (x *CreateViewStatement) OrReplace() *CreateViewStatement {
	c := *x
	c.orReplace = true
	return &c
}

// WithCheckOption refuses a write through the view that the view would not
// show.
func (x *CreateViewStatement) WithCheckOption(o CheckOption) *CreateViewStatement {
	c := *x
	c.check = o
	return &c
}

// Build renders the statement.
func (x *CreateViewStatement) Build() (ddl.Script, error) {
	if x.recursive && len(x.columns) == 0 {
		return nil, ErrRecursiveColumns
	}
	return buildOne(x.v.Name, true, func(a *sqlb.Args) string {
		kw := kwCreateView
		if x.orReplace {
			kw = kwCreateOrReplace
		}
		// The modifiers go between OR REPLACE and VIEW, which is why the
		// head is assembled here rather than taken from one constant.
		if x.temporary || x.recursive {
			kw = strings.TrimSuffix(kw, " "+kwObjView)
			if x.temporary {
				kw += " " + kwTempView
			}
			if x.recursive {
				kw += " " + kwRecursive
			}
			kw += " " + kwObjView
		}
		s := kw + " " + qualified(a.Dialect(), x.v.Schema, x.v.Name) + writeColumnList(a, x.columns)
		if len(x.options) > 0 {
			s += " " + kwWith + " " + writeSettings(a, x.options)
		}
		s += " " + kwAs + " " + renderBody(a, x.body)
		if x.check != 0 {
			k, ok := x.check.keyword()
			s += " " + keywordOf(a, k, ok, "WithCheckOption") + " " + kwWithCheckOption
		}
		return s
	})
}

// RenameViewStatement is ALTER VIEW ... RENAME TO.
type RenameViewStatement struct {
	from, to View
	ifExists bool
}

// RenameView renames a view; its schema is unchanged.
func RenameView(from, to View) *RenameViewStatement { return &RenameViewStatement{from: from, to: to} }

// IfExists makes renaming a missing view a no-op.
func (x *RenameViewStatement) IfExists() *RenameViewStatement { c := *x; c.ifExists = true; return &c }

// Build renders the statement.
func (x *RenameViewStatement) Build() (ddl.Script, error) {
	if x.to.Name == "" {
		return nil, ErrEmptyName
	}
	return buildOne(x.from.Name, true, func(a *sqlb.Args) string {
		d := a.Dialect()
		return kwAlterView + ifExists(x.ifExists) + " " + qualified(d, x.from.Schema, x.from.Name) + " " + kwRenameTo + " " + d.QuoteIdent(x.to.Name)
	})
}

// DropView drops a view.
func DropView(v View) *DropObjectStatement {
	return &DropObjectStatement{kw: kwDropView, schema: v.Schema, name: v.Name}
}

// CreateMaterializedViewQuery is CREATE MATERIALIZED VIEW with no body yet.
type CreateMaterializedViewQuery struct {
	v           MaterializedView
	ifNotExists bool
}

// CreateMaterializedView starts a materialized view.
func CreateMaterializedView(v MaterializedView) *CreateMaterializedViewQuery {
	return &CreateMaterializedViewQuery{v: v}
}

// IfNotExists makes creating an existing materialized view a no-op.
func (x *CreateMaterializedViewQuery) IfNotExists() *CreateMaterializedViewQuery {
	c := *x
	c.ifNotExists = true
	return &c
}

// As gives the body.
func (x *CreateMaterializedViewQuery) As(body *SelectQuery) *CreateMaterializedViewStatement {
	return &CreateMaterializedViewStatement{v: x.v, ifNotExists: x.ifNotExists, body: body}
}

// CreateMaterializedViewStatement is a complete CREATE MATERIALIZED VIEW.
type CreateMaterializedViewStatement struct {
	// columns names the view's output columns, method its access method and
	// with the storage parameters of the table behind it.
	columns     []string
	method      AccessMethod
	with        []StorageParameter
	v           MaterializedView
	ifNotExists bool
	body        *SelectQuery
	noData      bool
}

// IfNotExists makes creating an existing materialized view a no-op.
func (x *CreateMaterializedViewStatement) IfNotExists() *CreateMaterializedViewStatement {
	c := *x
	c.ifNotExists = true
	return &c
}

// WithNoData creates it empty, so the install does not run the query; it
// cannot be read until the first refresh.
func (x *CreateMaterializedViewStatement) WithNoData() *CreateMaterializedViewStatement {
	c := *x
	c.noData = true
	return &c
}

// Build renders the statement.
func (x *CreateMaterializedViewStatement) Build() (ddl.Script, error) {
	return buildOne(x.v.Name, true, func(a *sqlb.Args) string {
		d := a.Dialect()
		s := kwCreateMatView + ifNotExists(x.ifNotExists) + " " + qualified(d, x.v.Schema, x.v.Name) + writeColumnList(a, x.columns)
		if x.method != "" {
			s += " " + kwUsingMethod + " " + d.QuoteIdent(string(x.method))
		}
		if len(x.with) > 0 {
			s += " " + writeWithParameters(a, x.with)
		}
		s += " " + kwAs + " " + renderBody(a, x.body)
		if x.noData {
			s += " " + kwWithNoData
		}
		return s
	})
}

// RefreshStatement is REFRESH MATERIALIZED VIEW.
type RefreshStatement struct {
	v            MaterializedView
	concurrently bool
}

// RefreshMaterializedView recomputes a materialized view, blocking reads of it
// while it runs.
func RefreshMaterializedView(v MaterializedView) *RefreshStatement { return &RefreshStatement{v: v} }

// Concurrently refreshes without blocking reads. It needs a unique index on
// the view and a view that has been populated once.
func (x *RefreshStatement) Concurrently() *RefreshStatement {
	c := *x
	c.concurrently = true
	return &c
}

// Build renders the statement.
func (x *RefreshStatement) Build() (ddl.Script, error) {
	return buildOne(x.v.Name, true, func(a *sqlb.Args) string {
		kw := kwRefreshMatView
		if x.concurrently {
			kw += " " + kwConcurrently
		}
		return kw + " " + qualified(a.Dialect(), x.v.Schema, x.v.Name)
	})
}

// DropMaterializedView drops a materialized view.
func DropMaterializedView(v MaterializedView) *DropObjectStatement {
	return &DropObjectStatement{kw: kwDropMatView, schema: v.Schema, name: v.Name}
}
