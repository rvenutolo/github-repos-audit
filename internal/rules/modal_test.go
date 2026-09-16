package rules_test

import (
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
	"github.com/rvenutolo/github-repos-audit/internal/rules"
)

// exceptionsFor runs a set of repositories through the rules and returns the
// settings exceptions and the settings that reached no consensus.
func exceptionsFor(t *testing.T, repos ...audit.Repo) ([]rules.Exception, []string) {
	t.Helper()
	rep, err := rules.Evaluate(&audit.Snapshot{Types: standardTypes(), Repos: repos})
	if err != nil {
		t.Fatalf("Evaluate() error = %v, want nil", err)
	}
	return rep.Exceptions, rep.NoConsensus
}

// pick returns the exceptions for one setting, so a test asserts on the
// setting it is about rather than on every setting at once.
func pick(exceptions []rules.Exception, setting string) []rules.Exception {
	var out []rules.Exception
	for _, e := range exceptions {
		if e.Setting == setting {
			out = append(out, e)
		}
	}
	return out
}

func TestModal_reportsTheMinorityAgainstAClearMajority(t *testing.T) {
	t.Parallel()

	repos := toolsRepos("alpha", "bravo", "charlie", "delta")
	// One repository runs the wiki. It is a deliberate deviation, and it stays
	// listed: there is no accepted-exceptions list, because a deviation that
	// had been forgotten could hide inside one.
	repos[2].Settings.HasWiki = true

	exceptions, noConsensus := exceptionsFor(t, repos...)
	if len(noConsensus) != 0 {
		t.Errorf("noConsensus = %v, want none", noConsensus)
	}
	want := []rules.Exception{
		{Repo: "charlie", Setting: "wiki", Value: "enabled", Modal: "disabled"},
	}
	if diff := cmp.Diff(want, pick(exceptions, "wiki")); diff != "" {
		t.Errorf("wiki exceptions mismatch (-want +got):\n%s", diff)
	}
}

func TestModal_reportsNoConsensusAtExactlyHalf(t *testing.T) {
	t.Parallel()

	repos := toolsRepos("alpha", "bravo", "charlie", "delta")
	// Two on, two off. Listing two exceptions out of four would say nothing
	// about which side is the norm, so the setting has no consensus instead.
	repos[0].Settings.HasWiki = true
	repos[1].Settings.HasWiki = true

	exceptions, noConsensus := exceptionsFor(t, repos...)
	if got := pick(exceptions, "wiki"); len(got) != 0 {
		t.Errorf("wiki exceptions = %+v, want none when there is no consensus", got)
	}
	if !slices.Contains(noConsensus, "wiki") {
		t.Errorf("noConsensus = %v, want it to contain \"wiki\"", noConsensus)
	}
}

func TestModal_aBareMajorityIsAConsensus(t *testing.T) {
	t.Parallel()

	repos := toolsRepos("alpha", "bravo", "charlie")
	repos[0].Settings.HasWiki = true // two of three disabled: a majority

	exceptions, noConsensus := exceptionsFor(t, repos...)
	if slices.Contains(noConsensus, "wiki") {
		t.Fatalf("noConsensus = %v, want wiki to have a consensus at two of three", noConsensus)
	}
	if got := pick(exceptions, "wiki"); len(got) != 1 || got[0].Repo != "alpha" {
		t.Errorf("wiki exceptions = %+v, want alpha alone", got)
	}
}

// TestModal_denominatorIsTheRepositoriesTheSettingAppliesTo is the reason the
// tie rule counts applicable repositories rather than all of them:
// actions-reuse exists only on private repositories, and measuring it against
// every repository would leave it permanently without consensus.
func TestModal_denominatorIsTheRepositoriesTheSettingAppliesTo(t *testing.T) {
	t.Parallel()

	privateA := fixture("alpha", "tools")
	privateB := fixture("bravo", "tools")
	privateC := fixture("charlie", "tools")
	privateC.Settings.ActionsAccessLevel = "organization"
	// Four public repositories, where /access answers 422 and the setting
	// simply does not exist.
	repos := publicToolsRepos("delta", "echo", "foxtrot", "golf")
	repos = append(repos, privateA, privateB, privateC)

	exceptions, noConsensus := exceptionsFor(t, repos...)
	if slices.Contains(noConsensus, "Actions reusable from other repositories") {
		t.Fatal("actions-reuse should have a consensus among the private repositories alone")
	}
	got := pick(exceptions, "Actions reusable from other repositories")
	if len(got) != 1 || got[0].Repo != "charlie" {
		t.Errorf("actions-reuse exceptions = %+v, want charlie alone", got)
	}
}

func TestModal_aSettingNoRepositoryCarriesIsSilent(t *testing.T) {
	t.Parallel()

	// Every repository public, so no repository has an actions access level.
	repos := publicToolsRepos("alpha", "bravo")
	exceptions, noConsensus := exceptionsFor(t, repos...)
	if got := pick(exceptions, "Actions reusable from other repositories"); len(got) != 0 {
		t.Errorf("exceptions = %+v, want none", got)
	}
	if slices.Contains(noConsensus, "Actions reusable from other repositories") {
		t.Error("a setting nothing carries is neither an exception nor a lack of consensus")
	}
}

func TestModal_reportsTheAllowlistItself(t *testing.T) {
	t.Parallel()

	repos := toolsRepos("alpha", "bravo", "charlie")
	repos[1].Settings.AllowedActions = &audit.AllowedActions{
		GitHubOwnedAllowed: true,
		Patterns:           []string{"nix-community/*", "step-security/*"},
	}
	exceptions, _ := exceptionsFor(t, repos...)
	got := pick(exceptions, "Actions allowlist")
	if len(got) != 1 || got[0].Repo != "bravo" {
		t.Fatalf("allowlist exceptions = %+v, want bravo alone", got)
	}
	if got[0].Value != "nix-community/*, step-security/*" || got[0].Modal != "step-security/*" {
		t.Errorf("allowlist exception = %+v, want the two patterns against the one", got[0])
	}
}

func TestModal_exceptionsSortByRepositoryThenSetting(t *testing.T) {
	t.Parallel()

	repos := toolsRepos("alpha", "Bravo", "charlie", "delta", "echo")
	repos[1].Settings.HasWiki = true
	repos[1].Settings.HasProjects = true
	repos[0].Settings.HasWiki = true

	exceptions, _ := exceptionsFor(t, repos...)
	order := make([]string, 0, len(exceptions))
	for _, e := range exceptions {
		order = append(order, e.Repo+"/"+e.Setting)
	}
	want := []string{"alpha/wiki", "Bravo/projects", "Bravo/wiki"}
	if diff := cmp.Diff(want, order); diff != "" {
		t.Errorf("exception order mismatch (-want +got):\n%s", diff)
	}
}

// TestSettingLabels_areUniqueAndNonEmpty guards the one defect the settings
// table can hide: two rows sharing a label, which would make an exception
// unattributable.
func TestSettingLabels_areUniqueAndNonEmpty(t *testing.T) {
	t.Parallel()

	seen := map[string]bool{}
	labels := rules.SettingLabels()
	if len(labels) == 0 {
		t.Fatal("SettingLabels() is empty")
	}
	for _, l := range labels {
		if l == "" {
			t.Error("a setting has an empty label")
		}
		if seen[l] {
			t.Errorf("duplicate setting label %q", l)
		}
		seen[l] = true
	}
}

// TestModal_reportsAnEmptyAllowlistAsNone separates "the allowlist is empty"
// from "there is no allowlist". A selected-actions policy that permits no
// pattern at all is a real, reportable setting; a repository whose policy is
// not "selected" carries no allowlist and must stay out of the denominator.
func TestModal_reportsAnEmptyAllowlistAsNone(t *testing.T) {
	t.Parallel()

	repos := toolsRepos("alpha", "bravo", "charlie")
	repos[1].Settings.AllowedActions = &audit.AllowedActions{GitHubOwnedAllowed: true}

	exceptions, _ := exceptionsFor(t, repos...)
	want := []rules.Exception{
		{Repo: "bravo", Setting: "Actions allowlist", Value: "none", Modal: "step-security/*"},
	}
	if diff := cmp.Diff(want, pick(exceptions, "Actions allowlist")); diff != "" {
		t.Errorf("allowlist exceptions mismatch (-want +got):\n%s", diff)
	}
}
