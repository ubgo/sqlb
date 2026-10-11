package pg_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/pg"
	"github.com/ubgo/sqlb/pg/trgm"
	"github.com/ubgo/sqlb/pg/vector"
)

type oStatus string

const (
	oActive    oStatus = "active"
	oBanned    oStatus = "banned"
	oSuspended oStatus = "suspended"
	oPaused    oStatus = "paused"
)

var (
	oAuth       = pg.Schema{Name: "auth"}
	oStatusType = pg.Enum[oStatus]{Schema: "auth", Name: "user_status"}
	oStatCol    = sqlb.Col[oStatus](tUsers, "status")
	oProfile    = sqlb.Col[json.RawMessage](tUsers, "profile")
	oEmbed      = sqlb.Col[vector.Vector](tUsers, "embedding")
)

func buildAll(t *testing.T, bs ...interface{ Build() (ddl.Script, error) }) string {
	t.Helper()
	var out []string
	for _, b := range bs {
		s, err := b.Build()
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, s.SQL()...)
	}
	return strings.Join(out, "\n")
}

// TestObjects_SchemasExtensionsAndEnums renders section 6.1 and 6.2.
func TestObjects_SchemasExtensionsAndEnums(t *testing.T) {
	got := buildAll(t,
		pg.CreateSchema(oAuth).IfNotExists(),
		pg.CreateExtension(vector.Extension).IfNotExists(),
		pg.CreateExtension(trgm.Extension).IfNotExists(),
		pg.CreateEnum(oStatusType, oActive, oBanned),
		pg.Guarded(pg.CreateEnum(oStatusType, oActive, oBanned)),
		pg.AlterEnum(oStatusType).AddValue(oSuspended).Before(oBanned).IfNotExists(),
		pg.AlterEnum(oStatusType).RenameValue(oSuspended, oPaused),
		pg.AlterEnum(oStatusType).RenameTo(pg.Enum[oStatus]{Schema: "auth", Name: "account_status"}),
		pg.DropType(oStatusType).IfExists().Cascade(),
		pg.DropExtension(trgm.Extension).IfExists(),
		pg.DropSchema(oAuth).IfExists().Cascade(),
		pg.RenameSchema(oAuth, pg.Schema{Name: "identity"}),
	)
	want := `CREATE SCHEMA IF NOT EXISTS "auth"
CREATE EXTENSION IF NOT EXISTS "vector"
CREATE EXTENSION IF NOT EXISTS "pg_trgm"
CREATE TYPE "auth"."user_status" AS ENUM ('active', 'banned')
DO $sqlb$ BEGIN CREATE TYPE "auth"."user_status" AS ENUM ('active', 'banned'); EXCEPTION WHEN duplicate_object THEN NULL; END $sqlb$
ALTER TYPE "auth"."user_status" ADD VALUE IF NOT EXISTS 'suspended' BEFORE 'banned'
ALTER TYPE "auth"."user_status" RENAME VALUE 'suspended' TO 'paused'
ALTER TYPE "auth"."user_status" RENAME TO "account_status"
DROP TYPE IF EXISTS "auth"."user_status" CASCADE
DROP EXTENSION IF EXISTS "pg_trgm"
DROP SCHEMA IF EXISTS "auth" CASCADE
ALTER SCHEMA "auth" RENAME TO "identity"`
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

// TestObjects_EnumIsAColumnType pins that an Enum is the column's SQL type.
func TestObjects_EnumIsAColumnType(t *testing.T) {
	got := buildAll(t, pg.CreateTable(tUsers).Column(ddl.Column(oStatCol).As(oStatusType).Default(oActive)))
	if want := `CREATE TABLE "auth"."users" ("status" "auth"."user_status" NOT NULL DEFAULT 'active')`; got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

// TestGuarded_TagTheBodyCannotEnd pins that the dollar tag changes when the
// body contains it, so a label cannot close the quote early.
func TestGuarded_TagTheBodyCannotEnd(t *testing.T) {
	got := buildAll(t, pg.Guarded(pg.CreateEnum(pg.Enum[oStatus]{Name: "x"}, oStatus("$sqlb$"))))
	if !strings.HasPrefix(got, "DO $sqlb1$ BEGIN") || !strings.HasSuffix(got, "END $sqlb1$") {
		t.Fatalf("the tag was not changed: %s", got)
	}
}

// TestIndexes_TheLibraryForms renders section 6.8.
func TestIndexes_TheLibraryForms(t *testing.T) {
	ix := func(n string) pg.Index { return pg.Index{Schema: "auth", Name: n} }
	got := buildAll(t,
		pg.CreateIndex(ix("sessions_user_id_idx"), tSessions).IfNotExists().On(pg.IndexKey(tSessUser)),
		pg.CreateIndex(ix("users_nick_idx"), tUsers).On(pg.IndexKey(tNick)).Where(sqlb.IsNull(tNick)),
		pg.CreateIndex(ix("users_email_ci_idx"), tUsers).Unique().IfNotExists().On(pg.IndexExpr(sqlb.Lower(tEmail))).Include(oStatCol),
		pg.CreateIndex(ix("users_tenant_id_idx"), tUsers).On(pg.IndexKey(tTenantID), pg.IndexKey(tUserID).Desc().NullsLast()),
		pg.CreateIndex(ix("users_profile_gin"), tUsers).Using(pg.GIN).On(pg.IndexKey(oProfile).Opclass(pg.JSONBPathOps)),
		pg.CreateIndex(ix("users_nick_trgm"), tUsers).Using(pg.GIN).On(pg.IndexKey(tNick).Opclass(pg.NullableOps(trgm.GinOps))),
		pg.CreateIndex(ix("users_embedding_hnsw"), tUsers).Using(vector.HNSW).On(pg.IndexKey(oEmbed).Opclass(vector.CosineOps)).With(vector.M(16), vector.EfConstruction(64)),
		pg.CreateIndex(ix("users_email_key"), tUsers).Unique().On(pg.IndexKey(tEmail)).NullsNotDistinct(),
		pg.CreateIndex(ix("users_email_idx"), tUsers).Concurrently().IfNotExists().On(pg.IndexKey(tEmail)),
		pg.RenameIndex(ix("sessions_user_id_idx"), ix("sessions_by_user")).IfExists(),
		pg.DropIndex(ix("a"), ix("b")).IfExists().Concurrently(),
	)
	want := `CREATE INDEX IF NOT EXISTS "sessions_user_id_idx" ON "auth"."sessions" ("user_id")
CREATE INDEX "users_nick_idx" ON "auth"."users" ("nick") WHERE "nick" IS NULL
CREATE UNIQUE INDEX IF NOT EXISTS "users_email_ci_idx" ON "auth"."users" ((lower("email"))) INCLUDE ("status")
CREATE INDEX "users_tenant_id_idx" ON "auth"."users" ("tenant_id", "id" DESC NULLS LAST)
CREATE INDEX "users_profile_gin" ON "auth"."users" USING "gin" ("profile" "jsonb_path_ops")
CREATE INDEX "users_nick_trgm" ON "auth"."users" USING "gin" ("nick" "gin_trgm_ops")
CREATE INDEX "users_embedding_hnsw" ON "auth"."users" USING "hnsw" ("embedding" "vector_cosine_ops") WITH ("m" = 16, "ef_construction" = 64)
CREATE UNIQUE INDEX "users_email_key" ON "auth"."users" ("email") NULLS NOT DISTINCT
CREATE INDEX CONCURRENTLY IF NOT EXISTS "users_email_idx" ON "auth"."users" ("email")
ALTER INDEX IF EXISTS "auth"."sessions_user_id_idx" RENAME TO "sessions_by_user"
DROP INDEX CONCURRENTLY IF EXISTS "auth"."a"
DROP INDEX CONCURRENTLY IF EXISTS "auth"."b"`
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

// TestIndexes_ConcurrentlyIsNotTransactional pins the flag a runner reads.
func TestIndexes_ConcurrentlyIsNotTransactional(t *testing.T) {
	for name, b := range map[string]interface{ Build() (ddl.Script, error) }{
		"create": pg.CreateIndex(pg.Index{Name: "i"}, tUsers).Concurrently().On(pg.IndexKey(tEmail)),
		"drop":   pg.DropIndex(pg.Index{Name: "i"}).Concurrently(),
	} {
		s, err := b.Build()
		if err != nil || s.Transactional() {
			t.Errorf("%s: transactional %v, %v", name, s.Transactional(), err)
		}
	}
	s, _ := pg.CreateIndex(pg.Index{Name: "i"}, tUsers).On(pg.IndexKey(tEmail)).Build()
	if !s.Transactional() {
		t.Error("a plain CREATE INDEX is transactional")
	}
}

// TestComments_EveryTarget renders COMMENT ON for each kind, with a hostile
// string written as a literal.
func TestComments_EveryTarget(t *testing.T) {
	got := buildAll(t,
		pg.CommentOnTable(tUsers).Is("It's the owner's table; DROP TABLE x; --"),
		pg.CommentOnColumn(tEmailLow).Is(`a\b`),
		pg.CommentOnColumn(tEmailLow).IsNull(),
		pg.CommentOn(pg.Index{Schema: "auth", Name: "i"}).Is("x"),
		pg.CommentOn(oAuth).Is("x"),
		pg.CommentOn(vector.Extension).Is("x"),
		pg.CommentOn(oStatusType).Is("x"),
	)
	want := `COMMENT ON TABLE "auth"."users" IS 'It''s the owner''s table; DROP TABLE x; --'
COMMENT ON COLUMN "auth"."users"."email_lower" IS E'a\\b'
COMMENT ON COLUMN "auth"."users"."email_lower" IS NULL
COMMENT ON INDEX "auth"."i" IS 'x'
COMMENT ON SCHEMA "auth" IS 'x'
COMMENT ON EXTENSION "vector" IS 'x'
COMMENT ON TYPE "auth"."user_status" IS 'x'`
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

// TestObjects_Refusals pins what Build refuses.
func TestObjects_Refusals(t *testing.T) {
	for name, tc := range map[string]struct {
		b    interface{ Build() (ddl.Script, error) }
		want error
	}{
		"a schema with no name":         {pg.CreateSchema(pg.Schema{}), pg.ErrEmptyName},
		"an index with no name":         {pg.CreateIndex(pg.Index{}, tUsers).On(pg.IndexKey(tEmail)), pg.ErrEmptyName},
		"an index on another table":     {pg.CreateIndex(pg.Index{Name: "i"}, tUsers).On(pg.IndexKey(tSessID)), ddl.ErrForeignColumn},
		"an include from another table": {pg.CreateIndex(pg.Index{Name: "i"}, tUsers).On(pg.IndexKey(tEmail)).Include(tSessID), ddl.ErrForeignColumn},
		"a comment on a non-column":     {pg.CommentOnColumn(sqlb.Literal[int64]("1")).Is("x"), ddl.ErrNotAColumn},
		"a guarded unnamed constraint":  {pg.Guarded(pg.AddConstraintStatement(pg.References(tSessUser, tUserID))), pg.ErrUnnamedConstraint},
		"a rename to an empty name":     {pg.RenameIndex(pg.Index{Name: "a"}, pg.Index{}), pg.ErrEmptyName},
	} {
		t.Run(name, func(t *testing.T) {
			if s, err := tc.b.Build(); !errors.Is(err, tc.want) || s != nil {
				t.Fatalf("got %v %v, want %v", s, err, tc.want)
			}
		})
	}
}

// TestGuarded_CatchesTheConditionEachKindRaises pins the measured conditions:
// a key constraint's second run raises duplicate_table, its index being a
// relation; a check or foreign key raises duplicate_object.
func TestGuarded_CatchesTheConditionEachKindRaises(t *testing.T) {
	for want, c := range map[string]pg.Constraint{
		"WHEN duplicate_table ":  pg.Unique(pg.TableConstraint{Table: tSessUser.Table(), Name: "k"}, tSessUser),
		"WHEN duplicate_table":   pg.PrimaryKey(tSessID).Named(pg.TableConstraint{Table: tSessID.Table(), Name: "pk"}),
		"WHEN duplicate_object ": pg.Check(pg.TableConstraint{Table: tSessions, Name: "c"}, sqlb.Greater(tSessID, 0)),
		"WHEN duplicate_object":  pg.ForeignKey(pg.TableConstraint{Table: tSessUser.Table(), Name: "f"}, pg.RefPair(tSessUser, tUserID)),
	} {
		if got := buildAll(t, pg.Guarded(pg.AddConstraintStatement(c))); !strings.Contains(got, strings.TrimSpace(want)+" THEN NULL") {
			t.Errorf("%T: %s", c, got)
		}
	}
}

// TestExclude_AndLike renders an exclusion constraint, in CREATE TABLE and
// added later, and CREATE TABLE ... LIKE with each option.
func TestExclude_AndLike(t *testing.T) {
	rooms := sqlb.Table{Name: "bookings", Alias: "b"}
	room := sqlb.Col[int64](rooms, "room")
	during := sqlb.Col[pg.Range[time.Time]](rooms, "during")
	cancelled := sqlb.Col[bool](rooms, "cancelled")
	ex := pg.Exclude(pg.TableConstraint{Table: room.Table(), Name: "bookings_no_overlap"}, pg.GiST, pg.ExcludeOn(room, pg.OpEquals), pg.ExcludeOn(during, pg.OpOverlaps)).
		Where(sqlb.Eq(cancelled, false))
	got := buildAll(t,
		pg.CreateTable(rooms).Column(ddl.Column(room), ddl.Column(during).As(ddl.RawType[pg.Range[time.Time]]("tstzrange")), ddl.Column(cancelled)).Clause(ex),
		pg.AlterTable(rooms).Clause(pg.AddConstraint(ex)),
		pg.Guarded(pg.AddConstraintStatement(ex)),
		pg.CreateTableLike(sqlb.Table{Name: "bookings_archive"}, rooms, pg.IncludingDefaults, pg.IncludingConstraints, pg.IncludingGenerated, pg.IncludingIdentity,
			pg.IncludingIndexes, pg.IncludingComments, pg.IncludingStatistics, pg.IncludingStorage, pg.IncludingCompression).IfNotExists(),
	)
	want := `CREATE TABLE "bookings" ("room" BIGINT NOT NULL, "during" tstzrange NOT NULL, "cancelled" BOOLEAN NOT NULL, CONSTRAINT "bookings_no_overlap" EXCLUDE USING "gist" ("room" WITH =, "during" WITH &&) WHERE ("cancelled" = false))
ALTER TABLE "bookings" ADD CONSTRAINT "bookings_no_overlap" EXCLUDE USING "gist" ("room" WITH =, "during" WITH &&) WHERE ("cancelled" = false)
DO $sqlb$ BEGIN ALTER TABLE "bookings" ADD CONSTRAINT "bookings_no_overlap" EXCLUDE USING "gist" ("room" WITH =, "during" WITH &&) WHERE ("cancelled" = false); EXCEPTION WHEN duplicate_table THEN NULL; END $sqlb$
CREATE TABLE IF NOT EXISTS "bookings_archive" (LIKE "bookings" INCLUDING DEFAULTS INCLUDING CONSTRAINTS INCLUDING GENERATED INCLUDING IDENTITY INCLUDING INDEXES INCLUDING COMMENTS INCLUDING STATISTICS INCLUDING STORAGE INCLUDING COMPRESSION)`
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	if _, err := pg.CreateTable(rooms).Column(ddl.Column(room)).Clause(ex).Build(); !errors.Is(err, ddl.ErrUnknownColumn) {
		t.Errorf("an exclusion on an undefined column: %v", err)
	}
	if _, err := pg.CreateTableLike(rooms, sqlb.Table{Name: "x"}, pg.LikeOption(0)).Build(); !errors.Is(err, pg.ErrUnknownKeyword) {
		t.Errorf("a LIKE option outside the constants: %v", err)
	}
}

type invoiceNo int32

// TestSequences_AndViews renders sections 6.4 and 6.9.
func TestSequences_AndViews(t *testing.T) {
	seq := pg.Sequence[int64]{Schema: "auth", Name: "invoice_number"}
	small := pg.Sequence[invoiceNo]{Name: "n"}
	number := sqlb.Col[int64](tUsers, "number")
	v := pg.View{Schema: "auth", Name: "active_users"}
	mv := pg.MaterializedView{Schema: "auth", Name: "user_counts"}
	body := pg.Select(tUserID, tEmail).From(tUsers).Where(sqlb.Eq(tEmail, `a\b`))
	got := buildAll(t,
		pg.CreateSequence(seq).IfNotExists().Start(1000).IncrementBy(1).MinValue(1).MaxValue(1<<40).Cache(20),
		pg.CreateSequence(small),
		pg.AlterSequence(seq).Restart(5000).IfExists(),
		pg.AlterSequence(seq).OwnedBy(number),
		pg.AlterSequence(seq).OwnedByNone(),
		pg.DropSequence(seq).IfExists().Cascade(),
		pg.CreateTable(tUsers).Column(ddl.Column(number).DefaultExpr(pg.NextVal(seq))),
		pg.CreateView(v).OrReplace().As(body).WithCheckOption(pg.Local),
		pg.CreateView(v).As(body).WithCheckOption(pg.Cascaded),
		pg.RenameView(v, pg.View{Name: "live_users"}).IfExists(),
		pg.DropView(v).IfExists(),
		pg.CreateMaterializedView(mv).IfNotExists().As(pg.Select(tTenantID).From(tUsers)).WithNoData(),
		pg.RefreshMaterializedView(mv).Concurrently(),
		pg.DropMaterializedView(mv).IfExists(),
		pg.CommentOn(seq).Is("s"), pg.CommentOn(v).Is("v"), pg.CommentOn(mv).Is("m"),
	)
	want := `CREATE SEQUENCE IF NOT EXISTS "auth"."invoice_number" AS bigint INCREMENT BY 1 MINVALUE 1 MAXVALUE 1099511627776 START WITH 1000 CACHE 20
CREATE SEQUENCE "n" AS integer
ALTER SEQUENCE IF EXISTS "auth"."invoice_number" RESTART WITH 5000
ALTER SEQUENCE "auth"."invoice_number" OWNED BY "auth"."users"."number"
ALTER SEQUENCE "auth"."invoice_number" OWNED BY NONE
DROP SEQUENCE IF EXISTS "auth"."invoice_number" CASCADE
CREATE TABLE "auth"."users" ("number" BIGINT NOT NULL DEFAULT (nextval('"auth"."invoice_number"')))
CREATE OR REPLACE VIEW "auth"."active_users" AS SELECT "u"."id", "u"."email" FROM "auth"."users" AS "u" WHERE ("u"."email" = E'a\\b') WITH LOCAL CHECK OPTION
CREATE VIEW "auth"."active_users" AS SELECT "u"."id", "u"."email" FROM "auth"."users" AS "u" WHERE ("u"."email" = E'a\\b') WITH CASCADED CHECK OPTION
ALTER VIEW IF EXISTS "auth"."active_users" RENAME TO "live_users"
DROP VIEW IF EXISTS "auth"."active_users"
CREATE MATERIALIZED VIEW IF NOT EXISTS "auth"."user_counts" AS SELECT "u"."tenant_id" FROM "auth"."users" AS "u" WITH NO DATA
REFRESH MATERIALIZED VIEW CONCURRENTLY "auth"."user_counts"
DROP MATERIALIZED VIEW IF EXISTS "auth"."user_counts"
COMMENT ON SEQUENCE "auth"."invoice_number" IS 's'
COMMENT ON VIEW "auth"."active_users" IS 'v'
COMMENT ON MATERIALIZED VIEW "auth"."user_counts" IS 'm'`
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

// TestTriggers_AndPartitions renders sections 6.10 and the partition forms.
func TestTriggers_AndPartitions(t *testing.T) {
	fn := pg.Function{Schema: "auth", Name: "touch_updated_at"}
	tr := pg.Trigger{Name: "users_touch", Table: tUsers}
	events := sqlb.Table{Schema: "auth", Name: "events", Alias: "e"}
	at := sqlb.Col[time.Time](events, "at")
	kind := sqlb.Col[string](events, "kind")
	id := sqlb.Col[int64](events, "id")
	sep := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	oct := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	got := buildAll(t,
		pg.CreateTriggerFunction(fn, "\nBEGIN NEW.updated_at := now(); RETURN NEW; END\n").OrReplace(),
		pg.CreateTrigger(tr).OrReplace().Before(pg.OnUpdate()).ForEachRow().Execute(fn),
		pg.CreateTrigger(tr).After(pg.OnInsert(), pg.OnUpdateOf(tEmail, tNick), pg.OnDelete()).ForEachRow().
			When(pg.IsDistinctFrom(pg.Old(tEmail), pg.New(tEmail))).Execute(fn),
		pg.CreateTrigger(tr).After(pg.OnDelete()).Execute(fn),
		pg.RenameTrigger(tr, pg.Trigger{Name: "users_touched", Table: tUsers}),
		pg.DropTrigger(tr).IfExists(),
		pg.DropFunction(fn).IfExists().Cascade(),
		pg.CommentOn(fn).Is("f"), pg.CommentOn(tr).Is("t"),
		pg.CreateTable(events).Column(ddl.Column(id), ddl.Column(at), ddl.Column(kind)).Clause(pg.PrimaryKey(id, at), pg.PartitionByRange(at)),
		pg.CreatePartition(sqlb.Table{Schema: "auth", Name: "events_2026_09"}, events).IfNotExists().For(pg.FromTo(at, sep, oct)),
		pg.CreatePartition(sqlb.Table{Schema: "auth", Name: "events_default"}, events).For(pg.DefaultPartition()),
		pg.CreateTable(events).Column(ddl.Column(kind)).Clause(pg.PartitionByList(kind)),
		pg.CreatePartition(sqlb.Table{Name: "events_auth"}, events).For(pg.ListValues(kind, "login", "logout")),
		pg.CreateTable(events).Column(ddl.Column(id)).Clause(pg.PartitionByHash(id)),
		pg.CreatePartition(sqlb.Table{Name: "events_h0"}, events).For(pg.HashOf(4, 0)),
		pg.AlterTable(events).Clause(pg.AttachPartition(sqlb.Table{Name: "events_old"}, pg.FromTo(at, sep.AddDate(0, -1, 0), sep))),
		pg.DetachPartition(events, sqlb.Table{Name: "events_old"}).Concurrently(),
		pg.DetachPartition(events, sqlb.Table{Name: "events_old"}).Finalize(),
	)
	want := `CREATE OR REPLACE FUNCTION "auth"."touch_updated_at"() RETURNS trigger LANGUAGE plpgsql AS $sqlb$
BEGIN NEW.updated_at := now(); RETURN NEW; END
$sqlb$
CREATE OR REPLACE TRIGGER "users_touch" BEFORE UPDATE ON "auth"."users" FOR EACH ROW EXECUTE FUNCTION "auth"."touch_updated_at"()
CREATE TRIGGER "users_touch" AFTER INSERT OR UPDATE OF "email", "nick" OR DELETE ON "auth"."users" FOR EACH ROW WHEN ((OLD."email" IS DISTINCT FROM NEW."email")) EXECUTE FUNCTION "auth"."touch_updated_at"()
CREATE TRIGGER "users_touch" AFTER DELETE ON "auth"."users" FOR EACH STATEMENT EXECUTE FUNCTION "auth"."touch_updated_at"()
ALTER TRIGGER "users_touch" ON "auth"."users" RENAME TO "users_touched"
DROP TRIGGER IF EXISTS "users_touch" ON "auth"."users"
DROP FUNCTION IF EXISTS "auth"."touch_updated_at"() CASCADE
COMMENT ON FUNCTION "auth"."touch_updated_at"() IS 'f'
COMMENT ON TRIGGER "users_touch" ON "auth"."users" IS 't'
CREATE TABLE "auth"."events" ("id" BIGINT NOT NULL, "at" TIMESTAMPTZ NOT NULL, "kind" TEXT NOT NULL, PRIMARY KEY ("id", "at")) PARTITION BY RANGE ("at")
CREATE TABLE IF NOT EXISTS "auth"."events_2026_09" PARTITION OF "auth"."events" FOR VALUES FROM ('2026-09-01 00:00:00Z'::timestamptz) TO ('2026-10-01 00:00:00Z'::timestamptz)
CREATE TABLE "auth"."events_default" PARTITION OF "auth"."events" DEFAULT
CREATE TABLE "auth"."events" ("kind" TEXT NOT NULL) PARTITION BY LIST ("kind")
CREATE TABLE "events_auth" PARTITION OF "auth"."events" FOR VALUES IN ('login', 'logout')
CREATE TABLE "auth"."events" ("id" BIGINT NOT NULL) PARTITION BY HASH ("id")
CREATE TABLE "events_h0" PARTITION OF "auth"."events" FOR VALUES WITH (MODULUS 4, REMAINDER 0)
ALTER TABLE "auth"."events" ATTACH PARTITION "events_old" FOR VALUES FROM ('2026-08-01 00:00:00Z'::timestamptz) TO ('2026-09-01 00:00:00Z'::timestamptz)
ALTER TABLE "auth"."events" DETACH PARTITION "events_old" CONCURRENTLY
ALTER TABLE "auth"."events" DETACH PARTITION "events_old" FINALIZE`
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	if _, err := pg.CreateTable(events).Column(ddl.Column(id)).Clause(pg.PartitionByRange(at)).Build(); !errors.Is(err, ddl.ErrUnknownColumn) {
		t.Errorf("a partition key the table does not define: %v", err)
	}
	if _, err := pg.CreateTrigger(tr).After(pg.OnUpdateOf(tSessID)).Execute(fn).Build(); !errors.Is(err, ddl.ErrForeignColumn) {
		t.Errorf("an UPDATE OF another table's column: %v", err)
	}
	if _, err := pg.CreateTriggerFunction(fn, "  ").Build(); !errors.Is(err, pg.ErrEmptyBody) {
		t.Errorf("an empty body: %v", err)
	}
	if s, _ := pg.DetachPartition(events, sqlb.Table{Name: "x"}).Concurrently().Build(); s.Transactional() {
		t.Error("DETACH CONCURRENTLY claims to be transactional")
	}
}

// TestPolicies_AndTier4 renders sections 6.11, 6.3 and 6.14.
func TestPolicies_AndTier4(t *testing.T) {
	pol := pg.Policy{Name: "sessions_owner", Table: tSessions}
	me := sqlb.Cast[int64](pg.CurrentSetting("app.user_id"), "bigint")
	email := pg.Domain[string]{Schema: "auth", Name: "email"}
	ci := pg.Collation{Schema: "auth", Name: "ci"}
	got := buildAll(t,
		pg.Guarded(pg.CreatePolicy(pol).Permissive().For(pg.AllCommands).To(pg.RolePublic, pg.RoleCurrentUser).
			Using(sqlb.EqExpr(tSessUser, me)).WithCheck(sqlb.EqExpr(tSessUser, me))),
		pg.CreatePolicy(pol).Restrictive().For(pg.SelectCommand),
		pg.AlterPolicy(pol).RenameTo(pg.Policy{Name: "sessions_self", Table: tSessions}),
		pg.AlterPolicy(pol).Using(sqlb.EqExpr(tSessUser, me)),
		pg.AlterPolicy(pol).WithCheck(sqlb.EqExpr(tSessUser, me)),
		pg.DropPolicy(pol).IfExists(),
		pg.Guarded(pg.CreateDomain(email, ddl.RawType[string]("text")).NotNull().Check(pg.DomainConstraint[string]{Domain: email, Name: "email_at"}, pg.Regex(pg.DomainValue[string](), "^[^@]+@[^@]+$"))),
		pg.AlterDomain(email).AddConstraint(pg.DomainConstraint[string]{Domain: email, Name: "email_len"}, sqlb.LessEq(pg.Length(pg.DomainValue[string]()), 320)).NotValid(),
		pg.AlterDomain(email).ValidateConstraint(pg.DomainConstraint[string]{Domain: email, Name: "email_len"}),
		pg.AlterDomain(email).DropConstraint(pg.DomainConstraint[string]{Domain: email, Name: "email_len"}),
		pg.DropDomain(email).IfExists().Cascade(),
		pg.CreateCollation(ci, "und-u-ks-level2").ICU().Nondeterministic().IfNotExists(),
		pg.CreateTable(tUsers).Column(ddl.Column(tEmail).As(email)).Clause(pg.ColumnCollation(tEmail, ci)),
		pg.DropCollation(ci).IfExists(),
		pg.CreateStatistics(pg.Statistics{Schema: "auth", Name: "users_tenant_email"}, tUsers, tTenantID, tEmail).IfNotExists().Kinds(pg.Dependencies, pg.NDistinct, pg.MCV),
		pg.DropStatistics(pg.Statistics{Schema: "auth", Name: "users_tenant_email"}).IfExists(),
		pg.UpdateExtension(vector.Extension),
		pg.UpdateExtension(vector.Extension).To("0.8.0"),
		pg.RenameMaterializedView(pg.MaterializedView{Name: "a"}, pg.MaterializedView{Name: "b"}).IfExists(),
		pg.CommentOn(pol).Is("p"), pg.CommentOn(email).Is("d"), pg.CommentOn(ci).Is("c"),
	)
	want := `DO $sqlb$ BEGIN CREATE POLICY "sessions_owner" ON "auth"."sessions" AS PERMISSIVE FOR ALL TO PUBLIC, CURRENT_USER USING ("user_id" = CAST(current_setting('app.user_id', true) AS bigint)) WITH CHECK ("user_id" = CAST(current_setting('app.user_id', true) AS bigint)); EXCEPTION WHEN duplicate_object THEN NULL; END $sqlb$
CREATE POLICY "sessions_owner" ON "auth"."sessions" AS RESTRICTIVE FOR SELECT
ALTER POLICY "sessions_owner" ON "auth"."sessions" RENAME TO "sessions_self"
ALTER POLICY "sessions_owner" ON "auth"."sessions" USING ("user_id" = CAST(current_setting('app.user_id', true) AS bigint))
ALTER POLICY "sessions_owner" ON "auth"."sessions" WITH CHECK ("user_id" = CAST(current_setting('app.user_id', true) AS bigint))
DROP POLICY IF EXISTS "sessions_owner" ON "auth"."sessions"
DO $sqlb$ BEGIN CREATE DOMAIN "auth"."email" AS text NOT NULL CONSTRAINT "email_at" CHECK ((VALUE ~ '^[^@]+@[^@]+$')); EXCEPTION WHEN duplicate_object THEN NULL; END $sqlb$
ALTER DOMAIN "auth"."email" ADD CONSTRAINT "email_len" CHECK (length(VALUE) <= 320) NOT VALID
ALTER DOMAIN "auth"."email" VALIDATE CONSTRAINT "email_len"
ALTER DOMAIN "auth"."email" DROP CONSTRAINT IF EXISTS "email_len"
DROP DOMAIN IF EXISTS "auth"."email" CASCADE
CREATE COLLATION IF NOT EXISTS "auth"."ci" (provider = icu, locale = 'und-u-ks-level2', deterministic = false)
CREATE TABLE "auth"."users" ("email" "auth"."email" COLLATE "auth"."ci" NOT NULL)
DROP COLLATION IF EXISTS "auth"."ci"
CREATE STATISTICS IF NOT EXISTS "auth"."users_tenant_email" (dependencies, ndistinct, mcv) ON "tenant_id", "email" FROM "auth"."users"
DROP STATISTICS IF EXISTS "auth"."users_tenant_email"
ALTER EXTENSION "vector" UPDATE
ALTER EXTENSION "vector" UPDATE TO '0.8.0'
ALTER MATERIALIZED VIEW IF EXISTS "a" RENAME TO "b"
COMMENT ON POLICY "sessions_owner" ON "auth"."sessions" IS 'p'
COMMENT ON DOMAIN "auth"."email" IS 'd'
COMMENT ON COLLATION "auth"."ci" IS 'c'`
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}
