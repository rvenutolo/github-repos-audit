package main

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
	"github.com/rvenutolo/github-repos-audit/internal/config"
	"github.com/rvenutolo/github-repos-audit/internal/github"
	"github.com/rvenutolo/github-repos-audit/internal/rules"
)

// collector is the part of the GitHub client this program uses. It is defined
// here, at the consumer, so the render and json paths can be driven by a
// hand-rolled fake instead of the network.
type collector interface {
	// Discover lists every owned repository that is neither a fork nor
	// archived.
	Discover(ctx context.Context) ([]string, error)
	// Collect reads each named repository. It fills every field of audit.Repo
	// except the ones repos.toml declares; probe says what to look for in
	// file text.
	Collect(ctx context.Context, names []string, probe github.Probe) ([]audit.Repo, error)
	// Owner is the account the repositories belong to.
	Owner() string
}

// newCollector builds a collector. It is a parameter rather than a call so a
// test can supply a fake without a token or a network.
type newCollector func(ctx context.Context, getenv func(string) string, logger *slog.Logger) (collector, error)

// liveCollector resolves the token and builds the real client against GitHub.
func liveCollector(ctx context.Context, getenv func(string) string, logger *slog.Logger) (collector, error) {
	return collectorAt(ctx, "", getenv, logger)
}

// collectorAt is liveCollector with the API root as a parameter: empty means
// GitHub itself. The seam exists so a test can prove this wiring — the token,
// the owner lookup and the client it builds — against a fake server, because
// the wiring is what breaks when the owner stops being a constant.
func collectorAt(
	ctx context.Context, baseURL string, getenv func(string) string, logger *slog.Logger,
) (collector, error) {
	token, err := github.ResolveToken(ctx, getenv)
	if err != nil {
		return nil, err
	}
	logger.DebugContext(ctx, "resolving the token's account")
	owner, err := github.Viewer(ctx, github.Options{Token: token, BaseURL: baseURL, Logger: logger})
	if err != nil {
		return nil, err
	}
	return github.New(github.Options{Owner: owner, Token: token, BaseURL: baseURL, Logger: logger})
}

// collect discovers the account, compares it with repos.toml, reads every
// repository both name and runs the live validation, returning a snapshot
// with the declared fields filled in. The offline validation is prepare's,
// and has already passed.
//
// A disagreement between the account and repos.toml does not stop the run.
// Creating or archiving a repository is routine, and the report is where the
// owner looks: so the repositories both name are audited, and the rest are
// recorded on the snapshot for the report to say. Only a config with nothing
// in common with the account is an error, because then there is no report.
//
// The order matters. The comparison comes before anything else is fetched, so
// a repository with no entry costs no API call and a config pointed at the
// wrong account costs none either.
func collect(
	ctx context.Context,
	cfg *config.Config,
	client collector,
	logger *slog.Logger,
	now func() time.Time,
) (*audit.Snapshot, error) {
	discovered, err := client.Discover(ctx)
	if err != nil {
		return nil, err
	}
	drift := rules.Coverage(discovered, cfg.Names())

	configured := make([]string, 0, len(discovered))
	for _, name := range discovered {
		if _, ok := cfg.Repos[name]; ok {
			configured = append(configured, name)
		}
	}
	if len(configured) == 0 {
		// The counts tell the two causes apart: an account of 0 is a token that
		// sees nothing, a larger one is a config pointed at the wrong account.
		// Names stay out of the message: it reaches logs.
		return nil, fmt.Errorf("%w: repos.toml names no repository the account has (the account has %d, repos.toml declares %d)",
			rules.ErrConfig, len(discovered), len(cfg.Repos))
	}
	warnDrift(ctx, logger, drift)

	repos, err := client.Collect(ctx, configured, github.Probe{Lint: cfg.Hermetic.Lint})
	if err != nil {
		return nil, err
	}
	for i := range repos {
		declared, ok := cfg.Repos[repos[i].Name]
		if !ok { // coverage-exempt: unreachable while Collect answers only for the names it is given
			// Only configured names were asked for, so this cannot happen;
			// saying so beats a silently untyped repository if it ever does.
			return nil, fmt.Errorf("collect: %s has no repos.toml entry", repos[i].Name)
		}
		repos[i].Type = string(declared.Type)
		repos[i].Published = declared.Published
		repos[i].Overrides = declared.Overrides
	}

	snap := &audit.Snapshot{
		GeneratedAt: now().UTC(),
		Owner:       client.Owner(),
		Types:       cfg.Types,
		Identity:    cfg.Identity,
		Hermetic:    cfg.Hermetic,
		Repos:       repos,
		Drift:       drift,
	}
	// The half of the validation that needs live data: published on a private
	// repository, and an override dead against a visibility-dependent row.
	if err := rules.Validate(snap); err != nil {
		return nil, err
	}
	return snap, nil
}

// warnDrift tells whoever reads the run's log what the report says at its
// top. It is a warning and not an error because the run succeeds; it is not
// left to the report alone because a log is what a failed-looking refresh is
// first read through.
func warnDrift(ctx context.Context, logger *slog.Logger, drift audit.ConfigDrift) {
	if len(drift.Unconfigured) > 0 {
		logger.WarnContext(ctx,
			"repositories with no repos.toml entry are not audited; add a [repos.<name>] table for each",
			"repositories", strings.Join(drift.Unconfigured, ", "))
	}
	if len(drift.StaleEntries) > 0 {
		logger.WarnContext(ctx,
			"repos.toml entries name no repository the account has; remove each, or check whether it was archived, deleted or turned into a fork",
			"entries", strings.Join(drift.StaleEntries, ", "))
	}
}

// prepare does the work render and json share: load the configuration, run the
// offline validation, and read GitHub.
func prepare(
	ctx context.Context,
	path string,
	getenv func(string) string,
	logger *slog.Logger,
	newClient newCollector,
	now func() time.Time,
) (*audit.Snapshot, error) {
	cfg, err := config.Load(path)
	if err != nil {
		return nil, err
	}
	// A typo in repos.toml should cost no API call.
	if err := rules.ValidateOffline(cfg.Types, cfg.Declarations()); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	client, err := newClient(ctx, getenv, logger)
	if err != nil {
		return nil, err
	}
	return collect(ctx, cfg, client, logger, now)
}
