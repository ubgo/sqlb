package pg

import (
	"slices"
	"strconv"
	"strings"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
)

// The kinds of type CREATE TYPE makes that are not an enum or a composite: a
// range type, a shell type and a base type.
//
// A range type is what a library declares when its rows hold an interval of
// its own scalar: it is one statement over an existing type, it needs no C and
// it gives every range operator this package already builds. It is typed here
// as a ddl.SQLType[Range[T]], so declaring the type and declaring the column
// that holds it are the same act, and a column of another Go type does not
// compile.
//
// A base type is the other end of the scale: it is how a type is added to
// Postgres rather than composed out of its types, and its input and output
// functions must take and return the type itself, which no built-in function
// does. It is nevertheless buildable and executable, because those functions
// need not be C in a shared object — a LANGUAGE internal function bound to an
// existing symbol is enough, which is what the execution test uses. A shell
// type is the forward declaration those functions need, since each names the
// type it does not yet exist without.
//
// Every value in all three is a literal, as everywhere in DDL: no engine takes
// a placeholder in CREATE TYPE.

const (
	kwAsRange              = "AS RANGE"
	optSubtype             = "SUBTYPE"
	optSubtypeOpClass      = "SUBTYPE_OPCLASS"
	optCollation           = "COLLATION"
	optCanonical           = "CANONICAL"
	optSubtypeDiff         = "SUBTYPE_DIFF"
	optMultirangeTypeName  = "MULTIRANGE_TYPE_NAME"
	optInput               = "INPUT"
	optOutput              = "OUTPUT"
	optReceive             = "RECEIVE"
	optSend                = "SEND"
	optTypmodIn            = "TYPMOD_IN"
	optTypmodOut           = "TYPMOD_OUT"
	optAnalyze             = "ANALYZE"
	optSubscript           = "SUBSCRIPT"
	optInternalLength      = "INTERNALLENGTH"
	optVariable            = "VARIABLE"
	optPassedByValue       = "PASSEDBYVALUE"
	optAlignment           = "ALIGNMENT"
	optStorage             = "STORAGE"
	optLikeType            = "LIKE"
	optCategory            = "CATEGORY"
	optPreferred           = "PREFERRED"
	optDefaultValue        = "DEFAULT"
	optElement             = "ELEMENT"
	optDelimiter           = "DELIMITER"
	optCollatable          = "COLLATABLE"
	kwCreateTypeStandalone = "CREATE TYPE"
)

// RangeType is a Postgres range type over the values of Go type T, declared
// once as a value. It is also the SQL type of a column holding one, so a
// Col[Range[T]] can be declared with it and a column of any other Go type
// cannot.
type RangeType[T any] struct{ Schema, Name string }

// Spelling is the type's qualified name, as a column definition writes it.
func (r RangeType[T]) Spelling(d sqlb.Dialect) (string, error) {
	if r.Name == "" {
		return "", ErrEmptyName
	}
	return qualified(d, r.Schema, r.Name), nil
}

// TypeOf types the range type to Range[T] for ddl.SQLType.
func (RangeType[T]) TypeOf(Range[T]) {}

func (r RangeType[T]) commentTarget(d sqlb.Dialect) (string, string, string, error) {
	return kwObjType, qualified(d, r.Schema, r.Name), r.Name, nil
}

func (RangeType[T]) movable() {}

// CreateRangeTypeStatement is CREATE TYPE ... AS RANGE.
type CreateRangeTypeStatement[T any] struct {
	r       RangeType[T]
	subtype ddl.SQLType[T]
	opts    []func(a *sqlb.Args) string
}

// CreateRangeType creates a range type over subtype, the SQL type of the
// values the range bounds. Postgres has no IF NOT EXISTS for CREATE TYPE, so
// wrap it in Guarded to make it safe to run twice.
//
// Postgres derives a multirange type beside it, named after this one unless
// MultirangeTypeName says otherwise, and works out the default operator class
// and collation from the subtype; a subtype with no default b-tree operator
// class is the one case SubtypeOpClass is required.
func CreateRangeType[T any](r RangeType[T], subtype ddl.SQLType[T]) *CreateRangeTypeStatement[T] {
	return &CreateRangeTypeStatement[T]{r: r, subtype: subtype}
}

// with derives a statement carrying one more option.
func (x *CreateRangeTypeStatement[T]) with(o func(a *sqlb.Args) string) *CreateRangeTypeStatement[T] {
	c := *x
	c.opts = append(slices.Clone(x.opts), o)
	return &c
}

// SubtypeOpClass names the b-tree operator class that orders the subtype,
// which is what decides which values a range contains. It is required for a
// subtype with no default class, and is how a range is ordered by something
// other than the subtype's usual ordering.
func (x *CreateRangeTypeStatement[T]) SubtypeOpClass(o OpClass[T]) *CreateRangeTypeStatement[T] {
	return x.with(func(a *sqlb.Args) string {
		if o.name == "" {
			a.Fail(ErrEmptyName)
			return ""
		}
		return optSubtypeOpClass + " = " + a.Dialect().QuoteIdent(o.name)
	})
}

// Collation names the collation the range's ordering uses, for a subtype
// whose ordering depends on one.
func (x *CreateRangeTypeStatement[T]) Collation(c Collation) *CreateRangeTypeStatement[T] {
	return x.with(func(a *sqlb.Args) string {
		if c.Name == "" {
			a.Fail(ErrEmptyName)
			return ""
		}
		return optCollation + " = " + qualified(a.Dialect(), c.Schema, c.Name)
	})
}

// Canonical names a function that puts every equal range in one form, which a
// range over a discrete type needs: without it [1,2] and [1,3) are two values
// the server cannot tell are the same. The function takes and returns the
// range type, so it can only be written after this statement has run, which is
// why it is usually added with a second pass rather than declared here.
func (x *CreateRangeTypeStatement[T]) Canonical(f Function) *CreateRangeTypeStatement[T] {
	return x.with(functionOption(optCanonical, f))
}

// SubtypeDiff names a function returning the distance between two subtype
// values as a float8, which is what lets the planner estimate selectivity over
// the range and what a GiST index uses to split a page well.
func (x *CreateRangeTypeStatement[T]) SubtypeDiff(f Function) *CreateRangeTypeStatement[T] {
	return x.with(functionOption(optSubtypeDiff, f))
}

// MultirangeTypeName names the multirange type Postgres creates beside this
// one, rather than letting it derive the name.
func (x *CreateRangeTypeStatement[T]) MultirangeTypeName(name string) *CreateRangeTypeStatement[T] {
	return x.with(func(a *sqlb.Args) string {
		if name == "" {
			a.Fail(ErrEmptyName)
			return ""
		}
		return optMultirangeTypeName + " = " + a.Dialect().QuoteIdent(name)
	})
}

// Build renders the statement.
func (x *CreateRangeTypeStatement[T]) Build() (ddl.Script, error) {
	if x.subtype == nil {
		return nil, ddl.ErrZeroValue
	}
	return buildOne(x.r.Name, true, func(a *sqlb.Args) string {
		d := a.Dialect()
		sp, err := x.subtype.Spelling(d)
		if err != nil {
			a.Fail(err)
			return ""
		}
		parts := append([]string{optSubtype + " = " + sp}, renderOptions(a, x.opts)...)
		return kwCreateType + " " + qualified(d, x.r.Schema, x.r.Name) + " " + kwAsRange + " (" + strings.Join(parts, ", ") + ")"
	})
}

// duplicateCondition is what a repeated CREATE TYPE raises.
func (*CreateRangeTypeStatement[T]) duplicateCondition() string { return condDuplicateObject }

// BaseType is a Postgres base type, a type implemented by functions rather
// than composed from other types, whose values a Go program sees as T. It is
// also the SQL type of a column holding one.
type BaseType[T any] struct{ Schema, Name string }

// Spelling is the type's qualified name, as a column definition writes it.
func (b BaseType[T]) Spelling(d sqlb.Dialect) (string, error) {
	if b.Name == "" {
		return "", ErrEmptyName
	}
	return qualified(d, b.Schema, b.Name), nil
}

// TypeOf types the base type to T for ddl.SQLType.
func (BaseType[T]) TypeOf(T) {}

func (b BaseType[T]) commentTarget(d sqlb.Dialect) (string, string, string, error) {
	return kwObjType, qualified(d, b.Schema, b.Name), b.Name, nil
}

func (BaseType[T]) movable() {}

// CreateShellTypeStatement is CREATE TYPE with nothing after the name.
type CreateShellTypeStatement[T any] struct{ b BaseType[T] }

// CreateShellType declares the name of a base type before the type itself
// exists. It is not optional and it is not a convenience: a base type's input
// function takes a cstring and returns the type, and its output function takes
// the type, so neither can be created until the name exists, and the name
// cannot be created with its functions because they do not exist yet. The
// shell breaks the cycle, and CreateBaseType then fills it in.
func CreateShellType[T any](b BaseType[T]) *CreateShellTypeStatement[T] {
	return &CreateShellTypeStatement[T]{b: b}
}

// Build renders the statement.
func (x *CreateShellTypeStatement[T]) Build() (ddl.Script, error) {
	return buildOne(x.b.Name, true, func(a *sqlb.Args) string {
		return kwCreateTypeStandalone + " " + qualified(a.Dialect(), x.b.Schema, x.b.Name)
	})
}

// duplicateCondition is what a repeated CREATE TYPE raises.
func (*CreateShellTypeStatement[T]) duplicateCondition() string { return condDuplicateObject }

// TypeAlignment is the storage alignment a base type's values need.
type TypeAlignment int

// The alignments Postgres allows for a base type. AlignInt4 is the default; a
// type wider than its alignment is stored unaligned and read slowly, and one
// narrower wastes the difference in every row.
const (
	AlignChar TypeAlignment = iota + 1
	AlignInt2
	AlignInt4
	AlignDouble
)

func (a TypeAlignment) keyword() (string, bool) {
	switch a {
	case AlignChar:
		return "char", true
	case AlignInt2:
		return "int2", true
	case AlignInt4:
		return "int4", true
	case AlignDouble:
		return "double", true
	}
	return "", false
}

// TypeStorage is how a base type's values are stored when they do not fit in a
// row.
type TypeStorage int

// The storage strategies. StoragePlain is the only one a fixed-length type may
// use; the others allow a variable-length value to be compressed, moved out of
// line, or both, which is what makes a wide type usable at all.
const (
	StoragePlain TypeStorage = iota + 1
	StorageExternal
	StorageExtended
	StorageMain
)

func (s TypeStorage) keyword() (string, bool) {
	switch s {
	case StoragePlain:
		return "plain", true
	case StorageExternal:
		return "external", true
	case StorageExtended:
		return "extended", true
	case StorageMain:
		return "main", true
	}
	return "", false
}

// TypeCategory is the class a base type belongs to, which decides which
// implicit casts the parser will consider in an ambiguous call. It is an open
// set of one-character codes: Postgres reserves the upper-case letters for
// itself and leaves the rest to a user-defined type.
type TypeCategory string

// The categories a user-defined base type is likely to claim. CategoryUser is
// the default and the safe answer; claiming a built-in category puts the type
// into the parser's resolution rules for that category, which changes how
// calls that were never about this type resolve.
const (
	CategoryUser      TypeCategory = "U"
	CategoryNumeric   TypeCategory = "N"
	CategoryString    TypeCategory = "S"
	CategoryDateTime  TypeCategory = "D"
	CategoryBoolean   TypeCategory = "B"
	CategoryGeometric TypeCategory = "G"
	CategoryNetwork   TypeCategory = "I"
	CategoryRange     TypeCategory = "R"
	CategoryTimespan  TypeCategory = "T"
)

// CreateBaseTypeStatement is the CREATE TYPE that fills in a shell type.
type CreateBaseTypeStatement[T any] struct {
	b             BaseType[T]
	input, output Function
	opts          []func(a *sqlb.Args) string
}

// CreateBaseType turns a shell type into a base type, with input the function
// that parses a value's text form and output the one that prints it. Both must
// already exist and both must name the shell type, which is why CreateShellType
// runs first.
//
// A base type is the one thing in this package that cannot be undone by
// declaration alone: dropping it takes everything that uses it, and getting
// the length, alignment or storage wrong corrupts every value stored under it
// rather than failing. It is built here because the statement is a statement
// like any other, not because a library should reach for one.
func CreateBaseType[T any](b BaseType[T], input, output Function) *CreateBaseTypeStatement[T] {
	return &CreateBaseTypeStatement[T]{b: b, input: input, output: output}
}

// with derives a statement carrying one more option.
func (x *CreateBaseTypeStatement[T]) with(o func(a *sqlb.Args) string) *CreateBaseTypeStatement[T] {
	c := *x
	c.opts = append(slices.Clone(x.opts), o)
	return &c
}

// Receive names the function that reads the type's binary form, without which
// the type cannot be sent over the binary protocol.
func (x *CreateBaseTypeStatement[T]) Receive(f Function) *CreateBaseTypeStatement[T] {
	return x.with(functionOption(optReceive, f))
}

// Send names the function that writes the type's binary form.
func (x *CreateBaseTypeStatement[T]) Send(f Function) *CreateBaseTypeStatement[T] {
	return x.with(functionOption(optSend, f))
}

// TypmodIn names the function that parses a type modifier, the (n) in a
// declaration like numeric(10,2).
func (x *CreateBaseTypeStatement[T]) TypmodIn(f Function) *CreateBaseTypeStatement[T] {
	return x.with(functionOption(optTypmodIn, f))
}

// TypmodOut names the function that prints a type modifier back.
func (x *CreateBaseTypeStatement[T]) TypmodOut(f Function) *CreateBaseTypeStatement[T] {
	return x.with(functionOption(optTypmodOut, f))
}

// Analyze names the function that gathers the planner's statistics for the
// type; without it the type has none and every estimate over it is a guess.
func (x *CreateBaseTypeStatement[T]) Analyze(f Function) *CreateBaseTypeStatement[T] {
	return x.with(functionOption(optAnalyze, f))
}

// Subscript names the function that implements value[i] for the type.
func (x *CreateBaseTypeStatement[T]) Subscript(f Function) *CreateBaseTypeStatement[T] {
	return x.with(functionOption(optSubscript, f))
}

// InternalLength is the fixed width of a value in bytes. It must match what
// the input function actually produces: a wrong length is not rejected, it
// reads the wrong bytes out of every row.
func (x *CreateBaseTypeStatement[T]) InternalLength(n int) *CreateBaseTypeStatement[T] {
	return x.with(func(*sqlb.Args) string { return optInternalLength + " = " + strconv.Itoa(n) })
}

// VariableLength declares the type's values variable width, the form whose
// first four bytes are the length.
func (x *CreateBaseTypeStatement[T]) VariableLength() *CreateBaseTypeStatement[T] {
	return x.with(func(*sqlb.Args) string { return optInternalLength + " = " + optVariable })
}

// PassedByValue passes a value in a register rather than by reference, which
// only a type no wider than a pointer may do.
func (x *CreateBaseTypeStatement[T]) PassedByValue() *CreateBaseTypeStatement[T] {
	return x.with(func(*sqlb.Args) string { return optPassedByValue })
}

// Alignment is the boundary a stored value starts on.
func (x *CreateBaseTypeStatement[T]) Alignment(al TypeAlignment) *CreateBaseTypeStatement[T] {
	return x.with(func(a *sqlb.Args) string {
		kw, ok := al.keyword()
		return optAlignment + " = " + keywordOf(a, kw, ok, "Alignment")
	})
}

// Storage is what the server may do with a value too wide for its row.
func (x *CreateBaseTypeStatement[T]) Storage(s TypeStorage) *CreateBaseTypeStatement[T] {
	return x.with(func(a *sqlb.Args) string {
		kw, ok := s.keyword()
		return optStorage + " = " + keywordOf(a, kw, ok, "Storage")
	})
}

// Like copies another type's length, alignment, storage and by-value
// properties, which is how a type that is an int4 underneath is declared
// without repeating what an int4 is.
func (x *CreateBaseTypeStatement[T]) Like(r TypeRef) *CreateBaseTypeStatement[T] {
	return x.with(func(a *sqlb.Args) string { return optLikeType + " = " + r.spelling(a) })
}

// Category puts the type in a class for the parser's cast resolution.
func (x *CreateBaseTypeStatement[T]) Category(c TypeCategory) *CreateBaseTypeStatement[T] {
	return x.with(func(a *sqlb.Args) string {
		if c == "" {
			a.Fail(ErrEmptyName)
			return ""
		}
		return optCategory + " = " + closedLiteral(a, c, c.valid(), "type category")
	})
}

// Preferred makes the type the one the parser casts towards within its
// category, which a user-defined type should almost never claim.
func (x *CreateBaseTypeStatement[T]) Preferred(on bool) *CreateBaseTypeStatement[T] {
	return x.with(func(*sqlb.Args) string { return optPreferred + " = " + boolOption(on) })
}

// Default is the value a column of the type holds where neither the insert nor
// the column definition gives one, written as a literal of T.
func (x *CreateBaseTypeStatement[T]) Default(v T) *CreateBaseTypeStatement[T] {
	return x.with(func(a *sqlb.Args) string { return optDefaultValue + " = " + a.Literal(v) })
}

// Element declares the type an array of r, so subscripting it is defined.
func (x *CreateBaseTypeStatement[T]) Element(r TypeRef) *CreateBaseTypeStatement[T] {
	return x.with(func(a *sqlb.Args) string { return optElement + " = " + r.spelling(a) })
}

// Delimiter is the character separating two values in the text form of an
// array of this type.
func (x *CreateBaseTypeStatement[T]) Delimiter(c string) *CreateBaseTypeStatement[T] {
	return x.with(func(a *sqlb.Args) string {
		if c == "" {
			a.Fail(ErrEmptyName)
			return ""
		}
		return optDelimiter + " = " + a.Literal(c)
	})
}

// Collatable allows a column of the type to carry a collation, which its own
// functions must then honour.
func (x *CreateBaseTypeStatement[T]) Collatable(on bool) *CreateBaseTypeStatement[T] {
	return x.with(func(*sqlb.Args) string { return optCollatable + " = " + boolOption(on) })
}

// Build renders the statement.
func (x *CreateBaseTypeStatement[T]) Build() (ddl.Script, error) {
	if x.input.Name == "" || x.output.Name == "" {
		return nil, ErrEmptyName
	}
	return buildOne(x.b.Name, true, func(a *sqlb.Args) string {
		d := a.Dialect()
		parts := append([]string{
			optInput + " = " + qualified(d, x.input.Schema, x.input.Name),
			optOutput + " = " + qualified(d, x.output.Schema, x.output.Name),
		}, renderOptions(a, x.opts)...)
		return kwCreateType + " " + qualified(d, x.b.Schema, x.b.Name) + " (" + strings.Join(parts, ", ") + ")"
	})
}

// duplicateCondition is what a repeated CREATE TYPE raises.
func (*CreateBaseTypeStatement[T]) duplicateCondition() string { return condDuplicateObject }

// functionOption writes one NAME = function option, naming the function bare
// of its arguments, which is the form CREATE TYPE's options take.
func functionOption(name string, f Function) func(a *sqlb.Args) string {
	return func(a *sqlb.Args) string {
		if f.Name == "" {
			a.Fail(ErrEmptyName)
			return ""
		}
		return name + " = " + qualified(a.Dialect(), f.Schema, f.Name)
	}
}

// renderOptions renders a statement's accumulated options in the order they
// were given.
func renderOptions(a *sqlb.Args, opts []func(a *sqlb.Args) string) []string {
	out := make([]string, len(opts))
	for i, o := range opts {
		out[i] = o(a)
	}
	return out
}
