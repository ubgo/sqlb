package sqlite

import (
	"errors"
	"time"

	"github.com/ubgo/sqlb"
)

// Time in SQLite.
//
// There is no date or timestamp type. A time is stored as ISO-8601 text, as a
// Julian day number, or as a Unix epoch integer, and the column's declared
// type does not say which. Every function here takes a time value plus
// modifiers and returns text, so the caller decides the representation and
// the schema has to be consistent about it on its own.
//
// That is the trap this package cannot close: two rows in one column can hold
// two different representations and SQLite will not complain. Pick one for
// the whole column, and prefer ISO-8601 text, which sorts correctly as text
// and is the only form a human can read in a dump.

// Modifier is a SQLite time modifier such as "+1 day", "start of month" or
// "utc". They apply left to right, so order changes the answer.
//
// The type is a string rather than a closed set because it cannot be closed:
// an offset carries its number inside the string, so "+1 day", "-7 days" and
// "+1.5 seconds" are all modifiers and no enumeration covers them. Every
// modifier is written through the dialect as a LITERAL, so a value from
// anywhere reaches SQLite as one argument and cannot leave its position; that
// is the property an open type has to have.
//
// **What it cannot protect you from is a modifier SQLite does not recognise,
// because SQLite does not report one.** Measured on 3.50.3: date('2020-03-15',
// '+1 banana') returns NULL, and so does 'start of week', and so does the empty
// string. Not an error — NULL, from the whole function, silently. So the parts
// of the vocabulary that ARE closed are closed here: Offset takes a Unit
// rather than a string, which turns a misspelled unit from a NULL at run time
// into a compile error, and an empty Modifier is refused at Build. Anything
// else you spell yourself is on you, and a NULL result is the symptom to look
// for.
type Modifier string

// The modifiers with fixed spellings. An offset such as "+1 day" is written
// by Offset, because its value is part of the string.
//
// Note the absence of a "start of week": SQLite has no such modifier, and
// writing one returns NULL rather than an error. Weekday is how the same
// question is asked.
const (
	StartOfDay   Modifier = "start of day"
	StartOfMonth Modifier = "start of month"
	StartOfYear  Modifier = "start of year"
	UTC          Modifier = "utc"
	LocalTime    Modifier = "localtime"
	Unixepoch    Modifier = "unixepoch"
	JulianDayMod Modifier = "julianday"
	Auto         Modifier = "auto"
	// Subsec keeps fractional seconds in the result, which the other
	// functions truncate away.
	Subsec Modifier = "subsec"
	// Ceiling and Floor decide how a month-or-year offset lands on a day the
	// target month does not have: 31 January plus one month is 2 March under
	// Ceiling and 28 February under Floor.
	Ceiling Modifier = "ceiling"
	Floor   Modifier = "floor"
)

// ErrEmptyModifier is an empty time modifier, which SQLite answers with NULL
// from the whole function rather than with an error.
var ErrEmptyModifier = errors.New("sqlb/sqlite: a time modifier cannot be empty")

// Unit is the unit of a relative time offset. It is a closed set, unlike
// Modifier itself, because these six are the whole vocabulary.
type Unit string

// The offset units. SQLite accepts the singular and the plural of each,
// measured; the plural is written because it reads correctly for every count
// including one.
const (
	Day    Unit = "days"
	Hour   Unit = "hours"
	Minute Unit = "minutes"
	Second Unit = "seconds"
	Month  Unit = "months"
	Year   Unit = "years"
)

// valid reports whether the unit is one of the six constants.
func (u Unit) valid() bool {
	switch u {
	case Day, Hour, Minute, Second, Month, Year:
		return true
	}
	return false
}

func withModifiers(e sqlb.SQLer, mods []Modifier) []sqlb.SQLer {
	out := make([]sqlb.SQLer, 0, len(mods)+1)
	out = append(out, e)
	for _, m := range mods {
		out = append(out, modifierLit(m))
	}
	return out
}

// modifierLit renders one modifier as a literal, failing the build on an empty
// one rather than passing SQLite a string it answers with NULL.
func modifierLit(m Modifier) sqlb.SQLer {
	return sqlb.Raw(func(a *sqlb.Args) string {
		if m == "" {
			a.Fail(ErrEmptyModifier)
			return ""
		}
		return quoteLit(string(m)).SQL(a)
	})
}

// Date renders date(e, modifiers...), the date part as "YYYY-MM-DD".
func Date(e sqlb.SQLer, mods ...Modifier) sqlb.Expr[string] {
	return fn[string](constructTime, "date", withModifiers(e, mods)...)
}

// Time renders time(e, modifiers...), the time part as "HH:MM:SS".
func Time(e sqlb.SQLer, mods ...Modifier) sqlb.Expr[string] {
	return fn[string](constructTime, "time", withModifiers(e, mods)...)
}

// DateTime renders datetime(e, modifiers...) as "YYYY-MM-DD HH:MM:SS". It is
// the closest thing SQLite has to date_trunc, through StartOfDay and friends.
func DateTime(e sqlb.SQLer, mods ...Modifier) sqlb.Expr[string] {
	return fn[string](constructTime, "datetime", withModifiers(e, mods)...)
}

// JulianDay renders julianday(e, modifiers...), days since noon in Greenwich
// on 24 November 4714 BC. Subtracting two of them is how SQLite measures an
// interval, since it has no interval type.
func JulianDay(e sqlb.SQLer, mods ...Modifier) sqlb.Expr[float64] {
	return fn[float64](constructTime, "julianday", withModifiers(e, mods)...)
}

// UnixEpoch renders unixepoch(e, modifiers...) (3.38), seconds since 1970.
func UnixEpoch(e sqlb.SQLer, mods ...Modifier) sqlb.Expr[int64] {
	return fn[int64](constructTime, "unixepoch", withModifiers(e, mods)...)
}

// Strftime renders strftime(format, e, modifiers...), the general formatter
// every other function here is a shorthand for. The format is an author
// constant such as "%Y-%W" for the ISO week.
func Strftime(format string, e sqlb.SQLer, mods ...Modifier) sqlb.Expr[string] {
	args := append([]sqlb.SQLer{quoteLit(format)}, withModifiers(e, mods)...)
	return fn[string](constructTime, "strftime", args...)
}

// Now renders the literal 'now', the argument every time function takes to
// mean the current instant. It is UTC unless followed by LocalTime.
func Now() sqlb.Expr[string] {
	return expr[string](constructTime, func(*sqlb.Args) string { return "'now'" })
}

// Offset is a relative time modifier: Offset(-7, Day) is "-7 days".
//
// The unit is a closed set rather than a string, and that is the whole point
// of the type. SQLite does not refuse a unit it does not know — the function
// returns NULL, silently, measured — so Offset(1, "fortnights"), or a
// misspelling of a real unit, would have been a run-time NULL in a date
// somebody reads months later. As a Unit neither compiles. An unrecognised value reaching here anyway, through a conversion,
// renders a modifier that fails at Build.
func Offset(n int, unit Unit) Modifier {
	if !unit.valid() {
		// The modifier carries its own refusal, since a Modifier is a value
		// and has nowhere to return an error from.
		return Modifier("")
	}
	sign := "+"
	if n < 0 {
		sign, n = "-", -n
	}
	return Modifier(sign + itoa(n) + " " + string(unit))
}

// Weekday moves forward to the next date whose day of the week is n, 0 for
// Sunday through 6 for Saturday, and does nothing when the date already is
// that day. It is how "the start of this week" is asked for, since SQLite has
// no start-of-week modifier.
func Weekday(n int) Modifier { return Modifier("weekday " + itoa(n)) }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// TimeLit binds a Go time as the ISO-8601 text SQLite compares and sorts
// correctly. Passing a time.Time straight through leaves the format to the
// driver, and drivers disagree.
func TimeLit(t time.Time) sqlb.Expr[string] {
	return sqlb.Param(t.UTC().Format("2006-01-02 15:04:05"))
}
