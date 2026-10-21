package workflows_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
	"github.com/rvenutolo/github-repos-audit/internal/workflows"
)

// FuzzParse holds the invariants every input must keep, since the text is
// whatever a repository happens to contain: neither parser panics, and a
// result with ParseError carries nothing but Path and the error.
func FuzzParse(f *testing.F) {
	for _, seed := range []string{
		"", "on: push\n", "jobs: [", "- a\n",
		"jobs:\n  a:\n    steps:\n      - uses: a@b\n        with: {k: 1}\n      - run: nix flake update\n",
		"runs:\n  using: composite\n  steps:\n    - uses: a@b\n",
		"a: &x [*x]\n", "jobs: &j {a: *j}\n",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, text string) {
		w := workflows.ParseWorkflow("w.yml", text)
		if w.ParseError != "" {
			if diff := cmp.Diff(audit.Workflow{Path: "w.yml", ParseError: w.ParseError}, w); diff != "" {
				t.Errorf("ParseWorkflow() carries more than the error (-want +got):\n%s", diff)
			}
		}
		c := workflows.ParseComposite("action.yml", text)
		if c.ParseError != "" {
			if diff := cmp.Diff(audit.CompositeAction{Path: "action.yml", ParseError: c.ParseError}, c); diff != "" {
				t.Errorf("ParseComposite() carries more than the error (-want +got):\n%s", diff)
			}
		}
	})
}
