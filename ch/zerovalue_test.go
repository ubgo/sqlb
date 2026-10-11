package ch_test

import (
	"testing"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ch"
	"github.com/ubgo/sqlb/sqlbtest"
)

// TestZeroValues_AreRefused builds the zero value of every exported ch type
// and fails on a panic or a clean build; see sqlbtest.ZeroValues.
func TestZeroValues_AreRefused(t *testing.T) {
	sqlbtest.ZeroValues(t, sqlbtest.ZeroConfig{
		Dir:     ".",
		Dialect: ch.Dialect{},
		Probes: map[string]func() error{
			"Setting": func() error { return zSettings(ch.Setting{}) },
		},
		Controls: map[string]func() error{
			"Setting": func() error { return zSettings(ch.JoinUseNulls()) },
		},
	})
}

func zSettings(s ...ch.Setting) error {
	t := sqlb.Table{Name: "z", Alias: "z"}
	_, err := sqlb.NewSelect(ch.Dialect{}).Columns(sqlb.Bind(sqlb.Col[int64](t, "id"), new(int64))).From(t).Clause(ch.Settings(s...)).Build()
	return err
}

// TestSettings_NoneIsRefused pins the other empty case: SETTINGS with nothing
// after it.
func TestSettings_NoneIsRefused(t *testing.T) {
	if err := zSettings(); err == nil {
		t.Fatal("SETTINGS with no settings built")
	}
}
