# Commands

Every command this repository uses, and why. Every command is a [Taskfile](https://taskfile.dev) task, so `task --list` shows them all and nothing here has to be retyped. The first half is the workflows; the second half is generated from `Taskfile.yml` and is every task with the exact commands it runs, so it cannot drift from what the tasks do.

## Prerequisites

| Tool | Needed for | Check |
|---|---|---|
| Go 1.24 or later | everything | `go version` |
| [task](https://taskfile.dev) | running the tasks below | `task --version` |
| Python 3 with PyYAML | the documentation gates | `python3 -c "import yaml"` |
| golangci-lint | `task lint` | `golangci-lint --version` |
| a local Postgres 17 with `psql`, `createdb` and `dropdb` | the Postgres proof | `psql -d postgres -c 'select version()'` |
| Docker | the MySQL proof, in a throwaway container | `docker info` |
| curl | `task detector:sqlite` only | `curl --version` |

SQLite needs nothing: its driver is pure Go and every SQLite test runs in memory.

## The gate

```sh
task check    # gofmt, vet and tests in every module, the negative type-check build, the zero-dependency check, and every documentation gate
task lint     # golangci-lint in every module; must report zero issues
```

`task check` is the gate a change is held to. CI exists but is `workflow_dispatch` only, because this repository is private and GitHub Actions minutes are metered, so the local gate is the real one. `task check` runs the Postgres and MySQL proofs too, but they skip loudly when no server is configured; the next section is how to run them for real.

## Running the proof on all three engines

Rendering tests prove the text. Only a real database proves the SQL, and every engine has found bugs its rendering tests passed. One command runs all three:

```sh
task test:engines
```

It creates a scratch Postgres database, starts a throwaway MySQL 8.4 container, runs the SQLite, Postgres and MySQL proofs with the race detector, and removes both servers when it finishes, including when a proof fails.

The steps one at a time, for when you want the servers to stay up between runs:

```sh
task engines:pg:up        # createdb sqlb_scratch
task engines:mysql:up     # docker run mysql:8.4 on 127.0.0.1:33069, then waits until it accepts queries
task test:sqlite          # contrib/driver-sqlite, in memory and file-backed
task test:pg              # contrib/driver-pgx against postgres:///sqlb_scratch
task test:mysql           # contrib/driver-mysql against root:sqlb@tcp(127.0.0.1:33069)/sqlb
task engines:mysql:down   # docker stop; --rm removes the container and its data
task engines:pg:down      # dropdb --if-exists sqlb_scratch
```

Any default can be overridden on the command line:

```sh
task test:pg PG_DSN='postgres://user:pw@db.internal:5432/sqlb_test?sslmode=disable'
task test:mysql MYSQL_DSN='user:pw@tcp(db.internal:3306)/sqlb_test'
task engines:mysql:up MYSQL_PORT=33070 MYSQL_IMAGE=mysql:8.0
task engines:pg:up PG_DB=my_scratch
```

Without `task`, the proofs are ordinary `go test` runs with one environment variable each:

```sh
createdb sqlb_scratch
docker run -d --rm --name sqlb-mysql-scratch -e MYSQL_ROOT_PASSWORD=sqlb -e MYSQL_DATABASE=sqlb -p 127.0.0.1:33069:3306 mysql:8.4
docker exec sqlb-mysql-scratch mysql -uroot -psqlb -e 'SELECT 1' sqlb   # repeat until it succeeds, which takes a few seconds on first start

(cd contrib/driver-sqlite && go test -race ./...)
(cd contrib/driver-pgx && SQLB_PG_DSN='postgres:///sqlb_scratch?sslmode=disable' go test -race ./...)
(cd contrib/driver-mysql && SQLB_MYSQL_DSN='root:sqlb@tcp(127.0.0.1:33069)/sqlb' go test -race ./...)

docker stop sqlb-mysql-scratch
dropdb sqlb_scratch
```

| Variable | Read by | When unset |
|---|---|---|
| `SQLB_PG_DSN` | `contrib/driver-pgx` tests | every Postgres test skips, and says so |
| `SQLB_MYSQL_DSN` | `contrib/driver-mysql` tests | every MySQL test skips, and says so |

A skip is not a pass. `ok` from `go test` is printed for a skipped package too, so a run that must prove something is run with `-v` or through the tasks above, which set the variable. `task test:mysql` with no server running fails on the connection rather than skipping, which is how to tell the variable reached the tests.

## The DDL detectors

Each engine's DDL detector checks that every statement the engine has is classified as built, partly built, planned or out of scope, against a list captured from the engine itself:

```sh
task detector              # run all three against their checked-in lists
task detector:pg           # refetch with psql \h from the local Postgres, then run (PG_MAJOR=18 for a new version)
task detector:sqlite       # refetch sqlite.org/lang.html, then run
task detector:mysql        # refetch HELP 'Data Definition' from the scratch container, then run (needs engines:mysql:up)
```

A refetch that adds a statement fails the detector until someone classifies it, and the classification is the decision.

## Documentation

```sh
task docs:check            # links and anchors, compiled Setup blocks, snippet names, export coverage, and this page
task docs:reference        # regenerate the reference table at the end of each engine's page
task docs:commands:gen     # regenerate the task reference below from Taskfile.yml
```

After adding an export to an engine package, run `task docs:reference`; after adding or changing a task, run `task docs:commands:gen`. The gate fails until you do, which is the point.

## Coverage, formatting and modules

```sh
task cover                 # total coverage per module, measured by scripts/covmerge.py
task test:uncovered        # every statement no test reaches, worst file first
task test:race             # every module's tests with the race detector, twice
task fmt                   # gofmt -w .
task tidy                  # go mod tidy in every module
```

Coverage is always measured through `scripts/covmerge.py`, never by one `go test -coverpkg=./... ./...` run, which keeps the wrong copy of each block and reports the core far lower than it is. See [COVERAGE.md](./COVERAGE.md).

## Releases

```sh
task release:dryrun        # print the next patch, minor and major tags
task release:patch         # tag the next patch; pushing is a separate step: git push origin --tags
```

## Every task

<!-- BEGIN GENERATED TASKS -->

Generated from `Taskfile.yml` by `python3 scripts/gen-commands-doc.py`; `task docs:commands` fails when it is stale. 54 tasks.

`MODULES` is `. contrib/driver-sqlite contrib/driver-pgx contrib/driver-mysql contrib/vet contrib/migrate contrib/debug contrib/structbind`; a task that says *for each MODULE* runs its step in each of those directories.

### `task check`

The gate - every module formatted, vetted and tested, plus the core's own gates

```sh
task check:modules
task typecheck
task zerodep
task reuse:check
task tasks:check
task docs:check
```

### `task check:modules`

fmt, vet and test in every module

```sh
task module:check
```

### `task cookbook`

Print the cookbook recipes with their rendered SQL

```sh
go test ./examples/cookbook -run Example -v
```

### `task cover`

Print total coverage per module

```sh
task module:cover
```

### `task default`

Show available tasks

```sh
task --list
```

### `task detector`

Run every DDL detector against its checked-in list, without refetching

```sh
go test ./pg/ ./sqlite/ ./mysql/ -run TestDetector -v
```

### `task detector:mysql`

Re-read MySQL's DDL statement list from the scratch container into mysql/testdata and run the detector

```sh
{
  echo "# MySQL $(docker exec sqlb-mysql-scratch mysql -uroot -psqlb -N -e 'SELECT version()' 2>/dev/null): HELP 'Data Definition', captured $(date +%Y-%m-%d) from the scratch container."
  docker exec sqlb-mysql-scratch mysql -uroot -psqlb -N -e "HELP 'Data Definition'" 2>/dev/null | tail -n +2 | sed 's/^ *//'
} > mysql/testdata/ddl-8.4.txt
go test ./mysql/ -run TestDetector -v
```

### `task detector:pg`

Re-read Postgres's command list and ALTER TABLE grammar from a live server into pg/testdata (psql must reach it; PG_MAJOR names the files)

Variables, each overridable on the command line as `task detector:pg NAME=value`:

- `PG_MAJOR`, default `17`

```sh
psql -X -c '\h' > pg/testdata/commands-{{.PG_MAJOR}}.txt
psql -X -c '\h ALTER TABLE' > pg/testdata/alter-table-{{.PG_MAJOR}}.txt
go test ./pg/ -run TestDetector -v
```

### `task detector:sqlite`

Re-fetch SQLite's grammar index into sqlite/testdata/lang-index.txt and run the detector

```sh
curl -sS --fail --max-time 30 https://sqlite.org/lang.html | python3 scripts/sqlite-lang-index.py > sqlite/testdata/lang-index.txt.new
mv sqlite/testdata/lang-index.txt.new sqlite/testdata/lang-index.txt
go test ./sqlite/ -run TestDetector -v
```

### `task docs:check`

Verify the markdown - links resolve, anchors match, no rules, no hard wraps

```sh
python3 scripts/check-docs.py
task docs:setup
task docs:names
task docs:commands
task docs:not-built
```

### `task docs:commands`

FAIL if COMMANDS.md does not describe every task in this Taskfile exactly; regenerate with python3 scripts/gen-commands-doc.py

```sh
python3 scripts/gen-commands-doc.py --check
```

### `task docs:commands:gen`

Regenerate the task reference in COMMANDS.md from this Taskfile

```sh
python3 scripts/gen-commands-doc.py
```

### `task docs:coverage`

Fail when a package's exports are not named in the documentation

```sh
python3 scripts/check-doc-coverage.py
```

### `task docs:names`

FAIL if a documentation snippet names a symbol that does not exist

```sh
python3 scripts/check-doc-names.py
task docs:coverage
```

### `task docs:not-built`

FAIL if docs/ddl/not-built.md lacks a section for a partial or left-out detector entry, or its appendix is stale

```sh
python3 scripts/gen-not-built.py --check
```

### `task docs:not-built:gen`

Regenerate the detector appendix in docs/ddl/not-built.md

```sh
python3 scripts/gen-not-built.py
```

### `task docs:reference`

Regenerate the reference table in each engine package's page

```sh
python3 scripts/regen-reference.py
```

### `task docs:setup`

FAIL if a per-package page's Setup block does not compile

```sh
python3 scripts/check-doc-setup.py
```

### `task engines:mysql:down`

Stop the scratch MySQL container, which --rm then removes with its data

```sh
docker stop sqlb-mysql-scratch
```

### `task engines:mysql:up`

Start a throwaway MySQL 8.4 container on 127.0.0.1:{{.MYSQL_PORT}} and wait until it accepts queries

Variables, each overridable on the command line as `task engines:mysql:up NAME=value`:

- `MYSQL_PORT`, default `33069`
- `MYSQL_IMAGE`, default `mysql:8.4`

```sh
docker run -d --rm --name sqlb-mysql-scratch -e MYSQL_ROOT_PASSWORD=sqlb -e MYSQL_DATABASE=sqlb -p 127.0.0.1:{{.MYSQL_PORT}}:3306 {{.MYSQL_IMAGE}}
task engines:mysql:wait
```

### `task engines:mysql:wait`

Wait up to two minutes for the scratch MySQL to accept queries (its first start initialises the data directory)

```sh
for i in $(seq 1 60); do
  if docker exec sqlb-mysql-scratch mysql -uroot -psqlb -e 'SELECT 1' sqlb >/dev/null 2>&1; then
    echo "mysql ready"; exit 0
  fi
  sleep 2
done
echo "mysql did not become ready"; exit 1
```

### `task engines:pg18:down`

Stop the scratch Postgres 18 container

```sh
docker rm -f sqlb-pg18-scratch >/dev/null 2>&1 || true
```

### `task engines:pg18:up`

Start a throwaway Postgres 18 container on 127.0.0.1:{{.PG18_PORT}} and wait until it accepts queries, for the constructs that need 18

Variables, each overridable on the command line as `task engines:pg18:up NAME=value`:

- `PG18_PORT`, default `54318`
- `PG18_IMAGE`, default `postgres:18`

```sh
docker run -d --rm --name sqlb-pg18-scratch -e POSTGRES_PASSWORD=sqlb -p 127.0.0.1:{{.PG18_PORT}}:5432 {{.PG18_IMAGE}}
for i in $(seq 1 60); do
  if docker exec sqlb-pg18-scratch pg_isready -U postgres >/dev/null 2>&1; then exit 0; fi
  sleep 2
done
echo "sqlb-pg18-scratch did not accept queries within two minutes" >&2
exit 1
```

### `task engines:pg:down`

Drop the scratch Postgres database {{.PG_DB}}

Variables, each overridable on the command line as `task engines:pg:down NAME=value`:

- `PG_DB`, default `sqlb_scratch`

```sh
dropdb --if-exists {{.PG_DB}}
```

### `task engines:pg:up`

Create the scratch Postgres database {{.PG_DB}} on the local server psql reaches

Variables, each overridable on the command line as `task engines:pg:up NAME=value`:

- `PG_DB`, default `sqlb_scratch`

```sh
createdb {{.PG_DB}}
```

### `task fmt`

Format every Go file

```sh
gofmt -w .
```

### `task lint`

Run golangci-lint in every module

```sh
task module:lint
```

### `task module:check` (internal)

Internal: called by other tasks, not run directly.

Runs in `{{.DIR}}`.

```sh
echo "--- {{.DIR}}"
test -z "$(gofmt -l .)" || (gofmt -l . && exit 1)
go vet ./...
go test ./...
```

### `task module:cover` (internal)

Internal: called by other tasks, not run directly.

Runs in `{{.DIR}}`.

```sh
python3 {{if eq .DIR "."}}scripts{{else}}../../scripts{{end}}/covmerge.py cover.out
echo "{{.DIR}}: $(go tool cover -func=cover.out | tail -1 | awk '{print $NF}')"
rm -f cover.out
```

### `task module:lint` (internal)

Internal: called by other tasks, not run directly.

Runs in `{{.DIR}}`.

```sh
golangci-lint run ./...
```

### `task module:race` (internal)

Internal: called by other tasks, not run directly.

Runs in `{{.DIR}}`.

```sh
go test -race -count=2 ./...
```

### `task module:test` (internal)

Internal: called by other tasks, not run directly.

Runs in `{{.DIR}}`.

```sh
go test ./...
```

### `task module:tidy` (internal)

Internal: called by other tasks, not run directly.

Runs in `{{.DIR}}`.

```sh
go mod tidy
```

### `task module:uncovered` (internal)

Internal: called by other tasks, not run directly.

Runs in `{{.DIR}}`.

```sh
python3 {{if eq .DIR "."}}scripts{{else}}../../scripts{{end}}/covmerge.py cover.out
echo "--- {{.DIR}}"
sh {{if eq .DIR "."}}scripts{{else}}../../scripts{{end}}/uncovered.sh cover.out
```

### `task release:dryrun`

Print the next tags for each bump without tagging

```sh
echo "current: $(git describe --tags --abbrev=0 2>/dev/null || echo none)"
echo "patch -> $(sh scripts/nextver.sh patch)"
echo "minor -> $(sh scripts/nextver.sh minor)"
echo "major -> $(sh scripts/nextver.sh major)"
```

### `task release:major`

Tag the next major release (no push)

```sh
git tag "$(sh scripts/nextver.sh major)"
echo "tagged $(git describe --tags --abbrev=0). Push with: git push origin --tags"
```

### `task release:minor`

Tag the next minor release (no push)

```sh
git tag "$(sh scripts/nextver.sh minor)"
echo "tagged $(git describe --tags --abbrev=0). Push with: git push origin --tags"
```

### `task release:patch`

Tag the next patch release (no push)

```sh
git tag "$(sh scripts/nextver.sh patch)"
echo "tagged $(git describe --tags --abbrev=0). Push with: git push origin --tags"
```

### `task reuse:check`

FAIL if a function taking a statement is not covered by the reuse test

```sh
python3 scripts/check-reuse-coverage.py
```

### `task surface`

Print the Postgres surface catalogue with its rendered SQL

```sh
go test ./examples/pgsurface/... -run Example -v
```

### `task tasks:check`

FAIL if a package has fewer end-to-end task tests than its floor

```sh
python3 scripts/check-task-tests.py
```

### `task test`

Run every test in every module

```sh
task module:test
```

### `task test:cover`

Write a coverage profile per module and print each total

```sh
task module:cover
```

### `task test:cover:html`

Open an HTML coverage report for the core module

```sh
go test -coverpkg=./... -coverprofile=cover.out -covermode=atomic ./...
go tool cover -html=cover.out -o cover.html
echo "wrote cover.html"
```

### `task test:engines`

Start every scratch server, run the SQLite, Postgres 17, Postgres 18 and MySQL proofs, and remove the servers even when a proof fails

```sh
task engines:pg:up
task engines:pg18:up
task engines:mysql:up
defer: task engines:mysql:down   # runs when the task ends, even if a step failed
defer: task engines:pg18:down   # runs when the task ends, even if a step failed
defer: task engines:pg:down   # runs when the task ends, even if a step failed
task test:sqlite
task test:pg
task test:pg18
task test:mysql
```

### `task test:mysql`

Run the MySQL proof against MYSQL_DSN (default the scratch container) with the race detector - driver-mysql, vet's catalogue and drift checks, and the migration ledger

Variables, each overridable on the command line as `task test:mysql NAME=value`:

- `MYSQL_DSN`, default `root:sqlb@tcp(127.0.0.1:33069)/sqlb`

Environment it sets for its commands:

- `SQLB_MYSQL_DSN`, set from `MYSQL_DSN`

```sh
go test -race -count=1 ./contrib/driver-mysql/... ./contrib/vet/... ./contrib/migrate/...
```

### `task test:pg`

Run the Postgres proof against PG_DSN (default the scratch database) with the race detector - driver-pgx, vet's catalogue and drift checks, and the migration ledger

Variables, each overridable on the command line as `task test:pg NAME=value`:

- `PG_DSN`, default `postgres:///sqlb_scratch?sslmode=disable`

Environment it sets for its commands:

- `SQLB_PG_DSN`, set from `PG_DSN`

```sh
go test -race -count=1 ./contrib/driver-pgx/... ./contrib/vet/... ./contrib/migrate/...
```

### `task test:pg18`

Run the Postgres proof against the scratch Postgres 18 container, which is what covers the constructs 17 refuses - virtual generated columns, NOT ENFORCED constraints, the wider ALTER CONSTRAINT and temporal keys

Variables, each overridable on the command line as `task test:pg18 NAME=value`:

- `PG18_DSN`, default `postgres://postgres:sqlb@127.0.0.1:54318/postgres?sslmode=disable`

Environment it sets for its commands:

- `SQLB_PG_DSN`, set from `PG18_DSN`

```sh
go test -race -count=1 ./contrib/driver-pgx/... ./contrib/vet/... ./contrib/migrate/...
```

### `task test:race`

Run every test with the race detector

```sh
task module:race
```

### `task test:sqlite`

Run the SQLite proof - the conformance suite and every sqlite construct and DDL statement - with the race detector

Runs in `contrib/driver-sqlite`.

```sh
go test -race -count=1 ./...
```

### `task test:uncovered`

List every statement with zero coverage, per module, worst file first

```sh
task module:uncovered
```

### `task tidy`

Tidy every module

```sh
task module:tidy
```

### `task typecheck`

FAIL unless every deliberately wrong statement in examples/typecheck is rejected by the compiler, line by line

```sh
python3 scripts/check-typecheck.py
```

### `task zerodep`

FAIL if the core module gained a third-party dependency

```sh
if grep -E '^[[:space:]]+[^/[:space:]]+\.[^/[:space:]]+/' go.mod | grep -v 'github.com/ubgo'; then
  echo "the sqlb core has a third-party dependency - it belongs in contrib/"; exit 1
fi
echo "zerodep: the core imports nothing outside the standard library"
```

<!-- END GENERATED TASKS -->
