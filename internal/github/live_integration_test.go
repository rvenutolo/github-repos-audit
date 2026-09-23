//go:build integration

package github

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
)

// The live suite. Real requests against real GitHub, read-only, with the same
// token the daily refresh uses.
//
// It exists because recorded fixtures pass forever while the API drifts
// underneath them. The entire collection is one GraphQL query (plus one per
// further hundred commits of history) and eight REST calls, so a single
// renamed field or changed enum breaks every run — and nothing offline can see
// it coming.
//
// A failure here is informational, not a broken build: it means GitHub changed
// something and the collector needs attention before the next cron run does it
// loudly. That is why it is behind a build tag and never in the gate.

// liveTargets is what the live suite reads. The names come from the
// environment, because this code is meant to be public and must not name any
// real account's repositories:
//
//   - SMOKE_OWNER is the account the token belongs to.
//   - SMOKE_PUBLIC_REPO is public, has CI and cuts releases.
//   - SMOKE_PRIVATE_REPO is private, which is what makes the n/a paths
//     observable: security_and_analysis and private-vulnerability-reporting
//     both behave differently there.
type liveTargets struct {
	owner, publicRepo, privateRepo string
}

// targets reads liveTargets, or skips when any is unset. The reusable smoke
// workflow makes all three required inputs, so CI cannot skip silently.
func targets(t *testing.T) liveTargets {
	t.Helper()

	var missing []string
	get := func(key string) string {
		v := os.Getenv(key)
		if v == "" {
			missing = append(missing, key)
		}
		return v
	}
	lt := liveTargets{
		owner:       get("SMOKE_OWNER"),
		publicRepo:  get("SMOKE_PUBLIC_REPO"),
		privateRepo: get("SMOKE_PRIVATE_REPO"),
	}
	if len(missing) > 0 {
		t.Skipf("%s unset; export the smoke targets, then run `just smoke`", strings.Join(missing, ", "))
	}
	return lt
}

// liveClient builds a client against the real API for the configured owner,
// or skips when no token or no targets are available.
func liveClient(t *testing.T) (*Client, liveTargets) {
	t.Helper()

	lt := targets(t)
	token, err := ResolveToken(t.Context(), os.Getenv)
	if err != nil {
		t.Skipf("no token available (%v); run `just smoke`", err)
	}
	logger := slog.New(slog.DiscardHandler)
	owner, err := Viewer(t.Context(), Options{Token: token, Logger: logger})
	if err != nil {
		t.Fatalf("Viewer() error = %v, want nil", err)
	}
	if owner != lt.owner {
		t.Fatal("the token authenticates as a different account than SMOKE_OWNER: " +
			"the variable or the token is misconfigured, which is not API drift")
	}
	c, err := New(Options{Owner: owner, Token: token, Logger: logger})
	if err != nil {
		t.Fatalf("New() error = %v, want nil", err)
	}
	// Every client built here leaves HTTPClient unset, so newClient falls back
	// to &http.Client{Timeout: DefaultTimeout} with a nil Transport, which
	// means http.DefaultTransport: one process-wide connection pool shared by
	// every client this file builds, Viewer's included. An idle connection to
	// api.github.com keeps a read and a write goroutine alive in that pool,
	// and they outlive the test that opened them. The package's TestMain runs
	// goleak over the whole binary, so whatever is still parked when the last
	// test returns is reported as a leak. Closing idle connections through
	// c.http reaches the shared DefaultTransport and so closes every client's
	// pooled connections, not just this one — harmless, since GitHub is asked
	// for a new connection on the next request, and it keeps goleak's signal
	// about our own goroutines rather than about the connection pool.
	t.Cleanup(c.http.CloseIdleConnections)
	return c, lt
}

// TestLive_collectParsesEveryField is the broad one: if any GraphQL field were
// renamed or any REST shape changed, this is where it surfaces.
func TestLive_collectParsesEveryField(t *testing.T) {
	t.Parallel()

	c, lt := liveClient(t)
	repos, err := c.Collect(t.Context(), []string{lt.publicRepo, lt.privateRepo})
	if err != nil {
		t.Fatalf("Collect() error = %v, want nil", err)
	}
	if len(repos) != 2 {
		t.Fatalf("Collect() returned %d repositories, want 2", len(repos))
	}

	byName := map[string]int{}
	for i, r := range repos {
		byName[r.Name] = i
	}

	pub := repos[byName[lt.publicRepo]]
	if pub.Visibility != "public" {
		t.Errorf("%s visibility = %q, want public", pub.Name, pub.Visibility)
	}
	if pub.Description == "" {
		t.Errorf("%s has no description; the field may have been renamed", pub.Name)
	}
	if pub.PushedAt.IsZero() {
		t.Errorf("%s has no pushed_at", pub.Name)
	}
	if len(pub.HeadOID) != 40 {
		t.Errorf("%s head oid = %q, want forty characters", pub.Name, pub.HeadOID)
	}
	if pub.DefaultBranch == "" {
		t.Errorf("%s has no default branch", pub.Name)
	}
	if !pub.Files.README {
		t.Errorf("%s reads as having no README; the file probes may have changed", pub.Name)
	}
	if len(pub.Files.Workflows) == 0 {
		t.Errorf("%s reads as having no workflows; the tree probe may have changed", pub.Name)
	}
	if len(pub.Rulesets) == 0 {
		t.Errorf("%s reads as having no rulesets; Administration: Read may be missing", pub.Name)
	}
	if !pub.Branch.Known {
		t.Errorf("%s branch rules were not read", pub.Name)
	}
	// A read-only token gets these over GraphQL; REST silently drops them.
	if !pub.Settings.MergeCommitAllowed && !pub.Settings.SquashMergeAllowed && !pub.Settings.RebaseMergeAllowed {
		t.Errorf("%s allows no merge strategy at all, which means the fields were dropped", pub.Name)
	}
	if pub.Settings.ActionsPolicy == "" {
		t.Errorf("%s has no actions policy", pub.Name)
	}
	if pub.Settings.DefaultWorkflowPermissions == "" {
		t.Errorf("%s has no default workflow permissions", pub.Name)
	}
	// Shape only, never values: the identities are real people's names and
	// addresses, and this suite's output is a CI log. A repository with
	// commits has at least one identity, and at least one of them carries
	// both halves; none at all means the history selection was renamed.
	if len(pub.Identities) == 0 {
		t.Errorf("%s has no identities; the history selection may have changed", pub.Name)
	}
	if !slices.ContainsFunc(pub.Identities, func(id audit.Identity) bool { return id.Name != "" && id.Email != "" }) {
		t.Errorf("%s has no identity with both a name and an email; the actor fields may have changed", pub.Name)
	}

	priv := repos[byName[lt.privateRepo]]
	if priv.Visibility != "private" {
		t.Errorf("%s visibility = %q, want private", priv.Name, priv.Visibility)
	}
}

// TestLive_normalNotErrors asserts every row of the design's
// normal-not-errors table still behaves as documented. Each of these was a
// hard-won lesson; a change to any of them turns an ordinary answer into an
// aborted run.
func TestLive_normalNotErrors(t *testing.T) {
	t.Parallel()

	c, lt := liveClient(t)
	repos, err := c.Collect(t.Context(), []string{lt.publicRepo, lt.privateRepo})
	if err != nil {
		t.Fatalf("Collect() error = %v, want nil", err)
	}
	byName := map[string]int{}
	for i, r := range repos {
		byName[r.Name] = i
	}
	pub, priv := repos[byName[lt.publicRepo]], repos[byName[lt.privateRepo]]

	// A private repository on a personal plan reports no secret scanning at
	// all — GitHub omits or nulls the block rather than saying "disabled".
	if priv.Settings.SecretScanning != "" {
		t.Errorf("%s secret scanning = %q, want empty; the private-repo n/a may be gone",
			priv.Name, priv.Settings.SecretScanning)
	}
	if pub.Settings.SecretScanning == "" {
		t.Errorf("%s has no secret-scanning status; the public path may have changed", pub.Name)
	}

	// /private-vulnerability-reporting answers 404 on a private repository.
	if priv.Settings.PrivateVulnerabilityReporting != nil {
		t.Errorf("%s reports private vulnerability reporting = %v, want nil on a private repository",
			priv.Name, *priv.Settings.PrivateVulnerabilityReporting)
	}
	if pub.Settings.PrivateVulnerabilityReporting == nil {
		t.Errorf("%s reports no private vulnerability reporting; the public path may have changed", pub.Name)
	}

	// /actions/permissions/access answers 422 on a public repository: the
	// setting does not exist there.
	if pub.Settings.ActionsAccessLevel != "" {
		t.Errorf("%s actions access level = %q, want empty on a public repository",
			pub.Name, pub.Settings.ActionsAccessLevel)
	}
	if priv.Settings.ActionsAccessLevel == "" {
		t.Errorf("%s has no actions access level; the private path may have changed", priv.Name)
	}
}

// runsResponse is the shape of actions/runs, used only by the cross-check
// below.
type runsResponse struct {
	TotalCount int `json:"total_count"`
	Runs       []struct {
		Name       string `json:"name"`
		Conclusion string `json:"conclusion"`
	} `json:"workflow_runs"`
}

// TestLive_rollupAgreesWithRunAggregation is the load-bearing assertion of the
// whole suite.
//
// statusCheckRollup is read because it costs no extra request and natively
// covers third-party commit statuses, which run-aggregation would miss. Its
// one risk is the failure mode this design keeps running into: GitHub omits
// what a token cannot see rather than saying so, and a rollup missing an
// invisible check run would report green on a red branch.
//
// This is the guard that makes the cheap method safe. A disagreement means the
// rollup can no longer be trusted and the collector should switch to
// aggregating runs.
func TestLive_rollupAgreesWithRunAggregation(t *testing.T) {
	t.Parallel()

	c, lt := liveClient(t)
	repos, err := c.Collect(t.Context(), []string{lt.publicRepo})
	if err != nil {
		t.Fatalf("Collect() error = %v, want nil", err)
	}
	repo := repos[0]
	if repo.HeadOID == "" {
		t.Skip("the repository has no commits")
	}

	var runs runsResponse
	path := "/repos/" + lt.owner + "/" + lt.publicRepo +
		"/actions/runs?head_sha=" + repo.HeadOID + "&status=completed&per_page=100"
	if _, err := c.getJSON(t.Context(), path, &runs); err != nil {
		t.Fatalf("actions/runs error = %v, want nil", err)
	}
	if runs.TotalCount == 0 {
		t.Skipf("no completed runs on %s yet; nothing to cross-check", repo.HeadOID[:8])
	}

	aggregated := "SUCCESS"
	for _, r := range runs.Runs {
		switch r.Conclusion {
		case "success", "skipped", "neutral":
		case "":
			// Still reporting; the rollup would say PENDING.
			aggregated = "PENDING"
		default:
			aggregated = "FAILURE"
		}
		if aggregated == "FAILURE" {
			break
		}
	}

	// The rollup may legitimately be PENDING while every *completed* run has
	// succeeded, because a run still in flight is not in this query's answer.
	if repo.CIState == "PENDING" && aggregated == "SUCCESS" {
		t.Skipf("a run is still in flight on %s; the two cannot be compared", repo.HeadOID[:8])
	}
	if repo.CIState != aggregated {
		t.Errorf("statusCheckRollup = %q but aggregating %d completed runs gives %q on %s.\n"+
			"The rollup may be omitting a check run this token cannot see. "+
			"Switch the collector to run-aggregation before trusting the CI cell again.",
			repo.CIState, runs.TotalCount, aggregated, repo.HeadOID)
	}
}

// TestLive_headShaNeedsTheFullOID is why the collector keeps all forty
// characters. An abbreviated SHA does not error: it answers 200 with
// total_count 0, so a truncated oid would silently report a commit with no
// runs and the cross-check above would pass vacuously forever.
func TestLive_headShaNeedsTheFullOID(t *testing.T) {
	t.Parallel()

	c, lt := liveClient(t)
	repos, err := c.Collect(t.Context(), []string{lt.publicRepo})
	if err != nil {
		t.Fatalf("Collect() error = %v, want nil", err)
	}
	full := repos[0].HeadOID
	if len(full) != 40 {
		t.Fatalf("head oid = %q, want forty characters", full)
	}

	base := "/repos/" + lt.owner + "/" + lt.publicRepo + "/actions/runs?per_page=1&head_sha="

	var whole runsResponse
	if _, err := c.getJSON(t.Context(), base+full, &whole); err != nil {
		t.Fatalf("actions/runs with the full oid error = %v, want nil", err)
	}

	var short runsResponse
	resp, err := c.getJSON(t.Context(), base+full[:8], &short)
	if err != nil {
		t.Fatalf("actions/runs with an abbreviated oid error = %v, want a 200 with no runs", err)
	}
	if resp.status != http.StatusOK {
		t.Errorf("abbreviated oid gave HTTP %d, want 200 — the silent-zero behaviour may have changed", resp.status)
	}
	if short.TotalCount != 0 {
		t.Errorf("abbreviated oid matched %d runs, want 0; head_sha may now accept a prefix",
			short.TotalCount)
	}
}

// TestLive_rollupNeedsNoChecksPermission records something that is not obvious
// and that an earlier draft of the design got wrong: GitHub offers no Checks
// permission on a personal fine-grained PAT, and statusCheckRollup is readable
// without one. If that ever stops being true, the CI cell goes quietly wrong
// rather than loudly.
func TestLive_rollupNeedsNoChecksPermission(t *testing.T) {
	t.Parallel()

	c, lt := liveClient(t)
	repos, err := c.Collect(t.Context(), []string{lt.publicRepo})
	if err != nil {
		t.Fatalf("Collect() error = %v, want nil", err)
	}
	if repos[0].CIState == "" {
		t.Errorf("%s has a null rollup on %s; the token may have lost visibility of its check runs",
			repos[0].Name, repos[0].HeadOID)
	}
}

// TestLive_discoverSeesTheWholeAccount checks the token still has repository
// access "All repositories" rather than a selected list — a selected list
// would make a repository created after the token invisible to exactly the
// coverage check that exists to catch it.
func TestLive_discoverSeesTheWholeAccount(t *testing.T) {
	t.Parallel()

	c, lt := liveClient(t)
	names, err := c.Discover(t.Context())
	if err != nil {
		t.Fatalf("Discover() error = %v, want nil", err)
	}
	if len(names) < 2 {
		t.Fatalf("Discover() found %d repositories, want the whole account", len(names))
	}
	for _, want := range []string{lt.publicRepo, lt.privateRepo} {
		if !slices.Contains(names, want) {
			t.Errorf("Discover() did not return %s; the token may be scoped to a selected list", want)
		}
	}
}

// TestLive_collectIsPromptlyCancellable guards the daily run against a hung
// GitHub: the whole fan-out has to stop when the context does.
func TestLive_collectIsPromptlyCancellable(t *testing.T) {
	t.Parallel()

	c, lt := liveClient(t)
	ctx, cancel := context.WithTimeout(t.Context(), time.Nanosecond)
	defer cancel()

	_, err := c.Collect(ctx, []string{lt.publicRepo, lt.privateRepo})
	if err == nil {
		t.Fatal("Collect() error = nil, want a cancellation")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Errorf("Collect() error = %v, want a context error", err)
	}
}
