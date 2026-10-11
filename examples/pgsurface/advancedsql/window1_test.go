package advancedsql_test

import (
	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/pg"
)

// Section 1. SQL Window Function Part 1.

// 1.1 max salary over the table.
func Example_s1_1_maxSalary() {
	show(sel().Columns(sqlb.Bind(sqlb.As(sqlb.Max(Salary), "max_salary"), &sink.i32)).From(Employee).Build())
	// Output:
	// SELECT max("e"."salary") AS "max_salary" FROM "employee" AS "e"
}

// 1.1 max salary per department with GROUP BY.
func Example_s1_1_maxPerDept() {
	show(sel().Columns(
		sqlb.Bind(DeptName, &sink.s),
		sqlb.Bind(sqlb.As(sqlb.Max(Salary), "max_salary_per_department"), &sink.i32),
	).From(Employee).GroupBy(DeptName).Build())
	// Output:
	// SELECT "e"."dept_name", max("e"."salary") AS "max_salary_per_department" FROM "employee" AS "e" GROUP BY "e"."dept_name"
}

// 1.1 the OVER clause with no parameter: a window of the whole table.
func Example_s1_1_maxOverAll() {
	show(sel().Columns(sqlb.Bindings(EmpBindings(&sink.emp), []sqlb.Binding{
		sqlb.Bind(sqlb.As(sqlb.Over(sqlb.Max(Salary), sqlb.NewWindow()), "max_salary"), &sink.i32),
	})...).From(Employee).Build())
	// Output:
	// SELECT "e"."emp_id", "e"."emp_name", "e"."dept_name", "e"."salary", max("e"."salary") OVER () AS "max_salary" FROM "employee" AS "e"
}

// 1.1 max salary per department beside every employee row.
func Example_s1_1_maxOverPartition() {
	w := sqlb.NewWindow().PartitionBy(DeptName)
	show(sel().Columns(sqlb.Bindings(EmpBindings(&sink.emp), []sqlb.Binding{
		sqlb.Bind(sqlb.As(sqlb.Over(sqlb.Max(Salary), w), "max_salary_per_dept"), &sink.i32),
	})...).From(Employee).Build())
	// Output:
	// SELECT "e"."emp_id", "e"."emp_name", "e"."dept_name", "e"."salary", max("e"."salary") OVER (PARTITION BY "e"."dept_name") AS "max_salary_per_dept" FROM "employee" AS "e"
}

// 1.2 row_number over the whole table.
func Example_s1_2_rowNumberAll() {
	show(sel().Columns(sqlb.Bindings(EmpBindings(&sink.emp), []sqlb.Binding{
		sqlb.Bind(sqlb.As(sqlb.RowNumber().Over(sqlb.NewWindow()), "rn"), &sink.i64),
	})...).From(Employee).Build())
	// Output:
	// SELECT "e"."emp_id", "e"."emp_name", "e"."dept_name", "e"."salary", row_number() OVER () AS "rn" FROM "employee" AS "e"
}

// 1.2 row_number per department. The README writes "over(partition by
// dept_name) over()", which is not valid SQL; this is the statement the
// prose describes.
func Example_s1_2_rowNumberPerDept() {
	show(sel().Columns(sqlb.Bindings(EmpBindings(&sink.emp), []sqlb.Binding{
		sqlb.Bind(sqlb.As(sqlb.RowNumber().Over(sqlb.NewWindow().PartitionBy(DeptName)), "rn"), &sink.i64),
	})...).From(Employee).Build())
	// Output:
	// SELECT "e"."emp_id", "e"."emp_name", "e"."dept_name", "e"."salary", row_number() OVER (PARTITION BY "e"."dept_name") AS "rn" FROM "employee" AS "e"
}

// 1.2 rank per department by salary, the Postgres manual's example.
func Example_s1_2_empsalaryRank() {
	w := sqlb.NewWindow().PartitionBy(ESDepname).OrderBy(sqlb.OrderBy(ESSalary, sqlb.SortDesc))
	show(sel().Columns(
		sqlb.Bind(ESDepname, &sink.s), sqlb.Bind(ESEmpno, &sink.i32), sqlb.Bind(ESSalary, &sink.i32),
		sqlb.Bind(sqlb.Rank().Over(w), &sink.i64),
	).From(EmpSalary).Build())
	// Output:
	// SELECT "es"."depname", "es"."empno", "es"."salary", rank() OVER (PARTITION BY "es"."depname" ORDER BY "es"."salary" DESC) FROM "empsalary" AS "es"
}

// 1.2 row_number ordered inside the partition.
func Example_s1_2_rowNumberOrdered() {
	w := sqlb.NewWindow().PartitionBy(DeptName).OrderBy(sqlb.OrderBy(EmpID, sqlb.SortAsc))
	show(sel().Columns(sqlb.Bindings(EmpBindings(&sink.emp), []sqlb.Binding{
		sqlb.Bind(sqlb.As(sqlb.RowNumber().Over(w), "rn"), &sink.i64),
	})...).From(Employee).Build())
	// Output:
	// SELECT "e"."emp_id", "e"."emp_name", "e"."dept_name", "e"."salary", row_number() OVER (PARTITION BY "e"."dept_name" ORDER BY "e"."emp_id" ASC) AS "rn" FROM "employee" AS "e"
}

// 1.2 the first two employees per department, through a derived table.
func Example_s1_2_firstTwoPerDept() {
	w := sqlb.NewWindow().PartitionBy(DeptName).OrderBy(sqlb.OrderBy(EmpID, sqlb.SortAsc))
	inner := sel().Exprs(EmpID, EmpName, DeptName, Salary, sqlb.As(sqlb.RowNumber().Over(w), "rn")).From(Employee)
	sq := sqlb.Derived(inner, "sq")
	show(sel().Columns(
		sqlb.Bind(cteCol[int32](sq, "emp_id"), &sink.i32),
		sqlb.Bind(cteCol[string](sq, "emp_name"), &sink.s),
		sqlb.Bind(cteCol[string](sq, "dept_name"), &sink.s2),
		sqlb.Bind(cteCol[int32](sq, "salary"), &sink.i32),
		sqlb.Bind(cteCol[int64](sq, "rn"), &sink.i64),
	).From(sq).Where(sqlb.Less(cteCol[int64](sq, "rn"), 3)).Build())
	// Output:
	// SELECT "sq"."emp_id", "sq"."emp_name", "sq"."dept_name", "sq"."salary", "sq"."rn" FROM (SELECT "e"."emp_id", "e"."emp_name", "e"."dept_name", "e"."salary", row_number() OVER (PARTITION BY "e"."dept_name" ORDER BY "e"."emp_id" ASC) AS "rn" FROM "employee" AS "e") AS "sq" WHERE ("sq"."rn" < $1)
	// args: [3]
}

// 1.3 rank by salary within department.
func Example_s1_3_rank() {
	w := sqlb.NewWindow().PartitionBy(DeptName).OrderBy(sqlb.OrderBy(Salary, sqlb.SortDesc))
	show(sel().Columns(sqlb.Bindings(EmpBindings(&sink.emp), []sqlb.Binding{
		sqlb.Bind(sqlb.As(sqlb.Rank().Over(w), "rnk"), &sink.i64),
	})...).From(Employee).Build())
	// Output:
	// SELECT "e"."emp_id", "e"."emp_name", "e"."dept_name", "e"."salary", rank() OVER (PARTITION BY "e"."dept_name" ORDER BY "e"."salary" DESC) AS "rnk" FROM "employee" AS "e"
}

// 1.3 top three salaries per department.
func Example_s1_3_topThree() {
	w := sqlb.NewWindow().PartitionBy(DeptName).OrderBy(sqlb.OrderBy(Salary, sqlb.SortDesc))
	inner := sel().Exprs(EmpID, EmpName, DeptName, Salary, sqlb.As(sqlb.Rank().Over(w), "rnk")).From(Employee)
	sq := sqlb.Derived(inner, "sq")
	rnk := cteCol[int64](sq, "rnk")
	show(sel().Columns(
		sqlb.Bind(cteCol[string](sq, "emp_name"), &sink.s),
		sqlb.Bind(rnk, &sink.i64),
	).From(sq).Where(sqlb.Less(rnk, 4)).Build())
	// Output:
	// SELECT "sq"."emp_name", "sq"."rnk" FROM (SELECT "e"."emp_id", "e"."emp_name", "e"."dept_name", "e"."salary", rank() OVER (PARTITION BY "e"."dept_name" ORDER BY "e"."salary" DESC) AS "rnk" FROM "employee" AS "e") AS "sq" WHERE ("sq"."rnk" < $1)
	// args: [4]
}

// 1.3 rank and dense_rank side by side.
func Example_s1_3_rankAndDenseRank() {
	w := sqlb.NewWindow().PartitionBy(DeptName).OrderBy(sqlb.OrderBy(Salary, sqlb.SortDesc))
	show(sel().Columns(sqlb.Bindings(EmpBindings(&sink.emp), []sqlb.Binding{
		sqlb.Bind(sqlb.As(sqlb.Rank().Over(w), "rnk"), &sink.i64),
		sqlb.Bind(sqlb.As(sqlb.DenseRank().Over(w), "dense_rnk"), &sink.i64),
	})...).From(Employee).Build())
	// Output:
	// SELECT "e"."emp_id", "e"."emp_name", "e"."dept_name", "e"."salary", rank() OVER (PARTITION BY "e"."dept_name" ORDER BY "e"."salary" DESC) AS "rnk", dense_rank() OVER (PARTITION BY "e"."dept_name" ORDER BY "e"."salary" DESC) AS "dense_rnk" FROM "employee" AS "e"
}

// 1.3 the same through a named window.
func Example_s1_3_namedWindow() {
	w := sqlb.NewWindow().PartitionBy(DeptName).OrderBy(sqlb.OrderBy(Salary, sqlb.SortDesc))
	show(sel().Columns(sqlb.Bindings(EmpBindings(&sink.emp), []sqlb.Binding{
		sqlb.Bind(sqlb.As(sqlb.Rank().OverNamed("w"), "rnk"), &sink.i64),
		sqlb.Bind(sqlb.As(sqlb.DenseRank().OverNamed("w"), "dense_rnk"), &sink.i64),
	})...).From(Employee).Window("w", w).Build())
	// Output:
	// SELECT "e"."emp_id", "e"."emp_name", "e"."dept_name", "e"."salary", rank() OVER "w" AS "rnk", dense_rank() OVER "w" AS "dense_rnk" FROM "employee" AS "e" WINDOW "w" AS (PARTITION BY "e"."dept_name" ORDER BY "e"."salary" DESC)
}

// 1.3 aggregates over a named window, from the Postgres manual.
func Example_s1_3_sumAvgOverNamed() {
	w := sqlb.NewWindow().PartitionBy(ESDepname).OrderBy(sqlb.OrderBy(ESSalary, sqlb.SortDesc))
	show(sel().Columns(
		sqlb.Bind(sqlb.OverNamed(sqlb.Sum(ESSalary), "w"), &sink.i32),
		sqlb.Bind(sqlb.OverNamed(sqlb.Avg(ESSalary), "w"), &sink.f64),
	).From(EmpSalary).Window("w", w).Build())
	// Output:
	// SELECT sum("es"."salary") OVER "w", avg("es"."salary") OVER "w" FROM "empsalary" AS "es" WINDOW "w" AS (PARTITION BY "es"."depname" ORDER BY "es"."salary" DESC)
}

// 1.4 lag: the previous employee's salary, nullable on the first row.
func Example_s1_4_lag() {
	w := sqlb.NewWindow().PartitionBy(DeptName).OrderBy(sqlb.OrderBy(EmpID, sqlb.SortAsc))
	show(sel().Columns(sqlb.Bindings(EmpBindings(&sink.emp), []sqlb.Binding{
		sqlb.Bind(sqlb.As(pg.Lag(Salary, 1).Over(w), "prev_emp_salary"), &sink.i32p),
	})...).From(Employee).Build())
	// Output:
	// SELECT "e"."emp_id", "e"."emp_name", "e"."dept_name", "e"."salary", lag("e"."salary", $1) OVER (PARTITION BY "e"."dept_name" ORDER BY "e"."emp_id" ASC) AS "prev_emp_salary" FROM "employee" AS "e"
	// args: [1]
}

// 1.4 lag by two rows with a default, so the result is not null.
func Example_s1_4_lagWithDefault() {
	w := sqlb.NewWindow().PartitionBy(DeptName).OrderBy(sqlb.OrderBy(EmpID, sqlb.SortAsc))
	show(sel().Columns(sqlb.Bindings(EmpBindings(&sink.emp), []sqlb.Binding{
		sqlb.Bind(sqlb.As(pg.LagDefault(Salary, 2, sqlb.Param[int32](0)).Over(w), "prev_emp_salary"), &sink.i32),
	})...).From(Employee).Build())
	// Output:
	// SELECT "e"."emp_id", "e"."emp_name", "e"."dept_name", "e"."salary", lag("e"."salary", $1, $2) OVER (PARTITION BY "e"."dept_name" ORDER BY "e"."emp_id" ASC) AS "prev_emp_salary" FROM "employee" AS "e"
	// args: [2 0]
}

// 1.4 lead.
func Example_s1_4_lead() {
	w := sqlb.NewWindow().PartitionBy(DeptName).OrderBy(sqlb.OrderBy(EmpID, sqlb.SortAsc))
	show(sel().Columns(sqlb.Bindings(EmpBindings(&sink.emp), []sqlb.Binding{
		sqlb.Bind(sqlb.As(pg.Lead(Salary, 1).Over(w), "next_emp_salary"), &sink.i32p),
	})...).From(Employee).Build())
	// Output:
	// SELECT "e"."emp_id", "e"."emp_name", "e"."dept_name", "e"."salary", lead("e"."salary", $1) OVER (PARTITION BY "e"."dept_name" ORDER BY "e"."emp_id" ASC) AS "next_emp_salary" FROM "employee" AS "e"
	// args: [1]
}

// 1.4 CASE over lag comparisons. The lag result is *int32, so the salary
// column is reinterpreted with Nullable for the comparison; the CASE has no
// ELSE, so its result is *string.
func Example_s1_4_lagCase() {
	w := sqlb.NewWindow().PartitionBy(DeptName).OrderBy(sqlb.OrderBy(EmpID, sqlb.SortAsc))
	prev := pg.Lag(Salary, 1).OverNamed("w")
	salRange := sqlb.Case[string]().
		When(sqlb.GreaterExpr(sqlb.Nullable(Salary), prev), sqlb.Param("Higher than previous employee")).
		When(sqlb.LessExpr(sqlb.Nullable(Salary), prev), sqlb.Param("Lower than previous employee")).
		When(sqlb.EqExpr(sqlb.Nullable(Salary), prev), sqlb.Param("Same as previous employee")).
		End()
	show(sel().Columns(sqlb.Bindings(EmpBindings(&sink.emp), []sqlb.Binding{
		sqlb.Bind(sqlb.As(prev, "prev_emp_salary"), &sink.i32p),
		sqlb.Bind(sqlb.As(salRange, "sal_range"), &sink.sp),
	})...).From(Employee).Window("w", w).Build())
	// Output:
	// SELECT "e"."emp_id", "e"."emp_name", "e"."dept_name", "e"."salary", lag("e"."salary", $1) OVER "w" AS "prev_emp_salary", CASE WHEN "e"."salary" > lag("e"."salary", $2) OVER "w" THEN $3 WHEN "e"."salary" < lag("e"."salary", $4) OVER "w" THEN $5 WHEN "e"."salary" = lag("e"."salary", $6) OVER "w" THEN $7 END AS "sal_range" FROM "employee" AS "e" WINDOW "w" AS (PARTITION BY "e"."dept_name" ORDER BY "e"."emp_id" ASC)
	// args: [1 1 Higher than previous employee 1 Lower than previous employee 1 Same as previous employee]
}

// 1.4 the payment variant with an IS NULL branch and an ELSE.
func Example_s1_4_paymentCase() {
	w := sqlb.NewWindow().PartitionBy(PayCustomer).OrderBy(sqlb.OrderBy(PayDate, sqlb.SortAsc))
	prev := pg.Lag(PayAmount, 1).OverNamed("w")
	amountRange := sqlb.Case[string]().
		When(sqlb.GreaterExpr(sqlb.Nullable(PayAmount), prev), sqlb.Param("Higher than previous amount")).
		When(sqlb.LessExpr(sqlb.Nullable(PayAmount), prev), sqlb.Param("Lower than previous amount")).
		When(sqlb.IsNull(prev), sqlb.Param("No info")).
		Else(sqlb.Param("The same as previous amount"))
	show(sel().Columns(
		sqlb.Bind(PayID, &sink.i32),
		sqlb.Bind(sqlb.As(PayAmount, "current_amount"), &sink.f64),
		sqlb.Bind(sqlb.As(prev, "prev_amount"), &sink.f64p),
		sqlb.Bind(sqlb.As(amountRange, "amount_range"), &sink.s),
	).From(Payment).Window("w", w).Build())
	// Output:
	// SELECT "p"."payment_id", "p"."amount" AS "current_amount", lag("p"."amount", $1) OVER "w" AS "prev_amount", CASE WHEN "p"."amount" > lag("p"."amount", $2) OVER "w" THEN $3 WHEN "p"."amount" < lag("p"."amount", $4) OVER "w" THEN $5 WHEN lag("p"."amount", $6) OVER "w" IS NULL THEN $7 ELSE $8 END AS "amount_range" FROM "payment" AS "p" WINDOW "w" AS (PARTITION BY "p"."customer_id" ORDER BY "p"."payment_date" ASC)
	// args: [1 1 Higher than previous amount 1 Lower than previous amount 1 No info The same as previous amount]
}
