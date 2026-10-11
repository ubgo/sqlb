package ddl

// The keywords and separators the universal DDL statements write. Each is
// accepted with the same meaning by Postgres and SQLite, executed, and by
// MySQL 8 and ClickHouse per their documentation; an engine's own keywords
// live in its package.
const (
	kwCreate      = "CREATE"
	kwTable       = "TABLE"
	kwIfNotExists = "IF NOT EXISTS"
	kwIfExists    = "IF EXISTS"
	kwDrop        = "DROP"
	kwAlter       = "ALTER"
	kwAddColumn   = "ADD COLUMN"
	kwDropColumn  = "DROP COLUMN"
	kwRename      = "RENAME COLUMN"
	kwTo          = "TO"
	kwNotNull     = "NOT NULL"
	kwDefault     = "DEFAULT"
	kwConstraint  = "CONSTRAINT"
	kwCheck       = "CHECK"

	sepSpace = " "
	sepComma = ", "
	parOpen  = "("
	parClose = ")"
)
