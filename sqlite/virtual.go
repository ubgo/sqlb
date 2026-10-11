package sqlite

import (
	"errors"
	"regexp"
	"strings"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
)

// CREATE VIRTUAL TABLE for the modules this package does not have a builder
// for, and for geopoly.
//
// FTS5 and R*Tree have builders of their own, in fts.go and rtree.go, because
// their arguments are a grammar worth typing: a tokenizer, a prefix index, one
// to five dimensions. Every other module is a different problem. A virtual
// table's arguments are not SQL at all — SQLite hands the text inside the
// parentheses to the module, which parses it however it likes — so a builder
// for a module this package has never heard of could only pretend to check
// something. CreateVirtualTable is therefore the honest shape: the module's
// name, and its arguments written out as the module's own language.
//
// geopoly gets its own constructor rather than being left to that escape
// hatch, because its arguments are ordinary column names and so can be checked
// like any other DDL: a column named in CreateGeopoly must be declared on the
// table being created, or Build refuses it.

const (
	// kwUsing opens the module clause of CREATE VIRTUAL TABLE.
	kwUsing = "USING"
)

// Module is the name of a SQLite virtual-table module.
//
// It is a named type with constants for the modules SQLite ships, and still a
// string underneath, because the set cannot be closed: a module can be
// compiled in or loaded at run time, so an exhaustive list would be a claim
// about somebody else's build. What the type buys is that the module name and
// its arguments cannot be confused for one another at a call site.
type Module string

// The modules built into a stock SQLite. ModuleFTS5, ModuleRTree and
// ModuleRTreeI32 have typed builders in this package and are named here only
// for the reader; reaching for them through CreateVirtualTable gives up the
// checking those builders do.
const (
	ModuleFTS5     Module = "fts5"
	ModuleRTree    Module = "rtree"
	ModuleRTreeI32 Module = "rtree_i32"
	ModuleGeopoly  Module = "geopoly"
	ModuleDBStat   Module = "dbstat"
)

// ErrNoModule is a CREATE VIRTUAL TABLE with no module name.
var ErrNoModule = errors.New("sqlb/sqlite: a virtual table needs a module name")

// ErrModuleName is a module name that is not a bare identifier.
//
// The name is written into the statement verbatim, since no engine takes a
// placeholder there and quoting it would change which module SQLite looks for.
// That makes it the one piece of a virtual table that could carry text out of
// its position, so anything but letters, digits and underscores is refused
// here rather than rendered.
var ErrModuleName = errors.New("sqlb/sqlite: a module name must be letters, digits and underscores")

// moduleName is the shape ErrModuleName enforces.
var moduleName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// CreateVirtualTableStatement is a complete CREATE VIRTUAL TABLE for a module
// this package has no builder for, or for geopoly.
type CreateVirtualTableStatement struct {
	t           sqlb.Table
	module      Module
	args        []string
	cols        []ddl.ColumnRef
	ifNotExists bool
}

// CreateVirtualTable creates table t with module m and the arguments args.
//
// The arguments are the module's own language and are not checked, not quoted
// and not escaped: SQLite passes the text between the parentheses to the
// module, which may read it as column declarations (fts5), as coordinates
// (rtree), as a filename, or as anything else it chose. They are written out
// exactly as given, joined with commas, and they are authored text like every
// other value in ddl — never a value that came from a request, which is the
// one thing that cannot be made safe here.
//
// With no arguments the statement is USING m with no parentheses, which is the
// form SQLite's own documentation writes for a module that takes none;
// measured, USING m() is accepted too.
//
// Prefer CreateFTS5, CreateRTree and CreateGeopoly where they apply. This is
// the escape hatch for the rest, including a module loaded at run time, and it
// exists so that a module nobody here has heard of does not mean giving up the
// builder and writing the whole statement as a string.
func CreateVirtualTable(t sqlb.Table, m Module, args ...string) *CreateVirtualTableStatement {
	return &CreateVirtualTableStatement{t: t, module: m, args: append([]string(nil), args...)}
}

// CreateGeopoly creates a geopoly table: SQLite's polygon index, which answers
// "which shapes contain this point" and "which overlap this box" without
// scanning.
//
// The columns are the table's own, declared on t as for any table, and each is
// checked to belong to it. They are the extra columns stored beside the shape;
// geopoly adds the hidden _shape column itself, which holds the polygon and is
// read and written through the geopoly_* functions rather than declared here.
// A geopoly table with no extra columns at all is legal and measured.
//
// The module is compiled into the modernc driver contrib/driver-sqlite uses;
// with another build, check it is there, as CREATE VIRTUAL TABLE fails with
// "no such module: geopoly" when it is not.
func CreateGeopoly(t sqlb.Table, cols ...ddl.ColumnRef) *CreateVirtualTableStatement {
	return &CreateVirtualTableStatement{t: t, module: ModuleGeopoly, cols: append([]ddl.ColumnRef(nil), cols...)}
}

// IfNotExists makes creating an existing virtual table a no-op.
func (x *CreateVirtualTableStatement) IfNotExists() *CreateVirtualTableStatement {
	c := *x
	c.args = append([]string(nil), x.args...)
	c.cols = append([]ddl.ColumnRef(nil), x.cols...)
	c.ifNotExists = true
	return &c
}

// Build renders the statement.
func (x *CreateVirtualTableStatement) Build() (ddl.Script, error) {
	if x.module == "" {
		return nil, ErrNoModule
	}
	if !moduleName.MatchString(string(x.module)) {
		return nil, ErrModuleName
	}
	for _, r := range x.cols {
		if r == nil || r.Name() == "" {
			return nil, ddl.ErrNotAColumn
		}
		if !onTable(r, x.t) {
			return nil, ddl.ErrForeignColumn
		}
	}
	return buildOne(x.t.Name, func(a *sqlb.Args) string {
		d := a.Dialect()
		args := make([]string, 0, len(x.args)+len(x.cols))
		for _, r := range x.cols {
			args = append(args, d.QuoteIdent(r.Name()))
		}
		args = append(args, x.args...)
		out := kwCreateVirtualTable + ifNotExists(x.ifNotExists) + " " + x.t.Qualified(d) + " " +
			kwUsing + " " + string(x.module)
		if len(args) == 0 {
			return out
		}
		return out + "(" + strings.Join(args, ", ") + ")"
	})
}
