---
ds:
  covers: [bound-each-qvvzm6tq, bound-one-x39s9kka, bound-values-3t7cnq5d, column-and-null-p2fy3sp5, column-declaration-gthk6bb2, core-boundaries-y4kpz7kr, ddl-contract-s2xdb9he, dialect-contract-smkbmeyz, dialect-failure-zqdgvzs3, gate-9g2wphfm, literal-refusals-3xx754he, mysql-literals-23295m4b, migration-execute-hgdykxdq, migration-install-ennvmeq2, migration-ledger-h5dv2yvb, mysql-collation-vqcp6ms3, mysql-gate-xzbtwu8b, mysql-numbers-zxvc6pdk, null-scan-9xxzyqc7, parameter-flow-pdh445xg, postgres-arrays-5w7ytu86, postgres-extensions-j5kc5mqu, postgres-gate-ctc94x6a, project-purpose-7mzq92jv, promise-gate-a57aksdv, query-overlap-5hddx8sm, query-ownership-vsv882ny, reader-allocation-nktfvyeg, reader-cardinality-332zvda7, reader-composition-ymehc5ss, reader-flow-xcq2taqt, reader-projection-3xfnp55v, reader-sharing-79k4pyx5, schema-order-mc4m9j96, sqlite-memory-kyedhky9, sqlite-open-jda68c72, sqlite-pragmas-d6qmetcs, workspace-f7vpgvxp]
---

# SQLB patterns and operating lessons

These notes describe verified implementation behavior and the procedure it motivates. The corresponding memories, architecture notes, patterns and hotfixes are shared in `.lore/data`; use Lore search by the titles below.

## Patterns

### Export columns; let callers compose

Expose typed table/column values from a library and accept caller composition at query boundaries. Keep application-only joins and filters in the application. This preserves compile-time expression compatibility without creating imports between packages that own unrelated tables. [project purpose](ds:block?id=project-purpose-7mzq92jv&assert=true) [column declaration](ds:block?id=column-declaration-gthk6bb2)

### Choose destinations by lifetime

Create Bind destinations and the query together for one call. In Each callbacks copy the populated values before retaining them. For shared query shapes, compose Field with Map/Map2 into a Reader and use fresh per-row storage in custom allocators. The reason is lifetime ownership: a saved pointer to a reused binding observes later rows. [bound each](ds:block?id=bound-each-qvvzm6tq) [reader composition](ds:block?id=reader-composition-ymehc5ss) [reader allocation](ds:block?id=reader-allocation-nktfvyeg) [reader sharing](ds:block?id=reader-sharing-79k4pyx5&assert=true)

### Keep portable mechanisms and guarded engine syntax separate

Put common expression/statement mechanisms in core layers and engine syntax in pg/mysql/sqlite/ch. Use dialect capabilities and engine guards, then test wrong-dialect refusal as well as the intended SQL. This catches a query built for the wrong engine before it reaches the server; it does not detect server-version or extension availability. [dialect contract](ds:block?id=dialect-contract-smkbmeyz) [dialect failure](ds:block?id=dialect-failure-zqdgvzs3&assert=true) [postgres arrays](ds:block?id=postgres-arrays-5w7ytu86&assert=true) [postgres extensions](ds:block?id=postgres-extensions-j5kc5mqu)

### Separate values from authored SQL

Use Param and typed predicates for runtime query values; reserve Literal/raw fragments and raw schema type/default text for authored SQL. For DDL, let inline rendering call the dialect literal writer and propagate refusal. This retains argument binding for query data and avoids pretending every SQL context supports placeholders. [parameter flow](ds:block?id=parameter-flow-pdh445xg) [bound values](ds:block?id=bound-values-3t7cnq5d&assert=true) [literal refusals](ds:block?id=literal-refusals-3xx754he&assert=true) [MySQL literal cases](ds:block?id=mysql-literals-23295m4b&assert=true) [ddl contract](ds:block?id=ddl-contract-s2xdb9he)

### Assemble schema contributions before executing

Collect each package's DDL under an owner, use Install to order the whole contribution set, then run the Script through the migration executor. Preserve the Statement flags instead of flattening the Script to SQL strings. This preserves foreign-key ordering and checks whose failure is expressed as returned rows. [migration install](ds:block?id=migration-install-ennvmeq2) [schema order](ds:block?id=schema-order-mc4m9j96&assert=true) [ddl contract](ds:block?id=ddl-contract-s2xdb9he) [migration execute](ds:block?id=migration-execute-hgdykxdq)

### Add a module to both workspace and gates

Keep third-party dependencies in a contrib module; register the module in go.work and Taskfile MODULES. Build and test that module explicitly as part of the multi-module gate. A root go test ./... does not walk nested module boundaries, so an unregistered module can otherwise look untested but green. [workspace](ds:block?id=workspace-f7vpgvxp) [gate](ds:block?id=gate-9g2wphfm) [core boundaries](ds:block?id=core-boundaries-y4kpz7kr&assert=true)

### Use layered evidence for behavior claims

Pair rendering assertions with negative type checks and server tests where semantics depend on a database. Inspect skips and record DSNs' presence, server versions and extension limits in the verification result. Promise tags provide traceability, while actual assertions and executed suites provide behavioral evidence. [gate](ds:block?id=gate-9g2wphfm) [promise gate](ds:block?id=promise-gate-a57aksdv) [postgres gate](ds:block?id=postgres-gate-ctc94x6a) [mysql gate](ds:block?id=mysql-gate-xzbtwu8b) [postgres extensions](ds:block?id=postgres-extensions-j5kc5mqu)

### Persist explanations with their code references

Store verified project facts as Lore memories, multi-layer explanations as architecture notes, reusable approaches as patterns and operational traps as hotfixes. Cite stable Docsync definitions in the public explanation and commit the shared Lore JSON with it. This makes future code changes expose stale explanatory claims through task check, instead of leaving knowledge only in a completed conversation. [gate](ds:block?id=gate-9g2wphfm) [promise gate](ds:block?id=promise-gate-a57aksdv)

## Traps and recovery

### One has two cardinality contracts

scan.One returns the first row and does not test for a second; exec.One returns ErrMultipleRows for a bound query matching more than one row. Use the API whose contract fits, or use scan.All and check the length when asserting uniqueness. Do not add LIMIT 1 to hide a duplicate when uniqueness is the requirement. [reader cardinality](ds:block?id=reader-cardinality-332zvda7&assert=true) [bound one](ds:block?id=bound-one-x39s9kka)

### Reader projection columns are appended

Pass a select with no existing projection to scan.All/One. Reader columns append; they do not replace or ignore existing selected columns. database/sql then rejects a wider result than the Reader's destinations. Put desired fields in the Reader. This replaces the old, incorrect comment that extra columns were selected but never scanned. [reader projection](ds:block?id=reader-projection-3xfnp55v&assert=true) [reader flow](ds:block?id=reader-flow-xcq2taqt)

### NULL changes the result type, not the SQL

Nullable changes Expr[T] to Expr[*T] without inserting a SQL cast or changing nullability in the database. Use nullable destinations for outer-join missing rows and nullable columns; AssertNotNull is an assertion, not a runtime filter. A non-null declaration does not make a LEFT JOIN result non-null. Check the query shape and actual scan errors rather than treating zero values as missing data. [column and null](ds:block?id=column-and-null-p2fy3sp5) [null scan](ds:block?id=null-scan-9xxzyqc7&assert=true)

### Execution guards do not own arbitrary caller memory

Do not cache a bound query across concurrent calls or share destination variables between independently built queries. Shared derived-query guards detect overlaps only within that guard family, and struct-literal Query values have no guard. Build per call or use a correctly allocated Reader. Copy Each callback values before retaining them. [query ownership](ds:block?id=query-ownership-vsv882ny) [query overlap](ds:block?id=query-overlap-5hddx8sm&assert=true) [bound each](ds:block?id=bound-each-qvvzm6tq) [reader sharing](ds:block?id=reader-sharing-79k4pyx5&assert=true)

### MySQL equality follows column collation

MySQL 8.4's default utf8mb4_0900_ai_ci can equate strings differing in case and accents. The conformance fixture uses binary collation when exact comparison is required. Select the schema collation intentionally; changing only a dialect does not make Eq byte-exact. The explicit default-collation test demonstrates the difference. [mysql collation](ds:block?id=mysql-collation-vqcp6ms3&assert=true)

### MySQL numeric parameters and literal refusals

Preserve the NumericParam hook: integer parameters are cast SIGNED/UNSIGNED to avoid unintended DECIMAL widening in numeric expressions. Inline literal rendering deliberately rejects values it cannot faithfully spell, including MySQL strings with backslashes and SQLite time values in the tested contexts. Handle these build errors; do not bypass them by pasting runtime values into raw SQL. [mysql numbers](ds:block?id=mysql-numbers-zxvc6pdk) [literal refusals](ds:block?id=literal-refusals-3xx754he&assert=true) [MySQL literal cases](ds:block?id=mysql-literals-23295m4b&assert=true)

### SQLite pragmas belong to connections

Use the SQLite wrapper's DSN pragma handling so settings reach new pooled connections, rather than applying a one-off PRAGMA to an arbitrary connection. Memory defaults to one connection because separate :memory: connections see separate databases. Do not override that pool size without changing the database-sharing design; Open itself does not ping, so first-use errors still need handling. [sqlite pragmas](ds:block?id=sqlite-pragmas-d6qmetcs&assert=true) [sqlite open](ds:block?id=sqlite-open-jda68c72) [sqlite memory](ds:block?id=sqlite-memory-kyedhky9)

### Skipped server tests are not server proof

SQLB_PG_DSN and SQLB_MYSQL_DSN gate live suites. PostgreSQL array_sort, its descending form and array_reverse are exercised only at version 18 or newer; the old engine guide said 17. Extension tests can skip when the server lacks required extensions. Read verbose skips and state coverage limits, including the absence of a ClickHouse driver suite, before describing a green gate as multi-engine proof. [postgres gate](ds:block?id=postgres-gate-ctc94x6a) [mysql gate](ds:block?id=mysql-gate-xzbtwu8b) [postgres arrays](ds:block?id=postgres-arrays-5w7ytu86&assert=true) [postgres extensions](ds:block?id=postgres-extensions-j5kc5mqu) [workspace](ds:block?id=workspace-f7vpgvxp)

### DDL flags and MySQL partial commits

A ddl.Script is not just a list of strings. Honor OutsideTransaction, Transactional, MustReturnNoRows and SkipIf through Execute, and pin the connection for session locks. MySQL DDL commits implicitly, so a failure may leave schema changes without a ledger row. Make migrations safe to repeat and inspect partial state before retrying; PostgreSQL/SQLite transactional behavior cannot be assumed for MySQL. [ddl contract](ds:block?id=ddl-contract-s2xdb9he) [migration execute](ds:block?id=migration-execute-hgdykxdq) [migration ledger](ds:block?id=migration-ledger-h5dv2yvb)

### A root-only test command misses nested modules

Use task check with its Go, Task, Python/PyYAML and Docsync prerequisites, plus lore setup/render when hydrating a clone. Root go test ./... alone excludes nested contrib modules and the documentation/negative-build gates. A baseline task check with no database DSNs is useful, but its passing result does not execute the gated server suites. [workspace](ds:block?id=workspace-f7vpgvxp) [gate](ds:block?id=gate-9g2wphfm) [postgres gate](ds:block?id=postgres-gate-ctc94x6a) [mysql gate](ds:block?id=mysql-gate-xzbtwu8b)
