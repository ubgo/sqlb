# contrib/driver-mysql

Opens MySQL databases for `github.com/ubgo/sqlb`, and proves the core on a real MySQL 8.

```sh
go get github.com/ubgo/sqlb/contrib/driver-mysql
```

```go
db, err := drivermysql.Open("user:pass@tcp(127.0.0.1:3306)/app")
q, err := sqlb.NewSelect(sqlb.MySQLDialect{}).Columns(sqlb.Bind(UserEmail, &email)).From(Users).Build()
err = sqlb.One(ctx, db, q)
```

`Open` parses the DSN with `github.com/go-sql-driver/mysql` and forces three settings whatever it says, because each is a silent wrong answer without it: `parseTime`, so a `DATETIME` scans into a `time.Time`; the UTC location, because the MySQL dialect writes times in UTC; and `clientFoundRows`, so an `UPDATE` reports the rows it matched, as Postgres and SQLite do. Without the third, an `UPDATE` setting a row to the value it already has reports 0 affected on MySQL and 1 elsewhere, and code reading 0 as "not found" says so about a row that exists; `sqlbtest`'s `crud_transcript` case found it. `WithChangedRows` restores MySQL's own changed-rows count for a caller that wants it and knows it is on MySQL. `WithMaxOpenConns` and `WithConnMaxLifetime` override the pool defaults, `DefaultMaxOpenConns` and `DefaultConnMaxLifetime`. `DriverName` is the name the driver registers.

## The proof

The module's tests run the shared conformance suite, `sqlbtest.Run`, against the database `SQLB_MYSQL_DSN` names (`EnvDSN`, read by `DSNFromEnv`), and skip loudly when it is unset. A scratch server:

```sh
docker run -d --rm --name sqlb-mysql -e MYSQL_ROOT_PASSWORD=sqlb -e MYSQL_DATABASE=sqlb -p 127.0.0.1:33069:3306 mysql:8.4
SQLB_MYSQL_DSN='root:sqlb@tcp(127.0.0.1:33069)/sqlb' go test ./...
```

Its first run found that the core's `LIKE` escape clause, `ESCAPE '\'`, was an unterminated string in MySQL's default `sql_mode`; the core escapes with `!` now, and every engine's conformance run checks it. The `crud_transcript` case, added 2026-09-28, found two more: the changed-rows count above, and that `COALESCE(sum(x), ?)` or `sum(x) + ?` comes back as `DECIMAL(65,30)`, so an `Expr[int64]` could not be scanned; the MySQL dialect now types a bare integer parameter there through `render.NumericParameters`.

The suite runs twice here: `TestConformance` on `utf8mb4_bin` text columns, which compare by their bytes as Postgres and SQLite do, and `TestConformance_DefaultCollation` on the server default, `utf8mb4_0900_ai_ci`, where a plain `=` ignores case and accents and `case_sensitive_eq` is skipped with that reason.
