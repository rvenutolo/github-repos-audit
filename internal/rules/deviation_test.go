package rules_test

import (
	"fmt"
	"testing"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
	"github.com/rvenutolo/github-repos-audit/internal/rules"
)

// pathChecks are the checks whose cell carries a file path, beside the
// justfile and Renovate ones the report has always had. The new ones are
// listed with the field that feeds each, so a check wired to the wrong field
// fails here.
var pathChecks = []struct {
	check rules.Check
	set   func(f *audit.Files, path string)
}{
	{rules.CheckJustfile, func(f *audit.Files, p string) { f.JustfilePath = p }},
	{rules.CheckRenovate, func(f *audit.Files, p string) { f.RenovateConfig = p }},
	{rules.CheckLicenseFile, func(f *audit.Files, p string) { f.LicenseFile = p }},
	{rules.CheckSecurityPolicy, func(f *audit.Files, p string) { f.SecurityPolicy = p }},
	{rules.CheckCodeowners, func(f *audit.Files, p string) { f.Codeowners = p }},
	{rules.CheckPRTemplate, func(f *audit.Files, p string) { f.PRTemplate = p }},
	{rules.CheckClaudeMD, func(f *audit.Files, p string) { f.ClaudeMD = p }},
	{rules.CheckCommitlint, func(f *audit.Files, p string) { f.Commitlint = p }},
	{rules.CheckGitCliff, func(f *audit.Files, p string) { f.GitCliff = p }},
	{rules.CheckTreefmt, func(f *audit.Files, p string) { f.Treefmt = p }},
	{rules.CheckTypos, func(f *audit.Files, p string) { f.Typos = p }},
	{rules.CheckYamllint, func(f *audit.Files, p string) { f.Yamllint = p }},
	{rules.CheckMarkdownlint, func(f *audit.Files, p string) { f.Markdownlint = p }},
	{rules.CheckPrettier, func(f *audit.Files, p string) { f.Prettier = p }},
	{rules.CheckEditorconfigChecker, func(f *audit.Files, p string) { f.EditorconfigChecker = p }},
	{rules.CheckLychee, func(f *audit.Files, p string) { f.Lychee = p }},
	{rules.CheckZizmor, func(f *audit.Files, p string) { f.Zizmor = p }},
	{rules.CheckCodecov, func(f *audit.Files, p string) { f.Codecov = p }},
	{rules.CheckMkDocs, func(f *audit.Files, p string) { f.MkDocs = p }},
}

// forceWord returns the repository with one check forced to a word.
func forceWord(r audit.Repo, c rules.Check, word string) audit.Repo {
	r.Overrides = map[string]string{c.String(): word}
	return r
}

func mustCells(t *testing.T, r audit.Repo) map[rules.Check]rules.Cell {
	t.Helper()

	cells, err := evaluateOne(r)
	if err != nil {
		t.Fatalf("Evaluate() error = %v, want nil", err)
	}
	return cells
}

func TestEvaluate_pathChecks(t *testing.T) {
	t.Parallel()

	for _, pc := range pathChecks {
		t.Run(pc.check.String(), func(t *testing.T) {
			t.Parallel()

			// Both cells ignore the type, so each case forces the word.
			present := fixture("a", "tools")
			pc.set(&present.Files, "some/path.toml")
			cell := mustCells(t, forceWord(present, pc.check, rules.OverrideRequired))[pc.check]
			if cell.Value != "some/path.toml" || !cell.Code || !cell.Scalar || cell.Verdict != rules.Pass {
				t.Errorf("present, required: cell = %+v, want Pass carrying the path as code and scalar", cell)
			}

			absent := fixture("a", "tools")
			pc.set(&absent.Files, "")
			if pc.check == rules.CheckJustfile {
				absent.Files.Justfile = false
			}
			if got := mustCells(t, forceWord(absent, pc.check, rules.OverrideRequired))[pc.check].Verdict; got != rules.Fail {
				t.Errorf("absent, required: verdict = %v, want Fail", got)
			}
			if got := mustCells(t, forceWord(absent, pc.check, rules.OverrideNotRequired))[pc.check].Verdict; got != rules.NA {
				t.Errorf("absent, not_required: verdict = %v, want NA", got)
			}
		})
	}
}

func TestEvaluate_composeJoinsEveryFileName(t *testing.T) {
	t.Parallel()

	r := fixture("a", "tools")
	r.Files.Compose = []string{"compose.yaml", "docker-compose.yml"}
	cell := mustCells(t, forceWord(r, rules.CheckCompose, rules.OverrideRequired))[rules.CheckCompose]
	if cell.Value != "compose.yaml, docker-compose.yml" || cell.Verdict != rules.Pass {
		t.Errorf("compose cell = {%v %q}, want Pass with the names joined by \", \"", cell.Verdict, cell.Value)
	}

	r.Files.Compose = nil
	if got := mustCells(t, forceWord(r, rules.CheckCompose, rules.OverrideRequired))[rules.CheckCompose].Verdict; got != rules.Fail {
		t.Errorf("compose without files, required: verdict = %v, want Fail", got)
	}
}

func TestCheck_PathCheck(t *testing.T) {
	t.Parallel()

	want := map[rules.Check]bool{rules.CheckCompose: true}
	for _, pc := range pathChecks {
		want[pc.check] = true
	}
	for _, c := range rules.Checks() {
		if got := c.PathCheck(); got != want[c] {
			t.Errorf("%s.PathCheck() = %t, want %t", c, got, want[c])
		}
	}
}

// deviations evaluates one tools repository per value, with the given check
// forced on, and returns which of them deviate, by position.
func deviations(t *testing.T, c rules.Check, set func(*audit.Files, string), values ...string) []bool {
	t.Helper()

	repos := make([]audit.Repo, 0, len(values))
	for i, v := range values {
		r := forceWord(fixture(fmt.Sprintf("repo-%d", i), "tools"), c, rules.OverrideNotRequired)
		set(&r.Files, v)
		repos = append(repos, r)
	}
	rep, err := rules.Evaluate(&audit.Snapshot{Types: standardTypes(), Identity: fixtureIdentity(), Repos: repos})
	if err != nil {
		t.Fatalf("Evaluate() error = %v, want nil", err)
	}
	out := make([]bool, len(rep.Repos))
	for i := range rep.Repos { // repo-N names sort in input order for fewer than ten
		out[i] = rep.Repos[i].Cell(c).Deviates
	}
	return out
}

func TestMarkDeviations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		values []string
		want   []bool
	}{
		{"a unique plurality makes the rest deviate", []string{"a", "a", "b"}, []bool{false, false, true}},
		{"a tie between two names no standard", []string{"a", "b"}, []bool{false, false}},
		{"a single value names no standard", []string{"a"}, []bool{false}},
		{"an empty cell never deviates and is not counted", []string{"a", "a", ""}, []bool{false, false, false}},
		{"a tie for most common names no standard", []string{"a", "a", "b", "b", "c"}, []bool{false, false, false, false, false}},
		{"a case-only difference deviates", []string{"justfile", "justfile", "Justfile"}, []bool{false, false, true}},
		{"every deviating value is marked", []string{"a", "a", "a", "b", "c"}, []bool{false, false, false, true, true}},
		{"empty cells do not break a tie", []string{"a", "b", "", "", ""}, []bool{false, false, false, false, false}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := deviations(t, rules.CheckTypos, func(f *audit.Files, p string) { f.Typos = p }, tc.values...)
			if fmt.Sprint(got) != fmt.Sprint(tc.want) {
				t.Errorf("Deviates for %q = %v, want %v", tc.values, got, tc.want)
			}
		})
	}
}

func TestMarkDeviations_coversJustfileAndRenovate(t *testing.T) {
	t.Parallel()

	for _, pc := range pathChecks[:2] {
		got := deviations(t, pc.check, pc.set, "x", "x", "y")
		if fmt.Sprint(got) != fmt.Sprint([]bool{false, false, true}) {
			t.Errorf("%s: Deviates = %v, want only the third", pc.check, got)
		}
	}
}

func TestDeviation_onlyPathChecks(t *testing.T) {
	t.Parallel()

	repos := []audit.Repo{fixture("a", "tools"), fixture("b", "tools"), fixture("c", "tools")}
	repos[0].Topics, repos[1].Topics, repos[2].Topics = 1, 1, 7
	repos[0].License, repos[1].License, repos[2].License = "MIT", "MIT", "Apache-2.0"
	rep, err := rules.Evaluate(&audit.Snapshot{Types: standardTypes(), Identity: fixtureIdentity(), Repos: repos})
	if err != nil {
		t.Fatalf("Evaluate() error = %v, want nil", err)
	}
	for _, r := range rep.Repos {
		for _, c := range rules.Checks() {
			if cell := r.Cell(c); cell.Deviates && !c.PathCheck() {
				t.Errorf("%s: %s deviates (value %q) but is not a path check", r.Repo.Name, c, cell.Value)
			}
		}
	}
}
