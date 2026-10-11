package sqlite

import (
	"strconv"
	"strings"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
)

const (
	kwCreateVirtualTable = "CREATE VIRTUAL TABLE"
	kwUsingFTS5          = "USING fts5"
)

// Tokenizer is how an FTS5 table splits text into terms.
type Tokenizer struct{ spec string }

// Unicode61 is FTS5's default tokenizer, folding case for every script.
// removeDiacritics is 0, 1 or 2: 2 folds diacritics correctly for combining
// characters, which is what a search box wants.
func Unicode61(removeDiacritics int) Tokenizer {
	return Tokenizer{spec: "unicode61 remove_diacritics " + strconv.Itoa(removeDiacritics)}
}

// ASCII folds only ASCII case, and is faster.
func ASCII() Tokenizer { return Tokenizer{spec: "ascii"} }

// Trigram indexes every three-character sequence, which serves substring
// search and LIKE rather than word search.
func Trigram() Tokenizer { return Tokenizer{spec: "trigram"} }

// Porter stems English words over another tokenizer, so "running" matches
// "run".
func Porter(inner Tokenizer) Tokenizer { return Tokenizer{spec: "porter " + inner.spec} }

// CreateFTS5Query is CREATE VIRTUAL TABLE ... USING fts5 with no columns yet,
// which has no Build.
type CreateFTS5Query struct{ core ftsCore }

type ftsCore struct {
	t           sqlb.Table
	ifNotExists bool
	cols        []ddl.ColumnRef
	content     *sqlb.Table
	rowid       ddl.ColumnRef
	tokenize    string
	// tokenizeSet is true once Tokenize was called, so a Tokenizer{} with no
	// spec is refused rather than read as "the default tokenizer".
	tokenizeSet bool
	prefixes    []int
}

// CreateFTS5 starts an FTS5 table t, whose columns are declared on t as for
// any table and queried with Match, Rank and BM25.
func CreateFTS5(t sqlb.Table) *CreateFTS5Query { return &CreateFTS5Query{core: ftsCore{t: t}} }

// IfNotExists makes creating an existing FTS5 table a no-op.
func (x *CreateFTS5Query) IfNotExists() *CreateFTS5Query {
	c := x.core
	c.ifNotExists = true
	return &CreateFTS5Query{core: c}
}

// Columns gives the indexed columns, at least one, declared on the FTS5 table.
func (x *CreateFTS5Query) Columns(first ddl.ColumnRef, more ...ddl.ColumnRef) *CreateFTS5Statement {
	c := x.core
	c.cols = append([]ddl.ColumnRef{first}, more...)
	return &CreateFTS5Statement{core: c}
}

// CreateFTS5Statement is a complete CREATE VIRTUAL TABLE ... USING fts5.
type CreateFTS5Statement struct{ core ftsCore }

func (x *CreateFTS5Statement) derive() ftsCore {
	c := x.core
	c.cols = append([]ddl.ColumnRef(nil), x.core.cols...)
	c.prefixes = append([]int(nil), x.core.prefixes...)
	return c
}

// ExternalContent makes the index read its text from content, so the text is
// stored once; rowid is content's integer key, which the FTS5 rowid mirrors.
// The index is kept in step by triggers the author writes, or by the
// 'rebuild' command.
func (x *CreateFTS5Statement) ExternalContent(content sqlb.Table, rowid ddl.ColumnRef) *CreateFTS5Statement {
	c := x.derive()
	c.content, c.rowid = &content, rowid
	return &CreateFTS5Statement{core: c}
}

// Tokenize picks the tokenizer.
func (x *CreateFTS5Statement) Tokenize(t Tokenizer) *CreateFTS5Statement {
	c := x.derive()
	c.tokenize, c.tokenizeSet = t.spec, true
	return &CreateFTS5Statement{core: c}
}

// Prefix adds prefix indexes of the given lengths, which make a prefix query
// ("abc*") fast.
func (x *CreateFTS5Statement) Prefix(lengths ...int) *CreateFTS5Statement {
	c := x.derive()
	c.prefixes = append(c.prefixes, lengths...)
	return &CreateFTS5Statement{core: c}
}

// IfNotExists makes creating an existing FTS5 table a no-op.
func (x *CreateFTS5Statement) IfNotExists() *CreateFTS5Statement {
	c := x.derive()
	c.ifNotExists = true
	return &CreateFTS5Statement{core: c}
}

// Build renders the statement.
func (x *CreateFTS5Statement) Build() (ddl.Script, error) {
	c := x.core
	for _, r := range c.cols {
		if r.Name() == "" {
			return nil, ddl.ErrNotAColumn
		}
		if !onTable(r, c.t) {
			return nil, ddl.ErrForeignColumn
		}
	}
	if c.content != nil && (c.rowid == nil || !onTable(c.rowid, *c.content)) {
		return nil, ddl.ErrForeignColumn
	}
	return buildOne(c.t.Name, func(a *sqlb.Args) string {
		d := a.Dialect()
		args := make([]string, 0, len(c.cols)+4)
		for _, r := range c.cols {
			args = append(args, d.QuoteIdent(r.Name()))
		}
		if c.content != nil {
			args = append(args, "content = "+a.Literal(c.content.Name), "content_rowid = "+a.Literal(c.rowid.Name()))
		}
		if c.tokenizeSet && c.tokenize == "" {
			a.Fail(ddl.ErrZeroValue)
		}
		if c.tokenize != "" {
			args = append(args, "tokenize = "+a.Literal(c.tokenize))
		}
		if len(c.prefixes) > 0 {
			ps := make([]string, len(c.prefixes))
			for i, p := range c.prefixes {
				ps[i] = strconv.Itoa(p)
			}
			args = append(args, "prefix = "+a.Literal(strings.Join(ps, " ")))
		}
		return kwCreateVirtualTable + ifNotExists(c.ifNotExists) + " " + c.t.Qualified(d) + " " + kwUsingFTS5 + "(" + strings.Join(args, ", ") + ")"
	})
}
