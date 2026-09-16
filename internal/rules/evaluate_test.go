package rules_test

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
	"github.com/rvenutolo/github-repos-audit/internal/rules"
)

// TestEvaluate_standardTypesByVisibility walks the cells whose expectation
// varies, one case per meaningful (check, type, visibility, published)
// combination. Each case starts from a fixture that passes everything and
// asserts only what the type says about that cell.
func TestEvaluate_standardTypesByVisibility(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		repo  audit.Repo
		check rules.Check
		want  rules.Verdict
	}{
		// The unconditional rows apply to every type.
		{"readme is expected of content", fixture("a", "content"), rules.CheckREADME, rules.Pass},
		{"gitignore is expected of content", fixture("a", "content"), rules.CheckGitignore, rules.Pass},
		{"editorconfig is expected of tools", fixture("a", "tools"), rules.CheckEditorconfig, rules.Pass},
		{"tag ruleset is expected of content", fixture("a", "content"), rules.CheckTagRuleset, rules.Pass},

		// pub rows: n/a while private, judged once public.
		{"license is n/a on a private repo", fixture("a", "software"), rules.CheckLicense, rules.NA},
		{"license is judged on a public repo", public(fixture("a", "software")), rules.CheckLicense, rules.Pass},
		{"secret scanning is n/a on a private repo", fixture("a", "tools"), rules.CheckSecretScanning, rules.NA},
		{"secret scanning is judged on a public repo", public(fixture("a", "tools")), rules.CheckSecretScanning, rules.Pass},
		{"vuln reporting is n/a for config even when public", public(fixture("a", "config")), rules.CheckVulnReporting, rules.NA},
		{"vuln reporting is judged for public software", public(fixture("a", "software")), rules.CheckVulnReporting, rules.Pass},

		// pub+pub'd rows: public alone is not enough.
		{"homepage is n/a on a public but unpublished repo", public(fixture("a", "content")), rules.CheckHomepage, rules.NA},
		{"homepage is judged once published", published(public(fixture("a", "content"))), rules.CheckHomepage, rules.Pass},
		{"topics is n/a on a public but unpublished repo", public(fixture("a", "infra")), rules.CheckTopics, rules.NA},
		{"topics is judged once published", published(public(fixture("a", "infra"))), rules.CheckTopics, rules.Pass},
		{"community files are n/a for published content", published(public(fixture("a", "content"))), rules.CheckCommunityFiles, rules.NA},
		{"community files are judged for published software", published(public(fixture("a", "software"))), rules.CheckCommunityFiles, rules.Pass},

		// The tooling rows exempt content and nothing else.
		{"renovate is n/a for content", fixture("a", "content"), rules.CheckRenovate, rules.NA},
		{"renovate is expected of config", fixture("a", "config"), rules.CheckRenovate, rules.Pass},
		{"flake.nix is n/a for content", fixture("a", "content"), rules.CheckFlakeNix, rules.NA},
		{"flake.nix is expected of infra", fixture("a", "infra"), rules.CheckFlakeNix, rules.Pass},
		{"justfile is expected of environment", fixture("a", "environment"), rules.CheckJustfile, rules.Pass},
		{"ci green is n/a for content", fixture("a", "content"), rules.CheckCIGreen, rules.NA},

		// Releases and CHANGELOG belong to software alone.
		{"releases are n/a for tools", fixture("a", "tools"), rules.CheckReleases, rules.NA},
		{"releases are expected of software", fixture("a", "software"), rules.CheckReleases, rules.Pass},
		{"changelog is n/a for infra", fixture("a", "infra"), rules.CheckChangelog, rules.NA},
		{"changelog is expected of software", fixture("a", "software"), rules.CheckChangelog, rules.Pass},

		// Informational rows are shown and never judged.
		{"last push is informational", fixture("a", "content"), rules.CheckLastPush, rules.Info},
		{"open PRs are informational", fixture("a", "tools"), rules.CheckOpenPRs, rules.Info},
		{"last release age is informational for software", fixture("a", "software"), rules.CheckLastReleaseAge, rules.Info},
		{"last release age is n/a for everything else", fixture("a", "config"), rules.CheckLastReleaseAge, rules.NA},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cells, err := evaluateOne(tc.repo)
			if err != nil {
				t.Fatalf("Evaluate() error = %v, want nil", err)
			}
			if got := cells[tc.check].Verdict; got != tc.want {
				t.Errorf("%s verdict = %v, want %v", tc.check, got, tc.want)
			}
		})
	}
}

// TestEvaluate_naCellsStillCarryTheirValue is the "an n/a cell still shows a
// value it has" decision: the report withholds the judgement, not the fact.
func TestEvaluate_naCellsStillCarryTheirValue(t *testing.T) {
	t.Parallel()

	// Public but unpublished, so the topics row is n/a — and the repository
	// nonetheless has six topics, which the reader should see.
	r := public(fixture("a", "content"))
	r.Topics = 6

	cells, err := evaluateOne(r)
	if err != nil {
		t.Fatalf("Evaluate() error = %v, want nil", err)
	}
	cell := cells[rules.CheckTopics]
	if cell.Verdict != rules.NA {
		t.Fatalf("topics verdict = %v, want NA", cell.Verdict)
	}
	if cell.Value != "6" {
		t.Errorf("topics value = %q, want %q", cell.Value, "6")
	}

	// flake.nix, by contrast, is present-or-absent. An n/a cell there renders
	// n/a, because a bare cross on a non-gap cell is indistinguishable from a
	// gap in the same column.
	if got := cells[rules.CheckFlakeNix].Value; got != "" {
		t.Errorf("flake.nix value = %q, want empty so the cell renders n/a", got)
	}
}

func TestEvaluate_scalarBearingCells(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func(audit.Repo) audit.Repo
		check   rules.Check
		want    string
		verdict rules.Verdict
		code    bool
	}{
		{
			name:   "license carries its SPDX id",
			mutate: func(r audit.Repo) audit.Repo { r.License = "Apache-2.0"; return r },
			check:  rules.CheckLicense, want: "Apache-2.0", verdict: rules.Pass,
		},
		{
			name:   "noassertion is not a pass",
			mutate: func(r audit.Repo) audit.Repo { r.License = "NOASSERTION"; return r },
			check:  rules.CheckLicense, want: "NOASSERTION", verdict: rules.Fail,
		},
		{
			name:   "renovate carries its path as code",
			mutate: func(r audit.Repo) audit.Repo { r.Files.RenovateConfig = ".github/renovate.json5"; return r },
			check:  rules.CheckRenovate, want: ".github/renovate.json5", verdict: rules.Pass, code: true,
		},
		{
			name:   "community files carry a fraction",
			mutate: func(r audit.Repo) audit.Repo { r.Files.CodeOfConduct = false; return r },
			check:  rules.CheckCommunityFiles, want: "2/3", verdict: rules.Fail,
		},
		{
			name: "releases exclude drafts",
			mutate: func(r audit.Repo) audit.Repo {
				r.Releases = audit.Releases{Total: 5, Drafts: 2}
				return r
			},
			check: rules.CheckReleases, want: "3", verdict: rules.Pass,
		},
		{
			name: "a repo with only draft releases has none",
			mutate: func(r audit.Repo) audit.Repo {
				r.Releases = audit.Releases{Total: 1, Drafts: 1}
				return r
			},
			check: rules.CheckReleases, want: "0", verdict: rules.Fail,
		},
		{
			name:   "open PRs carry the count",
			mutate: func(r audit.Repo) audit.Repo { r.OpenPullRequests = 4; return r },
			check:  rules.CheckOpenPRs, want: "4", verdict: rules.Info,
		},
		{
			name:   "branches count everything but the default branch",
			mutate: func(r audit.Repo) audit.Repo { r.Branches = 4; return r },
			check:  rules.CheckBranches, want: "3", verdict: rules.Info,
		},
		{
			name:   "a repo with only its default branch has no others",
			mutate: func(r audit.Repo) audit.Repo { r.Branches = 1; return r },
			check:  rules.CheckBranches, want: "0", verdict: rules.Info,
		},
		{
			name:   "an empty repo has no branches at all",
			mutate: func(r audit.Repo) audit.Repo { r.Branches = 0; return r },
			check:  rules.CheckBranches, want: "0", verdict: rules.Info,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cells, err := evaluateOne(tc.mutate(published(public(fixture("a", "software")))))
			if err != nil {
				t.Fatalf("Evaluate() error = %v, want nil", err)
			}
			cell := cells[tc.check]
			if cell.Value != tc.want {
				t.Errorf("%s value = %q, want %q", tc.check, cell.Value, tc.want)
			}
			if cell.Verdict != tc.verdict {
				t.Errorf("%s verdict = %v, want %v", tc.check, cell.Verdict, tc.verdict)
			}
			if cell.Code != tc.code {
				t.Errorf("%s code = %v, want %v", tc.check, cell.Code, tc.code)
			}
		})
	}
}

func TestEvaluate_tagRuleset(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		rulesets []audit.Ruleset
		want     string
		verdict  rules.Verdict
		code     bool
	}{
		{
			name: "the baseline ruleset renders as default",
			rulesets: []audit.Ruleset{
				{Name: "protect-tags", Target: "TAG", Enforcement: "ACTIVE", Include: []string{"refs/tags/**"}},
			},
			want: "default", verdict: rules.Pass,
		},
		{
			name: "a differently named ruleset passes and stays visible",
			rulesets: []audit.Ruleset{
				{Name: "release-tag-protection", Target: "TAG", Enforcement: "ACTIVE", Include: []string{"refs/tags/v*"}},
			},
			want: "release-tag-protection", verdict: rules.Pass, code: true,
		},
		{
			name: "the baseline name over a different scope is not default",
			rulesets: []audit.Ruleset{
				{Name: "protect-tags", Target: "TAG", Enforcement: "ACTIVE", Include: []string{"refs/tags/v*"}},
			},
			want: "protect-tags", verdict: rules.Pass, code: true,
		},
		{
			name: "an evaluate-only ruleset does not pass",
			rulesets: []audit.Ruleset{
				{Name: "protect-tags", Target: "TAG", Enforcement: "EVALUATE", Include: []string{"refs/tags/**"}},
			},
			want: "", verdict: rules.Fail,
		},
		{
			name: "a branch ruleset is not a tag ruleset",
			rulesets: []audit.Ruleset{
				{Name: "protect-main", Target: "BRANCH", Enforcement: "ACTIVE"},
			},
			want: "", verdict: rules.Fail,
		},
		{name: "no rulesets at all", rulesets: nil, want: "", verdict: rules.Fail},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := fixture("a", "tools")
			r.Rulesets = tc.rulesets
			cells, err := evaluateOne(r)
			if err != nil {
				t.Fatalf("Evaluate() error = %v, want nil", err)
			}
			cell := cells[rules.CheckTagRuleset]
			if cell.Value != tc.want || cell.Verdict != tc.verdict || cell.Code != tc.code {
				t.Errorf("tag ruleset = {%q %v code=%v}, want {%q %v code=%v}",
					cell.Value, cell.Verdict, cell.Code, tc.want, tc.verdict, tc.code)
			}
		})
	}
}

func TestEvaluate_ciRollupStates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		state string
		want  rules.Verdict
	}{
		{"SUCCESS", rules.Pass},
		{"EXPECTED", rules.Pass},
		{"FAILURE", rules.Fail},
		{"ERROR", rules.Fail},
		// A run in flight is not a failure, and the daily cadence means it will
		// have settled by the next render.
		{"PENDING", rules.Info},
		// A null rollup on a repository expected to have CI is a cross.
		{"", rules.Fail},
	}
	for _, tc := range tests {
		t.Run("rollup "+strings.ToLower(tc.state), func(t *testing.T) {
			t.Parallel()

			r := fixture("a", "tools")
			r.CIState = tc.state
			cells, err := evaluateOne(r)
			if err != nil {
				t.Fatalf("Evaluate() error = %v, want nil", err)
			}
			cell := cells[rules.CheckCIGreen]
			if cell.Verdict != tc.want {
				t.Errorf("rollup %q verdict = %v, want %v", tc.state, cell.Verdict, tc.want)
			}
			if cell.Value != tc.state {
				t.Errorf("rollup %q value = %q, want %q", tc.state, cell.Value, tc.state)
			}
		})
	}
}

func TestEvaluate_requiredChecksAndCIWorkflows(t *testing.T) {
	t.Parallel()

	t.Run("merge-gate alone is not a gate", func(t *testing.T) {
		t.Parallel()

		r := fixture("a", "tools")
		r.Branch.RequiredChecks = []string{"merge-gate"}
		cells, err := evaluateOne(r)
		if err != nil {
			t.Fatalf("Evaluate() error = %v, want nil", err)
		}
		cell := cells[rules.CheckRequiredChecks]
		if cell.Verdict != rules.Fail {
			t.Errorf("required checks verdict = %v, want Fail", cell.Verdict)
		}
		// Renders ✗ rather than a count: a zero would read like a number
		// somebody chose.
		if cell.Value != "" {
			t.Errorf("required checks value = %q, want empty so the cell renders a cross", cell.Value)
		}
	})

	t.Run("only merge-gate.yml is not CI", func(t *testing.T) {
		t.Parallel()

		r := fixture("a", "tools")
		r.Files.Workflows = []string{"merge-gate.yml"}
		cells, err := evaluateOne(r)
		if err != nil {
			t.Fatalf("Evaluate() error = %v, want nil", err)
		}
		if got := cells[rules.CheckCIWorkflows].Verdict; got != rules.Fail {
			t.Errorf("CI workflows verdict = %v, want Fail", got)
		}
	})

	// The case the two-row split exists for: a repository that allows direct
	// push can still have real CI on its pull requests while having no
	// required checks at all.
	t.Run("required checks is n/a under direct push, CI workflows still reads pass", func(t *testing.T) {
		t.Parallel()

		r := fixture("a", "content")
		r.Branch.Types = []string{"required_signatures"} // no pull_request rule
		r.Branch.RequiredChecks = nil
		r.Files.Workflows = []string{"ci.yml"}
		cells, err := evaluateOne(r)
		if err != nil {
			t.Fatalf("Evaluate() error = %v, want nil", err)
		}
		if got := cells[rules.CheckRequiredChecks].Verdict; got != rules.NA {
			t.Errorf("required checks verdict = %v, want NA", got)
		}
		if got := cells[rules.CheckDirectPush].Verdict; got != rules.Pass {
			t.Errorf("direct push verdict = %v, want Pass for a content repo", got)
		}
	})
}

func TestEvaluate_directPush(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		typ     rules.Type
		blocked bool
		want    rules.Verdict
		value   string
	}{
		{"content should allow", "content", false, rules.Pass, "allowed"},
		{"content that blocks is a gap", "content", true, rules.Fail, "blocked"},
		{"tools should block", "tools", true, rules.Pass, "blocked"},
		{"tools that allows is a gap", "tools", false, rules.Fail, "allowed"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := fixture("a", tc.typ)
			if tc.blocked {
				r.Branch.Types = []string{"pull_request", "required_signatures"}
			} else {
				r.Branch.Types = []string{"required_signatures"}
			}
			cells, err := evaluateOne(r)
			if err != nil {
				t.Fatalf("Evaluate() error = %v, want nil", err)
			}
			cell := cells[rules.CheckDirectPush]
			if cell.Verdict != tc.want || cell.Value != tc.value {
				t.Errorf("direct push = {%v %q}, want {%v %q}", cell.Verdict, cell.Value, tc.want, tc.value)
			}
			if !cell.Mark {
				t.Error("direct push cell should ask for the tick alongside the value")
			}
		})
	}
}

// TestEvaluate_emptyRepository covers the whole row for a repository with no
// commits at all. That is an ordinary answer, not a failure: it cannot be
// judged on what is in its commits, and it can and should still be judged on
// its description, topics and settings.
func TestEvaluate_emptyRepository(t *testing.T) {
	t.Parallel()

	r := audit.Repo{
		Name:        "go-linter",
		Type:        "config",
		Visibility:  "private",
		Description: "",
		Empty:       true,
		Branch:      audit.BranchRules{Known: false},
	}
	cells, err := evaluateOne(r)
	if err != nil {
		t.Fatalf("Evaluate() error = %v, want nil", err)
	}

	// Branch-derived cells are unknown.
	for _, c := range []rules.Check{rules.CheckSignedCommits, rules.CheckRequiredChecks} {
		if got := cells[c].Verdict; got != rules.NA {
			t.Errorf("%s verdict = %v, want NA on an empty repository", c, got)
		}
	}
	// File probes and CI are crosses where the type expects them.
	for _, c := range []rules.Check{
		rules.CheckREADME, rules.CheckGitignore, rules.CheckEditorconfig,
		rules.CheckFlakeNix, rules.CheckJustfile, rules.CheckRenovate,
		rules.CheckCIWorkflows, rules.CheckCIGreen, rules.CheckTagRuleset,
		rules.CheckDescription,
	} {
		if got := cells[c].Verdict; got != rules.Fail {
			t.Errorf("%s verdict = %v, want Fail on an empty repository", c, got)
		}
	}
	// With no rulesets at all, direct push reads allowed — a real gap for a
	// config repository, and the right answer rather than a blank.
	push := cells[rules.CheckDirectPush]
	if push.Value != "allowed" || push.Verdict != rules.Fail {
		t.Errorf("direct push = {%v %q}, want {Fail \"allowed\"}", push.Verdict, push.Value)
	}
}

func TestEvaluate_emptyRepositoryDirectPushFromRulesets(t *testing.T) {
	t.Parallel()

	r := audit.Repo{
		Name: "empty", Type: "tools", Visibility: "private", Empty: true,
		Branch: audit.BranchRules{Known: false},
		Rulesets: []audit.Ruleset{
			{
				Name: "protect-main", Target: "BRANCH", Enforcement: "ACTIVE",
				Include: []string{"~DEFAULT_BRANCH"}, Rules: []string{"PULL_REQUEST"},
			},
		},
	}
	cells, err := evaluateOne(r)
	if err != nil {
		t.Fatalf("Evaluate() error = %v, want nil", err)
	}
	push := cells[rules.CheckDirectPush]
	if push.Value != "blocked" || push.Verdict != rules.Pass {
		t.Errorf("direct push = {%v %q}, want {Pass \"blocked\"}", push.Verdict, push.Value)
	}
}

func TestEvaluate_overrides(t *testing.T) {
	t.Parallel()

	t.Run("required forces a check the type calls n/a", func(t *testing.T) {
		t.Parallel()

		r := fixture("a", "content") // renovate is n/a for content
		r.Files.RenovateConfig = ""
		r.Overrides = map[string]string{"renovate": rules.OverrideRequired}
		cells, err := evaluateOne(r)
		if err != nil {
			t.Fatalf("Evaluate() error = %v, want nil", err)
		}
		cell := cells[rules.CheckRenovate]
		if cell.Verdict != rules.Fail {
			t.Errorf("renovate verdict = %v, want Fail once required", cell.Verdict)
		}
		if !cell.Overridden {
			t.Error("cell should be marked as overridden")
		}
	})

	t.Run("not_required excuses a check the type expects", func(t *testing.T) {
		t.Parallel()

		r := public(fixture("a", "content"))
		r.Settings.SecretScanning = "disabled"
		r.Overrides = map[string]string{"secret_scanning": rules.OverrideNotRequired}
		cells, err := evaluateOne(r)
		if err != nil {
			t.Fatalf("Evaluate() error = %v, want nil", err)
		}
		cell := cells[rules.CheckSecretScanning]
		if cell.Verdict != rules.NA {
			t.Errorf("secret scanning verdict = %v, want NA once excused", cell.Verdict)
		}
		if !cell.Overridden {
			t.Error("cell should be marked as overridden")
		}
	})

	t.Run("a value override replaces the expected direct-push value", func(t *testing.T) {
		t.Parallel()

		r := fixture("a", "tools")
		r.Branch.Types = []string{"required_signatures"} // direct push allowed
		r.Overrides = map[string]string{"direct_push": "allowed"}
		cells, err := evaluateOne(r)
		if err != nil {
			t.Fatalf("Evaluate() error = %v, want nil", err)
		}
		if got := cells[rules.CheckDirectPush].Verdict; got != rules.Pass {
			t.Errorf("direct push verdict = %v, want Pass once overridden to allowed", got)
		}
	})

	t.Run("not_required turns the direct-push row off entirely", func(t *testing.T) {
		t.Parallel()

		r := fixture("a", "tools")
		r.Branch.Types = []string{"required_signatures"} // direct push allowed
		r.Overrides = map[string]string{"direct_push": rules.OverrideNotRequired}
		cells, err := evaluateOne(r)
		if err != nil {
			t.Fatalf("Evaluate() error = %v, want nil", err)
		}
		cell := cells[rules.CheckDirectPush]
		if cell.Verdict != rules.NA {
			t.Errorf("direct push verdict = %v, want NA once excused", cell.Verdict)
		}
		if cell.Value != "allowed" {
			t.Errorf("direct push value = %q, want the observation to survive the excuse", cell.Value)
		}
		if !cell.Overridden {
			t.Error("cell should be marked as overridden")
		}
	})

	t.Run("required leaves the always-judged direct-push row as it was", func(t *testing.T) {
		t.Parallel()

		// The row is unconditional, so "required" cannot change the verdict.
		// It still marks the cell, because the declaration is real and the
		// Overrides section lists it.
		r := fixture("a", "tools")
		r.Branch.Types = []string{"required_signatures"} // direct push allowed
		r.Overrides = map[string]string{"direct_push": rules.OverrideRequired}
		cells, err := evaluateOne(r)
		if err != nil {
			t.Fatalf("Evaluate() error = %v, want nil", err)
		}
		cell := cells[rules.CheckDirectPush]
		if cell.Verdict != rules.Fail {
			t.Errorf("direct push verdict = %v, want Fail, unchanged by \"required\"", cell.Verdict)
		}
		if !cell.Overridden {
			t.Error("cell should be marked as overridden")
		}
	})

	t.Run("a value override on a presence check only marks the cell", func(t *testing.T) {
		t.Parallel()

		// Only direct_push reads a value; on a present-or-absent row a value
		// is meaningless, so the verdict stands and the cell is merely marked
		// so the Overrides section can show what was declared.
		r := fixture("a", "tools")
		r.Files.RenovateConfig = ""
		r.Overrides = map[string]string{"renovate": "quarterly"}
		cells, err := evaluateOne(r)
		if err != nil {
			t.Fatalf("Evaluate() error = %v, want nil", err)
		}
		cell := cells[rules.CheckRenovate]
		if cell.Verdict != rules.Fail {
			t.Errorf("renovate verdict = %v, want Fail, unchanged by a value override", cell.Verdict)
		}
		if !cell.Overridden {
			t.Error("cell should be marked as overridden")
		}
	})
}

func TestEvaluate_reportsGapsInTheFixedOrder(t *testing.T) {
	t.Parallel()

	// One repository missing several things at once, so the ordering is
	// visible rather than incidental.
	r := fixture("bravo", "tools")
	r.Files.README = false
	r.Description = ""
	r.Files.Gitignore = false
	r.CIState = "FAILURE"

	other := fixture("alpha", "tools")
	other.Files.README = false

	rep, err := rules.Evaluate(&audit.Snapshot{Types: standardTypes(), Repos: []audit.Repo{r, other}})
	if err != nil {
		t.Fatalf("Evaluate() error = %v, want nil", err)
	}

	// A check with no failures, such as justfile, is omitted entirely.
	want := []rules.Check{
		rules.CheckREADME, rules.CheckDescription, rules.CheckGitignore, rules.CheckCIGreen,
	}
	got := make([]rules.Check, 0, len(rep.Gaps))
	for _, g := range rep.Gaps {
		got = append(got, g.Check)
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("gap order mismatch (-want +got):\n%s", diff)
	}
	// Repositories inside a gap sort case-insensitively.
	if diff := cmp.Diff([]string{"alpha", "bravo"}, rep.Gaps[0].Repos); diff != "" {
		t.Errorf("README gap repos mismatch (-want +got):\n%s", diff)
	}
}

func TestEvaluate_communityGapsNameTheMissingFiles(t *testing.T) {
	t.Parallel()

	r := published(public(fixture("alpha", "software")))
	r.Files.Security = false
	r.Files.CodeOfConduct = false

	rep, err := rules.Evaluate(&audit.Snapshot{Types: standardTypes(), Repos: []audit.Repo{r}})
	if err != nil {
		t.Fatalf("Evaluate() error = %v, want nil", err)
	}

	var labels []string
	for _, g := range rep.Gaps {
		if g.Check == rules.CheckCommunityFiles {
			labels = append(labels, g.Label)
		}
	}
	want := []string{"No SECURITY.md", "No CODE_OF_CONDUCT.md"}
	if diff := cmp.Diff(want, labels); diff != "" {
		t.Errorf("community gaps mismatch (-want +got):\n%s", diff)
	}
}

func TestEvaluate_sortsRepositoriesCaseInsensitively(t *testing.T) {
	t.Parallel()

	snap := &audit.Snapshot{Types: standardTypes(), Repos: []audit.Repo{
		fixture("media-server", "infra"),
		fixture("mixedCase-flake", "software"),
		fixture("github-repos-audit", "tools"),
	}}
	rep, err := rules.Evaluate(snap)
	if err != nil {
		t.Fatalf("Evaluate() error = %v, want nil", err)
	}
	got := make([]string, 0, len(rep.Repos))
	for _, r := range rep.Repos {
		got = append(got, r.Repo.Name)
	}
	want := []string{"github-repos-audit", "media-server", "mixedCase-flake"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("repository order mismatch (-want +got):\n%s", diff)
	}
}

func TestEvaluate_carriesInstantsRatherThanRelativeDates(t *testing.T) {
	t.Parallel()

	// The renderer turns an instant into "12d" against its injected clock.
	// Formatting it here would make the golden files rot daily.
	pushed := time.Date(2026, 8, 24, 9, 0, 0, 0, time.UTC)
	r := fixture("a", "software")
	r.PushedAt = pushed

	cells, err := evaluateOne(r)
	if err != nil {
		t.Fatalf("Evaluate() error = %v, want nil", err)
	}
	cell := cells[rules.CheckLastPush]
	if !cell.At.Equal(pushed) {
		t.Errorf("last push at = %v, want %v", cell.At, pushed)
	}
	if cell.Value != "" {
		t.Errorf("last push value = %q, want empty; the renderer formats the date", cell.Value)
	}
}

func TestEvaluate_listsEveryOverride(t *testing.T) {
	t.Parallel()

	r := public(fixture("recipe-site", "content"))
	r.Overrides = map[string]string{"secret_scanning": rules.OverrideNotRequired}

	rep, err := rules.Evaluate(&audit.Snapshot{Types: standardTypes(), Repos: []audit.Repo{r}})
	if err != nil {
		t.Fatalf("Evaluate() error = %v, want nil", err)
	}
	if len(rep.Overrides) != 1 {
		t.Fatalf("overrides = %+v, want exactly one", rep.Overrides)
	}
	got := rep.Overrides[0]
	if got.Repo != "recipe-site" || got.Check != rules.CheckSecretScanning || got.Value != rules.OverrideNotRequired {
		t.Errorf("override = %+v, want recipe-site/secret_scanning/not_required", got)
	}
}

func TestEvaluate_topicsFailWhenThereAreNone(t *testing.T) {
	t.Parallel()

	// Topics is a scalar row: it reports a count AND a verdict, and the verdict
	// turns on `r.Topics > 0`. Relaxing that to `>= 0` makes every repository
	// pass while still rendering an honest "0", which is the worst kind of
	// wrong — the number on the page contradicts the tick beside it. Surfaced
	// as a surviving CONDITIONALS_BOUNDARY mutant at evaluate.go:241.
	tests := []struct {
		name   string
		topics int
		want   rules.Verdict
	}{
		{name: "none", topics: 0, want: rules.Fail},
		{name: "exactly one", topics: 1, want: rules.Pass},
		{name: "several", topics: 3, want: rules.Pass},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// pub+pub'd: topics are expected only of a published public repo.
			r := published(public(fixture("alpha", "software")))
			r.Topics = tc.topics

			cells, err := evaluateOne(r)
			if err != nil {
				t.Fatalf("Evaluate() error = %v, want nil", err)
			}
			cell := cells[rules.CheckTopics]
			if cell.Verdict != tc.want {
				t.Errorf("topics verdict for %d topic(s) = %v, want %v", tc.topics, cell.Verdict, tc.want)
			}
			if got, want := cell.Value, strconv.Itoa(tc.topics); got != want {
				t.Errorf("topics value = %q, want %q", got, want)
			}
		})
	}
}

func TestEvaluate_requiredChecksCountsOnlyItsOwn(t *testing.T) {
	t.Parallel()

	// The row reports HOW MANY required checks a branch has, excluding the
	// always-green merge-gate placeholder. Nothing asserted the number itself,
	// only that the row passed, so turning the `own++` that produces it into
	// `own--` went unnoticed: the row still passed and rendered "-2". Surfaced
	// as a surviving INCREMENT_DECREMENT mutant at evaluate.go:300.
	tests := []struct {
		name     string
		required []string
		want     string
		wantPass bool
	}{
		{
			name:     "two of its own",
			required: []string{"gate (ubuntu-latest)", "commitlint"},
			want:     "2",
			wantPass: true,
		},
		{
			name:     "the placeholder does not count",
			required: []string{"gate (ubuntu-latest)", "merge-gate"},
			want:     "1",
			wantPass: true,
		},
		{
			name:     "only the placeholder is no gate at all",
			required: []string{"merge-gate"},
			want:     "",
			wantPass: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := fixture("alpha", "tools")
			r.Branch.RequiredChecks = tc.required

			cells, err := evaluateOne(r)
			if err != nil {
				t.Fatalf("Evaluate() error = %v, want nil", err)
			}
			cell := cells[rules.CheckRequiredChecks]
			if got, want := cell.Value, tc.want; got != want {
				t.Errorf("required checks value = %q, want %q", got, want)
			}
			if got := cell.Verdict == rules.Pass; got != tc.wantPass {
				t.Errorf("required checks passed = %t, want %t (verdict %v)", got, tc.wantPass, cell.Verdict)
			}
		})
	}
}

func TestEvaluate_directPushGapsNameTheDirectionThatFailed(t *testing.T) {
	t.Parallel()

	// The direct-push worklist selects on TWO things: the cell failed, and it
	// failed in THIS direction. The direction half is what pairs a repository
	// with a label, and negating it — `cell.Value != observed` — swaps the two
	// labels wholesale: every repository that wrongly allows direct push gets
	// filed under "blocked", and vice versa. Both gaps still appear, with the
	// right repository counts, which is why nothing noticed. Surfaced as a
	// surviving CONDITIONALS_NEGATION mutant at gaps.go:124.
	//
	// Both directions have to fail at once for the pairing to be observable,
	// and the two types expect opposite things: content wants direct push
	// allowed, every other type wants it blocked.
	allows := fixture("alpha", "tools")
	allows.Branch.Types = []string{"required_signatures"} // no pull_request rule

	blocks := fixture("bravo", "content") // fixture already blocks

	rep, err := rules.Evaluate(&audit.Snapshot{Types: standardTypes(), Repos: []audit.Repo{allows, blocks}})
	if err != nil {
		t.Fatalf("Evaluate() error = %v, want nil", err)
	}

	got := make(map[string][]string)
	for _, g := range rep.Gaps {
		if g.Check == rules.CheckDirectPush {
			got[g.Label] = g.Repos
		}
	}
	want := map[string][]string{
		"Direct push to the default branch allowed": {"alpha"},
		"Direct push to the default branch blocked": {"bravo"},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("direct push gaps mismatch (-want +got):\n%s", diff)
	}
}

func TestEvaluate_directPushGapsSkipRepositoriesThatPass(t *testing.T) {
	t.Parallel()

	// The other half of the same predicate: a repository whose direct-push row
	// PASSES must not reach the worklist at all. Without this, dropping the
	// verdict test still produces plausible-looking gaps, because every
	// repository has a direct-push value whether or not it is the wrong one.
	rep, err := rules.Evaluate(&audit.Snapshot{Types: standardTypes(), Repos: []audit.Repo{
		fixture("alpha", "tools"),
		fixture("bravo", "tools"),
	}})
	if err != nil {
		t.Fatalf("Evaluate() error = %v, want nil", err)
	}

	for _, r := range rep.Repos {
		if got := r.Cells[rules.CheckDirectPush].Verdict; got != rules.Pass {
			t.Fatalf("%s direct push verdict = %v, want Pass (the fixture must pass this row)", r.Repo.Name, got)
		}
	}
	for _, g := range rep.Gaps {
		if g.Check == rules.CheckDirectPush {
			t.Errorf("direct push gap %q lists %v, but every repository passes the row", g.Label, g.Repos)
		}
	}
}

// TestEvaluate_emptyRepositoryIgnoresIrrelevantRulesets pins the two rulesets
// the fallback must step over. A repository with no default branch answers the
// direct-push question from its rulesets, and only an ACTIVE branch ruleset
// can block a push: a tag ruleset governs other refs, and an evaluate-mode
// ruleset reports without enforcing.
func TestEvaluate_emptyRepositoryIgnoresIrrelevantRulesets(t *testing.T) {
	t.Parallel()

	r := audit.Repo{
		Name: "empty", Type: "tools", Visibility: "private", Empty: true,
		Branch: audit.BranchRules{Known: false},
		Rulesets: []audit.Ruleset{
			{
				Name: "protect-tags", Target: "TAG", Enforcement: "ACTIVE",
				Include: []string{"refs/tags/**"}, Rules: []string{"PULL_REQUEST"},
			},
			{
				Name: "dry-run-main", Target: "BRANCH", Enforcement: "EVALUATE",
				Include: []string{"~DEFAULT_BRANCH"}, Rules: []string{"PULL_REQUEST"},
			},
		},
	}
	cells, err := evaluateOne(r)
	if err != nil {
		t.Fatalf("Evaluate() error = %v, want nil", err)
	}
	push := cells[rules.CheckDirectPush]
	if push.Value != "allowed" || push.Verdict != rules.Fail {
		t.Errorf("direct push = {%v %q}, want {Fail \"allowed\"}", push.Verdict, push.Value)
	}
}

// TestEvaluate_overridesSectionSkipsAnUnknownKey keeps the report honest about
// an override it cannot attribute. An unknown key is repos.toml's error to
// report; listing it here under some arbitrary check would be worse than
// leaving it out.
func TestEvaluate_overridesSectionSkipsAnUnknownKey(t *testing.T) {
	t.Parallel()

	r := fixture("alpha", "tools")
	r.Overrides = map[string]string{
		"not_a_check": rules.OverrideRequired,
		"renovate":    rules.OverrideNotRequired,
	}
	rep, err := rules.Evaluate(&audit.Snapshot{Types: standardTypes(), Repos: []audit.Repo{r}})
	if err != nil {
		t.Fatalf("Evaluate() error = %v, want nil", err)
	}
	want := []rules.Override{
		{Repo: "alpha", Check: rules.CheckRenovate, Value: rules.OverrideNotRequired},
	}
	if diff := cmp.Diff(want, rep.Overrides); diff != "" {
		t.Errorf("overrides mismatch (-want +got):\n%s", diff)
	}
}

func TestEvaluate_carriesTheOwner(t *testing.T) {
	t.Parallel()

	snap := &audit.Snapshot{Owner: "gh-owner", Types: standardTypes(), Repos: []audit.Repo{fixture("alpha", "tools")}}
	rep, err := rules.Evaluate(snap)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if rep.Owner != "gh-owner" {
		t.Errorf("Report.Owner = %q, want %q", rep.Owner, "gh-owner")
	}
}

func TestEvaluate_rejectsANilSnapshot(t *testing.T) {
	t.Parallel()

	if _, err := rules.Evaluate(nil); err == nil {
		t.Fatal("Evaluate(nil) error = nil, want an error")
	}
}

// TestEvaluate_rejectsASnapshotWithoutATypesTable: a snapshot that carries no
// types table defines no types, so every repository's type is unknown.
func TestEvaluate_rejectsASnapshotWithoutATypesTable(t *testing.T) {
	t.Parallel()

	_, err := rules.Evaluate(&audit.Snapshot{Repos: []audit.Repo{fixture("alpha", "tools")}})
	if err == nil || !strings.Contains(err.Error(), `unknown type "tools"`) {
		t.Fatalf("Evaluate() error = %v, want tools reported as an unknown type", err)
	}
}
