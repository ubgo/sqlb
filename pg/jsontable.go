package pg

import (
	"strings"

	"github.com/ubgo/sqlb"
)

const (
	constructJSONTable = "pg.JSONTable"

	kwJSONTable     = "JSON_TABLE"
	kwColumns       = "COLUMNS"
	kwForOrdinality = "FOR ORDINALITY"
	kwPath          = "PATH"
	kwDefault       = "DEFAULT"
	kwOnEmpty       = "ON EMPTY"
	kwOnError       = "ON ERROR"
	kwNestedPath    = "NESTED PATH"
	kwExistsCol     = "EXISTS"
)

// JSONTableColumn is one column of a JSON_TABLE (PG17). Type and Path are
// author constants, as the SQL grammar requires literals there. Ordinality
// makes it a FOR ORDINALITY counter and ignores Type and Path. Nested holds
// the columns of a NESTED PATH.
type JSONTableColumn struct {
	Name           string
	Type           string
	Path           string
	Ordinality     bool
	Exists         bool
	DefaultOnEmpty string
	DefaultOnError string
	Nested         *JSONTableNested
}

// JSONTableNested is a NESTED PATH with its own column list.
type JSONTableNested struct {
	Path    string
	Columns []JSONTableColumn
}

// JSONTable renders JSON_TABLE(doc, 'path' COLUMNS (...)) AS alias, a FROM
// source. Reference its columns with sqlb.Col against sqlb.Ref(source).
func JSONTable(doc sqlb.SQLer, path string, alias string, cols ...JSONTableColumn) sqlb.Source {
	return sqlb.SourceOf(alias, func(a *sqlb.Args) string {
		if !guard(a, constructJSONTable) {
			return ""
		}
		d := a.Dialect()
		return kwJSONTable + "(" + doc.SQL(a) + ", " + quoteLit(path).SQL(a) + " " + jsonTableColumns(a, cols) + ") AS " + d.QuoteIdent(alias)
	})
}

// jsonTableColumns renders the COLUMNS clause. It takes the statement's Args
// rather than only its dialect because every path is a literal, and a literal
// is written by the dialect's quoter, which records a failure on the Args.
func jsonTableColumns(a *sqlb.Args, cols []JSONTableColumn) string {
	d := a.Dialect()
	if len(cols) == 0 {
		a.Fail(ErrIncompleteSpec)
		return ""
	}
	parts := make([]string, len(cols))
	for i, c := range cols {
		switch {
		case c.Nested != nil && c.Nested.Path == "":
			a.Fail(ErrIncompleteSpec)
			return ""
		case c.Nested == nil && (c.Name == "" || (!c.Ordinality && c.Type == "")):
			a.Fail(ErrIncompleteSpec)
			return ""
		case c.Nested != nil:
			parts[i] = kwNestedPath + " " + quoteLit(c.Nested.Path).SQL(a) + " " + jsonTableColumns(a, c.Nested.Columns)
		case c.Ordinality:
			parts[i] = d.QuoteIdent(c.Name) + " " + kwForOrdinality
		case c.Exists:
			parts[i] = d.QuoteIdent(c.Name) + " " + c.Type + " " + kwExistsCol + " " + kwPath + " " + quoteLit(c.Path).SQL(a)
		default:
			s := d.QuoteIdent(c.Name) + " " + c.Type + " " + kwPath + " " + quoteLit(c.Path).SQL(a)
			if c.DefaultOnEmpty != "" {
				s += " " + kwDefault + " " + c.DefaultOnEmpty + " " + kwOnEmpty
			}
			if c.DefaultOnError != "" {
				s += " " + kwDefault + " " + c.DefaultOnError + " " + kwOnError
			}
			parts[i] = s
		}
	}
	return kwColumns + " (" + strings.Join(parts, ", ") + ")"
}
