package github

import (
	"context"
	"strings"
	"sync"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
	"github.com/rvenutolo/github-repos-audit/internal/renovate"
)

// renovateFacts reads the Renovate facts (minimum release age, nix manager,
// lock-file maintenance) for one repository whose config at path answered
// blob. Everything short of an unexpected status is a fact: a bad config in
// one repository must not stop the report. Merging the file with its presets
// is internal/renovate's; this only feeds it the text and the fetches.
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
		NixManager:          res.NixManager,
		LockFileMaintenance: res.LockFileMaintenance,
		AutomergeMajor:      res.AutomergeMajor,
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
	file contentsAnswer
	err  error
}

func newPresetCache(c *Client) *presetCache {
	return &presetCache{c: c, entries: map[string]*presetEntry{}}
}

// fetch is a renovate.Fetch. A 404 is renovate.ProblemNotFound, the problem
// on which the resolver falls back to renovate.json.
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
		e.file, e.err = p.c.fetchContents(ctx, contentsRequest{Repo: preset.Repo, Path: preset.Path, Ref: preset.Ref})
	})
	switch {
	case e.err != nil:
		return "", "", e.err
	case e.file.notFound:
		return "", renovate.ProblemNotFound, nil
	}
	return e.file.text, e.file.problem, nil
}
