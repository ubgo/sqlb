package vet_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"

	"github.com/ubgo/sqlb/contrib/vet"
	"github.com/ubgo/sqlb/render"
	"github.com/ubgo/sqlb/schema"
)

var errBoom = errors.New("boom")

// failedAsMeant reports whether err is the injected failure: errBoom when one
// was injected, or any error that is not a query the fake did not expect.
func failedAsMeant(err error, injected bool) bool {
	if err == nil || errors.Is(err, errUnexpected) {
		return false
	}
	return !injected || errors.Is(err, errBoom)
}

func injected(qs []fakeQuery) bool {
	for _, q := range qs {
		if q.err != nil || q.rowErr != nil || q.closeErr != nil {
			return true
		}
	}
	return false
}

var catalogueCols = []string{"table_schema", "table_name", "column_name", "data_type", "nullable", "is_default", "column_default", "extra", "column_type"}

// TestReadCatalogue_ParsesEachEngine runs the Postgres and MySQL readers on
// canned catalogue rows, so their parsing runs on every go test and not only
// against a server: a default-schema table is under both keys, another
// schema's only qualified, a NULL default is none, a MySQL literal is quoted
// and a MySQL expression kept.
func TestReadCatalogue_ParsesEachEngine(t *testing.T) {
	ctx := context.Background()
	pg := (&faultDB{queries: []fakeQuery{{match: "information_schema.columns", cols: catalogueCols, rows: [][]driver.Value{
		{"public", "users", "id", "bigint", false, true, nil, "", ""},
		{"public", "users", "status", "text", false, true, "'a'::text", "", ""},
		{"auth", "users", "id", "text", true, false, nil, "", ""},
	}}}}).open()
	cat, err := vet.ReadCatalogue(ctx, pg, render.DialectPostgres)
	if err != nil {
		t.Fatal(err)
	}
	if cat["users"]["id"].Type != "bigint" || cat["public.users"]["id"].Type != "bigint" || cat["auth.users"]["id"].Type != "text" {
		t.Fatalf("keys: %+v", cat)
	}
	if cat["users"]["id"].Default != nil || *cat["users"]["status"].Default != "'a'::text" || !cat["auth.users"]["id"].Nullable {
		t.Fatalf("columns: %+v", cat)
	}

	my := (&faultDB{queries: []fakeQuery{{match: "information_schema.columns", cols: catalogueCols, rows: [][]driver.Value{
		{"app", "t", "s", "varchar", true, true, "it's", "", ""},
		{"app", "t", "at", "datetime", true, true, "utc_timestamp()", "DEFAULT_GENERATED", ""},
	}}}}).open()
	cat, err = vet.ReadCatalogue(ctx, my, render.DialectMySQL)
	if err != nil {
		t.Fatal(err)
	}
	if *cat["t"]["s"].Default != "'it''s'" || *cat["t"]["at"].Default != "utc_timestamp()" {
		t.Fatalf("mysql defaults: %q %q", *cat["t"]["s"].Default, *cat["t"]["at"].Default)
	}
	if _, err := vet.ReadCatalogue(ctx, my, "oracle"); err == nil {
		t.Error("an unknown dialect was read")
	}
}

// TestReadCatalogue_FailsOnEveryDatabaseError drives each error return: the
// query refused, a row that fails mid-read, a row that does not scan, and
// Close failing, on the information_schema reader and the SQLite reader.
func TestReadCatalogue_FailsOnEveryDatabaseError(t *testing.T) {
	ctx := context.Background()
	row := []driver.Value{"public", "t", "c", "text", true, true, nil, "", ""}
	for name, q := range map[string]fakeQuery{
		"query refused":     {match: "information_schema.columns", err: errBoom},
		"row fails":         {match: "information_schema.columns", cols: catalogueCols, rows: [][]driver.Value{row, row}, failAt: 2, rowErr: errBoom},
		"row does not scan": {match: "information_schema.columns", cols: catalogueCols[:3], rows: [][]driver.Value{{"a", "b", "c"}}},
	} {
		if _, err := vet.ReadCatalogue(ctx, (&faultDB{queries: []fakeQuery{q}}).open(), render.DialectPostgres); !failedAsMeant(err, injected([]fakeQuery{q})) {
			t.Errorf("postgres, %s: %v", name, err)
		}
	}
	tables := fakeQuery{match: "pragma_table_list", cols: []string{"name", "strict", "wr"}, rows: [][]driver.Value{{"t", int64(0), int64(0)}}}
	pkIndex := fakeQuery{match: "pragma_index_list", cols: []string{"n"}, rows: [][]driver.Value{{int64(0)}}}
	columns := fakeQuery{match: "pragma_table_info", cols: []string{"name", "type", "notnull", "pk", "dflt"}, rows: [][]driver.Value{{"c", "text", int64(0), int64(0), nil}}}
	for name, qs := range map[string][]fakeQuery{
		"tables refused":       {{match: "pragma_table_list", err: errBoom}},
		"tables row fails":     {{match: "pragma_table_list", cols: tables.cols, rows: tables.rows, failAt: 1, rowErr: errBoom}},
		"tables close fails":   {{match: "pragma_table_list", cols: tables.cols, rows: tables.rows, closeErr: errBoom}},
		"tables do not scan":   {{match: "pragma_table_list", cols: []string{"name"}, rows: [][]driver.Value{{"t"}}}},
		"pk index refused":     {tables, {match: "pragma_index_list", err: errBoom}},
		"columns refused":      {tables, pkIndex, {match: "pragma_table_info", err: errBoom}},
		"column row fails":     {tables, pkIndex, {match: "pragma_table_info", cols: columns.cols, rows: columns.rows, failAt: 1, rowErr: errBoom}},
		"columns close fails":  {tables, pkIndex, {match: "pragma_table_info", cols: columns.cols, rows: columns.rows, closeErr: errBoom}},
		"column does not scan": {tables, pkIndex, {match: "pragma_table_info", cols: []string{"name"}, rows: [][]driver.Value{{"c"}}}},
	} {
		if _, err := vet.ReadCatalogue(ctx, (&faultDB{queries: qs}).open(), render.DialectSQLite); !failedAsMeant(err, injected(qs)) {
			t.Errorf("sqlite, %s: %v", name, err)
		}
	}
	if _, err := vet.ReadCatalogue(ctx, (&faultDB{queries: []fakeQuery{tables, pkIndex, columns}}).open(), render.DialectSQLite); err != nil {
		t.Errorf("the control failed, so the cases above test nothing: %v", err)
	}
	if _, err := vet.ReadCatalogue(ctx, (&faultDB{connectErr: errBoom}).open(), render.DialectPostgres); !errors.Is(err, errBoom) {
		t.Error("a refused connection read a catalogue")
	}
}

// TestReadKeys_ParsesAndFails runs the Postgres and MySQL key readers on
// canned rows, and drives every error return of all three.
func TestReadKeys_ParsesAndFails(t *testing.T) {
	ctx := context.Background()
	sep := "\x1f"
	pgIdx := fakeQuery{match: "FROM pg_index", cols: []string{"s", "t", "p", "u", "c", "n"}, rows: [][]driver.Value{
		{"public", "orders", true, true, "a" + sep + "b", "orders_pkey"},
		{"public", "orders", false, true, "code", "orders_code"},
		{"auth", "users", true, true, "id", "users_pkey"},
		{"public", "bare", false, false, nil, "bare_idx"},
	}}
	pgFK := fakeQuery{match: "FROM pg_constraint", cols: []string{"s", "t", "n", "rs", "rt", "d", "u", "c", "rc"}, rows: [][]driver.Value{
		{"public", "orders", "orders_user_fk", "auth", "users", "c", "a", "user_id", "id"},
	}}
	pgDef := fakeQuery{match: "SELECT current_schema()", cols: []string{"s"}, rows: [][]driver.Value{{"public"}}}
	keys, err := vet.ReadKeys(ctx, (&faultDB{queries: []fakeQuery{pgDef, pgIdx, pgFK}}).open(), render.DialectPostgres)
	if err != nil {
		t.Fatal(err)
	}
	o := keys.Tables["orders"]
	if strings.Join(o.PrimaryKey, ",") != "a,b" || len(o.Indexes) != 1 || !o.Indexes[0].Unique || keys.Tables["auth.users"].PrimaryKey[0] != "id" {
		t.Fatalf("pg keys: %+v", keys)
	}
	if ix := keys.Tables["bare"].Indexes; len(ix) != 1 || ix[0].Columns != nil {
		t.Fatalf("a NULL column list must read as no columns: %+v", ix)
	}
	if fk := o.ForeignKeys[0]; fk.RefTable != "auth.users" || fk.OnDelete != schema.Cascade || fk.OnUpdate != schema.NoAction {
		t.Fatalf("pg foreign key: %+v", fk)
	}
	badAction := pgFK
	badAction.rows = [][]driver.Value{{"public", "orders", "fk", "public", "users", "z", "a", "user_id", "id"}}
	if _, err := vet.ReadKeys(ctx, (&faultDB{queries: []fakeQuery{pgDef, pgIdx, badAction}}).open(), render.DialectPostgres); !errors.Is(err, vet.ErrUnknownAction) {
		t.Errorf("an unknown action letter: %v", err)
	}

	myDef := fakeQuery{match: "SELECT DATABASE()", cols: []string{"d"}, rows: [][]driver.Value{{"app"}}}
	myIdx := fakeQuery{match: "information_schema.statistics", cols: []string{"s", "t", "i", "u", "c"}, rows: [][]driver.Value{
		{"app", "orders", "PRIMARY", true, "id"},
		{"app", "orders", "orders_code", true, "code"},
		{"app", "orders", "orders_expr", false, nil},
		{"app", "orders", "orders_ab", false, "a"},
		{"app", "orders", "orders_ab", false, "b"},
	}}
	myFK := fakeQuery{match: "key_column_usage", cols: []string{"s", "t", "n", "c", "rs", "rt", "rc", "d", "u"}, rows: [][]driver.Value{
		{"app", "orders", "fk", "a", "app", "p", "x", "CASCADE", "NO ACTION"},
		{"app", "orders", "fk", "b", "app", "p", "y", "CASCADE", "NO ACTION"},
	}}
	keys, err = vet.ReadKeys(ctx, (&faultDB{queries: []fakeQuery{myDef, myIdx, myFK}}).open(), render.DialectMySQL)
	if err != nil {
		t.Fatal(err)
	}
	o = keys.Tables["orders"]
	if o.PrimaryKey[0] != "id" || len(o.Indexes) != 2 || strings.Join(o.Indexes[1].Columns, ",") != "a,b" {
		t.Fatalf("mysql indexes (the expression index must be skipped): %+v", o.Indexes)
	}
	if fk := o.ForeignKeys[0]; strings.Join(fk.Columns, ",") != "a,b" || strings.Join(fk.RefColumns, ",") != "x,y" || fk.RefTable != "app.p" {
		t.Fatalf("mysql foreign key: %+v", fk)
	}

	for name, c := range map[string]struct {
		dialect string
		qs      []fakeQuery
	}{
		"pg default schema refused":  {render.DialectPostgres, []fakeQuery{{match: "current_schema", err: errBoom}}},
		"pg indexes refused":         {render.DialectPostgres, []fakeQuery{pgDef, {match: "FROM pg_index", err: errBoom}}},
		"pg index row fails":         {render.DialectPostgres, []fakeQuery{pgDef, {match: "FROM pg_index", cols: pgIdx.cols, rows: pgIdx.rows, failAt: 1, rowErr: errBoom}}},
		"pg index does not scan":     {render.DialectPostgres, []fakeQuery{pgDef, {match: "FROM pg_index", cols: []string{"s"}, rows: [][]driver.Value{{"x"}}}}},
		"pg foreign keys refused":    {render.DialectPostgres, []fakeQuery{pgDef, pgIdx, {match: "FROM pg_constraint", err: errBoom}}},
		"pg foreign key no scan":     {render.DialectPostgres, []fakeQuery{pgDef, pgIdx, {match: "FROM pg_constraint", cols: []string{"s"}, rows: [][]driver.Value{{"x"}}}}},
		"mysql database refused":     {render.DialectMySQL, []fakeQuery{{match: "DATABASE()", err: errBoom}}},
		"mysql indexes refused":      {render.DialectMySQL, []fakeQuery{myDef, {match: "statistics", err: errBoom}}},
		"mysql index does not scan":  {render.DialectMySQL, []fakeQuery{myDef, {match: "statistics", cols: []string{"s"}, rows: [][]driver.Value{{"x"}}}}},
		"mysql foreign keys refused": {render.DialectMySQL, []fakeQuery{myDef, myIdx, {match: "key_column_usage", err: errBoom}}},
		"mysql foreign key no scan":  {render.DialectMySQL, []fakeQuery{myDef, myIdx, {match: "key_column_usage", cols: []string{"s"}, rows: [][]driver.Value{{"x"}}}}},
		"mysql close fails":          {render.DialectMySQL, []fakeQuery{myDef, myIdx, {match: "key_column_usage", cols: myFK.cols, rows: myFK.rows, closeErr: errBoom}}},
		"sqlite tables refused":      {render.DialectSQLite, []fakeQuery{{match: "pragma_table_list", err: errBoom}}},
		"sqlite tables no scan":      {render.DialectSQLite, []fakeQuery{{match: "pragma_table_list", cols: []string{"a", "b"}, rows: [][]driver.Value{{"t", "x"}}}}},
		"sqlite pk refused": {render.DialectSQLite, []fakeQuery{
			{match: "pragma_table_list", cols: []string{"name"}, rows: [][]driver.Value{{"t"}}}, {match: "pragma_table_info", err: errBoom}}},
		"sqlite pk no scan": {render.DialectSQLite, []fakeQuery{
			{match: "pragma_table_list", cols: []string{"name"}, rows: [][]driver.Value{{"t"}}},
			{match: "pragma_table_info", cols: []string{"a", "b"}, rows: [][]driver.Value{{"x", "y"}}}}},
		"sqlite index list refused": {render.DialectSQLite, []fakeQuery{
			{match: "pragma_table_list", cols: []string{"name"}, rows: [][]driver.Value{{"t"}}},
			{match: "pragma_table_info", cols: []string{"name"}}, {match: "pragma_index_list", err: errBoom}}},
		"sqlite index list no scan": {render.DialectSQLite, []fakeQuery{
			{match: "pragma_table_list", cols: []string{"name"}, rows: [][]driver.Value{{"t"}}},
			{match: "pragma_table_info", cols: []string{"name"}}, {match: "pragma_index_list", cols: []string{"a"}, rows: [][]driver.Value{{"x"}}}}},
		"sqlite index info refused": {render.DialectSQLite, []fakeQuery{
			{match: "pragma_table_list", cols: []string{"name"}, rows: [][]driver.Value{{"t"}}},
			{match: "pragma_table_info", cols: []string{"name"}},
			{match: "pragma_index_list", cols: []string{"name", "unique"}, rows: [][]driver.Value{{"i", int64(1)}}},
			{match: "pragma_index_info", err: errBoom}}},
		"sqlite index info no scan": {render.DialectSQLite, []fakeQuery{
			{match: "pragma_table_list", cols: []string{"name"}, rows: [][]driver.Value{{"t"}}},
			{match: "pragma_table_info", cols: []string{"name"}},
			{match: "pragma_index_list", cols: []string{"name", "unique"}, rows: [][]driver.Value{{"i", int64(1)}}},
			{match: "pragma_index_info", cols: []string{"a", "b"}, rows: [][]driver.Value{{"x", "y"}}}}},
		"sqlite foreign keys refused": {render.DialectSQLite, []fakeQuery{
			{match: "pragma_table_list", cols: []string{"name"}, rows: [][]driver.Value{{"t"}}},
			{match: "pragma_table_info", cols: []string{"name"}}, {match: "pragma_index_list", cols: []string{"name", "unique"}},
			{match: "pragma_foreign_key_list", err: errBoom}}},
		"sqlite foreign key no scan": {render.DialectSQLite, []fakeQuery{
			{match: "pragma_table_list", cols: []string{"name"}, rows: [][]driver.Value{{"t"}}},
			{match: "pragma_table_info", cols: []string{"name"}}, {match: "pragma_index_list", cols: []string{"name", "unique"}},
			{match: "pragma_foreign_key_list", cols: []string{"a"}, rows: [][]driver.Value{{"x"}}}}},
		"unknown dialect": {"oracle", nil},
	} {
		_, err := vet.ReadKeys(ctx, (&faultDB{queries: c.qs}).open(), c.dialect)
		if c.dialect == "oracle" {
			if err == nil {
				t.Errorf("%s: no error", name)
			}
			continue
		}
		if !failedAsMeant(err, injected(c.qs)) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := vet.CheckTables(ctx, (&faultDB{queries: []fakeQuery{{match: "information_schema.columns", err: errBoom}}}).open(), render.DialectPostgres, nil); err == nil {
		t.Error("CheckTables: a refused catalogue passed")
	}
	if _, err := vet.CheckTables(ctx, (&faultDB{queries: []fakeQuery{{match: "information_schema.columns", cols: catalogueCols}, {match: "current_schema", err: errBoom}}}).open(), render.DialectPostgres, nil); err == nil {
		t.Error("CheckTables: refused keys passed")
	}
}

// TestSchema_FailsWhereItsPartsFail pins that the --dsn mode surfaces each
// part's failure: packages that do not load, and a catalogue that cannot be
// read, rather than an empty diff that reads as clean.
func TestSchema_FailsWhereItsPartsFail(t *testing.T) {
	ctx := context.Background()
	db := (&faultDB{}).open()
	if _, err := vet.Schema(ctx, db, render.DialectSQLite, ".", "./internal/probe/nosuchpackage"); err == nil {
		t.Error("a package that does not load passed")
	}
	refused := (&faultDB{queries: []fakeQuery{{match: "pragma_table_list", err: errBoom}}}).open()
	if _, err := vet.Schema(ctx, refused, render.DialectSQLite, ".", "./internal/probe/schema"); !errors.Is(err, errBoom) {
		t.Errorf("a refused catalogue: %v", err)
	}
}
