// Command sqlbvet is the analyzers as a go vet tool, so they run inside the
// standard toolchain with no subcommand of their own:
//
//	go build -o sqlbvet github.com/ubgo/sqlb/contrib/vet/cmd/sqlbvet
//	go vet -vettool=./sqlbvet ./...
//
// It is the same analyzers cmd/sqlb runs; this binary exists for the places
// that already invoke go vet, such as a golangci-lint configuration or an
// editor. The schema diff and the escape-hatch listing need a flag or a
// connection that the vet protocol does not carry, and stay in cmd/sqlb.
package main

import (
	"golang.org/x/tools/go/analysis/unitchecker"

	"github.com/ubgo/sqlb/contrib/vet"
)

func main() { unitchecker.Main(vet.Analyzers()...) }
