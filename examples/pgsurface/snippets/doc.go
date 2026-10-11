// Package snippets translates a catalogue of advanced PostgreSQL snippets
// into sqlb, section by section and in the catalogue's own order and
// numbering.
//
// The test files are its sections: s01 to s21, one file per section or group
// of sections, 21 sections and 193 SQL blocks in all. Every "### x.y" heading
// appears in the same order as a comment, so the files can be read straight
// through as one document. A heading whose SQL is a query is an Example whose Output block
// go test checks. A heading whose SQL is DDL, a procedure, a psql command or
// an administration statement keeps its place as a comment that says which
// kind it is, because those are not the query layer and sqlb does not
// render them.
//
// Conventions, stated once:
//
//   - Tables are declared inline in each example, as the snippets do.
//   - Most examples build the select list with Exprs, which carries no scan
//     targets; a few use Bind to show the typed scan. Rendering is the same.
//   - A construct with no pg helper goes through a typed door, sqlb.Fn,
//     sqlb.New, sqlb.Raw or sqlb.SourceOf, and the comment says so.
//   - Literal constants that the snippet writes inline, such as an interval
//     or a jsonpath, are author constants here too; request values are bound.
//
// None of this SQL has run against a Postgres server from here.
package snippets
