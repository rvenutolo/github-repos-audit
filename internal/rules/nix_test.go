package rules_test

import (
	"strings"
	"testing"
	"time"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
	"github.com/rvenutolo/github-repos-audit/internal/rules"
)

// evaluateHermetic judges one repository against standard-types.toml, which
// holds tools to hermetic_nix = "required", and the fixture [hermetic]
// standard, returning its hermetic_nix cell.
func evaluateHermetic(t *testing.T, r audit.Repo) rules.Cell {
	t.Helper()
	cells, err := evaluateOne(r)
	if err != nil {
		t.Fatalf("Evaluate() error = %v, want nil", err)
	}
	return cells[rules.CheckHermeticNix]
}

// TestEvaluate_hermetic_allPartsPass: a flake that declares the configured
// input, locks it, names the lint in flake.nix and wires it into a gate file
// passes with a plain tick, since there is nothing further to say.
func TestEvaluate_hermetic_allPartsPass(t *testing.T) {
	t.Parallel()

	got := evaluateHermetic(t, fixture("alpha", "tools"))
	if got.Verdict != rules.Pass || got.Value != "" {
		t.Errorf("cell = %v %q, want a pass with no value", got.Verdict, got.Value)
	}
}

// TestEvaluate_hermetic_namesEveryMissingPart: the cell names each missing
// part of the contract in order, so the fix list reads off the cell.
func TestEvaluate_hermetic_namesEveryMissingPart(t *testing.T) {
	t.Parallel()

	r := fixture("alpha", "tools")
	r.Flake.Inputs = []string{"github:NixOS/nixpkgs/nixos-unstable"}
	r.Flake.LockedInputs = []string{"github:nixos/nixpkgs"}
	r.Flake.GateFiles = nil

	got := evaluateHermetic(t, r)
	if got.Verdict != rules.Fail || got.Value != "no input, not locked, not in gate" || !got.Mark {
		t.Errorf("cell = %v %q mark=%t, want a marked fail %q", got.Verdict, got.Value, got.Mark, "no input, not locked, not in gate")
	}
}

func TestEvaluate_hermetic_eachPart(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*audit.Repo)
		want   string
	}{
		{"no input", func(r *audit.Repo) { r.Flake.Inputs = nil }, "no input"},
		{"a different input", func(r *audit.Repo) { r.Flake.Inputs = []string{"github:gh-owner/preset-store"} }, "no input"},
		{"not locked", func(r *audit.Repo) { r.Flake.LockedInputs = []string{"github:nixos/nixpkgs"} }, "not locked"},
		{"no lock at all", func(r *audit.Repo) { r.Flake.HasLock, r.Flake.LockedInputs = false, nil }, "not locked"},
		{"not in devShell", func(r *audit.Repo) { r.Flake.NamesLint = false }, "not in devShell"},
		{"not in gate", func(r *audit.Repo) { r.Flake.GateFiles = nil }, "not in gate"},
		{"nothing at all", func(r *audit.Repo) { r.Flake = audit.Flake{} }, "no input, not locked, not in devShell, not in gate"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := fixture("alpha", "tools")
			tc.mutate(&r)
			got := evaluateHermetic(t, r)
			if got.Verdict != rules.Fail || got.Value != tc.want || !got.Mark {
				t.Errorf("cell = %v %q mark=%t, want a marked fail %q", got.Verdict, got.Value, got.Mark, tc.want)
			}
		})
	}
}

// TestEvaluate_hermetic_lockMatchIgnoresCaseAndSuffix: flake.lock records the
// input lower-cased and without the ref or directory flake.nix may add, so the
// configured flake is matched on its lower-cased github:<owner>/<repo> alone.
func TestEvaluate_hermetic_lockMatchIgnoresCaseAndSuffix(t *testing.T) {
	t.Parallel()

	for _, flake := range []string{
		"github:GH-Owner/Lint-Flake",
		"github:gh-owner/lint-flake/v1",
		"github:gh-owner/lint-flake?dir=lint",
		"github:gh-owner/lint-flake#default",
	} {
		t.Run(flake, func(t *testing.T) {
			t.Parallel()

			r := fixture("alpha", "tools")
			r.Flake.Inputs = []string{flake}
			snap := &audit.Snapshot{
				Types:    standardTypes(),
				Identity: fixtureIdentity(),
				Hermetic: audit.HermeticStandard{Flake: flake, Lint: fixtureHermetic().Lint},
				Repos:    []audit.Repo{r},
			}
			rep, err := rules.Evaluate(snap)
			if err != nil {
				t.Fatalf("Evaluate() error = %v, want nil", err)
			}
			if got := rep.Repos[0].Cell(rules.CheckHermeticNix); got.Verdict != rules.Pass {
				t.Errorf("cell = %v %q, want a pass", got.Verdict, got.Value)
			}
		})
	}
}

// TestEvaluate_hermetic_nonGitHubFlakeIsNeverLocked: flake.lock's GitHub-type
// nodes are the only ones recorded, so a configured flake of any other kind
// cannot be found locked; the cell says so rather than passing.
// The github: scheme is read as written, so a differently-cased scheme is
// not one either. A github: reference with no owner or no repository names no lock node
// either, and is never found locked, even beside a lock entry it could be
// mistaken for.
func TestEvaluate_hermetic_nonGitHubFlakeIsNeverLocked(t *testing.T) {
	t.Parallel()

	for _, flake := range []string{
		"git+https://example.invalid/lint-flake",
		"gh:x",
		"github:gh-owner",
		"github:/lint-flake",
		"github:gh-owner/",
		"GitHub:gh-owner/lint-flake",
		"GITHUB:gh-owner/lint-flake",
	} {
		t.Run(flake, func(t *testing.T) {
			t.Parallel()

			r := fixture("alpha", "tools")
			r.Flake.Inputs = []string{flake}
			r.Flake.LockedInputs = append(r.Flake.LockedInputs, "github:gh-owner/", "github:/lint-flake")
			snap := &audit.Snapshot{
				Types:    standardTypes(),
				Identity: fixtureIdentity(),
				Hermetic: audit.HermeticStandard{Flake: flake, Lint: fixtureHermetic().Lint},
				Repos:    []audit.Repo{r},
			}
			rep, err := rules.Evaluate(snap)
			if err != nil {
				t.Fatalf("Evaluate() error = %v, want nil", err)
			}
			if got := rep.Repos[0].Cell(rules.CheckHermeticNix); got.Verdict != rules.Fail || got.Value != "not locked" {
				t.Errorf("cell = %v %q, want a fail %q", got.Verdict, got.Value, "not locked")
			}
		})
	}
}

// TestEvaluate_hermetic_noFlakeIsNA: a repository with no flake.nix has no
// Nix to be hermetic about; the flake_nix row already reports the missing
// file, so this one is n/a rather than a second gap for the same thing.
func TestEvaluate_hermetic_noFlakeIsNA(t *testing.T) {
	t.Parallel()

	r := fixture("alpha", "tools")
	r.Files.FlakeNix = false
	r.Flake = audit.Flake{}
	cells, err := evaluateOne(r)
	if err != nil {
		t.Fatalf("Evaluate() error = %v, want nil", err)
	}
	if got := cells[rules.CheckHermeticNix]; got.Verdict != rules.NA {
		t.Errorf("cell = %v %q, want n/a", got.Verdict, got.Value)
	}
}

// TestEvaluate_hermetic_unreadableFlakeNix: a flake.nix the collector could
// not read fails as unreadable and names no other part, even with the lock
// and gate parts missing too, so the cell holds the one fix. With no
// flake.nix at all the row is still n/a.
func TestEvaluate_hermetic_unreadableFlakeNix(t *testing.T) {
	t.Parallel()

	t.Run("unreadable", func(t *testing.T) {
		t.Parallel()

		r := fixture("alpha", "tools")
		r.Flake = audit.Flake{NixError: "flake.nix: not a text file"}
		got := evaluateHermetic(t, r)
		if got.Verdict != rules.Fail || got.Value != "unreadable flake.nix" || !got.Mark {
			t.Errorf("cell = %v %q mark=%t, want a marked fail %q", got.Verdict, got.Value, got.Mark, "unreadable flake.nix")
		}
	})
	t.Run("no flake.nix", func(t *testing.T) {
		t.Parallel()

		r := fixture("alpha", "tools")
		r.Files.FlakeNix = false
		r.Flake = audit.Flake{NixError: "flake.nix: not a text file"}
		if got := evaluateHermetic(t, r); got.Verdict != rules.NA {
			t.Errorf("cell = %v %q, want n/a", got.Verdict, got.Value)
		}
	})
}

// TestEvaluate_hermetic_reachesTheWorklist: a failing cell is a gap under its
// own label.
func TestEvaluate_hermetic_reachesTheWorklist(t *testing.T) {
	t.Parallel()

	r := fixture("alpha", "tools")
	r.Flake.GateFiles = nil
	rep, err := rules.Evaluate(&audit.Snapshot{Types: standardTypes(), Identity: fixtureIdentity(), Hermetic: fixtureHermetic(), Repos: []audit.Repo{r}})
	if err != nil {
		t.Fatalf("Evaluate() error = %v, want nil", err)
	}
	for _, g := range rep.Gaps {
		if g.Label == "Hermetic lint not wired in" && len(g.Repos) == 1 && g.Repos[0] == "alpha" {
			return
		}
	}
	t.Errorf("Gaps = %+v, want alpha under %q", rep.Gaps, "Hermetic lint not wired in")
}

// TestEvaluate_hermetic_judgedWithoutStandardErrors: config validation
// refuses a judged hermetic_nix with no [hermetic] table, so a snapshot
// without the standard was assembled some other way, and judging it against
// an empty flake reference would fail every repository for nothing. A
// standard missing either half is as unusable as none.
func TestEvaluate_hermetic_judgedWithoutStandardErrors(t *testing.T) {
	t.Parallel()

	for name, std := range map[string]audit.HermeticStandard{
		"none":     {},
		"no lint":  {Flake: fixtureHermetic().Flake},
		"no flake": {Lint: fixtureHermetic().Lint},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			snap := &audit.Snapshot{Types: standardTypes(), Identity: fixtureIdentity(), Hermetic: std, Repos: []audit.Repo{fixture("alpha", "tools")}}
			if _, err := rules.Evaluate(snap); err == nil || !strings.Contains(err.Error(), "hermetic_nix") {
				t.Errorf("Evaluate(judged, %s) error = %v, want one naming hermetic_nix", name, err)
			}
		})
	}

	// Not judged, the standard is not needed: content holds hermetic_nix to
	// not_required.
	rep, err := rules.Evaluate(&audit.Snapshot{Types: standardTypes(), Identity: fixtureIdentity(), Repos: []audit.Repo{fixture("alpha", "content")}})
	if err != nil {
		t.Fatalf("Evaluate(not judged, no standard) error = %v, want nil", err)
	}
	if got := rep.Repos[0].Cell(rules.CheckHermeticNix); got.Verdict != rules.NA {
		t.Errorf("cell = %v, want n/a", got.Verdict)
	}
}

// lockAgeAt judges one tools repository, which standard-types.toml holds to
// nix_lock_age = "required", as of now, and returns its nix_lock_age cell.
func lockAgeAt(t *testing.T, r audit.Repo, now time.Time) rules.Cell {
	t.Helper()
	rep, err := rules.Evaluate(&audit.Snapshot{
		GeneratedAt: now, Types: standardTypes(), Identity: fixtureIdentity(), Hermetic: fixtureHermetic(),
		Repos: []audit.Repo{r},
	})
	if err != nil {
		t.Fatalf("Evaluate() error = %v, want nil", err)
	}
	return rep.Repos[0].Cell(rules.CheckNixLockAge)
}

// TestEvaluate_lockAge_boundary: nixpkgs locked at most 30 days before the
// snapshot passes, and a second more fails. The age is measured against the
// snapshot's own instant, so the same audit.json always judges the same way,
// and the cell carries the lock instant for the renderer to show as an age
// beside its mark.
func TestEvaluate_lockAge_boundary(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		at   time.Time
		want rules.Verdict
	}{
		{"exactly 30 days", now.Add(-30 * 24 * time.Hour), rules.Pass},
		{"30 days and a second", now.Add(-30*24*time.Hour - time.Second), rules.Fail},
		{"locked yesterday", now.Add(-24 * time.Hour), rules.Pass},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := fixture("alpha", "tools")
			r.Flake.NixpkgsLastModified = tc.at
			got := lockAgeAt(t, r, now)
			if got.Verdict != tc.want || !got.At.Equal(tc.at) || !got.Mark || got.Value != "" {
				t.Errorf("cell = %v at=%v mark=%t value=%q, want %v at %v, marked, no value",
					got.Verdict, got.At, got.Mark, got.Value, tc.want, tc.at)
			}
		})
	}
}

// TestEvaluate_lockAge_unreadable: a flake.lock that could not be read has no
// date to judge, and fails saying so rather than passing on nothing.
func TestEvaluate_lockAge_unreadable(t *testing.T) {
	t.Parallel()

	r := fixture("alpha", "tools")
	r.Flake = audit.Flake{HasLock: true, LockError: "flake.lock: invalid JSON"}
	got := lockAgeAt(t, r, time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC))
	if got.Verdict != rules.Fail || got.Value != "unreadable flake.lock" || !got.Mark {
		t.Errorf("cell = %v %q mark=%t, want a marked fail %q", got.Verdict, got.Value, got.Mark, "unreadable flake.lock")
	}
}

// TestEvaluate_lockAge_noLockIsNA: with no flake.lock, or a lock with no
// input named nixpkgs, there is no nixpkgs date to be old, so the check does
// not apply.
func TestEvaluate_lockAge_noLockIsNA(t *testing.T) {
	t.Parallel()

	for name, f := range map[string]audit.Flake{
		"no lock":            {},
		"no nixpkgs":         {HasLock: true, LockedInputs: []string{"github:gh-owner/lint-flake"}},
		"stale, but no lock": {NixpkgsLastModified: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r := fixture("alpha", "tools")
			r.Flake = f
			got := lockAgeAt(t, r, time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC))
			if got.Verdict != rules.NA {
				t.Errorf("cell = %v %q, want n/a", got.Verdict, got.Value)
			}
		})
	}
}

// TestEvaluate_lockAge_reachesTheWorklist: a stale lock is a gap under its
// own label.
func TestEvaluate_lockAge_reachesTheWorklist(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	r := fixture("alpha", "tools")
	r.Flake.NixpkgsLastModified = now.Add(-47 * 24 * time.Hour)
	rep, err := rules.Evaluate(&audit.Snapshot{
		GeneratedAt: now, Types: standardTypes(), Identity: fixtureIdentity(), Hermetic: fixtureHermetic(),
		Repos: []audit.Repo{r},
	})
	if err != nil {
		t.Fatalf("Evaluate() error = %v, want nil", err)
	}
	for _, g := range rep.Gaps {
		if g.Label == "nixpkgs locked more than 30 days ago" && len(g.Repos) == 1 && g.Repos[0] == "alpha" {
			return
		}
	}
	t.Errorf("Gaps = %+v, want alpha under %q", rep.Gaps, "nixpkgs locked more than 30 days ago")
}

// lockRefresh judges one tools repository, which standard-types.toml holds to
// nix_lock_refresh = "required", and returns its nix_lock_refresh cell.
func lockRefresh(t *testing.T, r audit.Repo) rules.Cell {
	t.Helper()
	cells, err := evaluateOne(r)
	if err != nil {
		t.Fatalf("Evaluate() error = %v, want nil", err)
	}
	return cells[rules.CheckNixLockRefresh]
}

// withoutRenovateRefresh is the fixture with Renovate refreshing nothing, so a
// test can add exactly one source of refreshes.
func withoutRenovateRefresh(r audit.Repo) audit.Repo {
	r.Renovate.NixManager = false
	r.Renovate.LockFileMaintenance = false
	return r
}

// scheduled is a workflow triggered on a schedule, running one job.
func scheduled(path string, job audit.Job) audit.Workflow {
	return audit.Workflow{Path: path, Events: []string{"schedule", "workflow_dispatch"}, Jobs: []audit.Job{job}}
}

// TestEvaluate_lockRefresh_renovateNeedsNixManager: lock-file maintenance
// rewrites lock files only for the managers that run, so without the nix
// manager it never touches flake.lock; the nix manager alone, on a lock whose
// inputs are not pinned by rev, opens no pull request for a moving branch
// either. Only both together, from a configuration that exists, refresh it.
func TestEvaluate_lockRefresh_renovateNeedsNixManager(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*audit.Repo)
		pass   bool
	}{
		{"both", func(*audit.Repo) {}, true},
		{"maintenance without the nix manager", func(r *audit.Repo) { r.Renovate.NixManager = false }, false},
		{"the nix manager without maintenance", func(r *audit.Repo) { r.Renovate.LockFileMaintenance = false }, false},
		{"no configuration", func(r *audit.Repo) { r.Files.RenovateConfig = "" }, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := fixture("alpha", "tools")
			r.Renovate.NixManager, r.Renovate.LockFileMaintenance = true, true
			tc.mutate(&r)
			got := lockRefresh(t, r)
			switch {
			case tc.pass && (got.Verdict != rules.Pass || got.Value != "Renovate" || !got.Mark):
				t.Errorf("cell = %v %q mark=%t, want a marked pass %q", got.Verdict, got.Value, got.Mark, "Renovate")
			case !tc.pass && (got.Verdict != rules.Fail || got.Value != ""):
				t.Errorf("cell = %v %q, want a bare fail", got.Verdict, got.Value)
			}
		})
	}
}

// TestEvaluate_lockRefresh_revPinnedNeedsNoMaintenance: when every input is
// pinned by rev, the nix manager's own digest updates move the lock, so
// lock-file maintenance is not needed.
func TestEvaluate_lockRefresh_revPinnedNeedsNoMaintenance(t *testing.T) {
	t.Parallel()

	r := fixture("alpha", "tools")
	r.Renovate.NixManager, r.Renovate.LockFileMaintenance = true, false
	r.Flake.AllRevPinned = true
	if got := lockRefresh(t, r); got.Verdict != rules.Pass || got.Value != "Renovate" {
		t.Errorf("cell = %v %q, want a pass %q", got.Verdict, got.Value, "Renovate")
	}

	r.Renovate.NixManager = false
	if got := lockRefresh(t, r); got.Verdict != rules.Fail {
		t.Errorf("rev-pinned without the nix manager: cell = %v %q, want a fail", got.Verdict, got.Value)
	}
}

// TestEvaluate_lockRefresh_scheduledWorkflow: a scheduled workflow refreshes
// the lock when one of its jobs runs `nix flake update`, uses the
// update-flake-lock action, or uses a local composite that runs `nix flake
// update`; a composite is found from either local spelling, as action.yml or
// action.yaml.
func TestEvaluate_lockRefresh_scheduledWorkflow(t *testing.T) {
	t.Parallel()

	composite := func(path string) []audit.CompositeAction {
		return []audit.CompositeAction{{Path: path, FlakeUpdate: true}}
	}
	uses := func(action string) audit.Job {
		return audit.Job{ID: "update", Uses: []audit.StepUse{{Action: action}}}
	}
	tests := []struct {
		name    string
		job     audit.Job
		actions []audit.CompositeAction
	}{
		{"a run step", audit.Job{ID: "update", FlakeUpdate: true}, nil},
		{"the update action", uses("DeterminateSystems/update-flake-lock@0123456789abcdef0123456789abcdef01234567"), nil},
		// GitHub matches owner and repository names in any case.
		{"the update action in another case", uses("determinatesystems/Update-Flake-Lock@v24"), nil},
		{"a ./ composite", uses("./.github/actions/update"), composite(".github/actions/update/action.yml")},
		{"a $/ composite", uses("$/.github/actions/update"), composite(".github/actions/update/action.yml")},
		{"an action.yaml composite", uses("./.github/actions/update"), composite(".github/actions/update/action.yaml")},
		{"a ./ composite with a trailing slash", uses("./.github/actions/update/"), composite(".github/actions/update/action.yml")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := withoutRenovateRefresh(fixture("alpha", "tools"))
			r.Workflows = []audit.Workflow{scheduled(".github/workflows/update.yml", tc.job)}
			r.Actions = tc.actions
			if got := lockRefresh(t, r); got.Verdict != rules.Pass || got.Value != "workflow" || !got.Mark {
				t.Errorf("cell = %v %q mark=%t, want a marked pass %q", got.Verdict, got.Value, got.Mark, "workflow")
			}
		})
	}

	// Both sources at once are both named, Renovate first.
	r := fixture("alpha", "tools")
	r.Renovate.NixManager, r.Renovate.LockFileMaintenance = true, true
	r.Workflows = []audit.Workflow{scheduled(".github/workflows/update.yml", audit.Job{ID: "update", FlakeUpdate: true})}
	if got := lockRefresh(t, r); got.Verdict != rules.Pass || got.Value != "Renovate, workflow" {
		t.Errorf("both: cell = %v %q, want a pass %q", got.Verdict, got.Value, "Renovate, workflow")
	}
}

// TestEvaluate_lockRefresh_unscheduledFlakeUpdateDoesNotCount: a workflow
// that runs `nix flake update` only when pushed to or dispatched by hand
// refreshes nothing on its own; nor does a composite that runs it from an
// unscheduled workflow, a local-looking action that names no composite, a
// composite that does not update, or a lookalike of the update action.
func TestEvaluate_lockRefresh_unscheduledFlakeUpdateDoesNotCount(t *testing.T) {
	t.Parallel()

	uses := func(action string) audit.Job {
		return audit.Job{ID: "update", Uses: []audit.StepUse{{Action: action}}}
	}
	tests := []struct {
		name     string
		workflow audit.Workflow
		actions  []audit.CompositeAction
	}{
		{"pushed", audit.Workflow{Path: ".github/workflows/ci.yml", Events: []string{"push", "workflow_dispatch"}, Jobs: []audit.Job{{ID: "update", FlakeUpdate: true}}}, nil},
		{
			"an unscheduled composite",
			audit.Workflow{Path: ".github/workflows/ci.yml", Events: []string{"push"}, Jobs: []audit.Job{uses("./.github/actions/update")}},
			[]audit.CompositeAction{{Path: ".github/actions/update/action.yml", FlakeUpdate: true}},
		},
		{"a missing composite", scheduled(".github/workflows/update.yml", uses("./.github/actions/update")), nil},
		{
			"a composite that does not update",
			scheduled(".github/workflows/update.yml", uses("./.github/actions/update")),
			[]audit.CompositeAction{{Path: ".github/actions/update/action.yml"}},
		},
		{
			"a remote action of the same path",
			scheduled(".github/workflows/update.yml", uses("gh-owner/preset-store/.github/actions/update@v1")),
			[]audit.CompositeAction{{Path: "gh-owner/preset-store/.github/actions/update/action.yml", FlakeUpdate: true}},
		},
		{"a lookalike action", scheduled(".github/workflows/update.yml", uses("someone/DeterminateSystems/update-flake-lock@v1")), nil},
		{"a longer repository name", scheduled(".github/workflows/update.yml", uses("DeterminateSystems/update-flake-lock-fork@v1")), nil},
		{"no version", scheduled(".github/workflows/update.yml", uses("DeterminateSystems/update-flake-lock")), nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := withoutRenovateRefresh(fixture("alpha", "tools"))
			r.Workflows = []audit.Workflow{tc.workflow}
			r.Actions = tc.actions
			if got := lockRefresh(t, r); got.Verdict != rules.Fail {
				t.Errorf("cell = %v %q, want a fail", got.Verdict, got.Value)
			}
		})
	}
}

// TestEvaluate_lockRefresh_unreadableWorkflowIsNamed: a workflow that could
// not be read might be the scheduled one, so with no refresh found the cell
// names it rather than failing bare; a source that does refresh still
// passes beside it.
func TestEvaluate_lockRefresh_unreadableWorkflowIsNamed(t *testing.T) {
	t.Parallel()

	r := withoutRenovateRefresh(fixture("alpha", "tools"))
	r.Workflows = []audit.Workflow{
		{Path: ".github/workflows/a.yml", ParseError: "invalid YAML"},
		scheduled(".github/workflows/b.yml", audit.Job{ID: "update", Uses: []audit.StepUse{{Action: "./.github/actions/update"}}}),
	}
	r.Actions = []audit.CompositeAction{{Path: ".github/actions/update/action.yml", ParseError: "invalid YAML"}}
	want := "unreadable .github/workflows/a.yml, unreadable .github/actions/update/action.yml"
	if got := lockRefresh(t, r); got.Verdict != rules.Fail || got.Value != want || !got.Mark {
		t.Errorf("cell = %v %q mark=%t, want a marked fail %q", got.Verdict, got.Value, got.Mark, want)
	}

	r.Renovate.NixManager, r.Renovate.LockFileMaintenance = true, true
	if got := lockRefresh(t, r); got.Verdict != rules.Pass || got.Value != "Renovate" {
		t.Errorf("with Renovate: cell = %v %q, want a pass %q", got.Verdict, got.Value, "Renovate")
	}
}

// TestEvaluate_lockRefresh_unreadableCompositeNamedOnce: an unreadable
// composite that several scheduled jobs use is one thing to look at, so it
// is named once, where it was first met.
func TestEvaluate_lockRefresh_unreadableCompositeNamedOnce(t *testing.T) {
	t.Parallel()

	update := audit.StepUse{Action: "./.github/actions/update"}
	r := withoutRenovateRefresh(fixture("alpha", "tools"))
	r.Workflows = []audit.Workflow{
		{
			Path: ".github/workflows/a.yml", Events: []string{"schedule"},
			Jobs: []audit.Job{{ID: "one", Uses: []audit.StepUse{update}}, {ID: "two", Uses: []audit.StepUse{update}}},
		},
		{Path: ".github/workflows/b.yml", ParseError: "invalid YAML"},
		scheduled(".github/workflows/c.yml", audit.Job{ID: "three", Uses: []audit.StepUse{update}}),
	}
	r.Actions = []audit.CompositeAction{{Path: ".github/actions/update/action.yml", ParseError: "invalid YAML"}}
	want := "unreadable .github/actions/update/action.yml, unreadable .github/workflows/b.yml"
	if got := lockRefresh(t, r); got.Verdict != rules.Fail || got.Value != want || !got.Mark {
		t.Errorf("cell = %v %q mark=%t, want a marked fail %q", got.Verdict, got.Value, got.Mark, want)
	}
}

// TestEvaluate_lockRefresh_noLockIsNA: with no flake.lock there is nothing to
// refresh.
func TestEvaluate_lockRefresh_noLockIsNA(t *testing.T) {
	t.Parallel()

	r := withoutRenovateRefresh(fixture("alpha", "tools"))
	r.Flake = audit.Flake{}
	if got := lockRefresh(t, r); got.Verdict != rules.NA {
		t.Errorf("cell = %v %q, want n/a", got.Verdict, got.Value)
	}
}

// TestEvaluate_lockRefresh_reachesTheWorklist: a lock nothing refreshes is a
// gap under its own label.
func TestEvaluate_lockRefresh_reachesTheWorklist(t *testing.T) {
	t.Parallel()

	r := withoutRenovateRefresh(fixture("alpha", "tools"))
	rep, err := rules.Evaluate(&audit.Snapshot{Types: standardTypes(), Identity: fixtureIdentity(), Hermetic: fixtureHermetic(), Repos: []audit.Repo{r}})
	if err != nil {
		t.Fatalf("Evaluate() error = %v, want nil", err)
	}
	for _, g := range rep.Gaps {
		if g.Label == "No automated flake.lock refresh" && len(g.Repos) == 1 && g.Repos[0] == "alpha" {
			return
		}
	}
	t.Errorf("Gaps = %+v, want alpha under %q", rep.Gaps, "No automated flake.lock refresh")
}
