package pg

import (
	"time"

	"github.com/ubgo/sqlb"
)

const (
	constructTime = "pg.time"

	fnNow                = "now"
	fnClockTimestamp     = "clock_timestamp"
	fnStatementTimestamp = "statement_timestamp"
	fnCurrentDate        = "current_date"
	fnDateTrunc          = "date_trunc"
	fnDateBin            = "date_bin"
	fnExtract            = "extract"
	fnAge                = "age"
	fnMakeInterval       = "make_interval"
	fnToTimestamp        = "to_timestamp"
	fnToDate             = "to_date"
	fnJustifyInterval    = "justify_interval"
)

// Interval is a Postgres interval in its text form, for example "1 day" or
// "01:30:00". Scanned and valued as text; the driver casts.
type Interval string

// DateField names the field for Extract and DateTrunc. A closed set, so a
// typo is a compile error.
type DateField string

// Every field date_trunc and extract accept. A named type rather than a
// string, so a typo is a compile error and not a run-time surprise.
const (
	FieldYear    DateField = "year"
	FieldQuarter DateField = "quarter"
	FieldMonth   DateField = "month"
	FieldWeek    DateField = "week"
	FieldDay     DateField = "day"
	FieldHour    DateField = "hour"
	FieldMinute  DateField = "minute"
	FieldSecond  DateField = "second"
	FieldDow     DateField = "dow"
	FieldDoy     DateField = "doy"
	FieldEpoch   DateField = "epoch"
)

// Now renders now(), the transaction start time.
func Now() sqlb.Expr[time.Time] { return fn[time.Time](constructTime, fnNow) }

// ClockTimestamp renders clock_timestamp(), the wall clock.
func ClockTimestamp() sqlb.Expr[time.Time] { return fn[time.Time](constructTime, fnClockTimestamp) }

// StatementTimestamp renders statement_timestamp(), fixed for the statement.
// It sits between now(), which is fixed for the transaction, and
// ClockTimestamp, which moves within a statement.
func StatementTimestamp() sqlb.Expr[time.Time] {
	return fn[time.Time](constructTime, fnStatementTimestamp)
}

// CurrentDate renders current_date, which takes no parentheses.
func CurrentDate() sqlb.Expr[time.Time] {
	return expr[time.Time](constructTime, func(*sqlb.Args) string { return fnCurrentDate })
}

// DateTrunc renders date_trunc('field', e). The field is a constant.
func DateTrunc(field DateField, e sqlb.Expr[time.Time]) sqlb.Expr[time.Time] {
	return fn[time.Time](constructTime, fnDateTrunc, closedLit(field, field.valid(), "date field"), e)
}

// DateBin renders date_bin(stride, e, origin): fixed-width buckets.
func DateBin(stride Interval, e sqlb.Expr[time.Time], origin time.Time) sqlb.Expr[time.Time] {
	return expr[time.Time](constructTime, func(a *sqlb.Args) string {
		return fnDateBin + "(" + a.Next(string(stride)) + "::interval, " + e.SQL(a) + ", " + a.Next(origin) + ")"
	})
}

// Extract renders extract(field FROM e). Postgres returns numeric; float64
// is the Go choice and the precision note is in the builder spec.
func Extract(field DateField, e sqlb.Expr[time.Time]) sqlb.Expr[float64] {
	return expr[float64](constructTime, func(a *sqlb.Args) string {
		return fnExtract + "(" + closedWord(a, field, field.valid(), "date field") + " FROM " + e.SQL(a) + ")"
	})
}

// AtTimeZone renders e AT TIME ZONE $n.
func AtTimeZone(e sqlb.Expr[time.Time], zone string) sqlb.Expr[time.Time] {
	return expr[time.Time](constructTime, func(a *sqlb.Args) string {
		return e.SQL(a) + " AT TIME ZONE " + a.Next(zone)
	})
}

// AddInterval renders e + $n::interval; SubInterval renders e - $n::interval.
func AddInterval(e sqlb.Expr[time.Time], iv Interval) sqlb.Expr[time.Time] {
	return intervalArith(e, "+", iv)
}

// SubInterval renders (e - interval '...') with a constant interval.
func SubInterval(e sqlb.Expr[time.Time], iv Interval) sqlb.Expr[time.Time] {
	return intervalArith(e, "-", iv)
}
func intervalArith(e sqlb.Expr[time.Time], op string, iv Interval) sqlb.Expr[time.Time] {
	return expr[time.Time](constructTime, func(a *sqlb.Args) string {
		return "(" + e.SQL(a) + " " + op + " " + a.Next(string(iv)) + "::interval)"
	})
}

// Diff renders (l - r), a timestamp difference, as an Interval.
func Diff(l, r sqlb.Expr[time.Time]) sqlb.Expr[Interval] {
	return binop[Interval](constructTime, "-", l, r)
}

// Age renders age(l, r).
func Age(l, r sqlb.Expr[time.Time]) sqlb.Expr[Interval] {
	return fn[Interval](constructTime, fnAge, l, r)
}

// MakeInterval renders make_interval(days => $1, hours => $2).
func MakeInterval(days, hours int) sqlb.Expr[Interval] {
	return expr[Interval](constructTime, func(a *sqlb.Args) string {
		return fnMakeInterval + "(days => " + a.Next(days) + ", hours => " + a.Next(hours) + ")"
	})
}

// ToTimestamp renders to_timestamp(e, format). ToDate renders to_date.
func ToTimestamp(e sqlb.Expr[string], format string) sqlb.Expr[time.Time] {
	return fn[time.Time](constructTime, fnToTimestamp, e, sqlb.Param(format))
}

// ToDate renders to_date(e, format), parsing text with a Postgres format
// string such as 'YYYY-MM-DD'. The format is bound, not interpolated.
func ToDate(e sqlb.Expr[string], format string) sqlb.Expr[time.Time] {
	return fn[time.Time](constructTime, fnToDate, e, sqlb.Param(format))
}

// EpochSeconds renders to_timestamp(seconds) for a float epoch.
func EpochSeconds(e sqlb.Expr[float64]) sqlb.Expr[time.Time] {
	return fn[time.Time](constructTime, fnToTimestamp, e)
}

// OverlapsPeriod renders (s1, e1) OVERLAPS (s2, e2).
func OverlapsPeriod(s1, e1, s2, e2 sqlb.Expr[time.Time]) sqlb.Predicate {
	return pred(constructTime, func(a *sqlb.Args) string {
		return "(" + s1.SQL(a) + ", " + e1.SQL(a) + ") OVERLAPS (" + s2.SQL(a) + ", " + e2.SQL(a) + ")"
	})
}
