package drivermysql_test

import (
	"database/sql"
	"testing"

	"github.com/ubgo/sqlb"
	drivermysql "github.com/ubgo/sqlb/contrib/driver-mysql"
	"github.com/ubgo/sqlb/sqlbtest"
)

// openOrSkip opens the database SQLB_MYSQL_DSN names, or skips loudly.
func openOrSkip(t *testing.T) *sql.DB {
	t.Helper()
	dsn, ok := drivermysql.DSNFromEnv()
	if !ok {
		t.Skipf("%s not set: the MySQL proof did not run", drivermysql.EnvDSN)
	}
	db, err := drivermysql.Open(dsn)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// setupDDL is sqlbtest.Schema in MySQL's spelling, with the text columns
// declared utf8mb4_bin so they compare by their bytes, as Postgres and SQLite
// do. That is what the suite's case_sensitive_eq case needs, and what a
// library that switches engines must declare to keep Eq meaning "exactly this
// value" on MySQL; defaultCollationDDL is the same schema without it.
var setupDDL = conformanceDDL(" COLLATE utf8mb4_bin")

// defaultCollationDDL leaves the text columns on the server's default
// collation, utf8mb4_0900_ai_ci on 8.4, which is what a CREATE TABLE that
// names none gets.
var defaultCollationDDL = conformanceDDL("")

func conformanceDDL(collate string) []string {
	return []string{
		"DROP TABLE IF EXISTS sqlb_conformance_post",
		"DROP TABLE IF EXISTS sqlb_conformance_user",
		`CREATE TABLE sqlb_conformance_user (
		id     BIGINT       PRIMARY KEY,
		email  VARCHAR(255)` + collate + ` NOT NULL,
		name   VARCHAR(255)` + collate + `,
		age    BIGINT       NOT NULL,
		active BOOLEAN      NOT NULL
	)`,
		`CREATE TABLE sqlb_conformance_post (
		id      BIGINT       PRIMARY KEY,
		user_id BIGINT       NOT NULL REFERENCES sqlb_conformance_user(id),
		title   VARCHAR(255)` + collate + ` NOT NULL,
		views   BIGINT       NOT NULL
	)`,
	}
}

var teardownDDL = []string{
	"DROP TABLE IF EXISTS sqlb_conformance_post",
	"DROP TABLE IF EXISTS sqlb_conformance_user",
}

// TestConformance runs the shared suite against a real MySQL 8, the engine
// the universality rule names and nothing had executed before.
func TestConformance(t *testing.T) {
	db := openOrSkip(t)
	sqlbtest.Run(t, sqlbtest.Config{
		DB:       db,
		Dialect:  sqlb.MySQLDialect{},
		Setup:    setupDDL,
		Teardown: teardownDDL,
	})
}

// TestConformance_DefaultCollation runs the same suite on MySQL's default
// collation. Every case passes there but one: case_sensitive_eq, because under
// utf8mb4_0900_ai_ci a plain = ignores case and accents, which
// TestDefaultCollation_EqIgnoresCaseAndAccents measures. The fold cases pass
// under both collations, which is the point of them: a query that asks for
// folding gets the same ids whatever the column was declared with.
func TestConformance_DefaultCollation(t *testing.T) {
	db := openOrSkip(t)
	sqlbtest.Run(t, sqlbtest.Config{
		DB:       db,
		Dialect:  sqlb.MySQLDialect{},
		Setup:    defaultCollationDDL,
		Teardown: teardownDDL,
		Skip: map[string]string{
			"case_sensitive_eq": "utf8mb4_0900_ai_ci compares case- and accent-insensitively, so Eq(\"ADA@X\") matches \"ada@x\"; measured by TestDefaultCollation_EqIgnoresCaseAndAccents",
		},
	})
}
