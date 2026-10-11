package expr

import (
	"strings"

	"github.com/ubgo/sqlb/render"
)

// Source is anything that can stand in FROM or be joined: a table, a derived
// table, or an engine-specific source such as VALUES, a set-returning
// function or a LATERAL body.
//
// Every source has an alias, because columns are referenced through the alias
// and Build checks alias uniqueness across every source in a statement.
type Source interface {
	SourceSQL(a *render.Args) string
	SourceAlias() string
}

// SourceSQL renders "name AS alias". Table satisfies Source.
func (t Table) SourceSQL(a *render.Args) string { return t.SQL(a.Dialect()) }

// SourceAlias returns the name this source is known by in the statement: the
// alias when there is one, otherwise the table's own name. Table satisfies
// Source.
//
// The fallback is what makes the duplicate-alias check mean something for
// unaliased tables. Returning "" for every one of them made two different
// tables collide under the empty key, and made a genuine self-join with no
// aliases — which really is ambiguous — indistinguishable from it.
func (t Table) SourceAlias() string {
	if t.Alias != "" {
		return t.Alias
	}
	return t.Name
}

// Ref returns a Table value that references a source by its alias only, for
// making Col values against a derived table, a CTE or a VALUES list:
//
//	t := sqlb.Derived(sub, "t")
//	n := sqlb.Col[int64](sqlb.Ref(t), "n")   // renders "t"."n"
//
// Name is the alias too, so Ref(t) used as a FROM source renders
// "alias" AS "alias", which is what a CTE reference needs.
func Ref(src Source) Table {
	return Table{Name: src.SourceAlias(), Alias: src.SourceAlias()}
}

// Statement is any statement that renders into a parent's Args: a Select used
// as a subquery or CTE body, or a data-modifying statement in a CTE.
type Statement interface {
	WriteSQL(b *strings.Builder, args *render.Args)
}

type derived struct {
	body  Statement
	alias string
}

// Derived makes a subquery a FROM source: "(SELECT ...) AS alias".
func Derived(body Statement, alias string) Source { return derived{body, alias} }

func (d derived) SourceSQL(a *render.Args) string {
	var b strings.Builder
	b.WriteString("(")
	d.body.WriteSQL(&b, a)
	b.WriteString(") ")
	b.WriteString(kwAs)
	b.WriteString(sepSpace)
	b.WriteString(a.Dialect().QuoteIdent(d.alias))
	return b.String()
}
func (d derived) SourceAlias() string { return d.alias }

// SourceOf makes a Source from a render function and an alias. Engine
// packages build VALUES lists, function sources and LATERAL bodies with it.
func SourceOf(alias string, render func(a *render.Args) string) Source {
	return sourceFunc{alias: alias, render: render}
}

type sourceFunc struct {
	alias  string
	render func(a *render.Args) string
}

func (s sourceFunc) SourceSQL(a *render.Args) string { return s.render(a) }
func (s sourceFunc) SourceAlias() string             { return s.alias }

// QuoteList renders identifiers comma-separated and quoted, for column lists.
func QuoteList(d render.Dialect, names []string) string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = d.QuoteIdent(n)
	}
	return strings.Join(out, sepComma)
}
