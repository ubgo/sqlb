# The conformance suite

Rendering SQL and executing it are two different claims. The builder's own tests prove the text: given these calls, this statement and these arguments. Only a database proves the rest, that the placeholders, the argument order and the scan targets line up, and that the engine accepts what was rendered.

`github.com/ubgo/sqlb/sqlbtest` is the suite that makes the second claim. It has no third-party dependencies and takes a `sqlb.DBTX`, so it runs against a `*sql.DB`, a `*sql.Tx` or any wrapper of your own.

## What it proves

| Case | What it catches |
|---|---|
| `insert_select_one` | a row written by the insert builder comes back through `One` with every target filled |
| `param_order` | four parameters across three clauses bind in the order the SQL names them |
| `null_scan` | a NULL column reaches a `*T` target instead of failing the scan |
| `no_rows` | `One` reports `ErrNoRows` rather than leaving the targets untouched |
| `multiple_rows` | `One` reports `ErrMultipleRows` rather than silently taking the first |
| `each_order` | `Each` yields every row, in ORDER BY order, reusing the same targets |
| `empty_in` | `IN` with no values runs as a constant instead of being a syntax error |
| `update` | the update builder writes, and only where the predicate says |
| `delete` | the delete builder removes, and only where the predicate says |
| `join_group` | a join with an aggregate and GROUP BY scans both sides |
| `limit_offset` | LIMIT and OFFSET are bound parameters and page correctly |
| `search_escapes` | `Contains`, `StartsWith` and `EndsWith` match a value's `%`, `_`, `!` and backslash as themselves |
| `set_ops` | `UNION`, `UNION ALL`, `INTERSECT` and `EXCEPT` return the rows each promises |
| `cte` | `WITH` and `WITH RECURSIVE` run, the recursive one counting to a bound |
| `window` | `ROW_NUMBER` over a partition numbers each group from 1 in its order |
| `expressions` | `CASE`, `COALESCE`, `concat`, `upper` and `lower` evaluate as the core renders them |
| `subqueries` | `EXISTS`, `IN` over a subquery and a row `IN` list select the same rows |
| `case_fold` | `EqFold`, `InFold`, `NotInFold`, `ContainsFold`, `StartsWithFold` and `EndsWithFold` select one fixed list of ids on every engine, and filter an UPDATE and a DELETE |
| `crud_transcript` | one scripted create, read, update and delete workload produces `CRUDTranscript` line for line: every row read back, every aggregate, page and join, and every rows-affected count |
| `case_sensitive_eq` | `Eq` and `In` stay exact, so folding happens only where it is asked for; needs text compared by its bytes, which on MySQL means `utf8mb4_bin` |
| `returning` | `INSERT ... RETURNING` fills its bindings, where the dialect has it |

It runs on SQLite (twice, in memory and file-backed), on Postgres 17 and on MySQL 8.4, and the six cases after `limit_offset` are where "universal" is measured rather than read. Their first run, on 2026-09-22, found three core constructs that rendered cleanly and failed on a real engine: `ESCAPE '\'` is an unterminated string in MySQL's default mode, a parenthesised set-operation branch is a syntax error in SQLite, and `concat()` with a bound value is refused by Postgres, which cannot type the parameter. Each is fixed, and each case is what keeps it fixed.

`case_fold` is the proof behind switching engines by changing the dialect. Every expected result is a fixed list of ids written in the suite, so an engine that passes answers exactly what every other passing engine answers. `contrib/driver-mysql` runs the suite twice, once on `utf8mb4_bin` columns and once on the default collation, where `case_sensitive_eq` is skipped with its reason and `case_fold` still passes. `contrib/driver-pgx` runs it a second time with the ILIKE capability removed, and it returns the same ids with the portable spelling.

`crud_transcript` is the whole-program version of the same claim. It inserts one row and then several in one statement, reads every row and column back, filters with fifteen predicates, pages, aggregates, LEFT-joins, updates (every matched row, none, a row to the value it already has, a NULL, an expression, a fold) and deletes, writing every observable result to a transcript. `sqlbtest.CRUDTranscript` is the golden, and a difference fails naming the line. Its first MySQL run found two differences the single-property cases had missed: an `UPDATE` that changes nothing reported 0 affected rows where Postgres and SQLite report 1, now fixed by `contrib/driver-mysql` setting `clientFoundRows`, and `COALESCE(sum(x), ?)` came back as `DECIMAL(65,30)` and could not be scanned into an `int64`, now fixed by the MySQL dialect typing the parameter.

`param_order` is the case worth understanding. A driver that binds parameters out of order does not return an error; it returns the wrong row. So the case asserts the value, not merely the absence of a failure.

## Running it

The suite does not create its own tables, so the caller supplies the schema in its engine's own spelling; [`ddl`](./ddl/README.md) can write it, or plain SQL can. The table and column declarations the suite binds to are exported, and `sqlbtest.Schema` documents the shape they expect.

```go
func TestConformance(t *testing.T) {
	db, err := driversqlite.Memory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	sqlbtest.Run(t, sqlbtest.Config{
		DB:       db,
		Dialect:  sqlb.SQLiteDialect{},
		Setup:    setupDDL,
		Teardown: teardownDDL,
	})
}
```

Each case empties both tables first, so the order of the cases cannot matter and a failure is never inherited from the case before it.

## Skipping

`Config.Skip` maps a case name to a reason, and the reason is printed in the test log where a reviewer reads it. Use it for a genuine engine limitation. Do not use it to hide a failure: a skipped case with a reason is a documented gap, and a skipped case without one is a lie with extra steps.

## Adding a case

Add it to `Cases()` with a `Why` that says what it proves, and implement the body. Every integration picks it up on its next run, which is the point of the suite being shared. If a case cannot pass on an engine that should support it, that is a finding, not a reason to weaken the case.

## RETURNING is a separate call

`Run` takes the dialect as a runtime value, so every statement it builds is universal SQL by construction — and `RETURNING` is a capability that `InsertReturning` only accepts from a dialect *type* that has it. The case therefore lives in `RunReturning(t, cfg, sqlite.Dialect{})`, called after `Run` in the same test function with the concrete dialect. A driver whose dialect lacks `RETURNING` cannot write that call, which is the skip made into a type rather than a runtime check.

## Zero values

`sqlbtest.ZeroValues` is the other check `sqlbtest` holds, and it needs no database. Every exported clause, statement and builder in this module is a struct, so a caller can write `pg.ForeignKeyClause{}` as well as call `pg.ForeignKey`, and a struct literal skips whatever the constructor guaranteed. Before the check existed, several such literals panicked inside `Build` and others rendered `PRIMARY KEY ()` for the server to reject.

```go
func TestZeroValues_AreRefused(t *testing.T) {
	sqlbtest.ZeroValues(t, sqlbtest.ZeroConfig{
		Dir:     ".",
		Dialect: pg.Dialect{},
		Values:  []any{pg.ForeignKeyClause{}, pg.PrimaryKeyClause{}},
		Probes:  map[string]func() error{"Schema": func() error { _, err := pg.CreateSchema(pg.Schema{}).Build(); return err }},
		Controls: map[string]func() error{"Schema": func() error {
			_, err := pg.CreateSchema(pg.Schema{Name: "s"}).Build()
			return err
		}},
		Exempt: map[string]string{"JSON": "a scan wrapper whose zero value is a valid empty document"},
	})
}
```

A `ZeroConfig` names the package directory, whose source is read for every exported struct type, and puts each type in one of three places. `Values` holds the zero value of a type with a `Build` or render method, which must fail with an error, never panic and never render. `Probes` holds a check for a type with neither, an object value passed to its constructor or a part placed in the construct that consumes it, and every probe needs a `Control` that builds the same construct with a real value and must succeed, so a probe that fails because the test is wrong proves nothing and says so. `Exempt` names a type that renders nothing and says why. A type in none of them fails the check, a name that is no longer an exported struct fails it, and a directory with no exported structs fails it rather than passing over nothing. `ddl`, `pg`, `sqlite`, `mysql` and `ch` each run it.

