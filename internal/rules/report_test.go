package rules_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/rvenutolo/github-repos-audit/internal/rules"
)

func TestRepoReport_Cell(t *testing.T) {
	t.Parallel()

	stored := rules.Cell{Check: rules.CheckLicense, Verdict: rules.Pass, Value: "MIT"}
	report := &rules.RepoReport{Cells: map[rules.Check]rules.Cell{rules.CheckLicense: stored}}

	if diff := cmp.Diff(stored, report.Cell(rules.CheckLicense)); diff != "" {
		t.Errorf("Cell(license) mismatch (-want +got):\n%s", diff)
	}
	// An absent check is the zero Cell: the report never invents a verdict,
	// and the completeness of Cells is asserted elsewhere.
	if diff := cmp.Diff(rules.Cell{}, report.Cell(rules.CheckREADME)); diff != "" {
		t.Errorf("Cell(readme) mismatch (-want +got):\n%s", diff)
	}
}
