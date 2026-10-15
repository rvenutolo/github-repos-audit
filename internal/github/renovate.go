package github

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
	"github.com/rvenutolo/github-repos-audit/internal/renovate"
)

// renovateFacts reads the Renovate minimum release age for one repository
// whose config at path answered blob. Everything short of an unexpected
// status is a fact: a bad config in one repository must not stop the report.
// Merging the file with its presets is internal/renovate's; this only feeds it
// the text and the fetches.
func (c *Client) renovateFacts(
	ctx context.Context, path string, blob *renovateBlob, presets *presetCache,
) (audit.Renovate, error) {
	switch {
	case blob.IsBinary || blob.Text == nil:
		return audit.Renovate{MinReleaseAgeError: path + ": not a text file"}, nil
	case blob.IsTruncated:
		// A truncated text parses as a different config, or not at all;
		// either way it is not the file Renovate reads.
		return audit.Renovate{MinReleaseAgeError: path + ": too large to read in full"}, nil
	}
	res, err := renovate.Resolve(ctx, c.owner, path, *blob.Text, presets.fetch)
	if err != nil {
		return audit.Renovate{}, err
	}
	return audit.Renovate{
		MinReleaseAge:       res.MinReleaseAge,
		MinReleaseAgeSource: res.Source,
		MinReleaseAgeError:  res.Unresolved,
	}, nil
}

// presetCache fetches each preset file at most once per run. Several
// repositories usually extend the same shared preset, and the collectors run
// concurrently. Each key gets one entry, created under the mutex by whichever
// caller misses first, and the entry's sync.Once runs the fetch: every other
// caller, early or late, waits on that same Once and reads its outcome. A
// lookup and a separate deduplicated call would leave a gap between them in
// which a caller that missed the map starts a second fetch after the first
// has finished. A fetch outcome for one ref cannot change within a run.
//
// The error is kept with the outcome. It is never read past the run it came
// from: any error aborts the run (see Client.Collect), and the cache is made
// per run, and every collector in a run shares one context, so the first
// caller's context is everyone's.
type presetCache struct {
	c       *Client
	mu      sync.Mutex
	entries map[string]*presetEntry
}

// presetEntry is one key's fetch, run once.
type presetEntry struct {
	once sync.Once
	file presetFile
	err  error
}

// presetFile is one fetch outcome: the file's text, or the problem that makes
// it unusable (renovate.ProblemNotFound for a 404).
type presetFile struct {
	text    string
	problem string
}

func newPresetCache(c *Client) *presetCache {
	return &presetCache{c: c, entries: map[string]*presetEntry{}}
}

// fetch is a renovate.Fetch.
func (p *presetCache) fetch(ctx context.Context, preset renovate.Preset) (string, string, error) {
	// GitHub repository names are case-insensitive; paths and refs are not.
	// NUL cannot occur in any of the three, so the key is unambiguous.
	key := strings.ToLower(preset.Repo) + "\x00" + preset.Path + "\x00" + preset.Ref
	p.mu.Lock()
	e, ok := p.entries[key]
	if !ok {
		e = &presetEntry{}
		p.entries[key] = e
	}
	p.mu.Unlock()
	e.once.Do(func() {
		e.file, e.err = p.c.fetchContents(ctx, preset)
	})
	if e.err != nil {
		return "", "", e.err
	}
	return e.file.text, e.file.problem, nil
}

// contentsFile is the slice of the contents API's answer this tool reads.
type contentsFile struct {
	Type     string `json:"type"`
	Encoding string `json:"encoding"`
	Content  string `json:"content"`
}

// fetchContents reads one preset file with GET /repos/{owner}/{repo}/contents.
// A 404 — repository, path or ref missing — is an ordinary answer. The
// default Accept header returns the JSON envelope with base64 content; a
// directory answers with an array, and a file over 1 MB with encoding "none",
// neither of which is a usable preset.
//
// The path and ref come from a repository's own file, so each path segment and
// the ref are escaped rather than concatenated: the path's slashes stay
// separators, and nothing in it can reach another endpoint or add a parameter.
func (c *Client) fetchContents(ctx context.Context, p renovate.Preset) (presetFile, error) {
	segments := strings.Split(p.Path, "/")
	for i, s := range segments {
		segments[i] = url.PathEscape(s)
	}
	path := c.repoPath(p.Repo) + "/contents/" + strings.Join(segments, "/")
	if p.Ref != "" {
		path += "?ref=" + url.QueryEscape(p.Ref)
	}

	var raw json.RawMessage
	resp, err := c.getJSON(ctx, path, &raw, http.StatusNotFound)
	if err != nil {
		return presetFile{}, err
	}
	if resp.status == http.StatusNotFound {
		return presetFile{problem: renovate.ProblemNotFound}, nil
	}
	return decodeContents(raw), nil
}

// decodeContents reads a 200 answer from the contents API. Every way it can
// fail to be a preset's text is a problem to record, not an error: GitHub gave
// a definite answer, it just is not a file this tool can read.
func decodeContents(raw json.RawMessage) presetFile {
	var f contentsFile
	// A directory answers with an array, which does not decode into an object;
	// a symlink or a submodule answers with its own type.
	if json.Unmarshal(raw, &f) != nil || f.Type != "file" {
		return presetFile{problem: "not a file"}
	}
	if f.Encoding != "base64" {
		return presetFile{problem: "too large to read"}
	}
	// GitHub wraps the base64 at 60 characters with newlines.
	text, decodeErr := base64.StdEncoding.DecodeString(strings.ReplaceAll(f.Content, "\n", ""))
	if decodeErr != nil {
		return presetFile{problem: "content is not valid base64"}
	}
	return presetFile{text: string(text)}
}
