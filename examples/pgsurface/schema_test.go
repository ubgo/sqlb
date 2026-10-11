// Package pgsurface_test is the Postgres surface catalogue as code: one
// Example per construct of docsi/PG_SURFACE.md, with the rendered SQL checked
// by go test. The schema below is the one every recipe uses.
package pgsurface_test

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/pg"
	"github.com/ubgo/sqlb/pg/vector"
)

var d = sqlb.PostgresDialect{}

func show(q sqlb.Query, err error) {
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println(q.SQL)
	if len(q.Args) > 0 {
		fmt.Println("args:", jsonArgsAsText(q.Args))
	}
}

// jsonArgsAsText returns args with every json.RawMessage converted to a
// string. fmt prints a RawMessage as its bytes on Go 1.24, the module's floor,
// and as its text on Go 1.27, so printing args directly made the Output of
// every recipe with a JSON argument depend on the toolchain. As a string it
// prints the same on both.
func jsonArgsAsText(args []any) []any {
	out := make([]any, len(args))
	for i, a := range args {
		if raw, ok := a.(json.RawMessage); ok {
			a = string(raw)
		}
		out[i] = a
	}
	return out
}

var (
	Users    = sqlb.Table{Name: "users", Alias: "u"}
	Posts    = sqlb.Table{Name: "posts", Alias: "p"}
	Orders   = sqlb.Table{Name: "orders", Alias: "o"}
	Profiles = sqlb.Table{Name: "profiles", Alias: "pr"}
	Cats     = sqlb.Table{Name: "categories", Alias: "c"}
	Events   = sqlb.Table{Name: "events", Alias: "e"}
	Docs     = sqlb.Table{Name: "docs", Alias: "dc"}
	Jobs     = sqlb.Table{Name: "jobs", Alias: "j"}
	Bookings = sqlb.Table{Name: "bookings", Alias: "b"}
)

type Plan string

const PlanPro Plan = "pro"

var (
	UID        = sqlb.Col[string](Users, "id")
	UEmail     = sqlb.Col[string](Users, "email")
	UName      = sqlb.Col[string](Users, "name")
	UAge       = sqlb.Col[int32](Users, "age")
	UPlan      = sqlb.Col[Plan](Users, "plan")
	UActive    = sqlb.Col[bool](Users, "active")
	UTags      = sqlb.Col[[]string](Users, "tags")
	UMeta      = sqlb.Col[json.RawMessage](Users, "meta")
	UCreatedAt = sqlb.Col[time.Time](Users, "created_at")
	UDeletedAt = sqlb.Col[*time.Time](Users, "deleted_at")

	PID        = sqlb.Col[string](Posts, "id")
	PUserID    = sqlb.Col[string](Posts, "user_id")
	PTitle     = sqlb.Col[string](Posts, "title")
	PBody      = sqlb.Col[string](Posts, "body")
	PSearch    = sqlb.Col[pg.TSVector](Posts, "search")
	PPublished = sqlb.Col[bool](Posts, "published")
	PViews     = sqlb.Col[int64](Posts, "views")
	PCreatedAt = sqlb.Col[time.Time](Posts, "created_at")

	OID        = sqlb.Col[string](Orders, "id")
	OUserID    = sqlb.Col[string](Orders, "user_id")
	OTotal     = sqlb.Col[float64](Orders, "total")
	OQty       = sqlb.Col[int32](Orders, "qty")
	OPrice     = sqlb.Col[float64](Orders, "price")
	OStatus    = sqlb.Col[string](Orders, "status")
	OCreatedAt = sqlb.Col[time.Time](Orders, "created_at")

	PrUserID = sqlb.Col[string](Profiles, "user_id")
	PrBio    = sqlb.Col[string](Profiles, "bio")

	CID       = sqlb.Col[int64](Cats, "id")
	CParentID = sqlb.Col[*int64](Cats, "parent_id")
	CName     = sqlb.Col[string](Cats, "name")

	EUserID = sqlb.Col[string](Events, "user_id")
	EKind   = sqlb.Col[string](Events, "kind")
	EAt     = sqlb.Col[time.Time](Events, "at")

	DcID        = sqlb.Col[string](Docs, "id")
	DcEmbedding = sqlb.Col[vector.Vector](Docs, "embedding")

	JID    = sqlb.Col[string](Jobs, "id")
	JQueue = sqlb.Col[string](Jobs, "queue")
	JRunAt = sqlb.Col[time.Time](Jobs, "run_at")

	BID     = sqlb.Col[string](Bookings, "id")
	BPeriod = sqlb.Col[pg.Range[time.Time]](Bookings, "period")
)

var (
	t0   = time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	t1   = time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	sink = struct {
		s, s2, s3 string
		i32       int32
		i64       int64
		i64p      *int64
		f64       float64
		f32       float32
		b         bool
		t         time.Time
		tp        *time.Time
		j         json.RawMessage
		ss        []string
		sp        *string
		iv        pg.Interval
		r         pg.Range[time.Time]
		i32p      *int32
	}{}
)

// sel starts a Postgres SELECT.
func sel() *sqlb.Select[sqlb.PostgresDialect] { return sqlb.NewSelect(d) }
