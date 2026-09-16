package render_test

import (
	"flag"
	"os"
	"path/filepath"
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
// no settings exceptions and no overrides renders the tables and nothing else.
func TestBlock_nothingWrong(t *testing.T) {
	t.Parallel()

	a := baseRepo("alpha", "tools")
	b := baseRepo("bravo", "tools")
	snap := &audit.Snapshot{GeneratedAt: renderedAt, Owner: "gh-owner", Types: standardTypes(), Repos: []audit.Repo{a, b}}

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
	golden(t, "clean.md", got)
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
	// The cron commits this output directly, so every row of a table must be
	// the same rendered width as its header. An unpadded table would land on
	// the default branch and fail the repository's own format check daily.
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
			snap := &audit.Snapshot{GeneratedAt: renderedAt, Owner: "gh-owner", Types: standardTypes(), Repos: []audit.Repo{r}}
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
	snap := &audit.Snapshot{GeneratedAt: renderedAt, Owner: "gh-owner", Types: standardTypes(), Repos: []audit.Repo{r}}

	got, err := render.Block(evaluate(t, snap), clock(renderedAt))
	if err != nil {
		t.Fatalf("Block() error = %v, want nil", err)
	}
	// An unescaped pipe would end the cell and shift every column after it.
	if !strings.Contains(got, `before \| after`) {
		t.Errorf("want the pipe escaped in:\n%s", got)
	}
}

// TestBlock_naCellCarryingAScalar is the "an n/a cell still shows a value it
// has" decision, seen from the renderer's side.
func TestBlock_naCellCarryingAScalar(t *testing.T) {
	t.Parallel()

	// Public but unpublished: the topics row is n/a and the repository has six.
	r := baseRepo("cipher-lib", "content")
	r.Visibility = "public"
	r.Topics = 6
	snap := &audit.Snapshot{GeneratedAt: renderedAt, Owner: "gh-owner", Types: standardTypes(), Repos: []audit.Repo{r}}

	got, err := render.Block(evaluate(t, snap), clock(renderedAt))
	if err != nil {
		t.Fatalf("Block() error = %v, want nil", err)
	}
	if !strings.Contains(got, "| 6 ") {
		t.Errorf("want the topic count shown despite the n/a verdict:\n%s", got)
	}
	// flake.nix, by contrast, is present-or-absent, so its n/a cell says n/a
	// rather than showing a bare cross that would look like a gap.
	if !strings.Contains(got, "| n/a ") {
		t.Errorf("want an n/a cell in:\n%s", got)
	}
}

// TestBlock_backticksABareURLInADescription guards the gate against a
// repository description carrying a plain URL: a bare URL in markdown is an
// MD034 failure — on a file no human wrote and nobody can fix by editing it.
func TestBlock_backticksABareURLInADescription(t *testing.T) {
	t.Parallel()

	r := baseRepo("config-files", "environment")
	r.Description = "Config files managed by chezmoi - https://www.chezmoi.io/"
	snap := &audit.Snapshot{GeneratedAt: renderedAt, Owner: "gh-owner", Types: standardTypes(), Repos: []audit.Repo{r}}

	got, err := render.Block(evaluate(t, snap), clock(renderedAt))
	if err != nil {
		t.Fatalf("Block() error = %v, want nil", err)
	}
	if !strings.Contains(got, "`https://www.chezmoi.io/`") {
		t.Errorf("want the bare URL backticked in:\n%s", got)
	}
	// Backticks rather than a link: the gate must not depend on a third-party
	// host being reachable.
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
	snap := &audit.Snapshot{GeneratedAt: renderedAt, Owner: "gh-owner", Types: standardTypes(), Repos: []audit.Repo{r}}

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
	snap := &audit.Snapshot{GeneratedAt: renderedAt, Owner: "gh-owner", Types: standardTypes(), Repos: []audit.Repo{a, b}}

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
