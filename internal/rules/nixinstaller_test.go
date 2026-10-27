package rules_test

import (
	"testing"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
	"github.com/rvenutolo/github-repos-audit/internal/rules"
)

// Pinned references to each Nix installer action, as a workflow step writes
// them.
const (
	determinateNixAction  = "DeterminateSystems/determinate-nix-action@0123456789abcdef0123456789abcdef01234567"
	nixInstallerAction    = "DeterminateSystems/nix-installer-action@0123456789abcdef0123456789abcdef01234567"
	installNixAction      = "cachix/install-nix-action@0123456789abcdef0123456789abcdef01234567"
	nixQuickInstallAction = "nixbuild/nix-quick-install-action@0123456789abcdef0123456789abcdef01234567"
)

// nixInstaller judges one tools repository, which standard-types.toml holds
// to nix_installer = "required", and returns its nix_installer cell.
func nixInstaller(t *testing.T, r audit.Repo) rules.Cell {
	t.Helper()
	rep := mustEvaluateOne(t, r)
	return rep.Repos[0].Cell(rules.CheckNixInstaller)
}

// installerFixture is a tools fixture whose workflows are exactly ws.
func installerFixture(ws ...audit.Workflow) audit.Repo {
	r := fixture("alpha", "tools")
	r.Workflows = ws
	return r
}

// stepsWorkflow is a workflow at path whose one job runs the given steps.
func stepsWorkflow(path string, steps ...audit.StepUse) audit.Workflow {
	return audit.Workflow{
		Path:   path,
		Events: []string{"push"},
		Jobs:   []audit.Job{{ID: "check", Uses: steps}},
	}
}

func step(action string) audit.StepUse { return audit.StepUse{Action: action} }

// quiet is a determinate-nix-action step with its telemetry turned off, the
// one install the check passes.
func quiet() audit.StepUse {
	return audit.StepUse{Action: determinateNixAction, Env: map[string]string{"OTEL_SDK_DISABLED": "true"}}
}

// setupComposite is the local composite at .github/actions/setup, running
// the given steps.
func setupComposite(steps ...audit.StepUse) audit.CompositeAction {
	return audit.CompositeAction{Path: ".github/actions/setup/action.yml", Uses: steps}
}

// TestEvaluate_nixInstaller_naWithoutFlakeOrInstall: there is nothing to
// standardize in a repository without a flake.nix, nor in one whose CI never
// installs Nix, so both are n/a.
func TestEvaluate_nixInstaller_naWithoutFlakeOrInstall(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		repo func() audit.Repo
	}{
		{"no flake.nix", func() audit.Repo {
			r := installerFixture(stepsWorkflow(".github/workflows/ci.yml", step(installNixAction)))
			r.Files.FlakeNix = false
			return r
		}},
		{"no workflows", func() audit.Repo { return installerFixture() }},
		{"no installer step", func() audit.Repo {
			return installerFixture(stepsWorkflow(".github/workflows/ci.yml", step("actions/checkout@v5")))
		}},
		{"a composite with no installer step", func() audit.Repo {
			r := installerFixture(stepsWorkflow(".github/workflows/ci.yml", step("./.github/actions/setup")))
			r.Actions = []audit.CompositeAction{setupComposite(step("actions/checkout@v5"))}
			return r
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := nixInstaller(t, tc.repo()); got.Verdict != rules.NA {
				t.Errorf("cell = %v %q, want n/a", got.Verdict, got.Value)
			}
		})
	}
}

// TestEvaluate_nixInstaller_determinateActionPasses: determinate-nix-action
// always installs Determinate Nix, so a repository installing Nix only
// through it, with telemetry off, passes, whether a job runs it directly or
// through a local composite, and in whatever case the step names it, since
// GitHub matches owner and repository names without regard to case.
func TestEvaluate_nixInstaller_determinateActionPasses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		repo func() audit.Repo
	}{
		{"in a workflow", func() audit.Repo {
			return installerFixture(stepsWorkflow(".github/workflows/ci.yml", quiet()))
		}},
		{"in another case", func() audit.Repo {
			return installerFixture(stepsWorkflow(".github/workflows/ci.yml", audit.StepUse{
				Action: "determinatesystems/Determinate-Nix-Action@v3",
				Env:    map[string]string{"OTEL_SDK_DISABLED": "true"},
			}))
		}},
		{"in a composite", func() audit.Repo {
			r := installerFixture(stepsWorkflow(".github/workflows/ci.yml", step("./.github/actions/setup")))
			r.Actions = []audit.CompositeAction{setupComposite(quiet())}
			return r
		}},
		{"in every workflow", func() audit.Repo {
			return installerFixture(
				stepsWorkflow(".github/workflows/ci.yml", quiet()),
				stepsWorkflow(".github/workflows/release.yml", quiet()),
			)
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			wantCell(t, nixInstaller(t, tc.repo()), rules.Pass, "")
		})
	}
}

// TestEvaluate_nixInstaller_otherInstallerFails: any other installer action
// fails it, the older Determinate installer included, since the standard is
// the one action that installs nothing but Determinate Nix. The cell names
// the action, without its version.
func TestEvaluate_nixInstaller_otherInstallerFails(t *testing.T) {
	t.Parallel()

	tests := []struct {
		action string
		want   string
	}{
		{nixInstallerAction, "DeterminateSystems/nix-installer-action"},
		{installNixAction, "cachix/install-nix-action"},
		{nixQuickInstallAction, "nixbuild/nix-quick-install-action"},
		{"Cachix/Install-Nix-Action@v31", "cachix/install-nix-action"},
	}
	for _, tc := range tests {
		t.Run(tc.action, func(t *testing.T) {
			t.Parallel()

			r := installerFixture(stepsWorkflow(".github/workflows/ci.yml", step(tc.action)))
			wantCell(t, nixInstaller(t, r), rules.Fail, tc.want)
		})
	}
}

// TestEvaluate_nixInstaller_oneOtherInstallerIsEnough: every install must use
// the standard action, so one other installer fails it even beside the
// standard one, and an installer inside a composite counts as one in the
// workflow that calls it.
func TestEvaluate_nixInstaller_oneOtherInstallerIsEnough(t *testing.T) {
	t.Parallel()

	r := installerFixture(
		stepsWorkflow(".github/workflows/ci.yml", quiet()),
		stepsWorkflow(".github/workflows/release.yml", step("./.github/actions/setup")),
	)
	r.Actions = []audit.CompositeAction{setupComposite(step(installNixAction))}
	wantCell(t, nixInstaller(t, r), rules.Fail, "cachix/install-nix-action")
}

// TestEvaluate_nixInstaller_eachOtherInstallerOnce: the cell names each other
// installer once, in a fixed order, however many steps use it.
func TestEvaluate_nixInstaller_eachOtherInstallerOnce(t *testing.T) {
	t.Parallel()

	r := installerFixture(
		stepsWorkflow(".github/workflows/ci.yml", step(nixInstallerAction), step(installNixAction)),
		stepsWorkflow(".github/workflows/release.yml", step(installNixAction), step(nixInstallerAction)),
	)
	wantCell(t, nixInstaller(t, r), rules.Fail, "DeterminateSystems/nix-installer-action, cachix/install-nix-action")
}

// TestEvaluate_nixInstaller_unreadableFails: a workflow or composite that
// could not be read may hold an installer the check cannot see, so it fails
// naming the file, both when no installer was found elsewhere and when every
// one found is the standard action.
func TestEvaluate_nixInstaller_unreadableFails(t *testing.T) {
	t.Parallel()

	broken := audit.Workflow{Path: ".github/workflows/broken.yml", ParseError: "yaml: line 3: did not find expected key"}
	brokenSetup := audit.CompositeAction{Path: ".github/actions/setup/action.yml", ParseError: "yaml: line 2: mapping values are not allowed"}

	tests := []struct {
		name string
		repo func() audit.Repo
		want string
	}{
		{"an unreadable workflow alone", func() audit.Repo {
			return installerFixture(broken)
		}, "unreadable .github/workflows/broken.yml"},
		{"an unreadable workflow beside the standard action", func() audit.Repo {
			return installerFixture(stepsWorkflow(".github/workflows/ci.yml", quiet()), broken)
		}, "unreadable .github/workflows/broken.yml"},
		{"an unreadable composite", func() audit.Repo {
			r := installerFixture(stepsWorkflow(".github/workflows/ci.yml", step("./.github/actions/setup")))
			r.Actions = []audit.CompositeAction{brokenSetup}
			return r
		}, "unreadable .github/actions/setup/action.yml"},
		{"an unreadable composite used twice", func() audit.Repo {
			r := installerFixture(
				stepsWorkflow(".github/workflows/ci.yml", step("./.github/actions/setup")),
				stepsWorkflow(".github/workflows/release.yml", step("./.github/actions/setup")),
			)
			r.Actions = []audit.CompositeAction{brokenSetup}
			return r
		}, "unreadable .github/actions/setup/action.yml"},
		{"another installer and an unreadable workflow", func() audit.Repo {
			return installerFixture(stepsWorkflow(".github/workflows/ci.yml", step(installNixAction)), broken)
		}, "cachix/install-nix-action, unreadable .github/workflows/broken.yml"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			wantCell(t, nixInstaller(t, tc.repo()), rules.Fail, tc.want)
		})
	}
}

// TestEvaluate_nixInstaller_lookalikesAreNotInstallers: only the published
// installer actions themselves count; a path inside one, a longer repository
// name, or a reference with no version names a different action, and a local
// action is judged by its steps, not its name.
func TestEvaluate_nixInstaller_lookalikesAreNotInstallers(t *testing.T) {
	t.Parallel()

	for _, action := range []string{
		"someone/cachix/install-nix-action@v1",
		"cachix/install-nix-action-fork@v1",
		"cachix/install-nix-action",
		"./install-nix-action",
	} {
		t.Run(action, func(t *testing.T) {
			t.Parallel()

			r := installerFixture(stepsWorkflow(".github/workflows/ci.yml", quiet(), step(action)))
			wantCell(t, nixInstaller(t, r), rules.Pass, "")
		})
	}
}

// TestEvaluate_nixInstaller_telemetryOnFails: determinate-nix-action reports
// to Determinate Systems over OpenTelemetry unless the step's own env sets
// OTEL_SDK_DISABLED to true. Anything else leaves it on and fails, naming the
// file that holds the step: no env, another value, the variable on another
// step, an expression the audit cannot resolve, or an empty
// OTEL_EXPORTER_OTLP_ENDPOINT, the other off switch, which the standard does
// not use. The value is trimmed and read without regard to case, as the
// action's OpenTelemetry SDK reads it.
func TestEvaluate_nixInstaller_telemetryOnFails(t *testing.T) {
	t.Parallel()

	withEnv := func(env map[string]string) audit.StepUse {
		return audit.StepUse{Action: determinateNixAction, Env: env}
	}
	tests := []struct {
		name    string
		install audit.StepUse
		others  []audit.StepUse
	}{
		{"no env", step(determinateNixAction), nil},
		{"another variable only", withEnv(map[string]string{"FOO": "true"}), nil},
		{"false", withEnv(map[string]string{"OTEL_SDK_DISABLED": "false"}), nil},
		{"an expression", withEnv(map[string]string{"OTEL_SDK_DISABLED": "${{ vars.OTEL_SDK_DISABLED }}"}), nil},
		{"an empty exporter endpoint", withEnv(map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": ""}), nil},
		{"set on another step", step(determinateNixAction), []audit.StepUse{{
			Action: "actions/checkout@v5",
			Env:    map[string]string{"OTEL_SDK_DISABLED": "true"},
		}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := installerFixture(stepsWorkflow(".github/workflows/ci.yml", append(tc.others, tc.install)...))
			wantCell(t, nixInstaller(t, r), rules.Fail, "telemetry on in .github/workflows/ci.yml")
		})
	}

	for _, value := range []string{"TRUE", " true", "true\n"} {
		t.Run(value, func(t *testing.T) {
			t.Parallel()

			r := installerFixture(stepsWorkflow(".github/workflows/ci.yml", withEnv(map[string]string{"OTEL_SDK_DISABLED": value})))
			wantCell(t, nixInstaller(t, r), rules.Pass, "")
		})
	}
}

// TestEvaluate_nixInstaller_telemetryNamesEachFileOnce: the cell names the
// file holding each step with telemetry on, a composite rather than the
// workflows that call it, each once and in path order, after any other
// installer and before any unreadable file.
func TestEvaluate_nixInstaller_telemetryNamesEachFileOnce(t *testing.T) {
	t.Parallel()

	r := installerFixture(
		stepsWorkflow(".github/workflows/release.yml", step(determinateNixAction), step("./.github/actions/setup")),
		stepsWorkflow(".github/workflows/ci.yml", step("./.github/actions/setup"), quiet(), step(installNixAction)),
		stepsWorkflow(".github/workflows/fuzz.yml", step(determinateNixAction), step(determinateNixAction)),
		audit.Workflow{Path: ".github/workflows/broken.yml", ParseError: "yaml: line 3: did not find expected key"},
	)
	r.Actions = []audit.CompositeAction{setupComposite(step(determinateNixAction))}
	wantCell(t, nixInstaller(t, r), rules.Fail, "cachix/install-nix-action, "+
		"telemetry on in .github/actions/setup/action.yml, "+
		"telemetry on in .github/workflows/fuzz.yml, "+
		"telemetry on in .github/workflows/release.yml, "+
		"unreadable .github/workflows/broken.yml")
}
