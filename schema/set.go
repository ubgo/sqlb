package schema

import (
	"errors"
	"fmt"
	"sort"
)

// Set is the schema of a whole database, assembled from packages that do not
// know about each other.
//
// This is the type the package exists for. A library contributes its tables, an
// application contributes its own, and the result can be validated and ordered
// as one thing. Neither side imports the other; they meet here, in a value both
// can construct.
//
// A Set is ordinary and copyable, and holds no hidden state: the same tables
// always produce the same ordering, so a migration planned on a developer's
// machine is the one that runs in CI.
type Set struct {
	tables map[string]Table
	owners map[string]string
}

// New builds a Set from one contribution. Most callers use Add afterwards.
func New(owner string, tables ...Table) (*Set, error) {
	s := &Set{tables: map[string]Table{}, owners: map[string]string{}}
	return s, s.Add(owner, tables...)
}

// ErrDuplicateTable reports that two contributors declared the same table name.
//
// It is an error rather than a last-write-wins merge because the two
// declarations are usually different, and silently picking one produces a
// database that matches neither package's expectations. Naming both owners is
// the whole value of the error: the fix is a prefix or a schema, and you cannot
// choose one without knowing who collided.
var ErrDuplicateTable = errors.New("schema: two packages declare the same table")

// Add contributes tables under an owner name, which is used only in errors.
//
// The owner is worth the parameter. When a migration fails because two modules
// both defined "users", the question is always which two, and a Set that did
// not record it can only report the name.
func (s *Set) Add(owner string, tables ...Table) error {
	if s.tables == nil {
		s.tables = map[string]Table{}
		s.owners = map[string]string{}
	}
	var errs []error
	for _, t := range tables {
		key := t.qualifiedName()
		if prev, dup := s.owners[key]; dup {
			errs = append(errs, fmt.Errorf("%w: %q declared by both %q and %q", ErrDuplicateTable, key, prev, owner))
			continue
		}
		s.tables[key] = t
		s.owners[key] = owner
	}
	return errors.Join(errs...)
}

// qualifiedName is the key a table is stored under. The schema is part of it, so
// two packages may each own a "users" table as long as they put them in
// different schemas, which is the intended way to resolve a collision.
func (t Table) qualifiedName() string {
	if t.Schema == "" {
		return t.Name
	}
	return t.Schema + "." + t.Name
}

// Tables returns every table, ordered by name. Use Sorted when the order has to
// respect foreign keys.
func (s Set) Tables() []Table {
	out := make([]Table, 0, len(s.tables))
	for _, k := range s.keys() {
		out = append(out, s.tables[k])
	}
	return out
}

// Owner reports which contributor declared a table.
func (s Set) Owner(qualifiedName string) (string, bool) {
	o, ok := s.owners[qualifiedName]
	return o, ok
}

func (s Set) keys() []string {
	out := make([]string, 0, len(s.tables))
	for k := range s.tables {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ErrMissingTable reports a foreign key pointing at a table nobody contributed.
//
// This is the error that makes assembling a schema worth doing at all. A
// library's foreign key names its target by string precisely so the library
// need not import whoever owns it, and the cost of that freedom is that the
// reference is unchecked until the pieces are put together. Here is where it
// gets checked.
var ErrMissingTable = errors.New("schema: foreign key references a table no package contributed")

// ErrCycle reports a circular foreign key dependency.
var ErrCycle = errors.New("schema: foreign keys form a cycle, so no creation order exists")

// Validate checks every table and every cross-table reference.
func (s Set) Validate() error {
	var errs []error
	for _, k := range s.keys() {
		t := s.tables[k]
		if err := t.Validate(); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", s.owners[k], err))
		}
		for _, fk := range t.ForeignKeys {
			ref, ok := s.tables[fk.RefTable]
			if !ok {
				errs = append(errs, fmt.Errorf("%w: %q on %q references %q",
					ErrMissingTable, fk.Name, k, fk.RefTable))
				continue
			}
			for _, rc := range fk.RefColumns {
				if _, ok := ref.Column(rc); !ok {
					errs = append(errs, fmt.Errorf("schema: foreign key %q on %q references %s.%s, which is not declared",
						fk.Name, k, fk.RefTable, rc))
				}
			}
		}
	}
	return errors.Join(errs...)
}

// Sorted returns the tables in an order where every table comes after the ones
// it references, which is the order they can be created in.
//
// This is the question a migration runner actually has, and the reason two
// packages can be migrated together without either importing the other: the
// dependency is expressed as a name, and the ordering is computed here from all
// the names at once. Neither package could have worked it out alone.
//
// The sort is deterministic. Kahn's algorithm leaves genuine freedom in the
// order of independent tables, and resolving that freedom by name rather than
// by map iteration is what keeps a generated migration from changing shape
// between runs for no reason.
func (s Set) Sorted() ([]Table, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}

	deps := map[string]map[string]bool{}
	for _, k := range s.keys() {
		deps[k] = map[string]bool{}
		for _, fk := range s.tables[k].ForeignKeys {
			// A self-reference is legal and imposes no ordering: the table is
			// created once and the constraint points back into itself.
			if fk.RefTable != k {
				deps[k][fk.RefTable] = true
			}
		}
	}

	var out []Table
	done := map[string]bool{}
	for len(done) < len(deps) {
		progress := false
		for _, k := range s.keys() {
			if done[k] {
				continue
			}
			ready := true
			for d := range deps[k] {
				if !done[d] {
					ready = false
					break
				}
			}
			if ready {
				out = append(out, s.tables[k])
				done[k] = true
				progress = true
			}
		}
		if !progress {
			return nil, fmt.Errorf("%w: among %v", ErrCycle, remaining(s.keys(), done))
		}
	}
	return out, nil
}

// remaining lists the tables a cycle left unplaced, so the error names the
// suspects rather than announcing that one exists.
func remaining(keys []string, done map[string]bool) []string {
	var out []string
	for _, k := range keys {
		if !done[k] {
			out = append(out, k)
		}
	}
	return out
}
