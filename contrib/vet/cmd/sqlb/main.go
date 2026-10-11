// Command sqlb is the command-line front for the sqlb toolkit.
//
//	sqlb vet [--dsn DSN] [packages]    run the analyzers the compiler cannot replace;
//	                                   with --dsn, also diff every Col declaration against the database
//
// vet is the only subcommand today; migrate and schema are the shape of the
// next ones, which is why this is a command with subcommands rather than a
// single-purpose binary. The analyzers are standard analysis.Analyzer values
// in github.com/ubgo/sqlb/contrib/vet, so an application can also run them
// from its own test with vet.Check and no binary installed.
//
// Exit status is 0 with no findings, 1 with findings, 2 on a usage or load
// error. Findings are printed one per line as file:line:col: analyzer: message.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/ubgo/sqlb/contrib/vet"
	"github.com/ubgo/sqlb/render"

	// The three drivers are registered here rather than through the contrib
	// driver modules: those add pragmas and pool settings a checker reading
	// a catalogue does not need, and no contrib module depends on a sibling.
	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

// schemaFindings opens the database, runs the schema diff and closes it; a
// function of its own so the close runs before main decides an exit code.
func schemaFindings(dsn string, patterns []string) ([]vet.Finding, error) {
	db, dialect, err := openDSN(dsn)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	return vet.Schema(context.Background(), db, dialect, ".", patterns...)
}

// mysqlScheme prefixes a MySQL DSN on the command line. go-sql-driver's own
// form, user:pw@tcp(host:3306)/db, has no scheme a caller could be told apart
// from a SQLite path by, so the flag takes mysql:// and strips it.
const mysqlScheme = "mysql://"

// openDSN picks the driver from the connection string's shape: a URL scheme
// of postgres or postgresql is Postgres, mysql:// is MySQL, anything else is a
// SQLite path.
func openDSN(dsn string) (*sql.DB, string, error) {
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		db, err := sql.Open("pgx", dsn)
		return db, render.DialectPostgres, err
	}
	if rest, ok := strings.CutPrefix(dsn, mysqlScheme); ok {
		db, err := sql.Open("mysql", rest)
		return db, render.DialectMySQL, err
	}
	db, err := sql.Open("sqlite", dsn)
	return db, render.DialectSQLite, err
}

func main() {
	if len(os.Args) < 2 || os.Args[1] != "vet" {
		fmt.Fprintln(os.Stderr, "usage: sqlb vet [packages]")
		os.Exit(2)
	}
	fs := flag.NewFlagSet("sqlb vet", flag.ExitOnError)
	dsn := fs.String("dsn", "", "database to check declarations against (postgres://..., mysql://user:pw@tcp(host:3306)/db, or a SQLite file); the source checks always run")
	escapes := fs.Bool("escapes", false, "also list every escape hatch use (Raw, Literal, New, PredicateOf, JoinType, SetOp, non-constant Col) as warnings")
	_ = fs.Parse(os.Args[2:])
	patterns := fs.Args()
	if len(patterns) == 0 {
		patterns = []string{"./..."}
	}
	analyzers := vet.Analyzers()
	if *escapes {
		analyzers = append(analyzers, vet.Escapes)
	}
	findings, err := vet.RunWith(".", analyzers, patterns...)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if *dsn != "" {
		more, err := schemaFindings(*dsn, patterns)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		findings = append(findings, more...)
	}
	for _, f := range findings {
		if f.Warning {
			fmt.Printf("%s: %s: warning: %s\n", f.Pos, f.Analyzer, f.Message)
			continue
		}
		fmt.Printf("%s: %s: %s\n", f.Pos, f.Analyzer, f.Message)
	}
	if vet.Failing(findings) {
		os.Exit(1)
	}
}
