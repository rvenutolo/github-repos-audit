package render_test

import (
	"flag"
	"maps"
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

// update rewrites the golden files. go test parses flags before any test runs,
// so a package-level flag var is the idiomatic shape here.
var update = flag.Bool("update", false, "rewrite golden files")

// golden compares got against testdata/golden/<name>, rewriting it under
// -update. The diff is read before it is committed.
func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name)
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s (run `go test ./internal/render/ -update` to create it): %v", path, err)
	}
	if diff := cmp.Diff(string(want), got); diff != "" {
		t.Errorf("%s mismatch (-want +got):\n%s", name, diff)
	}
}

// clock returns an injected clock fixed at t.
func clock(t time.Time) func() time.Time { return func() time.Time { return t } }

func evaluate(t *testing.T, snap *audit.Snapshot) *rules.Report {
	t.Helper()
	rep, err := rules.Evaluate(snap)
	if err != nil {
		t.Fatalf("Evaluate() error = %v, want nil", err)
	}
	return rep
}

func TestBlock_golden(t *testing.T) {
	t.Parallel()

	got, err := render.Block(evaluate(t, fullSnapshot()), clock(renderedAt))
	if err != nil {
		t.Fatalf("Block() error = %v, want nil", err)
	}
	golden(t, "block.md", got)
}

func TestBlock_linksUseTheReportsOwner(t *testing.T) {
	t.Parallel()

	snap := fullSnapshot()
	snap.Owner = "another-owner"
	got, err := render.Block(evaluate(t, snap), clock(renderedAt))
	if err != nil {
		t.Fatalf("Block() error = %v", err)
	}
	if !strings.Contains(got, "(https://github.com/another-owner/") {
		t.Errorf("Block() links do not use the report's owner:\n%s", got)
	}
}

func TestBlock_rejectsAReportWithNoOwner(t *testing.T) {
	t.Parallel()

	if _, err := render.Block(&rules.Report{}, clock(renderedAt)); err == nil {
		t.Error("Block() error = nil, want one for a report with no owner")
	}
}

// TestBlock_nothingWrong is the empty-sections case: an account with no gaps,
// no settings exceptions and no overrides renders the tables, the identities
// list and the footer, and nothing else.
func TestBlock_nothingWrong(t *testing.T) {
	t.Parallel()

	a := baseRepo("alpha", "tools")
	b := baseRepo("bravo", "tools")
	snap := &audit.Snapshot{GeneratedAt: renderedAt, Owner: "gh-owner", Types: standardTypes(), Identity: fixtureIdentity(), Repos: []audit.Repo{a, b}}

	got, err := render.Block(evaluate(t, snap), clock(renderedAt))
	if err != nil {
		t.Fatalf("Block() error = %v, want nil", err)
	}
	if strings.Contains(got, "## Gaps") {
		t.Error("a clean account should render no Gaps section")
	}
	if strings.Contains(got, "## Overrides") {
		t.Error("an account with no overrides should render no Overrides section")
	}
	if strings.Contains(got, "### Workflows without cancel-in-progress") {
		t.Error("an account with nothing to list should render no cancel-in-progress list")
	}
	if strings.Contains(got, "### Jobs without timeouts") {
		t.Error("an account with nothing to list should render no timeout list")
	}
	golden(t, "clean.md", got)
}

// TestBlock_nixSection: the Nix section sits after File paths and before
// Policy, and carries a column per Nix check.
func TestBlock_nixSection(t *testing.T) {
	t.Parallel()

	got, err := render.Block(evaluate(t, fullSnapshot()), clock(renderedAt))
	if err != nil {
		t.Fatalf("Block() error = %v, want nil", err)
	}
	nix := strings.Index(got, "## Nix\n")
	if nix < 0 || nix < strings.Index(got, "## File paths\n") || nix > strings.Index(got, "## Policy\n") {
		t.Fatalf("Block() has no Nix section between File paths and Policy:\n%s", got)
	}
	section := got[nix:strings.Index(got, "## Policy\n")]
	for _, col := range []string{"| Repository ", "| Lock age ", "| Lock refresh ", "| Nix cache "} {
		if !strings.Contains(section, col) {
			t.Errorf("Nix section header lacks %q:\n%s", col, section)
		}
	}
}

// TestBlock_ciEfficiencySection: the CI efficiency section sits right after
// the Nix section and before Policy, carries Go cache, Cache use, Cancel
// superseded, Timeouts and Artifacts columns, a go_cache fail shows the size
// it found beside its mark, a cache_usage pass shows the usage it measured,
// artifact_storage shows its count and size with no mark, and the
// workflows behind a ci_cancel_superseded count, then the jobs behind a
// job_timeouts count, are listed under the table.
func TestBlock_ciEfficiencySection(t *testing.T) {
	t.Parallel()

	got, err := render.Block(evaluate(t, fullSnapshot()), clock(renderedAt))
	if err != nil {
		t.Fatalf("Block() error = %v, want nil", err)
	}
	ci, nix, policy := strings.Index(got, "## CI efficiency\n"), strings.Index(got, "## Nix\n"), strings.Index(got, "## Policy\n")
	if ci < 0 || nix < 0 || policy < 0 || ci < nix || ci > policy {
		t.Fatalf("Block() has no CI efficiency section between Nix and Policy:\n%s", got)
	}
	section := got[ci:policy]
	for _, col := range []string{"| Repository ", "| Go cache ", "| Cache use ", "| Cancel superseded ", "| Timeouts ", "| Artifacts "} {
		if !strings.Contains(section, col) {
			t.Errorf("CI efficiency section header lacks %q:\n%s", col, section)
		}
	}
	if !strings.Contains(section, "✗ empty (430 B)") {
		t.Errorf("CI efficiency section lacks the tools repository's fail:\n%s", section)
	}
	if !strings.Contains(section, "✓ 2.3 GB") {
		t.Errorf("CI efficiency section lacks the tools repository's cache usage:\n%s", section)
	}
	if !strings.Contains(section, "✗ 2 workflows") {
		t.Errorf("CI efficiency section lacks the tools repository's cancel count:\n%s", section)
	}
	if !strings.Contains(section, "| 12 · 206 MB ") {
		t.Errorf("CI efficiency section lacks the software repository's artifacts:\n%s", section)
	}
	if !strings.Contains(section, "✗ 1 job ") {
		t.Errorf("CI efficiency section lacks the tools repository's timeout count:\n%s", section)
	}
	lists := "\n### Workflows without cancel-in-progress\n\n" +
		"- **github-repos-audit** — `.github/workflows/ci.yml`, `.github/workflows/workflow-02.yml`\n" +
		"\n### Jobs without timeouts\n\n" +
		"- **github-repos-audit** — `workflow-02.yml / lint`\n" +
		"\n### Unrecognised caches\n\n" +
		"- **github-repos-audit** — `custom-1e2f` (2 MB, main), `custom-5c6d` (3 MB, PR 12)\n" +
		"\n### Action-owned caches\n\n" +
		"- **github-repos-audit** — `trivy-3a4b` (61 MB, main)\n" +
		"\n### Pull-request caches\n\n" +
		"- **mixedCase-flake** — 1 cache · 900 B\n"
	if !strings.HasSuffix(section, lists+"\n") {
		t.Errorf("CI efficiency section does not end with the cancel-in-progress, timeout and cache lists %q:\n%s", lists, section)
	}
}

// TestBlock_cacheListKeysAreFreeText: a cache key is whatever a workflow
// passed to a cache action, so it is rendered as inline code that a
// backtick cannot close and a line break cannot end the bullet of, and a
// key holding " (" keeps it inside the code span. The size and ref follow
// in parentheses, the ref outside the code span with markdown's own
// characters escaped, since a branch name is the owner's choice too.
func TestBlock_cacheListKeysAreFreeText(t *testing.T) {
	t.Parallel()

	rep := &rules.Report{Owner: "gh-owner", Repos: []rules.RepoReport{{Repo: audit.Repo{Name: "alpha"}}}, UnrecognisedCaches: []rules.RepoList[rules.CacheItem]{{Repo: "alpha", Items: []rules.CacheItem{
		{Key: "a`b", Size: "1 KB", Ref: "main"},
		{Key: "line\r\nbreak\nhere", Size: "2 B", Ref: "PR 12"},
		{Key: "x (y)", Size: "3 MB", Ref: "my_branch*<b>"},
		{Key: "a|b", Size: "4 B", Ref: "a`b\nc"},
	}}}}
	got, err := render.Block(rep, clock(renderedAt))
	if err != nil {
		t.Fatalf("Block() error = %v, want nil", err)
	}
	want := "\n### Unrecognised caches\n\n" +
		"- **alpha** — `` a`b `` (1 KB, main), `line break here` (2 B, PR 12), " +
		"`x (y)` (3 MB, my\\_branch\\*\\<b\\>), `a|b` (4 B, a\\`b c)\n"
	if !strings.Contains(got, want) {
		t.Errorf("Block() lacks %q:\n%s", want, got)
	}
}

// TestBlock_cacheListsOmittedWhenEmpty: with no entries, none of the three
// cache lists has a subheading.
func TestBlock_cacheListsOmittedWhenEmpty(t *testing.T) {
	t.Parallel()

	a := baseRepo("alpha", "tools")
	snap := &audit.Snapshot{GeneratedAt: renderedAt, Owner: "gh-owner", Types: standardTypes(), Identity: fixtureIdentity(), Repos: []audit.Repo{a}}
	got, err := render.Block(evaluate(t, snap), clock(renderedAt))
	if err != nil {
		t.Fatalf("Block() error = %v, want nil", err)
	}
	for _, h := range []string{"### Unrecognised caches", "### Action-owned caches", "### Pull-request caches"} {
		if strings.Contains(got, h) {
			t.Errorf("Block() has %q with no cache to list:\n%s", h, got)
		}
	}
}

// TestBlock_cancelMissingUnreadable: an unreadable workflow is listed with
// its word outside the code span, so the path reads as a path and the word
// as what was found about it.
func TestBlock_cancelMissingUnreadable(t *testing.T) {
	t.Parallel()

	a := baseRepo("alpha", "tools")
	a.Workflows = []audit.Workflow{{Path: ".github/workflows/broken.yml", ParseError: "invalid YAML"}}
	snap := &audit.Snapshot{GeneratedAt: renderedAt, Owner: "gh-owner", Types: standardTypes(), Identity: fixtureIdentity(), Repos: []audit.Repo{a}}
	got, err := render.Block(evaluate(t, snap), clock(renderedAt))
	if err != nil {
		t.Fatalf("Block() error = %v, want nil", err)
	}
	want := "- **alpha** — unreadable `.github/workflows/broken.yml`\n"
	if !strings.Contains(got, want) {
		t.Errorf("Block() lacks %q:\n%s", want, got)
	}
}

func TestBlock_rejectsNilArguments(t *testing.T) {
	t.Parallel()

	if _, err := render.Block(nil, clock(renderedAt)); err == nil {
		t.Error("Block(nil, clock) error = nil, want an error")
	}
	if _, err := render.Block(&rules.Report{}, nil); err == nil {
		t.Error("Block(report, nil) error = nil, want an error")
	}
}

func TestBlock_padsItsOwnColumns(t *testing.T) {
	t.Parallel()

	got, err := render.Block(evaluate(t, fullSnapshot()), clock(renderedAt))
	if err != nil {
		t.Fatalf("Block() error = %v, want nil", err)
	}
	// Every row must be as wide as its header; see the package doc in
	// markdown.go for why the renderer pads.
	var widths []int
	for line := range strings.SplitSeq(got, "\n") {
		if !strings.HasPrefix(line, "|") {
			widths = nil
			continue
		}
		w := len([]rune(line))
		widths = append(widths, w)
		if widths[0] != w {
			t.Fatalf("table row %q is %d wide, want %d:\n%s", line, w, widths[0], got)
		}
	}
}

func TestBlock_footerCarriesTheRenderDate(t *testing.T) {
	t.Parallel()

	got, err := render.Block(evaluate(t, fullSnapshot()), clock(renderedAt))
	if err != nil {
		t.Fatalf("Block() error = %v, want nil", err)
	}
	if !strings.Contains(got, "_Read from GitHub on 2026-09-05._") {
		t.Errorf("footer missing from:\n%s", got)
	}
}

func TestBlock_relativeDates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		pushed time.Time
		want   string
	}{
		{"today", renderedAt.Add(-2 * time.Hour), "today"},
		{"days", at(2026, time.August, 24), "11d"},
		{"months", at(2026, time.April, 2), "5mo"},
		{"years", at(2022, time.January, 1), "4y"},
		{"a clock skewed into the future reads as today", renderedAt.Add(48 * time.Hour), "today"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := baseRepo("alpha", "tools")
			r.PushedAt = tc.pushed
			snap := &audit.Snapshot{GeneratedAt: renderedAt, Owner: "gh-owner", Types: standardTypes(), Identity: fixtureIdentity(), Repos: []audit.Repo{r}}
			got, err := render.Block(evaluate(t, snap), clock(renderedAt))
			if err != nil {
				t.Fatalf("Block() error = %v, want nil", err)
			}
			if !strings.Contains(got, "| "+tc.want+" ") {
				t.Errorf("want a %q cell in:\n%s", tc.want, got)
			}
		})
	}
}

func TestBlock_escapesAPipeInADescription(t *testing.T) {
	t.Parallel()

	r := baseRepo("alpha", "tools")
	r.Description = "before | after"
	snap := &audit.Snapshot{GeneratedAt: renderedAt, Owner: "gh-owner", Types: standardTypes(), Identity: fixtureIdentity(), Repos: []audit.Repo{r}}

	got, err := render.Block(evaluate(t, snap), clock(renderedAt))
	if err != nil {
		t.Fatalf("Block() error = %v, want nil", err)
	}
	// An unescaped pipe would end the cell and shift every column after it.
	if !strings.Contains(got, `before \| after`) {
		t.Errorf("want the pipe escaped in:\n%s", got)
	}
}

// TestBlock_lineBreaksInADescription: a line break would end the table row,
// so each becomes one space, a CRLF pair included, the same rule the lists
// under the tables follow.
func TestBlock_lineBreaksInADescription(t *testing.T) {
	t.Parallel()

	r := baseRepo("alpha", "tools")
	r.Description = "one\r\ntwo\nthree\rfour"
	snap := &audit.Snapshot{GeneratedAt: renderedAt, Owner: "gh-owner", Types: standardTypes(), Identity: fixtureIdentity(), Repos: []audit.Repo{r}}

	got, err := render.Block(evaluate(t, snap), clock(renderedAt))
	if err != nil {
		t.Fatalf("Block() error = %v, want nil", err)
	}
	if !strings.Contains(got, "one two three four") {
		t.Errorf("want each line break as one space in:\n%s", got)
	}
}

// TestBlock_listPathsAreOneLine: a workflow file name is the owner's
// choice and may hold a line break, which would end the bullet, so a path
// in the lists under a table is put on one line, unreadable or not.
func TestBlock_listPathsAreOneLine(t *testing.T) {
	t.Parallel()

	rep := &rules.Report{
		Owner: "gh-owner", Repos: []rules.RepoReport{{Repo: audit.Repo{Name: "alpha"}}},
		CancelMissing: []rules.RepoList[string]{{Repo: "alpha", Items: []string{
			".github/workflows/a\nb.yml", "unreadable .github/workflows/c\r\nd.yml",
		}}},
	}
	got, err := render.Block(rep, clock(renderedAt))
	if err != nil {
		t.Fatalf("Block() error = %v, want nil", err)
	}
	want := "- **alpha** — `.github/workflows/a b.yml`, unreadable `.github/workflows/c d.yml`\n"
	if !strings.Contains(got, want) {
		t.Errorf("Block() lacks %q:\n%s", want, got)
	}
}

// blockOf renders rep, failing the test on error.
func blockOf(t *testing.T, rep *rules.Report) string {
	t.Helper()
	got, err := render.Block(rep, clock(renderedAt))
	if err != nil {
		t.Fatalf("Block() error = %v, want nil", err)
	}
	return got
}

// wantOneLine fails unless got holds want as one span and no line of it
// starts with the second half of a broken value.
func wantOneLine(t *testing.T, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Errorf("Block() lacks %q:\n%s", want, got)
	}
	for line := range strings.SplitSeq(got, "\n") {
		if strings.HasPrefix(line, "b") {
			t.Errorf("a line break split a bullet: line %q in:\n%s", line, got)
		}
	}
}

// TestBlock_staleEntryNamesAreOneLine: a stale entry is a repos.toml key, so
// it is free text and may hold a line break, which would end the bullet.
func TestBlock_staleEntryNamesAreOneLine(t *testing.T) {
	t.Parallel()

	rep := &rules.Report{Owner: "gh-owner", Drift: audit.ConfigDrift{StaleEntries: []string{"a\nb", "a\r\nb"}}}
	wantOneLine(t, blockOf(t, rep), "`a b`, `a b`")
}

// TestBlock_gitIdentitiesAreOneLine: an identity comes from commit metadata
// and is rendered in a bullet, so a line break in it must not end the bullet.
func TestBlock_gitIdentitiesAreOneLine(t *testing.T) {
	t.Parallel()

	rep := &rules.Report{Owner: "gh-owner", Identities: []rules.IdentityLine{{
		Repo:       "alpha",
		Identities: []rules.IdentityEntry{{Name: "a\nb", Email: "x@example.invalid"}},
	}}}
	wantOneLine(t, blockOf(t, rep), "`a b <x@example.invalid>`")
}

// TestBlock_exceptionValuesAreOneLine: a deviating value and the usual one
// are settings read from the repository, so either may hold a line break.
func TestBlock_exceptionValuesAreOneLine(t *testing.T) {
	t.Parallel()

	rep := &rules.Report{Owner: "gh-owner", Exceptions: []rules.Exception{
		{Repo: "alpha", Setting: "wiki", Value: "a\nb", Modal: "a\r\nb"},
	}}
	wantOneLine(t, blockOf(t, rep), "wiki `a b`, usually `a b`")
}

// TestBlock_overrideValuesAreOneLine: an override value is whatever
// repos.toml says, so it may hold a line break.
func TestBlock_overrideValuesAreOneLine(t *testing.T) {
	t.Parallel()

	rep := &rules.Report{Owner: "gh-owner", Overrides: []rules.Override{
		{Repo: "alpha", Check: rules.CheckREADME, Value: "a\r\nb"},
	}}
	wantOneLine(t, blockOf(t, rep), "is `a b`")
}

// TestBlock_naCellCarryingAScalar is the "an n/a cell still shows a value it
// has" decision, seen from the renderer's side.
func TestBlock_naCellCarryingAScalar(t *testing.T) {
	t.Parallel()

	// Public but unpublished: the topics row is n/a and the repository has six.
	r := baseRepo("cipher-lib", "content")
	r.Visibility = "public"
	r.Topics = 6
	snap := &audit.Snapshot{GeneratedAt: renderedAt, Owner: "gh-owner", Types: standardTypes(), Identity: fixtureIdentity(), Repos: []audit.Repo{r}}

	got, err := render.Block(evaluate(t, snap), clock(renderedAt))
	if err != nil {
		t.Fatalf("Block() error = %v, want nil", err)
	}
	if !strings.Contains(got, "| 6 ") {
		t.Errorf("want the topic count shown despite the n/a verdict:\n%s", got)
	}
	// flake.nix is present-or-absent, so its n/a cell reads n/a;
	// rules.Cell.Scalar says why.
	if !strings.Contains(got, "| n/a ") {
		t.Errorf("want an n/a cell in:\n%s", got)
	}
}

// TestBlock_backticksABareURLInADescription guards a repository description
// carrying a plain URL; escape() in markdown.go says why it is backticked.
func TestBlock_backticksABareURLInADescription(t *testing.T) {
	t.Parallel()

	r := baseRepo("config-files", "environment")
	r.Description = "Config files managed by chezmoi - https://www.chezmoi.io/"
	snap := &audit.Snapshot{GeneratedAt: renderedAt, Owner: "gh-owner", Types: standardTypes(), Identity: fixtureIdentity(), Repos: []audit.Repo{r}}

	got, err := render.Block(evaluate(t, snap), clock(renderedAt))
	if err != nil {
		t.Fatalf("Block() error = %v, want nil", err)
	}
	if !strings.Contains(got, "`https://www.chezmoi.io/`") {
		t.Errorf("want the bare URL backticked in:\n%s", got)
	}
	// Not a link: see escape() in markdown.go.
	if strings.Contains(got, "](https://www.chezmoi.io/)") {
		t.Error("the URL became a link; lychee would then check a host this project does not control")
	}
}

// TestBlock_emptyReport is the degenerate account: no repositories at all. A
// table with no rows renders as nothing rather than as a header over empty
// space, so every section disappears and only the footer is left.
func TestBlock_emptyReport(t *testing.T) {
	t.Parallel()

	got, err := render.Block(&rules.Report{Owner: "gh-owner"}, clock(renderedAt))
	if err != nil {
		t.Fatalf("Block() error = %v, want nil", err)
	}
	want := "_Read from GitHub on 2026-09-05._\n"
	if got != want {
		t.Errorf("Block() = %q, want just the footer %q", got, want)
	}
}

// TestBlock_widensTheFenceAroundABacktick covers a value GitHub will happily
// hand back and markdown cannot render naively. A single-backtick fence around
// a string containing a backtick closes early and corrupts the rest of the
// row, so the fence widens and the value is padded away from it.
func TestBlock_widensTheFenceAroundABacktick(t *testing.T) {
	t.Parallel()

	r := baseRepo("alpha", "tools")
	r.Files.RenovateConfig = "re``no`vate.json"
	snap := &audit.Snapshot{GeneratedAt: renderedAt, Owner: "gh-owner", Types: standardTypes(), Identity: fixtureIdentity(), Repos: []audit.Repo{r}}

	got, err := render.Block(evaluate(t, snap), clock(renderedAt))
	if err != nil {
		t.Fatalf("Block() error = %v, want nil", err)
	}
	// Three backticks: the value's longest run is two, so the fence clears it.
	want := "``` re``no`vate.json ```"
	if !strings.Contains(got, want) {
		t.Errorf("Block() should fence the value as %q; got:\n%s", want, got)
	}
}

// TestBlock_noConsensusWithoutExceptions covers the one arrangement in which
// the section exists but its list does not: every setting either matches the
// norm or has no norm to match. Saying so beats a bare heading over nothing.
func TestBlock_noConsensusWithoutExceptions(t *testing.T) {
	t.Parallel()

	// Two repositories, split evenly on the wiki: at exactly half there is no
	// majority, so neither is an exception and the setting has no consensus.
	a := baseRepo("alpha", "tools")
	b := baseRepo("bravo", "tools")
	b.Settings.HasWiki = true
	snap := &audit.Snapshot{GeneratedAt: renderedAt, Owner: "gh-owner", Types: standardTypes(), Identity: fixtureIdentity(), Repos: []audit.Repo{a, b}}

	rep := evaluate(t, snap)
	if len(rep.Exceptions) != 0 {
		t.Fatalf("Exceptions = %+v, want none for this test to be about what it says", rep.Exceptions)
	}
	if len(rep.NoConsensus) == 0 {
		t.Fatal("NoConsensus = none, want the evenly split setting")
	}

	got, err := render.Block(rep, clock(renderedAt))
	if err != nil {
		t.Fatalf("Block() error = %v, want nil", err)
	}
	if !strings.Contains(got, "No repository deviates from the account's usual settings.") {
		t.Errorf("Block() should say the exception list is empty; got:\n%s", got)
	}
	if !strings.Contains(got, "### No consensus") {
		t.Errorf("Block() should still list the settings with no norm; got:\n%s", got)
	}
}

// Identities the Git identities tests use: the fixture vocabulary only. The
// bot and GitHub identities never appear in Markdown, since they are never
// the owner's; the fake noreply address does, as an accepted identity, and
// internal/fixturevocab holds it to the identity vocabulary rather than the
// host scan.
var (
	patCanonical = audit.Identity{Name: "Pat Example", Email: "pat@example.com"}
	patOther     = audit.Identity{Name: "Pat Example", Email: "pat@example.org"}
	patrick      = audit.Identity{Name: "Patrick Example", Email: "patrick@example.org"}
	robin        = audit.Identity{Name: "Robin Other", Email: "robin@example.org"}
	patNoreply   = audit.Identity{Name: "Pat Example", Email: "12345+pat@users.noreply.github.com"}
)

// withTypesWord is standardTypes with git_identity at word in every type.
func withTypesWord(word string) rules.Types {
	types := make(rules.Types, len(standardTypes()))
	for name, entries := range standardTypes() {
		// standardTypes is shared read-only, so each table is copied.
		copied := maps.Clone(entries)
		copied[rules.CheckGitIdentity.String()] = word
		types[name] = copied
	}
	return types
}

// policyCells finds a repository's row in the Policy table and returns its
// cells keyed by column header. The columns are padded, so cells are split
// and trimmed rather than matched as raw strings.
func policyCells(t *testing.T, block, repo string) map[string]string {
	t.Helper()
	_, after, ok := strings.Cut(block, "## Policy\n\n")
	if !ok {
		t.Fatalf("no Policy section in:\n%s", block)
	}
	var header []string
	for line := range strings.SplitSeq(after, "\n") {
		if !strings.HasPrefix(line, "|") {
			break
		}
		cells := splitRow(line)
		if header == nil {
			header = cells
			continue
		}
		if strings.HasPrefix(cells[0], "["+repo+"]") {
			out := make(map[string]string, len(header))
			for i, h := range header {
				out[h] = cells[i]
			}
			return out
		}
	}
	t.Fatalf("no Policy row for %s in:\n%s", repo, block)
	return nil
}

func splitRow(line string) []string {
	parts := strings.Split(strings.Trim(line, "|"), "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func TestBlock_gitIdentities(t *testing.T) {
	t.Parallel()

	alpha := baseRepo("alpha", "tools")
	alpha.Identities = []audit.Identity{patCanonical, patrick}
	bravo := baseRepo("bravo", "tools")
	bravo.Identities = []audit.Identity{robin}
	// An override and a settings deviation, so the Overrides and Settings
	// exceptions sections exist to be ordered against.
	bravo.Overrides = map[string]string{"flake_nix": rules.OverrideNotRequired}
	bravo.Settings.HasWiki = true
	charlie := baseRepo("charlie", "tools")
	charlie.Identities = []audit.Identity{patOther}
	delta := baseRepo("delta", "tools")
	delta.Identities = []audit.Identity{patNoreply, patCanonical}
	// Only the accepted identity: listed, tagged, and still a pass.
	echo := baseRepo("echo", "tools")
	echo.Identities = []audit.Identity{patNoreply}
	snap := &audit.Snapshot{
		GeneratedAt: renderedAt, Owner: "gh-owner", Types: standardTypes(), Identity: fixtureIdentity(),
		Repos: []audit.Repo{alpha, bravo, charlie, delta, echo},
	}

	got, err := render.Block(evaluate(t, snap), clock(renderedAt))
	if err != nil {
		t.Fatalf("Block() error = %v, want nil", err)
	}
	want := "## Git identities\n\n" +
		"- **alpha** — `Pat Example <pat@example.com>` (canonical), `Patrick Example <patrick@example.org>` — mixed\n" +
		"- **charlie** — `Pat Example <pat@example.org>` — not canonical\n" +
		"- **delta** — `Pat Example <pat@example.com>` (canonical), `Pat Example <12345+pat@users.noreply.github.com>` (accepted)\n" +
		"- **echo** — `Pat Example <12345+pat@users.noreply.github.com>` (accepted)\n"
	if !strings.Contains(got, want) {
		t.Fatalf("Block() should contain\n%s\ngot:\n%s", want, got)
	}
	// A repository with none of the owner's identities has nothing to rewrite.
	// Only the section is searched: bravo has an Overrides line of the same shape.
	_, section, _ := strings.Cut(got, "## Git identities\n\n")
	section, _, _ = strings.Cut(section, "\n\n")
	if strings.Contains(section, "**bravo**") {
		t.Errorf("bravo carries only another person's identity and should have no line:\n%s", section)
	}
	activity := strings.Index(got, "## Activity")
	identities := strings.Index(got, "## Git identities")
	exceptions := strings.Index(got, "## Settings exceptions")
	overrides := strings.Index(got, "## Overrides")
	if activity < 0 || exceptions < 0 || overrides < 0 {
		t.Fatalf("want Activity, Settings exceptions and Overrides sections for this test to mean anything:\n%s", got)
	}
	if activity >= identities || identities >= exceptions || identities >= overrides {
		t.Errorf("want Git identities after Activity and before Settings exceptions and Overrides:\n%s", got)
	}

	cells := policyCells(t, got, "charlie")
	if cells["Identity"] != "✗ 1 wrong" {
		t.Errorf("charlie's Identity cell = %q, want %q", cells["Identity"], "✗ 1 wrong")
	}
	if cells := policyCells(t, got, "delta"); cells["Identity"] != "✓" {
		t.Errorf("delta's Identity cell = %q, want %q", cells["Identity"], "✓")
	}
	if cells := policyCells(t, got, "echo"); cells["Identity"] != "✓" {
		t.Errorf("echo's Identity cell = %q, want %q", cells["Identity"], "✓")
	}
}

func TestBlock_identityColumnFollowsSigned(t *testing.T) {
	t.Parallel()

	got, err := render.Block(evaluate(t, fullSnapshot()), clock(renderedAt))
	if err != nil {
		t.Fatalf("Block() error = %v, want nil", err)
	}
	_, after, _ := strings.Cut(got, "## Policy\n\n")
	header, _, _ := strings.Cut(after, "\n")
	cols := splitRow(header)
	i := slices.Index(cols, "Signed")
	if i < 0 || i+1 >= len(cols) || cols[i+1] != "Identity" {
		t.Errorf("Policy header = %q, want Identity immediately after Signed", cols)
	}
}

func TestBlock_noIdentitiesSectionWithoutAStandard(t *testing.T) {
	t.Parallel()

	r := baseRepo("alpha", "tools")
	r.Identities = []audit.Identity{patOther}
	snap := &audit.Snapshot{
		GeneratedAt: renderedAt, Owner: "gh-owner", Types: withTypesWord(rules.OverrideNotRequired),
		Repos: []audit.Repo{r},
	}
	got, err := render.Block(evaluate(t, snap), clock(renderedAt))
	if err != nil {
		t.Fatalf("Block() error = %v, want nil", err)
	}
	if strings.Contains(got, "## Git identities") {
		t.Errorf("with no identity standard there should be no Git identities section:\n%s", got)
	}
}

// TestBlock_identityInfoCells: an informational type shows the count and
// never a mark, and n/a where nothing is wrong.
func TestBlock_identityInfoCells(t *testing.T) {
	t.Parallel()

	fine := baseRepo("alpha", "tools")
	fine.Identities = []audit.Identity{patCanonical}
	wrong := baseRepo("bravo", "tools")
	wrong.Identities = []audit.Identity{patOther}
	accepted := baseRepo("charlie", "tools")
	accepted.Identities = []audit.Identity{patNoreply}
	snap := &audit.Snapshot{
		GeneratedAt: renderedAt, Owner: "gh-owner", Types: withTypesWord("info"), Identity: fixtureIdentity(),
		Repos: []audit.Repo{fine, wrong, accepted},
	}
	got, err := render.Block(evaluate(t, snap), clock(renderedAt))
	if err != nil {
		t.Fatalf("Block() error = %v, want nil", err)
	}
	if cell := policyCells(t, got, "alpha")["Identity"]; cell != "n/a" {
		t.Errorf("alpha's Identity cell = %q, want n/a", cell)
	}
	if cell := policyCells(t, got, "bravo")["Identity"]; cell != "1 wrong" {
		t.Errorf("bravo's Identity cell = %q, want %q", cell, "1 wrong")
	}
	if cell := policyCells(t, got, "charlie")["Identity"]; cell != "n/a" {
		t.Errorf("charlie's Identity cell = %q, want n/a", cell)
	}
}

// TestBlock_widensTheFenceAroundABacktickInAnIdentity: a commit's name is
// whatever its author typed, so a backtick in one must not break the bullet.
func TestBlock_widensTheFenceAroundABacktickInAnIdentity(t *testing.T) {
	t.Parallel()

	r := baseRepo("alpha", "tools")
	r.Identities = []audit.Identity{{Name: "Pat `Example", Email: "pat@example.com"}}
	std := fixtureIdentity()
	std.Match = "Example <" // " Example <" would not see past the backtick
	snap := &audit.Snapshot{GeneratedAt: renderedAt, Owner: "gh-owner", Types: standardTypes(), Identity: std, Repos: []audit.Repo{r}}
	got, err := render.Block(evaluate(t, snap), clock(renderedAt))
	if err != nil {
		t.Fatalf("Block() error = %v, want nil", err)
	}
	want := "- **alpha** — `` Pat `Example <pat@example.com> `` — not canonical\n"
	if !strings.Contains(got, want) {
		t.Errorf("Block() should contain %q; got:\n%s", want, got)
	}
}

// TestBlock_configDrift: a repository with no entry, or an entry with no
// repository, is the first thing the report says, because it is the one
// finding that means the rest of the report is incomplete.
func TestBlock_configDrift(t *testing.T) {
	t.Parallel()

	got, err := render.Block(evaluate(t, driftSnapshot()), clock(renderedAt))
	if err != nil {
		t.Fatalf("Block() error = %v, want nil", err)
	}
	if !strings.HasPrefix(got, "## Configuration drift\n") {
		t.Errorf("the block does not open with the drift section:\n%s", got)
	}
	// Linked, since the repository exists.
	if want := "[hook-guard](https://github.com/gh-owner/hook-guard)"; !strings.Contains(got, want) {
		t.Errorf("the block does not link the unconfigured repository %s", want)
	}
	// Not linked: there may be nothing at the other end.
	if strings.Contains(got, "gh-owner/media-server") {
		t.Error("the block links a stale entry, whose repository may not exist")
	}
	if !strings.Contains(got, "`media-server`") {
		t.Error("the block does not name the stale entry")
	}
	// The configured repositories are still reported.
	for _, name := range []string{"alpha", "bravo"} {
		if !strings.Contains(got, "["+name+"](https://github.com/gh-owner/"+name+")") {
			t.Errorf("the block dropped the configured repository %s", name)
		}
	}
	golden(t, "drift.md", got)
}

// TestBlock_configDriftListsOnlyWhatDrifted: each direction has its own
// bullet, and a bullet with nothing under it is not written.
func TestBlock_configDriftListsOnlyWhatDrifted(t *testing.T) {
	t.Parallel()

	const unconfigured, stale = "Needs a `repos.toml` entry", "Stale `repos.toml` entry"
	tests := []struct {
		name    string
		drift   audit.ConfigDrift
		want    string
		wantNot string
	}{
		{name: "only unconfigured", drift: audit.ConfigDrift{Unconfigured: []string{"hook-guard"}}, want: unconfigured, wantNot: stale},
		{name: "only stale", drift: audit.ConfigDrift{StaleEntries: []string{"media-server"}}, want: stale, wantNot: unconfigured},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			snap := driftSnapshot()
			snap.Drift = tc.drift
			got, err := render.Block(evaluate(t, snap), clock(renderedAt))
			if err != nil {
				t.Fatalf("Block() error = %v, want nil", err)
			}
			if !strings.Contains(got, "**"+tc.want+"**") {
				t.Errorf("the block has no %q bullet:\n%s", tc.want, got)
			}
			if strings.Contains(got, "**"+tc.wantNot+"**") {
				t.Errorf("the block has an empty %q bullet", tc.wantNot)
			}
		})
	}
}

// TestBlock_configDriftCountsAndJoins: several names share one bullet, in the
// order the report was given, with the count the Gaps bullets carry.
func TestBlock_configDriftCountsAndJoins(t *testing.T) {
	t.Parallel()

	snap := driftSnapshot()
	snap.Drift = audit.ConfigDrift{StaleEntries: []string{"media-server", "updater"}}
	got, err := render.Block(evaluate(t, snap), clock(renderedAt))
	if err != nil {
		t.Fatalf("Block() error = %v, want nil", err)
	}
	if want := "(2) — `media-server`, `updater`\n"; !strings.Contains(got, want) {
		t.Errorf("the block does not carry %q:\n%s", want, got)
	}
}

// TestBlock_noDriftSectionWhenTheConfigMatches: the section is a finding, so
// an account whose config matches has none.
func TestBlock_noDriftSectionWhenTheConfigMatches(t *testing.T) {
	t.Parallel()

	snap := driftSnapshot()
	snap.Drift = audit.ConfigDrift{}
	got, err := render.Block(evaluate(t, snap), clock(renderedAt))
	if err != nil {
		t.Fatalf("Block() error = %v, want nil", err)
	}
	if strings.Contains(got, "Configuration drift") {
		t.Errorf("a matching config rendered a drift section:\n%s", got)
	}
}

// TestBlock_configDriftComesBeforeTheGaps: the worklist below it is only as
// complete as the config, so the drift is read first.
func TestBlock_configDriftComesBeforeTheGaps(t *testing.T) {
	t.Parallel()

	snap := fullSnapshot()
	snap.Drift = audit.ConfigDrift{Unconfigured: []string{"hook-guard"}}
	got, err := render.Block(evaluate(t, snap), clock(renderedAt))
	if err != nil {
		t.Fatalf("Block() error = %v, want nil", err)
	}
	drift, gaps := strings.Index(got, "## Configuration drift"), strings.Index(got, "## Gaps")
	if drift < 0 || gaps < 0 || drift > gaps {
		t.Errorf("drift section at %d, Gaps at %d; want drift first and both present", drift, gaps)
	}
}

// sectionTable returns the header cells of the first table under heading in
// block, or fails when the heading is absent.
func sectionTable(t *testing.T, block, heading string) []string {
	t.Helper()
	_, after, ok := strings.Cut(block, heading+"\n\n")
	if !ok {
		t.Fatalf("no %q in:\n%s", heading, block)
	}
	for line := range strings.SplitSeq(after, "\n") {
		if strings.HasPrefix(line, "|") {
			return splitRow(line)
		}
	}
	t.Fatalf("no table under %q in:\n%s", heading, block)
	return nil
}

func TestBlock_filePathsSection(t *testing.T) {
	t.Parallel()

	got, err := render.Block(evaluate(t, fullSnapshot()), clock(renderedAt))
	if err != nil {
		t.Fatalf("Block() error = %v, want nil", err)
	}

	const intro = "Where each tooling and community file lives. " +
		"`≠` marks a path that differs from the one most repositories use.\n"
	positions := []string{"## File paths\n\n" + intro, "### Build & release\n", "### Linters\n", "### GitHub & docs\n"}
	last := -1
	for _, p := range positions {
		i := strings.Index(got, p)
		if i < 0 {
			t.Fatalf("block has no %q:\n%s", p, got)
		}
		if i < last {
			t.Errorf("%q appears before the heading above it", p)
		}
		last = i
	}

	tests := []struct {
		heading string
		want    []string
	}{
		{"### Build & release", []string{"Repository", "justfile", "Renovate", "commitlint", "git-cliff", "treefmt", "Compose"}},
		{"### Linters", []string{
			"Repository", "typos", "yamllint", "markdownlint", "Prettier", "editorconfig-checker",
			"lychee", "zizmor", "Codecov", "MkDocs",
		}},
		{"### GitHub & docs", []string{"Repository", "LICENSE", "SECURITY", "CODEOWNERS", "PR template", "CLAUDE.md", "MCP config", "Claude settings"}},
	}
	for _, tc := range tests {
		if diff := cmp.Diff(tc.want, sectionTable(t, got, tc.heading)); diff != "" {
			t.Errorf("%s columns mismatch (-want +got):\n%s", tc.heading, diff)
		}
	}

	// The Claude Code configuration columns name the files as code, all of a
	// repository's settings files in one cell, and n/a where it has none.
	_, docs, _ := strings.Cut(got, "### GitHub & docs\n\n")
	docs, _, _ = strings.Cut(docs, "\n\n")
	for line := range strings.SplitSeq(docs, "\n") {
		if !strings.HasPrefix(line, "| [") {
			continue
		}
		cells := strings.Split(strings.Trim(line, "|"), "|")
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		gotClaude := cells[len(cells)-2:]
		wantClaude := []string{"n/a", "n/a"}
		if strings.Contains(line, "[mixedCase-flake]") {
			wantClaude = []string{"`.mcp.json`", "`.claude/settings.json, docs/.claude/settings.local.json`"}
		}
		if diff := cmp.Diff(wantClaude, gotClaude); diff != "" {
			t.Errorf("GitHub & docs Claude Code configuration cells mismatch (-want +got):\n%s\n%s", diff, line)
		}
	}
}

func TestBlock_treeLosesMovedColumns(t *testing.T) {
	t.Parallel()

	got, err := render.Block(evaluate(t, fullSnapshot()), clock(renderedAt))
	if err != nil {
		t.Fatalf("Block() error = %v, want nil", err)
	}
	header := sectionTable(t, got, "## Tree")
	for _, moved := range []string{"Renovate", ".justfile"} {
		if slices.Contains(header, moved) {
			t.Errorf("Tree columns = %v, want no %q", header, moved)
		}
	}
	if !slices.Contains(header, "Min. release age") {
		t.Errorf("Tree columns = %v, want Min. release age", header)
	}
}

func TestCell_deviates(t *testing.T) {
	t.Parallel()

	// The fixture's Renovate paths: two repositories share renovate.json and
	// one keeps its file elsewhere, so only that one deviates.
	got, err := render.Block(evaluate(t, fullSnapshot()), clock(renderedAt))
	if err != nil {
		t.Fatalf("Block() error = %v, want nil", err)
	}
	for _, want := range []string{"`.github/renovate.json5` ≠", "`renovate.json` "} {
		if !strings.Contains(got, want) {
			t.Errorf("block has no %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "`renovate.json` ≠") {
		t.Errorf("the usual path is marked as deviating:\n%s", got)
	}
	// A bare mark carries no value for the suffix to qualify.
	for _, bad := range []string{"✗ ≠", "n/a ≠"} {
		if strings.Contains(got, bad) {
			t.Errorf("block has %q, want the suffix only after a value", bad)
		}
	}
}

func TestBlock_tiedPathColumnIsUnmarked(t *testing.T) {
	t.Parallel()

	got, err := render.Block(evaluate(t, fullSnapshot()), clock(renderedAt))
	if err != nil {
		t.Fatalf("Block() error = %v, want nil", err)
	}
	// Two repositories use each commitlint spelling: no standard, no mark.
	if !strings.Contains(got, "`.commitlintrc.json`") || !strings.Contains(got, "`.commitlintrc.yml`") {
		t.Fatalf("fixture lacks both commitlint spellings:\n%s", got)
	}
	if strings.Contains(got, "`.commitlintrc.json` ≠") || strings.Contains(got, "`.commitlintrc.yml` ≠") {
		t.Errorf("a tied column is marked:\n%s", got)
	}
}

// ciTimingBlock renders a report holding only the given CI timing lines, so a
// test reads the section without the fixture around it.
func ciTimingBlock(t *testing.T, lines ...rules.CITimingLine) string {
	t.Helper()
	got, err := render.Block(&rules.Report{Owner: "gh-owner", CITiming: lines}, clock(renderedAt))
	if err != nil {
		t.Fatalf("Block() error = %v, want nil", err)
	}
	return got
}

// TestCIDuration pins the compact duration format through the rendered PR CI
// cell: seconds under a minute, zero-padded seconds under an hour, and minutes
// alone from an hour up.
func TestCIDuration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		d    time.Duration
		want string
	}{
		{45 * time.Second, "45s"},
		{2*time.Minute + 5*time.Second, "2m05s"},
		{9*time.Minute + 40*time.Second, "9m40s"},
		{59*time.Minute + 59*time.Second, "59m59s"},
		{time.Hour, "1h00m"},
		{time.Hour + 5*time.Minute + 30*time.Second, "1h05m"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()
			got := ciTimingBlock(t, rules.CITimingLine{Repo: "alpha", PR: tt.d, HasPR: true})
			if want := "| " + tt.want + " "; !strings.Contains(got, want) {
				t.Errorf("Block() for %v lacks %q:\n%s", tt.d, want, got)
			}
		})
	}
}

func TestCITimingSection_omittedWhenEmpty(t *testing.T) {
	t.Parallel()

	if got := ciTimingBlock(t); strings.Contains(got, "## CI timing") {
		t.Errorf("Block() with no CI timing lines has a CI timing heading:\n%s", got)
	}
}

func TestCITimingSection_naCells(t *testing.T) {
	t.Parallel()

	got := ciTimingBlock(t,
		rules.CITimingLine{Repo: "bot-only", Main: 90 * time.Second, HasMain: true},
		rules.CITimingLine{Repo: "pr-only", PR: 30 * time.Second, HasPR: true, SlowestJob: "ci / check", SlowestMean: 20 * time.Second},
	)
	for l := range strings.SplitSeq(got, "\n") {
		switch {
		case strings.Contains(l, "bot-only"):
			if strings.Count(l, "n/a") != 3 || !strings.Contains(l, "| 1m30s ") {
				t.Errorf("bot-only row = %q, want n/a for PR CI, slowest job and its mean, and 1m30s for Main CI", l)
			}
		case strings.Contains(l, "pr-only"):
			for _, want := range []string{"| 30s ", "| n/a ", "| `ci / check` ", "| 20s "} {
				if !strings.Contains(l, want) {
					t.Errorf("pr-only row = %q, lacks %q", l, want)
				}
			}
		}
	}
}

// nixCell renders snap and returns the cell under the given header of the Nix
// section's row for the named repository. Locating the column by its header
// keeps a column added to the table from shifting the cell under test.
func nixCell(t *testing.T, snap *audit.Snapshot, name, header string) string {
	t.Helper()
	got, err := render.Block(evaluate(t, snap), clock(renderedAt))
	if err != nil {
		t.Fatalf("Block() error = %v, want nil", err)
	}
	start := strings.Index(got, "## Nix\n")
	end := strings.Index(got, "## Policy\n")
	if start < 0 || end < start {
		t.Fatalf("Block() has no Nix section before Policy:\n%s", got)
	}
	section := got[start:end]
	// The section holds several tables; the row's own header is the last one
	// above it.
	var headers, row []string
	for l := range strings.SplitSeq(section, "\n") {
		switch {
		case strings.HasPrefix(l, "| Repository"):
			headers = splitCells(l)
		case strings.HasPrefix(l, "| ["+name+"]"):
			row = splitCells(l)
		}
		if row != nil {
			break
		}
	}
	if headers == nil || row == nil {
		t.Fatalf("Nix section has no header row or no row for %s:\n%s", name, section)
	}
	i := slices.Index(headers, header)
	if i < 0 || i >= len(row) {
		t.Fatalf("Nix section has no %q column (headers %q) in row %q", header, headers, row)
	}
	return row[i]
}

// splitCells splits a Markdown table row into its trimmed cells.
func splitCells(line string) []string {
	cells := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|")
	for i, c := range cells {
		cells[i] = strings.TrimSpace(c)
	}
	return cells
}

// TestMarkedDateCell: the lock-age cell shows the age beside its mark, so a
// stale lock reads "✗ 47d" rather than a bare cross; a type that does not
// judge the check still shows the age it found; a lock with nothing to date
// is n/a; and an unreadable lock says so.
func TestMarkedDateCell(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		typ    rules.Type
		mutate func(*audit.Flake)
		want   string
	}{
		{"fresh", "tools", func(f *audit.Flake) { f.NixpkgsLastModified = renderedAt.Add(-4 * 24 * time.Hour) }, "✓ 4d"},
		{"stale", "tools", func(f *audit.Flake) { f.NixpkgsLastModified = renderedAt.Add(-47 * 24 * time.Hour) }, "✗ 47d"},
		{"not judged", "content", func(f *audit.Flake) { f.NixpkgsLastModified = renderedAt.Add(-47 * 24 * time.Hour) }, "47d"},
		{"no nixpkgs", "tools", func(f *audit.Flake) { f.NixpkgsLastModified = time.Time{} }, "n/a"},
		{"unreadable", "tools", func(f *audit.Flake) { *f = audit.Flake{HasLock: true, LockError: "flake.lock: invalid JSON"} }, "✗ unreadable flake.lock"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := baseRepo("alpha", tc.typ)
			tc.mutate(&r.Flake)
			snap := &audit.Snapshot{GeneratedAt: renderedAt, Owner: "gh-owner", Types: standardTypes(), Identity: fixtureIdentity(), Repos: []audit.Repo{r}}
			if got := nixCell(t, snap, "alpha", "Lock age"); got != tc.want {
				t.Errorf("Lock age cell = %q, want %q", got, tc.want)
			}
		})
	}
}

// claudeIntro is the line under the Claude heading.
const claudeIntro = "Only repositories with Claude Code skills, commands, hooks or a plugin are listed, " +
	"plus the configured plugin marketplace."

// TestBlock_claudeSection: the Claude section sits right after File paths,
// opens with its intro, and its Skills table carries a row only for a
// repository with skills or command files, each cell a count that names the
// files at fault, unreadable, or n/a.
func TestBlock_claudeSection(t *testing.T) {
	t.Parallel()

	got, err := render.Block(evaluate(t, fullSnapshot()), clock(renderedAt))
	if err != nil {
		t.Fatalf("Block() error = %v, want nil", err)
	}

	paths := strings.Index(got, "## File paths\n")
	claude := strings.Index(got, "## Claude\n\n"+claudeIntro+"\n\n### Skills\n")
	nix := strings.Index(got, "## Nix\n")
	if paths < 0 || claude < 0 || nix < 0 || paths >= claude || claude >= nix {
		t.Fatalf("want ## File paths, then ## Claude with its intro and ### Skills, then ## Nix:\n%s", got)
	}

	want := []string{"Repository", "Slash-only", "Name = dir", "Known keys", "Description", "No wrappers", "Longest SKILL.md"}
	if diff := cmp.Diff(want, sectionTable(t, got, "### Skills")); diff != "" {
		t.Errorf("Skills columns mismatch (-want +got):\n%s", diff)
	}

	hooksAt := strings.Index(got, "### Hooks\n")
	if hooksAt < claude || hooksAt > nix {
		t.Fatalf("want ### Hooks inside the Claude section:\n%s", got)
	}
	tableRows := func(section string) map[string][]string {
		out := map[string][]string{}
		for line := range strings.SplitSeq(section, "\n") {
			if !strings.HasPrefix(line, "| [") {
				continue
			}
			cells := splitRow(line)
			name, _, _ := strings.Cut(strings.TrimPrefix(cells[0], "["), "]")
			out[name] = cells[1:]
		}
		return out
	}
	rows := tableRows(got[claude:hooksAt])
	const unreadable = "✗ unreadable .claude/skills/skill-01/SKILL.md"
	wantRows := map[string][]string{
		"mixedCase-flake": {"1/1", "1/1", "1/1", "1/1", "n/a", "45"},
		"github-repos-audit": {
			"✗ 2/3 skill-02", "2/2", "✗ 2/3 skill-02", "✗ 1/2 skill-02", "✗ 0/1 command-01", "612",
		},
		"web-app": {unreadable, unreadable, unreadable, unreadable, "n/a", "unreadable .claude/skills/skill-01/SKILL.md"},
	}
	if diff := cmp.Diff(wantRows, rows); diff != "" {
		t.Errorf("Skills rows mismatch (-want +got):\n%s", diff)
	}

	wantHooks := []string{"Repository", "Events", "Executable", "Plugin root", "Timeout", "Matcher"}
	if diff := cmp.Diff(wantHooks, sectionTable(t, got, "### Hooks")); diff != "" {
		t.Errorf("Hooks columns mismatch (-want +got):\n%s", diff)
	}
	const unreadableHooks = "✗ unreadable .claude/settings.json"
	wantHookRows := map[string][]string{
		"mixedCase-flake": {"1/1", "1/1", "1/1", "1/1", "1/1"},
		// The unexecutable script and the tool hook without a matcher or timeout
		// fail; the SessionStart hook needs no matcher and is not counted there.
		"github-repos-audit": {"2/2", "✗ 1/2 .claude/settings.json PostToolUse", "n/a", "✗ 1/2 .claude/settings.json PostToolUse", "✗ 0/1 .claude/settings.json PostToolUse"},
		// Its unreadable hooks file is a project settings file, which the
		// plugin-root check would never judge.
		"web-app": {unreadableHooks, unreadableHooks, "n/a", unreadableHooks, unreadableHooks},
	}
	pluginAt := strings.Index(got, "### Plugin\n")
	if pluginAt < hooksAt || pluginAt > nix {
		t.Fatalf("want ### Plugin after ### Hooks inside the Claude section:\n%s", got)
	}
	if diff := cmp.Diff(wantHookRows, tableRows(got[hooksAt:pluginAt])); diff != "" {
		t.Errorf("Hooks rows mismatch (-want +got):\n%s", diff)
	}

	wantPlugin := []string{
		"Repository", "Manifest", "No own marketplace", "Releases", "Version = tag",
		"Listed", "Pin valid", "Pin current", "Entries valid", "Complete",
	}
	if diff := cmp.Diff(wantPlugin, sectionTable(t, got, "### Plugin")); diff != "" {
		t.Errorf("Plugin columns mismatch (-want +got):\n%s", diff)
	}
	const unreadablePlugin = "✗ unreadable .claude-plugin/plugin.json"
	wantPluginRows := map[string][]string{
		// Listed, pinned to its latest release.
		"mixedCase-flake": {"✓", "✓", "✓", "1.2.0", "✓", "v1.2.0", "v1.2.0", "n/a", "n/a"},
		// Listed validly, but a release behind one published ten days ago.
		"cipher-lib": {"✓", "✓", "✓", "1.2.0", "✓", "v1.1.0", "✗ v1.1.0 → v1.2.0", "n/a", "n/a"},
		// Missing fields are named; the repository's own marketplace and the
		// version behind the release fail with no value to show. It is the
		// marketplace, private and so not judged on being listed: one entry
		// names a repository outside the audit, and plugin-kit has none.
		"github-repos-audit": {
			"✗ homepage, license", "✗", "✓", "✗ 1.1.0 < v1.2.0", "n/a", "n/a", "n/a", "✗ 2/3 skill-pack", "✗ plugin-kit",
		},
		"plugin-kit": {"✓", "✓", "✓", "1.0.0", "✗", "n/a", "n/a", "n/a", "n/a"},
		// Private, so its unreadable manifest leaves the listing unjudged.
		"web-app": {unreadablePlugin, unreadablePlugin, unreadablePlugin, unreadablePlugin, "n/a", "n/a", "n/a", "n/a", "n/a"},
	}
	if diff := cmp.Diff(wantPluginRows, tableRows(got[pluginAt:nix])); diff != "" {
		t.Errorf("Plugin rows mismatch (-want +got):\n%s", diff)
	}
}

// TestBlock_noClaudeSectionWithoutClaudeTooling: an account where no
// repository carries Claude Code tooling has no Claude heading at all.
func TestBlock_noClaudeSectionWithoutClaudeTooling(t *testing.T) {
	t.Parallel()

	snap := &audit.Snapshot{
		GeneratedAt: renderedAt, Owner: "gh-owner", Types: standardTypes(), Identity: fixtureIdentity(),
		Repos: []audit.Repo{baseRepo("alpha", "tools"), baseRepo("bravo", "tools")},
	}
	got, err := render.Block(evaluate(t, snap), clock(renderedAt))
	if err != nil {
		t.Fatalf("Block() error = %v, want nil", err)
	}
	if strings.Contains(got, "## Claude") {
		t.Errorf("an account with no Claude tooling renders a Claude section:\n%s", got)
	}
}

// TestBlock_marketplaceRowWithoutAPlugin: the marketplace repository has a
// Plugin row for its two marketplace cells even when it is no plugin itself,
// and an unreadable marketplace reads unreadable there and on every public
// plugin.
func TestBlock_marketplaceRowWithoutAPlugin(t *testing.T) {
	t.Parallel()

	kit := baseRepo("plugin-kit", "tools")
	kit.Visibility = "public"
	kit.Claude = audit.Claude{Plugin: audit.ClaudePlugin{Present: true, Name: "plugin-kit", Version: "1.0.0", Fields: pluginFields()}}
	kit.Releases = audit.Releases{Total: 1, LatestTag: "v1.0.0", LatestPublishedAt: at(2026, time.August, 3)}
	snap := &audit.Snapshot{
		GeneratedAt: renderedAt, Owner: "gh-owner", Types: standardTypes(), Identity: fixtureIdentity(),
		Repos:       []audit.Repo{kit, baseRepo("plugin-market", "tools")},
		Marketplace: audit.ClaudeMarketplace{Repo: "plugin-market", Unreadable: "no .claude-plugin/marketplace.json"},
	}
	got, err := render.Block(evaluate(t, snap), clock(renderedAt))
	if err != nil {
		t.Fatalf("Block() error = %v, want nil", err)
	}
	const unreadable = "✗ unreadable plugin-market"
	want := map[string][]string{
		"plugin-kit":    {"✓", "✓", "✓", "1.0.0", unreadable, unreadable, unreadable, "n/a", "n/a"},
		"plugin-market": {"n/a", "n/a", "n/a", "n/a", "n/a", "n/a", "n/a", unreadable, unreadable},
	}
	pluginAt := strings.Index(got, "### Plugin\n")
	if pluginAt < 0 {
		t.Fatalf("block has no Plugin table:\n%s", got)
	}
	rows := map[string][]string{}
	for line := range strings.SplitSeq(got[pluginAt:], "\n") {
		if strings.HasPrefix(line, "## ") {
			break
		}
		if !strings.HasPrefix(line, "| [") {
			continue
		}
		cells := splitRow(line)
		name, _, _ := strings.Cut(strings.TrimPrefix(cells[0], "["), "]")
		rows[name] = cells[1:]
	}
	if diff := cmp.Diff(want, rows); diff != "" {
		t.Errorf("Plugin rows mismatch (-want +got):\n%s", diff)
	}
}

// TestBlock_claudeCellsAreEscaped: a skill's directory is the owner's to
// name and may hold a pipe, which would split the table cell it is listed
// in.
func TestBlock_claudeCellsAreEscaped(t *testing.T) {
	t.Parallel()

	r := baseRepo("alpha", "tools")
	r.Claude = audit.Claude{Skills: []audit.ClaudeSkill{{
		Path: ".claude/skills/a|b/SKILL.md", Dir: "a|b", Name: "a|b",
		Keys: map[string]string{"description": "!!str"}, HasDescription: true, DescriptionChars: 10, Lines: 3,
	}}}
	snap := &audit.Snapshot{
		GeneratedAt: renderedAt, Owner: "gh-owner", Types: standardTypes(), Identity: fixtureIdentity(),
		Repos: []audit.Repo{r},
	}
	got, err := render.Block(evaluate(t, snap), clock(renderedAt))
	if err != nil {
		t.Fatalf("Block() error = %v, want nil", err)
	}
	if want := `✗ 0/1 a\|b`; !strings.Contains(got, want) {
		t.Errorf("block has no %q:\n%s", want, got)
	}
}
