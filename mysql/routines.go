package mysql

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
)

// The three schema objects MySQL keeps beside its tables that hold behaviour
// rather than data: the EVENT, a statement the server runs on a schedule, and
// the stored FUNCTION and PROCEDURE.
//
// All three were left out of this package until 2026-09-24 on the reading
// that a scheduled job belongs to the application's own scheduler and that a
// stored routine is a second language a library does not ship. Both readings
// are about whether a caller should use one, which is the caller's decision;
// neither is a reason this module cannot build the statement. What remains
// true is the language: a routine's body, and MySQL's procedural control flow
// generally, is not SQL this module can check, so Body takes it as a string
// the caller wrote, exactly as StepRaw does for a trigger, and says so.
//
// Two server-side conditions are worth knowing before either is used, because
// each makes a correctly built statement do nothing visible:
//
//   - An event never runs unless the event scheduler is on
//     (SET GLOBAL event_scheduler = ON); CREATE EVENT succeeds either way.
//   - A schedule's timestamp is read in the session's time zone, not in UTC.
//     The dialect writes a time.Time as UTC text, so a session on another
//     zone shifts it by the offset; set time_zone = '+00:00' on the
//     connection that installs the event, which contrib/driver-mysql's test
//     does.

const (
	kwCreateEvent      = "CREATE EVENT"
	kwAlterEvent       = "ALTER EVENT"
	kwDropEvent        = "DROP EVENT"
	kwOnSchedule       = "ON SCHEDULE"
	kwAt               = "AT"
	kwEvery            = "EVERY"
	kwStarts           = "STARTS"
	kwEnds             = "ENDS"
	kwOnCompletion     = "ON COMPLETION"
	kwPreserve         = "PRESERVE"
	kwNotPreserve      = "NOT PRESERVE"
	kwEnable           = "ENABLE"
	kwDisable          = "DISABLE"
	kwDisableOnReplica = "DISABLE ON REPLICA"
	kwEventComment     = "COMMENT"
	kwDo               = "DO"
	kwRenameToEvent    = "RENAME TO"
	kwCurrentTimestamp = "CURRENT_TIMESTAMP"
	kwInterval         = "INTERVAL"
	kwRoutineComment   = "COMMENT"

	kwCreateFunction  = "CREATE FUNCTION"
	kwCreateProcedure = "CREATE PROCEDURE"
	kwAlterFunction   = "ALTER FUNCTION"
	kwAlterProcedure  = "ALTER PROCEDURE"
	kwDropFunction    = "DROP FUNCTION"
	kwDropProcedure   = "DROP PROCEDURE"
	kwReturns         = "RETURNS"
	kwIn              = "IN"
	kwOut             = "OUT"
	kwInOut           = "INOUT"
)

// Event is a MySQL event, a statement the server runs on a schedule. Its name
// is unique per schema, and it is declared once so no statement types it
// twice.
type Event struct{ Name string }

// IntervalUnit is the unit of an event's interval. The set is MySQL's
// single-field units; the compound ones (DAY_HOUR and its siblings) are
// absent because each takes a two-part quantity like '1:30', which is a
// second value syntax for one construct.
type IntervalUnit string

// The interval units.
const (
	UnitSecond  IntervalUnit = "SECOND"
	UnitMinute  IntervalUnit = "MINUTE"
	UnitHour    IntervalUnit = "HOUR"
	UnitDay     IntervalUnit = "DAY"
	UnitWeek    IntervalUnit = "WEEK"
	UnitMonth   IntervalUnit = "MONTH"
	UnitQuarter IntervalUnit = "QUARTER"
	UnitYear    IntervalUnit = "YEAR"
)

func (u IntervalUnit) valid() bool {
	switch u {
	case UnitSecond, UnitMinute, UnitHour, UnitDay, UnitWeek, UnitMonth, UnitQuarter, UnitYear:
		return true
	}
	return false
}

// ErrBadSchedule is returned for a schedule outside what MySQL accepts: a
// zero Schedule, an interval unit outside the constants, or a quantity below
// one, which MySQL refuses with ER_EVENT_INTERVAL_NOT_POSITIVE.
var ErrBadSchedule = errors.New("sqlb/mysql: the event's schedule is incomplete or outside its named constants")

// scheduleKind distinguishes the two schedule shapes; the zero value is
// neither, so a Schedule{} literal fails Build.
type scheduleKind int

const (
	scheduleAt scheduleKind = iota + 1
	scheduleEvery
	scheduleAfter
)

// Schedule is when an event runs: once at a time, or every interval. It is a
// value, so a schedule declared once can be given to the CREATE EVENT and to
// a later ALTER EVENT that restores it.
type Schedule struct {
	kind       scheduleKind
	at         time.Time
	every      int64
	unit       IntervalUnit
	starts     time.Time
	ends       time.Time
	hasS, hasE bool
}

// EventAt schedules the event to run once, at t. MySQL drops such an event
// after it has run unless the event says ON COMPLETION PRESERVE, which is
// Completion's Preserve.
func EventAt(t time.Time) Schedule { return Schedule{kind: scheduleAt, at: t} }

// EventEvery schedules the event to run every n units, starting when it is
// created unless Starts says otherwise. A run that is still going when the
// next is due does not overlap: MySQL skips the due run.
func EventEvery(n int64, u IntervalUnit) Schedule {
	return Schedule{kind: scheduleEvery, every: n, unit: u}
}

// Starts moves a repeating event's first run, so an hourly job can be pinned
// to the top of the hour rather than to the minute it was installed.
func (s Schedule) Starts(t time.Time) Schedule { s.starts, s.hasS = t, true; return s }

// Ends stops a repeating event after t, after which MySQL drops it unless it
// says ON COMPLETION PRESERVE.
func (s Schedule) Ends(t time.Time) Schedule { s.ends, s.hasE = t, true; return s }

// write renders the schedule, failing a on anything outside the constants.
func (s Schedule) write(a *sqlb.Args) string {
	switch s.kind {
	case scheduleAt:
		return kwAt + " " + a.Literal(s.at)
	case scheduleAfter:
		if s.every < 1 || !s.unit.valid() {
			a.Fail(fmt.Errorf("%w: after %d %q", ErrBadSchedule, s.every, string(s.unit)))
			return ""
		}
		return kwAt + " " + kwCurrentTimestamp + " + " + kwInterval + " " + strconv.FormatInt(s.every, 10) + " " + string(s.unit)
	case scheduleEvery:
		if s.every < 1 || !s.unit.valid() {
			a.Fail(fmt.Errorf("%w: every %d %q", ErrBadSchedule, s.every, string(s.unit)))
			return ""
		}
		out := kwEvery + " " + strconv.FormatInt(s.every, 10) + " " + string(s.unit)
		if s.hasS {
			out += " " + kwStarts + " " + a.Literal(s.starts)
		}
		if s.hasE {
			out += " " + kwEnds + " " + a.Literal(s.ends)
		}
		return out
	}
	a.Fail(ErrBadSchedule)
	return ""
}

// EventAfter schedules the event to run once, n units from the moment the
// server reads the statement. It is MySQL's AT CURRENT_TIMESTAMP + INTERVAL
// form, and it is the one that needs no time zone: EventAt writes a
// timestamp, which MySQL reads in the session's zone, while this is relative
// to the server's own clock and cannot be shifted by an offset.
func EventAfter(n int64, u IntervalUnit) Schedule {
	return Schedule{kind: scheduleAfter, every: n, unit: u}
}

// Completion is what MySQL does with an event that has no run left: a
// one-shot event that has fired, or a repeating one past its ENDS.
type Completion int

// The completion behaviours.
const (
	// Preserve keeps the event in the schema, disabled. It is what a
	// migration that declares an event wants, because a dropped event is one
	// the next Install would create again.
	Preserve Completion = iota + 1
	// NotPreserve drops the event, which is MySQL's default.
	NotPreserve
)

func (c Completion) keyword() (string, bool) {
	switch c {
	case Preserve:
		return kwPreserve, true
	case NotPreserve:
		return kwNotPreserve, true
	}
	return "", false
}

// EventStatus is whether the scheduler runs the event.
type EventStatus int

// The statuses.
const (
	// EventEnable runs the event on its schedule, which is the default.
	EventEnable EventStatus = iota + 1
	// EventDisable keeps the event and does not run it: the way a job is
	// turned off without losing its definition.
	EventDisable
	// EventDisableOnReplica runs the event on the source and not on a
	// replica, where the replicated rows arrive anyway; running it on both
	// would apply the work twice.
	EventDisableOnReplica
)

func (s EventStatus) keyword() (string, bool) {
	switch s {
	case EventEnable:
		return kwEnable, true
	case EventDisable:
		return kwDisable, true
	case EventDisableOnReplica:
		return kwDisableOnReplica, true
	}
	return "", false
}

// eventCore is what a CREATE or ALTER EVENT carries; a nil pointer field is
// a part the statement does not write.
type eventCore struct {
	ev          Event
	ifNotExists bool
	schedule    *Schedule
	completion  Completion
	status      EventStatus
	comment     *string
	rename      *Event
	steps       []TriggerStep
}

func (c eventCore) derive() eventCore { c.steps = slices.Clone(c.steps); return c }

// writeCommon renders the parts CREATE EVENT and ALTER EVENT share, in
// MySQL's order; RENAME TO sits between ON COMPLETION and the status, which
// is why ALTER writes it rather than this.
func (c eventCore) writeStatus(a *sqlb.Args, b *strings.Builder) {
	if c.status == 0 {
		return
	}
	kw, ok := c.status.keyword()
	if !ok {
		a.Fail(fmt.Errorf("%w: event status", ErrUnknownKeyword))
		return
	}
	b.WriteString(" " + kw)
}

func (c eventCore) writeCompletion(a *sqlb.Args, b *strings.Builder) {
	if c.completion == 0 {
		return
	}
	kw, ok := c.completion.keyword()
	if !ok {
		a.Fail(fmt.Errorf("%w: event completion", ErrUnknownKeyword))
		return
	}
	b.WriteString(" " + kwOnCompletion + " " + kw)
}

// CreateEventQuery is CREATE EVENT with no schedule yet, which has no Build:
// ON SCHEDULE is not optional in MySQL's grammar, so the statement is
// incomplete until On has been called.
type CreateEventQuery struct{ core eventCore }

// CreateEvent starts an event.
func CreateEvent(e Event) *CreateEventQuery {
	return &CreateEventQuery{core: eventCore{ev: e}}
}

// IfNotExists makes creating an existing event a no-op.
func (x *CreateEventQuery) IfNotExists() *CreateEventQuery {
	c := x.core.derive()
	c.ifNotExists = true
	return &CreateEventQuery{core: c}
}

// On gives the schedule, which MySQL requires.
func (x *CreateEventQuery) On(s Schedule) *ScheduledEventQuery {
	c := x.core.derive()
	c.schedule = &s
	return &ScheduledEventQuery{core: c}
}

// ScheduledEventQuery is CREATE EVENT with its schedule and no body yet,
// which has no Build: DO is not optional either.
type ScheduledEventQuery struct{ core eventCore }

// OnCompletion says what happens to the event once it has no run left.
func (x *ScheduledEventQuery) OnCompletion(c Completion) *ScheduledEventQuery {
	n := x.core.derive()
	n.completion = c
	return &ScheduledEventQuery{core: n}
}

// Status enables or disables the event.
func (x *ScheduledEventQuery) Status(s EventStatus) *ScheduledEventQuery {
	n := x.core.derive()
	n.status = s
	return &ScheduledEventQuery{core: n}
}

// Comment attaches a comment, which SHOW EVENTS reports.
func (x *ScheduledEventQuery) Comment(text string) *ScheduledEventQuery {
	n := x.core.derive()
	n.comment = &text
	return &ScheduledEventQuery{core: n}
}

// Do gives the body and completes the event. One step is written as the body;
// several are written BEGIN s1; s2; END, exactly as a trigger's are, and no
// client DELIMITER is involved because database/sql sends the statement
// whole.
func (x *ScheduledEventQuery) Do(first TriggerStep, more ...TriggerStep) *CreateEventStatement {
	c := x.core.derive()
	c.steps = append(append(c.steps, first), more...)
	return &CreateEventStatement{core: c}
}

// CreateEventStatement is a complete CREATE EVENT.
type CreateEventStatement struct{ core eventCore }

// Build renders the statement.
func (x *CreateEventStatement) Build() (ddl.Script, error) {
	c := x.core
	if c.schedule == nil || len(c.steps) == 0 {
		return nil, ddl.ErrZeroValue
	}
	return buildOne(c.ev.Name, func(a *sqlb.Args) string {
		var b strings.Builder
		b.WriteString(kwCreateEvent + ifNotExists(c.ifNotExists) + " " + a.Dialect().QuoteIdent(c.ev.Name))
		b.WriteString(" " + kwOnSchedule + " " + c.schedule.write(a))
		c.writeCompletion(a, &b)
		c.writeStatus(a, &b)
		if c.comment != nil {
			b.WriteString(" " + kwEventComment + " " + a.Literal(*c.comment))
		}
		b.WriteString(" " + kwDo + " ")
		writeSteps(&b, a, c.steps)
		return b.String()
	})
}

// AlterEventQuery is ALTER EVENT with no change yet, which has no Build:
// MySQL refuses an ALTER EVENT naming no part to change, so the statement is
// incomplete until one of the methods below has been called.
type AlterEventQuery struct{ core eventCore }

// AlterEvent starts a change to an existing event. It is the statement that
// turns a job off (Status(EventDisable)) and back on without touching its
// definition, which is the reason it is worth having beside CreateEvent.
func AlterEvent(e Event) *AlterEventQuery {
	return &AlterEventQuery{core: eventCore{ev: e}}
}

// AlterEventStatement is an ALTER EVENT with at least one change.
type AlterEventStatement struct{ core eventCore }

// On replaces the schedule.
func (x *AlterEventQuery) On(s Schedule) *AlterEventStatement { return alterEventOn(x.core, s) }

// On replaces the schedule.
func (x *AlterEventStatement) On(s Schedule) *AlterEventStatement { return alterEventOn(x.core, s) }

func alterEventOn(core eventCore, s Schedule) *AlterEventStatement {
	c := core.derive()
	c.schedule = &s
	return &AlterEventStatement{core: c}
}

// OnCompletion replaces what happens once the event has no run left.
func (x *AlterEventQuery) OnCompletion(v Completion) *AlterEventStatement {
	return alterEventCompletion(x.core, v)
}

// OnCompletion replaces what happens once the event has no run left.
func (x *AlterEventStatement) OnCompletion(v Completion) *AlterEventStatement {
	return alterEventCompletion(x.core, v)
}

func alterEventCompletion(core eventCore, v Completion) *AlterEventStatement {
	c := core.derive()
	c.completion = v
	return &AlterEventStatement{core: c}
}

// Status enables or disables the event.
func (x *AlterEventQuery) Status(s EventStatus) *AlterEventStatement {
	return alterEventStatus(x.core, s)
}

// Status enables or disables the event.
func (x *AlterEventStatement) Status(s EventStatus) *AlterEventStatement {
	return alterEventStatus(x.core, s)
}

func alterEventStatus(core eventCore, s EventStatus) *AlterEventStatement {
	c := core.derive()
	c.status = s
	return &AlterEventStatement{core: c}
}

// Comment replaces the event's comment.
func (x *AlterEventQuery) Comment(text string) *AlterEventStatement {
	return alterEventComment(x.core, text)
}

// Comment replaces the event's comment.
func (x *AlterEventStatement) Comment(text string) *AlterEventStatement {
	return alterEventComment(x.core, text)
}

func alterEventComment(core eventCore, text string) *AlterEventStatement {
	c := core.derive()
	c.comment = &text
	return &AlterEventStatement{core: c}
}

// Rename renames the event, which is also how it is moved to another schema.
func (x *AlterEventQuery) Rename(to Event) *AlterEventStatement { return alterEventRename(x.core, to) }

// Rename renames the event.
func (x *AlterEventStatement) Rename(to Event) *AlterEventStatement {
	return alterEventRename(x.core, to)
}

func alterEventRename(core eventCore, to Event) *AlterEventStatement {
	c := core.derive()
	c.rename = &to
	return &AlterEventStatement{core: c}
}

// Do replaces the event's body.
func (x *AlterEventQuery) Do(first TriggerStep, more ...TriggerStep) *AlterEventStatement {
	return alterEventDo(x.core, first, more)
}

// Do replaces the event's body.
func (x *AlterEventStatement) Do(first TriggerStep, more ...TriggerStep) *AlterEventStatement {
	return alterEventDo(x.core, first, more)
}

func alterEventDo(core eventCore, first TriggerStep, more []TriggerStep) *AlterEventStatement {
	c := core.derive()
	c.steps = append(append(c.steps, first), more...)
	return &AlterEventStatement{core: c}
}

// Build renders the statement.
func (x *AlterEventStatement) Build() (ddl.Script, error) {
	c := x.core
	return buildOne(c.ev.Name, func(a *sqlb.Args) string {
		d := a.Dialect()
		var b strings.Builder
		b.WriteString(kwAlterEvent + " " + d.QuoteIdent(c.ev.Name))
		if c.schedule != nil {
			b.WriteString(" " + kwOnSchedule + " " + c.schedule.write(a))
		}
		c.writeCompletion(a, &b)
		if c.rename != nil {
			if c.rename.Name == "" {
				a.Fail(ErrEmptyName)
				return ""
			}
			b.WriteString(" " + kwRenameToEvent + " " + d.QuoteIdent(c.rename.Name))
		}
		c.writeStatus(a, &b)
		if c.comment != nil {
			b.WriteString(" " + kwEventComment + " " + a.Literal(*c.comment))
		}
		if len(c.steps) > 0 {
			b.WriteString(" " + kwDo + " ")
			writeSteps(&b, a, c.steps)
		}
		return b.String()
	})
}

// DropEvent drops an event.
func DropEvent(e Event) *DropStatement { return &DropStatement{kw: kwDropEvent, name: e.Name} }

// Function is a MySQL stored function, which returns a value and can be
// called in an expression. Its name is unique per schema, in a namespace
// separate from a Procedure's.
type Function struct{ Name string }

// Procedure is a MySQL stored procedure, which is invoked by CALL and may
// return result sets and OUT parameters.
type Procedure struct{ Name string }

// ParamMode is how a procedure's parameter passes its value. A function's
// parameters are always IN and MySQL refuses a mode on one.
type ParamMode int

// The parameter modes.
const (
	// ModeIn passes a value in, which is the default and the only mode a
	// function's parameter may have.
	ModeIn ParamMode = iota + 1
	// ModeOut passes a value back to the caller's variable.
	ModeOut
	// ModeInOut does both.
	ModeInOut
)

func (m ParamMode) keyword() (string, bool) {
	switch m {
	case ModeIn:
		return kwIn, true
	case ModeOut:
		return kwOut, true
	case ModeInOut:
		return kwInOut, true
	}
	return "", false
}

// Param is one parameter of a stored routine: a name, a SQL type and, for a
// procedure, a mode. The type is a ddl.SQLType, the same value a column
// declaration takes, so a parameter and the column it is written to are
// spelled from one declaration rather than twice.
type Param struct {
	name string
	mode ParamMode
	// spell is the SQLType's Spelling closed over its Go type, which is how a
	// Param holds a ddl.SQLType[T] without the type parameter escaping into
	// Param itself; a routine's parameters rarely share one Go type, so a
	// generic Param could not be put in a slice.
	spell func(d sqlb.Dialect) (string, error)
}

// RoutineParam makes a parameter of the Go type T, spelled by t.
func RoutineParam[T any](name string, t ddl.SQLType[T]) Param {
	return Param{name: name, spell: func(d sqlb.Dialect) (string, error) {
		if t == nil {
			return "", ddl.ErrZeroValue
		}
		return t.Spelling(d)
	}}
}

// Mode sets how the parameter passes its value. It is a procedure's only:
// MySQL's grammar has no mode on a function's parameter, so CreateFunction
// refuses one with ErrFunctionParamMode rather than rendering a parse error.
func (p Param) Mode(m ParamMode) Param { p.mode = m; return p }

// write renders the parameter, failing a on a zero one.
func (p Param) write(a *sqlb.Args) string {
	if p.name == "" || p.spell == nil {
		a.Fail(ddl.ErrZeroValue)
		return ""
	}
	spelling, err := p.spell(a.Dialect())
	if err != nil {
		a.Fail(err)
		return ""
	}
	out := ""
	if p.mode != 0 {
		kw, ok := p.mode.keyword()
		if !ok {
			a.Fail(fmt.Errorf("%w: parameter mode", ErrUnknownKeyword))
			return ""
		}
		out = kw + " "
	}
	return out + a.Dialect().QuoteIdent(p.name) + " " + spelling
}

// RoutineCharacteristic is one of the clauses MySQL calls a routine's
// characteristics. The set is closed and complete for 8.4.
type RoutineCharacteristic int

// The characteristics. Two matter beyond documentation. MySQL refuses to
// create a function at all, with ER_BINLOG_UNSAFE_ROUTINE, when binary
// logging is on and the function declares none of Deterministic, NoSQL or
// ReadsSQLData, unless log_bin_trust_function_creators is set — so a function
// meant to install anywhere declares one. And a routine declared
// Deterministic that is not lets the optimizer cache a call's result, which
// is a wrong answer rather than a slow one.
const (
	Deterministic RoutineCharacteristic = iota + 1
	NotDeterministic
	ContainsSQL
	NoSQL
	ReadsSQLData
	ModifiesSQLData
	SecurityDefiner
	SecurityInvoker
	LanguageSQL
)

// characteristics is the keyword of each, and whether ALTER accepts it.
// MySQL's ALTER FUNCTION and ALTER PROCEDURE take the comment, the language,
// the SQL-data clause and the security clause, and nothing else: a routine's
// determinism and its body are changed by dropping and recreating it.
var characteristics = map[RoutineCharacteristic]struct {
	kw       string
	alterOK  bool
	category int
}{
	Deterministic:    {"DETERMINISTIC", false, catDeterminism},
	NotDeterministic: {"NOT DETERMINISTIC", false, catDeterminism},
	ContainsSQL:      {"CONTAINS SQL", true, catSQLData},
	NoSQL:            {"NO SQL", true, catSQLData},
	ReadsSQLData:     {"READS SQL DATA", true, catSQLData},
	ModifiesSQLData:  {"MODIFIES SQL DATA", true, catSQLData},
	SecurityDefiner:  {"SQL SECURITY DEFINER", true, catSecurity},
	SecurityInvoker:  {"SQL SECURITY INVOKER", true, catSecurity},
	LanguageSQL:      {"LANGUAGE SQL", true, catLanguage},
}

// The categories a characteristic belongs to. MySQL takes at most one from
// each, and silently keeps the last of two from the same one, so a routine
// declared both NoSQL and ModifiesSQLData would be created as the second with
// no complaint at all.
const (
	catDeterminism = iota + 1
	catSQLData
	catSecurity
	catLanguage
)

// ErrConflictingCharacteristics is returned for two characteristics of one
// category, NoSQL and ReadsSQLData say, which MySQL resolves by keeping the
// last rather than by failing.
var ErrConflictingCharacteristics = errors.New("sqlb/mysql: two routine characteristics of one kind")

// ErrCharacteristicNotAlterable is returned for a characteristic in an ALTER
// that MySQL's ALTER FUNCTION and ALTER PROCEDURE do not take: determinism
// and the body are changed by dropping and recreating the routine.
var ErrCharacteristicNotAlterable = errors.New("sqlb/mysql: this routine characteristic cannot be altered")

// writeCharacteristics renders the list, refusing an unknown value, a
// repeated category and, when alterOnly, one ALTER does not take.
func writeCharacteristics(a *sqlb.Args, cs []RoutineCharacteristic, alterOnly bool) string {
	seen := map[int]bool{}
	parts := make([]string, 0, len(cs))
	for _, c := range cs {
		spec, ok := characteristics[c]
		if !ok {
			a.Fail(fmt.Errorf("%w: routine characteristic %d", ErrUnknownKeyword, int(c)))
			return ""
		}
		if alterOnly && !spec.alterOK {
			a.Fail(fmt.Errorf("%w: %s", ErrCharacteristicNotAlterable, spec.kw))
			return ""
		}
		if seen[spec.category] {
			a.Fail(fmt.Errorf("%w: %s", ErrConflictingCharacteristics, spec.kw))
			return ""
		}
		seen[spec.category] = true
		parts = append(parts, spec.kw)
	}
	return strings.Join(parts, " ")
}

// routineCore is what a CREATE FUNCTION or CREATE PROCEDURE carries.
type routineCore struct {
	kw, name    string
	ifNotExists bool
	params      []Param
	// returns is a function's RETURNS type, nil for a procedure.
	returns func(d sqlb.Dialect) (string, error)
	chars   []RoutineCharacteristic
	comment *string
	body    string
}

func (c routineCore) derive() routineCore {
	c.params = slices.Clone(c.params)
	c.chars = slices.Clone(c.chars)
	return c
}

// CreateRoutineQuery is a CREATE FUNCTION or CREATE PROCEDURE with no body
// yet, which has no Build: a routine without one is not a statement.
type CreateRoutineQuery struct{ core routineCore }

// CreateRoutineStatement is a complete CREATE FUNCTION or CREATE PROCEDURE.
type CreateRoutineStatement struct{ core routineCore }

// CreateProcedure starts a stored procedure.
func CreateProcedure(p Procedure) *CreateRoutineQuery {
	return &CreateRoutineQuery{core: routineCore{kw: kwCreateProcedure, name: p.Name}}
}

// CreateFunction starts a stored function returning the Go type T, spelled by
// returns. It is a function rather than a method taking the type because a
// method cannot introduce a type parameter, and T has to enter somewhere for
// the return type to be checked at all.
func CreateFunction[T any](f Function, returns ddl.SQLType[T]) *CreateRoutineQuery {
	return &CreateRoutineQuery{core: routineCore{kw: kwCreateFunction, name: f.Name, returns: func(d sqlb.Dialect) (string, error) {
		if returns == nil {
			return "", ddl.ErrZeroValue
		}
		return returns.Spelling(d)
	}}}
}

// IfNotExists makes creating an existing routine a no-op; MySQL 8.0.29 or
// later.
func (x *CreateRoutineQuery) IfNotExists() *CreateRoutineQuery {
	c := x.core.derive()
	c.ifNotExists = true
	return &CreateRoutineQuery{core: c}
}

// Param adds parameters, in the order they are declared.
func (x *CreateRoutineQuery) Param(first Param, more ...Param) *CreateRoutineQuery {
	c := x.core.derive()
	c.params = append(append(c.params, first), more...)
	return &CreateRoutineQuery{core: c}
}

// Characteristic adds characteristics; at most one of each kind.
func (x *CreateRoutineQuery) Characteristic(first RoutineCharacteristic, more ...RoutineCharacteristic) *CreateRoutineQuery {
	c := x.core.derive()
	c.chars = append(append(c.chars, first), more...)
	return &CreateRoutineQuery{core: c}
}

// Comment attaches a comment to the routine, which information_schema
// reports. It is a characteristic in MySQL's grammar like the others, and it
// is a method rather than a RoutineCharacteristic because it carries a value
// and that set is a plain enumeration.
func (x *CreateRoutineQuery) Comment(text string) *CreateRoutineQuery {
	c := x.core.derive()
	c.comment = &text
	return &CreateRoutineQuery{core: c}
}

// ErrFunctionParamMode is returned for a stored function whose parameter
// carries IN, OUT or INOUT. MySQL's grammar gives a mode to a procedure's
// parameter only — a function's is always IN and the keyword is a parse
// error, measured on 8.4. A rendering test had asserted the IN form for a
// week before anything ran it.
var ErrFunctionParamMode = errors.New("sqlb/mysql: a stored function's parameter takes no IN, OUT or INOUT")

// ErrEmptyBody is returned for a routine whose body is blank.
var ErrEmptyBody = errors.New("sqlb/mysql: the routine has no body")

// Body gives the routine's body and completes the statement.
//
// The body is written into the statement unchanged and nothing here parses or
// validates it, because it is not SQL: MySQL's stored-program language has
// its own declarations, handlers, cursors and control flow, and a builder for
// it would be a compiler for a language this module does not speak. Every
// other construct in this package renders text a dialect can check; a
// procedural block would look the same and check nothing, which is worse than
// an honest string. A value from outside the program must never reach it, for
// the same reason a raw SQL fragment must not.
//
// A single-statement body is written out in full, RETURN x for a function or
// one statement for a procedure; a multi-statement one is the caller's own
// BEGIN ... END. No client DELIMITER is involved, because database/sql sends
// the statement whole.
func (x *CreateRoutineQuery) Body(body string) *CreateRoutineStatement {
	c := x.core.derive()
	c.body = body
	return &CreateRoutineStatement{core: c}
}

// Build renders the statement.
func (x *CreateRoutineStatement) Build() (ddl.Script, error) {
	c := x.core
	if c.kw == "" {
		return nil, ddl.ErrZeroValue
	}
	if strings.TrimSpace(c.body) == "" {
		return nil, ErrEmptyBody
	}
	if c.returns != nil {
		for _, p := range c.params {
			if p.mode != 0 {
				return nil, fmt.Errorf("%w: %s", ErrFunctionParamMode, p.name)
			}
		}
	}
	return buildOne(c.name, func(a *sqlb.Args) string {
		var b strings.Builder
		b.WriteString(c.kw + ifNotExists(c.ifNotExists) + " " + a.Dialect().QuoteIdent(c.name) + " (")
		for i, p := range c.params {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(p.write(a))
		}
		b.WriteString(")")
		if c.returns != nil {
			spelling, err := c.returns(a.Dialect())
			if err != nil {
				a.Fail(err)
				return ""
			}
			b.WriteString(" " + kwReturns + " " + spelling)
		}
		if len(c.chars) > 0 {
			b.WriteString(" " + writeCharacteristics(a, c.chars, false))
		}
		if c.comment != nil {
			b.WriteString(" " + kwRoutineComment + " " + a.Literal(*c.comment))
		}
		b.WriteString(" " + c.body)
		return b.String()
	})
}

// alterRoutineCore is what an ALTER FUNCTION or ALTER PROCEDURE carries.
type alterRoutineCore struct {
	kw, name string
	chars    []RoutineCharacteristic
	comment  *string
}

// AlterRoutineQuery is an ALTER FUNCTION or ALTER PROCEDURE with no
// characteristic yet, which has no Build: MySQL refuses an ALTER naming
// nothing to change.
type AlterRoutineQuery struct{ core alterRoutineCore }

// AlterRoutineStatement is an ALTER FUNCTION or ALTER PROCEDURE with at least
// one characteristic.
type AlterRoutineStatement struct{ core alterRoutineCore }

// AlterFunction starts a change to a stored function's characteristics. Its
// body and its determinism are not among them; those are changed by dropping
// and recreating the function, and Characteristic refuses them with
// ErrCharacteristicNotAlterable rather than letting MySQL do it.
func AlterFunction(f Function) *AlterRoutineQuery {
	return &AlterRoutineQuery{core: alterRoutineCore{kw: kwAlterFunction, name: f.Name}}
}

// AlterProcedure starts a change to a stored procedure's characteristics.
func AlterProcedure(p Procedure) *AlterRoutineQuery {
	return &AlterRoutineQuery{core: alterRoutineCore{kw: kwAlterProcedure, name: p.Name}}
}

// Characteristic adds characteristics ALTER accepts, and so completes the
// statement.
func (x *AlterRoutineQuery) Characteristic(first RoutineCharacteristic, more ...RoutineCharacteristic) *AlterRoutineStatement {
	return alterRoutineWith(x.core, first, more)
}

// Characteristic adds further characteristics.
func (x *AlterRoutineStatement) Characteristic(first RoutineCharacteristic, more ...RoutineCharacteristic) *AlterRoutineStatement {
	return alterRoutineWith(x.core, first, more)
}

func alterRoutineWith(core alterRoutineCore, first RoutineCharacteristic, more []RoutineCharacteristic) *AlterRoutineStatement {
	c := core
	c.chars = append(append(slices.Clone(c.chars), first), more...)
	return &AlterRoutineStatement{core: c}
}

// Comment replaces the routine's comment, which MySQL's ALTER accepts.
func (x *AlterRoutineQuery) Comment(text string) *AlterRoutineStatement {
	return alterRoutineComment(x.core, text)
}

// Comment replaces the routine's comment.
func (x *AlterRoutineStatement) Comment(text string) *AlterRoutineStatement {
	return alterRoutineComment(x.core, text)
}

func alterRoutineComment(core alterRoutineCore, text string) *AlterRoutineStatement {
	c := core
	c.chars = slices.Clone(c.chars)
	c.comment = &text
	return &AlterRoutineStatement{core: c}
}

// Build renders the statement.
func (x *AlterRoutineStatement) Build() (ddl.Script, error) {
	c := x.core
	if c.kw == "" || (len(c.chars) == 0 && c.comment == nil) {
		return nil, ddl.ErrZeroValue
	}
	return buildOne(c.name, func(a *sqlb.Args) string {
		s := c.kw + " " + a.Dialect().QuoteIdent(c.name)
		if len(c.chars) > 0 {
			s += " " + writeCharacteristics(a, c.chars, true)
		}
		if c.comment != nil {
			s += " " + kwRoutineComment + " " + a.Literal(*c.comment)
		}
		return s
	})
}

// DropFunction drops a stored function.
func DropFunction(f Function) *DropStatement { return &DropStatement{kw: kwDropFunction, name: f.Name} }

// DropProcedure drops a stored procedure.
func DropProcedure(p Procedure) *DropStatement {
	return &DropStatement{kw: kwDropProcedure, name: p.Name}
}
