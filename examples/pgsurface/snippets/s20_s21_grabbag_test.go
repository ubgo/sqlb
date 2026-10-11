package snippets_test

import (
	"time"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/pg"
	"github.com/ubgo/sqlb/pg/crypto"
	"github.com/ubgo/sqlb/pg/earth"
	"github.com/ubgo/sqlb/pg/ltree"
	"github.com/ubgo/sqlb/pg/unaccent"
)

// ## 20. Grab bag

// ### 20.1 UUID v7 on any version, and extracting its timestamp
// The CREATE FUNCTION is DDL. The timestamp extraction is pg.UUIDv7Timestamp.
func Example_s20_01_uuidV7Timestamp() {
	s := sqlb.Derived(sel().Exprs(sqlb.As(sqlb.Fn[string]("uuid_generate_v7"), "u")), "s")
	show(sel().Exprs(sqlb.As(pg.UUIDv7Timestamp(ref[string](s, "u")), "created_at")).From(s).Build())
	// Output:
	// SELECT to_timestamp(('x' || substr(replace("s"."u"::text, '-', ''), 1, 12))::bit(48)::bigint / 1000.0) AS "created_at" FROM (SELECT uuid_generate_v7() AS "u") AS "s"
}

// ### 20.2 Password hashing and encryption with pgcrypto
// CREATE EXTENSION is DDL. The statements render through the pgcrypto helpers.
func Example_s20_02_pgcrypto() {
	u := tbl("users", "users")
	id, email, hash := sqlb.Col[int64](u, "id"), sqlb.Col[string](u, "email"), sqlb.Col[string](u, "pw_hash")
	show(sqlb.NewInsert(d).Into(u).Row(sqlb.Set(email, "a@x.com"), sqlb.SetExpr(hash, crypto.Crypt(sqlb.Param("secret"), crypto.GenSalt("bf", 12)))).Build())
	show(sel().Exprs(id).From(u).Where(sqlb.Eq(email, "a@x.com"), sqlb.EqExpr(hash, crypto.Crypt(sqlb.Param("secret"), hash))).Build())
	show(sel().Exprs(pg.Encode(crypto.HMAC(sqlb.Param("payload"), sqlb.Param("secret"), "sha256"), "hex"), pg.Encode(crypto.Digest(sqlb.Param("x"), "sha256"), "hex")).Build())
	vault := tbl("vault", "vault")
	show(sel().Exprs(crypto.PgpSymEncrypt(sqlb.Param("card number"), sqlb.Param("key")), crypto.PgpSymDecrypt(sqlb.Col[[]byte](vault, "blob"), sqlb.Param("key"))).From(vault).Build())
	// Output:
	// INSERT INTO "users" AS "users" ("email", "pw_hash") VALUES ($1, crypt($2, gen_salt($3, $4)))
	// args: 4
	// SELECT "users"."id" FROM "users" AS "users" WHERE ("users"."email" = $1 AND "users"."pw_hash" = crypt($2, "users"."pw_hash"))
	// args: 2
	// SELECT encode(hmac($1, $2, $3), $4), encode(digest($5, $6), $7)
	// args: 7
	// SELECT pgp_sym_encrypt($1, $2), pgp_sym_decrypt("vault"."blob", $3) FROM "vault" AS "vault"
	// args: 3
}

// ### 20.3 Base62 short ids
// The CREATE FUNCTION is DDL. Its body is a recursive CTE, which renders.
func Example_s20_03_base62() {
	dd := tbl("d", "d")
	rem, out := ref[int64](dd, "rem"), ref[string](dd, "out")
	seed := sel().Exprs(sqlb.As(sqlb.Param[int64](123456789), "rem"), sqlb.As(sqlb.Cast[string](sqlb.Param(""), "text"), "out"))
	step := sel().Exprs(sqlb.Div(rem, sqlb.Param[int64](62)),
		pg.ConcatOp(pg.SubstrExpr(sqlb.Param("0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"),
			sqlb.Add(sqlb.Cast[int32](sqlb.Mod(rem, sqlb.Param[int64](62)), "int"), sqlb.Param[int32](1)), sqlb.Param(1)), out)).
		From(dd).Where(sqlb.Greater(rem, 0))
	final := sqlb.Subquery[string](sel().Exprs(out).From(dd).Where(sqlb.Eq(rem, 0)))
	zero := "0"
	show(sel().WithRecursive("d", seed, step).Exprs(sqlb.Coalesce(sqlb.NullIf(sqlb.AssertNotNull(final), sqlb.Param("")), sqlb.Param(&zero))).Build())
	// Output:
	// WITH RECURSIVE "d" AS (SELECT $1 AS "rem", CAST($2 AS text) AS "out" UNION ALL SELECT ("d"."rem" / $3), (substr($4, (CAST(("d"."rem" % $5) AS int) + $6), $7) || "d"."out") FROM "d" AS "d" WHERE ("d"."rem" > $8))
	// SELECT COALESCE(NULLIF((SELECT "d"."out" FROM "d" AS "d" WHERE ("d"."rem" = $9)), $10), $11)
	// args: 11
}

// ### 20.4 Slugs
func Example_s20_04_slugs() {
	show(sel().Exprs(pg.TrimChars(pg.TrimBoth, "-", pg.RegexpReplace(sqlb.Lower(unaccent.Unaccent(sqlb.Param("Crème Brûlée & Co."))), "[^a-z0-9]+", "-", "g"))).Build())
	// Output:
	// SELECT trim(BOTH $1 FROM regexp_replace(lower(unaccent($2)), $3, $4, $5))
	// args: 5
}

// ### 20.5 Great-circle distance without PostGIS
func Example_s20_05_greatCircle() {
	p := tbl("places", "places")
	id, name, lat, lng := sqlb.Col[int64](p, "id"), sqlb.Col[string](p, "name"), sqlb.Col[float64](p, "lat"), sqlb.Col[float64](p, "lng")
	sq := func(e sqlb.Expr[float64]) sqlb.Expr[float64] { return pg.PowerOp(e, sqlb.Param(2)) }
	myLat, myLng := sqlb.Param(48.85), sqlb.Param(2.35)
	h := sqlb.Add(sq(pg.Sin(sqlb.Div(pg.Radians(sqlb.Sub(lat, myLat)), sqlb.Param(2.0)))),
		sqlb.Mul(sqlb.Mul(pg.Cos(pg.Radians(myLat)), pg.Cos(pg.Radians(lat))), sq(pg.Sin(sqlb.Div(pg.Radians(sqlb.Sub(lng, myLng)), sqlb.Param(2.0))))))
	km := sqlb.Mul(sqlb.Mul(sqlb.Param(6371.0), sqlb.Param(2.0)), pg.Asin(pg.Sqrt(h)))
	show(sel().Exprs(id, name, sqlb.As(km, "km")).From(p).Order(asc(sqlb.OutputCol[float64]("km"))).Limit(10).Build())
	// earthdistance: CREATE EXTENSION and CREATE INDEX are DDL; the indexed predicate renders.
	show(sel().Exprs(id).From(p).Where(earth.BoxContains(earth.Box(earth.LLToEarth(myLat, myLng), 5000), earth.LLToEarth(lat, lng))).Build())
	// Output:
	// SELECT "places"."id", "places"."name", (($1 * $2) * asin(sqrt(((sin((radians(("places"."lat" - $3)) / $4)) ^ $5) + ((cos(radians($6)) * cos(radians("places"."lat"))) * (sin((radians(("places"."lng" - $7)) / $8)) ^ $9)))))) AS "km" FROM "places" AS "places" ORDER BY "km" ASC LIMIT $10
	// args: 10
	// SELECT "places"."id" FROM "places" AS "places" WHERE ((earth_box(ll_to_earth($1, $2), $3) @> ll_to_earth("places"."lat", "places"."lng")))
	// args: 3
}

// ### 20.6 ltree hierarchies
// CREATE EXTENSION and CREATE INDEX are DDL. The four queries render.
func Example_s20_06_ltree() {
	c := tbl("categories", "categories")
	path := sqlb.Col[ltree.Path](c, "path")
	show(sel().Exprs(path).From(c).Where(ltree.DescendantOf(path, ltree.Lit("electronics.phones"))).Build())
	show(sel().Exprs(path).From(c).Where(ltree.AncestorOf(path, ltree.Lit("electronics.phones.android"))).Build())
	show(sel().Exprs(path).From(c).Where(ltree.MatchesLQuery(path, "electronics.*{1}")).Build())
	show(sel().Exprs(ltree.NLevel(path), ltree.Subpath(path, 0, 2), ltree.Index(path, "phones")).From(c).Build())
	// Output:
	// SELECT "categories"."path" FROM "categories" AS "categories" WHERE (("categories"."path" <@ $1::ltree))
	// args: 1
	// SELECT "categories"."path" FROM "categories" AS "categories" WHERE (("categories"."path" @> $1::ltree))
	// args: 1
	// SELECT "categories"."path" FROM "categories" AS "categories" WHERE (("categories"."path" ~ $1::lquery))
	// args: 1
	// SELECT nlevel("categories"."path"), subpath("categories"."path", $1, $2), index("categories"."path", $3::ltree) FROM "categories" AS "categories"
	// args: 3
}

// ### 20.7 Materialized views that refresh without blocking readers
// Not the query layer: CREATE MATERIALIZED VIEW, CREATE UNIQUE INDEX, REFRESH.

// ### 20.8 Updatable views with CHECK OPTION
// CREATE VIEW is DDL. The UPDATE through the view renders like any table.
func Example_s20_08_updatableView() {
	v := tbl("active_users", "active_users")
	show(sqlb.NewUpdate(d).Table(v).Set(sqlb.SetExpr(sqlb.Col[*time.Time](v, "deleted_at"), sqlb.Nullable(pg.Now()))).Where(sqlb.Eq(sqlb.Col[int64](v, "id"), 1)).Build())
	// Output:
	// UPDATE "active_users" AS "active_users" SET "deleted_at" = now() WHERE ("active_users"."id" = $1)
	// args: 1
}

// ### 20.9 Search every text column of a table
func Example_s20_09_searchEveryColumn() {
	u := tbl("users", "u")
	uRow := pg.RowRef(u)
	show(sel().Exprs(sqlb.Col[[]byte](u, "*")).From(u).
		Where(pg.ILike(sqlb.Cast[string](pg.ToJSONB(uRow), "text"), "%needle%")).Build())
	// Output:
	// SELECT "u"."*" FROM "users" AS "u" WHERE ((CAST(to_jsonb("u") AS text) ILIKE $1))
	// args: 1
}

// ### 20.10 Generate INSERT statements for a subset of rows
func Example_s20_10_generateInserts() {
	u := tbl("users", "u")
	uRow := pg.RowRef(u)
	show(sel().Exprs(pg.Format("INSERT INTO %I SELECT * FROM jsonb_populate_record(NULL::%I, %L);", sqlb.Param("users"), sqlb.Param("users"), pg.ToJSONB(uRow))).
		From(u).Where(sqlb.Less(sqlb.Col[int64](u, "id"), 10)).Build())
	// Output:
	// SELECT format($1, $2, $3, to_jsonb("u")) FROM "users" AS "u" WHERE ("u"."id" < $4)
	// args: 4
}

// ### 20.11 Validate input without exceptions (PG16)
func Example_s20_11_validateInput() {
	show(sel().Exprs(pg.InputIsValid("abc", "integer"), pg.InputIsValid("2024-02-30", "date"), pg.InputIsValid(`{"a":`, "jsonb")).Build())
	info := pg.FuncSource(sqlb.Fn[[]byte]("pg_input_error_info", sqlb.Param("abc"), sqlb.Param("integer")), "e")
	show(sel().Exprs(sqlb.Col[[]byte](sqlb.Ref(info), "*")).From(info).Build())
	// Output:
	// SELECT pg_input_is_valid($1, $2), pg_input_is_valid($3, $4), pg_input_is_valid($5, $6)
	// args: 6
	// SELECT "e"."*" FROM pg_input_error_info($1, $2) AS "e"
	// args: 2
}

// ### 20.12 MVCC system columns and transaction ids
func Example_s20_12_systemColumns() {
	t := tbl("t", "t")
	show(sel().Exprs(sqlb.Col[string](t, "ctid"), sqlb.Col[int64](t, "xmin"), sqlb.Col[int64](t, "xmax"), sqlb.Col[[]byte](t, "*")).From(t).Where(sqlb.Eq(sqlb.Col[int64](t, "id"), 1)).Build())
	show(sel().Exprs(pg.CurrentXactID(), pg.CurrentXactIDIfAssigned(), pg.TxidCurrent()).Build())
	show(sel().Exprs(pg.ExportSnapshot()).Build())
	// Output:
	// SELECT "t"."ctid", "t"."xmin", "t"."xmax", "t"."*" FROM "t" AS "t" WHERE ("t"."id" = $1)
	// args: 1
	// SELECT pg_current_xact_id(), pg_current_xact_id_if_assigned(), txid_current()
	// SELECT pg_export_snapshot()
}

// ### 20.13 Two-phase commit
// Not the query layer: PREPARE TRANSACTION and COMMIT PREPARED are
// transaction control.

// ### 20.14 Foreign tables and cross-database queries
// CREATE EXTENSION, CREATE SERVER, USER MAPPING and IMPORT FOREIGN SCHEMA are DDL.
// The query over the foreign table renders with a schema-qualified Table.
func Example_s20_14_foreignTable() {
	ev := sqlb.Table{Schema: "remote", Name: "events", Alias: "e"}
	created := sqlb.Col[time.Time](ev, "created_at")
	show(sel().Exprs(sqlb.Col[[]byte](ev, "*")).From(ev).Where(sqlb.GreaterExpr(created, pg.SubIntervalExpr(pg.Now(), pg.IntervalLit("1 day")))).Build())
	// Output:
	// SELECT "e"."*" FROM "remote"."events" AS "e" WHERE ("e"."created_at" > (now() - interval '1 day'))
}

// ### 20.15 Bit flags
func Example_s20_15_bitFlags() {
	u := tbl("users", "users")
	perms, id := sqlb.Col[int32](u, "perms"), sqlb.Col[int64](u, "id")
	show(sel().Exprs(id).From(u).Where(sqlb.NotEq(pg.BitAnd(perms, sqlb.Param[int32](4)), 0)).Build())
	show(sqlb.NewUpdate(d).Table(u).Set(sqlb.SetExpr(perms, pg.BitOr(perms, sqlb.Param[int32](8)))).Where(sqlb.Eq(id, 1)).Build())
	show(sqlb.NewUpdate(d).Table(u).Set(sqlb.SetExpr(perms, pg.BitAnd(perms, pg.BitNot(sqlb.Param[int32](8))))).Where(sqlb.Eq(id, 1)).Build())
	show(sel().Exprs(pg.BitStringOr(pg.BitLit("1011"), pg.BitLit("0100")), pg.GetBit(pg.BitLit("1011"), 0), pg.BitCount(pg.BitLit("1011"))).Build())
	// Output:
	// SELECT "users"."id" FROM "users" AS "users" WHERE (("users"."perms" & $1) <> $2)
	// args: 2
	// UPDATE "users" AS "users" SET "perms" = ("users"."perms" | $1) WHERE ("users"."id" = $2)
	// args: 2
	// UPDATE "users" AS "users" SET "perms" = ("users"."perms" & (~$1)) WHERE ("users"."id" = $2)
	// args: 2
	// SELECT (B'1011' | B'0100'), get_bit(B'1011', $1), bit_count(B'1011')
	// args: 1
}

// ### 20.16 Logical decoding peek (change data capture without a client)
func Example_s20_16_logicalDecoding() {
	show(sel().Exprs(sqlb.Fn[[]byte]("pg_create_logical_replication_slot", sqlb.Param("debug_slot"), sqlb.Param("test_decoding"))).Build())
	changes := pg.FuncSource(sqlb.Fn[[]byte]("pg_logical_slot_get_changes", sqlb.Param("debug_slot"), sqlb.Literal[*string]("NULL"), sqlb.Literal[*string]("NULL")), "c")
	show(sel().Exprs(sqlb.Col[[]byte](sqlb.Ref(changes), "*")).From(changes).Build())
	show(sel().Exprs(sqlb.Fn[[]byte]("pg_drop_replication_slot", sqlb.Param("debug_slot"))).Build())
	// Output:
	// SELECT pg_create_logical_replication_slot($1, $2)
	// args: 2
	// SELECT "c"."*" FROM pg_logical_slot_get_changes($1, NULL, NULL) AS "c"
	// args: 1
	// SELECT pg_drop_replication_slot($1)
	// args: 1
}

// ## 21. Anti-pattern cheat sheet
// A table of rewrites, no SQL to render. Each left-hand form that is a query
// has its right-hand form above: 11.1 for NOT IN, 5.1 for check-then-act,
// 16.1 for OFFSET, 16.7 for count versus EXISTS, 16.11 for random(), 10.6
// for now() per row, 10.11 for the date index, 12.1 for lock strength, and
// 5.5 for MERGE versus ON CONFLICT.
