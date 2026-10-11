package advancedsql_test

import (
	"fmt"
	"time"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/pg"
)

var d = sqlb.PostgresDialect{}

func sel() *sqlb.Select[sqlb.PostgresDialect] { return sqlb.NewSelect(d) }

func show(q sqlb.Query, err error) {
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println(q.SQL)
	if len(q.Args) > 0 {
		fmt.Println("args:", q.Args)
	}
}

// Tables of the catalogue, with the columns its queries touch.

var (
	Employee = sqlb.Table{Name: "employee", Alias: "e"}
	EmpID    = sqlb.Col[int32](Employee, "emp_id")
	EmpName  = sqlb.Col[string](Employee, "emp_name")
	DeptName = sqlb.Col[string](Employee, "dept_name")
	Salary   = sqlb.Col[int32](Employee, "salary")

	EmpSalary = sqlb.Table{Name: "empsalary", Alias: "es"}
	ESDepname = sqlb.Col[string](EmpSalary, "depname")
	ESEmpno   = sqlb.Col[int32](EmpSalary, "empno")
	ESSalary  = sqlb.Col[int32](EmpSalary, "salary")

	Payment     = sqlb.Table{Name: "payment", Alias: "p"}
	PayID       = sqlb.Col[int32](Payment, "payment_id")
	PayCustomer = sqlb.Col[int32](Payment, "customer_id")
	PayRental   = sqlb.Col[int32](Payment, "rental_id")
	PayAmount   = sqlb.Col[float64](Payment, "amount")
	PayDate     = sqlb.Col[time.Time](Payment, "payment_date")

	Product   = sqlb.Table{Name: "product", Alias: "p"}
	ProdName  = sqlb.Col[string](Product, "product_name")
	ProdCat   = sqlb.Col[string](Product, "product_category")
	ProdPrice = sqlb.Col[float64](Product, "price")
	ProdBrand = sqlb.Col[string](Product, "brand")

	Sales      = sqlb.Table{Name: "sales", Alias: "s"}
	SalesStore = sqlb.Col[int32](Sales, "store_id")
	SalesCost  = sqlb.Col[float64](Sales, "cost")

	Users     = sqlb.Table{Name: "users", Alias: "u"}
	UserID    = sqlb.Col[int32](Users, "user_id")
	UserName  = sqlb.Col[string](Users, "user_name")
	UserEmail = sqlb.Col[string](Users, "email")

	Doctors  = sqlb.Table{Name: "doctors", Alias: "l"}
	DocID    = sqlb.Col[int32](Doctors, "id")
	DocName  = sqlb.Col[string](Doctors, "name")
	DocSpec  = sqlb.Col[string](Doctors, "speciality")
	DocHosp  = sqlb.Col[string](Doctors, "hospital")
	DocCity  = sqlb.Col[string](Doctors, "city")
	DocFee   = sqlb.Col[int32](Doctors, "consultation_fee")
	DoctorsR = sqlb.Table{Name: "doctors", Alias: "r"}
	DocRSpec = sqlb.Col[string](DoctorsR, "speciality")
	DocRHosp = sqlb.Col[string](DoctorsR, "hospital")

	Logins    = sqlb.Table{Name: "login_details", Alias: "ld"}
	LoginID   = sqlb.Col[int32](Logins, "login_id")
	LoginUser = sqlb.Col[string](Logins, "user_name")
	LoginDate = sqlb.Col[time.Time](Logins, "login_date")

	Students    = sqlb.Table{Name: "students", Alias: "st"}
	StudentID   = sqlb.Col[int32](Students, "id")
	StudentName = sqlb.Col[string](Students, "student_name")

	Weather = sqlb.Table{Name: "weather", Alias: "w"}
	WID     = sqlb.Col[int32](Weather, "id")
	WCity   = sqlb.Col[string](Weather, "city")
	WTemp   = sqlb.Col[int32](Weather, "temperature")
	WDay    = sqlb.Col[time.Time](Weather, "day")

	PatientLogs = sqlb.Table{Name: "patient_logs", Alias: "pl"}
	PLAccount   = sqlb.Col[int32](PatientLogs, "account_id")
	PLDate      = sqlb.Col[time.Time](PatientLogs, "date")
	PLPatient   = sqlb.Col[int32](PatientLogs, "patient_id")

	Events  = sqlb.Table{Name: "events", Alias: "e"}
	EvType  = sqlb.Col[string](Events, "event_type")
	EvValue = sqlb.Col[int32](Events, "value")
	EvTime  = sqlb.Col[time.Time](Events, "time")
)

// EmpBindings stands in for "select e.*".
func EmpBindings(dst *Emp) []sqlb.Binding {
	return []sqlb.Binding{
		sqlb.Bind(EmpID, &dst.ID), sqlb.Bind(EmpName, &dst.Name),
		sqlb.Bind(DeptName, &dst.Dept), sqlb.Bind(Salary, &dst.Salary),
	}
}

type Emp struct {
	ID     int32
	Name   string
	Dept   string
	Salary int32
}

// ProdBindings stands in for "select p.*".
func ProdBindings(dst *Prod) []sqlb.Binding {
	return []sqlb.Binding{
		sqlb.Bind(ProdName, &dst.Name), sqlb.Bind(ProdCat, &dst.Category),
		sqlb.Bind(ProdBrand, &dst.Brand), sqlb.Bind(ProdPrice, &dst.Price),
	}
}

type Prod struct {
	Name, Category, Brand string
	Price                 float64
}

// cteCols makes typed column references against a CTE or derived table.
func cteCol[T any](src sqlb.Source, name string) sqlb.Expr[T] {
	return sqlb.Col[T](sqlb.Ref(src), name)
}

var sink struct {
	s, s2   string
	sp, sp2 *string
	i32     int32
	i32p    *int32
	i64     int64
	f64     float64
	f64p    *float64
	t       time.Time
	emp     Emp
	prod    Prod
	iv      pg.Interval
}
