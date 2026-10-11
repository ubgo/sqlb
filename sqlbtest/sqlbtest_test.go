package sqlbtest

import (
	"strings"
	"testing"
)

// The suite's own coverage comes from the driver integrations that run it;
// a conformance suite cannot execute without a database. What is checkable
// here is the shape of the case list, because a duplicate or empty name
// turns two subtests into one and a Skip entry then silences the wrong case.
func TestCases_AreWellFormed(t *testing.T) {
	cases := Cases()
	if len(cases) == 0 {
		t.Fatal("no cases")
	}
	seen := map[string]bool{}
	for i, c := range cases {
		if c.Name == "" {
			t.Errorf("case %d has no name", i)
		}
		if c.Why == "" {
			t.Errorf("case %q does not say what it proves", c.Name)
		}
		if c.run == nil {
			t.Errorf("case %q has no body", c.Name)
		}
		if seen[c.Name] {
			t.Errorf("case %q is declared twice; Skip could not address them separately", c.Name)
		}
		seen[c.Name] = true
	}
}

// TestTranscriptDiff pins the report crud_transcript fails with: every
// differing line by number, including a line one side lacks, and nothing at
// all when the two agree. A diff that reported only the first line, or that
// ignored a missing tail, would let an engine that stopped early pass.
func TestTranscriptDiff(t *testing.T) {
	for _, tc := range []struct {
		name      string
		got, want []string
		lines     []string
	}{
		{"identical", []string{"a", "b"}, []string{"a", "b"}, nil},
		{"one line differs", []string{"a", "x"}, []string{"a", "b"}, []string{"line 2:"}},
		{"every line differs", []string{"x", "y"}, []string{"a", "b"}, []string{"line 1:", "line 2:"}},
		{"got stops early", []string{"a"}, []string{"a", "b"}, []string{"line 2:"}},
		{"got runs long", []string{"a", "b", "c"}, []string{"a", "b"}, []string{"line 3:"}},
	} {
		d := transcriptDiff(tc.got, tc.want)
		if (d == "") != (len(tc.lines) == 0) {
			t.Errorf("%s: diff %q, want %d differing lines", tc.name, d, len(tc.lines))
		}
		for _, l := range tc.lines {
			if !strings.Contains(d, l) {
				t.Errorf("%s: diff does not name %q:\n%s", tc.name, l, d)
			}
		}
		if n := strings.Count(d, "line "); n != len(tc.lines) {
			t.Errorf("%s: diff names %d lines, want %d:\n%s", tc.name, n, len(tc.lines), d)
		}
	}
}
