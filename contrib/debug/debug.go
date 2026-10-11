// Package debug renders a built statement with its arguments substituted, for
// reading.
//
// The need is ordinary: when a query misbehaves you want to see what it says,
// and "$1 $2 $3" beside a slice of values is not that. The danger is equally
// ordinary: a function that hands back a SQL string with values already in it
// is one careless call away from being executed, and then the placeholders
// that made the statement safe are gone.
//
// So the result is not a string. Rendered is a named string type, which prints
// exactly like a string and cannot be passed where a string is wanted without
// an explicit conversion:
//
//	db.ExecContext(ctx, debug.Interpolate(d, q))
//	// cannot use ... as string value in argument to ExecContext
//
// Nobody writes that conversion by accident, which is the whole mechanism. It
// lives in contrib rather than the core for the same reason: reading a query is
// a development concern, and a package that only builds statements should not
// carry the tools for defeating them.
//
// The output is for a human. It is not guaranteed to be valid SQL for any
// engine, it does not attempt the escaping rules of every type a driver
// accepts, and it must never be sent to a database.
package debug

import (
	"database/sql"
	"database/sql/driver"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/ubgo/sqlb/render"
	"github.com/ubgo/sqlb/stmt"
)

// Rendered is a SQL statement with its arguments substituted, for reading.
//
// It is deliberately not a string. See the package comment.
type Rendered string

// String makes Rendered printable, which is the only thing it is for.
func (r Rendered) String() string { return string(r) }

// Interpolate substitutes q's arguments into its SQL.
//
// The dialect is needed because placeholder syntax is the dialect's decision:
// asking it for Placeholder(1) and Placeholder(2) is how this works out
// whether markers are positional, as "?" is, or numbered, as "$1" is, without
// knowing anything about the engine.
func Interpolate(d render.Dialect, q stmt.Query) Rendered {
	if len(q.Args) == 0 {
		return Rendered(q.SQL)
	}
	if d.Placeholder(1) == d.Placeholder(2) {
		return Rendered(positional(d.Placeholder(1), q))
	}
	return Rendered(numbered(d, q))
}

// positional replaces each marker with the next argument, left to right, which
// is what "?" means.
func positional(marker string, q stmt.Query) string {
	var b strings.Builder
	next := 0
	for _, seg := range split(q.SQL, marker) {
		if seg.isMarker && next < len(q.Args) {
			b.WriteString(literal(q.Args[next]))
			next++
			continue
		}
		b.WriteString(seg.text)
	}
	return b.String()
}

// numbered replaces each marker by its own number, highest first so that $1
// does not match the start of $10.
func numbered(d render.Dialect, q stmt.Query) string {
	out := q.SQL
	for i := len(q.Args); i >= 1; i-- {
		out = replaceOutsideLiterals(out, d.Placeholder(i), literal(q.Args[i-1]))
	}
	return out
}

// segment is a run of SQL, or one placeholder.
type segment struct {
	text     string
	isMarker bool
}

// split walks the SQL and separates placeholders from everything else, without
// looking inside a quoted string or a quoted identifier.
//
// Skipping quoted runs matters because a question mark inside a string literal
// is data, not a placeholder, and substituting it would corrupt the very thing
// somebody is trying to read.
func split(sql, marker string) []segment {
	var out []segment
	var cur strings.Builder
	i := 0
	for i < len(sql) {
		if q := quoteRun(sql, i); q > i {
			cur.WriteString(sql[i:q])
			i = q
			continue
		}
		if marker != "" && strings.HasPrefix(sql[i:], marker) {
			out = append(out, segment{text: cur.String()}, segment{isMarker: true})
			cur.Reset()
			i += len(marker)
			continue
		}
		cur.WriteByte(sql[i])
		i++
	}
	out = append(out, segment{text: cur.String()})
	return out
}

// replaceOutsideLiterals replaces every occurrence of old outside quoted runs.
func replaceOutsideLiterals(sql, old, replacement string) string {
	var b strings.Builder
	i := 0
	for i < len(sql) {
		if q := quoteRun(sql, i); q > i {
			b.WriteString(sql[i:q])
			i = q
			continue
		}
		if old != "" && strings.HasPrefix(sql[i:], old) {
			b.WriteString(replacement)
			i += len(old)
			continue
		}
		b.WriteByte(sql[i])
		i++
	}
	return b.String()
}

// quoteRun returns the index just past a quoted run starting at i, or i when
// the byte there does not open one. Doubled quotes escape, which is how both
// SQL string literals and quoted identifiers work.
func quoteRun(sql string, i int) int {
	if i >= len(sql) {
		return i
	}
	q := sql[i]
	if q != '\'' && q != '"' && q != '`' {
		return i
	}
	j := i + 1
	for j < len(sql) {
		if sql[j] != q {
			j++
			continue
		}
		if j+1 < len(sql) && sql[j+1] == q {
			j += 2
			continue
		}
		return j + 1
	}
	return len(sql) // unterminated; treat the rest as quoted
}

// literal formats one argument for reading.
func literal(v any) string {
	switch t := v.(type) {
	case nil:
		return "NULL"
	case sql.NamedArg:
		return literal(t.Value)
	case driver.Valuer:
		got, err := t.Value()
		if err != nil {
			return "<Value() error: " + err.Error() + ">"
		}
		return literal(got)
	case string:
		return quote(t)
	case []byte:
		return "'\\x" + fmt.Sprintf("%x", t) + "'"
	case bool:
		if t {
			return "TRUE"
		}
		return "FALSE"
	case time.Time:
		return quote(t.Format(time.RFC3339Nano))
	case int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64:
		return fmt.Sprintf("%v", t)
	default:
		return quote(fmt.Sprintf("%v", t))
	}
}

// quote wraps a value in single quotes, doubling any it contains.
func quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// Explain is Interpolate with the argument count appended, for a log line.
func Explain(d render.Dialect, q stmt.Query) Rendered {
	return Rendered(string(Interpolate(d, q)) +
		"  -- " + strconv.Itoa(len(q.Args)) + " arg(s), " + render.DialectName(d))
}
