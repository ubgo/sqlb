package pg

import "github.com/ubgo/sqlb"

const (
	constructMath = "pg.math"

	fnAbs         = "abs"
	fnCeil        = "ceil"
	fnFloor       = "floor"
	fnRound       = "round"
	fnTrunc       = "trunc"
	fnPower       = "power"
	fnSqrt        = "sqrt"
	fnExp         = "exp"
	fnLn          = "ln"
	fnLog         = "log"
	fnRandom      = "random"
	fnWidthBucket = "width_bucket"
	fnGCD         = "gcd"
	fnSign        = "sign"
	fnGenUUID     = "gen_random_uuid"
	fnNextval     = "nextval"
	fnCurrentUser = "current_user"
	fnVersion     = "version"
	fnPgBackend   = "pg_backend_pid"
	fnNumNonnulls = "num_nonnulls"
)

// Abs renders abs(e).
//
// This and the three below keep the operand type, because Postgres does:
// rounding an integer column does not silently widen it to a float in the Go
// type, so a bound target stays the type the column actually returns.
func Abs[T sqlb.Number](e sqlb.Expr[T]) sqlb.Expr[T] { return fn[T](constructMath, fnAbs, e) }

// Ceil renders ceil(e), rounding toward positive infinity.
func Ceil[T sqlb.Number](e sqlb.Expr[T]) sqlb.Expr[T] { return fn[T](constructMath, fnCeil, e) }

// Floor renders floor(e), rounding toward negative infinity.
func Floor[T sqlb.Number](e sqlb.Expr[T]) sqlb.Expr[T] { return fn[T](constructMath, fnFloor, e) }

// Sign renders sign(e): -1, 0 or 1.
func Sign[T sqlb.Number](e sqlb.Expr[T]) sqlb.Expr[T] { return fn[T](constructMath, fnSign, e) }

// Round renders round(e, places). Postgres accepts places only for numeric,
// so the operand is cast: round(e::numeric, $n).
func Round[T sqlb.Number](e sqlb.Expr[T], places int) sqlb.Expr[float64] {
	return expr[float64](constructMath, func(a *sqlb.Args) string {
		return fnRound + "(" + e.SQL(a) + "::numeric, " + a.Next(places) + ")"
	})
}

// Trunc renders trunc(e, places), rounding toward zero. Like Round it casts
// to numeric, because Postgres accepts a digit count only for that type.
func Trunc[T sqlb.Number](e sqlb.Expr[T], places int) sqlb.Expr[float64] {
	return expr[float64](constructMath, func(a *sqlb.Args) string {
		return fnTrunc + "(" + e.SQL(a) + "::numeric, " + a.Next(places) + ")"
	})
}

// Power renders power(base, exp). The result is float64 whatever the operand
// type, because an integer base with a fractional exponent is not an integer.
func Power[T sqlb.Number](base sqlb.Expr[T], exp float64) sqlb.Expr[float64] {
	return fn[float64](constructMath, fnPower, base, sqlb.Param(exp))
}

// Sqrt renders sqrt(e).
func Sqrt[T sqlb.Number](e sqlb.Expr[T]) sqlb.Expr[float64] {
	return fn[float64](constructMath, fnSqrt, e)
}

// Exp renders exp(e), e raised to the operand.
func Exp[T sqlb.Number](e sqlb.Expr[T]) sqlb.Expr[float64] {
	return fn[float64](constructMath, fnExp, e)
}

// Ln renders ln(e), the natural logarithm.
func Ln[T sqlb.Number](e sqlb.Expr[T]) sqlb.Expr[float64] { return fn[float64](constructMath, fnLn, e) }

// Log10 renders log(e). Postgres spells the base-10 logarithm log, and the
// natural one ln, which is the reverse of several other engines; the Go name
// says which one you get.
func Log10[T sqlb.Number](e sqlb.Expr[T]) sqlb.Expr[float64] {
	return fn[float64](constructMath, fnLog, e)
}

// Random renders random(), a value in [0,1). It is volatile, so a query that
// names it twice gets two different values.
func Random() sqlb.Expr[float64] { return fn[float64](constructMath, fnRandom) }

// DivExact renders (l::numeric / r), so integer division does not truncate.
func DivExact[T sqlb.Number](l, r sqlb.Expr[T]) sqlb.Expr[float64] {
	return expr[float64](constructMath, func(a *sqlb.Args) string {
		return "(" + l.SQL(a) + "::numeric / " + r.SQL(a) + ")"
	})
}

// WidthBucket renders width_bucket(e, lo, hi, count).
func WidthBucket[T sqlb.Number](e sqlb.Expr[T], lo, hi T, count int) sqlb.Expr[int32] {
	return fn[int32](constructMath, fnWidthBucket, e, sqlb.Param(lo), sqlb.Param(hi), sqlb.Param(count))
}

// GCD renders gcd(l, r), the greatest common divisor.
func GCD[T sqlb.Number](l, r sqlb.Expr[T]) sqlb.Expr[T] { return fn[T](constructMath, fnGCD, l, r) }

// GenRandomUUID renders gen_random_uuid(), typed string.
func GenRandomUUID() sqlb.Expr[string] { return fn[string](constructMath, fnGenUUID) }

// Nextval renders nextval('seq'). The sequence name is a constant.
func Nextval(sequence string) sqlb.Expr[int64] {
	return fn[int64](constructMath, fnNextval, quoteLit(sequence))
}

// CurrentUser renders current_user, which takes no parentheses.
func CurrentUser() sqlb.Expr[string] {
	return expr[string](constructMath, func(*sqlb.Args) string { return fnCurrentUser })
}

// Version renders version(), the server version banner. Useful in a health
// check; never parse it to gate behaviour, because its shape is not stable.
func Version() sqlb.Expr[string] { return fn[string](constructMath, fnVersion) }

// BackendPID renders pg_backend_pid(), the server-side process id of this
// connection. It is what pg_stat_activity and pg_cancel_backend key on.
func BackendPID() sqlb.Expr[int32] { return fn[int32](constructMath, fnPgBackend) }

// NumNonnulls renders num_nonnulls(items...), counting how many arguments
// are not NULL. The usual use is a check that exactly one of several
// mutually exclusive columns is set.
func NumNonnulls(items ...sqlb.SQLer) sqlb.Expr[int32] {
	return fn[int32](constructMath, fnNumNonnulls, items...)
}
