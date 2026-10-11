package vet_test

import (
	"strings"
	"testing"

	"github.com/ubgo/sqlb/contrib/vet"
	"github.com/ubgo/sqlb/schema"
)

// TestCompareKeys_EdgeCases pins the matching rules on a catalogue literal:
// primary-key order matters; an index is matched by columns and the unique
// one is preferred when MySQL also holds its own foreign-key index on them; a
// reference with no schema is the default schema's table; an action compares
// case-insensitively with an unstated one as NO ACTION; an extra plain index
// is not reported; and a table the catalogue lacks is left to CompareTables.
func TestCompareKeys_EdgeCases(t *testing.T) {
	keys := vet.KeyCatalogue{DefaultSchema: "public", Tables: map[string]vet.TableKeys{
		"orders": {
			PrimaryKey: []string{"b", "a"},
			Indexes: []schema.Index{
				{Name: "orders_user_fk", Columns: []string{"user_id"}},
				{Name: "orders_user", Columns: []string{"user_id"}, Unique: true},
				{Name: "orders_extra", Columns: []string{"note"}},
			},
			ForeignKeys: []schema.ForeignKey{
				{Columns: []string{"user_id"}, RefTable: "public.users", RefColumns: []string{"id"}, OnDelete: "cascade", OnUpdate: schema.NoAction},
			},
		},
	}}
	desc := schema.Table{
		Name:       "orders",
		PrimaryKey: []string{"a", "b"},
		Indexes:    []schema.Index{{Columns: []string{"user_id"}, Unique: true}},
		ForeignKeys: []schema.ForeignKey{
			{Columns: []string{"user_id"}, RefTable: "users", RefColumns: []string{"id"}, OnDelete: schema.Cascade},
		},
	}
	got := vet.CompareKeys([]schema.Table{desc, {Name: "absent"}}, keys)
	if len(got) != 1 || !strings.Contains(got[0].Message, "primary key is (a, b), the database's is (b, a)") {
		t.Fatalf("got %v", got)
	}
	desc.PrimaryKey = []string{"b", "a"}
	if got := vet.CompareKeys([]schema.Table{desc}, keys); len(got) != 0 {
		t.Fatalf("a matching table reported: %v", got)
	}
	desc.ForeignKeys[0].RefTable = "billing.users"
	if got := vet.CompareKeys([]schema.Table{desc}, keys); len(got) != 1 || !strings.Contains(got[0].Message, "references billing.users(id) in the DDL") {
		t.Fatalf("a reference to another schema matched: %v", got)
	}
}

// TestCompareKeys_ReportsEachDifference pins every finding CompareKeys makes,
// one per case, against a catalogue that differs in exactly that way.
func TestCompareKeys_ReportsEachDifference(t *testing.T) {
	desc := schema.Table{
		Name:       "t",
		PrimaryKey: []string{"id"},
		Indexes:    []schema.Index{{Columns: []string{"a"}}, {Columns: []string{"b"}, Unique: true}},
		ForeignKeys: []schema.ForeignKey{
			{Columns: []string{"p"}, RefTable: "p", RefColumns: []string{"id"}, OnUpdate: schema.Cascade},
		},
	}
	good := vet.TableKeys{
		PrimaryKey:  []string{"id"},
		Indexes:     []schema.Index{{Columns: []string{"a"}}, {Columns: []string{"b"}, Unique: true}},
		ForeignKeys: []schema.ForeignKey{{Columns: []string{"p"}, RefTable: "main.p", RefColumns: []string{"id"}, OnUpdate: schema.Cascade}},
	}
	for name, c := range map[string]struct {
		edit func(*vet.TableKeys)
		want string
		warn bool
	}{
		"a plain index missing":     {func(k *vet.TableKeys) { k.Indexes = k.Indexes[1:] }, "the DDL's index on (a) is not in the database", false},
		"a unique index missing":    {func(k *vet.TableKeys) { k.Indexes = k.Indexes[:1] }, "the DDL's unique index on (b) is not in the database", false},
		"a unique index not unique": {func(k *vet.TableKeys) { k.Indexes[1].Unique = false }, "the DDL's unique index on (b) is not unique in the database", false},
		"a foreign key missing":     {func(k *vet.TableKeys) { k.ForeignKeys = nil }, "the DDL's foreign key on (p) is not in the database", false},
		"ON UPDATE differs":         {func(k *vet.TableKeys) { k.ForeignKeys[0].OnUpdate = schema.Restrict }, "is ON UPDATE CASCADE in the DDL and RESTRICT", false},
		"another referenced column": {func(k *vet.TableKeys) { k.ForeignKeys[0].RefColumns = []string{"code"} }, "references p(id) in the DDL and main.p(code)", false},
		"an extra foreign key": {func(k *vet.TableKeys) {
			k.ForeignKeys = append(k.ForeignKeys, schema.ForeignKey{Columns: []string{"q"}, RefTable: "main.q", RefColumns: []string{"id"}})
		}, "the database has a foreign key on (q) the DDL does not create", true},
		"an extra plain index is not reported": {func(k *vet.TableKeys) {
			k.Indexes = append(k.Indexes, schema.Index{Columns: []string{"z"}})
		}, "", false},
	} {
		k := good
		k.Indexes = append([]schema.Index(nil), good.Indexes...)
		k.ForeignKeys = append([]schema.ForeignKey(nil), good.ForeignKeys...)
		c.edit(&k)
		got := vet.CompareKeys([]schema.Table{desc}, vet.KeyCatalogue{DefaultSchema: "main", Tables: map[string]vet.TableKeys{"t": k}})
		if c.want == "" {
			if len(got) != 0 {
				t.Errorf("%s: %v", name, got)
			}
			continue
		}
		if len(got) != 1 || !strings.Contains(got[0].Message, c.want) || got[0].Warning != c.warn {
			t.Errorf("%s: got %v, want one finding containing %q (warning %v)", name, got, c.want, c.warn)
		}
	}
}

// TestReadKeys_SQLiteShapes reads the key shapes SQLite reports unusually,
// from a real database: REFERENCES with no column list, whose referenced
// column SQLite reports as NULL and which names the parent's primary key; an
// expression index and a partial index, which are skipped; and a composite
// key whose order is the key's, not the table's.
func TestReadKeys_SQLiteShapes(t *testing.T) {
	db := memorySQLite(t)
	for _, q := range []string{
		`CREATE TABLE p (b INTEGER, a INTEGER, PRIMARY KEY (a, b))`,
		`CREATE TABLE c (x INTEGER, y INTEGER, n TEXT, FOREIGN KEY (x, y) REFERENCES p ON DELETE CASCADE)`,
		`CREATE INDEX c_expr ON c (lower(n))`,
		`CREATE UNIQUE INDEX c_partial ON c (n) WHERE n IS NOT NULL`,
		`CREATE UNIQUE INDEX c_n ON c (n)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	keys, err := vet.ReadKeys(t.Context(), db, "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(keys.Tables["p"].PrimaryKey, ","); got != "a,b" {
		t.Errorf("composite key order %q", got)
	}
	c := keys.Tables["c"]
	if len(c.ForeignKeys) != 1 || strings.Join(c.ForeignKeys[0].RefColumns, ",") != "a,b" || c.ForeignKeys[0].OnDelete != schema.Cascade {
		t.Errorf("REFERENCES p with no columns: %+v", c.ForeignKeys)
	}
	if len(c.Indexes) != 1 || c.Indexes[0].Name != "c_n" {
		t.Errorf("expression and partial indexes must be skipped: %+v", c.Indexes)
	}
	if keys.Tables["main.c"].Indexes == nil {
		t.Error("c is not under main.c")
	}
}
