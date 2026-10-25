package github

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
	"github.com/rvenutolo/github-repos-audit/internal/claude"
)

// marketplaceFile and pluginManifest are where Claude Code reads a
// marketplace's catalogue and a plugin's manifest.
const (
	marketplaceFile = ".claude-plugin/marketplace.json"
	pluginManifest  = ".claude-plugin/plugin.json"
)

// maxTagDepth bounds how many annotated tag objects are followed to reach a
// commit. Git allows a tag of a tag, so one hop is not enough, but a chain
// longer than this is no pin anyone wrote on purpose, and a bound means a
// malformed answer cannot keep the run reading forever.
const maxTagDepth = 5

// refTarget is the object a ref or an annotated tag points at.
type refTarget struct {
	Object struct {
		SHA  string `json:"sha"`
		Type string `json:"type"`
	} `json:"object"`
}

// CollectMarketplace reads the marketplace repository's marketplace.json from
// its default branch and, for each entry whose source is a repository in
// audited, what GitHub says about the entry's pin: whether its ref is a
// release, the commit its tag resolves to, and the plugin name the manifest
// at its sha declares.
//
// Nothing short of an unexpected status is an error. A marketplace that is
// not among the audited repositories, or whose file is absent or unparsable,
// is recorded as Unreadable; a 404 from a release, ref, tag or contents
// lookup is an answer recorded on the entry, as are an empty repository and
// a ref no request is made for. The run goes on either way, so one bad
// marketplace costs the report its marketplace checks and nothing else.
func (c *Client) CollectMarketplace(
	ctx context.Context, repo string, audited []audit.Repo,
) (audit.ClaudeMarketplace, error) {
	out := audit.ClaudeMarketplace{Repo: repo}
	if _, ok := auditedRepo(repo, audited); !ok {
		// Archived, a fork or missing: either way the audit reads no
		// repository it does not audit.
		out.Unreadable = "not among the audited repositories"
		return out, nil
	}
	file, err := c.fetchContents(ctx, contentsRequest{Repo: repo, Path: marketplaceFile})
	if err != nil {
		return audit.ClaudeMarketplace{}, err
	}
	switch {
	case file.notFound:
		out.Unreadable = "no " + marketplaceFile
		return out, nil
	case file.problem != "":
		out.Unreadable = marketplaceFile + ": " + file.problem
		return out, nil
	}
	parsed := claude.ParseMarketplace(file.text)
	if parsed.Unreadable != "" {
		out.Unreadable = marketplaceFile + ": " + parsed.Unreadable
		return out, nil
	}
	for _, e := range parsed.Entries {
		entry := audit.MarketplaceEntry{Name: e.Name, Repo: e.Repo, Ref: e.Ref, SHA: e.SHA, HasVersion: e.HasVersion}
		if err := c.lookUpPin(ctx, &entry, audited); err != nil {
			return audit.ClaudeMarketplace{}, err
		}
		out.Entries = append(out.Entries, entry)
	}
	return out, nil
}

// auditedRepo finds name among the audited repositories, whose Name is the
// name as GitHub spells it. GitHub matches repository names
// case-insensitively, so a marketplace entry may write one in another case
// and still name it.
func auditedRepo(name string, audited []audit.Repo) (audit.Repo, bool) {
	for _, r := range audited {
		if strings.EqualFold(r.Name, name) {
			return r, true
		}
	}
	return audit.Repo{}, false
}

// lookUpPin fills the answers about one entry's pin. Only an entry naming a
// repository of this account that the run audited is looked up: any other
// pin points outside what the audit can vouch for, which is the marketplace
// checks' finding to report, and reading it would spend requests on a
// repository nobody asked about.
func (c *Client) lookUpPin(ctx context.Context, e *audit.MarketplaceEntry, audited []audit.Repo) error {
	owner, name, found := strings.Cut(e.Repo, "/")
	repo, ok := auditedRepo(name, audited)
	// GitHub logins are case-insensitive too.
	if !found || !ok || !strings.EqualFold(owner, c.owner) {
		e.Problem = "not an audited repository"
		return nil
	}
	if repo.Empty {
		// A repository with no commits has no tag, release or manifest to
		// pin, and GitHub answers its ref lookup with a 409, which is no
		// documented answer and would abort the run.
		e.Problem = "empty repository"
		return nil
	}
	name = repo.Name
	tag, safe := escapeSegments(e.Ref)
	var problems []string
	switch {
	case e.Ref == "":
		problems = append(problems, "no ref")
	case !safe:
		problems = append(problems, "unsafe ref")
	default:
		release, err := c.isRelease(ctx, name, tag)
		if err != nil {
			return err
		}
		e.ReleaseTag = release
		commit, problem, err := c.tagCommit(ctx, name, tag, e.Ref)
		if err != nil {
			return err
		}
		e.TagCommit = commit
		if problem != "" {
			problems = append(problems, problem)
		}
	}
	if e.SHA == "" {
		problems = append(problems, "no sha")
	} else {
		pluginName, problem, err := c.manifestName(ctx, name, e.SHA)
		if err != nil {
			return err
		}
		e.PluginName = pluginName
		if problem != "" {
			problems = append(problems, problem)
		}
	}
	e.Problem = strings.Join(problems, "; ")
	return nil
}

// isRelease asks whether the tag, already escaped by escapeSegments, names a
// published release of repo. The endpoint answers 404 for a tag with no
// release, which is an answer.
func (c *Client) isRelease(ctx context.Context, repo, escaped string) (bool, error) {
	var ignored struct{}
	resp, err := c.getJSON(ctx, c.repoPath(repo)+"/releases/tags/"+escaped, &ignored, http.StatusNotFound)
	if err != nil {
		return false, err
	}
	return resp.status != http.StatusNotFound, nil
}

// tagCommit resolves tag to the commit it names. A lightweight tag's ref
// points at the commit itself; an annotated tag's points at a tag object,
// which is read in turn. A 404 for the ref is a repository with no such tag,
// which is an answer and leaves the commit empty with no problem: whether
// that matters is the checks' to say. A tag that leads to anything other than
// a commit, or names an object with no sha, has no commit to compare with a
// pin, and returns why.
//
// The tag comes from another repository's file, so the caller hands it in
// escaped by escapeSegments, as isRelease takes it, and as written for the
// problem text.
func (c *Client) tagCommit(ctx context.Context, repo, escaped, tag string) (string, string, error) {
	base := c.repoPath(repo) + "/git/"
	var obj refTarget
	resp, err := c.getJSON(ctx, base+"ref/tags/"+escaped, &obj, http.StatusNotFound)
	if err != nil {
		return "", "", err
	}
	if resp.status == http.StatusNotFound {
		return "", "", nil
	}
	for range maxTagDepth {
		switch {
		case obj.Object.Type == "commit" && obj.Object.SHA != "":
			return obj.Object.SHA, "", nil
		case obj.Object.Type == "tag" && obj.Object.SHA != "":
		default:
			return "", "tag " + tag + " does not resolve to a commit", nil
		}
		next := obj.Object.SHA
		obj = refTarget{}
		// A tag object the ref just named exists, so a 404 here is not an
		// answer GitHub documents, and aborts like any other.
		if _, err := c.getJSON(ctx, base+"tags/"+url.PathEscape(next), &obj); err != nil {
			return "", "", err
		}
	}
	return "", "tag " + tag + " does not resolve to a commit", nil
}

// manifestName reads plugin.json's name at sha. A manifest that is absent,
// not a file, unparsable or nameless leaves the name empty and returns why:
// the entry's pin then names nothing the checks can compare.
func (c *Client) manifestName(ctx context.Context, repo, sha string) (string, string, error) {
	file, err := c.fetchContents(ctx, contentsRequest{Repo: repo, Path: pluginManifest, Ref: sha})
	if err != nil {
		return "", "", err
	}
	switch {
	case file.notFound:
		return "", "no " + pluginManifest + " at " + sha, nil
	case file.problem != "":
		return "", pluginManifest + " at " + sha + ": " + file.problem, nil
	}
	p := claude.ParsePlugin(file.text)
	switch {
	case p.Unreadable != "":
		return "", pluginManifest + " at " + sha + ": " + p.Unreadable, nil
	case p.Name == "":
		return "", pluginManifest + " at " + sha + ": no name", nil
	}
	return p.Name, "", nil
}
