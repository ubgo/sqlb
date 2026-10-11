package sqlite_test

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The detector for SQLite, the counterpart of pg/ddl_coverage_test.go.
//
// testdata/lang-index.txt is every entry of SQLite's own grammar index, and
// each must carry a status here, so a statement SQLite has and this module
// cannot build is visible rather than silent. The index mixes statements with
// pages that are not statements, a function list or "expression", and those
// are classified too, so nothing on it is skipped by a filter somebody wrote.
//
// A status is one of:
//
//	partial <Name> <what is missing>
//	                    the construct exists and builds the statement, and the
//	                    named parts of it are still to come
//	built <Name>        the construct exists in this package, and is checked to
//	core <Method>       built once in stmt and reached from here, checked there
//	planned <tier>      in scope, tier 1 to 4 as the DDL spec ranks them
//	operator            maintenance and connection state: the operator's
//	not-ddl             a transaction or introspection command
//	not-a-statement     an index page that is not a statement at all
//
// What built does NOT assert, and what was done about it. The format records
// one constructor per statement, so a built entry says the statement has an
// entry point and says nothing about which of its forms render. Every built
// entry was read against SQLite's railroad diagrams on 2026-09-24 and each
// form measured on 3.50.3; what that found is now built and executed in
// contrib/driver-sqlite:
//
//   - TEMPORARY on CREATE TABLE, VIEW and TRIGGER; there is no CREATE TEMP
//     INDEX, which is a syntax error, and a temporary index is the index in
//     the temp schema instead.
//   - A schema on an Index, View or Trigger, and on the table of an FTS5,
//     R*Tree or other virtual table, which was a silent wrong answer rather
//     than an absence: the schema was dropped and the object was created in
//     main.
//   - CREATE VIEW's own column list.
//   - COLLATE and ASC/DESC inside a table-level PRIMARY KEY and UNIQUE, which
//     is what makes a case-insensitive unique column writable.
//   - The column-level PRIMARY KEY with ASC/DESC and a conflict clause.
//   - All three deferrability spellings on a foreign key.
//   - RAISE, in all four of its forms, inside a trigger body.
//   - The one-table argument of integrity_check, quick_check and
//     foreign_key_check.
//   - CREATE TABLE ... AS SELECT, as sqlite.CreateTableAs. It is a statement
//     of its own rather than a form of ddl's CREATE TABLE, on the
//     pg.CreateTableLike precedent: ddl's typestate requires a column and a
//     table whose body is a SELECT has no column list, which SQLite refuses
//     outright. So this needed nothing from ddl after all, which the first
//     pass had it down as needing.
//   - A conflict clause on NOT NULL, as sqlite.NotNullOnConflict, at ddl's
//     PartAfterNotNull, which is the only position SQLite takes it in:
//     written after the DEFAULT instead, the statement is a syntax error.
//     Measured, OrReplace writes the column's DEFAULT and keeps the row while
//     the other four refuse it, so the clause changes what an insert does
//     rather than only how it reads.
//
// What is left, with the reason, because the reason is the point:
//
//   - A conflict clause on CHECK is left out because SQLite does not honour
//     it. A column-level one is a syntax error, which the railroad diagram
//     does not say, and a table-level one is accepted and ignored: every
//     action behaves as ABORT, measured on 3.50.3 against a UNIQUE control
//     where the same clause demonstrably works. Building it would promise
//     behaviour the engine does not perform, which is the MATCH case again.
//     TestForms_CheckConflictClauseIsIgnoredBySQLite in contrib/driver-sqlite
//     keeps the measurement, so a later SQLite honouring it fails the build
//     rather than going unnoticed.
//   - MATCH on a foreign key is left out because SQLite parses it and ignores
//     it, so building it would promise an enforcement the engine does not do.
//   - The N argument of integrity_check and quick_check, because the
//     table-valued form the wrapped check is built on reads any argument as a
//     table name and answers "no such table: 5", and a cap on how many errors
//     are listed is meaningless under MustReturnNoRows, which asks only
//     whether there is one.
//   - ALTER TABLE ... ALTER COLUMN SET/DROP NOT NULL does not exist in 3.50.3,
//     measured: "near ALTER: syntax error".
//
// One finding from this work is not about a statement at all and belongs with
// the rest of the record. Three types in this package are a named string over
// what looks like a closed set, and each needed a different answer. OrAction
// is genuinely closed, so it validates at every one of its four render sites;
// two of those were in clauses shipped before the one that found the hole.
// Module is closed enough to constrain, so its single render site refuses
// anything but a bare identifier. Modifier cannot be closed, because an offset
// carries its number inside the string, so it is quoted as a literal instead —
// and the part of it that IS closed, an offset's unit, became a Unit constant
// set, because SQLite answers an unrecognised modifier with NULL from the
// whole function rather than with an error. A scan of every caller-supplied
// string in this package found no other value reaching statement text without
// passing through QuoteIdent, a literal or a bound parameter, beyond the
// documented escape hatches: the Expr/Pred/Func family and a virtual table's
// module arguments.
var sqliteIndex = map[string]string{
	"SQL keywords":        "not-a-statement",
	"aggregate functions": "not-a-statement",
	"ALTER TABLE":         "built AlterTable",
	"ANALYZE":             "operator",
	"ATTACH DATABASE":     "operator",
	"BEGIN TRANSACTION":   "not-ddl",
	"comment":             "not-a-statement",
	"COMMIT TRANSACTION":  "not-ddl",
	"core functions":      "not-a-statement",
	"CREATE INDEX":        "built CreateIndex",
	"CREATE TABLE":        "built CreateTable",
	"CREATE TRIGGER":      "built CreateTrigger",
	"CREATE VIEW":         "built CreateView",
	// CREATE VIRTUAL TABLE is built three ways, because a virtual table's
	// arguments are the module's language rather than SQL: CreateFTS5 and
	// CreateRTree type the two modules whose grammar is worth typing,
	// CreateGeopoly types geopoly, whose arguments are ordinary column names
	// and so are checked against the table, and CreateVirtualTable is the
	// general form for every other module, compiled in or loaded at run time,
	// whose arguments no builder here could check.
	"CREATE VIRTUAL TABLE":    "built CreateVirtualTable",
	"date and time functions": "not-a-statement",
	"DELETE":                  "built Delete",
	"DETACH DATABASE":         "operator",
	"DROP INDEX":              "built DropIndex",
	"DROP TABLE":              "built DropTable",
	"DROP TRIGGER":            "built DropTrigger",
	"DROP VIEW":               "built DropView",
	"END TRANSACTION":         "not-ddl",
	"EXPLAIN":                 "not-ddl",
	"expression":              "not-a-statement",
	"INDEXED BY":              "built IndexedBy",
	"INSERT":                  "built Insert",
	"keywords":                "not-a-statement",
	"math functions":          "not-a-statement",
	"ON CONFLICT clause":      "built OnConflict",
	"REINDEX":                 "operator",
	"RELEASE SAVEPOINT":       "not-ddl",
	"REPLACE":                 "built InsertOr",
	"RETURNING clause":        "built InsertReturning",
	"ROLLBACK TRANSACTION":    "not-ddl",
	"SAVEPOINT":               "not-ddl",
	"SELECT":                  "built Select",
	"UPDATE":                  "built Update",
	"UPSERT":                  "built OnConflict",
	"VACUUM":                  "operator",
	"WITH clause":             "core With",
	// PRAGMA is built for every pragma that configures a database a library
	// owns: the header values, the two foreign-key switches, journal_mode,
	// synchronous, busy_timeout, cache_size, temp_store, mmap_size, page_size,
	// auto_vacuum, secure_delete, recursive_triggers and legacy_alter_table,
	// plus the three checks. What stays out stays out for a reason written at
	// the top of pragma.go, and none of it is a pragma this module could not
	// render: the introspection pragmas are reads and belong in a SELECT, the
	// maintenance and resource pragmas are decisions about a running database
	// rather than about a schema, writable_schema and ignore_check_constraints
	// exist to break the guarantees the rest of this package makes, and the
	// rest are deprecated or already no-ops.
	"PRAGMA": "built SetJournalMode",
}

// namesIn is every exported package-level name in dir, grouped declarations
// included, and every exported method name.
func namesIn(t *testing.T, dir string) (names, methods map[string]bool) {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	decl := regexp.MustCompile(`(?m)^(?:func|type|var|const)\s+([A-Z]\w*)|^\t([A-Z]\w*)\s+=`)
	meth := regexp.MustCompile(`(?m)^func \([^)]+\) ([A-Z]\w*)\(`)
	names, methods = map[string]bool{}, map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range decl.FindAllStringSubmatch(string(src), -1) {
			names[m[1]+m[2]] = true
		}
		for _, m := range meth.FindAllStringSubmatch(string(src), -1) {
			methods[m[1]] = true
		}
	}
	return names, methods
}

// TestDetector_EverySQLiteStatementIsClassified fails on an index entry with
// no status, a status for an entry SQLite no longer lists, a malformed status,
// and a built or core name that does not exist. It fails closed on an index
// that parsed to nothing.
func TestDetector_EverySQLiteStatementIsClassified(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "lang-index.txt"))
	if err != nil {
		t.Fatal(err)
	}
	var entries []string
	for _, l := range strings.Split(string(b), "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
			entries = append(entries, l)
		}
	}
	if len(entries) == 0 {
		t.Fatal("parsed no entry from testdata; the parser is broken, not the list empty")
	}
	own, _ := namesIn(t, ".")
	_, coreMethods := namesIn(t, filepath.Join("..", "stmt"))

	seen := map[string]bool{}
	var missing []string
	for _, e := range entries {
		seen[e] = true
		if _, ok := sqliteIndex[e]; !ok {
			missing = append(missing, e)
		}
	}
	sort.Strings(missing)
	for _, m := range missing {
		t.Errorf("SQLite lists %q and it has no status here", m)
	}
	for e, st := range sqliteIndex {
		if !seen[e] {
			t.Errorf("%q has a status but SQLite's index does not list it", e)
		}
		kind, detail, _ := strings.Cut(st, " ")
		switch kind {
		case "built":
			if !own[detail] {
				t.Errorf("%q is marked built by %s, which this package does not export", e, detail)
			}
		case "partial":
			name, missing, _ := strings.Cut(detail, " ")
			if !own[name] {
				t.Errorf("%q is marked partial by %s, which this package does not export", e, name)
			}
			if strings.TrimSpace(missing) == "" {
				t.Errorf("%q is partial without saying what is missing", e)
			}
		case "core":
			if !coreMethods[detail] {
				t.Errorf("%q is marked core %s, which stmt does not define", e, detail)
			}
		case "planned":
			if len(detail) != 1 || detail < "1" || detail > "4" {
				t.Errorf("%q is planned without a tier from 1 to 4: %q", e, st)
			}
		case "operator", "not-ddl", "not-a-statement":
		default:
			t.Errorf("%q has an unknown status %q", e, st)
		}
	}
}
