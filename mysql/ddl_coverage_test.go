package mysql_test

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The DDL detector for MySQL: every statement the server's own HELP 'Data
// Definition' lists, captured from MySQL 8.4 into testdata, carries a status
// here, so a statement MySQL has and this package cannot build is visible
// rather than silent. The list is what the server reports, not what the
// documentation was read to say.
//
// A status is one of:
//
//	built <Name>        the constructor exists in this package
//	partial <Name> <what is missing>
//	operator            server, storage and account administration
//	left-out <reason>   deliberately not built, with the reason in words
var mysqlDDL = map[string]string{
	"ALTER DATABASE":                  "operator",
	"ALTER EVENT":                     "built AlterEvent",
	"ALTER FUNCTION":                  "built AlterFunction",
	"ALTER INSTANCE":                  "operator",
	"ALTER LOGFILE GROUP":             "operator",
	"ALTER PROCEDURE":                 "built AlterProcedure",
	"ALTER SCHEMA":                    "operator",
	"ALTER SERVER":                    "operator",
	"ALTER TABLE":                     "built AlterTable",
	"ALTER TABLESPACE":                "operator",
	"ALTER VIEW":                      "built AlterView",
	"CREATE DATABASE":                 "operator",
	"CREATE EVENT":                    "built CreateEvent",
	"CREATE FUNCTION":                 "built CreateFunction",
	"CREATE INDEX":                    "built CreateIndex",
	"CREATE LOGFILE GROUP":            "operator",
	"CREATE PROCEDURE":                "built CreateProcedure",
	"CREATE SCHEMA":                   "operator",
	"CREATE SERVER":                   "operator",
	"CREATE SPATIAL REFERENCE SYSTEM": "operator",
	"CREATE TABLE":                    "built CreateTable",
	"CREATE TABLESPACE":               "operator",
	"CREATE TRIGGER":                  "built CreateTrigger",
	"CREATE VIEW":                     "built CreateView",
	"DROP DATABASE":                   "operator",
	"DROP EVENT":                      "built DropEvent",
	"DROP FUNCTION":                   "built DropFunction",
	"DROP INDEX":                      "built DropIndex",
	"DROP PROCEDURE":                  "built DropProcedure",
	"DROP SCHEMA":                     "operator",
	"DROP SERVER":                     "operator",
	"DROP SPATIAL REFERENCE SYSTEM":   "operator",
	"DROP TABLE":                      "built DropTable",
	"DROP TABLESPACE":                 "operator",
	"DROP TRIGGER":                    "built DropTrigger",
	"DROP VIEW":                       "built DropView",
	"FOREIGN KEY":                     "built ForeignKey",
	"RENAME TABLE":                    "built RenameTable",
	"TRUNCATE TABLE":                  "built Truncate",
}

// exported is every exported package-level name in this package's source.
func exported(t *testing.T) map[string]bool {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	decl := regexp.MustCompile(`(?m)^(?:func|type|var|const)\s+([A-Z]\w*)|^\t([A-Z]\w*)\s+=`)
	out := map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range decl.FindAllStringSubmatch(string(src), -1) {
			out[m[1]+m[2]] = true
		}
	}
	return out
}

// TestDetector_EveryMySQLStatementIsClassified fails on a statement the
// server lists with no status, a status for one it does not list, a malformed
// status, and a built or partial name that does not exist. It fails closed on
// a list that parsed to nothing.
func TestDetector_EveryMySQLStatementIsClassified(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "ddl-8.4.txt"))
	if err != nil {
		t.Fatal(err)
	}
	var listed []string
	for _, l := range strings.Split(string(b), "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
			listed = append(listed, l)
		}
	}
	if len(listed) == 0 {
		t.Fatal("parsed no statement from testdata; the parser is broken, not the list empty")
	}
	names := exported(t)
	seen := map[string]bool{}
	var missing []string
	for _, s := range listed {
		seen[s] = true
		if _, ok := mysqlDDL[s]; !ok {
			missing = append(missing, s)
		}
	}
	sort.Strings(missing)
	for _, m := range missing {
		t.Errorf("MySQL lists %q and it has no status here", m)
	}
	for s, st := range mysqlDDL {
		if !seen[s] {
			t.Errorf("%q has a status but MySQL does not list it", s)
		}
		kind, detail, _ := strings.Cut(st, " ")
		switch kind {
		case "built":
			if !names[detail] {
				t.Errorf("%q is marked built by %s, which this package does not export", s, detail)
			}
		case "partial":
			name, what, _ := strings.Cut(detail, " ")
			if !names[name] || strings.TrimSpace(what) == "" {
				t.Errorf("%q is partial by %s without an export or without saying what is missing", s, name)
			}
		case "left-out":
			if strings.TrimSpace(detail) == "" {
				t.Errorf("%q is left out with no reason", s)
			}
		case "operator":
		default:
			t.Errorf("%q has an unknown status %q", s, st)
		}
	}
}
