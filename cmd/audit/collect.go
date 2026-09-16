package main

import (
	"context"
	"fmt"
	"log/slog"
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
	// except the ones repos.toml declares.
	Collect(ctx context.Context, names []string) ([]audit.Repo, error)
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

// collect reads the configuration, then GitHub, and returns a snapshot with
// the declared fields filled in.
//
// The order matters. The offline validation runs first, so a typo in
// repos.toml costs no API call. Coverage is checked next, before anything else
// is fetched, so a repository created since the last run aborts the run rather
// than being quietly skipped. Only then is the fan-out worth starting.
func collect(
	ctx context.Context,
	cfg *config.Config,
	client collector,
	now func() time.Time,
) (*audit.Snapshot, error) {
	discovered, err := client.Discover(ctx)
	if err != nil {
		return nil, err
	}
	if err := rules.CheckCoverage(discovered, cfg.Names()); err != nil {
		return nil, err
	}

	repos, err := client.Collect(ctx, discovered)
	if err != nil {
		return nil, err
	}
	for i := range repos {
		declared, ok := cfg.Repos[repos[i].Name]
		if !ok {
			// CheckCoverage has already proved this cannot happen; saying so
			// beats a silently untyped repository if it ever does.
			return nil, fmt.Errorf("collect: %s has no repos.toml entry", repos[i].Name)
		}
		repos[i].Type = string(declared.Type)
		repos[i].Published = declared.Published
		repos[i].Overrides = declared.Overrides
	}

	snap := &audit.Snapshot{GeneratedAt: now().UTC(), Owner: client.Owner(), Types: cfg.Types, Repos: repos}
	// The half of the validation that needs live data: published on a private
	// repository, and an override dead against a visibility-dependent row.
	if err := rules.Validate(snap); err != nil {
		return nil, err
	}
	return snap, nil
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
	return collect(ctx, cfg, client, now)
}
