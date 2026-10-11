# snippets

A catalogue of advanced PostgreSQL snippets, translated into `sqlb` section by section, in the catalogue's own order and numbering: 21 sections and 193 SQL blocks. Read the test files top to bottom and they are one document. Every `### x.y` heading appears in order, and each is either an `Example` whose SQL `go test` checks, or a comment that says why there is nothing to render.

## Layout

```
helpers_test.go        tbl, ref, lit, join and order shorthands, the show printer
s01_recursive_test.go  1. Recursive CTEs and graphs
s02_window_test.go     2. Window functions
s03_lateral_test.go    3. LATERAL and set-returning functions
s04_aggregation_test.go 4. Aggregation
s05_writes_test.go     5. Writes: upsert, MERGE, data-modifying CTEs
s06_json_test.go       6. JSON and JSONB
s07_arrays_test.go     7. Arrays
s08_ranges_test.go     8. Range types, exclusion constraints, temporal modelling
s09_search_test.go     9. Full-text and fuzzy search
s10_time_test.go       10. Date and time edge cases
s11_edge_test.go       11. NULL, numeric, and string edge cases
s12_locking_test.go    12. Locking, concurrency, queues
s13_s17_ddl_ops_test.go 13. Partitioning and DDL, 14. Triggers and PL/pgSQL, 15. Security, 16. Performance, 17. Operations
s18_s19_pivot_analytics_test.go 18. Pivot, unpivot, dedup, test data; 19. Analytics patterns
s20_s21_grabbag_test.go 20. Grab bag; 21. Anti-pattern cheat sheet
```

| Count | |
|---|---|
| Headings in the catalogue, all present in order | 201 |
| Examples with checked SQL output | 159 |
| Headings kept as comments because the SQL is DDL, PL/pgSQL, psql, session control or administration | 42 |

Run `go test ./pgsurface/snippets -run Example -v` from `design/` to print every rendered statement.

## How to read a comparison

Take a heading in the markdown, find the same heading in the test file, and compare three things: the Go that builds the statement, the SQL in the `// Output:` block, and the SQL in the markdown. The Go should read like the SQL. Where the text differs, it differs on purpose: sqlb quotes every identifier, binds every value, parenthesises every binary expression and every AND group, and writes `CAST(x AS t)` rather than `x::t`. The statement is the same; the bytes are not.

Every construct in the catalogue has a named helper or a core constructor. The first pass left seven kinds of construct on the typed door, `sqlb.New`, `sqlb.Raw`, `sqlb.SourceOf`, `sqlb.Compare`; the second pass added a helper for each and rewrote the examples, so no example builds SQL from a string any more. What still calls `sqlb.Fn` by name is a user-defined function from the catalogue (`uuid_generate_v7`, `count_where`, `top_customers`, `dblink_exec`) or a set-returning function used as a source with the caller's own column names, which is what `pg.FuncSource` plus `sqlb.Fn` is for.

## What the catalogue added to the builder

Six gaps were universal SQL and went into the core: a `SELECT` with no `FROM`, a suffix on a recursive CTE for `SEARCH` and `CYCLE`, a frame bound with an expression offset for `RANGE ... interval PRECEDING`, `INSERT ... SELECT` without a column list, `NotEqExpr` with the `<=` and `>=` expression forms, `InExprs`, `AssertNotNull`, and an assignment target that is an expression, for JSONB subscript assignment.

The Postgres-only pieces went into `pg`, by area:

- **SQL/JSON and JSONB.** `JSONTable` with typed, nested and ordinality columns; `JSONObject`, `JSONArrayAgg`, `JSONObjectAgg` with `ABSENT ON NULL`, `WITH UNIQUE KEYS` and `RETURNING`; `IsJSON`; `JSONValue`, `JSONQuery`, `JSONExists`; `JSONSubscript` and `SetSubscript`; `PathQueryVars`, `PathQueryArray`, `PathQueryScalar`, `JSONBSetExpr`, `JSONBSetLax`; `JSONNull`, `EmptyJSONArray`, `RowRef`.
- **Sources.** `RowsFrom`, `UnnestMulti`, `RecordSet` with a typed column list, `GenerateSeriesTime`, `GenerateSeriesExpr`, `GenerateSeriesScalar`, `StringToTable`, `Variadic` and `Named` arguments.
- **Recursion.** `SearchDepthFirst`, `SearchBreadthFirst`, `Cycle`.
- **Text.** `SubstringRegex`, `TrimChars`, `Normalize`, `Encode`, `RegexpInstr`, `RegexpSubstr`, `OctetLength`, `QuoteIdent`, `QuoteLiteral`, `RepeatExpr`, `SubstrExpr`, `StringToArrayNull`.
- **Search.** `TSRankCDNormalized` in `pg`; the trigram, phonetic and ltree helpers are in the extension packages below.
- **Arrays.** `ArrayOf`, `AnyExpr`, `AllExpr`, `OverlapsExpr`, `ArrayContainsExpr`, `ArrayContainedByExpr`, `SliceFrom`, `SliceTo`, `SliceExprs`, `SubscriptExpr`, `ArrayPositions`, `ArrayRemoveNull`, `EmptyArray`, `ArrayLit`, `UnnestScalar`, `WidthBucketThresholds`.
- **Time.** `IntervalLit`, `IntervalMul`, `AddIntervalExpr`, `SubIntervalExpr`, `JustifyInterval`, `ExtractFrom`, `DateTruncZone`, `DateLit`, `TimestampLit`, `TimestamptzLit`, `Infinity`, `OverlapsInterval`, `DaysBetween`, `DatePlusDays`, `DateMinusDays`, `FieldIsoDow` and friends.
- **Ranges.** `DateRange`, `NumRange`, `Int4Range`, `TstzRangeExpr`, `NullBound`, `UpperInf`, `LowerInf`, `Multirange`, `TstzMultirange`, `MultirangeDiff`, `RangeAggTyped`, `RangeIntersectAgg`, `UnnestMultirange`.
- **Numbers and bits.** `Div`, `ModFn`, `RoundHalf`, `TruncZero`, `PowerOp`, `PowerExpr`, `Radians`, `Sin`, `Cos`, `Asin`, `RandomNormal`, `Setseed`, `BitAnd`, `BitOr`, `BitXor`, `BitNot`, `ShiftLeft`, `ShiftRight`, `BitLit`, `BitStringOr`, `GetBit`, `BitCount`.
- **Locks, session, catalog.** `AdvisoryXactLock`, `AdvisoryXactLock2`, `TryAdvisoryXactLock`, `Hashtext`, `Regclass`, `SizePretty`, `TotalRelationSize`, `RelationSize`, `IndexesSize`, `ColumnSize`, `XidAge`, `FormatType`, `CancelBackend`, `TerminateBackend`, `BlockingPids`, `InputIsValid`, `CurrentDatabase`, `HasRole`, `CurrentXactID`, `TxidCurrent`, `ExportSnapshot`, `CurrentWalLsn`, `WalLsnDiff`, `LastXactReplayTimestamp`, `GetSerialSequence`, `Setval`.
- **Extensions.** Each in its own sub-package with a README: `pg/trgm` (`Similar`, `Similarity`, `Distance`, `WordSimilarity`), `pg/fuzzy` (`Dmetaphone`, `LevenshteinLessEqual`), `pg/ltree` (`Path`, `Lit`, `AncestorOf`, `DescendantOf`, `MatchesLQuery`, `NLevel`, `Subpath`, `Index`), `pg/crypto` (`Crypt`, `GenSalt`, `Digest`, `HMAC`, `PgpSymEncrypt`, `PgpSymDecrypt`), `pg/earth` (`LLToEarth`, `Box`, `BoxContains`), `pg/unaccent` (`Unaccent`). `UUIDv7Timestamp` needs no extension and stays in `pg`.

## What is not proven

Rendering and compile-time checks only. Nothing here has run against a Postgres server. The catalogue marks which of its snippets were executed against PostgreSQL 17; the translations inherit no such mark until the integration module runs them.
