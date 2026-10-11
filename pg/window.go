package pg

import "github.com/ubgo/sqlb"

const (
	constructWindow = "pg.window"

	fnPercentRank = "percent_rank"
	fnCumeDist    = "cume_dist"
	fnNtile       = "ntile"
	fnLag         = "lag"
	fnLead        = "lead"
)

// FrameGroups is the GROUPS frame mode. Postgres and SQLite only.
const FrameGroups sqlb.FrameMode = "GROUPS"

// Frame EXCLUDE values. Postgres and SQLite only.
const (
	ExcludeCurrentRow sqlb.FrameExclude = "EXCLUDE CURRENT ROW"
	ExcludeGroup      sqlb.FrameExclude = "EXCLUDE GROUP"
	ExcludeTies       sqlb.FrameExclude = "EXCLUDE TIES"
	ExcludeNoOthers   sqlb.FrameExclude = "EXCLUDE NO OTHERS"
)

func winFn0[T any](name string) sqlb.WinFn[T] {
	return sqlb.NewWinFn[T](func(a *sqlb.Args) string {
		if !guard(a, constructWindow) {
			return ""
		}
		return name + "()"
	})
}

// PercentRank renders percent_rank(), the rank as a fraction from 0 to 1.
// Never null: a rank always exists for a row.
func PercentRank() sqlb.WinFn[float64] { return winFn0[float64](fnPercentRank) }

// CumeDist renders cume_dist(), the cumulative distribution. It differs from
// PercentRank on ties, where it counts the whole peer group.
func CumeDist() sqlb.WinFn[float64] { return winFn0[float64](fnCumeDist) }

// Ntile splits the partition into n buckets. n is bound.
func Ntile(n int) sqlb.WinFn[int32] {
	return sqlb.NewWinFn[int32](func(a *sqlb.Args) string {
		if !guard(a, constructWindow) {
			return ""
		}
		return fnNtile + "(" + a.Next(n) + ")"
	})
}

// Lag and Lead without a default are nullable: the row may not exist.
// ClickHouse spells these lagInFrame and leadInFrame, which is why they are
// here and not in the core.
func Lag[T any](e sqlb.Expr[T], offset int) sqlb.WinFn[*T] {
	return offsetFn[*T](fnLag, e, offset, nil)
}

// Lead renders lead(e, offset), the row ahead. Nullable: the last rows of a
// partition have nothing ahead of them.
func Lead[T any](e sqlb.Expr[T], offset int) sqlb.WinFn[*T] {
	return offsetFn[*T](fnLead, e, offset, nil)
}

// LagDefault renders lag(e, offset, default). Supplying a default is what
// makes the result non-null, which is why it is typed T and not *T.
func LagDefault[T any](e sqlb.Expr[T], offset int, def sqlb.Expr[T]) sqlb.WinFn[T] {
	return offsetFn[T](fnLag, e, offset, &def)
}

// LeadDefault renders lead(e, offset, default), non-null for the same reason.
func LeadDefault[T any](e sqlb.Expr[T], offset int, def sqlb.Expr[T]) sqlb.WinFn[T] {
	return offsetFn[T](fnLead, e, offset, &def)
}

func offsetFn[R, T any](name string, e sqlb.Expr[T], offset int, def *sqlb.Expr[T]) sqlb.WinFn[R] {
	return sqlb.NewWinFn[R](func(a *sqlb.Args) string {
		if !guard(a, constructWindow) {
			return ""
		}
		s := name + "(" + e.SQL(a) + ", " + a.Next(offset)
		if def != nil {
			s += ", " + def.SQL(a)
		}
		return s + ")"
	})
}
