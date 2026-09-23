package github

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"golang.org/x/sync/errgroup"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
)

// Collect reads every named repository and returns the facts, sorted by name
// case-insensitively so audit.json diffs are stable. It fills every field of
// audit.Repo except Type and Published, which are declared in repos.toml and
// are the caller's to set.
//
// The fan-out is an errgroup with a limit rather than a hand-rolled pool: it
// bounds concurrency, propagates the first error and cancels its siblings,
// which is exactly the abort-the-whole-run behaviour the report needs. Any
// failure returns no repositories at all, so a partial reading can never reach
// the renderer and invent gaps out of fields GitHub declined to answer.
//
// The preset cache is made here, per run, and handed down rather than kept on
// the Client: a Client stays safe for concurrent use, and a second run reads
// every preset afresh instead of trusting an answer from an earlier one.
func (c *Client) Collect(ctx context.Context, names []string) ([]audit.Repo, error) {
	repos := make([]audit.Repo, len(names))
	presets := newPresetCache(c)

	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(fetchLimit)
	for i, name := range names {
		g.Go(func() error {
			repo, err := c.collectOne(ctx, name, presets)
			if err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
			repos[i] = repo
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}

	slices.SortFunc(repos, func(a, b audit.Repo) int { return compareFold(a.Name, b.Name) })
	c.log.InfoContext(ctx, "collected repositories", "count", len(repos))
	return repos, nil
}

// collectOne reads one repository: the single GraphQL query first, because the
// REST path needs the live default branch name from it, then the REST calls.
// A repository with a Renovate config also costs a contents read for each
// in-account preset it extends that no other repository in this run has
// already read. The first hundred commits of the default branch's history
// arrive with the GraphQL query; a longer history costs one more request per
// further hundred commits, so a large repository is the slow one to collect.
func (c *Client) collectOne(ctx context.Context, name string, presets *presetCache) (audit.Repo, error) {
	gql, err := c.fetchRepository(ctx, name)
	if err != nil {
		return audit.Repo{}, err
	}
	repo := gql.toAudit(name)
	ids, err := c.identities(ctx, name, gql)
	if err != nil {
		return audit.Repo{}, err
	}
	repo.Identities = ids
	if path, blob := gql.renovateConfig(); blob != nil {
		rn, err := c.renovateFacts(ctx, path, blob, presets)
		if err != nil {
			return audit.Repo{}, err
		}
		repo.Renovate = rn
	}
	if err := c.fetchREST(ctx, name, &repo); err != nil {
		return audit.Repo{}, err
	}
	return repo, nil
}

// toAudit turns one GraphQL answer into the collected facts. Nothing here
// derives a verdict: an absent file becomes false, a null rollup becomes an
// empty state, and what any of that means is internal/rules' decision.
func (r *repository) toAudit(name string) audit.Repo {
	repo := audit.Repo{
		Name:             name,
		Visibility:       strings.ToLower(r.Visibility),
		Description:      r.Description,
		Homepage:         r.HomepageURL,
		Topics:           r.Topics.TotalCount,
		OpenPullRequests: r.OpenPullRequests.TotalCount,
		Branches:         r.Branches.TotalCount,
		Files:            r.files(),
		Releases:         r.releases(),
		Rulesets:         r.rulesets(),
		Settings: audit.Settings{
			HasIssues:           r.HasIssues,
			HasWiki:             r.HasWiki,
			HasProjects:         r.HasProjects,
			HasDiscussions:      r.HasDiscussions,
			VulnerabilityAlerts: r.VulnerabilityAlerts,
			MergeCommitAllowed:  r.MergeCommitAllowed,
			SquashMergeAllowed:  r.SquashMergeAllowed,
			RebaseMergeAllowed:  r.RebaseMergeAllowed,
			MergeCommitTitle:    r.MergeCommitTitle,
			MergeCommitMessage:  r.MergeCommitMessage,
			AutoMergeAllowed:    r.AutoMergeAllowed,
			DeleteBranchOnMerge: r.DeleteBranchOnMerge,
		},
	}
	if r.LicenseInfo != nil {
		repo.License = r.LicenseInfo.SPDXID
	}
	if r.PushedAt != nil {
		repo.PushedAt = r.PushedAt.UTC()
	}

	// defaultBranchRef is null for a repository with no commits at all. That is
	// an ordinary answer: the branch-derived cells are unknown and everything
	// else — description, topics, settings, rulesets — still applies.
	if r.DefaultBranchRef == nil {
		repo.Empty = true
		return repo
	}
	repo.DefaultBranch = r.DefaultBranchRef.Name
	if target := r.DefaultBranchRef.Target; target != nil {
		// The oid is kept whole. Abbreviating it would silently break the smoke
		// suite's cross-check, which queries actions/runs?head_sha= and answers
		// total_count: 0 for a short SHA, with a 200.
		repo.HeadOID = target.OID
		if target.StatusCheckRollup != nil {
			repo.CIState = target.StatusCheckRollup.State
		}
	}
	return repo
}

// files records which probed paths exist at HEAD.
func (r *repository) files() audit.Files {
	renovatePath, _ := r.renovateConfig()
	files := audit.Files{
		README: present(r.ReadmeMD) || present(r.ReadmeGitHubMD) || present(r.ReadmeDocsMD) ||
			present(r.ReadmeRST) || present(r.ReadmeTXT) || present(r.ReadmePlain),
		Gitignore:    present(r.Gitignore),
		Editorconfig: present(r.Editorconfig),
		FlakeNix:     present(r.FlakeNix),
		// Either spelling counts; mixedCase-flake uses the undotted one.
		Justfile:  present(r.JustfileDot) || present(r.JustfilePlain),
		Changelog: present(r.ChangelogMD),
		// Each community health file is probed at all three locations GitHub
		// itself recognises: the root, .github/ and docs/.
		Security:       present(r.SecurityRoot) || present(r.SecurityGitHub) || present(r.SecurityDocs),
		Contributing:   present(r.ContributingRoot) || present(r.ContributingGitHub) || present(r.ContributingDocs),
		CodeOfConduct:  present(r.CoCRoot) || present(r.CoCGitHub) || present(r.CoCDocs),
		RenovateConfig: renovatePath,
	}
	if r.Workflows != nil {
		names := make([]string, 0, len(r.Workflows.Entries))
		for _, e := range r.Workflows.Entries {
			names = append(names, e.Name)
		}
		files.Workflows = sortedUnique(names)
	}
	return files
}

// renovateConfig returns the first of the seven accepted paths that exists, so
// the report can say where the configuration lives rather than only that it
// does, together with that file's blob, which is the configuration Renovate
// reads. The order is the one Renovate itself resolves in.
func (r *repository) renovateConfig() (string, *renovateBlob) {
	candidates := []struct {
		path string
		blob *renovateBlob
	}{
		{"renovate.json", r.RenovateJSON},
		{"renovate.json5", r.RenovateJSON5},
		{".renovaterc", r.RenovateRC},
		{".renovaterc.json", r.RenovateRCJSON},
		{".renovaterc.json5", r.RenovateRCJSON5},
		{".github/renovate.json", r.RenovateGitHubJSON},
		{".github/renovate.json5", r.RenovateGitHubJSON5},
	}
	for _, c := range candidates {
		if c.blob != nil {
			return c.path, c.blob
		}
	}
	return "", nil
}

// releases summarises the five most recent releases plus the total. Five are
// taken rather than one so that a run of drafts cannot hide the most recent
// published release behind them.
func (r *repository) releases() audit.Releases {
	out := audit.Releases{Total: r.Releases.TotalCount}
	for _, node := range r.Releases.Nodes {
		if node.IsDraft {
			out.Drafts++
			continue
		}
		if node.PublishedAt == nil {
			continue
		}
		if published := node.PublishedAt.UTC(); published.After(out.LastPublishedAt) {
			out.LastPublishedAt = published
		}
	}
	return out
}

// rulesets records every ruleset the repository defines, with the conditions
// and rule types needed to tell a tag ruleset from a branch one and an active
// ruleset from an evaluated one.
func (r *repository) rulesets() []audit.Ruleset {
	if len(r.Rulesets.Nodes) == 0 {
		return nil
	}
	out := make([]audit.Ruleset, 0, len(r.Rulesets.Nodes))
	for _, node := range r.Rulesets.Nodes {
		set := audit.Ruleset{
			Name:        node.Name,
			Target:      node.Target,
			Enforcement: node.Enforcement,
		}
		if node.Conditions != nil && node.Conditions.RefName != nil {
			set.Include = sortedUnique(node.Conditions.RefName.Include)
		}
		types := make([]string, 0, len(node.Rules.Nodes))
		for _, rule := range node.Rules.Nodes {
			types = append(types, rule.Type)
		}
		set.Rules = sortedUnique(types)
		out = append(out, set)
	}
	slices.SortFunc(out, func(a, b audit.Ruleset) int { return compareFold(a.Name, b.Name) })
	return out
}

// present reports whether a file probe found anything. object answers null for
// a path that does not exist, which is an ordinary answer rather than an error.
func present(o *gitObject) bool { return o != nil }
