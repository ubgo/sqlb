package pg_test

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The detector.
//
// Every gate in this repository measured the surface that already existed: the
// family-shape test enumerates families that exist, the coverage gate asks
// whether exports are documented, and every task test was a read or a write.
// None asked which statements an engine has that this module cannot build, and
// that is how DDL stayed out for the whole life of the project without a single
// test failing. These tests ask it. testdata holds what the server itself
// reports, and every entry must carry a status here: a new Postgres major
// version, checked in beside the old one, fails the build until each of its new
// commands is classified, and the classification is the decision.
//
// A status is one of:
//
//	built <Name>      the constructor exists in pg or ddl, and is checked to
//	partial <Name> <what is missing>
//	                  the constructor exists and builds the form, and the
//	                  named parts of it are still to come
//	planned <tier>    in scope, tier 1 to 4 as the DDL spec ranks them
//	operator          roles, grants, replication, tuning: the operator's
//	not-ddl           a query, transaction or session command
//	left-out <reason> deliberately not built, with the reason in words

// pgCommands is every command psql 17 reports, with what this module does
// about it.
var pgCommands = map[string]string{
	"ABORT":                                  "not-ddl",
	"ALTER AGGREGATE":                        "operator",
	"ALTER COLLATION":                        "operator",
	"ALTER CONVERSION":                       "operator",
	"ALTER DATABASE":                         "operator",
	"ALTER DEFAULT PRIVILEGES":               "operator",
	"ALTER DOMAIN":                           "built AlterDomain",
	"ALTER EVENT TRIGGER":                    "operator",
	"ALTER EXTENSION":                        "built UpdateExtension",
	"ALTER FOREIGN DATA WRAPPER":             "operator",
	"ALTER FOREIGN TABLE":                    "operator",
	"ALTER FUNCTION":                         "operator",
	"ALTER GROUP":                            "operator",
	"ALTER INDEX":                            "built AlterIndex",
	"ALTER LANGUAGE":                         "operator",
	"ALTER LARGE OBJECT":                     "operator",
	"ALTER MATERIALIZED VIEW":                "built AlterMaterializedView",
	"ALTER OPERATOR":                         "operator",
	"ALTER OPERATOR CLASS":                   "operator",
	"ALTER OPERATOR FAMILY":                  "operator",
	"ALTER POLICY":                           "built AlterPolicy",
	"ALTER PROCEDURE":                        "operator",
	"ALTER PUBLICATION":                      "operator",
	"ALTER ROLE":                             "operator",
	"ALTER ROUTINE":                          "operator",
	"ALTER RULE":                             "operator",
	"ALTER SCHEMA":                           "built RenameSchema",
	"ALTER SEQUENCE":                         "built AlterSequence",
	"ALTER SERVER":                           "operator",
	"ALTER STATISTICS":                       "operator",
	"ALTER SUBSCRIPTION":                     "operator",
	"ALTER SYSTEM":                           "operator",
	"ALTER TABLE":                            "built AlterTable",
	"ALTER TABLESPACE":                       "operator",
	"ALTER TEXT SEARCH CONFIGURATION":        "operator",
	"ALTER TEXT SEARCH DICTIONARY":           "operator",
	"ALTER TEXT SEARCH PARSER":               "operator",
	"ALTER TEXT SEARCH TEMPLATE":             "operator",
	"ALTER TRIGGER":                          "built RenameTrigger",
	"ALTER TYPE":                             "built AlterEnum",
	"ALTER USER":                             "operator",
	"ALTER USER MAPPING":                     "operator",
	"ALTER VIEW":                             "built RenameViewColumn",
	"ANALYZE":                                "not-ddl",
	"BEGIN":                                  "not-ddl",
	"CALL":                                   "not-ddl",
	"CHECKPOINT":                             "not-ddl",
	"CLOSE":                                  "not-ddl",
	"CLUSTER":                                "operator",
	"COMMENT":                                "built CommentOn",
	"COMMIT":                                 "not-ddl",
	"COMMIT PREPARED":                        "not-ddl",
	"COPY":                                   "not-ddl",
	"CREATE ACCESS METHOD":                   "operator",
	"CREATE AGGREGATE":                       "operator",
	"CREATE CAST":                            "operator",
	"CREATE COLLATION":                       "built CreateCollation",
	"CREATE CONVERSION":                      "operator",
	"CREATE DATABASE":                        "operator",
	"CREATE DOMAIN":                          "built CreateDomain",
	"CREATE EVENT TRIGGER":                   "operator",
	"CREATE EXTENSION":                       "built CreateExtension",
	"CREATE FOREIGN DATA WRAPPER":            "operator",
	"CREATE FOREIGN TABLE":                   "operator",
	"CREATE FUNCTION":                        "built CreateFunction",
	"CREATE GROUP":                           "operator",
	"CREATE INDEX":                           "built CreateIndex",
	"CREATE LANGUAGE":                        "operator",
	"CREATE MATERIALIZED VIEW":               "built CreateMaterializedView",
	"CREATE OPERATOR":                        "operator",
	"CREATE OPERATOR CLASS":                  "operator",
	"CREATE OPERATOR FAMILY":                 "operator",
	"CREATE POLICY":                          "built CreatePolicy",
	"CREATE PROCEDURE":                       "operator",
	"CREATE PUBLICATION":                     "operator",
	"CREATE ROLE":                            "operator",
	"CREATE RULE":                            "operator",
	"CREATE SCHEMA":                          "built CreateSchema",
	"CREATE SEQUENCE":                        "built CreateSequence",
	"CREATE SERVER":                          "operator",
	"CREATE STATISTICS":                      "built CreateStatistics",
	"CREATE SUBSCRIPTION":                    "operator",
	"CREATE TABLE":                           "built CreateTable",
	"CREATE TABLE AS":                        "built CreateTableAs",
	"CREATE TABLESPACE":                      "operator",
	"CREATE TEXT SEARCH CONFIGURATION TABLE": "operator",
	"CREATE TEXT SEARCH DICTIONARY":          "operator",
	"CREATE TEXT SEARCH PARSER":              "operator",
	"CREATE TEXT SEARCH TEMPLATE":            "operator",
	"CREATE TRANSFORM":                       "operator",
	"CREATE TRIGGER":                         "built CreateTrigger",
	"CREATE TYPE":                            "built CreateRangeType",
	"CREATE USER":                            "operator",
	"CREATE USER MAPPING":                    "operator",
	"CREATE VIEW":                            "built CreateView",
	"DEALLOCATE":                             "not-ddl",
	"DECLARE":                                "not-ddl",
	"DELETE":                                 "built Delete",
	"DISCARD":                                "not-ddl",
	"DO":                                     "not-ddl",
	"DROP ACCESS METHOD":                     "operator",
	"DROP AGGREGATE":                         "operator",
	"DROP CAST":                              "operator",
	"DROP COLLATION":                         "built DropCollation",
	"DROP CONVERSION":                        "operator",
	"DROP DATABASE":                          "operator",
	"DROP DOMAIN":                            "built DropDomain",
	"DROP EVENT TRIGGER":                     "operator",
	"DROP EXTENSION":                         "built DropExtension",
	"DROP FOREIGN DATA WRAPPER":              "operator",
	"DROP FOREIGN TABLE":                     "operator",
	"DROP FUNCTION":                          "built DropFunction",
	"DROP GROUP":                             "operator",
	"DROP INDEX":                             "built DropIndex",
	"DROP LANGUAGE":                          "operator",
	"DROP MATERIALIZED VIEW":                 "built DropMaterializedView",
	"DROP OPERATOR":                          "operator",
	"DROP OPERATOR CLASS":                    "operator",
	"DROP OPERATOR FAMILY":                   "operator",
	"DROP OWNED":                             "operator",
	"DROP POLICY":                            "built DropPolicy",
	"DROP PROCEDURE":                         "operator",
	"DROP PUBLICATION":                       "operator",
	"DROP ROLE":                              "operator",
	"DROP ROUTINE":                           "operator",
	"DROP RULE":                              "operator",
	"DROP SCHEMA":                            "built DropSchema",
	"DROP SEQUENCE":                          "built DropSequence",
	"DROP SERVER":                            "operator",
	"DROP STATISTICS":                        "built DropStatistics",
	"DROP SUBSCRIPTION":                      "operator",
	"DROP TABLE":                             "built DropTable",
	"DROP TABLESPACE":                        "operator",
	"DROP TEXT SEARCH CONFIGURATION":         "operator",
	"DROP TEXT SEARCH DICTIONARY":            "operator",
	"DROP TEXT SEARCH PARSER":                "operator",
	"DROP TEXT SEARCH TEMPLATE":              "operator",
	"DROP TRANSFORM":                         "operator",
	"DROP TRIGGER":                           "built DropTrigger",
	"DROP TYPE":                              "built DropType",
	"DROP USER":                              "operator",
	"DROP USER MAPPING":                      "operator",
	"DROP VIEW":                              "built DropView",
	"END":                                    "not-ddl",
	"EXECUTE":                                "not-ddl",
	"EXPLAIN":                                "not-ddl",
	"FETCH":                                  "not-ddl",
	"GRANT":                                  "operator",
	"IMPORT FOREIGN SCHEMA":                  "operator",
	"INSERT":                                 "built Insert",
	"LISTEN":                                 "not-ddl",
	"LOAD":                                   "not-ddl",
	"LOCK":                                   "built NewLockTable",
	"MERGE":                                  "built NewMerge",
	"MOVE":                                   "not-ddl",
	"NOTIFY":                                 "not-ddl",
	"PREPARE":                                "not-ddl",
	"PREPARE TRANSACTION":                    "not-ddl",
	"REASSIGN OWNED":                         "operator",
	"REFRESH MATERIALIZED VIEW":              "built RefreshMaterializedView",
	"REINDEX":                                "operator",
	"RELEASE SAVEPOINT":                      "not-ddl",
	"RESET":                                  "not-ddl",
	"REVOKE":                                 "operator",
	"ROLLBACK":                               "not-ddl",
	"ROLLBACK PREPARED":                      "not-ddl",
	"ROLLBACK TO SAVEPOINT":                  "not-ddl",
	"SAVEPOINT":                              "not-ddl",
	"SECURITY LABEL":                         "operator",
	"SELECT":                                 "built Select",
	"SELECT INTO":                            "not-ddl",
	"SET":                                    "not-ddl",
	"SET CONSTRAINTS":                        "not-ddl",
	"SET ROLE":                               "not-ddl",
	"SET SESSION AUTHORIZATION":              "not-ddl",
	"SET TRANSACTION":                        "not-ddl",
	"SHOW":                                   "not-ddl",
	"START TRANSACTION":                      "not-ddl",
	"TRUNCATE":                               "built NewTruncate",
	"UNLISTEN":                               "not-ddl",
	"UPDATE":                                 "built Update",
	"VACUUM":                                 "not-ddl",
	"VALUES":                                 "built Values",
	"WITH":                                   "not-ddl",
}

// pgAlterTable is every ALTER TABLE form psql 17 reports, keyed by its
// keywords, because the command count hides the work: ALTER TABLE alone has
// fifty-four forms.
var pgAlterTable = map[string]string{
	"ADD COLUMN IF NOT EXISTS COLLATE":                              "built AddColumnIfNotExists",
	"DROP COLUMN IF EXISTS RESTRICT CASCADE":                        "built DropColumnIfExists",
	"ALTER COLUMN SET DATA TYPE COLLATE USING":                      "built AlterType",
	"ALTER COLUMN SET DEFAULT":                                      "built SetDefault",
	"ALTER COLUMN DROP DEFAULT":                                     "built DropDefault",
	"ALTER COLUMN SET DROP NOT NULL":                                "built SetNotNull",
	"ALTER COLUMN ADD GENERATED ALWAYS BY DEFAULT AS IDENTITY":      "built AddIdentity",
	"RENAME COLUMN TO":                                              "built RenameColumn",
	"ADD NOT VALID":                                                 "built AddConstraintNotValid",
	"VALIDATE CONSTRAINT":                                           "built ValidateConstraint",
	"DROP CONSTRAINT IF EXISTS RESTRICT CASCADE":                    "built DropConstraint",
	"RENAME CONSTRAINT TO":                                          "built RenameConstraint",
	"RENAME TO":                                                     "built RenameTo",
	"SET SCHEMA":                                                    "built SetSchema",
	"ENABLE ROW LEVEL SECURITY":                                     "built EnableRowLevelSecurity",
	"ATTACH PARTITION FOR VALUES DEFAULT":                           "built AttachPartition",
	"DETACH PARTITION CONCURRENTLY FINALIZE":                        "built DetachPartition",
	"SET TABLESPACE NOWAIT":                                         "operator",
	"ALTER COLUMN SET EXPRESSION AS":                                "built SetExpression",
	"ALTER COLUMN DROP EXPRESSION IF EXISTS":                        "built DropExpression",
	"ALTER COLUMN SET GENERATED ALWAYS BY DEFAULT SET RESTART WITH": "built AlterIdentitySequence",
	"ALTER COLUMN DROP IDENTITY IF EXISTS":                          "built DropIdentity",
	"ALTER COLUMN SET STATISTICS DEFAULT":                           "operator",
	"ALTER COLUMN SET":                                              "operator",
	"ALTER COLUMN RESET":                                            "operator",
	"ALTER COLUMN SET STORAGE PLAIN EXTERNAL EXTENDED MAIN DEFAULT": "built SetColumnStorage",
	"ALTER COLUMN SET COMPRESSION":                                  "built SetColumnCompression",
	"ADD":                                                           "built AddUniqueUsingIndex",
	"ALTER CONSTRAINT DEFERRABLE NOT DEFERRABLE INITIALLY DEFERRED INITIALLY IMMEDIATE":                       "built AlterConstraint",
	"ALTER CONSTRAINT DEFERRABLE NOT DEFERRABLE INITIALLY DEFERRED INITIALLY IMMEDIATE ENFORCED NOT ENFORCED": "built AlterConstraint",
	"ALTER CONSTRAINT INHERIT NO INHERIT": "built AlterConstraint",
	"DISABLE TRIGGER ALL USER":            "operator",
	"ENABLE TRIGGER ALL USER":             "operator",
	"ENABLE REPLICA TRIGGER":              "operator",
	"ENABLE ALWAYS TRIGGER":               "operator",
	"DISABLE RULE":                        "built DisableRule",
	"ENABLE RULE":                         "built EnableRule",
	"ENABLE REPLICA RULE":                 "built EnableReplicaRule",
	"ENABLE ALWAYS RULE":                  "built EnableAlwaysRule",
	"DISABLE ROW LEVEL SECURITY":          "built DisableRowLevelSecurity",
	"FORCE ROW LEVEL SECURITY":            "built ForceRowLevelSecurity",
	"NO FORCE ROW LEVEL SECURITY":         "built NoForceRowLevelSecurity",
	"CLUSTER ON":                          "operator",
	"SET WITHOUT CLUSTER":                 "operator",
	"SET WITHOUT OIDS":                    "operator",
	"SET ACCESS METHOD DEFAULT":           "operator",
	"SET TABLESPACE":                      "operator",
	"SET LOGGED UNLOGGED":                 "operator",
	"SET":                                 "operator",
	"RESET":                               "operator",
	"INHERIT":                             "built Inherit",
	"NO INHERIT":                          "built NoInherit",
	"OF":                                  "built OfType",
	"NOT OF":                              "built NotOfType",
	"OWNER TO CURRENT_ROLE CURRENT_USER SESSION_USER":   "operator",
	"REPLICA IDENTITY DEFAULT USING INDEX FULL NOTHING": "operator",
}

// keywords keys a grammar line by its upper-case words, in order, which is
// what tells two forms of one command apart.
var keywords = regexp.MustCompile(`\b[A-Z][A-Z_]*\b`)

func readTestdata(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// parseCommands reads psql's \h listing, two columns of command names.
func parseCommands(text string) []string {
	var out []string
	_, body, ok := strings.Cut(text, "Available help:")
	if !ok {
		return nil
	}
	for _, line := range strings.Split(body, "\n") {
		for _, cell := range regexp.MustCompile(`\s{2,}`).Split(strings.TrimSpace(line), -1) {
			if cell != "" {
				out = append(out, cell)
			}
		}
	}
	return out
}

// parseAlterForms reads psql's \h ALTER TABLE: the line after each top-level
// ALTER TABLE, and every action under "where action is one of".
func parseAlterForms(text string) []string {
	lines := strings.Split(text, "\n")
	var out []string
	inActions := false
	for i, l := range lines {
		if strings.HasPrefix(l, "ALTER TABLE") && i+1 < len(lines) {
			if next := strings.TrimSpace(lines[i+1]); !strings.HasPrefix(next, "action") {
				out = append(out, next)
			}
		}
		switch {
		case strings.TrimSpace(l) == "where action is one of:":
			inActions = true
		case inActions && (strings.HasPrefix(l, "and ") || strings.HasPrefix(l, "URL:")):
			inActions = false
		case inActions && regexp.MustCompile(`^    [A-Z]`).MatchString(l):
			out = append(out, strings.TrimSpace(l))
		}
	}
	return out
}

func formKey(line string) string { return strings.Join(keywords.FindAllString(line, -1), " ") }

// exportedNames is every exported package-level name in pg's source and in
// ddl's, whose actions a pg ALTER TABLE takes, grouped declarations included.
func exportedNames(t *testing.T) map[string]bool {
	t.Helper()
	out := namesInDir(t, ".")
	for k := range namesInDir(t, filepath.Join("..", "ddl")) {
		out[k] = true
	}
	return out
}

// namesInDir is every exported package-level name in dir's source.
func namesInDir(t *testing.T, dir string) map[string]bool {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
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

// checkStatuses fails on an entry the server reports that has no status, a
// status for something the server does not report, and a malformed status.
//
// reported holds one list per Postgres version checked in, keyed by the
// version. Every form each version reports needs a status, and a status must
// name a form some version reports, so a status survives while any version
// still has its form.
func checkStatuses(t *testing.T, what string, reported map[string][]string, key func(string) string, statuses map[string]string, names map[string]bool) {
	t.Helper()
	if len(reported) == 0 {
		t.Fatalf("found no %s lists in testdata", what)
	}
	seen := map[string]bool{}
	for version, forms := range reported {
		if len(forms) == 0 {
			t.Fatalf("parsed no %s from the Postgres %s list; the parser is broken, not the list empty", what, version)
		}
		inVersion := map[string]bool{}
		var missing []string
		for _, r := range forms {
			k := key(r)
			if inVersion[k] {
				t.Errorf("Postgres %s: %s %q appears twice; the key does not tell two forms apart", version, what, k)
			}
			inVersion[k] = true
			seen[k] = true
			if _, ok := statuses[k]; !ok {
				missing = append(missing, k)
			}
		}
		sort.Strings(missing)
		for _, m := range missing {
			t.Errorf("Postgres %s: %s %q is reported by the server and has no status here", version, what, m)
		}
	}
	for k, st := range statuses {
		if !seen[k] {
			t.Errorf("%s %q has a status but the server does not report it", what, k)
		}
		kind, detail, _ := strings.Cut(st, " ")
		switch kind {
		case "built":
			if !names[detail] {
				t.Errorf("%s %q is marked built by %s, which pg does not export", what, k, detail)
			}
		case "partial":
			name, missing, _ := strings.Cut(detail, " ")
			if !names[name] {
				t.Errorf("%s %q is marked partial by %s, which pg does not export", what, k, name)
			}
			if strings.TrimSpace(missing) == "" {
				t.Errorf("%s %q is partial without saying what is missing", what, k)
			}
		case "planned":
			if detail < "1" || detail > "4" || len(detail) != 1 {
				t.Errorf("%s %q is planned without a tier from 1 to 4: %q", what, k, st)
			}
		case "left-out":
			if strings.TrimSpace(detail) == "" {
				t.Errorf("%s %q is left out with no reason", what, k)
			}
		case "operator", "not-ddl":
		default:
			t.Errorf("%s %q has an unknown status %q", what, k, st)
		}
	}
}

// TestDetector_EveryPostgresCommandIsClassified is the question no gate asked.
func TestDetector_EveryPostgresCommandIsClassified(t *testing.T) {
	cmds := versions(t, "commands", parseCommands)
	checkStatuses(t, "command", cmds, func(s string) string { return s }, pgCommands, exportedNames(t))
}

// versions reads every testdata list named prefix-<version>.txt, one per
// Postgres major version checked in, parsed by parse. Checking in a new
// version's list is how the detector learns what it added.
func versions(t *testing.T, prefix string, parse func(string) []string) map[string][]string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("testdata", prefix+"-*.txt"))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]string{}
	for _, f := range files {
		v := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(f), prefix+"-"), ".txt")
		out[v] = parse(readTestdata(t, filepath.Base(f)))
	}
	if len(out) < 2 {
		t.Fatalf("found %d %s lists; Postgres 17 and 18 are both checked in, so the glob is broken", len(out), prefix)
	}
	return out
}

// TestDetector_EveryAlterTableFormIsClassified is the same question one level
// down, where the depth is.
func TestDetector_EveryAlterTableFormIsClassified(t *testing.T) {
	forms := versions(t, "alter-table", parseAlterForms)
	checkStatuses(t, "ALTER TABLE form", forms, formKey, pgAlterTable, exportedNames(t))
}

// TestDetector_Summary logs where the Postgres surface stands, so a run shows
// the gap rather than only whether the classification is complete.
func TestDetector_Summary(t *testing.T) {
	for name, statuses := range map[string]map[string]string{"commands": pgCommands, "ALTER TABLE forms": pgAlterTable} {
		counts := map[string]int{}
		for _, st := range statuses {
			kind, _, _ := strings.Cut(st, " ")
			counts[kind]++
		}
		t.Logf("%s: %d built, %d partial, %d planned, %d left out, %d operator, %d not DDL", name,
			counts["built"], counts["partial"], counts["planned"], counts["left-out"], counts["operator"], counts["not-ddl"])
	}
}
