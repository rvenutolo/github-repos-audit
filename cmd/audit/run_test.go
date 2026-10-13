package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
	"github.com/rvenutolo/github-repos-audit/internal/render"
	"github.com/rvenutolo/github-repos-audit/internal/rules"
)

// noEnv is a getenv that reports every variable as unset.
func noEnv(string) string { return "" }

// exec drives run with buffers, the way main drives it with the real streams.
func exec(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errOut bytes.Buffer
	err = run(t.Context(), args, noEnv, strings.NewReader(""), &out, &errOut)
	return out.String(), errOut.String(), err
}

func TestRun_help(t *testing.T) {
	t.Parallel()

	for _, arg := range []string{"-h", "--help", "help"} {
		t.Run(arg, func(t *testing.T) {
			t.Parallel()

			stdout, _, err := exec(t, arg)
			if err != nil {
				t.Errorf("run(%q) error = %v, want nil", arg, err)
			}
			if !strings.Contains(stdout, "Usage:") {
				t.Errorf("run(%q) stdout = %q, want it to contain the usage block", arg, stdout)
			}
			// Every exit code the program can produce is documented in --help,
			// and exitCode is written against that list. A code that appears
			// in one and not the other is a lie to the caller.
			for _, code := range []string{"0", "1", "2", "130"} {
				if !strings.Contains(stdout, "  "+code+" ") {
					t.Errorf("run(%q) help does not document exit code %s", arg, code)
				}
			}
		})
	}
}

func TestRun_version(t *testing.T) {
	t.Parallel()

	stdout, _, err := exec(t, "--version")
	if err != nil {
		t.Fatalf("run(--version) error = %v, want nil", err)
	}
	if strings.TrimSpace(stdout) != version {
		t.Errorf("run(--version) stdout = %q, want %q", strings.TrimSpace(stdout), version)
	}
}

func TestRun_usageErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		// wantUsage is whether the whole usage block belongs on stderr: it
		// does when the caller got the command itself wrong, and not for a
		// bad flag, where the subcommand's own flag summary is the answer.
		wantUsage bool
	}{
		{name: "no command", args: nil, wantUsage: true},
		{name: "unknown command", args: []string{"frobnicate"}, wantUsage: true},
		{name: "unknown flag", args: []string{"validate", "--nonesuch"}},
		{name: "unexpected argument", args: []string{"validate", "extra"}},
		{name: "stray render argument", args: []string{"render", "extra"}},
		{name: "stray json argument", args: []string{"json", "extra"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, stderr, err := exec(t, tc.args...)
			if !errors.Is(err, errUsage) {
				t.Fatalf("run(%v) error = %v, want it to wrap errUsage", tc.args, err)
			}
			if got := exitCode(err); got != 2 {
				t.Errorf("exitCode(%v) = %d, want 2", err, got)
			}
			if tc.wantUsage && !strings.Contains(stderr, "Usage:") {
				t.Errorf("run(%v) stderr = %q, want it to contain the usage block", tc.args, stderr)
			}
		})
	}
}

// TestRunValidate_acceptsTheExample guards examples/repos.toml, the only
// configuration this repository validates.
func TestRunValidate_acceptsTheExample(t *testing.T) {
	t.Parallel()

	stdout, _, err := exec(t, "validate", "--config", "../../examples/repos.toml")
	if err != nil {
		t.Fatalf("validate examples/repos.toml: %v", err)
	}
	if !strings.Contains(stdout, "no problems found") {
		t.Errorf("stdout = %q, want the no-problems line", stdout)
	}
}

// TestRunValidate_rejectsTheInvalidExample keeps the failure path honest: the
// example exists so the gate proves validate still refuses a dead override,
// not merely that it accepts a good file.
func TestRunValidate_rejectsTheInvalidExample(t *testing.T) {
	t.Parallel()

	_, _, err := exec(t, "validate", "--config", "../../examples/repos.invalid.toml")
	if err == nil {
		t.Fatal("validate examples/repos.invalid.toml: error = nil, want a configuration error")
	}
	if got := err.Error(); !strings.Contains(got, "is dead") {
		t.Errorf("validate error = %q, want it to name the dead override", got)
	}
}

func TestRun_validateRejectsABadConfigAsARuntimeError(t *testing.T) {
	t.Parallel()

	// A malformed config exits 1, not 2: the invocation was well-formed, the
	// data was not. 2 is reserved for the caller getting the command line
	// wrong.
	path := filepath.Join(t.TempDir(), "repos.toml")
	if err := os.WriteFile(path, []byte("[repos.alpha]\ntype = \"widget\"\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	_, _, err := exec(t, "validate", "--config", path)
	if err == nil {
		t.Fatal("run(validate) error = nil, want a config error")
	}
	if errors.Is(err, errUsage) {
		t.Errorf("run(validate) error = %v, want a runtime error rather than a usage error", err)
	}
	if got := exitCode(err); got != 1 {
		t.Errorf("exitCode(%v) = %d, want 1", err, got)
	}
}

func TestRun_validateReportsAMissingFile(t *testing.T) {
	t.Parallel()

	_, _, err := exec(t, "validate", "--config", filepath.Join(t.TempDir(), "absent.toml"))
	if err == nil {
		t.Fatal("run(validate) error = nil, want a not-exist error")
	}
	if got := exitCode(err); got != 1 {
		t.Errorf("exitCode(%v) = %d, want 1", err, got)
	}
}

func TestRun_subcommandHelpIsNotAnError(t *testing.T) {
	t.Parallel()

	for _, cmd := range []string{"render", "json", "validate"} {
		t.Run(cmd, func(t *testing.T) {
			t.Parallel()

			stdout, _, err := exec(t, cmd, "-h")
			if err != nil {
				t.Errorf("run(%s -h) error = %v, want nil", cmd, err)
			}
			if !strings.Contains(stdout, "-config") {
				t.Errorf("run(%s -h) stdout = %q, want the flag list", cmd, stdout)
			}
		})
	}
}

func TestExitCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "success", err: nil, want: 0},
		{name: "usage", err: errUsage, want: 2},
		{name: "wrapped usage", err: errors.New("x: " + errUsage.Error()), want: 1},
		{name: "interrupted", err: context.Canceled, want: 130},
		{name: "runtime", err: errors.New("boom"), want: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := exitCode(tc.err); got != tc.want {
				t.Errorf("exitCode(%v) = %d, want %d", tc.err, got, tc.want)
			}
		})
	}
}

// fakeCollector is a hand-rolled stand-in for the GitHub client, so the render
// and json paths are testable without a token or a network. Like the real
// client it reports the collected count at info level through the logger the
// factory hands it, so a test can tell --verbose apart from the default.
type fakeCollector struct {
	names       []string
	repos       []audit.Repo
	discoverErr error
	collectErr  error
	log         *slog.Logger
	// asked is the names the last Collect call was given; nil if Collect was
	// never called. A test reads it to learn what the program chose to read.
	asked []string
}

func (f *fakeCollector) Discover(context.Context) ([]string, error) {
	return f.names, f.discoverErr
}

// Collect answers, like the real client, only for the names it is given: a
// repository the program did not ask about is not read.
func (f *fakeCollector) Collect(ctx context.Context, names []string) ([]audit.Repo, error) {
	f.asked = slices.Clone(names)
	repos := make([]audit.Repo, 0, len(names))
	for _, r := range f.repos {
		if slices.Contains(names, r.Name) {
			repos = append(repos, r)
		}
	}
	if f.log != nil {
		f.log.InfoContext(ctx, "collected repositories", "count", len(repos))
	}
	return repos, f.collectErr
}

// Owner answers with the fake account every render test is written against.
func (f *fakeCollector) Owner() string { return "gh-owner" }

// fixedClock is the instant every test in this file renders against.
func fixedClock() time.Time { return time.Date(2026, 9, 5, 7, 30, 0, 0, time.UTC) }

// scratchRepo builds a repository that passes nothing in particular; the tests
// here are about wiring, not verdicts.
func scratchRepo(name string) audit.Repo {
	return audit.Repo{
		Name:       name,
		Visibility: "private",
		PushedAt:   time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC),
		Branch:     audit.BranchRules{Known: true, Types: []string{"pull_request"}},
	}
}

// scratchProject writes a repos.toml — the standard types fixture followed by
// the given [repos.*] tables — and a README into a temp directory, and returns
// the two paths.
func scratchProject(t *testing.T, repos string) (dir, configPath string) {
	t.Helper()
	types, err := os.ReadFile(filepath.Join("..", "..", "internal", "rules", "testdata", "standard-types.toml"))
	if err != nil {
		t.Fatalf("read the standard types fixture: %v", err)
	}
	dir = t.TempDir()
	configPath = filepath.Join(dir, "repos.toml")
	if err := os.WriteFile(configPath, append(types, "\n"+repos...), 0o600); err != nil {
		t.Fatalf("write repos.toml: %v", err)
	}
	readme := "# scratch\n\n" + render.BeginMarker + "\n\nnothing yet\n\n" + render.EndMarker + "\n"
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte(readme), 0o600); err != nil {
		t.Fatalf("write README.md: %v", err)
	}
	return dir, configPath
}

const scratchConfig = "[repos.alpha]\ntype = \"tools\"\n\n[repos.bravo]\ntype = \"content\"\n"

// healthyFake is a collector that answers for both scratchConfig repositories
// without error, for the tests where collection is not the point.
func healthyFake() *fakeCollector {
	return &fakeCollector{
		names: []string{"alpha", "bravo"},
		repos: []audit.Repo{scratchRepo("alpha"), scratchRepo("bravo")},
	}
}

func fakeFactory(f *fakeCollector) newCollector {
	return func(_ context.Context, _ func(string) string, logger *slog.Logger) (collector, error) {
		f.log = logger
		return f, nil
	}
}

func TestRunRender_writesBothArtefactsAndReportsTheChange(t *testing.T) {
	t.Parallel()

	dir, configPath := scratchProject(t, scratchConfig)
	fake := &fakeCollector{
		names: []string{"alpha", "bravo"},
		repos: []audit.Repo{scratchRepo("alpha"), scratchRepo("bravo")},
	}

	var stdout, stderr bytes.Buffer
	err := runRender(t.Context(), []string{"--config", configPath, "--dir", dir},
		noEnv, &stdout, &stderr, fakeFactory(fake), fixedClock)
	if err != nil {
		t.Fatalf("runRender() error = %v, want nil", err)
	}

	// The one line the refresh workflow appends to $GITHUB_OUTPUT.
	if got := stdout.String(); got != "material-change=true\n" {
		t.Errorf("stdout = %q, want the material-change line alone", got)
	}
	for _, name := range []string{"README.md", "audit.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s was not written: %v", name, err)
		}
	}

	// Running again over its own output is a no-op; render.volatileFields
	// says why that matters.
	stdout.Reset()
	err = runRender(t.Context(), []string{"--config", configPath, "--dir", dir},
		noEnv, &stdout, &stderr, fakeFactory(fake), fixedClock)
	if err != nil {
		t.Fatalf("runRender() error = %v, want nil", err)
	}
	if got := stdout.String(); got != "material-change=false\n" {
		t.Errorf("stdout = %q, want material-change=false on an unchanged rerun", got)
	}
}

func TestRunRender_recordsTheOwner(t *testing.T) {
	t.Parallel()

	dir, configPath := scratchProject(t, scratchConfig)
	var stdout, stderr bytes.Buffer
	if err := runRender(t.Context(), []string{"--config", configPath, "--dir", dir},
		noEnv, &stdout, &stderr, fakeFactory(healthyFake()), fixedClock); err != nil {
		t.Fatalf("runRender() error = %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "audit.json"))
	if err != nil {
		t.Fatalf("read audit.json: %v", err)
	}
	var snap audit.Snapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		t.Fatalf("decode audit.json: %v", err)
	}
	if snap.Owner != "gh-owner" {
		t.Errorf("audit.json owner = %q, want %q", snap.Owner, "gh-owner")
	}
}

// TestRunJSON_recordsTheIdentityStandard: the [identity] table reaches the
// snapshot beside the types, so audit.json says which identity the history
// was held to, and the rules have a standard to judge git_identity against.
func TestRunJSON_recordsTheIdentityStandard(t *testing.T) {
	t.Parallel()

	_, configPath := scratchProject(t, scratchConfig)
	var stdout, stderr bytes.Buffer
	if err := runJSON(t.Context(), []string{"--config", configPath},
		noEnv, &stdout, &stderr, fakeFactory(healthyFake()), fixedClock); err != nil {
		t.Fatalf("runJSON() error = %v, want nil", err)
	}
	var snap audit.Snapshot
	if err := json.Unmarshal(stdout.Bytes(), &snap); err != nil {
		t.Fatalf("stdout is not valid JSON: %v", err)
	}
	// standard-types.toml's [identity] table, which scratchProject copies.
	want := audit.IdentityStandard{
		Canonical: "Pat Example <pat@example.com>",
		Match:     " Example <",
		Accepted:  []string{"Pat Example <12345+pat@users.noreply.github.com>"},
	}
	if diff := cmp.Diff(want, snap.Identity); diff != "" {
		t.Errorf("snapshot identity mismatch (-want +got):\n%s", diff)
	}
}

// TestRunRender_aTypesOnlyEditIsAMaterialChange: changing what a type expects
// changes verdicts, so it must open a pull request even when no repository
// changed.
func TestRunRender_aTypesOnlyEditIsAMaterialChange(t *testing.T) {
	t.Parallel()

	dir, configPath := scratchProject(t, scratchConfig)
	fake := healthyFake()
	var stdout, stderr bytes.Buffer
	args := []string{"--config", configPath, "--dir", dir}
	if err := runRender(t.Context(), args, noEnv, &stdout, &stderr, fakeFactory(fake), fixedClock); err != nil {
		t.Fatalf("runRender() error = %v, want nil", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read repos.toml: %v", err)
	}
	// The first tools line in the fixture is its readme; relax it.
	edited := strings.Replace(string(data), "[types.tools]\nreadme = \"required\"", "[types.tools]\nreadme = \"not_required\"", 1)
	if edited == string(data) {
		t.Fatal("the fixture's [types.tools] table no longer starts with readme; update this test")
	}
	if err := os.WriteFile(configPath, []byte(edited), 0o600); err != nil {
		t.Fatalf("write repos.toml: %v", err)
	}

	stdout.Reset()
	if err := runRender(t.Context(), args, noEnv, &stdout, &stderr, fakeFactory(fake), fixedClock); err != nil {
		t.Fatalf("runRender() error = %v, want nil", err)
	}
	if got := stdout.String(); got != "material-change=true\n" {
		t.Errorf("stdout = %q, want material-change=true after a types-only edit", got)
	}
}

func TestRun_logLevelFlags(t *testing.T) {
	t.Parallel()

	levels := []struct {
		name string
		// flags is appended to the ordinary invocation.
		flags []string
		// wantProgress is whether the info line reporting the collected
		// count should reach stderr.
		wantProgress bool
		// wantStart is whether the one debug line the command emits on
		// start-up should reach stderr.
		wantStart bool
		// wantSilent is whether stderr should carry nothing at all: the
		// default level shows warnings alone, and a healthy run has none.
		wantSilent bool
	}{
		{name: "quiet by default", flags: nil, wantProgress: false, wantStart: false, wantSilent: true},
		{name: "verbose stops short of debug", flags: []string{"--verbose"}, wantProgress: true, wantStart: false},
		{name: "debug shows the start line", flags: []string{"--debug"}, wantProgress: true, wantStart: true},
		{name: "debug wins over verbose", flags: []string{"--verbose", "--debug"}, wantProgress: true, wantStart: true},
	}
	commands := []struct {
		name string
		run  func(t *testing.T, flags []string, stderr io.Writer) error
	}{
		{
			name: "render",
			run: func(t *testing.T, flags []string, stderr io.Writer) error {
				t.Helper()
				dir, configPath := scratchProject(t, scratchConfig)
				args := slices.Concat([]string{"--config", configPath, "--dir", dir}, flags)
				return runRender(t.Context(), args, noEnv, io.Discard, stderr, fakeFactory(healthyFake()), fixedClock)
			},
		},
		{
			name: "json",
			run: func(t *testing.T, flags []string, stderr io.Writer) error {
				t.Helper()
				_, configPath := scratchProject(t, scratchConfig)
				args := slices.Concat([]string{"--config", configPath}, flags)
				return runJSON(t.Context(), args, noEnv, io.Discard, stderr, fakeFactory(healthyFake()), fixedClock)
			},
		},
	}
	for _, cmd := range commands {
		for _, tc := range levels {
			t.Run(cmd.name+" "+tc.name, func(t *testing.T) {
				t.Parallel()

				var stderr bytes.Buffer
				if err := cmd.run(t, tc.flags, &stderr); err != nil {
					t.Fatalf("%s %v error = %v, want nil", cmd.name, tc.flags, err)
				}

				// stderr is part of the contract: by default a caller sees
				// nothing there but warnings, --verbose adds the progress
				// counts, and --debug is what turns the start-up line on.
				// The start-up line names the command so a log from the
				// refresh workflow says which path produced it.
				got := stderr.String()
				if strings.Contains(got, "collected repositories") != tc.wantProgress {
					t.Errorf("stderr = %q, want a collected line: %t", got, tc.wantProgress)
				}
				if strings.Contains(got, "starting") != tc.wantStart {
					t.Errorf("stderr = %q, want a starting line: %t", got, tc.wantStart)
				}
				if tc.wantStart && !strings.Contains(got, "command="+cmd.name) {
					t.Errorf("stderr = %q, want it to name the command %q", got, cmd.name)
				}
				if tc.wantSilent && got != "" {
					t.Errorf("stderr = %q, want it empty", got)
				}
			})
		}
	}
}

// staleConfig is scratchConfig plus an entry for a repository the fake
// account never has.
const staleConfig = scratchConfig + "\n[repos.retired]\ntype = \"tools\"\n"

// driftFake is an account holding both scratchConfig repositories and one
// more, fresh, that no config in this file names. It can answer for fresh, so
// a test can tell "not asked" from "could not answer".
func driftFake() *fakeCollector {
	return &fakeCollector{
		names: []string{"alpha", "bravo", "fresh"},
		repos: []audit.Repo{scratchRepo("alpha"), scratchRepo("bravo"), scratchRepo("fresh")},
	}
}

// rendered is what one render run left behind.
type rendered struct {
	stdout, stderr string
	readme         string
	// raw is audit.json as written; snap is the same bytes decoded.
	raw  string
	snap audit.Snapshot
}

// renderInto runs render over dir and reads back everything it wrote.
func renderInto(t *testing.T, dir, configPath string, fake *fakeCollector) rendered {
	t.Helper()

	var stdout, stderr bytes.Buffer
	if err := runRender(t.Context(), []string{"--config", configPath, "--dir", dir},
		noEnv, &stdout, &stderr, fakeFactory(fake), fixedClock); err != nil {
		t.Fatalf("runRender() error = %v, want nil", err)
	}
	readme, err := os.ReadFile(filepath.Join(dir, "README.md"))
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "audit.json"))
	if err != nil {
		t.Fatalf("read audit.json: %v", err)
	}
	out := rendered{stdout: stdout.String(), stderr: stderr.String(), readme: string(readme), raw: string(raw)}
	if err := json.Unmarshal(raw, &out.snap); err != nil {
		t.Fatalf("audit.json is not valid JSON: %v", err)
	}
	return out
}

// repoNames is the names of a snapshot's audited repositories, in order.
func repoNames(snap audit.Snapshot) []string {
	names := make([]string, 0, len(snap.Repos))
	for _, r := range snap.Repos {
		names = append(names, r.Name)
	}
	return names
}

// TestRunRender_reportsAnUnconfiguredRepository: creating a repository is
// routine, and it must not cost every other repository its report. The run
// succeeds, audits what repos.toml names, and says in the report itself —
// where the owner looks — which repository needs an entry.
func TestRunRender_reportsAnUnconfiguredRepository(t *testing.T) {
	t.Parallel()

	dir, configPath := scratchProject(t, scratchConfig)
	fake := driftFake()
	got := renderInto(t, dir, configPath, fake)

	// Material, or the refresh workflow never commits the report that says so.
	if got.stdout != "material-change=true\n" {
		t.Errorf("stdout = %q, want the material-change line alone, true", got.stdout)
	}
	if !strings.Contains(got.readme, "## Configuration drift") {
		t.Errorf("README.md has no Configuration drift section:\n%s", got.readme)
	}
	if want := "[fresh](https://github.com/gh-owner/fresh)"; !strings.Contains(got.readme, want) {
		t.Errorf("README.md does not name the unconfigured repository as %s", want)
	}
	for _, name := range []string{"alpha", "bravo"} {
		if !strings.Contains(got.readme, "["+name+"](https://github.com/gh-owner/"+name+")") {
			t.Errorf("README.md dropped the configured repository %s", name)
		}
	}
	if diff := cmp.Diff(audit.ConfigDrift{Unconfigured: []string{"fresh"}}, got.snap.Drift); diff != "" {
		t.Errorf("audit.json config_drift mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"alpha", "bravo"}, repoNames(got.snap)); diff != "" {
		t.Errorf("audit.json repos mismatch (-want +got):\n%s", diff)
	}
	// With no type there is nothing to judge it against, so it is not read.
	if diff := cmp.Diff([]string{"alpha", "bravo"}, fake.asked); diff != "" {
		t.Errorf("collected names mismatch (-want +got):\n%s", diff)
	}
	// Someone reading the workflow run, rather than the report, is told too.
	if !strings.Contains(got.stderr, "level=WARN") || !strings.Contains(got.stderr, "fresh") {
		t.Errorf("stderr = %q, want a warning naming fresh", got.stderr)
	}

	// The same drift the next night is not news: no pull request over it.
	again := renderInto(t, dir, configPath, fake)
	if again.stdout != "material-change=false\n" {
		t.Errorf("stdout = %q, want material-change=false on an unchanged rerun", again.stdout)
	}
}

// TestRunRender_reportsAStaleEntry: archiving or deleting a repository is as
// routine as creating one, and is treated the same way.
func TestRunRender_reportsAStaleEntry(t *testing.T) {
	t.Parallel()

	dir, configPath := scratchProject(t, staleConfig)
	fake := healthyFake()
	got := renderInto(t, dir, configPath, fake)

	if got.stdout != "material-change=true\n" {
		t.Errorf("stdout = %q, want the material-change line alone, true", got.stdout)
	}
	if !strings.Contains(got.readme, "## Configuration drift") || !strings.Contains(got.readme, "`retired`") {
		t.Errorf("README.md does not report the stale entry retired:\n%s", got.readme)
	}
	if diff := cmp.Diff(audit.ConfigDrift{StaleEntries: []string{"retired"}}, got.snap.Drift); diff != "" {
		t.Errorf("audit.json config_drift mismatch (-want +got):\n%s", diff)
	}
	// A stale entry describes nothing that exists, so it is not a repository.
	if diff := cmp.Diff([]string{"alpha", "bravo"}, repoNames(got.snap)); diff != "" {
		t.Errorf("audit.json repos mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"alpha", "bravo"}, fake.asked); diff != "" {
		t.Errorf("collected names mismatch (-want +got):\n%s", diff)
	}
	if !strings.Contains(got.stderr, "level=WARN") || !strings.Contains(got.stderr, "retired") {
		t.Errorf("stderr = %q, want a warning naming retired", got.stderr)
	}
}

// TestRunRender_reportsBothDirectionsOfDrift is what a rename looks like: the
// new name has no entry and the old entry has no repository, in one run.
func TestRunRender_reportsBothDirectionsOfDrift(t *testing.T) {
	t.Parallel()

	dir, configPath := scratchProject(t, staleConfig)
	got := renderInto(t, dir, configPath, driftFake())

	want := audit.ConfigDrift{Unconfigured: []string{"fresh"}, StaleEntries: []string{"retired"}}
	if diff := cmp.Diff(want, got.snap.Drift); diff != "" {
		t.Errorf("audit.json config_drift mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"alpha", "bravo"}, repoNames(got.snap)); diff != "" {
		t.Errorf("audit.json repos mismatch (-want +got):\n%s", diff)
	}
	for _, name := range []string{"fresh", "retired"} {
		if !strings.Contains(got.readme, name) {
			t.Errorf("README.md does not name %s", name)
		}
		if !strings.Contains(got.stderr, name) {
			t.Errorf("stderr = %q, want a warning naming %s", got.stderr, name)
		}
	}
}

// TestRunRender_aMatchingConfigLeavesNoTraceOfDrift: audit.json is committed
// and diffed, so an account with nothing to report gains no key, no section
// and no warning.
func TestRunRender_aMatchingConfigLeavesNoTraceOfDrift(t *testing.T) {
	t.Parallel()

	dir, configPath := scratchProject(t, scratchConfig)
	got := renderInto(t, dir, configPath, healthyFake())

	if strings.Contains(got.raw, "config_drift") {
		t.Error("audit.json carries a config_drift key for a matching config")
	}
	if strings.Contains(got.readme, "Configuration drift") {
		t.Error("README.md carries a drift section for a matching config")
	}
	if got.stderr != "" {
		t.Errorf("stderr = %q, want it empty", got.stderr)
	}
}

// TestRunRender_failsWhenNoDeclaredRepositoryExists: with nothing in common
// there is nothing to audit. That is a config pointed at the wrong account or
// a token that sees nothing, and an empty report would hide it.
func TestRunRender_failsWhenNoDeclaredRepositoryExists(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		names []string
		// counts are the two phrases that tell a token that sees nothing
		// from a config pointed at the wrong account.
		counts []string
	}{
		{name: "nothing discovered", names: nil, counts: []string{"the account has 0", "repos.toml declares 2"}},
		{name: "nothing in common", names: []string{"fresh"}, counts: []string{"the account has 1", "repos.toml declares 2"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dir, configPath := scratchProject(t, scratchConfig)
			before, err := os.ReadFile(filepath.Join(dir, "README.md"))
			if err != nil {
				t.Fatalf("read README.md: %v", err)
			}
			fake := &fakeCollector{names: tc.names, repos: []audit.Repo{scratchRepo("fresh")}}

			var stdout, stderr bytes.Buffer
			err = runRender(t.Context(), []string{"--config", configPath, "--dir", dir},
				noEnv, &stdout, &stderr, fakeFactory(fake), fixedClock)
			if !errors.Is(err, rules.ErrConfig) {
				t.Fatalf("runRender() error = %v, want it to wrap rules.ErrConfig", err)
			}
			for _, want := range tc.counts {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error = %q, want it to contain %q", err, want)
				}
			}
			if got := exitCode(err); got != 1 {
				t.Errorf("exitCode = %d, want 1", got)
			}
			if stdout.Len() != 0 {
				t.Errorf("stdout = %q, want nothing: there is no verdict to report", stdout.String())
			}
			if fake.asked != nil {
				t.Errorf("Collect was asked for %v, want no collection at all", fake.asked)
			}
			after, err := os.ReadFile(filepath.Join(dir, "README.md"))
			if err != nil {
				t.Fatalf("read README.md: %v", err)
			}
			if string(before) != string(after) {
				t.Error("runRender() rewrote README.md despite failing")
			}
			if _, err := os.Stat(filepath.Join(dir, "audit.json")); err == nil {
				t.Error("runRender() wrote audit.json despite failing")
			}
		})
	}
}

// TestRunValidate_rejectsAKeyThatCannotBeARepositoryName: the offline gate is
// where a mistyped [repos.<key>] is caught, so it never reaches a report.
func TestRunValidate_rejectsAKeyThatCannotBeARepositoryName(t *testing.T) {
	t.Parallel()

	_, configPath := scratchProject(t, scratchConfig+"\n[repos.\"my repo\"]\ntype = \"tools\"\n")

	var stdout, stderr bytes.Buffer
	err := runValidate([]string{"--config", configPath}, &stdout, &stderr)
	if err == nil {
		t.Fatal("runValidate() error = nil, want the key refused")
	}
	if got := exitCode(err); got != 1 {
		t.Errorf("exitCode = %d, want 1", got)
	}
	if want := `"my repo"`; !strings.Contains(err.Error(), want) {
		t.Errorf("runValidate() error = %q, want it to name the entry %s", err.Error(), want)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want no all-clear line", stdout.String())
	}
}

// TestRunJSON_carriesTheDrift: json prints the snapshot render would write,
// so it tolerates and records the same disagreement.
func TestRunJSON_carriesTheDrift(t *testing.T) {
	t.Parallel()

	_, configPath := scratchProject(t, staleConfig)

	var stdout, stderr bytes.Buffer
	if err := runJSON(t.Context(), []string{"--config", configPath},
		noEnv, &stdout, &stderr, fakeFactory(driftFake()), fixedClock); err != nil {
		t.Fatalf("runJSON() error = %v, want nil", err)
	}
	var snap audit.Snapshot
	if err := json.Unmarshal(stdout.Bytes(), &snap); err != nil {
		t.Fatalf("stdout is not valid JSON: %v", err)
	}
	want := audit.ConfigDrift{Unconfigured: []string{"fresh"}, StaleEntries: []string{"retired"}}
	if diff := cmp.Diff(want, snap.Drift); diff != "" {
		t.Errorf("config_drift mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"alpha", "bravo"}, repoNames(snap)); diff != "" {
		t.Errorf("repos mismatch (-want +got):\n%s", diff)
	}
}

func TestRunRender_leavesTheReadmeAloneWhenCollectionFails(t *testing.T) {
	t.Parallel()

	dir, configPath := scratchProject(t, scratchConfig)
	before, err := os.ReadFile(filepath.Join(dir, "README.md"))
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	fake := &fakeCollector{names: []string{"alpha", "bravo"}, collectErr: errors.New("boom")}

	var stdout, stderr bytes.Buffer
	if err := runRender(t.Context(), []string{"--config", configPath, "--dir", dir},
		noEnv, &stdout, &stderr, fakeFactory(fake), fixedClock); err == nil {
		t.Fatal("runRender() error = nil, want the collection error")
	}

	// A failure before the write leaves both artefacts untouched.
	after, err := os.ReadFile(filepath.Join(dir, "README.md"))
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	if string(before) != string(after) {
		t.Error("runRender() rewrote README.md despite failing")
	}
	if _, err := os.Stat(filepath.Join(dir, "audit.json")); err == nil {
		t.Error("runRender() wrote audit.json despite failing")
	}
}

func TestRunRender_reportsAReadmeWithoutMarkers(t *testing.T) {
	t.Parallel()

	dir, configPath := scratchProject(t, scratchConfig)
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# no markers\n"), 0o600); err != nil {
		t.Fatalf("write README.md: %v", err)
	}
	fake := &fakeCollector{
		names: []string{"alpha", "bravo"},
		repos: []audit.Repo{scratchRepo("alpha"), scratchRepo("bravo")},
	}

	var stdout, stderr bytes.Buffer
	err := runRender(t.Context(), []string{"--config", configPath, "--dir", dir},
		noEnv, &stdout, &stderr, fakeFactory(fake), fixedClock)
	if !errors.Is(err, render.ErrMarkers) {
		t.Fatalf("runRender() error = %v, want it to wrap render.ErrMarkers", err)
	}
}

func TestRunJSON_printsTheSnapshotAndWritesNothing(t *testing.T) {
	t.Parallel()

	dir, configPath := scratchProject(t, scratchConfig)
	fake := &fakeCollector{
		names: []string{"alpha", "bravo"},
		repos: []audit.Repo{scratchRepo("alpha"), scratchRepo("bravo")},
	}

	var stdout, stderr bytes.Buffer
	if err := runJSON(t.Context(), []string{"--config", configPath},
		noEnv, &stdout, &stderr, fakeFactory(fake), fixedClock); err != nil {
		t.Fatalf("runJSON() error = %v, want nil", err)
	}

	var snap audit.Snapshot
	if err := json.Unmarshal(stdout.Bytes(), &snap); err != nil {
		t.Fatalf("stdout is not valid JSON: %v", err)
	}
	if len(snap.Repos) != 2 {
		t.Errorf("snapshot has %d repositories, want 2", len(snap.Repos))
	}
	if _, err := os.Stat(filepath.Join(dir, "audit.json")); err == nil {
		t.Error("json wrote audit.json; it should write nothing")
	}
}

func TestRunRender_rejectsADeadOverrideBeforeSpendingAnAPICall(t *testing.T) {
	t.Parallel()

	// tools already expects a flake, so requiring one says nothing.
	dir, configPath := scratchProject(t, "[repos.alpha]\ntype = \"tools\"\noverrides = { flake_nix = \"required\" }\n")
	fake := &fakeCollector{discoverErr: errors.New("this should never be called")}

	var stdout, stderr bytes.Buffer
	err := runRender(t.Context(), []string{"--config", configPath, "--dir", dir},
		noEnv, &stdout, &stderr, fakeFactory(fake), fixedClock)
	if err == nil {
		t.Fatal("runRender() error = nil, want a dead-override error")
	}
	if !strings.Contains(err.Error(), "is dead") {
		t.Errorf("runRender() error = %q, want the offline validation to have run first", err)
	}
}

// TestCollectorAt_wiresTheTokenThroughToTheOwner exercises collectorAt's seam
// against a fake server answering /user: with a token from the environment,
// the owner it names is the client's Owner. See collectorAt for why the seam
// exists.
func TestCollectorAt_wiresTheTokenThroughToTheOwner(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/user" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if _, err := w.Write([]byte(`{"login":"gh-owner"}`)); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	env := func(key string) string {
		if key == "GITHUB_TOKEN" {
			return "ghp_notarealtoken"
		}
		return ""
	}

	c, err := collectorAt(t.Context(), server.URL, env, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("collectorAt() error = %v, want nil", err)
	}
	if got := c.Owner(); got != "gh-owner" {
		t.Errorf("Owner() = %q, want %q", got, "gh-owner")
	}
}

func TestRunRender_reportsAFailureToBuildTheClient(t *testing.T) {
	t.Parallel()

	_, configPath := scratchProject(t, scratchConfig)
	failing := func(context.Context, func(string) string, *slog.Logger) (collector, error) {
		return nil, errors.New("no token")
	}

	var stdout, stderr bytes.Buffer
	if err := runRender(t.Context(), []string{"--config", configPath},
		noEnv, &stdout, &stderr, failing, fixedClock); err == nil {
		t.Fatal("runRender() error = nil, want the client error")
	}
	if err := runJSON(t.Context(), []string{"--config", configPath},
		noEnv, &stdout, &stderr, failing, fixedClock); err == nil {
		t.Fatal("runJSON() error = nil, want the client error")
	}
}

func TestRunRender_reportsAMissingReadme(t *testing.T) {
	t.Parallel()

	dir, configPath := scratchProject(t, scratchConfig)
	if err := os.Remove(filepath.Join(dir, "README.md")); err != nil {
		t.Fatalf("remove README.md: %v", err)
	}
	fake := &fakeCollector{
		names: []string{"alpha", "bravo"},
		repos: []audit.Repo{scratchRepo("alpha"), scratchRepo("bravo")},
	}

	var stdout, stderr bytes.Buffer
	err := runRender(t.Context(), []string{"--config", configPath, "--dir", dir},
		noEnv, &stdout, &stderr, fakeFactory(fake), fixedClock)
	if err == nil {
		t.Fatal("runRender() error = nil, want a read error")
	}
	// render is not a bootstrap: the README carries hand-written prose above
	// the markers, so creating one would be inventing content.
	if !strings.Contains(err.Error(), "README.md") {
		t.Errorf("runRender() error = %q, want it to name the file", err)
	}
}

func TestRunRender_reportsADiscoveryFailure(t *testing.T) {
	t.Parallel()

	dir, configPath := scratchProject(t, scratchConfig)
	fake := &fakeCollector{discoverErr: errors.New("502 from GitHub")}

	var stdout, stderr bytes.Buffer
	err := runRender(t.Context(), []string{"--config", configPath, "--dir", dir},
		noEnv, &stdout, &stderr, fakeFactory(fake), fixedClock)
	if err == nil {
		t.Fatal("runRender() error = nil, want the discovery error")
	}
	if exitCode(err) != 1 {
		t.Errorf("exitCode = %d, want 1", exitCode(err))
	}
}

func TestRunRender_reportsALiveOnlyConfigError(t *testing.T) {
	t.Parallel()

	// published on a private repository, which rules.Validate rejects; see its
	// comment for why.
	dir, configPath := scratchProject(t, "[repos.alpha]\ntype = \"tools\"\npublished = true\n")
	fake := &fakeCollector{names: []string{"alpha"}, repos: []audit.Repo{scratchRepo("alpha")}}

	var stdout, stderr bytes.Buffer
	err := runRender(t.Context(), []string{"--config", configPath, "--dir", dir},
		noEnv, &stdout, &stderr, fakeFactory(fake), fixedClock)
	if err == nil {
		t.Fatal("runRender() error = nil, want a published-but-private error")
	}
	if !strings.Contains(err.Error(), "published = true") {
		t.Errorf("runRender() error = %q, want it to name the combination", err)
	}
}

// TestRunValidate_rejectsADeadOverride is the half of validation the parser
// cannot do: repos.toml is well-formed and every key is known, and the
// override still says nothing the type does not already say. Catching it here
// is what stops a dead line rotting in the file until a type changes under it.
func TestRunValidate_rejectsADeadOverride(t *testing.T) {
	t.Parallel()

	// Tools already expects Renovate, so requiring it changes nothing.
	_, configPath := scratchProject(t,
		"[repos.alpha]\ntype = \"tools\"\n\n[repos.alpha.overrides]\nrenovate = \"required\"\n")

	stdout, _, err := exec(t, "validate", "--config", configPath)
	if err == nil {
		t.Fatal("run(validate) error = nil, want a dead-override error")
	}
	if errors.Is(err, errUsage) {
		t.Errorf("run(validate) error = %v, want a runtime error rather than a usage one", err)
	}
	if !strings.Contains(err.Error(), configPath) {
		t.Errorf("run(validate) error = %q, want it to name %s", err, configPath)
	}
	if !strings.Contains(err.Error(), "is dead") {
		t.Errorf("run(validate) error = %q, want it to explain the override is dead", err)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want nothing on a rejected config", stdout)
	}
}

// TestRunRender_reportsAnUnreadableSnapshot covers the one read whose absence
// is not a failure. A missing audit.json is the first run; an audit.json that
// cannot be read is a real error and must not be mistaken for one.
func TestRunRender_reportsAnUnreadableSnapshot(t *testing.T) {
	t.Parallel()

	dir, configPath := scratchProject(t, scratchConfig)
	// A directory where the snapshot belongs: it exists, so this is not the
	// first run, and reading it fails with something other than not-exist.
	if err := os.Mkdir(filepath.Join(dir, "audit.json"), 0o750); err != nil {
		t.Fatalf("create the obstruction: %v", err)
	}

	var stdout, stderr bytes.Buffer
	err := runRender(t.Context(), []string{"--config", configPath, "--dir", dir},
		noEnv, &stdout, &stderr, fakeFactory(healthyFake()), fixedClock)
	if err == nil {
		t.Fatal("runRender() error = nil, want the unreadable snapshot reported")
	}
	if !strings.Contains(err.Error(), "audit.json") {
		t.Errorf("runRender() error = %q, want it to name audit.json", err)
	}
}

// TestRunRender_reportsAMalformedSnapshot pins what happens when the previous
// audit.json is not JSON: the change detector cannot compare against it, and
// guessing "changed" would hide a corrupted file behind a green run.
func TestRunRender_reportsAMalformedSnapshot(t *testing.T) {
	t.Parallel()

	dir, configPath := scratchProject(t, scratchConfig)
	if err := os.WriteFile(filepath.Join(dir, "audit.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write the fixture: %v", err)
	}

	var stdout, stderr bytes.Buffer
	err := runRender(t.Context(), []string{"--config", configPath, "--dir", dir},
		noEnv, &stdout, &stderr, fakeFactory(healthyFake()), fixedClock)
	if err == nil {
		t.Fatal("runRender() error = nil, want the malformed snapshot reported")
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want no material-change line when the comparison failed", stdout.String())
	}
}

// TestRunRender_reportsAnUnwritableDirectory is the last failure before the
// rename: both artefacts are staged beside their destinations, so a directory
// that cannot be written to fails before either file is replaced.
func TestRunRender_reportsAnUnwritableDirectory(t *testing.T) {
	t.Parallel()

	if os.Geteuid() == 0 {
		t.Skip("root ignores the directory mode this test depends on")
	}

	dir, configPath := scratchProject(t, scratchConfig)
	readmePath := filepath.Join(dir, "README.md")
	before, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatalf("read the README: %v", err)
	}

	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("make the directory read-only: %v", err)
	}
	// TempDir's cleanup has to be able to remove it again.
	t.Cleanup(func() { _ = os.Chmod(dir, 0o750) }) //nolint:errcheck // best effort, so TempDir can clean up

	var stdout, stderr bytes.Buffer
	err = runRender(t.Context(), []string{"--config", configPath, "--dir", dir},
		noEnv, &stdout, &stderr, fakeFactory(healthyFake()), fixedClock)
	if err == nil {
		t.Fatal("runRender() error = nil, want the unwritable directory reported")
	}
	after, readErr := os.ReadFile(readmePath)
	if readErr != nil {
		t.Fatalf("read the README back: %v", readErr)
	}
	if string(after) != string(before) {
		t.Error("the README was modified even though the run failed")
	}
}

// TestLiveCollector_reportsAnUnresolvableToken covers the branch every real
// run starts with. No getenv means no GITHUB_TOKEN to read, which must come
// back as an error rather than as a client that will 401 on every call.
func TestLiveCollector_reportsAnUnresolvableToken(t *testing.T) {
	t.Parallel()

	_, err := liveCollector(t.Context(), nil, slog.New(slog.DiscardHandler))
	if err == nil {
		t.Fatal("liveCollector() error = nil, want the missing environment reported")
	}
}

// TestRunRender_reportsAMissingConfig is the first thing render does and the
// most likely thing to get wrong: --config pointing at nothing. It fails
// before a token is resolved or a request is made.
func TestRunRender_reportsAMissingConfig(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), "absent.toml")
	called := false
	factory := func(context.Context, func(string) string, *slog.Logger) (collector, error) {
		called = true
		return healthyFake(), nil
	}

	var stdout, stderr bytes.Buffer
	err := runRender(t.Context(), []string{"--config", missing}, noEnv, &stdout, &stderr, factory, fixedClock)
	if err == nil {
		t.Fatal("runRender() error = nil, want the missing config reported")
	}
	if !strings.Contains(err.Error(), missing) {
		t.Errorf("runRender() error = %q, want it to name %s", err, missing)
	}
	if called {
		t.Error("the client was built despite the configuration failing to load")
	}
}

// TestRunValidate_knowsNothingOfTheAccount: validate judges repos.toml alone.
// Whether its entries match the account is render's to report, so a config
// that names repositories no account has is still a valid config, and no
// collector is ever built to find out.
func TestRunValidate_knowsNothingOfTheAccount(t *testing.T) {
	t.Parallel()

	_, configPath := scratchProject(t, scratchConfig+"\n[repos.retired]\ntype = \"tools\"\n")

	var stdout, stderr bytes.Buffer
	if err := runValidate([]string{"--config", configPath}, &stdout, &stderr); err != nil {
		t.Fatalf("runValidate() error = %v, want nil", err)
	}
	if want := "3 repositories, no problems found"; !strings.Contains(stdout.String(), want) {
		t.Errorf("stdout = %q, want it to contain %q", stdout.String(), want)
	}
}
