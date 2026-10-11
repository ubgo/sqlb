package vet

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// statementConstructor matches an exported function that starts a statement:
// pg.Select returns *SelectQuery, pg.CreateIndex *CreateIndexQuery,
// pg.CreateSchema *CreateSchemaStatement. A generic one, pg.AlterEnum[T], has
// its type parameters between the name and the arguments, which the first
// version of this pattern did not allow, so generic constructors escaped it;
// it also knew only the Query suffix, so the Statement constructors did.
var statementConstructor = regexp.MustCompile(`(?m)^func ([A-Z]\w*)(?:\[[^\]]*\])?\([^)]*\) \*\w+(?:Query|Ready|Statement)\b`)

// TestEntryConstructors_MatchTheEnginePackages pins that entryConstructors
// names every function an engine package offers for starting a statement.
//
// The engine packages are the entry packages since pgq and liteq merged into
// pg and sqlite, so chainEngine recognises a statement's root by name. A new
// constructor, a CREATE TABLE for instance, would otherwise go unrecognised
// and the Dialect analyzer would silently stop checking every statement it
// starts. The scan fails closed: a source tree in which it finds nothing is a
// broken matcher, not a clean result.
func TestEntryConstructors_MatchTheEnginePackages(t *testing.T) {
	found := map[string]bool{}
	for pkg := range entryPackages {
		dir := filepath.Join("..", "..", strings.TrimPrefix(pkg, Module+"/"))
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			for _, m := range statementConstructor.FindAllStringSubmatch(string(src), -1) {
				found[m[1]] = true
			}
		}
	}
	if len(found) == 0 {
		t.Fatal("found no statement constructor in any engine package; the matcher is broken")
	}
	var missing, stale []string
	for name := range found {
		if !entryConstructors[name] {
			missing = append(missing, name)
		}
	}
	for name := range entryConstructors {
		if !found[name] {
			stale = append(stale, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	if len(missing) > 0 {
		t.Errorf("engine packages start statements with %v, which entryConstructors does not name", missing)
	}
	if len(stale) > 0 {
		t.Errorf("entryConstructors names %v, which no engine package defines", stale)
	}
}
