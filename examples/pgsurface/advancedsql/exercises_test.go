package advancedsql_test

import (
	"time"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/pg"
)

// Section 4. Practice Complex SQL Queries.

// 4.1 every row of users.
func Example_s4_1_allUsers() {
	show(sel().Columns(sqlb.Bind(UserID, &sink.i32), sqlb.Bind(UserName, &sink.s), sqlb.Bind(UserEmail, &sink.s2)).From(Users).Build())
	// Output:
	// SELECT "u"."user_id", "u"."user_name", "u"."email" FROM "users" AS "u"
}

// 4.1 duplicates through GROUP BY and HAVING.
func Example_s4_1_duplicatesGroupBy() {
	show(sel().Columns(
		sqlb.Bind(sqlb.Max(UserID), &sink.i32), sqlb.Bind(UserName, &sink.s), sqlb.Bind(UserEmail, &sink.s2),
	).From(Users).GroupBy(UserName, UserEmail).
		Having(sqlb.Greater(sqlb.CountOf(UserName), 1), sqlb.Greater(sqlb.CountOf(UserEmail), 1)).Build())
	// Output:
	// SELECT max("u"."user_id"), "u"."user_name", "u"."email" FROM "users" AS "u" GROUP BY "u"."user_name", "u"."email" HAVING (count("u"."user_name") > $1 AND count("u"."email") > $2)
	// args: [1 1]
}

// 4.1 duplicates through row_number in a CTE.
func Example_s4_1_duplicatesRowNumber() {
	w := sqlb.NewWindow().PartitionBy(UserName, UserEmail).OrderBy(sqlb.OrderBy(UserID, sqlb.SortAsc))
	body := sel().Exprs(UserID, UserName, UserEmail, sqlb.As(sqlb.RowNumber().Over(w), "rn")).From(Users).Order(sqlb.OrderBy(UserID, sqlb.SortAsc))
	dups := sqlb.Table{Name: "rn_dups", Alias: "rn_dups"}
	name := sqlb.Col[string](dups, "user_name")
	email := sqlb.Col[string](dups, "email")
	rn := sqlb.Col[int64](dups, "rn")
	show(sel().With("rn_dups", body).Columns(
		sqlb.Bind(name, &sink.s), sqlb.Bind(email, &sink.s2), sqlb.Bind(sqlb.As(sqlb.Max(rn), "nr_of_dups"), &sink.i64),
	).From(dups).Where(sqlb.Greater(rn, 1)).GroupBy(name, email).Build())
	// Output:
	// WITH "rn_dups" AS (SELECT "u"."user_id", "u"."user_name", "u"."email", row_number() OVER (PARTITION BY "u"."user_name", "u"."email" ORDER BY "u"."user_id" ASC) AS "rn" FROM "users" AS "u" ORDER BY "u"."user_id" ASC) SELECT "rn_dups"."user_name", "rn_dups"."email", max("rn_dups"."rn") AS "nr_of_dups" FROM "rn_dups" AS "rn_dups" WHERE ("rn_dups"."rn" > $1) GROUP BY "rn_dups"."user_name", "rn_dups"."email"
	// args: [1]
}

// 4.2 the second row by emp_id.
func Example_s4_2_secondRow() {
	body := sel().Exprs(EmpID, EmpName, DeptName, Salary, sqlb.As(sqlb.RowNumber().Over(sqlb.NewWindow().OrderBy(sqlb.OrderBy(EmpID, sqlb.SortAsc))), "rn")).From(Employee)
	rnT := sqlb.Table{Name: "rn", Alias: "rn"}
	rn := sqlb.Col[int64](rnT, "rn")
	show(sel().With("rn", body).Columns(
		sqlb.Bind(sqlb.Col[string](rnT, "emp_name"), &sink.s), sqlb.Bind(rn, &sink.i64),
	).From(rnT).Where(sqlb.Eq(rn, 2)).Build())
	// Output:
	// WITH "rn" AS (SELECT "e"."emp_id", "e"."emp_name", "e"."dept_name", "e"."salary", row_number() OVER (ORDER BY "e"."emp_id" ASC) AS "rn" FROM "employee" AS "e") SELECT "rn"."emp_name", "rn"."rn" FROM "rn" AS "rn" WHERE ("rn"."rn" = $1)
	// args: [2]
}

// 4.3 highest or lowest salary per department, first through two CTEs and
// a comma join. Postgres names max(salary) "max", so the CTE column is "max".
func Example_s4_3_highLowCTEs() {
	mas := sqlb.Table{Name: "max_salary", Alias: "mas"}
	mis := sqlb.Table{Name: "min_salary", Alias: "mis"}
	masDept, masMax := sqlb.Col[string](mas, "dept_name"), sqlb.Col[int32](mas, "max")
	misDept, misMin := sqlb.Col[string](mis, "dept_name"), sqlb.Col[int32](mis, "min")
	desc := sqlb.Case[string]().When(sqlb.EqExpr(Salary, masMax), sqlb.Param("Highest Salary")).Else(sqlb.Param("Lowest Salary"))
	show(sel().
		With("max_salary", sel().Exprs(DeptName, sqlb.Max(Salary)).From(Employee).GroupBy(DeptName)).
		With("min_salary", sel().Exprs(DeptName, sqlb.Min(Salary)).From(Employee).GroupBy(DeptName)).
		Columns(sqlb.Bindings(EmpBindings(&sink.emp), []sqlb.Binding{sqlb.Bind(sqlb.As(desc, "description"), &sink.s)})...).
		From(Employee).
		Join(sqlb.Join{Type: sqlb.JoinCross, Table: mas}, sqlb.Join{Type: sqlb.JoinCross, Table: mis}).
		Where(sqlb.Or(
			sqlb.And(sqlb.EqExpr(DeptName, masDept), sqlb.EqExpr(Salary, masMax)),
			sqlb.And(sqlb.EqExpr(DeptName, misDept), sqlb.EqExpr(Salary, misMin)),
		)).
		Order(sqlb.OrderBy(DeptName, sqlb.SortAsc), sqlb.OrderBy(Salary, sqlb.SortDesc)).Build())
	// Output:
	// WITH "max_salary" AS (SELECT "e"."dept_name", max("e"."salary") FROM "employee" AS "e" GROUP BY "e"."dept_name"), "min_salary" AS (SELECT "e"."dept_name", min("e"."salary") FROM "employee" AS "e" GROUP BY "e"."dept_name") SELECT "e"."emp_id", "e"."emp_name", "e"."dept_name", "e"."salary", CASE WHEN "e"."salary" = "mas"."max" THEN $1 ELSE $2 END AS "description" FROM "employee" AS "e" CROSS JOIN "max_salary" AS "mas" CROSS JOIN "min_salary" AS "mis" WHERE ((("e"."dept_name" = "mas"."dept_name" AND "e"."salary" = "mas"."max") OR ("e"."dept_name" = "mis"."dept_name" AND "e"."salary" = "mis"."min"))) ORDER BY "e"."dept_name" ASC, "e"."salary" DESC
	// args: [Highest Salary Lowest Salary]
}

// 4.3 the same through two ranks and a first_value delta. first_value over
// a partition with at least one row is never null, which is the reason for
// AssertNotNull.
func Example_s4_3_highLowRanks() {
	asc := sqlb.NewWindow().PartitionBy(DeptName).OrderBy(sqlb.OrderBy(Salary, sqlb.SortAsc))
	desc := sqlb.NewWindow().PartitionBy(DeptName).OrderBy(sqlb.OrderBy(Salary, sqlb.SortDesc))
	body := sel().Exprs(EmpID, EmpName, DeptName, Salary, sqlb.As(sqlb.Rank().Over(asc), "ls"), sqlb.As(sqlb.Rank().Over(desc), "hs")).From(Employee)
	lh := sqlb.Table{Name: "lh", Alias: "lh"}
	lhDept, lhSalary := sqlb.Col[string](lh, "dept_name"), sqlb.Col[int32](lh, "salary")
	ls, hs := sqlb.Col[int64](lh, "ls"), sqlb.Col[int64](lh, "hs")
	lhAsc := sqlb.NewWindow().PartitionBy(lhDept).OrderBy(sqlb.OrderBy(lhSalary, sqlb.SortAsc))
	lhDesc := sqlb.NewWindow().PartitionBy(lhDept).OrderBy(sqlb.OrderBy(lhSalary, sqlb.SortDesc))
	lowHigh := sqlb.Case[string]().When(sqlb.Eq(ls, 1), sqlb.Param("Lowest Salary in Dept")).Else(sqlb.Param("Highest Salary in Dept"))
	delta := sqlb.Sub(
		sqlb.AssertNotNull(sqlb.FirstValue(lhSalary).Over(lhDesc)),
		sqlb.AssertNotNull(sqlb.FirstValue(lhSalary).Over(lhAsc)),
	)
	show(sel().With("lh", body).Columns(
		sqlb.Bind(sqlb.Col[string](lh, "emp_name"), &sink.s), sqlb.Bind(lhDept, &sink.s2), sqlb.Bind(lhSalary, &sink.i32),
		sqlb.Bind(sqlb.As(lowHigh, "low_high"), &sink.s),
		sqlb.Bind(sqlb.As(delta, "delta"), &sink.i32),
	).From(lh).Where(sqlb.Or(sqlb.Eq(ls, 1), sqlb.Eq(hs, 1))).Build())
	// Output:
	// WITH "lh" AS (SELECT "e"."emp_id", "e"."emp_name", "e"."dept_name", "e"."salary", rank() OVER (PARTITION BY "e"."dept_name" ORDER BY "e"."salary" ASC) AS "ls", rank() OVER (PARTITION BY "e"."dept_name" ORDER BY "e"."salary" DESC) AS "hs" FROM "employee" AS "e") SELECT "lh"."emp_name", "lh"."dept_name", "lh"."salary", CASE WHEN "lh"."ls" = $1 THEN $2 ELSE $3 END AS "low_high", (first_value("lh"."salary") OVER (PARTITION BY "lh"."dept_name" ORDER BY "lh"."salary" DESC) - first_value("lh"."salary") OVER (PARTITION BY "lh"."dept_name" ORDER BY "lh"."salary" ASC)) AS "delta" FROM "lh" AS "lh" WHERE (("lh"."ls" = $4 OR "lh"."hs" = $5))
	// args: [1 Lowest Salary in Dept Highest Salary in Dept 1 1]
}

// 4.4 doctors in the same hospital with a different speciality: a self join.
func Example_s4_4_selfJoin() {
	show(sel().Columns(
		sqlb.Bind(DocID, &sink.i32), sqlb.Bind(DocName, &sink.s), sqlb.Bind(DocSpec, &sink.s2),
		sqlb.Bind(DocHosp, &sink.s), sqlb.Bind(DocCity, &sink.s2), sqlb.Bind(DocFee, &sink.i32),
	).From(Doctors).Join(sqlb.Join{Type: sqlb.JoinInner, Table: DoctorsR, On: []sqlb.Predicate{
		sqlb.EqExpr(DocHosp, DocRHosp),
		sqlb.NotEqExpr(DocSpec, DocRSpec),
	}}).Build())
	// Output:
	// SELECT "l"."id", "l"."name", "l"."speciality", "l"."hospital", "l"."city", "l"."consultation_fee" FROM "doctors" AS "l" INNER JOIN "doctors" AS "r" ON ("l"."hospital" = "r"."hospital" AND "l"."speciality" <> "r"."speciality")
}

// 4.5 three consecutive logins through two lags in a named window.
func Example_s4_5_consecutiveLags() {
	w := sqlb.NewWindow().OrderBy(sqlb.OrderBy(LoginDate, sqlb.SortAsc), sqlb.OrderBy(LoginID, sqlb.SortAsc))
	body := sel().Exprs(LoginID, LoginUser, LoginDate,
		sqlb.As(pg.Lag(LoginUser, 1).OverNamed("w"), "prev_user1"),
		sqlb.As(pg.Lag(LoginUser, 2).OverNamed("w"), "prev_user2"),
	).From(Logins).Window("w", w)
	l := sqlb.Table{Name: "l", Alias: "l"}
	user := sqlb.Col[string](l, "user_name")
	prev1, prev2 := sqlb.Col[*string](l, "prev_user1"), sqlb.Col[*string](l, "prev_user2")
	show(sel().With("l", body).Columns(
		sqlb.Bind(sqlb.Col[int32](l, "login_id"), &sink.i32), sqlb.Bind(user, &sink.s),
		sqlb.Bind(prev1, &sink.sp), sqlb.Bind(prev2, &sink.sp2),
	).From(l).Where(sqlb.EqExpr(sqlb.Nullable(user), prev1), sqlb.EqExpr(prev1, prev2)).Build())
	// Output:
	// WITH "l" AS (SELECT "ld"."login_id", "ld"."user_name", "ld"."login_date", lag("ld"."user_name", $1) OVER "w" AS "prev_user1", lag("ld"."user_name", $2) OVER "w" AS "prev_user2" FROM "login_details" AS "ld" WINDOW "w" AS (ORDER BY "ld"."login_date" ASC, "ld"."login_id" ASC)) SELECT "l"."login_id", "l"."user_name", "l"."prev_user1", "l"."prev_user2" FROM "l" AS "l" WHERE ("l"."user_name" = "l"."prev_user1" AND "l"."prev_user1" = "l"."prev_user2")
	// args: [1 2]
}

// 4.5 repeat flags: a CASE over lag, then a CASE over the lag of that flag.
func Example_s4_5_repeatFlags() {
	w := sqlb.NewWindow().OrderBy(sqlb.OrderBy(LoginDate, sqlb.SortAsc))
	rep := sqlb.Case[string]().
		When(sqlb.EqExpr(pg.Lag(LoginUser, 1).Over(w), sqlb.Nullable(LoginUser)), sqlb.Param("repeat")).
		Else(sqlb.Param("not repeat"))
	repT := sqlb.Table{Name: "rep", Alias: "rep"}
	repCol := sqlb.Col[string](repT, "rep")
	repDate := sqlb.Col[time.Time](repT, "login_date")
	w2 := sqlb.NewWindow().OrderBy(sqlb.OrderBy(repDate, sqlb.SortAsc))
	cume := sqlb.Case[int32]().
		When(sqlb.And(sqlb.Eq(repCol, "repeat"), sqlb.EqExpr(pg.Lag(repCol, 1).Over(w2), sqlb.Nullable(repCol))), sqlb.Param[int32](1)).
		Else(sqlb.Param[int32](0))
	cr := sqlb.Table{Name: "cume_rep", Alias: "cume_rep"}
	cumeCol := sqlb.Col[int32](cr, "cume_rep")
	show(sel().
		With("rep", sel().Exprs(LoginID, LoginUser, LoginDate, sqlb.As(rep, "rep")).From(Logins)).
		With("cume_rep", sel().Exprs(sqlb.Col[int32](repT, "login_id"), sqlb.Col[string](repT, "user_name"), repDate, repCol, sqlb.As(cume, "cume_rep")).From(repT)).
		Columns(sqlb.Bind(sqlb.Col[string](cr, "user_name"), &sink.s), sqlb.Bind(cumeCol, &sink.i32)).
		From(cr).Where(sqlb.Eq(cumeCol, 1)).Build())
	// Output:
	// WITH "rep" AS (SELECT "ld"."login_id", "ld"."user_name", "ld"."login_date", CASE WHEN lag("ld"."user_name", $1) OVER (ORDER BY "ld"."login_date" ASC) = "ld"."user_name" THEN $2 ELSE $3 END AS "rep" FROM "login_details" AS "ld"), "cume_rep" AS (SELECT "rep"."login_id", "rep"."user_name", "rep"."login_date", "rep"."rep", CASE WHEN ("rep"."rep" = $4 AND lag("rep"."rep", $5) OVER (ORDER BY "rep"."login_date" ASC) = "rep"."rep") THEN $6 ELSE $7 END AS "cume_rep" FROM "rep" AS "rep") SELECT "cume_rep"."user_name", "cume_rep"."cume_rep" FROM "cume_rep" AS "cume_rep" WHERE ("cume_rep"."cume_rep" = $8)
	// args: [1 repeat not repeat repeat 1 1 0 1]
}

// 4.5 islands: head of each run, the running max as island id, and the run
// length. The README's CASE without ELSE is written with ELSE NULL so both
// branches keep one Go type, *int32; the SQL meaning is identical.
func islandsCTEs(s *sqlb.Select[sqlb.PostgresDialect]) (*sqlb.Select[sqlb.PostgresDialect], sqlb.Table) {
	byDate := sqlb.NewWindow().OrderBy(sqlb.OrderBy(LoginDate, sqlb.SortAsc))
	// lag is null only on the first row, where the comparison is null and
	// the branch is not taken either way.
	rep := sqlb.Case[int32]().
		When(sqlb.EqExpr(sqlb.AssertNotNull(pg.Lag(LoginUser, 1).Over(byDate)), LoginUser), sqlb.Param[int32](1)).
		Else(sqlb.Param[int32](0))
	repT := sqlb.Table{Name: "rep", Alias: "rep"}
	repCol, repDate, repID := sqlb.Col[int32](repT, "rep"), sqlb.Col[time.Time](repT, "login_date"), sqlb.Col[int32](repT, "login_id")
	byRepDate := sqlb.NewWindow().OrderBy(sqlb.OrderBy(repDate, sqlb.SortAsc))
	head := sqlb.Case[int32]().
		When(sqlb.And(sqlb.Eq(repCol, 0), sqlb.Eq(sqlb.AssertNotNull(pg.Lead(repCol, 1).Over(byRepDate)), 1)), repID).
		End()
	ihT := sqlb.Table{Name: "island_head", Alias: "island_head"}
	ihRep, ihDate, ihHead := sqlb.Col[int32](ihT, "rep"), sqlb.Col[time.Time](ihT, "login_date"), sqlb.Col[*int32](ihT, "island_head")
	byHeadDate := sqlb.NewWindow().OrderBy(sqlb.OrderBy(ihDate, sqlb.SortAsc))
	islandID := sqlb.Case[*int32]().
		When(sqlb.Eq(ihRep, 1), sqlb.Over(sqlb.Max(ihHead), byHeadDate)).
		When(sqlb.Eq(ihRep, 0), ihHead).
		Else(sqlb.Literal[*int32]("NULL"))
	iiT := sqlb.Table{Name: "island_id", Alias: "island_id"}
	return s.
		With("rep", sel().Exprs(LoginID, LoginUser, LoginDate, sqlb.As(rep, "rep")).From(Logins)).
		With("island_head", sel().Exprs(repID, sqlb.Col[string](repT, "user_name"), repDate, repCol, sqlb.As(head, "island_head")).From(repT)).
		With("island_id", sel().Exprs(sqlb.Col[int32](ihT, "login_id"), sqlb.Col[string](ihT, "user_name"), ihDate, ihRep, ihHead, sqlb.As(islandID, "island_id")).From(ihT)), iiT
}

func Example_s4_5_islands() {
	s, ii := islandsCTEs(sel())
	iiID, iiDate, iiHead := sqlb.Col[*int32](ii, "island_id"), sqlb.Col[time.Time](ii, "login_date"), sqlb.Col[*int32](ii, "island_head")
	w := sqlb.NewWindow().PartitionBy(iiID).OrderBy(sqlb.OrderBy(iiDate, sqlb.SortAsc)).
		Frame(sqlb.FrameRows, sqlb.UnboundedPreceding(), sqlb.UnboundedFollowing())
	show(s.Columns(
		sqlb.Bind(sqlb.Col[string](ii, "user_name"), &sink.s), sqlb.Bind(iiID, &sink.i32p),
		sqlb.Bind(sqlb.Over(sqlb.CountOf(iiID), w), &sink.i64),
	).From(ii).Order(sqlb.OrderBy(iiDate, sqlb.SortAsc), sqlb.OrderBy(iiHead, sqlb.SortAsc)).Build())
	// Output:
	// WITH "rep" AS (SELECT "ld"."login_id", "ld"."user_name", "ld"."login_date", CASE WHEN lag("ld"."user_name", $1) OVER (ORDER BY "ld"."login_date" ASC) = "ld"."user_name" THEN $2 ELSE $3 END AS "rep" FROM "login_details" AS "ld"), "island_head" AS (SELECT "rep"."login_id", "rep"."user_name", "rep"."login_date", "rep"."rep", CASE WHEN ("rep"."rep" = $4 AND lead("rep"."rep", $5) OVER (ORDER BY "rep"."login_date" ASC) = $6) THEN "rep"."login_id" END AS "island_head" FROM "rep" AS "rep"), "island_id" AS (SELECT "island_head"."login_id", "island_head"."user_name", "island_head"."login_date", "island_head"."rep", "island_head"."island_head", CASE WHEN "island_head"."rep" = $7 THEN max("island_head"."island_head") OVER (ORDER BY "island_head"."login_date" ASC) WHEN "island_head"."rep" = $8 THEN "island_head"."island_head" ELSE NULL END AS "island_id" FROM "island_head" AS "island_head") SELECT "island_id"."user_name", "island_id"."island_id", count("island_id"."island_id") OVER (PARTITION BY "island_id"."island_id" ORDER BY "island_id"."login_date" ASC ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING) FROM "island_id" AS "island_id" ORDER BY "island_id"."login_date" ASC, "island_id"."island_head" ASC
	// args: [1 1 0 0 1 1 1 0]
}

// 4.5 the islands grouped, so each run is one row.
func Example_s4_5_islandsGrouped() {
	s, ii := islandsCTEs(sel())
	iiID, iiDate := sqlb.Col[*int32](ii, "island_id"), sqlb.Col[time.Time](ii, "login_date")
	w := sqlb.NewWindow().PartitionBy(iiID).OrderBy(sqlb.OrderBy(iiDate, sqlb.SortAsc)).
		Frame(sqlb.FrameRows, sqlb.UnboundedPreceding(), sqlb.UnboundedFollowing())
	island := sqlb.Table{Name: "island", Alias: "island"}
	isID, isCount, isUser := sqlb.Col[*int32](island, "island_id"), sqlb.Col[int64](island, "consecutive_logins"), sqlb.Col[string](island, "user_name")
	show(s.
		With("island", sel().Exprs(iiID, sqlb.Col[string](ii, "user_name"), sqlb.As(sqlb.Over(sqlb.CountOf(iiID), w), "consecutive_logins")).From(ii)).
		Columns(sqlb.Bind(isID, &sink.i32p), sqlb.Bind(isCount, &sink.i64), sqlb.Bind(isUser, &sink.s)).
		From(island).Where(sqlb.NotEq(isCount, 0)).GroupBy(isID, isCount, isUser).
		Order(sqlb.OrderBy(isID, sqlb.SortAsc)).Build())
	// Output:
	// WITH "rep" AS (SELECT "ld"."login_id", "ld"."user_name", "ld"."login_date", CASE WHEN lag("ld"."user_name", $1) OVER (ORDER BY "ld"."login_date" ASC) = "ld"."user_name" THEN $2 ELSE $3 END AS "rep" FROM "login_details" AS "ld"), "island_head" AS (SELECT "rep"."login_id", "rep"."user_name", "rep"."login_date", "rep"."rep", CASE WHEN ("rep"."rep" = $4 AND lead("rep"."rep", $5) OVER (ORDER BY "rep"."login_date" ASC) = $6) THEN "rep"."login_id" END AS "island_head" FROM "rep" AS "rep"), "island_id" AS (SELECT "island_head"."login_id", "island_head"."user_name", "island_head"."login_date", "island_head"."rep", "island_head"."island_head", CASE WHEN "island_head"."rep" = $7 THEN max("island_head"."island_head") OVER (ORDER BY "island_head"."login_date" ASC) WHEN "island_head"."rep" = $8 THEN "island_head"."island_head" ELSE NULL END AS "island_id" FROM "island_head" AS "island_head"), "island" AS (SELECT "island_id"."island_id", "island_id"."user_name", count("island_id"."island_id") OVER (PARTITION BY "island_id"."island_id" ORDER BY "island_id"."login_date" ASC ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING) AS "consecutive_logins" FROM "island_id" AS "island_id") SELECT "island"."island_id", "island"."consecutive_logins", "island"."user_name" FROM "island" AS "island" WHERE ("island"."consecutive_logins" <> $9) GROUP BY "island"."island_id", "island"."consecutive_logins", "island"."user_name" ORDER BY "island"."island_id" ASC
	// args: [1 1 0 0 1 1 1 0 0]
}

// 4.6 interchange adjacent names with lead and lag defaults over the whole table.
func Example_s4_6_interchange() {
	whole := sqlb.NewWindow()
	two := sqlb.Param[int32](2)
	swapped := sqlb.Case[string]().
		When(sqlb.Eq(sqlb.Mod(StudentID, two), 1), pg.LeadDefault(StudentName, 1, StudentName).Over(whole)).
		When(sqlb.Eq(sqlb.Mod(StudentID, two), 0), pg.LagDefault(StudentName, 1, StudentName).Over(whole)).
		End()
	show(sel().Columns(
		sqlb.Bind(StudentID, &sink.i32), sqlb.Bind(StudentName, &sink.s),
		sqlb.Bind(sqlb.As(swapped, "new_student_name"), &sink.sp),
	).From(Students).Build())
	// Output:
	// SELECT "st"."id", "st"."student_name", CASE WHEN ("st"."id" % $1) = $2 THEN lead("st"."student_name", $3, "st"."student_name") OVER () WHEN ("st"."id" % $4) = $5 THEN lag("st"."student_name", $6, "st"."student_name") OVER () END AS "new_student_name" FROM "students" AS "st"
	// args: [2 1 1 2 0 1]
}

// 4.7 three or more consecutive cold days: streak heads, island ids, island sizes.
func Example_s4_7_coldStreaks() {
	byID := sqlb.NewWindow().OrderBy(sqlb.OrderBy(WID, sqlb.SortAsc))
	// lag is null only on the first row, where the first branch is skipped
	// and the second branch covers a cold first day.
	streak := sqlb.Case[int32]().
		When(sqlb.And(sqlb.Less(WTemp, 0), sqlb.GreaterEq(sqlb.AssertNotNull(pg.Lag(WTemp, 1).Over(byID)), 0)), WID).
		When(sqlb.Less(WTemp, 0), sqlb.Param[int32](1)).
		End()
	st := sqlb.Table{Name: "streak", Alias: "streak"}
	stStreak, stID := sqlb.Col[*int32](st, "streak"), sqlb.Col[int32](st, "id")
	islandID := sqlb.Case[*int32]().
		When(sqlb.IsNotNull(stStreak), sqlb.Over(sqlb.Max(stStreak), sqlb.NewWindow().OrderBy(sqlb.OrderBy(stID, sqlb.SortAsc)))).
		Else(sqlb.Literal[*int32]("NULL"))
	ii := sqlb.Table{Name: "island_id", Alias: "island_id"}
	iiID, iiRowID := sqlb.Col[*int32](ii, "island_id"), sqlb.Col[int32](ii, "id")
	sizeW := sqlb.NewWindow().PartitionBy(iiID).OrderBy(sqlb.OrderBy(iiRowID, sqlb.SortAsc)).
		Frame(sqlb.FrameRows, sqlb.UnboundedPreceding(), sqlb.UnboundedFollowing())
	is := sqlb.Table{Name: "island_size", Alias: "island_size"}
	isSize := sqlb.Col[int64](is, "island_size")
	show(sel().
		With("streak", sel().Exprs(WID, WCity, WTemp, WDay, sqlb.As(streak, "streak")).From(Weather)).
		With("island_id", sel().Exprs(stID, sqlb.Col[string](st, "city"), sqlb.Col[int32](st, "temperature"), sqlb.Col[time.Time](st, "day"), sqlb.As(islandID, "island_id")).From(st)).
		With("island_size", sel().Exprs(iiRowID, sqlb.Col[string](ii, "city"), sqlb.Col[int32](ii, "temperature"), sqlb.Col[time.Time](ii, "day"), sqlb.As(sqlb.Over(sqlb.CountOf(iiID), sizeW), "island_size")).From(ii)).
		Columns(
			sqlb.Bind(sqlb.Col[int32](is, "id"), &sink.i32), sqlb.Bind(sqlb.Col[string](is, "city"), &sink.s),
			sqlb.Bind(sqlb.Col[int32](is, "temperature"), &sink.i32), sqlb.Bind(sqlb.Col[time.Time](is, "day"), &sink.t),
			sqlb.Bind(isSize, &sink.i64),
		).
		From(is).Where(sqlb.GreaterEq(isSize, 3)).Order(sqlb.OrderBy(sqlb.Col[int32](is, "id"), sqlb.SortAsc)).Build())
	// Output:
	// WITH "streak" AS (SELECT "w"."id", "w"."city", "w"."temperature", "w"."day", CASE WHEN ("w"."temperature" < $1 AND lag("w"."temperature", $2) OVER (ORDER BY "w"."id" ASC) >= $3) THEN "w"."id" WHEN "w"."temperature" < $4 THEN $5 END AS "streak" FROM "weather" AS "w"), "island_id" AS (SELECT "streak"."id", "streak"."city", "streak"."temperature", "streak"."day", CASE WHEN "streak"."streak" IS NOT NULL THEN max("streak"."streak") OVER (ORDER BY "streak"."id" ASC) ELSE NULL END AS "island_id" FROM "streak" AS "streak"), "island_size" AS (SELECT "island_id"."id", "island_id"."city", "island_id"."temperature", "island_id"."day", count("island_id"."island_id") OVER (PARTITION BY "island_id"."island_id" ORDER BY "island_id"."id" ASC ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING) AS "island_size" FROM "island_id" AS "island_id") SELECT "island_size"."id", "island_size"."city", "island_size"."temperature", "island_size"."day", "island_size"."island_size" FROM "island_size" AS "island_size" WHERE ("island_size"."island_size" >= $6) ORDER BY "island_size"."id" ASC
	// args: [0 1 0 0 1 3]
}

// 4.8 top two accounts per month by unique patients: DISTINCT, a count, a rank.
func Example_s4_8_topAccountsPerMonth() {
	dp := sqlb.Table{Name: "dp", Alias: "dp"}
	dpMonth, dpAccount := sqlb.Col[string](dp, "month"), sqlb.Col[int32](dp, "account_id")
	c := sqlb.Table{Name: "c", Alias: "c"}
	cMonth, cAccount, cCount := sqlb.Col[string](c, "month"), sqlb.Col[int32](c, "account_id"), sqlb.Col[int64](c, "c")
	r := sqlb.Table{Name: "r", Alias: "r"}
	rMonth, rRank := sqlb.Col[string](r, "month"), sqlb.Col[int64](r, "rnk")
	rankW := sqlb.NewWindow().PartitionBy(cMonth).OrderBy(sqlb.OrderBy(cCount, sqlb.SortDesc), sqlb.OrderBy(cAccount, sqlb.SortAsc))
	show(sel().
		With("dp", sel().Distinct().Exprs(sqlb.As(pg.ToChar(PLDate, "month"), "month"), PLAccount, PLPatient).From(PatientLogs).
			Order(sqlb.OrderBy(sqlb.OutputCol[string]("month"), sqlb.SortAsc))).
		With("c", sel().Exprs(dpMonth, dpAccount, sqlb.As(sqlb.CountOf(dpAccount), "c")).From(dp).GroupBy(dpMonth, dpAccount)).
		With("r", sel().Exprs(cMonth, cAccount, cCount, sqlb.As(sqlb.Rank().Over(rankW), "rnk")).From(c)).
		Columns(sqlb.Bind(rMonth, &sink.s), sqlb.Bind(sqlb.Col[int32](r, "account_id"), &sink.i32), sqlb.Bind(sqlb.Col[int64](r, "c"), &sink.i64), sqlb.Bind(rRank, &sink.i64)).
		From(r).Where(sqlb.Less(rRank, 3)).Order(sqlb.OrderBy(rMonth, sqlb.SortAsc), sqlb.OrderBy(rRank, sqlb.SortAsc)).Build())
	// Output:
	// WITH "dp" AS (SELECT DISTINCT to_char("pl"."date", $1) AS "month", "pl"."account_id", "pl"."patient_id" FROM "patient_logs" AS "pl" ORDER BY "month" ASC), "c" AS (SELECT "dp"."month", "dp"."account_id", count("dp"."account_id") AS "c" FROM "dp" AS "dp" GROUP BY "dp"."month", "dp"."account_id"), "r" AS (SELECT "c"."month", "c"."account_id", "c"."c", rank() OVER (PARTITION BY "c"."month" ORDER BY "c"."c" DESC, "c"."account_id" ASC) AS "rnk" FROM "c" AS "c") SELECT "r"."month", "r"."account_id", "r"."c", "r"."rnk" FROM "r" AS "r" WHERE ("r"."rnk" < $2) ORDER BY "r"."month" ASC, "r"."rnk" ASC
	// args: [month 3]
}
