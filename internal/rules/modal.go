package rules

import (
	"maps"
	"slices"
	"strings"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
)

// setting is one repository setting the modal computation tracks. Nothing
// declares an expected value for these: the tool computes the most common
// value across the repositories where the setting applies and reports the ones
// that differ.
//
// That beats declaring them twice over. It cannot drift from the OpenTofu
// baseline, because it asserts nothing; and it needs no maintenance when the
// baseline changes, because the majority simply moves.
type setting struct {
	// label is how the exception reads in the report.
	label string
	// read returns the repository's value, and whether the setting applies to
	// it at all. The denominator matters: actions-reuse applies only to
	// private repositories, and measuring it against every repository would
	// leave it permanently without consensus.
	read func(audit.Repo) (string, bool)
}

func onOff(b bool) string {
	if b {
		return "enabled"
	}
	return "disabled"
}

func always(f func(audit.Repo) string) func(audit.Repo) (string, bool) {
	return func(r audit.Repo) (string, bool) { return f(r), true }
}

// present treats an empty value as "the setting does not apply here", which is
// how every n/a in the collected model is spelled.
func present(f func(audit.Repo) string) func(audit.Repo) (string, bool) {
	return func(r audit.Repo) (string, bool) {
		v := f(r)
		return v, v != ""
	}
}

// settings is every tracked setting, in the order exceptions are reported.
//
// Push protection, the Actions allowlist and the approve-pull-requests flag
// are here rather than in a [types.*] table because they are collected
// but have no per-type expectation. Naming them explicitly matters: a field
// that is fetched and then rendered nowhere is exactly the defect the
// end-to-end test exists to catch.
//
//nolint:gochecknoglobals // immutable lookup table
var settings = []setting{
	{"issues", always(func(r audit.Repo) string { return onOff(r.Settings.HasIssues) })},
	{"wiki", always(func(r audit.Repo) string { return onOff(r.Settings.HasWiki) })},
	{"projects", always(func(r audit.Repo) string { return onOff(r.Settings.HasProjects) })},
	{"discussions", always(func(r audit.Repo) string { return onOff(r.Settings.HasDiscussions) })},
	{"vulnerability alerts", always(func(r audit.Repo) string { return onOff(r.Settings.VulnerabilityAlerts) })},
	{"Dependabot security updates", always(func(r audit.Repo) string { return onOff(r.Settings.DependabotSecurityUpdates) })},
	{"merge commits", always(func(r audit.Repo) string { return onOff(r.Settings.MergeCommitAllowed) })},
	{"squash merging", always(func(r audit.Repo) string { return onOff(r.Settings.SquashMergeAllowed) })},
	{"rebase merging", always(func(r audit.Repo) string { return onOff(r.Settings.RebaseMergeAllowed) })},
	{"merge commit title", present(func(r audit.Repo) string { return r.Settings.MergeCommitTitle })},
	{"merge commit message", present(func(r audit.Repo) string { return r.Settings.MergeCommitMessage })},
	{"auto-merge", always(func(r audit.Repo) string { return onOff(r.Settings.AutoMergeAllowed) })},
	{"delete branch on merge", always(func(r audit.Repo) string { return onOff(r.Settings.DeleteBranchOnMerge) })},
	{"secret-scanning push protection", present(func(r audit.Repo) string { return r.Settings.SecretScanningPushProtection })},
	{"Actions policy", present(func(r audit.Repo) string { return r.Settings.ActionsPolicy })},
	{"Actions SHA pinning", always(func(r audit.Repo) string { return onOff(r.Settings.SHAPinningRequired) })},
	{"Actions allowlist", allowlistPatterns},
	{"Actions allowlist: GitHub-owned", allowlistFlag(func(a audit.AllowedActions) bool { return a.GitHubOwnedAllowed })},
	{"Actions allowlist: verified", allowlistFlag(func(a audit.AllowedActions) bool { return a.VerifiedAllowed })},
	{"workflow token permissions", present(func(r audit.Repo) string { return r.Settings.DefaultWorkflowPermissions })},
	{"workflow token may approve pull requests", always(func(r audit.Repo) string { return onOff(r.Settings.CanApprovePullRequests) })},
	{"Actions reusable from other repositories", present(func(r audit.Repo) string { return r.Settings.ActionsAccessLevel })},
}

func allowlistPatterns(r audit.Repo) (string, bool) {
	if r.Settings.AllowedActions == nil {
		return "", false
	}
	if len(r.Settings.AllowedActions.Patterns) == 0 {
		return "none", true
	}
	return strings.Join(r.Settings.AllowedActions.Patterns, ", "), true
}

func allowlistFlag(f func(audit.AllowedActions) bool) func(audit.Repo) (string, bool) {
	return func(r audit.Repo) (string, bool) {
		if r.Settings.AllowedActions == nil {
			return "", false
		}
		return onOff(f(*r.Settings.AllowedActions)), true
	}
}

// modalExceptions computes, per setting, the most common value across the
// repositories where that setting applies, and returns the repositories that
// differ plus the settings that reached no consensus.
//
// Deliberate deviations stay listed. There is no accepted-exceptions list,
// because a deviation that had been forgotten could hide inside one — which is
// the one thing this report must not allow.
func modalExceptions(repos []audit.Repo) ([]Exception, []string) {
	var (
		exceptions  []Exception
		noConsensus []string
	)
	for _, s := range settings {
		values := make(map[string]string, len(repos)) // repo name -> value
		counts := make(map[string]int, len(repos))    // value -> how many
		for _, r := range repos {
			v, applies := s.read(r)
			if !applies {
				continue
			}
			values[r.Name] = v
			counts[v]++
		}
		if len(values) == 0 {
			continue
		}

		modal, ok := modalValue(counts, len(values))
		if !ok {
			noConsensus = append(noConsensus, s.label)
			continue
		}
		for _, name := range slices.SortedFunc(maps.Keys(values), compareNames) {
			if values[name] != modal {
				exceptions = append(exceptions, Exception{
					Repo: name, Setting: s.label, Value: values[name], Modal: modal,
				})
			}
		}
	}

	slices.SortFunc(exceptions, func(a, b Exception) int {
		if c := compareNames(a.Repo, b.Repo); c != 0 {
			return c
		}
		return strings.Compare(a.Setting, b.Setting)
	})
	slices.Sort(noConsensus)
	return exceptions, noConsensus
}

// modalValue returns the most common value, and whether it is common enough to
// call a norm. If it covers half or fewer of the repositories the setting
// applies to, there is no consensus: rendering seven exceptions out of twelve
// repositories would say nothing.
func modalValue(counts map[string]int, total int) (string, bool) {
	// Sorted, so an exact tie resolves the same way on every run rather than
	// following map order.
	//
	// `>` here is UNTESTABLE against `>=`, and deliberately left as the only
	// survivor of `just mutate` rather than chased with a test. The two differ
	// only when the highest count is tied between two values — `>` keeps the
	// first such value, `>=` the last — and a tie at the top can never be
	// returned: every repository the setting applies to contributes exactly one
	// increment, so the counts sum to `total`, and two values tied at the
	// maximum M force `total >= 2M`, which is precisely the no-consensus guard
	// below. The tied `best` is discarded before any caller sees it.
	//
	// Verified as well as argued: the whole suite, golden files included, is
	// green with `>=` substituted here. It is an equivalent mutant, so a test
	// claiming to kill it would be asserting something the function does not
	// promise.
	best, bestCount := "", 0
	for _, v := range slices.Sorted(maps.Keys(counts)) {
		if counts[v] > bestCount {
			best, bestCount = v, counts[v]
		}
	}
	if bestCount*2 <= total {
		return "", false
	}
	return best, true
}

// SettingLabels returns every tracked setting's label, in report order. It
// exists so a test can assert that the collected model has no field the report
// silently drops.
func SettingLabels() []string {
	out := make([]string, 0, len(settings))
	for _, s := range settings {
		out = append(out, s.label)
	}
	return out
}
