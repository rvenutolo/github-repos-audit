package rules_test

import (
	"testing"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
	"github.com/rvenutolo/github-repos-audit/internal/rules"
)

// TestEvaluate_ciWorkflow pins ci_workflow: .github/workflows/ci.yml, named
// exactly ci, runs on pull requests into the default branch, and no other
// workflow does, so a workflow keyed on `workflow_run: workflows: [ci]` sees
// every pull-request failure. The failing cell names the first reason.
func TestEvaluate_ciWorkflow(t *testing.T) {
	t.Parallel()

	const dir = ".github/workflows/"
	ci := audit.Workflow{Path: dir + "ci.yml", Name: "ci", Events: []string{"pull_request", "push"}}
	onPR := func(file string) audit.Workflow {
		return audit.Workflow{Path: dir + file, Events: []string{"pull_request"}}
	}
	with := func(ws ...audit.Workflow) audit.Repo {
		r := fixture("a", "tools")
		r.Workflows = ws
		return r
	}
	renamed := func(name string) audit.Workflow {
		w := ci
		w.Name = name
		return w
	}
	pushOnly := ci
	pushOnly.Events = []string{"push"}
	failureIssue := audit.Workflow{
		Path:                   dir + "failure-issue.yml",
		Events:                 []string{"pull_request_target", "workflow_run"},
		PullRequestTargetTypes: []string{"closed"},
	}
	otherBase := onPR("lint.yml")
	otherBase.PullRequestBranches = []string{"release"}
	prTarget := audit.Workflow{Path: dir + "pr.yml", Events: []string{"pull_request_target"}}
	yaml := ci
	yaml.Path = dir + "ci.yaml"
	content := with(ci)
	content.Type = "content"

	tests := []struct {
		name      string
		repo      audit.Repo
		want      rules.Verdict
		wantValue string
	}{
		{"ci alone passes", with(ci), rules.Pass, ""},
		{"the merge gate is not counted", with(ci, onPR("merge-gate.yml")), rules.Pass, ""},
		{"a workflow run only when a pull request closes is not counted", with(ci, failureIssue), rules.Pass, ""},
		{"a workflow for another base branch is not counted", with(ci, otherBase), rules.Pass, ""},
		{"a push-only workflow is not counted", with(ci, audit.Workflow{Path: dir + "release.yml", Events: []string{"push"}}), rules.Pass, ""},
		{"no workflows", with(), rules.Fail, "no ci.yml"},
		{"ci.yaml is not ci.yml", with(yaml), rules.Fail, "no ci.yml"},
		{"unreadable ci.yml", with(audit.Workflow{Path: dir + "ci.yml", ParseError: "x"}), rules.Fail, "unreadable ci.yml"},
		{"no name", with(renamed("")), rules.Fail, "no name:"},
		{"name is case-sensitive", with(renamed("CI")), rules.Fail, "name: CI"},
		{"ci.yml not on pull requests", with(pushOnly), rules.Fail, "not on PRs"},
		{"other pull-request workflows are listed sorted", with(ci, onPR("test.yml"), onPR("lint.yml")), rules.Fail, "also lint.yml, test.yml"},
		{"pull_request_target with default types counts", with(ci, prTarget), rules.Fail, "also pr.yml"},
		{"another unreadable workflow cannot be cleared", with(ci, audit.Workflow{Path: dir + "other.yml", ParseError: "x"}), rules.Fail, "unreadable other.yml"},
		{"ci.yml's own problem comes first", with(renamed("CI"), onPR("lint.yml")), rules.Fail, "name: CI"},
		{"not required is n/a", content, rules.NA, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cells, err := evaluateOne(tt.repo)
			if err != nil {
				t.Fatalf("Evaluate() error = %v, want nil", err)
			}
			got := cells[rules.CheckCIWorkflow]
			if got.Verdict != tt.want || got.Value != tt.wantValue {
				t.Errorf("cell = %v %q; want %v %q", got.Verdict, got.Value, tt.want, tt.wantValue)
			}
		})
	}
}
