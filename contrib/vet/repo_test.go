package vet_test

import (
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/ubgo/sqlb/contrib/vet"
)

// TestRepo_ReportsOnlyTheDeliberatePins runs every default analyzer over
// this whole repository and names the sites it may report: the tests that
// pin the dialect residue and the ones that pin a Query literal's missing
// guard. The README says "exactly seven, all deliberate"; this is what makes
// that a fact rather than a sentence, and an eighth is a finding to act on,
// not a number to bump.
//
// The seventh arrived on 2026-09-22 with the mysql package, whose own test
// pins the residue for MySQL as sqlite/entry_test.go does for SQLite: a MySQL
// expression built under Postgres, refused at Build. vet reports it because
// mysql is now an engine package it knows.
//
// The fifth and sixth arrived on 2026-09-21 with typed dialects on pg's
// Truncate and LockTable. Passing another engine's dialect to their
// constructors stopped compiling, so the runtime pins moved to the one path
// that still reaches the guard: the statement embedded as a CTE body in a
// SQLite statement, which is the residue those two tests exist to hold. Each
// file appears once on purpose. Before the same day's fix the analyzer also
// reported the pg.Dialect type in each, one mistake giving two findings.
func TestRepo_ReportsOnlyTheDeliberatePins(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	got, err := vet.Run(root, "./...")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"exec_test.go sqlbstructlit",
		"mysql/mysql_test.go sqlbdialect",
		"pg/locktable_test.go sqlbdialect",
		"pg/precedence_test.go sqlbdialect",
		"pg/truncate_test.go sqlbdialect",
		"sqlite/entry_test.go sqlbdialect",
		"stmt/stmt_test.go sqlbstructlit",
	}
	var have []string
	for _, f := range got {
		// The position is made relative to the repository root rather than
		// cut after the first "/sqlb/": a checkout at .../work/sqlb/sqlb, as
		// on a GitHub runner, left a "sqlb/" prefix on every file.
		file, _, _ := strings.Cut(f.Pos, ":")
		rel, err := filepath.Rel(root, file)
		if err != nil {
			t.Fatalf("finding outside the repository: %s", f.Pos)
		}
		file = filepath.ToSlash(rel)
		have = append(have, file+" "+f.Analyzer)
	}
	sort.Strings(have)
	if strings.Join(have, "\n") != strings.Join(want, "\n") {
		t.Errorf("findings over the repository changed:\nwant:\n%s\ngot:\n%s", strings.Join(want, "\n"), strings.Join(have, "\n"))
	}
}
