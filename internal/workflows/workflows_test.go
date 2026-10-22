package workflows_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
	"github.com/rvenutolo/github-repos-audit/internal/workflows"
)

// parse fails the test when the workflow did not parse, so each spec test
// below can compare whole values without first ruling out a ParseError.
func parse(t *testing.T, text string) audit.Workflow {
	t.Helper()
	w := workflows.ParseWorkflow(".github/workflows/ci.yml", text)
	if w.ParseError != "" {
		t.Fatalf("ParseWorkflow() ParseError = %q, want none", w.ParseError)
	}
	return w
}

func TestParseWorkflow_eventsForms(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		text string
		want []string
	}{
		{"string", "on: push\n", []string{"push"}},
		{"list", "on: [push, pull_request]\n", []string{"pull_request", "push"}},
		{
			"map",
			"on:\n  schedule:\n    - cron: '0 0 * * *'\n  pull_request:\n    branches: [main]\n",
			[]string{"pull_request", "schedule"},
		},
		// A map key with no value is how a bare event is written in map form.
		{"map with a bare key", "on:\n  workflow_dispatch:\n  push:\n", []string{"push", "workflow_dispatch"}},
		{"repeated in a list", "on: [push, push]\n", []string{"push"}},
		{"absent", "jobs: {}\n", nil},
		{"blank string", "on: \"\"\n", nil},
		{"null in a list", "on: [~]\n", nil},
		{"blank beside a name", "on: [push, \"\"]\n", []string{"push"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if diff := cmp.Diff(tt.want, parse(t, tt.text).Events); diff != "" {
				t.Errorf("Events mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestParseWorkflow_concurrency(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		text string
		want audit.Concurrency
	}{
		{
			"map", "concurrency:\n  group: x\n  cancel-in-progress: true\n",
			audit.Concurrency{Set: true, CancelInProgress: "true"},
		},
		{"string", "concurrency: ci-${{ github.ref }}\n", audit.Concurrency{Set: true}},
		{
			"expression kept verbatim",
			"concurrency:\n  group: x\n  cancel-in-progress: ${{ github.event_name == 'pull_request' }}\n",
			audit.Concurrency{Set: true, CancelInProgress: "${{ github.event_name == 'pull_request' }}"},
		},
		{"map without cancel-in-progress", "concurrency:\n  group: x\n", audit.Concurrency{Set: true}},
		{"absent", "on: push\n", audit.Concurrency{}},
		// An explicit null is how YAML writes "nothing here"; it sets nothing.
		{"null", "concurrency: ~\n", audit.Concurrency{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if diff := cmp.Diff(tt.want, parse(t, tt.text).Concurrency); diff != "" {
				t.Errorf("Concurrency mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestParseWorkflow_jobs(t *testing.T) {
	t.Parallel()

	text := `on: push
jobs:
  build:
    runs-on: ubuntu-latest
    timeout-minutes: 30
    permissions:
      actions: write
      contents: read
    concurrency:
      group: build
      cancel-in-progress: false
    steps:
      - uses: actions/checkout@0000000000000000000000000000000000000000
      - name: cache
        uses: nix-community/cache-nix-action@1111111111111111111111111111111111111111
        with:
          purge: true
          gc-max-store-size-linux: 0
          primary-key: nix-${{ runner.os }}
      - run: nix flake update --commit-lock-file
  call:
    uses: ./.github/workflows/x.yml
  plain:
    runs-on: ubuntu-latest
    steps:
      - run: echo PAYLOAD_RAN
`
	want := []audit.Job{
		{
			ID:             "build",
			TimeoutMinutes: "30",
			Concurrency:    audit.Concurrency{Set: true, CancelInProgress: "false"},
			Permissions: audit.Permissions{
				Set:    true,
				Scopes: map[string]string{"actions": "write", "contents": "read"},
			},
			Uses: []audit.StepUse{
				{Action: "actions/checkout@0000000000000000000000000000000000000000"},
				{
					Action: "nix-community/cache-nix-action@1111111111111111111111111111111111111111",
					With: map[string]string{
						"purge":                   "true",
						"gc-max-store-size-linux": "0",
						"primary-key":             "nix-${{ runner.os }}",
					},
				},
			},
			FlakeUpdate: true,
		},
		{ID: "call", Reusable: true},
		{ID: "plain"},
	}
	if diff := cmp.Diff(want, parse(t, text).Jobs); diff != "" {
		t.Errorf("Jobs mismatch (-want +got):\n%s", diff)
	}
}

func TestParseWorkflow_flakeUpdateNeedsTheWholePhrase(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  string
		want bool
	}{
		{"in a block scalar", "|\n          set -e\n          nix flake update\n", true},
		{"flake check is not an update", "nix flake check", false},
		{"lock is not an update", "nix flake lock --update-input nixpkgs", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			text := "jobs:\n  j:\n    steps:\n      - run: " + tt.run + "\n"
			jobs := parse(t, text).Jobs
			if len(jobs) != 1 {
				t.Fatalf("got %d jobs, want 1", len(jobs))
			}
			if jobs[0].FlakeUpdate != tt.want {
				t.Errorf("FlakeUpdate = %v, want %v", jobs[0].FlakeUpdate, tt.want)
			}
		})
	}
}

func TestParseWorkflow_permissionsStringForm(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		text string
		want audit.Permissions
	}{
		{"write-all", "permissions: write-all\n", audit.Permissions{Set: true, All: "write-all"}},
		{"read-all", "permissions: read-all\n", audit.Permissions{Set: true, All: "read-all"}},
		// `permissions: {}` grants nothing at all, which is not the same as
		// leaving permissions unset, so it is Set with an empty, non-nil map.
		{"empty map", "permissions: {}\n", audit.Permissions{Set: true, Scopes: map[string]string{}}},
		{"absent", "on: push\n", audit.Permissions{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if diff := cmp.Diff(tt.want, parse(t, tt.text).Permissions); diff != "" {
				t.Errorf("Permissions mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestParseWorkflow_keepsPath(t *testing.T) {
	t.Parallel()

	if got := workflows.ParseWorkflow(".github/workflows/a.yml", "on: push\n").Path; got != ".github/workflows/a.yml" {
		t.Errorf("Path = %q, want .github/workflows/a.yml", got)
	}
	if got := workflows.ParseWorkflow(".github/workflows/b.yml", "jobs: [").Path; got != ".github/workflows/b.yml" {
		t.Errorf("Path on a parse error = %q, want .github/workflows/b.yml", got)
	}
}

func TestParseWorkflow_anchorsAreFollowed(t *testing.T) {
	t.Parallel()

	text := `jobs:
  a:
    timeout-minutes: &t 15
    steps: &steps
      - uses: &u actions/checkout@v4
        with: &w
          fetch-depth: 0
  b:
    timeout-minutes: *t
    steps:
      - uses: *u
        with: *w
  c:
    steps: *steps
`
	use := audit.StepUse{Action: "actions/checkout@v4", With: map[string]string{"fetch-depth": "0"}}
	want := []audit.Job{
		{ID: "a", TimeoutMinutes: "15", Uses: []audit.StepUse{use}},
		{ID: "b", TimeoutMinutes: "15", Uses: []audit.StepUse{use}},
		{ID: "c", Uses: []audit.StepUse{use}},
	}
	if diff := cmp.Diff(want, parse(t, text).Jobs); diff != "" {
		t.Errorf("Jobs mismatch (-want +got):\n%s", diff)
	}
}

func TestParse_invalidYAML(t *testing.T) {
	t.Parallel()

	w := workflows.ParseWorkflow("a.yml", "jobs: [")
	if w.ParseError == "" {
		t.Error("ParseWorkflow() ParseError is empty, want the YAML error")
	}
	if w.Jobs != nil {
		t.Errorf("ParseWorkflow() Jobs = %+v, want none", w.Jobs)
	}
	c := workflows.ParseComposite("action.yml", "jobs: [")
	if c.ParseError == "" {
		t.Error("ParseComposite() ParseError is empty, want the YAML error")
	}
	if c.Uses != nil || c.FlakeUpdate {
		t.Errorf("ParseComposite() = %+v, want nothing but ParseError", c)
	}
}

// TestParse_wrongShape covers YAML that parses but is not a workflow or
// composite action GitHub would accept. Each is a ParseError carrying
// nothing else: a check reading a half-understood file could pass on what it
// failed to see, and an unreadable file has to fail instead.
func TestParse_wrongShape(t *testing.T) {
	t.Parallel()

	workflowTests := []struct {
		name string
		text string
		want string // a substring of ParseError
	}{
		{"empty document", "", "not a mapping"},
		{"top-level sequence", "- a\n", "not a mapping"},
		{"top-level scalar", "hello\n", "not a mapping"},
		{"jobs is a sequence", "jobs: [a]\n", "jobs"},
		{"job is a scalar", "jobs:\n  a: x\n", "jobs.a"},
		{"steps is a mapping", "jobs:\n  a:\n    steps: {x: y}\n", "steps"},
		{"step is a scalar", "jobs:\n  a:\n    steps: [x]\n", "steps"},
		{"uses is a mapping", "jobs:\n  a:\n    steps:\n      - uses: {x: y}\n", "uses"},
		{"with is a sequence", "jobs:\n  a:\n    steps:\n      - uses: a@b\n        with: [x]\n", "with"},
		{"with value is a mapping", "jobs:\n  a:\n    steps:\n      - uses: a@b\n        with:\n          k: {x: y}\n", "with.k"},
		{"run is a sequence", "jobs:\n  a:\n    steps:\n      - run: [x]\n", "run"},
		{"job uses is a sequence", "jobs:\n  a:\n    uses: [x]\n", "uses"},
		{"timeout is a mapping", "jobs:\n  a:\n    timeout-minutes: {x: y}\n", "timeout-minutes"},
		{"concurrency is a sequence", "concurrency: [x]\n", "concurrency"},
		{"cancel-in-progress is a sequence", "concurrency:\n  cancel-in-progress: [x]\n", "cancel-in-progress"},
		{"permissions is a sequence", "permissions: [x]\n", "permissions"},
		{"permission scope is a mapping", "permissions:\n  actions: {x: y}\n", "actions"},
		{"job permissions is a sequence", "jobs:\n  a:\n    permissions: [x]\n", "permissions"},
		{"job concurrency is a sequence", "jobs:\n  a:\n    concurrency: [x]\n", "concurrency"},
		{"event list holds a mapping", "on: [{x: y}]\n", "on"},
		{"repeated top-level key", "jobs:\n  a: {}\njobs:\n  b: {}\n", "jobs is defined twice"},
		{"repeated job key", "jobs:\n  a:\n    runs-on: x\n    runs-on: y\n", "jobs.a.runs-on is defined twice"},
		{
			"repeated with key",
			"jobs:\n  a:\n    steps:\n      - uses: a@b\n        with:\n          k: 1\n          k: 2\n",
			"with.k is defined twice",
		},
		{"repeated on trigger", "on:\n  push: {}\n  push: {}\n", "on.push is defined twice"},
	}
	for _, tt := range workflowTests {
		t.Run("workflow "+tt.name, func(t *testing.T) {
			t.Parallel()
			w := workflows.ParseWorkflow(".github/workflows/ci.yml", tt.text)
			if !strings.Contains(w.ParseError, tt.want) {
				t.Errorf("ParseError = %q, want it to mention %q", w.ParseError, tt.want)
			}
			if diff := cmp.Diff(audit.Workflow{Path: ".github/workflows/ci.yml", ParseError: w.ParseError}, w); diff != "" {
				t.Errorf("ParseWorkflow() carries more than the error (-want +got):\n%s", diff)
			}
		})
	}

	compositeTests := []struct {
		name string
		text string
		want string
	}{
		{"empty document", "", "not a mapping"},
		{"runs is a scalar", "runs: x\n", "runs"},
		{"steps is a mapping", "runs:\n  steps: {x: y}\n", "steps"},
		{"step uses is a sequence", "runs:\n  steps:\n    - uses: [x]\n", "uses"},
		{"repeated runs key", "runs:\n  steps: []\nruns:\n  steps: []\n", "runs is defined twice"},
	}
	for _, tt := range compositeTests {
		t.Run("composite "+tt.name, func(t *testing.T) {
			t.Parallel()
			c := workflows.ParseComposite("action.yml", tt.text)
			if !strings.Contains(c.ParseError, tt.want) {
				t.Errorf("ParseError = %q, want it to mention %q", c.ParseError, tt.want)
			}
			if diff := cmp.Diff(audit.CompositeAction{Path: "action.yml", ParseError: c.ParseError}, c); diff != "" {
				t.Errorf("ParseComposite() carries more than the error (-want +got):\n%s", diff)
			}
		})
	}
}

func TestParseComposite_steps(t *testing.T) {
	t.Parallel()

	text := `name: setup
description: shared setup
runs:
  using: composite
  steps:
    - uses: DeterminateSystems/nix-installer-action@2222222222222222222222222222222222222222
    - uses: nix-community/cache-nix-action@1111111111111111111111111111111111111111
      with:
        purge: true
    - run: nix flake update
      shell: bash
`
	want := audit.CompositeAction{
		Path: ".github/actions/setup/action.yml",
		Uses: []audit.StepUse{
			{Action: "DeterminateSystems/nix-installer-action@2222222222222222222222222222222222222222"},
			{Action: "nix-community/cache-nix-action@1111111111111111111111111111111111111111", With: map[string]string{"purge": "true"}},
		},
		FlakeUpdate: true,
	}
	got := workflows.ParseComposite(".github/actions/setup/action.yml", text)
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("ParseComposite() mismatch (-want +got):\n%s", diff)
	}
}

// TestParseComposite_notComposite: a JavaScript or Docker action has no
// steps, which is an ordinary answer rather than a parse failure.
func TestParseComposite_notComposite(t *testing.T) {
	t.Parallel()

	for _, text := range []string{
		"runs:\n  using: node20\n  main: index.js\n",
		"runs:\n  using: docker\n  image: Dockerfile\n",
		// No runs at all is not an action GitHub would run, but it has no
		// steps either, and a check looking for a step finds none: it fails
		// rather than passes, so it needs no error of its own.
		"name: x\n",
	} {
		got := workflows.ParseComposite("action.yml", text)
		if diff := cmp.Diff(audit.CompositeAction{Path: "action.yml"}, got); diff != "" {
			t.Errorf("ParseComposite(%q) mismatch (-want +got):\n%s", text, diff)
		}
	}
}
