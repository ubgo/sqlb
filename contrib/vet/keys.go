package vet

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/ubgo/sqlb/render"
	"github.com/ubgo/sqlb/schema"
)

// TableKeys is a table's keys as the database has them: its primary key, its
// indexes on plain columns, and its foreign keys. Each index carries every
// key's direction in Orders, with its NULLS placement on Postgres, and a
// partial index its predicate in Where, as the engine prints it. An expression
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
	// columns (beyond indnkeyatts) are not key columns, and an expression key
	// is attnum 0 and its index is skipped. indoption holds one flag word per
	// key column, bit 1 DESC and bit 2 NULLS FIRST; a partial index's
	// predicate is pg_get_expr of indpred, NULL for an index that is not.
	pgIndexQuery = `SELECT n.nspname, c.relname, i.indisprimary, i.indisunique,
			(SELECT string_agg(a.attname, chr(31) ORDER BY k.ord)
				FROM unnest(i.indkey::int2[]) WITH ORDINALITY AS k(attnum, ord)
				JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = k.attnum
				WHERE k.ord <= i.indnkeyatts),
			x.relname,
			(SELECT string_agg(o.opt::text, chr(31) ORDER BY o.ord)
				FROM unnest(i.indoption::int2[]) WITH ORDINALITY AS o(opt, ord)
				WHERE o.ord <= i.indnkeyatts),
			pg_get_expr(i.indpred, i.indrelid)
		FROM pg_index i
		JOIN pg_class c ON c.oid = i.indrelid
		JOIN pg_class x ON x.oid = i.indexrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname <> 'information_schema' AND left(n.nspname, 3) <> 'pg_'
			AND NOT (0 = ANY (i.indkey::int2[]))`
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
	// part has no column_name and makes its index skipped. collation is A for
	// ascending, D for descending and NULL for a key that is not sorted, a
	// HASH or FULLTEXT one. MySQL has no partial indexes.
	mysqlIndexQuery = `SELECT table_schema, table_name, index_name, non_unique = 0, column_name, collation
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
	// primary key.
	sqliteIndexListQuery = `SELECT name, "unique", partial FROM pragma_index_list(?) WHERE origin <> 'pk'`
	// sqliteIndexInfoQuery reads the key columns with their direction; key = 0
	// rows are the rowid and other auxiliary columns, not keys.
	sqliteIndexInfoQuery = `SELECT name, "desc" FROM pragma_index_xinfo(?) WHERE key = 1 ORDER BY seqno`
	// sqliteIndexSQLQuery reads a partial index's CREATE INDEX as written,
	// which is the only place SQLite keeps its predicate.
	sqliteIndexSQLQuery  = `SELECT sql FROM sqlite_master WHERE type = 'index' AND name = ?`
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
		var cols, opts, pred sql.NullString
		if err := rows.Scan(&sch, &table, &primary, &unique, &cols, &name, &opts, &pred); err != nil {
			return err
		}
		orders, err := pgOrders(opts)
		if err != nil {
			return fmt.Errorf("sqlb vet: keys: index %s: %w", name, err)
		}
		kb.update(sch, table, func(tk *TableKeys) {
			if primary {
				tk.PrimaryKey = splitCols(cols)
				return
			}
			tk.Indexes = append(tk.Indexes, schema.Index{Name: name, Columns: splitCols(cols), Unique: unique, Orders: orders, Where: pred.String})
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

// The indoption flags Postgres sets on an index key column.
const (
	pgIndOptionDesc       = 1
	pgIndOptionNullsFirst = 2
)

// pgOrders turns indoption's per-key flag words into key orders, stating the
// NULLS placement for every key because Postgres records one for every key.
func pgOrders(opts sql.NullString) ([]schema.KeyOrder, error) {
	words := splitCols(opts)
	if words == nil {
		return nil, nil
	}
	out := make([]schema.KeyOrder, len(words))
	for i, w := range words {
		n, err := strconv.Atoi(w)
		if err != nil {
			return nil, err
		}
		out[i] = schema.KeyOrder{Direction: schema.Ascending, Nulls: schema.NullsLast}
		if n&pgIndOptionDesc != 0 {
			out[i].Direction = schema.Descending
		}
		if n&pgIndOptionNullsFirst != 0 {
			out[i].Nulls = schema.NullsFirst
		}
	}
	return out, nil
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
		orders           []schema.KeyOrder
	}
	var order []string
	byKey := map[string]*idx{}
	err := eachRow(ctx, db, mysqlIndexQuery, nil, func(rows *sql.Rows) error {
		var sch, table, name string
		var unique bool
		var col, coll sql.NullString
		if err := rows.Scan(&sch, &table, &name, &unique, &col, &coll); err != nil {
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
		o := schema.KeyOrder{Direction: schema.Ascending}
		if coll.String == "D" {
			o.Direction = schema.Descending
		}
		x.orders = append(x.orders, o)
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
			tk.Indexes = append(tk.Indexes, schema.Index{Name: x.name, Columns: x.cols, Unique: x.unique, Orders: x.orders})
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
			name            string
			unique, partial bool
		}
		var idxs []listed
		if err := eachRow(ctx, db, sqliteIndexListQuery, []any{t}, func(rows *sql.Rows) error {
			var l listed
			if err := rows.Scan(&l.name, &l.unique, &l.partial); err != nil {
				return err
			}
			idxs = append(idxs, l)
			return nil
		}); err != nil {
			return KeyCatalogue{}, err
		}
		for _, l := range idxs {
			var cols []string
			var orders []schema.KeyOrder
			expr := false
			if err := eachRow(ctx, db, sqliteIndexInfoQuery, []any{l.name}, func(rows *sql.Rows) error {
				var c sql.NullString
				var desc bool
				if err := rows.Scan(&c, &desc); err != nil {
					return err
				}
				if !c.Valid {
					expr = true
				}
				cols = append(cols, c.String)
				o := schema.KeyOrder{Direction: schema.Ascending}
				if desc {
					o.Direction = schema.Descending
				}
				orders = append(orders, o)
				return nil
			}); err != nil {
				return KeyCatalogue{}, err
			}
			if expr {
				continue
			}
			ix := schema.Index{Name: l.name, Columns: cols, Unique: l.unique, Orders: orders}
			if l.partial {
				var create sql.NullString
				if err := db.QueryRowContext(ctx, sqliteIndexSQLQuery, l.name).Scan(&create); err != nil {
					return KeyCatalogue{}, fmt.Errorf("sqlb vet: keys: %w", err)
				}
				where, err := sqliteIndexPredicate(create.String)
				if err != nil {
					return KeyCatalogue{}, fmt.Errorf("%w: %s", err, l.name)
				}
				ix.Where = where
			}
			tk.Indexes = append(tk.Indexes, ix)
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
//   - an index whose description states Orders must sort every key the same
//     way, FindingIndexOrder otherwise; an unstated direction is ascending,
//     and an unstated NULLS placement is the default for its direction, and
//     is judged only where the catalogue reports one (Postgres);
//   - an index the DDL declares partial must be partial in the database and
//     the reverse, and two predicates must be the same, FindingIndexPredicate
//     otherwise. CompareKeys has no database to ask, so predicates whose
//     normal forms differ are FindingIndexPredicateNotCompared, a warning;
//     CheckTables asks the engine and reports them as same or different (the
//     rule is in predicate.go and contrib/vet/README.md);
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
	return compareKeys(tables, keys, textOnly)
}

// compareKeys is CompareKeys with the predicate comparer chosen by the
// caller, which is how CheckTables lets the engine judge predicates.
func compareKeys(tables []schema.Table, keys KeyCatalogue, samePredicate predicateComparer) []Finding {
	var out []Finding
	finding := func(pos, msg string, warn bool) {
		out = append(out, Finding{Pos: pos, Analyzer: schemaAnalyzer, Message: msg, Warning: warn})
	}
	typed := func(pos string, kind FindingKind, msg string, warn bool) {
		out = append(out, Finding{Pos: pos, Analyzer: schemaAnalyzer, Message: msg, Warning: warn, Kind: kind})
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
			i := matchIndex(want, got.Indexes, usedIdx)
			switch {
			case i < 0:
				finding(pos, fmt.Sprintf("the DDL's %s is not in the database", indexWord(want)), false)
			case want.Unique && !got.Indexes[i].Unique:
				finding(pos, fmt.Sprintf("the DDL's %s is not unique in the database", indexWord(want)), false)
				usedIdx[i] = true
			default:
				usedIdx[i] = true
			}
			if i >= 0 {
				g := got.Indexes[i]
				if msg, differ := orderDifference(want, g); differ {
					typed(pos, FindingIndexOrder, msg, false)
				}
				switch {
				case want.Where != "" && g.Where == "":
					typed(pos, FindingIndexPredicate, fmt.Sprintf("the DDL's %s is partial (WHERE %s) and the database's is not", indexWord(want), want.Where), false)
				case want.Where == "" && g.Where != "":
					typed(pos, FindingIndexPredicate, fmt.Sprintf("the database's %s is partial (WHERE %s) and the DDL's is not", indexWord(want), g.Where), false)
				case want.Where != "":
					switch m, why := samePredicate(t, want.Where, g.Where); m {
					case predicateDifferent:
						typed(pos, FindingIndexPredicate, fmt.Sprintf("the DDL's %s is WHERE %s, the database's is WHERE %s", indexWord(want), want.Where, g.Where), false)
					case predicateNotCompared:
						typed(pos, FindingIndexPredicateNotCompared, fmt.Sprintf("the %s is partial on both sides, not compared (%s): the DDL's is WHERE %s, the database's is WHERE %s", indexWord(want), why, want.Where, g.Where), true)
					case predicateSame:
					}
				}
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

// matchIndex finds the database index over the same columns. Several may
// share them, a partial index beside a full one, so it prefers, in order: one
// no earlier declaration matched; one whose uniqueness matches, since MySQL
// may hold a unique index and the plain one it made for a foreign key over the
// same columns; one with the declared name; one that is partial exactly when
// the declaration is.
func matchIndex(want schema.Index, got []schema.Index, used map[int]bool) int {
	found, best := -1, -1
	for i, g := range got {
		if !slices.Equal(g.Columns, want.Columns) {
			continue
		}
		score := 0
		if !used[i] {
			score += 8
		}
		if g.Unique == want.Unique {
			score += 4
		}
		if want.Name != "" && g.Name == want.Name {
			score += 2
		}
		if (g.Where == "") == (want.Where == "") {
			score++
		}
		if score > best {
			found, best = i, score
		}
	}
	return found
}

// orderDifference compares the key orders of a declared index and the
// database's, when the declaration states them. An unstated direction is
// ascending; a NULLS placement is judged only when the catalogue reports one,
// an unstated one being Postgres's default for the direction.
func orderDifference(want, got schema.Index) (string, bool) {
	if want.Orders == nil {
		return "", false
	}
	var ddlSide, dbSide []string
	for i, col := range want.Columns {
		w := schema.KeyOrder{}
		if i < len(want.Orders) {
			w = want.Orders[i]
		}
		g := schema.KeyOrder{}
		if i < len(got.Orders) {
			g = got.Orders[i]
		}
		wDir, gDir := direction(w.Direction), direction(g.Direction)
		same := wDir == gDir
		wNulls, gNulls := "", ""
		if g.Nulls != "" {
			wNulls, gNulls = " "+string(nulls(w.Nulls, wDir)), " "+string(g.Nulls)
			same = same && wNulls == gNulls
		}
		if !same {
			ddlSide = append(ddlSide, col+" "+string(wDir)+wNulls)
			dbSide = append(dbSide, col+" "+string(gDir)+gNulls)
		}
	}
	if ddlSide == nil {
		return "", false
	}
	return fmt.Sprintf("the DDL's %s sorts %s, the database's sorts %s", indexWord(want), strings.Join(ddlSide, ", "), strings.Join(dbSide, ", ")), true
}

// direction is an index key's direction with the unstated one as ascending,
// which is what every engine does with it.
func direction(d schema.Direction) schema.Direction {
	if d == "" {
		return schema.Ascending
	}
	return d
}

// nulls is a key's NULLS placement with the unstated one as Postgres's
// default for the direction: last ascending, first descending.
func nulls(n schema.NullsOrder, d schema.Direction) schema.NullsOrder {
	switch {
	case n != "":
		return n
	case d == schema.Descending:
		return schema.NullsFirst
	}
	return schema.NullsLast
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
// catalogues and returns CompareTables's findings followed by CompareKeys's,
// except that two partial-index predicates whose texts differ are judged by
// the engine (on Postgres by its canonical text, inside a transaction that is
// rolled back; on SQLite by the stored text) instead of left not compared.
func CheckTables(ctx context.Context, db *sql.DB, dialect string, tables []schema.Table) ([]Finding, error) {
	cat, err := ReadCatalogue(ctx, db, dialect)
	if err != nil {
		return nil, err
	}
	keys, err := ReadKeys(ctx, db, dialect)
	if err != nil {
		return nil, err
	}
	return append(CompareTables(dialect, tables, cat), compareKeys(tables, keys, comparerFor(ctx, db, dialect))...), nil
}
