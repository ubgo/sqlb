// Package advancedsql translates an external catalogue of advanced
// PostgreSQL queries into sqlb and checks the rendered SQL.
//
// The catalogue covered window functions in two parts, CTEs, a set of
// practice exercises including islands-and-gaps, miscellaneous notes, and a
// group of interview-style solutions. Its six sections are the six test
// files, and the section numbers in the test names are the catalogue's own,
// so the examples can be read in the order they were written.
//
// The point of the package is a question the grammar catalogues cannot
// answer: can this builder write a real analyst's query rather than one row
// of a syntax table. Every query here needed only constructs that already
// existed, plus AssertNotNull for a window result compared with a non-null
// column.
//
// Translation rules, stated once:
//
//   - "select *" and "select e.*" bind the table's known columns. sqlb never
//     emits a star, so a new column cannot silently change an old scan.
//   - A window result that the source compared with a non-null column goes
//     through sqlb.AssertNotNull, with the run-time reason beside it.
//   - Two of the source statements are not valid SQL as written: a doubled
//     OVER in 1.2, and a SELECT with no FROM in section 3. The tests render
//     the statement the surrounding prose described and say so in a comment.
//
// The tests prove rendering and compile-time checks. None of this SQL has run
// against a Postgres server from here.
package advancedsql
