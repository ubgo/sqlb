package snippets_test

import (
	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/pg"
	"github.com/ubgo/sqlb/pg/fuzzy"
	"github.com/ubgo/sqlb/pg/trgm"
)

// ## 9. Full-text and fuzzy search

// ### 9.1 Weighted generated tsvector with GIN, ranking and highlighting
// ALTER TABLE ... GENERATED and CREATE INDEX are DDL. The search renders.
func Example_s9_01_weightedSearch() {
	a := tbl("articles", "articles")
	aID, title, body, search := sqlb.Col[int64](a, "id"), sqlb.Col[string](a, "title"), sqlb.Col[string](a, "body"), sqlb.Col[pg.TSVector](a, "search")
	qSrc := pg.FuncSource(pg.WebsearchToTSQuery("english", `postgres -mysql "window function"`), "q")
	q := ref[pg.TSQuery](qSrc, "q")
	show(sel().Exprs(aID, title,
		sqlb.As(pg.TSRankCDNormalized(search, q, 32), "rank"),
		sqlb.As(pg.TSHeadline("english", body, q, "MaxFragments=2, MinWords=5, MaxWords=15, StartSel=<b>, StopSel=</b>"), "snippet")).
		From(a).Join(cross(qSrc)).Where(pg.Matches(search, q)).Order(desc(sqlb.OutputCol[float32]("rank"))).Limit(10).Build())
	// Output:
	// SELECT "articles"."id", "articles"."title", ts_rank_cd("articles"."search", "q"."q", $1) AS "rank", ts_headline('english', "articles"."body", "q"."q", $2) AS "snippet" FROM "articles" AS "articles" CROSS JOIN websearch_to_tsquery('english', $3) AS "q" WHERE (("articles"."search" @@ "q"."q")) ORDER BY "rank" DESC LIMIT $4
	// args: 4
}

// ### 9.2 Prefix, phrase, and distance queries
func Example_s9_02_prefixPhraseDistance() {
	fox := pg.ToTSVector("english", sqlb.Param("the quick brown fox"))
	show(sel().Exprs(pg.ToTSQuery("english", "postg:*")).Build())
	show(sel().Exprs(sqlb.BoolExpr(pg.Matches(fox, pg.PhraseToTSQuery("english", "quick fox")))).Build())
	show(sel().Exprs(sqlb.BoolExpr(pg.Matches(fox, pg.ToTSQuery("english", "quick <2> fox")))).Build())
	// Output:
	// SELECT to_tsquery('english', $1)
	// args: 1
	// SELECT (to_tsvector('english', $1) @@ phraseto_tsquery('english', $2))
	// args: 2
	// SELECT (to_tsvector('english', $1) @@ to_tsquery('english', $2))
	// args: 2
}

// ### 9.3 Accent-insensitive search config
// CREATE EXTENSION and CREATE TEXT SEARCH CONFIGURATION are DDL. The test query renders.
func Example_s9_03_accentInsensitive() {
	show(sel().Exprs(sqlb.BoolExpr(pg.Matches(pg.ToTSVector("english_unaccent", sqlb.Param("Crème brûlée")), pg.ToTSQuery("english_unaccent", "creme")))).Build())
	// Output:
	// SELECT (to_tsvector('english_unaccent', $1) @@ to_tsquery('english_unaccent', $2))
	// args: 2
}

// ### 9.4 Trigram fuzzy match and substring LIKE that uses an index
// CREATE EXTENSION and the two indexes are DDL. The searches render.
func Example_s9_04_trigram() {
	u := tbl("users", "users")
	name, email := sqlb.Col[string](u, "name"), sqlb.Col[string](u, "email")
	needle := sqlb.Param("jon smith")
	show(sel().Exprs(name, sqlb.As(trgm.Similarity(name, needle), "sim")).From(u).
		Where(trgm.Similar(name, needle)).Order(asc(trgm.Distance(name, needle))).Limit(10).Build())
	show(sel().Exprs(email).From(u).Where(pg.ILike(email, "%@example.%")).Build())
	show(sel().Exprs(trgm.WordSimilarity(sqlb.Param("smith"), sqlb.Param("john smithers"))).Build())
	// Output:
	// SELECT "users"."name", similarity("users"."name", $1) AS "sim" FROM "users" AS "users" WHERE (("users"."name" % $2)) ORDER BY ("users"."name" <-> $3) ASC LIMIT $4
	// args: 4
	// SELECT "users"."email" FROM "users" AS "users" WHERE (("users"."email" ILIKE $1))
	// args: 1
	// SELECT word_similarity($1, $2)
	// args: 2
}

// ### 9.5 Phonetic and edit-distance matching
func Example_s9_05_phonetic() {
	u := tbl("users", "users")
	name := sqlb.Col[string](u, "name")
	show(sel().Exprs(name).From(u).Where(sqlb.EqExpr(fuzzy.Dmetaphone(name), fuzzy.Dmetaphone(sqlb.Param("Katherine")))).Build())
	show(sel().Exprs(name).From(u).Where(sqlb.LessEq(fuzzy.LevenshteinLessEqual(sqlb.Lower(name), sqlb.Param("jonathon"), 2), 2)).Build())
	// Output:
	// SELECT "users"."name" FROM "users" AS "users" WHERE (dmetaphone("users"."name") = dmetaphone($1))
	// args: 1
	// SELECT "users"."name" FROM "users" AS "users" WHERE (levenshtein_less_equal(lower("users"."name"), $1, $2) <= $3)
	// args: 3
}

// ### 9.6 Probable-duplicate customers
func Example_s9_06_probableDuplicates() {
	a, b := tbl("customers", "a"), tbl("customers", "b")
	aID, aName, aEmail := sqlb.Col[int64](a, "id"), sqlb.Col[string](a, "name"), sqlb.Col[string](a, "email")
	bID, bName, bEmail := sqlb.Col[int64](b, "id"), sqlb.Col[string](b, "name"), sqlb.Col[string](b, "email")
	show(sel().Exprs(aID, bID, aName, bName, sqlb.As(trgm.Similarity(aName, bName), "sim")).From(a).
		Join(inner(b, sqlb.LessExpr(aID, bID), trgm.Similar(aName, bName))).
		Where(pg.IsDistinctFrom(aEmail, bEmail)).Order(desc(sqlb.OutputCol[float32]("sim"))).Build())
	// Output:
	// SELECT "a"."id", "b"."id", "a"."name", "b"."name", similarity("a"."name", "b"."name") AS "sim" FROM "customers" AS "a" INNER JOIN "customers" AS "b" ON ("a"."id" < "b"."id" AND ("a"."name" % "b"."name")) WHERE (("a"."email" IS DISTINCT FROM "b"."email")) ORDER BY "sim" DESC
}
