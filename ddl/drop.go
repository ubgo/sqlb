package ddl

import (
	"slices"
	"strings"

	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/render"
)

// DropTable is a DROP TABLE of one or more tables. It is complete from its
// constructor, which takes at least one table, so there is no incomplete form.
//
// It renders one statement per table on every engine. SQLite accepts one table
// per DROP TABLE and rejects a list with a syntax error, measured; one per
// table is valid everywhere, so the tables drop in the order given and the
// author orders them.
type DropTable[D render.Dialect] struct {
	dialect  D
	tables   []expr.Table
	ifExists bool
	clauses  []DropClause[D]
}

// NewDropTable starts a DROP TABLE for d.
func NewDropTable[D render.Dialect](d D, first expr.Table, more ...expr.Table) *DropTable[D] {
	return &DropTable[D]{dialect: d, tables: append([]expr.Table{first}, more...)}
}

func (s *DropTable[D]) derive() *DropTable[D] {
	c := *s
	c.tables = slices.Clone(s.tables)
	c.clauses = slices.Clone(s.clauses)
	return &c
}

// IfExists makes each statement do nothing when its table does not exist.
func (s *DropTable[D]) IfExists() *DropTable[D] {
	c := s.derive()
	c.ifExists = true
	return c
}

// Clause adds engine options typed to this statement's dialect, written after
// every table's name.
func (s *DropTable[D]) Clause(cs ...DropClause[D]) *DropTable[D] {
	c := s.derive()
	c.clauses = append(c.clauses, cs...)
	return c
}

// Build renders one statement per table.
func (s *DropTable[D]) Build() (Script, error) {
	if len(s.tables) == 0 {
		return nil, ErrZeroValue
	}
	for _, t := range s.tables {
		if t.Name == "" {
			return nil, ErrZeroValue
		}
	}
	a, err := inlineArgs(s.dialect)
	if err != nil {
		return nil, err
	}
	out := make(Script, 0, len(s.tables))
	for _, t := range s.tables {
		var b strings.Builder
		b.WriteString(kwDrop)
		b.WriteString(sepSpace)
		b.WriteString(kwTable)
		b.WriteString(sepSpace)
		if s.ifExists {
			b.WriteString(kwIfExists)
			b.WriteString(sepSpace)
		}
		b.WriteString(t.Qualified(a.Dialect()))
		for _, cl := range s.clauses {
			b.WriteString(sepSpace)
			cl.WriteSQL(&b, a)
		}
		out = append(out, Statement{SQL: b.String(), Transactional: true})
	}
	if err := a.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
