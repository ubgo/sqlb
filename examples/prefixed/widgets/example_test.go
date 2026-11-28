package widgets_test

import (
	"fmt"

	"github.com/ubgo/sqlb/examples/prefixed/widgets"
)

// Example_migrations prints the numbered migrations for the prefix an
// application chose, on both engines. The prefix reaches every table, key
// and index name; nothing else changes.
func Example_migrations() {
	t, err := widgets.NewTables("acme_")
	if err != nil {
		panic(err)
	}
	for _, e := range []widgets.Engine{widgets.Postgres, widgets.SQLite} {
		ms, err := t.Migrations(e)
		if err != nil {
			panic(err)
		}
		for _, m := range ms {
			fmt.Println("--", m.Name)
			for _, s := range m.Script {
				fmt.Println(s.SQL)
			}
		}
	}
	// Output:
	// -- 0001
	// CREATE TABLE IF NOT EXISTS "acme_accounts" ("id" BIGINT NOT NULL, "email" TEXT NOT NULL, "active" BOOLEAN NOT NULL DEFAULT true, "created_at" TIMESTAMPTZ NOT NULL, PRIMARY KEY ("id"))
	// CREATE UNIQUE INDEX IF NOT EXISTS "acme_accounts_email_key" ON "acme_accounts" ("email")
	// CREATE TABLE IF NOT EXISTS "acme_members" ("account_id" BIGINT NOT NULL, "team" TEXT NOT NULL, "role" TEXT NOT NULL DEFAULT 'member', PRIMARY KEY ("account_id", "team"), FOREIGN KEY ("account_id") REFERENCES "acme_accounts" ("id") ON DELETE CASCADE)
	// -- 0002
	// ALTER TABLE "acme_accounts" ADD COLUMN "birthday" DATE, ADD COLUMN "profile" JSONB NOT NULL DEFAULT ('{}'), ADD COLUMN "avatar" BYTEA
	// CREATE INDEX IF NOT EXISTS "acme_members_team_idx" ON "acme_members" ("team")
	// -- 0003
	// CREATE INDEX IF NOT EXISTS "acme_members_owner_idx" ON "acme_members" ("team") WHERE "role" = 'owner'
	// -- 0001
	// CREATE TABLE IF NOT EXISTS "acme_accounts" ("id" INTEGER NOT NULL, "email" TEXT NOT NULL, "active" INTEGER NOT NULL DEFAULT 1, "created_at" TIMESTAMP NOT NULL, PRIMARY KEY ("id"))
	// CREATE UNIQUE INDEX IF NOT EXISTS "acme_accounts_email_key" ON "acme_accounts" ("email")
	// CREATE TABLE IF NOT EXISTS "acme_members" ("account_id" INTEGER NOT NULL, "team" TEXT NOT NULL, "role" TEXT NOT NULL DEFAULT 'member', PRIMARY KEY ("account_id", "team"), FOREIGN KEY ("account_id") REFERENCES "acme_accounts" ("id") ON DELETE CASCADE)
	// -- 0002
	// ALTER TABLE "acme_accounts" ADD COLUMN "birthday" DATE
	// ALTER TABLE "acme_accounts" ADD COLUMN "profile" TEXT NOT NULL DEFAULT ('{}')
	// ALTER TABLE "acme_accounts" ADD COLUMN "avatar" BLOB
	// CREATE INDEX IF NOT EXISTS "acme_members_team_idx" ON "acme_members" ("team")
	// -- 0003
	// CREATE INDEX IF NOT EXISTS "acme_members_owner_idx" ON "acme_members" ("team") WHERE "role" = 'owner'
}

// Example_describe prints what the declaration describes, the input to the
// live-schema check, so a reader sees the shape both engines are held to.
func Example_describe() {
	t, err := widgets.NewTables("acme_")
	if err != nil {
		panic(err)
	}
	tables, err := t.Describe(widgets.Postgres)
	if err != nil {
		panic(err)
	}
	for _, tb := range tables {
		fmt.Println(tb.Name, "key", tb.PrimaryKey)
		for _, c := range tb.Columns {
			fmt.Printf("  %s %s nullable=%t default=%q\n", c.Name, c.Type, c.Nullable, c.Default)
		}
		for _, ix := range tb.Indexes {
			fmt.Printf("  index %s %v unique=%t", ix.Name, ix.Columns, ix.Unique)
			if ix.Where != "" {
				fmt.Printf(" where %s", ix.Where)
			}
			fmt.Println()
		}
		for _, fk := range tb.ForeignKeys {
			fmt.Printf("  references %s%v on delete %s\n", fk.RefTable, fk.RefColumns, fk.OnDelete)
		}
	}
	// Output:
	// acme_accounts key [id]
	//   id bigint nullable=false default=""
	//   email text nullable=false default=""
	//   active bool nullable=false default="true"
	//   created_at timestamp nullable=false default=""
	//   birthday date nullable=true default=""
	//   profile json nullable=false default="('{}')"
	//   avatar bytes nullable=true default=""
	//   index acme_accounts_email_key [email] unique=true
	// acme_members key [account_id team]
	//   account_id bigint nullable=false default=""
	//   team text nullable=false default=""
	//   role text nullable=false default="'member'"
	//   index acme_members_team_idx [team] unique=false
	//   index acme_members_owner_idx [team] unique=false where "role" = 'owner'
	//   references acme_accounts[id] on delete CASCADE
}
