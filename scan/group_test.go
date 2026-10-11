package scan_test

import (
	"fmt"
	"testing"

	"github.com/ubgo/sqlb/scan"
)

// The shapes a join produces: a parent repeated once per child, with the child
// columns nullable when the join is a LEFT JOIN.
type actorRow struct {
	ID   int64
	Name string
}
type filmRow struct {
	ID    *int64
	Title *string
}
type joinRow struct {
	Actor actorRow
	Film  filmRow
}

type actorWithFilms struct {
	Name  string
	Films []string
}

func rowsFor(t *testing.T) []joinRow {
	t.Helper()
	p := func(i int64) *int64 { return &i }
	s := func(x string) *string { return &x }
	return []joinRow{
		{actorRow{1, "Ada"}, filmRow{p(10), s("Alpha")}},
		{actorRow{1, "Ada"}, filmRow{p(11), s("Beta")}},
		{actorRow{2, "Bob"}, filmRow{nil, nil}}, // LEFT JOIN, no films
		{actorRow{3, "Cyd"}, filmRow{p(12), s("Gamma")}},
	}
}

// TestGroupBy_FoldsAJoin is the basic contract: one parent per key, holding
// every child row that belonged to it.
func TestGroupBy_FoldsAJoin(t *testing.T) {
	got := scan.GroupBy(rowsFor(t),
		func(r joinRow) int64 { return r.Actor.ID },
		func(first joinRow, g []joinRow) actorWithFilms {
			return actorWithFilms{first.Actor.Name, scan.CollectPtr(g, func(r joinRow) *string { return r.Film.Title })}
		})

	want := "[{Ada [Alpha Beta]} {Bob []} {Cyd [Gamma]}]"
	if fmt.Sprint(got) != want {
		t.Fatalf("got  %v\nwant %s", got, want)
	}
}

// TestGroupBy_PreservesFirstAppearanceOrder pins the property a map alone does
// not have.
//
// Grouping by map iteration would return a different order on every run, and
// the difference would surface as an intermittent failure in somebody else's
// test long after. A query with an ORDER BY must keep it.
func TestGroupBy_PreservesFirstAppearanceOrder(t *testing.T) {
	rows := []joinRow{
		{Actor: actorRow{ID: 9, Name: "i"}},
		{Actor: actorRow{ID: 3, Name: "ii"}},
		{Actor: actorRow{ID: 7, Name: "iii"}},
		{Actor: actorRow{ID: 3, Name: "ii"}},
	}
	for i := 0; i < 50; i++ {
		got := scan.GroupBy(rows,
			func(r joinRow) int64 { return r.Actor.ID },
			func(f joinRow, _ []joinRow) string { return f.Actor.Name })
		if fmt.Sprint(got) != "[i ii iii]" {
			t.Fatalf("run %d returned %v; order is not stable", i, got)
		}
	}
}

// TestGroupByPtr_DropsTheNullChildRow is the distinction that makes a LEFT
// JOIN mapping correct.
//
// A parent with no children still produces one row, with every child column
// NULL. Grouping that row under a zero key would give the parent one
// zero-valued child, which is the difference between "Bob has no films" and
// "Bob has one film with no name". That is the classic bug in hand-written
// join mapping, and it is silent.
func TestGroupByPtr_DropsTheNullChildRow(t *testing.T) {
	bob := []joinRow{{actorRow{2, "Bob"}, filmRow{nil, nil}}}

	films := scan.GroupByPtr(bob,
		func(r joinRow) *int64 { return r.Film.ID },
		func(f joinRow, _ []joinRow) string { return *f.Film.Title })

	if len(films) != 0 {
		t.Fatalf("got %v, want no films at all", films)
	}
}

// TestGroupBy_NestsTwoLevels pins that deeper nesting is this function applied
// again rather than a second function, which is what keeps the API one idea.
func TestGroupBy_NestsTwoLevels(t *testing.T) {
	type row struct {
		Actor actorRow
		Film  filmRow
		Genre *string
	}
	p := func(i int64) *int64 { return &i }
	s := func(x string) *string { return &x }

	rows := []row{
		{actorRow{1, "Ada"}, filmRow{p(10), s("Alpha")}, s("Drama")},
		{actorRow{1, "Ada"}, filmRow{p(10), s("Alpha")}, s("Comedy")},
		{actorRow{1, "Ada"}, filmRow{p(11), s("Beta")}, s("Drama")},
		{actorRow{2, "Bob"}, filmRow{nil, nil}, nil},
	}

	type filmOut struct {
		Title  string
		Genres []string
	}
	type actorOut struct {
		Name  string
		Films []filmOut
	}

	got := scan.GroupBy(rows,
		func(r row) int64 { return r.Actor.ID },
		func(first row, byActor []row) actorOut {
			films := scan.GroupByPtr(byActor,
				func(r row) *int64 { return r.Film.ID },
				func(f row, byFilm []row) filmOut {
					return filmOut{*f.Film.Title, scan.CollectPtr(byFilm, func(r row) *string { return r.Genre })}
				})
			return actorOut{first.Actor.Name, films}
		})

	want := "[{Ada [{Alpha [Drama Comedy]} {Beta [Drama]}]} {Bob []}]"
	if fmt.Sprint(got) != want {
		t.Fatalf("got  %v\nwant %s", got, want)
	}
}

// TestGroupBy_Empty pins that nothing in produces nothing out rather than a
// panic on group[0], which is the one index this code performs.
func TestGroupBy_Empty(t *testing.T) {
	got := scan.GroupBy(nil,
		func(r joinRow) int64 { return r.Actor.ID },
		func(f joinRow, _ []joinRow) string { return f.Actor.Name })
	if got != nil {
		t.Fatalf("got %v, want nil", got)
	}
}

// TestCollect covers the two helpers, which exist so a combine function is one
// line rather than a loop.
func TestCollect(t *testing.T) {
	rows := rowsFor(t)

	names := scan.Collect(rows, func(r joinRow) string { return r.Actor.Name })
	if fmt.Sprint(names) != "[Ada Ada Bob Cyd]" {
		t.Errorf("Collect = %v", names)
	}

	titles := scan.CollectPtr(rows, func(r joinRow) *string { return r.Film.Title })
	if fmt.Sprint(titles) != "[Alpha Beta Gamma]" {
		t.Errorf("CollectPtr should drop the nil and dereference the rest, got %v", titles)
	}
}
