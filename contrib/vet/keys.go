package vet

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/ubgo/sqlb/render"
	"github.com/ubgo/sqlb/schema"
)

// TableKeys is a table's keys as the database has them: its primary key, its
// indexes on plain columns, and its foreign keys. An expression or partial
// index is left out, because schema.Index has no place for one and comparing
// its column list alone would call it something it is not. RefTable on a
// foreign key is always "schema.table".
type TableKeys struct {
	PrimaryKey  []string
	Indexes     []schema.Index
	ForeignKeys []schema.ForeignKey
}

// KeyCatalogue is every table's keys, keyed by CatalogueKey as Catalogue is,
// with the connection's default schema so a reference the DDL writes without
// one can be matched to the qualified name the database reports.
type KeyCatalogue struct {
	DefaultSchema string
	Tables        map[string]TableKeys
}

// columnSep joins column names in one catalogue row. It is the ASCII unit
// separator, which no identifier this module writes can contain, so a column
// named "a,b" stays one column.
const columnSep = "\x1f"

// The key catalogue queries, one per engine. Each was checked against its
// engine's own output on Postgres 17, MySQL 8.4 and SQLite 3.50.
const (
	// pgIndexQuery reads every index with its key columns in order: INCLUDE
	// columns (beyond indnkeyatts) are not key columns, an expression key is
	// attnum 0 and a partial index has indpred, and both are skipped.
	pgIndexQuery = `SELECT n.nspname, c.relname, i.indisprimary, i.indisunique,
			(SELECT string_agg(a.attname, chr(31) ORDER BY k.ord)
				FROM unnest(i.indkey::int2[]) WITH ORDINALITY AS k(attnum, ord)
				JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = k.attnum
				WHERE k.ord <= i.indnkeyatts),
			x.relname
		FROM pg_index i
		JOIN pg_class c ON c.oid = i.indrelid
		JOIN pg_class x ON x.oid = i.indexrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname <> 'information_schema' AND left(n.nspname, 3) <> 'pg_'
			AND i.indpred IS NULL AND NOT (0 = ANY (i.indkey::int2[]))`
	// pgForeignKeyQuery reads each foreign key once: a partition's copy of its
	// parent's key has conparentid set and is skipped.
	pgForeignKeyQuery = `SELECT n.nspname, c.relname, con.conname, rn.nspname, rc.relname,
			con.confdeltype, con.confupdtype,
			(SELECT string_agg(a.attname, chr(31) ORDER BY k.ord)
				FROM unnest(con.conkey) WITH ORDINALITY AS k(attnum, ord)
				JOIN pg_attribute a ON a.attrelid = con.conrelid AND a.attnum = k.attnum),
			(SELECT string_agg(a.attname, chr(31) ORDER BY k.ord)
				FROM unnest(con.confkey) WITH ORDINALITY AS k(attnum, ord)
				JOIN pg_attribute a ON a.attrelid = con.confrelid AND a.attnum = k.attnum)
		FROM pg_constraint con
		JOIN pg_class c ON c.oid = con.conrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		JOIN pg_class rc ON rc.oid = con.confrelid
		JOIN pg_namespace rn ON rn.oid = rc.relnamespace
		WHERE con.contype = 'f' AND con.conparentid = 0
			AND n.nspname <> 'information_schema' AND left(n.nspname, 3) <> 'pg_'`
	pgDefaultSchemaQuery = `SELECT current_schema()`

	// mysqlIndexQuery reads every index column in order; an expression key
	// part has no column_name and makes its index skipped.
	mysqlIndexQuery = `SELECT table_schema, table_name, index_name, non_unique = 0, column_name
		FROM information_schema.statistics
		WHERE table_schema NOT IN ('mysql', 'information_schema', 'performance_schema', 'sys')
		ORDER BY table_schema, table_name, index_name, seq_in_index`
	mysqlForeignKeyQuery = `SELECT k.table_schema, k.table_name, k.constraint_name, k.column_name,
			k.referenced_table_schema, k.referenced_table_name, k.referenced_column_name, r.delete_rule, r.update_rule
		FROM information_schema.key_column_usage k
		JOIN information_schema.referential_constraints r
			ON r.constraint_schema = k.constraint_schema AND r.constraint_name = k.constraint_name AND r.table_name = k.table_name
		WHERE k.referenced_table_name IS NOT NULL
			AND k.table_schema NOT IN ('mysql', 'information_schema', 'performance_schema', 'sys')
		ORDER BY k.table_schema, k.table_name, k.constraint_name, k.ordinal_position`
	mysqlDefaultSchemaQuery = `SELECT DATABASE()`
	// mysqlPrimaryIndex is the name MySQL gives every primary key's index.
	mysqlPrimaryIndex = "PRIMARY"

	sqliteKeyTablesQuery = `SELECT name FROM pragma_table_list WHERE schema = 'main' AND type = 'table' AND name NOT LIKE 'sqlite_%'`
	sqlitePKQuery        = `SELECT name FROM pragma_table_info(?) WHERE pk > 0 ORDER BY pk`
	// sqliteIndexListQuery skips the primary key's own index, which is the
	// primary key, and partial indexes.
	sqliteIndexListQuery = `SELECT name, "unique" FROM pragma_index_list(?) WHERE origin <> 'pk' AND partial = 0`
	sqliteIndexInfoQuery = `SELECT name FROM pragma_index_info(?) ORDER BY seqno`
	sqliteForeignKeyList = `SELECT id, "table", "from", "to", on_delete, on_update FROM pragma_foreign_key_list(?) ORDER BY id, seq`
)

// pgActions maps pg_constraint's action letters to schema's names.
var pgActions = map[string]schema.Action{
	"a": schema.NoAction, "r": schema.Restrict, "c": schema.Cascade, "n": schema.SetNull, "d": schema.SetDefault,
}

// ErrUnknownAction is returned when a catalogue reports a referential action
// this reader has no name for, rather than comparing it as something else.
var ErrUnknownAction = errors.New("sqlb vet: catalogue reports an unknown referential action")

// ReadKeys reads every user table's primary key, indexes and foreign keys,
// in every user schema. dialect is render.DialectPostgres,
// render.DialectSQLite or render.DialectMySQL.
func ReadKeys(ctx context.Context, db *sql.DB, dialect string) (KeyCatalogue, error) {
	switch dialect {
	case render.DialectPostgres:
		return readPGKeys(ctx, db)
	case render.DialectMySQL:
		return readMySQLKeys(ctx, db)
	case render.DialectSQLite:
		return readSQLiteKeys(ctx, db)
	}
	return KeyCatalogue{}, fmt.Errorf("sqlb vet: unknown dialect %q", dialect)
}

// keyBuilder collects a KeyCatalogue, storing a default-schema table under
// both keys as Catalogue.add does.
type keyBuilder struct {
	cat KeyCatalogue
}

func newKeyBuilder(defaultSchema string) *keyBuilder {
	return &keyBuilder{cat: KeyCatalogue{DefaultSchema: defaultSchema, Tables: map[string]TableKeys{}}}
}

func (k *keyBuilder) update(sch, table string, fn func(*TableKeys)) {
	key := CatalogueKey(sch, table)
	tk := k.cat.Tables[key]
	fn(&tk)
	k.cat.Tables[key] = tk
	if sch == k.cat.DefaultSchema {
		k.cat.Tables[table] = tk
	}
}

func splitCols(s sql.NullString) []string {
	if !s.Valid || s.String == "" {
		return nil
	}
	return strings.Split(s.String, columnSep)
}

func readPGKeys(ctx context.Context, db *sql.DB) (KeyCatalogue, error) {
	var def string
	if err := db.QueryRowContext(ctx, pgDefaultSchemaQuery).Scan(&def); err != nil {
		return KeyCatalogue{}, fmt.Errorf("sqlb vet: keys: %w", err)
	}
	kb := newKeyBuilder(def)
	err := eachRow(ctx, db, pgIndexQuery, nil, func(rows *sql.Rows) error {
		var sch, table, name string
		var primary, unique bool
		var cols sql.NullString
		if err := rows.Scan(&sch, &table, &primary, &unique, &cols, &name); err != nil {
			return err
		}
		kb.update(sch, table, func(tk *TableKeys) {
			if primary {
				tk.PrimaryKey = splitCols(cols)
				return
			}
			tk.Indexes = append(tk.Indexes, schema.Index{Name: name, Columns: splitCols(cols), Unique: unique})
		})
		return nil
	})
	if err != nil {
		return KeyCatalogue{}, err
	}
	err = eachRow(ctx, db, pgForeignKeyQuery, nil, func(rows *sql.Rows) error {
		var sch, table, name, rsch, rtable, del, upd string
		var cols, rcols sql.NullString
		if err := rows.Scan(&sch, &table, &name, &rsch, &rtable, &del, &upd, &cols, &rcols); err != nil {
			return err
		}
		onDel, ok1 := pgActions[del]
		onUpd, ok2 := pgActions[upd]
		if !ok1 || !ok2 {
			return fmt.Errorf("%w: %q, %q on %s", ErrUnknownAction, del, upd, name)
		}
		kb.update(sch, table, func(tk *TableKeys) {
			tk.ForeignKeys = append(tk.ForeignKeys, schema.ForeignKey{
				Name: name, Columns: splitCols(cols), RefTable: rsch + "." + rtable, RefColumns: splitCols(rcols),
				OnDelete: onDel, OnUpdate: onUpd,
			})
		})
		return nil
	})
	if err != nil {
		return KeyCatalogue{}, err
	}
	return kb.cat, nil
}

func readMySQLKeys(ctx context.Context, db *sql.DB) (KeyCatalogue, error) {
	var def sql.NullString
	if err := db.QueryRowContext(ctx, mysqlDefaultSchemaQuery).Scan(&def); err != nil {
		return KeyCatalogue{}, fmt.Errorf("sqlb vet: keys: %w", err)
	}
	kb := newKeyBuilder(def.String)
	type idx struct {
		sch, table, name string
		unique, expr     bool
		cols             []string
	}
	var order []string
	byKey := map[string]*idx{}
	err := eachRow(ctx, db, mysqlIndexQuery, nil, func(rows *sql.Rows) error {
		var sch, table, name string
		var unique bool
		var col sql.NullString
		if err := rows.Scan(&sch, &table, &name, &unique, &col); err != nil {
			return err
		}
		k := sch + "\x00" + table + "\x00" + name
		x, ok := byKey[k]
		if !ok {
			x = &idx{sch: sch, table: table, name: name, unique: unique}
			byKey[k] = x
			order = append(order, k)
		}
		if !col.Valid {
			x.expr = true
			return nil
		}
		x.cols = append(x.cols, col.String)
		return nil
	})
	if err != nil {
		return KeyCatalogue{}, err
	}
	for _, k := range order {
		x := byKey[k]
		if x.expr {
			continue
		}
		kb.update(x.sch, x.table, func(tk *TableKeys) {
			if x.name == mysqlPrimaryIndex {
				tk.PrimaryKey = x.cols
				return
			}
			tk.Indexes = append(tk.Indexes, schema.Index{Name: x.name, Columns: x.cols, Unique: x.unique})
		})
	}
	type fk struct {
		sch, table string
		key        schema.ForeignKey
	}
	var fkOrder []string
	fks := map[string]*fk{}
	err = eachRow(ctx, db, mysqlForeignKeyQuery, nil, func(rows *sql.Rows) error {
		var sch, table, name, col, rsch, rtable, rcol, del, upd string
		if err := rows.Scan(&sch, &table, &name, &col, &rsch, &rtable, &rcol, &del, &upd); err != nil {
			return err
		}
		k := sch + "\x00" + table + "\x00" + name
		f, ok := fks[k]
		if !ok {
			f = &fk{sch: sch, table: table, key: schema.ForeignKey{
				Name: name, RefTable: rsch + "." + rtable, OnDelete: schema.Action(del), OnUpdate: schema.Action(upd),
			}}
			fks[k] = f
			fkOrder = append(fkOrder, k)
		}
		f.key.Columns = append(f.key.Columns, col)
		f.key.RefColumns = append(f.key.RefColumns, rcol)
		return nil
	})
	if err != nil {
		return KeyCatalogue{}, err
	}
	for _, k := range fkOrder {
		f := fks[k]
		kb.update(f.sch, f.table, func(tk *TableKeys) { tk.ForeignKeys = append(tk.ForeignKeys, f.key) })
	}
	return kb.cat, nil
}

func readSQLiteKeys(ctx context.Context, db *sql.DB) (KeyCatalogue, error) {
	var tables []string
	err := eachRow(ctx, db, sqliteKeyTablesQuery, nil, func(rows *sql.Rows) error {
		var n string
		if err := rows.Scan(&n); err != nil {
			return err
		}
		tables = append(tables, n)
		return nil
	})
	if err != nil {
		return KeyCatalogue{}, err
	}
	kb := newKeyBuilder(sqliteMainSchema)
	pks := map[string][]string{}
	for _, t := range tables {
		var pk []string
		if err := eachRow(ctx, db, sqlitePKQuery, []any{t}, func(rows *sql.Rows) error {
			var c string
			if err := rows.Scan(&c); err != nil {
				return err
			}
			pk = append(pk, c)
			return nil
		}); err != nil {
			return KeyCatalogue{}, err
		}
		pks[t] = pk
	}
	for _, t := range tables {
		tk := TableKeys{PrimaryKey: pks[t]}
		type listed struct {
			name   string
			unique bool
		}
		var idxs []listed
		if err := eachRow(ctx, db, sqliteIndexListQuery, []any{t}, func(rows *sql.Rows) error {
			var l listed
			if err := rows.Scan(&l.name, &l.unique); err != nil {
				return err
			}
			idxs = append(idxs, l)
			return nil
		}); err != nil {
			return KeyCatalogue{}, err
		}
		for _, l := range idxs {
			var cols []string
			expr := false
			if err := eachRow(ctx, db, sqliteIndexInfoQuery, []any{l.name}, func(rows *sql.Rows) error {
				var c sql.NullString
				if err := rows.Scan(&c); err != nil {
					return err
				}
				if !c.Valid {
					expr = true
				}
				cols = append(cols, c.String)
				return nil
			}); err != nil {
				return KeyCatalogue{}, err
			}
			if !expr {
				tk.Indexes = append(tk.Indexes, schema.Index{Name: l.name, Columns: cols, Unique: l.unique})
			}
		}
		byID := map[int]*schema.ForeignKey{}
		var ids []int
		if err := eachRow(ctx, db, sqliteForeignKeyList, []any{t}, func(rows *sql.Rows) error {
			var id int
			var ref, from, del, upd string
			var to sql.NullString
			if err := rows.Scan(&id, &ref, &from, &to, &del, &upd); err != nil {
				return err
			}
			f, ok := byID[id]
			if !ok {
				f = &schema.ForeignKey{RefTable: CatalogueKey(sqliteMainSchema, ref), OnDelete: schema.Action(del), OnUpdate: schema.Action(upd)}
				byID[id] = f
				ids = append(ids, id)
			}
			f.Columns = append(f.Columns, from)
			f.RefColumns = append(f.RefColumns, to.String)
			return nil
		}); err != nil {
			return KeyCatalogue{}, err
		}
		sort.Ints(ids)
		for _, id := range ids {
			f := byID[id]
			// REFERENCES t with no column list names t's primary key, and
			// SQLite reports the referenced column as NULL.
			if slices.Contains(f.RefColumns, "") {
				f.RefColumns = pks[strings.TrimPrefix(f.RefTable, sqliteMainSchema+".")]
			}
			tk.ForeignKeys = append(tk.ForeignKeys, *f)
		}
		kb.update(sqliteMainSchema, t, func(dst *TableKeys) { *dst = tk })
	}
	return kb.cat, nil
}

// eachRow runs query and calls fn per row, closing the rows and returning the
// first error from the query, a row, iteration or Close.
func eachRow(ctx context.Context, db *sql.DB, query string, args []any, fn func(*sql.Rows) error) error {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("sqlb vet: keys: %w", err)
	}
	for rows.Next() {
		if err := fn(rows); err != nil {
			_ = rows.Close()
			return err
		}
	}
	return errors.Join(rows.Err(), rows.Close())
}

// CompareKeys diffs the keys a library's DDL describes against the database:
//
//   - the primary key must be the same columns in the same order;
//   - every index the DDL declares must exist over the same columns in the
//     same order, and a unique one must be unique there;
//   - every foreign key the DDL declares must exist over the same columns,
//     referencing the same table and columns, with the same ON DELETE and ON
//     UPDATE, an unstated action being NO ACTION as every engine reports it.
//
// Keys are matched by their columns, never by name: SQLite reports no foreign
// key names, and an unnamed key's generated name is the engine's. What the
// database has and the DDL does not is a warning for a unique index or a
// foreign key, which a later ALTER may have added, and nothing for a plain
// index, since MySQL creates one for every foreign key and CREATE INDEX is
// not part of a CREATE TABLE's description. A table missing from keys is
// CompareTables's finding and is skipped here.
func CompareKeys(tables []schema.Table, keys KeyCatalogue) []Finding {
	var out []Finding
	finding := func(pos, msg string, warn bool) {
		out = append(out, Finding{Pos: pos, Analyzer: schemaAnalyzer, Message: msg, Warning: warn})
	}
	for _, t := range tables {
		pos := CatalogueKey(t.Schema, t.Name)
		got, ok := keys.Tables[pos]
		if !ok {
			continue
		}
		if !slices.Equal(t.PrimaryKey, got.PrimaryKey) {
			finding(pos, fmt.Sprintf("the DDL's primary key is %s, the database's is %s", cols(t.PrimaryKey), cols(got.PrimaryKey)), false)
		}
		usedIdx := map[int]bool{}
		for _, want := range t.Indexes {
			i := matchIndex(want, got.Indexes)
			switch {
			case i < 0:
				finding(pos, fmt.Sprintf("the DDL's %s is not in the database", indexWord(want)), false)
			case want.Unique && !got.Indexes[i].Unique:
				finding(pos, fmt.Sprintf("the DDL's %s is not unique in the database", indexWord(want)), false)
				usedIdx[i] = true
			default:
				usedIdx[i] = true
			}
		}
		for i, g := range got.Indexes {
			if g.Unique && !usedIdx[i] && !slices.Equal(g.Columns, t.PrimaryKey) {
				finding(pos, fmt.Sprintf("the database has a unique index on %s the DDL does not create", cols(g.Columns)), true)
			}
		}
		usedFK := map[int]bool{}
		for _, want := range t.ForeignKeys {
			i := slices.IndexFunc(got.ForeignKeys, func(g schema.ForeignKey) bool { return slices.Equal(g.Columns, want.Columns) })
			if i < 0 {
				finding(pos, fmt.Sprintf("the DDL's foreign key on %s is not in the database", cols(want.Columns)), false)
				continue
			}
			usedFK[i] = true
			g := got.ForeignKeys[i]
			if !sameTable(want.RefTable, g.RefTable, keys.DefaultSchema) || !slices.Equal(want.RefColumns, g.RefColumns) {
				finding(pos, fmt.Sprintf("the foreign key on %s references %s%s in the DDL and %s%s in the database",
					cols(want.Columns), want.RefTable, cols(want.RefColumns), g.RefTable, cols(g.RefColumns)), false)
			}
			if action(want.OnDelete) != action(g.OnDelete) {
				finding(pos, fmt.Sprintf("the foreign key on %s is ON DELETE %s in the DDL and %s in the database", cols(want.Columns), action(want.OnDelete), action(g.OnDelete)), false)
			}
			if action(want.OnUpdate) != action(g.OnUpdate) {
				finding(pos, fmt.Sprintf("the foreign key on %s is ON UPDATE %s in the DDL and %s in the database", cols(want.Columns), action(want.OnUpdate), action(g.OnUpdate)), false)
			}
		}
		for i, g := range got.ForeignKeys {
			if !usedFK[i] {
				finding(pos, fmt.Sprintf("the database has a foreign key on %s the DDL does not create", cols(g.Columns)), true)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Pos < out[j].Pos })
	return out
}

// matchIndex finds the database index over the same columns, preferring one
// whose uniqueness matches, since MySQL may hold a unique index and the plain
// one it made for a foreign key over the same columns.
func matchIndex(want schema.Index, got []schema.Index) int {
	found := -1
	for i, g := range got {
		if !slices.Equal(g.Columns, want.Columns) {
			continue
		}
		if g.Unique == want.Unique {
			return i
		}
		if found < 0 {
			found = i
		}
	}
	return found
}

// sameTable reports whether a DDL reference and a catalogue's qualified name
// are the same table: exactly, or the DDL's unqualified name in the default
// schema.
func sameTable(ddlRef, catRef, defaultSchema string) bool {
	return ddlRef == catRef || (!strings.Contains(ddlRef, ".") && defaultSchema+"."+ddlRef == catRef)
}

// action is a referential action as the catalogues spell it; an unstated one
// is NO ACTION, which is what every engine reports for it.
func action(a schema.Action) schema.Action {
	if a == "" {
		return schema.NoAction
	}
	return schema.Action(strings.ToUpper(string(a)))
}

func cols(c []string) string { return "(" + strings.Join(c, ", ") + ")" }

func indexWord(ix schema.Index) string {
	if ix.Unique {
		return "unique index on " + cols(ix.Columns)
	}
	return "index on " + cols(ix.Columns)
}

// CheckTables is the boot check in one call: it reads the column and key
// catalogues and returns CompareTables's findings followed by CompareKeys's.
func CheckTables(ctx context.Context, db *sql.DB, dialect string, tables []schema.Table) ([]Finding, error) {
	cat, err := ReadCatalogue(ctx, db, dialect)
	if err != nil {
		return nil, err
	}
	keys, err := ReadKeys(ctx, db, dialect)
	if err != nil {
		return nil, err
	}
	return append(CompareTables(dialect, tables, cat), CompareKeys(tables, keys)...), nil
}
