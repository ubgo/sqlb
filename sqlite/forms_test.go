package sqlite_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/sqlite"
)

// The forms inside a statement this package already built: TEMP, schema
// qualification, a view's column list, a key's collation and sort order, a
// column-level primary key, deferrability and RAISE.
//
// The detector records one constructor per statement, so an entry marked built
// says a statement has an entry point and says nothing about which of its
// forms render. Each case here is a form SQLite's grammar carries that this
// package could not write before, and each is executed in
// contrib/driver-sqlite as well, because a form that renders and has never run
// is a claim.

var (
	fmNotes    = sqlb.Table{Name: "notes", Alias: "n"}
	fmNoteID   = sqlb.Col[int64](fmNotes, "id")
	fmNoteBody = sqlb.Col[string](fmNotes, "body")
	fmMail     = sqlb.Col[string](fmNotes, "email")
	// A view is read through a table of its own name, so its columns are
	// declared on one and the same values name the columns in CREATE VIEW.
	fmView     = sqlite.View{Name: "recent_notes"}
	fmViewT    = sqlb.Table{Name: "recent_notes"}
	fmViewID   = sqlb.Col[int64](fmViewT, "note_id")
	fmViewBody = sqlb.Col[string](fmViewT, "text")
)

func builtSQL(t *testing.T, built func() (ddl.Script, error)) string {
	t.Helper()
	s, err := built()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if len(s) != 1 {
		t.Fatalf("want one statement, got %d", len(s))
	}
	return s[0].SQL
}

// TestForms_TemporaryAndSchema pins the two placements SQLite's grammar gives
// every object: TEMP between CREATE and the object keyword, and a schema
// qualifying the object's own name.
//
// Qualification was a silent wrong answer before, not an absence: a View,
// Index or Trigger had nowhere to put a schema, and CreateFTS5, CreateRTree
// and CreateVirtualTable took a table that HAD one and dropped it, so a
// statement meant for the temp database created its table in main instead.
func TestForms_TemporaryAndSchema(t *testing.T) {
	tempNotes := sqlb.Table{Name: "notes", Schema: "temp"}
	tempBody := sqlb.Col[string](tempNotes, "body")
	cases := []struct {
		name  string
		built func() (ddl.Script, error)
		want  string
	}{
		{"temp view",
			sqlite.CreateView(fmView).Temporary().As(sqlite.Select(fmNoteID).From(fmNotes)).Build,
			`CREATE TEMPORARY VIEW "recent_notes" AS SELECT "n"."id" FROM "notes" AS "n"`},
		{"view in a schema",
			sqlite.CreateView(sqlite.View{Name: "v", Schema: "main"}).As(sqlite.Select(fmNoteID).From(fmNotes)).Build,
			`CREATE VIEW "main"."v" AS SELECT "n"."id" FROM "notes" AS "n"`},
		{"index in a schema",
			sqlite.CreateIndex(sqlite.Index{Name: "ix", Schema: "main"}, fmNotes).On(sqlite.IndexKey(fmNoteID)).Build,
			`CREATE INDEX "main"."ix" ON "notes" ("id")`},
		{"drop an index in a schema",
			sqlite.DropIndex(sqlite.Index{Name: "ix", Schema: "main"}).IfExists().Build,
			`DROP INDEX IF EXISTS "main"."ix"`},
		{"drop a view in a schema",
			sqlite.DropView(sqlite.View{Name: "v", Schema: "main"}).Build,
			`DROP VIEW "main"."v"`},
		{"drop a trigger in a schema",
			sqlite.DropTrigger(sqlite.Trigger{Name: "tr", Schema: "main"}).Build,
			`DROP TRIGGER "main"."tr"`},
		{"temp trigger",
			sqlite.CreateTrigger(sqlite.Trigger{Name: "tr"}).Temporary().
				After(sqlite.OnInsert(), fmNotes).Do(sqlite.StepSelect(sqlite.Select(fmNoteID).From(fmNotes))).Build,
			`CREATE TEMPORARY TRIGGER "tr" AFTER INSERT ON "notes" FOR EACH ROW BEGIN SELECT "n"."id" FROM "notes" AS "n"; END`},
		{"trigger in a schema",
			sqlite.CreateTrigger(sqlite.Trigger{Name: "tr", Schema: "main"}).
				After(sqlite.OnInsert(), fmNotes).Do(sqlite.StepSelect(sqlite.Select(fmNoteID).From(fmNotes))).Build,
			`CREATE TRIGGER "main"."tr" AFTER INSERT ON "notes" FOR EACH ROW BEGIN SELECT "n"."id" FROM "notes" AS "n"; END`},
		{"a virtual table in a schema",
			sqlite.CreateVirtualTable(tempNotes, sqlite.ModuleDBStat).Build,
			`CREATE VIRTUAL TABLE "temp"."notes" USING dbstat`},
		{"an FTS5 table in a schema",
			sqlite.CreateFTS5(tempNotes).Columns(tempBody).Build,
			`CREATE VIRTUAL TABLE "temp"."notes" USING fts5("body")`},
		{"a geopoly table in a schema",
			sqlite.CreateGeopoly(tempNotes, tempBody).Build,
			`CREATE VIRTUAL TABLE "temp"."notes" USING geopoly("body")`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := builtSQL(t, tc.built); got != tc.want {
				t.Errorf("got  %s\nwant %s", got, tc.want)
			}
		})
	}
}

// TestForms_TemporaryRefusesAnotherSchema pins the engine's rule rather than a
// guess: SQLite answers "temporary table name must be unqualified" for a
// temporary object named in any schema but temp, so Build refuses it.
func TestForms_TemporaryRefusesAnotherSchema(t *testing.T) {
	_, err := sqlite.CreateView(sqlite.View{Name: "v", Schema: "main"}).Temporary().
		As(sqlite.Select(fmNoteID).From(fmNotes)).Build()
	if !errors.Is(err, sqlite.ErrTemporaryQualified) {
		t.Errorf("a temp view in main built with %v", err)
	}
	_, err = sqlite.CreateTrigger(sqlite.Trigger{Name: "tr", Schema: "main"}).Temporary().
		After(sqlite.OnInsert(), fmNotes).Do(sqlite.StepSelect(sqlite.Select(fmNoteID).From(fmNotes))).Build()
	if !errors.Is(err, sqlite.ErrTemporaryQualified) {
		t.Errorf("a temp trigger in main built with %v", err)
	}
	// temp itself is the one schema a temporary object may name.
	if _, err := sqlite.CreateView(sqlite.View{Name: "v", Schema: "temp"}).Temporary().
		As(sqlite.Select(fmNoteID).From(fmNotes)).Build(); err != nil {
		t.Errorf("a temp view in temp was refused: %v", err)
	}
	// A temporary table checks the whole table, since the name it must not
	// qualify is the table's rather than the clause's.
	_, err = sqlite.CreateTable(sqlb.Table{Name: "t", Schema: "main"}).
		Column(ddl.Column(sqlb.Col[int64](sqlb.Table{Name: "t", Schema: "main"}, "id"))).
		Clause(sqlite.Temporary()).Build()
	if !errors.Is(err, sqlite.ErrTemporaryTable) {
		t.Errorf("a temp table in main built with %v", err)
	}
}

// TestForms_TemporaryTable pins where TEMP is written: between CREATE and
// TABLE, which is the one position an engine clause has there.
func TestForms_TemporaryTable(t *testing.T) {
	scratch := sqlb.Table{Name: "scratch"}
	id := sqlb.Col[int64](scratch, "id")
	got := builtSQL(t, sqlite.CreateTable(scratch).IfNotExists().Column(ddl.Column(id)).Clause(sqlite.Temporary()).Build)
	want := `CREATE TEMPORARY TABLE IF NOT EXISTS "scratch" ("id" INTEGER NOT NULL)`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

// TestForms_ViewColumnList pins the view's own column names, and that they are
// checked against the view the way a key is checked against its table.
func TestForms_ViewColumnList(t *testing.T) {
	got := builtSQL(t, sqlite.CreateView(fmView).Columns(fmViewID, fmViewBody).
		As(sqlite.Select(fmNoteID, fmNoteBody).From(fmNotes)).Build)
	want := `CREATE VIEW "recent_notes" ("note_id", "text") AS SELECT "n"."id", "n"."body" FROM "notes" AS "n"`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	// The same names added after the body, which is the other door.
	if got := builtSQL(t, sqlite.CreateView(fmView).As(sqlite.Select(fmNoteID, fmNoteBody).From(fmNotes)).
		Columns(fmViewID, fmViewBody).Build); got != want {
		t.Errorf("after As: got %s", got)
	}
	// A column of another table names something the view does not have.
	if _, err := sqlite.CreateView(fmView).Columns(fmNoteID).
		As(sqlite.Select(fmNoteID).From(fmNotes)).Build(); !errors.Is(err, ddl.ErrForeignColumn) {
		t.Errorf("a column of another table: %v", err)
	}
}

// TestForms_KeyColumnOptions pins a table-level key's collation and sort
// order. UNIQUE(email COLLATE NOCASE) is a different constraint from
// UNIQUE(email), and it was unwritable before.
func TestForms_KeyColumnOptions(t *testing.T) {
	cases := []struct {
		name string
		key  sqlite.KeyClause
		want string
	}{
		{"unique, case-insensitive",
			sqlite.UniqueOn(sqlite.Key(fmMail).Collate(sqlite.NoCase)),
			`UNIQUE ("email" COLLATE NOCASE)`},
		{"unique with a conflict resolution",
			sqlite.UniqueOn(sqlite.Key(fmMail).Collate(sqlite.NoCase)).OnConflict(sqlite.OrIgnore),
			`UNIQUE ("email" COLLATE NOCASE) ON CONFLICT IGNORE`},
		{"primary key, descending",
			sqlite.PrimaryKeyOn(sqlite.Key(fmNoteID).Desc()),
			`PRIMARY KEY ("id" DESC)`},
		{"a compound key, decorated both ways",
			sqlite.PrimaryKeyOn(sqlite.Key(fmMail).Collate(sqlite.NoCase).Asc(), sqlite.Key(fmNoteID).Desc()),
			`PRIMARY KEY ("email" COLLATE NOCASE ASC, "id" DESC)`},
		{"named, decorated",
			sqlite.UniqueOn(sqlite.Key(fmMail).Collate(sqlite.RTrim)).
				Named(sqlite.TableConstraint{Table: fmNotes, Name: "notes_email"}),
			`CONSTRAINT "notes_email" UNIQUE ("email" COLLATE RTRIM)`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var b strings.Builder
			a := sqlb.NewInlineArgs(sqlite.Dialect{})
			tc.key.WriteSQL(&b, a)
			if err := a.Err(); err != nil {
				t.Fatal(err)
			}
			if b.String() != tc.want {
				t.Errorf("got  %s\nwant %s", b.String(), tc.want)
			}
		})
	}
	// An unknown collation is refused rather than rendered as an empty word.
	var b strings.Builder
	a := sqlb.NewInlineArgs(sqlite.Dialect{})
	sqlite.UniqueOn(sqlite.Key(fmMail).Collate(sqlite.Collation(99))).WriteSQL(&b, a)
	if !errors.Is(a.Err(), sqlite.ErrUnknownKeyword) {
		t.Errorf("an unknown collation rendered with %v", a.Err())
	}
}

// TestForms_ColumnKey pins the column-level primary key, whose Desc form is
// the one that stops an INTEGER column being an alias for the rowid.
func TestForms_ColumnKey(t *testing.T) {
	cases := []struct {
		name string
		key  sqlite.ColumnKeyClause
		want string
	}{
		{"plain", sqlite.ColumnKey(fmNoteID), `PRIMARY KEY`},
		{"descending", sqlite.ColumnKey(fmNoteID).Desc(), `PRIMARY KEY DESC`},
		{"ascending", sqlite.ColumnKey(fmNoteID).Asc(), `PRIMARY KEY ASC`},
		{"resolving a conflict", sqlite.ColumnKey(fmMail).OnConflict(sqlite.OrReplace), `PRIMARY KEY ON CONFLICT REPLACE`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var b strings.Builder
			a := sqlb.NewInlineArgs(sqlite.Dialect{})
			tc.key.WriteSQL(&b, a)
			if err := a.Err(); err != nil {
				t.Fatal(err)
			}
			if b.String() != tc.want {
				t.Errorf("got %s, want %s", b.String(), tc.want)
			}
		})
	}
	got := builtSQL(t, sqlite.CreateTable(fmNotes).
		Column(ddl.Column(fmNoteID), ddl.Column(fmMail)).
		Clause(sqlite.ColumnKey(fmMail).OnConflict(sqlite.OrReplace)).Build)
	want := `CREATE TABLE "notes" ("id" INTEGER NOT NULL, "email" TEXT NOT NULL PRIMARY KEY ON CONFLICT REPLACE)`
	if got != want {
		t.Errorf("in a table: got  %s\nwant %s", got, want)
	}
}

// TestForms_Deferrability pins all three settings. Only the deferred one
// changes when the check happens; the other two are recorded in the schema
// and read back by anything that reads sqlite_schema.
func TestForms_Deferrability(t *testing.T) {
	other := sqlb.Table{Name: "authors"}
	otherID := sqlb.Col[int64](other, "id")
	local := sqlb.Col[int64](fmNotes, "author_id")
	cases := []struct {
		name string
		d    sqlite.Deferrability
		want string
	}{
		{"not deferrable", sqlite.NotDeferrable, `NOT DEFERRABLE`},
		{"initially deferred", sqlite.DeferrableInitiallyDeferred, `DEFERRABLE INITIALLY DEFERRED`},
		{"initially immediate", sqlite.DeferrableInitiallyImmediate, `DEFERRABLE INITIALLY IMMEDIATE`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var b strings.Builder
			a := sqlb.NewInlineArgs(sqlite.Dialect{})
			sqlite.References(local, otherID).Deferrable(tc.d).WriteSQL(&b, a)
			if err := a.Err(); err != nil {
				t.Fatal(err)
			}
			want := `FOREIGN KEY ("author_id") REFERENCES "authors" ("id") ` + tc.want
			if b.String() != want {
				t.Errorf("got  %s\nwant %s", b.String(), want)
			}
		})
	}
	// Deferred is the shorthand for the one setting that changes behaviour.
	var short, long strings.Builder
	a := sqlb.NewInlineArgs(sqlite.Dialect{})
	sqlite.References(local, otherID).Deferred().WriteSQL(&short, a)
	sqlite.References(local, otherID).Deferrable(sqlite.DeferrableInitiallyDeferred).WriteSQL(&long, a)
	if short.String() != long.String() {
		t.Errorf("Deferred is not Deferrable(DeferrableInitiallyDeferred):\n%s\n%s", short.String(), long.String())
	}
	var bad strings.Builder
	a = sqlb.NewInlineArgs(sqlite.Dialect{})
	sqlite.References(local, otherID).Deferrable(sqlite.Deferrability(99)).WriteSQL(&bad, a)
	if !errors.Is(a.Err(), sqlite.ErrUnknownKeyword) {
		t.Errorf("an unknown deferrability rendered with %v", a.Err())
	}
}

// TestForms_Raise pins RAISE, the one way a trigger refuses a row for a reason
// a CHECK cannot express.
func TestForms_Raise(t *testing.T) {
	cases := []struct {
		name string
		e    sqlb.Expr[any]
		want string
	}{
		{"rollback", sqlite.Raise(sqlite.RaiseRollback, "no"), `RAISE(ROLLBACK, 'no')`},
		{"abort", sqlite.Raise(sqlite.RaiseAbort, "no"), `RAISE(ABORT, 'no')`},
		{"fail", sqlite.Raise(sqlite.RaiseFail, "no"), `RAISE(FAIL, 'no')`},
		{"ignore", sqlite.RaiseIgnore(), `RAISE(IGNORE)`},
		{"a message with a quote", sqlite.Raise(sqlite.RaiseAbort, "it's bad"), `RAISE(ABORT, 'it''s bad')`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := sqlb.NewInlineArgs(sqlite.Dialect{})
			got := tc.e.SQL(a)
			if err := a.Err(); err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
	a := sqlb.NewInlineArgs(sqlite.Dialect{})
	sqlite.Raise(sqlite.RaiseAction(0), "x").SQL(a)
	if !errors.Is(a.Err(), sqlite.ErrUnknownKeyword) {
		t.Errorf("a zero RaiseAction rendered with %v", a.Err())
	}
	// Inside the trigger it belongs to.
	got := builtSQL(t, sqlite.CreateTrigger(sqlite.Trigger{Name: "notes_body_required"}).
		Before(sqlite.OnInsert(), fmNotes).
		When(sqlb.Eq(sqlite.New(fmNoteBody), "")).
		Do(sqlite.StepSelect(sqlite.Select(sqlite.Raise(sqlite.RaiseAbort, "body required")))).Build)
	want := `CREATE TRIGGER "notes_body_required" BEFORE INSERT ON "notes" FOR EACH ROW ` +
		`WHEN NEW."body" = '' BEGIN SELECT RAISE(ABORT, 'body required'); END`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

// TestForms_ChecksNarrowedToOneTable pins the table argument all three checks
// take, in both shapes: the bare pragma for foreign_key_check, and the
// table-valued form for the two that answer 'ok'.
func TestForms_ChecksNarrowedToOneTable(t *testing.T) {
	cases := []struct {
		name  string
		built func() (ddl.Script, error)
		want  string
	}{
		{"foreign keys of one table", sqlite.ForeignKeyCheckOf(fmNotes).Build,
			`PRAGMA foreign_key_check('notes')`},
		{"integrity of one table", sqlite.IntegrityCheckOf(fmNotes).Build,
			`SELECT 1 FROM pragma_integrity_check('notes') WHERE "integrity_check" <> 'ok'`},
		{"quick check of one table", sqlite.QuickCheckOf(fmNotes).Build,
			`SELECT 1 FROM pragma_quick_check('notes') WHERE "quick_check" <> 'ok'`},
		{"in a schema", sqlite.IntegrityCheckOf(sqlb.Table{Name: "notes", Schema: "main"}).Build,
			`SELECT 1 FROM "main".pragma_integrity_check('notes') WHERE "integrity_check" <> 'ok'`},
		{"foreign keys in a schema", sqlite.ForeignKeyCheckOf(sqlb.Table{Name: "notes", Schema: "main"}).Build,
			`PRAGMA "main".foreign_key_check('notes')`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := tc.built()
			if err != nil {
				t.Fatal(err)
			}
			if s[0].SQL != tc.want {
				t.Errorf("got  %s\nwant %s", s[0].SQL, tc.want)
			}
			if !s[0].MustReturnNoRows {
				t.Error("a narrowed check is still a check")
			}
		})
	}
	if _, err := sqlite.IntegrityCheckOf(sqlb.Table{}).Build(); !errors.Is(err, sqlite.ErrEmptyName) {
		t.Errorf("a table with no name built with %v", err)
	}
}

// TestForms_CreateTableAs pins the statement ddl's CREATE TABLE cannot be: a
// table whose body is a SELECT has no column list, which SQLite refuses, so it
// is a statement of its own on the pg.CreateTableLike precedent.
func TestForms_CreateTableAs(t *testing.T) {
	body := sqlite.Select(fmNoteID, fmNoteBody).From(fmNotes)
	staging := sqlb.Table{Name: "notes_staging"}
	cases := []struct {
		name  string
		built func() (ddl.Script, error)
		want  string
	}{
		{"plain", sqlite.CreateTableAs(staging, body).Build,
			`CREATE TABLE "notes_staging" AS SELECT "n"."id", "n"."body" FROM "notes" AS "n"`},
		{"if not exists", sqlite.CreateTableAs(staging, body).IfNotExists().Build,
			`CREATE TABLE IF NOT EXISTS "notes_staging" AS SELECT "n"."id", "n"."body" FROM "notes" AS "n"`},
		{"temporary", sqlite.CreateTableAs(staging, body).Temporary().Build,
			`CREATE TEMPORARY TABLE "notes_staging" AS SELECT "n"."id", "n"."body" FROM "notes" AS "n"`},
		{"in a schema", sqlite.CreateTableAs(sqlb.Table{Name: "s", Schema: "main"}, body).Build,
			`CREATE TABLE "main"."s" AS SELECT "n"."id", "n"."body" FROM "notes" AS "n"`},
		{"temporary and both", sqlite.CreateTableAs(staging, body).Temporary().IfNotExists().Build,
			`CREATE TEMPORARY TABLE IF NOT EXISTS "notes_staging" AS SELECT "n"."id", "n"."body" FROM "notes" AS "n"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := builtSQL(t, tc.built); got != tc.want {
				t.Errorf("got  %s\nwant %s", got, tc.want)
			}
		})
	}
	if _, err := sqlite.CreateTableAs(sqlb.Table{Name: "x", Schema: "main"}, body).Temporary().Build(); !errors.Is(err, sqlite.ErrTemporaryTable) {
		t.Errorf("a temporary table in main built with %v", err)
	}
	if _, err := sqlite.CreateTableAs(staging, nil).Build(); !errors.Is(err, ddl.ErrZeroValue) {
		t.Errorf("no body: %v", err)
	}
	// Both methods derive; the receiver is untouched.
	base := sqlite.CreateTableAs(staging, body)
	before := builtSQL(t, base.Build)
	if d := base.IfNotExists(); d == base {
		t.Error("IfNotExists returned its receiver")
	}
	_ = base.Temporary()
	if after := builtSQL(t, base.Build); after != before {
		t.Errorf("a method wrote to its receiver:\nbefore %s\nafter  %s", before, after)
	}
}

// TestForms_NotNullOnConflict pins the conflict clause on NOT NULL, which sits
// in the one position SQLite accepts: immediately after NOT NULL and before
// the DEFAULT. Written after the DEFAULT instead, the statement is a syntax
// error, measured on 3.50.3.
func TestForms_NotNullOnConflict(t *testing.T) {
	got := builtSQL(t, sqlite.CreateTable(fmNotes).
		Column(ddl.Column(fmNoteID), ddl.Column(fmNoteBody).Default("untitled")).
		Clause(sqlite.PrimaryKey(fmNoteID), sqlite.NotNullOnConflict(fmNoteBody, sqlite.OrReplace)).Build)
	want := `CREATE TABLE "notes" ("id" INTEGER NOT NULL, "body" TEXT NOT NULL ON CONFLICT REPLACE DEFAULT 'untitled', PRIMARY KEY ("id"))`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	// The column has a default for every action, not only REPLACE. REPLACE
	// needs one and is refused at Build without it (ddl.ErrNoDefault, see
	// TestForms_NotNullReplaceNeedsADefault in contrib/driver-sqlite); until
	// that refusal existed this loop built REPLACE on a column with no default,
	// which is the very statement that installs cleanly and fails in
	// production, just to check where the clause lands.
	for _, action := range []sqlite.OrAction{sqlite.OrRollback, sqlite.OrAbort, sqlite.OrFail, sqlite.OrIgnore, sqlite.OrReplace} {
		t.Run(string(action), func(t *testing.T) {
			got := builtSQL(t, sqlite.CreateTable(fmNotes).
				Column(ddl.Column(fmNoteID), ddl.Column(fmNoteBody).Default("untitled")).
				Clause(sqlite.NotNullOnConflict(fmNoteBody, action)).Build)
			if !strings.Contains(got, `NOT NULL ON CONFLICT `+string(action)) {
				t.Errorf("got %s", got)
			}
		})
	}
	// A column whose definition writes no NOT NULL has nothing for the clause
	// to attach to, and a bare ON CONFLICT would be read as a constraint of
	// its own.
	nullable := sqlb.Col[*string](fmNotes, "subtitle")
	if _, err := sqlite.CreateTable(fmNotes).Column(ddl.Column(fmNoteID), ddl.Column(nullable)).
		Clause(sqlite.NotNullOnConflict(nullable, sqlite.OrReplace)).Build(); !errors.Is(err, ddl.ErrNoNotNull) {
		t.Errorf("a nullable column built with %v", err)
	}
	if _, err := sqlite.CreateTable(fmNotes).Column(ddl.Column(fmNoteID), ddl.Column(fmNoteBody)).
		Clause(sqlite.NotNullOnConflict(fmNoteBody, "")).Build(); !errors.Is(err, ddl.ErrZeroValue) {
		t.Errorf("no action: %v", err)
	}
}

// TestForms_OrActionIsAClosedSet pins the fix that came with the clause above.
// OrAction is a named string, so a value outside the five constants is
// writable, and every site that rendered one used to interpolate it verbatim.
func TestForms_OrActionIsAClosedSet(t *testing.T) {
	bogus := sqlite.OrAction("ROLLBACK; DROP TABLE notes; --")
	cases := []struct {
		name  string
		build func() error
	}{
		{"INSERT OR", func() error {
			_, err := sqlite.Insert(fmNotes).Row(sqlb.Set(fmNoteBody, "x")).Clause(sqlite.InsertOr(bogus)).Build()
			return err
		}},
		{"a key's conflict clause", func() error {
			_, err := sqlite.CreateTable(fmNotes).Column(ddl.Column(fmNoteID)).
				Clause(sqlite.PrimaryKey(fmNoteID).OnConflict(bogus)).Build()
			return err
		}},
		{"a column key's conflict clause", func() error {
			_, err := sqlite.CreateTable(fmNotes).Column(ddl.Column(fmNoteID)).
				Clause(sqlite.ColumnKey(fmNoteID).OnConflict(bogus)).Build()
			return err
		}},
		{"a NOT NULL conflict clause", func() error {
			_, err := sqlite.CreateTable(fmNotes).Column(ddl.Column(fmNoteID), ddl.Column(fmNoteBody)).
				Clause(sqlite.NotNullOnConflict(fmNoteBody, bogus)).Build()
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.build(); !errors.Is(err, sqlite.ErrUnknownKeyword) {
				t.Errorf("an action outside the set rendered, with %v", err)
			}
		})
	}
	// Every one of the five still builds, so the check refuses the right set.
	for _, action := range []sqlite.OrAction{sqlite.OrRollback, sqlite.OrAbort, sqlite.OrFail, sqlite.OrIgnore, sqlite.OrReplace} {
		if _, err := sqlite.Insert(fmNotes).Row(sqlb.Set(fmNoteBody, "x")).Clause(sqlite.InsertOr(action)).Build(); err != nil {
			t.Errorf("%s was refused: %v", action, err)
		}
	}
}

// TestForms_ModifierIsQuotedAndClosedWhereItCanBe covers the second shape of
// the OrAction defect, in the type that is genuinely open rather than closed.
//
// A Modifier cannot be a closed set — an offset carries its number inside the
// string — so the answer is not validation but quoting: every modifier is
// written through the dialect as a literal, so a hostile value reaches SQLite
// as one argument it simply does not understand. What IS closed is the unit of
// an offset, and that is closed here.
func TestForms_ModifierIsQuotedAndClosedWhereItCanBe(t *testing.T) {
	// A hostile modifier stays inside its literal rather than leaving it.
	a := sqlb.NewInlineArgs(sqlite.Dialect{})
	got := sqlite.Date(fmNoteBody, sqlite.Modifier("'); DROP TABLE notes; --")).SQL(a)
	if err := a.Err(); err != nil {
		t.Fatal(err)
	}
	const want = `date("n"."body", '''); DROP TABLE notes; --')`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	// The embedded quote is doubled, which is what keeps it one argument; a
	// single one would have closed the literal and started statement text.
	if strings.Contains(got, `, ');`) {
		t.Errorf("the modifier closed its literal: %s", got)
	}

	// An empty modifier is refused, because SQLite answers it with NULL from
	// the whole function rather than with an error.
	a = sqlb.NewInlineArgs(sqlite.Dialect{})
	sqlite.Date(fmNoteBody, sqlite.Modifier("")).SQL(a)
	if !errors.Is(a.Err(), sqlite.ErrEmptyModifier) {
		t.Errorf("an empty modifier rendered, with %v", a.Err())
	}

	// Every unit builds, and the offset reads as SQLite spells it.
	for _, tc := range []struct {
		unit sqlite.Unit
		want sqlite.Modifier
	}{
		{sqlite.Day, "+3 days"},
		{sqlite.Hour, "+3 hours"},
		{sqlite.Minute, "+3 minutes"},
		{sqlite.Second, "+3 seconds"},
		{sqlite.Month, "+3 months"},
		{sqlite.Year, "+3 years"},
	} {
		if got := sqlite.Offset(3, tc.unit); got != tc.want {
			t.Errorf("Offset(3, %q) = %q, want %q", tc.unit, got, tc.want)
		}
	}
	// A unit outside the six, reachable only through a conversion, makes a
	// modifier that fails at Build rather than one SQLite answers with NULL.
	a = sqlb.NewInlineArgs(sqlite.Dialect{})
	sqlite.Date(fmNoteBody, sqlite.Offset(1, sqlite.Unit("fortnights"))).SQL(a)
	if !errors.Is(a.Err(), sqlite.ErrEmptyModifier) {
		t.Errorf("an unknown unit rendered, with %v", a.Err())
	}
}
