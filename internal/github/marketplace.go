package github

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
	"github.com/rvenutolo/github-repos-audit/internal/claude"
	"github.com/rvenutolo/github-repos-audit/internal/renovate"
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
// Nothing short of an unexpected status is an error. A marketplace the
// account does not have, or whose file is absent or unparsable, is recorded
// as Unreadable; a 404 from a release, ref, tag or contents lookup is an
// answer recorded on the entry. The run goes on either way, so one bad
// marketplace costs the report its marketplace checks and nothing else.
func (c *Client) CollectMarketplace(
	ctx context.Context, repo string, audited []audit.Repo,
) (audit.ClaudeMarketplace, error) {
	out := audit.ClaudeMarketplace{Repo: repo}
	if _, ok := auditedName(repo, audited); !ok {
		// A stale entry: the comparison with the account already said so,
		// and there is no repository to read.
		out.Unreadable = "no such repository in the account"
		return out, nil
	}
	file, err := c.fetchContents(ctx, renovate.Preset{Repo: repo, Path: marketplaceFile})
	if err != nil {
		return audit.ClaudeMarketplace{}, err
	}
	switch file.problem {
	case "":
	case renovate.ProblemNotFound:
		out.Unreadable = "no " + marketplaceFile
		return out, nil
	default:
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

// auditedName finds name among the audited repositories and returns the name
// as GitHub spells it. GitHub matches repository names case-insensitively, so
// a marketplace entry may write one in another case and still name it.
func auditedName(name string, audited []audit.Repo) (string, bool) {
	for _, r := range audited {
		if strings.EqualFold(r.Name, name) {
			return r.Name, true
		}
	}
	return "", false
}

// lookUpPin fills the answers about one entry's pin. Only an entry naming a
// repository of this account that the run audited is looked up: any other
// pin points outside what the audit can vouch for, which is the marketplace
// checks' finding to report, and reading it would spend requests on a
// repository nobody asked about.
func (c *Client) lookUpPin(ctx context.Context, e *audit.MarketplaceEntry, audited []audit.Repo) error {
	owner, name, found := strings.Cut(e.Repo, "/")
	name, ok := auditedName(name, audited)
	// GitHub logins are case-insensitive too.
	if !found || !ok || !strings.EqualFold(owner, c.owner) {
		e.Problem = "not an audited repository"
		return nil
	}
	var problems []string
	if e.Ref == "" {
		problems = append(problems, "no ref")
	} else {
		release, err := c.isRelease(ctx, name, e.Ref)
		if err != nil {
			return err
		}
		e.ReleaseTag = release
		commit, problem, err := c.tagCommit(ctx, name, e.Ref)
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

// isRelease asks whether tag names a published release of repo. The endpoint
// answers 404 for a tag with no release, which is an answer.
func (c *Client) isRelease(ctx context.Context, repo, tag string) (bool, error) {
	var ignored struct{}
	resp, err := c.getJSON(ctx, c.repoPath(repo)+"/releases/tags/"+url.PathEscape(tag), &ignored, http.StatusNotFound)
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
// a commit has no commit to compare with a pin, and returns why.
//
// The tag comes from another repository's file, so each of its segments is
// escaped as fetchContents escapes a path.
func (c *Client) tagCommit(ctx context.Context, repo, tag string) (string, string, error) {
	segments := strings.Split(tag, "/")
	for i, s := range segments {
		segments[i] = url.PathEscape(s)
	}
	base := c.repoPath(repo) + "/git/"
	var obj refTarget
	resp, err := c.getJSON(ctx, base+"ref/tags/"+strings.Join(segments, "/"), &obj, http.StatusNotFound)
	if err != nil {
		return "", "", err
	}
	if resp.status == http.StatusNotFound {
		return "", "", nil
	}
	for range maxTagDepth {
		switch obj.Object.Type {
		case "commit":
			return obj.Object.SHA, "", nil
		case "tag":
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
	file, err := c.fetchContents(ctx, renovate.Preset{Repo: repo, Path: pluginManifest, Ref: sha})
	if err != nil {
		return "", "", err
	}
	switch file.problem {
	case "":
	case renovate.ProblemNotFound:
		return "", "no " + pluginManifest + " at " + sha, nil
	default:
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
