package vet

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"go/ast"
	"go/constant"
	"go/types"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"

	"github.com/ubgo/sqlb/render"
	"github.com/ubgo/sqlb/schema"
)

// schemaAnalyzer is the name the database diff reports under; it is not an
// analysis.Analyzer because it needs a connection, but it prints like one.
const schemaAnalyzer = "sqlbschema"

// Declaration is one Col[T] declaration found in source: the table and
// column it names, the Go type it claims, and where it was written.
type Declaration struct {
	Pos      string
	Table    string
	Schema   string
	Column   string
	GoType   string
	Nullable bool // the Go type is a pointer
}

// TableDeclaration is one package-level Table literal: where it was written
// and the name it states, so a warning about the table has a position.
type TableDeclaration struct {
	Pos    string
	Name   string
	Schema string
}

// Declared is everything Declarations found: the columns and the tables they
// were resolved against.
type Declared struct {
	Columns []Declaration
	Tables  []TableDeclaration
}

// Catalogue is what a database says a column is, keyed by CatalogueKey and
// then by column name. Type is the engine's own spelling, lower-cased;
// Nullable is whether NULL is allowed.
//
// A table in the connection's default schema, Postgres's current_schema(),
// MySQL's DATABASE() or SQLite's main, is stored under its bare name and
// under "schema.name", so a declaration that names no schema and one that
// names the default both find it. A table in any other schema is stored only
// qualified. Keying by the bare name alone, as this did until 2026-09-22,
// read only the default schema and reported every table of a library that
// owns a schema of its own, auth.users say, as missing, and would have merged
// two same-named tables in different schemas into one.
type Catalogue map[string]map[string]CatalogueColumn

// CatalogueKey is the key a Catalogue stores a table under: the bare name
// when schemaName is empty, otherwise "schemaName.name".
func CatalogueKey(schemaName, name string) string {
	if schemaName == "" {
		return name
	}
	return schemaName + "." + name
}

// add stores one column under the table's qualified key and, for a table in
// the default schema, under its bare name too.
func (c Catalogue) add(schemaName, table, column string, col CatalogueColumn, isDefault bool) {
	keys := []string{CatalogueKey(schemaName, table)}
	if isDefault {
		keys = append(keys, table)
	}
	for _, k := range keys {
		if c[k] == nil {
			c[k] = map[string]CatalogueColumn{}
		}
		c[k][column] = col
	}
}

// CatalogueColumn is one column as the database describes it.
type CatalogueColumn struct {
	Type     string
	Nullable bool
	// Default is the column's default as SQL text, nil for none. Postgres and
	// SQLite report it as SQL already; MySQL reports a literal's value
	// unquoted, which ReadCatalogue quotes so every engine's is SQL. Compare
	// by meaning with CompareTables, never by string: see defaults.go.
	Default *string
	// ColumnType is MySQL's column_type, the type with its length and
	// precision, varchar(255) where Type is varchar; empty on the other
	// engines, whose Type already carries what CompareTables needs.
	ColumnType string
}

// Declarations finds every expr.Col[T] and sqlb.Col[T] call in the packages
// the patterns name, resolving each table argument to the package-level
// Table literal it refers to.
//
// It reads source rather than a runtime registry, which is the point: a
// declaration in a code path nothing executes is still checked. A table that
// is not a package-level literal with a literal Name cannot be resolved and
// is reported as a Declaration with an empty Table, so it is visible rather
// than silently skipped.
func Declarations(dir string, patterns ...string) (Declared, error) {
	cfg := &packages.Config{Mode: packages.LoadAllSyntax | packages.NeedModule, Dir: dir, Tests: false}
	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		return Declared{}, fmt.Errorf("sqlb vet: load: %w", err)
	}
	if n := packages.PrintErrors(pkgs); n > 0 {
		return Declared{}, fmt.Errorf("sqlb vet: %d package(s) had errors", n)
	}
	var out Declared
	for _, pkg := range pkgs {
		tables := tableLiterals(pkg)
		for _, tl := range tables {
			out.Tables = append(out.Tables, TableDeclaration{Pos: tl.pos, Name: tl.name, Schema: tl.schema})
		}
		for _, f := range pkg.Syntax {
			ast.Inspect(f, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || len(call.Args) != 2 {
					return true
				}
				ix, ok := call.Fun.(*ast.IndexExpr)
				if !ok {
					return true
				}
				sel, ok := ix.X.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "Col" {
					return true
				}
				id, ok := sel.X.(*ast.Ident)
				if !ok {
					return true
				}
				pn, ok := pkg.TypesInfo.Uses[id].(*types.PkgName)
				if !ok {
					return true
				}
				if p := pn.Imported().Path(); p != Module && p != Module+"/expr" {
					return true
				}
				d := Declaration{Pos: pkg.Fset.Position(call.Pos()).String()}
				d.Column, _ = constString(pkg, call.Args[1])
				if t := pkg.TypesInfo.TypeOf(ix.Index); t != nil {
					d.GoType = types.TypeString(t, func(p *types.Package) string { return p.Name() })
					_, d.Nullable = t.(*types.Pointer)
				}
				if tid, ok := call.Args[0].(*ast.Ident); ok {
					if obj := pkg.TypesInfo.Uses[tid]; obj != nil {
						if tl, ok := tables[obj]; ok {
							d.Table, d.Schema = tl.name, tl.schema
						}
					}
				}
				out.Columns = append(out.Columns, d)
				return true
			})
		}
	}
	sort.Slice(out.Tables, func(i, j int) bool { return out.Tables[i].Pos < out.Tables[j].Pos })
	return out, nil
}

type tableLit struct{ name, schema, pos string }

// constString is the value of a string constant expression: a literal, a
// named constant, or any expression the compiler folds to one, such as
// prefix + "users". The type checker has already evaluated it, so this reads
// its answer rather than re-parsing. Until 2026-09-22 only a literal was
// read, so a table named by a constant was reported as unverifiable and a
// column named by one had no name.
func constString(pkg *packages.Package, e ast.Expr) (string, bool) {
	tv, ok := pkg.TypesInfo.Types[e]
	if !ok || tv.Value == nil || tv.Value.Kind() != constant.String {
		return "", false
	}
	return constant.StringVal(tv.Value), true
}

// tableLiterals maps each package-level Table variable to the Name and Schema
// its composite literal states.
func tableLiterals(pkg *packages.Package) map[types.Object]tableLit {
	out := map[types.Object]tableLit{}
	for _, f := range pkg.Syntax {
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, name := range vs.Names {
					if i >= len(vs.Values) {
						break
					}
					cl, ok := vs.Values[i].(*ast.CompositeLit)
					if !ok {
						continue
					}
					var tl tableLit
					for _, el := range cl.Elts {
						kv, ok := el.(*ast.KeyValueExpr)
						if !ok {
							continue
						}
						key, _ := kv.Key.(*ast.Ident)
						v, ok := constString(pkg, kv.Value)
						if key == nil || !ok {
							continue
						}
						switch key.Name {
						case "Name":
							tl.name = v
						case "Schema":
							tl.schema = v
						}
					}
					if tl.name != "" {
						tl.pos = pkg.Fset.Position(name.Pos()).String()
						out[pkg.TypesInfo.Defs[name]] = tl
					}
				}
			}
		}
	}
	return out
}

// The catalogue queries. Each reads every user schema, not only the default
// one, and flags the default so Catalogue.add can store it under both keys.
// Postgres's information_schema shows only columns the connecting role can
// access, so a role without privileges on a table sees it as missing; the
// system schemas, and pg_toast and the pg_temp_N schemas, are excluded by
// prefix rather than by LIKE so no escape character is involved.
const (
	pgCatalogueQuery = `SELECT table_schema, table_name, column_name, lower(data_type), is_nullable = 'YES', table_schema = current_schema(), column_default, '', ''
		FROM information_schema.columns
		WHERE table_schema <> 'information_schema' AND left(table_schema, 3) <> 'pg_'
		ORDER BY table_schema, table_name, ordinal_position`
	mysqlCatalogueQuery = `SELECT table_schema, table_name, column_name, lower(data_type), is_nullable = 'YES', table_schema = DATABASE(), column_default, extra, lower(column_type)
		FROM information_schema.columns
		WHERE table_schema NOT IN ('mysql', 'information_schema', 'performance_schema', 'sys')
		ORDER BY table_schema, table_name, ordinal_position`
	// sqliteTablesQuery reads strict and wr (WITHOUT ROWID) from
	// pragma_table_list, SQLite 3.37 or later, because both decide whether a
	// primary-key column may hold NULL.
	sqliteTablesQuery  = `SELECT name, "strict", wr FROM pragma_table_list WHERE schema = 'main' AND type = 'table' AND name NOT LIKE 'sqlite_%'`
	sqliteColumnsQuery = `SELECT name, lower(type), "notnull", pk, dflt_value FROM pragma_table_info(?)`
	// sqlitePKIndexQuery counts the index SQLite made for a primary key. A
	// rowid table's key has one unless the key is the rowid itself, which is
	// the one case SQLite enforces NOT NULL without being told.
	sqlitePKIndexQuery = `SELECT count(*) FROM pragma_index_list(?) WHERE origin = 'pk'`
	// sqliteMainSchema is the schema name of SQLite's own database file.
	sqliteMainSchema = "main"
)

// ReadCatalogue reads every user table's columns from the database, in every
// user schema. dialect is render.DialectPostgres, render.DialectSQLite or
// render.DialectMySQL; the query is the engine's own catalogue.
func ReadCatalogue(ctx context.Context, db *sql.DB, dialect string) (Catalogue, error) {
	switch dialect {
	case render.DialectPostgres:
		return readInformationSchema(ctx, db, pgCatalogueQuery, false)
	case render.DialectMySQL:
		return readInformationSchema(ctx, db, mysqlCatalogueQuery, true)
	case render.DialectSQLite:
		return readSQLite(ctx, db)
	}
	return nil, fmt.Errorf("sqlb vet: unknown dialect %q", dialect)
}

// readInformationSchema reads a catalogue query's rows. mysqlDefaults says
// the default column holds MySQL's unquoted value, which is quoted by the
// EXTRA column beside it.
func readInformationSchema(ctx context.Context, db *sql.DB, query string, mysqlDefaults bool) (Catalogue, error) {
	cat := Catalogue{}
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("sqlb vet: catalogue: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var sch, t, c, typ, extra, colType string
		var nullable, isDefault bool
		var def sql.NullString
		if err := rows.Scan(&sch, &t, &c, &typ, &nullable, &isDefault, &def, &extra, &colType); err != nil {
			return nil, err
		}
		col := CatalogueColumn{Type: typ, Nullable: nullable, ColumnType: colType}
		if def.Valid {
			v := def.String
			col.Default = &v
			if mysqlDefaults {
				col.Default = mysqlCatalogueDefault(&v, extra)
			}
		}
		cat.add(sch, t, c, col, isDefault)
	}
	return cat, rows.Err()
}

// readSQLite reads SQLite's catalogue. A primary-key column's nullability is
// SQLite's rule, not the SQL standard's: it holds NULL unless it is declared
// NOT NULL, the table is STRICT or WITHOUT ROWID, or the key is the rowid
// itself (https://www.sqlite.org/lang_createtable.html). Treating every key
// column as NOT NULL, as this did until 2026-09-22, approved a Col[string] on
// a TEXT PRIMARY KEY that can hold NULL, which fails at Scan.
func readSQLite(ctx context.Context, db *sql.DB) (Catalogue, error) {
	type table struct {
		name       string
		strict, wr bool
	}
	tabs, err := db.QueryContext(ctx, sqliteTablesQuery)
	if err != nil {
		return nil, fmt.Errorf("sqlb vet: catalogue: %w", err)
	}
	var tables []table
	for tabs.Next() {
		var t table
		if err := tabs.Scan(&t.name, &t.strict, &t.wr); err != nil {
			_ = tabs.Close()
			return nil, err
		}
		tables = append(tables, t)
	}
	if err := errors.Join(tabs.Err(), tabs.Close()); err != nil {
		return nil, err
	}
	cat := Catalogue{}
	for _, t := range tables {
		var pkIndexes int
		if err := db.QueryRowContext(ctx, sqlitePKIndexQuery, t.name).Scan(&pkIndexes); err != nil {
			return nil, err
		}
		keyEnforced := t.strict || t.wr || pkIndexes == 0
		rows, err := db.QueryContext(ctx, sqliteColumnsQuery, t.name)
		if err != nil {
			return nil, err
		}
		cols := map[string]CatalogueColumn{}
		for rows.Next() {
			var c, typ string
			var notnull, pk int
			var def sql.NullString
			if err := rows.Scan(&c, &typ, &notnull, &pk, &def); err != nil {
				_ = rows.Close()
				return nil, err
			}
			col := CatalogueColumn{Type: typ, Nullable: notnull == 0 && (pk == 0 || !keyEnforced)}
			if def.Valid {
				v := def.String
				col.Default = &v
			}
			cols[c] = col
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return nil, err
		}
		for c, col := range cols {
			cat.add(sqliteMainSchema, t.name, c, col, true)
		}
	}
	return cat, nil
}

// portableTypes says which portable types a catalogue type may be: on
// Postgres the catalogue spells a type one way, so the answer is one type or
// none; on SQLite a declared type is free text resolved by affinity, so a
// TEXT column may be a text, a timestamp, a uuid or a json column and the
// declaration is right if it fits any of them. The table read is schema's,
// the same one contrib/migrate writes DDL from, so a column this check
// accepts is one CreateTable would have produced.
func portableTypes(dialect, catType string) []schema.Type {
	var out []schema.Type
	switch dialect {
	case render.DialectPostgres:
		catType = pgAliases[catType]
		if catType == "" {
			return nil
		}
	case render.DialectMySQL:
		catType = mysqlAliases[catType]
		if catType == "" {
			return nil
		}
	case render.DialectSQLite:
		catType = sqliteAffinity(catType)
		if catType == "NUMERIC" {
			// NUMERIC affinity holds every storage class, so a DATETIME or a
			// DECIMAL column is whatever the writer put in it; no opinion.
			return nil
		}
	}
	for _, t := range schema.TypeValues {
		if s, ok := schema.EngineType(dialect, t); ok && strings.EqualFold(s, catType) {
			out = append(out, t)
		}
	}
	return out
}

// pgAliases maps information_schema.columns.data_type, which spells types
// the standard's way, onto the spelling the schema table uses. A type not
// listed is one the schema layer has no portable name for, such as an array
// or an enum, and is not this check's call.
var pgAliases = map[string]string{
	"text": "TEXT", "character varying": "TEXT", "character": "TEXT",
	"integer": "INTEGER", "smallint": "INTEGER", "bigint": "BIGINT",
	"boolean": "BOOLEAN", "double precision": "DOUBLE PRECISION", "real": "DOUBLE PRECISION",
	"bytea": "BYTEA", "timestamp with time zone": "TIMESTAMPTZ", "timestamp without time zone": "TIMESTAMPTZ",
	"date": "DATE", "uuid": "UUID", "jsonb": "JSONB", "json": "JSONB", "numeric": "NUMERIC",
}

// mysqlAliases maps MySQL's information_schema.columns.data_type, which drops
// the length, onto the spelling the schema table uses. A TEXT column is read
// as text as a VARCHAR is, and a TINYINT as the BOOLEAN it spells.
var mysqlAliases = map[string]string{
	"varchar": "VARCHAR(255)", "text": "VARCHAR(255)", "char": "CHAR(36)",
	"int": "INT", "bigint": "BIGINT", "tinyint": "BOOLEAN", "double": "DOUBLE",
	"longblob": "LONGBLOB", "datetime": "DATETIME(6)", "date": "DATE", "json": "JSON", "decimal": "DECIMAL(38, 10)",
}

// sqliteAffinity is SQLite's own rule for turning a declared type into one
// of its five storage affinities, in the order the engine applies it. One
// deliberate deviation: the engine matches only the first four letters of
// DOUBLE and this matches the whole word, which is every real spelling.
func sqliteAffinity(declared string) string {
	d := strings.ToUpper(declared)
	switch {
	case strings.Contains(d, "INT"):
		return "INTEGER"
	case strings.Contains(d, "CHAR"), strings.Contains(d, "CLOB"), strings.Contains(d, "TEXT"):
		return "TEXT"
	case d == "" || strings.Contains(d, "BLOB"):
		return "BLOB"
	case strings.Contains(d, "REAL"), strings.Contains(d, "FLOA"), strings.Contains(d, "DOUBLE"):
		return "REAL"
	default:
		return "NUMERIC"
	}
}

// checkable says whether a declared Go type is one this check has an
// opinion on. A basic type, a byte slice and time.Time are; a caller's own
// type, such as pg.JSON[T] or anything implementing sql.Scanner, carries its
// own contract with the driver and is left alone.
func checkable(goType string) bool {
	switch goType {
	case "string", "bool", "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64",
		"float32", "float64", "[]uint8", "time.Time":
		return true
	}
	return false
}

// typeAllowed reports whether a Go type fits any portable reading of the
// catalogue type. An empty list of readings means the catalogue type is not
// portable, and the declaration is accepted rather than guessed at.
func typeAllowed(dialect, catType, goType string) bool {
	base := strings.TrimPrefix(goType, "*")
	if base == "[]byte" {
		base = "[]uint8"
	}
	if !checkable(base) {
		return true
	}
	readings := portableTypes(dialect, catType)
	if len(readings) == 0 {
		return true
	}
	for _, r := range readings {
		for _, g := range schema.GoTypes(r) {
			if g == base {
				return true
			}
		}
	}
	return false
}

// Compare diffs declarations against a catalogue and returns one Finding per
// mismatch: a table or column the database does not have, a Go type the
// column's type cannot hold, and a nullable column declared without a
// pointer. A declaration whose table could not be resolved is reported as
// unverifiable rather than skipped. A column the database has that no
// declaration names is a warning on the table's declaration, so a migration
// that adds a column is not invisible; it is a warning because a library
// may deliberately declare only the columns it reads.
func Compare(dialect string, decls Declared, cat Catalogue) []Finding {
	var out []Finding
	named := map[string]map[string]bool{}
	for _, d := range decls.Columns {
		key := CatalogueKey(d.Schema, d.Table)
		if named[key] == nil {
			named[key] = map[string]bool{}
		}
		named[key][d.Column] = true
		f := Finding{Pos: d.Pos, Analyzer: schemaAnalyzer}
		switch {
		case d.Table == "":
			f.Message = fmt.Sprintf("column %q: table is not a package-level literal, so it cannot be verified", d.Column)
		case d.Column == "":
			f.Message = fmt.Sprintf("%s: a column whose name is not a constant cannot be verified; the escape-hatch listing names it too", key)
		case cat[key] == nil:
			f.Message = fmt.Sprintf("%s.%s: table %q is not in the database", key, d.Column, key)
		case func() bool { _, ok := cat[key][d.Column]; return !ok }():
			f.Message = fmt.Sprintf("%s.%s: column is not in the database", key, d.Column)
		default:
			c := cat[key][d.Column]
			switch {
			case !typeAllowed(dialect, c.Type, d.GoType):
				f.Message = fmt.Sprintf("%s.%s: declared Col[%s], database has %s", key, d.Column, d.GoType, c.Type)
			case c.Nullable && !d.Nullable:
				f.Message = fmt.Sprintf("%s.%s: column is NULLABLE, declared Col[%s]; use Col[*%s]", key, d.Column, d.GoType, d.GoType)
			default:
				continue
			}
		}
		out = append(out, f)
	}
	for _, t := range decls.Tables {
		key := CatalogueKey(t.Schema, t.Name)
		cols := cat[key]
		if cols == nil {
			continue // already reported on every column that names it
		}
		var missing []string
		for c := range cols {
			if !named[key][c] {
				missing = append(missing, c)
			}
		}
		if len(missing) == 0 {
			continue
		}
		sort.Strings(missing)
		out = append(out, Finding{Pos: t.Pos, Analyzer: schemaAnalyzer, Warning: true,
			Message: fmt.Sprintf("%s: database has columns no declaration names: %s", key, strings.Join(missing, ", "))})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Pos < out[j].Pos })
	return out
}

// Schema is the whole --dsn mode: declarations from source, the catalogue
// from db, and the diff. dialect is render.DialectPostgres or
// render.DialectSQLite.
func Schema(ctx context.Context, db *sql.DB, dialect, dir string, patterns ...string) ([]Finding, error) {
	decls, err := Declarations(dir, patterns...)
	if err != nil {
		return nil, err
	}
	cat, err := ReadCatalogue(ctx, db, dialect)
	if err != nil {
		return nil, err
	}
	return Compare(dialect, decls, cat), nil
}

// CompareTables diffs tables a library's DDL describes, from
// ddl.CreateTableReady.Describe, against a catalogue: a table or column the
// database does not have, a column whose nullability differs in either
// direction, and a portable type the catalogue's type cannot be. It is the
// check a library wants at boot, "the database is not what this library's DDL
// creates", and unlike Compare it reads the DDL rather than the Col[T]
// declarations, so it sees nullability and the declared SQL type.
//
// A column declared with a Raw spelling, an enum or a domain or a citext, is
// checked for existence and nullability only: the catalogue spells such types
// its own way, and matching the spelling would be a guess. A column the
// database has and the DDL does not is a warning, as in Compare. Each finding
// is positioned at "table.column", or "schema.table.column" for a table with
// a schema, since a description has no source line. A column wrong in both
// nullability and type gets a finding for each. A default is compared by
// meaning, as defaults.go describes: a default on one side only, or two
// literals that differ, is an error, and two expressions that still differ
// once normalised are a warning, since the engine reprints an expression in a
// form nobody documents. Keys, indexes and foreign keys are CompareKeys's.
func CompareTables(dialect string, tables []schema.Table, cat Catalogue) []Finding {
	var out []Finding
	finding := func(pos, msg string, warn bool) {
		out = append(out, Finding{Pos: pos, Analyzer: schemaAnalyzer, Message: msg, Warning: warn})
	}
	for _, t := range tables {
		key := CatalogueKey(t.Schema, t.Name)
		cols := cat[key]
		if cols == nil {
			finding(key, fmt.Sprintf("table %q is not in the database", key), false)
			continue
		}
		declared := map[string]bool{}
		for _, c := range t.Columns {
			declared[c.Name] = true
			pos := key + "." + c.Name
			got, ok := cols[c.Name]
			if !ok {
				finding(pos, "column is not in the database", false)
				continue
			}
			// Nullability and type are independent, so both are reported:
			// a switch here hid a type drift behind a nullability one until
			// the first was fixed.
			if got.Nullable && !c.Nullable {
				finding(pos, "the DDL says NOT NULL, the database allows NULL", false)
			}
			if !got.Nullable && c.Nullable && !isKey(t, c.Name) {
				finding(pos, "the DDL allows NULL, the database says NOT NULL", false)
			}
			if c.Raw == "" && c.Type != "" && !typeIs(dialect, got, c.Type) {
				finding(pos, fmt.Sprintf("the DDL says %s, the database has %s", c.Type, got.Type), false)
			}
			switch compareDefaultsAs(c.Default, got.Default, isJSONColumn(c)) {
			case defaultsDiffer:
				finding(pos, fmt.Sprintf("the DDL's default is %s, the database's is %s", describeDefault(c.Default), describeDefault(deref(got.Default))), false)
			case defaultsMayDiffer:
				finding(pos, fmt.Sprintf("the DDL's default is %s and the database prints %s; the engine reprints an expression, so check it by eye", c.Default, deref(got.Default)), true)
			}
		}
		var extra []string
		for c := range cols {
			if !declared[c] {
				extra = append(extra, c)
			}
		}
		if len(extra) > 0 {
			sort.Strings(extra)
			finding(key, "database has columns the DDL does not create: "+strings.Join(extra, ", "), true)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Pos < out[j].Pos })
	return out
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func describeDefault(s string) string {
	if parseDefault(s).kind == kindNone {
		return "none"
	}
	return s
}

// isKey reports whether a column is in the table's primary key. A key column
// the DDL leaves nullable is NOT NULL in Postgres and MySQL, which impose it
// on every primary key, so that direction is the engine's rule rather than
// drift. The other direction is still reported: a key column the DDL says is
// NOT NULL and the database allows NULL in is a real difference, and SQLite
// has it, since a key there holds NULL unless it is declared NOT NULL.
func isKey(t schema.Table, col string) bool {
	for _, k := range t.PrimaryKey {
		if k == col {
			return true
		}
	}
	return false
}

// typeIs reports whether the catalogue's type is exactly the one the DDL's
// portable type is written as. It is strict where typeAllowed is lenient,
// because CompareTables asks "is this the column the DDL created" and Compare
// asks "can this Go type hold it"; until 2026-09-22 CompareTables reused the
// lenient readings and passed a timestamptz that had become a timestamp.
func typeIs(dialect string, got CatalogueColumn, want schema.Type) bool {
	spelling, ok := schema.EngineType(dialect, want)
	if !ok {
		return true // an engine with no type table has no spelling to compare
	}
	have := got.Type
	switch dialect {
	case render.DialectPostgres:
		have = pgStrict[got.Type]
	case render.DialectMySQL:
		have = got.ColumnType
		if strings.EqualFold(spelling, mysqlBoolean) {
			spelling = mysqlBooleanColumnType
		}
	}
	return normaliseType(have) == normaliseType(spelling)
}

// pgStrict maps information_schema.columns.data_type onto the spelling the
// schema table writes, for exactly the types that table writes. Unlike
// pgAliases, which answers whether a Go type can hold a column, this answers
// whether a column is the type the DDL created, so a timestamp without time
// zone is not a timestamptz, a real is not a double precision and a json is
// not a jsonb: each reads back differently, and each is drift.
var pgStrict = map[string]string{
	"text": "TEXT", "integer": "INTEGER", "bigint": "BIGINT", "boolean": "BOOLEAN",
	"double precision": "DOUBLE PRECISION", "bytea": "BYTEA", "timestamp with time zone": "TIMESTAMPTZ",
	"date": "DATE", "uuid": "UUID", "jsonb": "JSONB", "numeric": "NUMERIC",
}

// MySQL writes BOOLEAN as tinyint(1) and reports it that way in column_type.
const (
	mysqlBoolean           = "BOOLEAN"
	mysqlBooleanColumnType = "tinyint(1)"
)

// normaliseType lower-cases a type spelling and drops its spaces, so
// DECIMAL(38, 10) and decimal(38,10) compare equal.
func normaliseType(s string) string {
	return strings.ReplaceAll(strings.ToLower(s), " ", "")
}
