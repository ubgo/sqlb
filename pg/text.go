package pg

import "github.com/ubgo/sqlb"

const (
	constructText = "pg.text"

	opILike     = "ILIKE"
	opNotILike  = "NOT ILIKE"
	opSimilarTo = "SIMILAR TO"
	opRegex     = "~"
	opRegexI    = "~*"
	opNotRegex  = "!~"
	opNotRegexI = "!~*"

	fnUpper         = "upper"
	fnLower         = "lower"
	fnInitcap       = "initcap"
	fnLength        = "length"
	fnSubstring     = "substring"
	fnLeft          = "left"
	fnRight         = "right"
	fnTrim          = "btrim"
	fnLTrim         = "ltrim"
	fnRTrim         = "rtrim"
	fnLPad          = "lpad"
	fnRPad          = "rpad"
	fnPosition      = "strpos"
	fnStartsWith    = "starts_with"
	fnReplace       = "replace"
	fnSplitPart     = "split_part"
	fnConcatWs      = "concat_ws"
	fnRepeat        = "repeat"
	fnReverse       = "reverse"
	fnMD5           = "md5"
	fnSHA256        = "sha256"
	fnEncode        = "encode"
	fnRegexpReplace = "regexp_replace"
	fnRegexpMatch   = "regexp_match"
	fnRegexpCount   = "regexp_count"
	fnRegexpSplitT  = "regexp_split_to_table"
	fnRegexpSplitA  = "regexp_split_to_array"
	fnToChar        = "to_char"
	fnFormat        = "format"
)

// ILike renders e ILIKE $n, case-insensitive LIKE with a bound pattern.
// Postgres only; the other engines spell case-insensitive matching with
// lower() or a collation.
func ILike(e sqlb.Expr[string], pattern string) sqlb.Predicate {
	return binpred(constructText, opILike, e, sqlb.Param(pattern))
}

// NotILike renders e NOT ILIKE $n.
func NotILike(e sqlb.Expr[string], pattern string) sqlb.Predicate {
	return binpred(constructText, opNotILike, e, sqlb.Param(pattern))
}

// SimilarTo renders e SIMILAR TO $n.
func SimilarTo(e sqlb.Expr[string], pattern string) sqlb.Predicate {
	return binpred(constructText, opSimilarTo, e, sqlb.Param(pattern))
}

// Regex renders e ~ $n, a case-sensitive POSIX regular-expression match.
//
// The four operators in this family are ~, ~*, !~ and !~*: the star is
// case-insensitive and the bang negates. The pattern always binds.
func Regex(e sqlb.Expr[string], pattern string) sqlb.Predicate {
	return binpred(constructText, opRegex, e, sqlb.Param(pattern))
}

// RegexI renders e ~* $n, the case-insensitive match.
func RegexI(e sqlb.Expr[string], pattern string) sqlb.Predicate {
	return binpred(constructText, opRegexI, e, sqlb.Param(pattern))
}

// NotRegex renders e !~ $n.
func NotRegex(e sqlb.Expr[string], pattern string) sqlb.Predicate {
	return binpred(constructText, opNotRegex, e, sqlb.Param(pattern))
}

// NotRegexI renders e !~* $n.
func NotRegexI(e sqlb.Expr[string], pattern string) sqlb.Predicate {
	return binpred(constructText, opNotRegexI, e, sqlb.Param(pattern))
}

// Upper renders upper(e). The core has Lower, because every engine has it;
// upper() is here because ClickHouse spells it upperUTF8 for non-ASCII.
func Upper(e sqlb.Expr[string]) sqlb.Expr[string] { return fn[string](constructText, fnUpper, e) }

// Initcap renders initcap(e), capitalising the first letter of each word.
// Word boundaries are non-alphanumeric characters, so "o'brien" becomes
// "O'Brien" and not "O'brien".
func Initcap(e sqlb.Expr[string]) sqlb.Expr[string] { return fn[string](constructText, fnInitcap, e) }

// Length renders length(e), in characters. OctetLength counts bytes, and the
// two differ for anything that is not pure ASCII.
func Length(e sqlb.Expr[string]) sqlb.Expr[int32] { return fn[int32](constructText, fnLength, e) }

// Reverse renders reverse(e). Reverses by character, which is not the same
// as reversing by grapheme: a combining accent ends up on the wrong letter.
func Reverse(e sqlb.Expr[string]) sqlb.Expr[string] { return fn[string](constructText, fnReverse, e) }

// Substring renders substring(e, from, count) with bound positions.
func Substring(e sqlb.Expr[string], from, count int) sqlb.Expr[string] {
	// substring is overloaded on (text, int, int) and (text, text), so a
	// bare parameter pair resolves to the text form and the driver cannot
	// encode an integer there; the casts pick the overload.
	return fn[string](constructText, fnSubstring, e, sqlb.Cast[int](sqlb.Param(from), typeInt), sqlb.Cast[int](sqlb.Param(count), typeInt))
}

// Left renders left(e, n), the first n characters. A negative n means all
// but the last n.
func Left(e sqlb.Expr[string], n int) sqlb.Expr[string] {
	return fn[string](constructText, fnLeft, e, sqlb.Param(n))
}

// Right renders right(e, n), the last n characters.
func Right(e sqlb.Expr[string], n int) sqlb.Expr[string] {
	return fn[string](constructText, fnRight, e, sqlb.Param(n))
}

// Trim renders btrim(e), removing whitespace from both ends. TrimChars is
// the form that takes the characters to strip.
func Trim(e sqlb.Expr[string]) sqlb.Expr[string] { return fn[string](constructText, fnTrim, e) }

// LTrim renders ltrim(e), the leading end only.
func LTrim(e sqlb.Expr[string]) sqlb.Expr[string] { return fn[string](constructText, fnLTrim, e) }

// RTrim renders rtrim(e), the trailing end only.
func RTrim(e sqlb.Expr[string]) sqlb.Expr[string] { return fn[string](constructText, fnRTrim, e) }

// LPad renders lpad(e, n, fill). It also truncates: a value longer than n
// comes back cut to n, which is the half everyone forgets.
func LPad(e sqlb.Expr[string], n int, fill string) sqlb.Expr[string] {
	return fn[string](constructText, fnLPad, e, sqlb.Param(n), sqlb.Param(fill))
}

// RPad renders rpad(e, n, fill), padding and truncating on the right.
func RPad(e sqlb.Expr[string], n int, fill string) sqlb.Expr[string] {
	return fn[string](constructText, fnRPad, e, sqlb.Param(n), sqlb.Param(fill))
}

// Position renders strpos(e, needle): 1-based, 0 when absent.
func Position(e sqlb.Expr[string], needle string) sqlb.Expr[int32] {
	return fn[int32](constructText, fnPosition, e, sqlb.Param(needle))
}

// StartsWith renders starts_with(e, prefix). Unlike LIKE it needs no
// escaping of the prefix, so a user-supplied string with % or _ in it means
// what it says.
func StartsWith(e sqlb.Expr[string], prefix string) sqlb.Predicate {
	return pred(constructText, func(a *sqlb.Args) string {
		return fnStartsWith + "(" + e.SQL(a) + ", " + a.Next(prefix) + ")"
	})
}

// Replace renders replace(e, from, to), every occurrence. It is plain text,
// not a pattern; RegexpReplace is the pattern form.
func Replace(e sqlb.Expr[string], from, to string) sqlb.Expr[string] {
	return fn[string](constructText, fnReplace, e, sqlb.Param(from), sqlb.Param(to))
}

// SplitPart renders split_part(e, sep, n), the nth field, 1-based. Returns
// the empty string when n is past the end, not NULL.
func SplitPart(e sqlb.Expr[string], sep string, n int) sqlb.Expr[string] {
	return fn[string](constructText, fnSplitPart, e, sqlb.Param(sep), sqlb.Param(n))
}

// ConcatWs renders concat_ws(sep, parts...), joining with a separator and
// skipping NULL parts rather than propagating them. That skipping is the
// reason to prefer it over the || operator for optional fields.
func ConcatWs(sep string, parts ...sqlb.SQLer) sqlb.Expr[string] {
	return fn[string](constructText, fnConcatWs, append([]sqlb.SQLer{sqlb.Param(sep)}, parts...)...)
}

// Repeat renders repeat(e, n).
func Repeat(e sqlb.Expr[string], n int) sqlb.Expr[string] {
	return fn[string](constructText, fnRepeat, e, sqlb.Param(n))
}

// MD5 renders md5(e), returning hex text. For a checksum or a cache key
// only: it is not a password hash, and pg/crypto has the ones that are.
func MD5(e sqlb.Expr[string]) sqlb.Expr[string] { return fn[string](constructText, fnMD5, e) }

// SHA256Hex renders encode(sha256(e::bytea), 'hex').
func SHA256Hex(e sqlb.Expr[string]) sqlb.Expr[string] {
	return expr[string](constructText, func(a *sqlb.Args) string {
		return fnEncode + "(" + fnSHA256 + "(" + e.SQL(a) + "::bytea), 'hex')"
	})
}

// RegexpReplace renders regexp_replace(e, pattern, replacement, flags).
func RegexpReplace(e sqlb.Expr[string], pattern, replacement, flags string) sqlb.Expr[string] {
	return fn[string](constructText, fnRegexpReplace, e, sqlb.Param(pattern), sqlb.Param(replacement), sqlb.Param(flags))
}

// RegexpMatch renders regexp_match(e, pattern): the capture groups, or NULL.
func RegexpMatch(e sqlb.Expr[string], pattern string) sqlb.Expr[[]string] {
	return fn[[]string](constructText, fnRegexpMatch, e, sqlb.Param(pattern))
}

// RegexpCount renders regexp_count(e, pattern) (PG15), the number of matches.
func RegexpCount(e sqlb.Expr[string], pattern string) sqlb.Expr[int32] {
	return fn[int32](constructText, fnRegexpCount, e, sqlb.Param(pattern))
}

// RegexpSplitToArray renders regexp_split_to_array(e, pattern).
func RegexpSplitToArray(e sqlb.Expr[string], pattern string) sqlb.Expr[[]string] {
	return fn[[]string](constructText, fnRegexpSplitA, e, sqlb.Param(pattern))
}

// RegexpSplitToTable renders regexp_split_to_table(e, pattern) AS alias (part).
func RegexpSplitToTable(e sqlb.Expr[string], pattern string, alias string) (sqlb.Source, sqlb.Expr[string]) {
	src := FuncSource(fn[string](constructText, fnRegexpSplitT, e, sqlb.Param(pattern)), alias, "part")
	return src, sqlb.Col[string](sqlb.Ref(src), "part")
}

// ToChar renders to_char(e, format) for any input type.
func ToChar(e sqlb.SQLer, format string) sqlb.Expr[string] {
	return fn[string](constructText, fnToChar, e, sqlb.Param(format))
}

// Format renders format(fmt, args...).
func Format(format string, args ...sqlb.SQLer) sqlb.Expr[string] {
	return fn[string](constructText, fnFormat, append([]sqlb.SQLer{sqlb.Param(format)}, args...)...)
}
