# periodcompare

A period-comparison report over several fact tables, built with `sqlb` for Postgres. It is the worked example for the hardest shape a reporting API has: one row per dimension value, measures drawn from four or five different tables, three time periods side by side, dozens of optional filters, computed ratios that the caller can filter and sort on, pagination with a total count, and sources that switch on and off by request.

Every construct here is typed. Every column, join, comparison and scan target is checked by the compiler. Every request value is a bound parameter. The SQL of every example is checked by `go test`.

## The problem

A comparison report answers "how did each vendor, store, product, department, class or fiscal month do this period against the same period last year and the year before, with stock, open orders and ad spend beside it". The data lives in different tables with different grains:

| Source | Grain | Measures |
|---|---|---|
| `sales_lines` | one line per sale, dated | units, sales, cost, margin, returns, for each period |
| `stock_levels` | current stock per product and store | on hand, on-hand retail |
| `stock_snapshots` | stock per product and store per date | on hand at the end of the previous period |
| `po_lines` | open purchase order lines | on order, on-order retail |
| `ad_spend` | ad cost per product per day | ad cost |

The report needs one row per key with all of it, and the key is not a column. It is a different expression per dimension, and some dimensions need a join chain from the product to its category.

## What the usual designs cost

**Join chain.** `current FULL OUTER JOIN previous LEFT JOIN stock LEFT JOIN ...` on the key. It loses keys: a product that sold last year but not this year, and has stock, has no `current` row to hang the stock on. Each new source adds a join and a `COALESCE(current.key, previous.key, ...)` that grows with it.

**Wide union.** Every source becomes a sub-select with the same column list, in the same order, where a source fills its own measures and writes a typed zero for every other one. `UNION ALL`, then `SUM` every column per key. It keeps every key, and it forces the hacks:

- Every source repeats the full measure list. A new measure means a new `0 AS x` in every branch, and a missed one breaks the union at run time because branches line up by position.
- Same-table periods become separate branches, so the sales table is scanned once per period.
- A fiscal-month key collides across years, so the previous year's key is offset by a constant and mapped back to a label at the end.
- Computed ratios are aliases of the outer `SELECT`, so a numeric filter on them has to be a `HAVING` on the alias, and the alias name has to be rebuilt from the request string.
- The total count is a second statement that must repeat the whole union.
- Filters are mapped to columns per source by name, and a source that lacks the column skips the filter silently.
- Everything is a string, so an alias typo or a missing join is a database error, not a compile error.

## The shape used here

```sql
WITH sales AS (
  SELECT <key> AS key,
         sum(qty) FILTER (WHERE sold_on BETWEEN $cur_from AND $cur_to)   AS current_units,
         sum(qty) FILTER (WHERE sold_on BETWEEN $prev_from AND $prev_to) AS previous_units,
         ...
  FROM sales_lines s JOIN <dimension tables> WHERE <row filters> AND sold_on BETWEEN $earliest AND $latest
  GROUP BY <key>),
stock AS (SELECT <key> AS key, sum(on_hand) AS on_hand, ... FROM stock_levels ... GROUP BY <key>),
on_order AS (...), ad_spend AS (...),
keys AS (SELECT key FROM sales UNION SELECT key FROM stock UNION ...),
report AS (
  SELECT k.key,
         COALESCE(sales.current_units, 0) AS current_units, ..., 0 AS third_units,
         (current - previous) / NULLIF(previous, 0) * 100 AS units_pct, ...
  FROM keys k LEFT JOIN sales USING (key) LEFT JOIN stock USING (key) ...)
SELECT r.*, count(*) OVER ()
FROM report r LEFT JOIN labels USING (key)
WHERE r.sell_through > $1
ORDER BY r.roas DESC NULLS LAST, r.key ASC
LIMIT $2 OFFSET $3
```

Each decision, and the hack it removes:

- **One CTE per source, selecting only its own measures.** No zero list per source. A new measure touches one CTE and one line of the report CTE.
- **`keys` is the union of every source's keys, and `report` LEFT JOINs each source onto it.** Every key survives, as with the wide union, but the join is on a distinct key set, not on the full fact rows.
- **Same-table periods are `FILTER` aggregates on one scan.** The sales table is read once over the widest date span; each period is a filter on the aggregate.
- **A disabled source is not joined, and its measures are typed zeros chosen once, in the report CTE.** A ratio whose denominator would come from a disabled source is `NULL`, not a number computed over a zero that looks like data.
- **Computed metrics are columns of the `report` CTE.** The outer query filters and sorts on them with plain `WHERE` and `ORDER BY`, by column name from a closed vocabulary.
- **The total count is `count(*) OVER ()` in the same statement.** One round trip, and the count always agrees with the page.
- **The fiscal-month key is the period index, not the label.** The same period of three years lands on one row without an offset trick; a `labels` CTE attaches the current year's label at the end.
- **A filter that a source cannot honour is reported, not skipped.** `Result.SkippedFilters` names it, and `Result.DisabledSources` names every source that is out.

## The pieces

```
schema.go      tables and typed columns
dimension.go   Dimension: the key expression and the joins it needs, per source; the label CTE
params.go      Params, Filters, Include, Sort, Page; the closed vocabularies
metrics.go     Metric names, their typed report columns, and Row, the scan destination
filters.go     which filter applies to which source, the SKU-set filters, the final filters
sources.go     one function per source, each returning its CTE and its measure names
report.go      Build: keys, report CTE, labels, final filters, sort, page, total
report_test.go five checked examples and two tests
```

How the parts fit:

1. `Build` asks each source for its CTE. A source checks whether the dimension can be served from its columns, builds the key with the dimension's plan, applies the row filters it can honour, and groups by the key.
2. `Build` unions the keys of the enabled sources into `keys`, then writes `report` as `keys` LEFT JOIN each source. Every measure goes through `measures`, which returns either `COALESCE(source.col, 0)` or a typed zero literal.
3. The computed metrics are expressions over those measures. Ratios use `NULLIF` and are typed `*float64`, so a zero denominator scans as nil.
4. The outer statement binds every report column into `Row`, joins the label CTE when the dimension has one, applies the key and numeric filters, sorts by a `Metric` from the closed set with the key as tiebreak, pages, and adds the window count.

## Extending it

- **A new source.** Add a table and its columns in `schema.go`, a function in `sources.go` that returns its CTE and measure names, and the `measures` reads plus any computed metric in `report.go`. Nothing else changes.
- **A new dimension.** Add the constant, its `serves` rule and its `plan` in `dimension.go`, and a label CTE if the key is not its own label.
- **A new filter.** Add the field to `Filters` and its rule in `rowFilters`, stating which sources it applies to. If a source cannot honour it, append to `skipped`.
- **A new metric.** Add the `Metric` constant, its typed column in `metricColumn`, the expression in `report.go` and the field in `Row`.

## What is typed and what is not

Typed by the compiler: every column reference, every join condition, every comparison between a column and a value, every `Bind` into `Row`, every aggregate's operand, the dimension and metric vocabularies.

Checked at build time, before the database: unknown dimension, unknown metric in a filter or sort, unknown numeric operator, a fiscal report without a year, a duplicate alias in a statement.

Not checked here: that a column exists in the database, and that the SQL runs. The examples prove rendering. No statement in this package has run against a Postgres server yet.

## Known costs

- A bound value that appears in several aggregates is bound once per use, so a three-period report binds the same dates many times. Postgres does not mind; the argument list is long. A single placeholder per value is an open item in the builder spec.
- The label CTE for the product dimension groups every product. On a large catalogue it should be filtered to the keys of the page; that is a `WHERE key IN (SELECT key FROM report ...)` away.

## Shape or tool: what fixed the report

It is worth being exact about which part of this result the builder deserves, because the claim "a typed builder fixed the report" is not true and a reader who believes it will draw the wrong lesson.

**The query shape fixed the report, and the shape is portable.** A keys CTE, one CTE per source, `FILTER` aggregates for the periods, a report CTE for the computed metrics, a window count. Every engine has these, ClickHouse included with `sumIf` for `FILTER`, and any string builder can render them. A team with a string builder could have written this shape. The reason such teams end up with the wide union instead is history, not tooling: a join chain loses keys, the union fixes that under time pressure, and every later feature is added to the union as it stands because nobody pays for a second redesign.

**A string builder lets the hacks grow, and a typed builder does not.** The difference is not what each can render. It is what each refuses:

| Effect | Where it comes from |
|---|---|
| Every key survives, one scan per table, filters on computed metrics, count in the same statement | The shape. Portable to any engine and any builder |
| A disabled source becomes a typed zero or `NULL`, chosen in one place | The Go structure of `Build`. Possible with strings, natural with typed expressions |
| A wrong column, join, scan target or sort field fails before the database | The typed builder. A string builder cannot do this |
| No request value in the SQL text, ever | The typed builder. A string builder allows it and the quick path takes it |
| The SQL of every variant is checked by a test | The `Example` convention. Possible with strings; rarely done |

The mechanism is the string door. With a string builder, the shortest path to a date filter is a literal in a `Sprintf`, the shortest path to a sort is the request field capitalised into `ORDER BY`, and the shortest path to a per-source column is `alias + "." + name`. Each is quicker than the careful path, so each is taken, and each is a run-time failure waiting for the request that triggers it. With no string door, the careful path is the only path: a table and its columns are declared once, and from then on a join, a filter and a scan target are checked. Refactoring becomes cheap, so the shape gets fixed instead of patched.

**The honest claim** is therefore narrow. The shape fixes the report. The type system makes the shape cheap to reach, safe to change and impossible to erode with a shortcut. Building the full example here cost two compile errors and one duplicate-alias error from `Build`; the same three mistakes in a string builder are three failed requests found by running the report.

One caution for a column store. ClickHouse renders a LEFT JOIN miss as a default value, not `NULL`, unless `join_use_nulls` is set. The `COALESCE` in the report CTE is then harmless, but a ratio that should be `NULL` for a missing source becomes a number. Set the setting, or the honesty of the `NULL` ratios is lost.
