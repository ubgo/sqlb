# Test coverage

Measured per module on Go 1.24 by [`scripts/covmerge.py`](./scripts/covmerge.py), which runs each package separately and sums the counters. Regenerate with `task cover`. CI enforces a floor per module in [`.github/workflows/test.yml`](./.github/workflows/test.yml), so these numbers cannot silently regress.

| Module | Coverage | CI floor |
|---|---:|---:|
| `github.com/ubgo/sqlb` | **88.4%** | 80% |
| `contrib/driver-sqlite` | **97.7%** | 90% |
| `contrib/structbind` | **96.8%** | — |
| `contrib/migrate` | **91.7%** | — |
| `contrib/debug` | **91.5%** | — |
| `contrib/vet` | **77.4%** | — |
| `contrib/driver-pgx` | **11.1%** | — |

Real output of `task cover`:

```
.: 88.4%
contrib/driver-sqlite: 97.7%
contrib/driver-pgx: 11.1%
contrib/vet: 77.4%
contrib/migrate: 91.7%
contrib/debug: 91.5%
contrib/structbind: 96.8%
```

`contrib/driver-pgx` reads 11.1% without a server and that number is honest rather than alarming: its suite is gated on `SQLB_PG_DSN` and skips loudly when it is unset, so an ordinary run measures the module's own `Open` and little else. With a Postgres 17 to talk to, the same suite executes 555 cases. A coverage figure for a module whose job is to reach a server is a statement about the environment it ran in, not about the tests.

## Why the obvious command gives the wrong answer

`go test -coverpkg=./... -covermode=atomic -coverprofile=cover.out ./...` reports **24.5%** for the core, and that number is wrong rather than merely pessimistic.

With `-coverpkg` every test binary emits counters for every package in the module. The profile the go command writes for a multi-package run keeps one copy of each block instead of summing the copies, and the copy that survives is usually from a binary that never executed it. The effect is not subtle: `pg` measures 40.9% when tested on its own, and in the `./...` run every single one of its blocks reads zero.

It fails in the dangerous direction. A number that reads low looks like missing tests, so the reflex is to go and write tests that already exist. `covmerge.py` runs each package separately and adds the counts up, which is what the merged profile was supposed to be.

## Why `-coverpkg` matters here

Run `go test ./...` at the root without it and the total reads far lower. That number is wrong in a way worth understanding, because it will mislead anyone who measures this repository casually.

Most of the core is exercised from outside itself. The catalogues under `examples/` are external test packages: `examples/pgsurface` builds hundreds of statements against the core and checks every one against its expected SQL, and `examples/cookbook` does the same for the composition recipes. Go's default per-package measure credits coverage only to the package under test, so all of that work is attributed to `examples/pgsurface` and none of it to the core, whose own `_test.go` files then look like the only thing touching it.

`-coverpkg=./...` credits each test with every package it actually executes, which is what the catalogues are for.

## What the numbers do not measure

Coverage counts executed statements. It does not count whether the SQL produced was correct, and that is the property this library actually needs. Two other gates carry that weight:

- **The catalogues** check the rendered SQL and the bound arguments of every statement, so a change in rendering fails the build. Coverage would stay green through a rendering change; the `// Output:` blocks would not.
- **The conformance suite** runs the builder against a real database, which is the only way to prove that placeholders, argument order and scan targets line up. It contributes to `contrib/driver-sqlite`'s number and is the reason that module has one at all.

And one gate measures the opposite of coverage. `examples/typecheck/bad.go` must never compile, so it is never executed and never counted. A type-safety claim is proven by a compiler that refuses, not by a line that ran.

## The remainder, enumerated

`task test:uncovered` prints every block no test reached. What it currently prints, and why:

- **110 blocks in `sqlbtest`**, which is the conformance suite itself. It is executed in full by `contrib/driver-sqlite`, a different module and therefore a different profile. It is measured, just not here.
- **11 blocks in `pg`**, each the dialect-guard arm of one more helper. The arm's behaviour is pinned for a large sample of constructors in `pg/guards_test.go`, and the remainder are the same three lines in a different function. Closing each one asserts nothing the sweep has not already asserted.
- **11 blocks in `examples/`**, which are option branches in the demonstration packages. They are documentation that compiles, and the shape they demonstrate is already checked by the `Example` output.
- **1 block in `contrib/driver-sqlite`**, the arm where `sql.Open` returns an error. It is unreachable: `sql.Open` fails only for an unregistered driver name, the name is a constant, and the driver registers itself on import. There is no input that reaches it.

The number moved down a tenth of a point when eleven unguarded constants were given guards. That is the shape of an honest coverage number: closing a real hole adds arms, and the arms it adds are the ones that only run when someone makes a mistake.
