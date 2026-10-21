package github

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
)

// storagePageSize is the page size of the caches and artifacts lists, the
// API's maximum, so most repositories answer each list in one request.
const storagePageSize = "100"

// maxStoragePages stops a paging fault from looping forever. At the page size
// above it is ten thousand entries, far past what a repository's 10 GB cache
// limit holds and past any artifact retention this account uses; a list that
// long is an error rather than a sum taken over the pages read so far, which
// would be a smaller figure presented as the whole.
const maxStoragePages = 100

// bytesPerGB converts the storage limit, which GitHub gives in gigabytes, to
// bytes. Decimal, as every size this tool reports is.
const bytesPerGB = 1_000_000_000

// actionsCaches is one page of the caches list.
type actionsCaches struct {
	ActionsCaches []actionsCache `json:"actions_caches"`
}

// actionsCache is one cache entry. last_accessed_at is not read: see
// audit.ActionsStorage.
type actionsCache struct {
	Key         string    `json:"key"`
	Ref         string    `json:"ref"`
	SizeInBytes int64     `json:"size_in_bytes"`
	CreatedAt   time.Time `json:"created_at"`
}

// actionsCacheUsage is the repository's cache usage.
type actionsCacheUsage struct {
	ActiveCachesSizeInBytes int64 `json:"active_caches_size_in_bytes"`
}

// actionsCacheLimit is the repository's cache storage limit. The live API
// answers a whole number of gigabytes; a fractional one would fail the
// decode and abort the run, which is the right answer to a shape this tool
// has never seen.
type actionsCacheLimit struct {
	MaxCacheSizeGB int64 `json:"max_cache_size_gb"`
}

// actionsArtifacts is one page of the artifacts list.
type actionsArtifacts struct {
	Artifacts []actionsArtifact `json:"artifacts"`
}

// actionsArtifact is one artifact. GitHub keeps listing an artifact after it
// expires, with expired set and its storage already released.
type actionsArtifact struct {
	SizeInBytes int64 `json:"size_in_bytes"`
	Expired     bool  `json:"expired"`
}

// fetchStorage reads what the repository holds in Actions storage: every
// cache entry, the cache usage and limit, and the count and size of the
// artifacts that have not expired. base is the repository's
// /repos/{owner}/{name} path.
//
// It costs four requests, plus one more per further hundred caches or
// artifacts. Every endpoint answers 200 whatever the repository holds, an
// empty list or a zero usage included, so no other status is an answer: any
// of them aborts the run, because a usage GitHub declined to give is not a
// usage of zero.
func (c *Client) fetchStorage(ctx context.Context, base string, s *audit.ActionsStorage) error {
	var caches []audit.Cache
	err := getPages(ctx, c, base+"/actions/caches?per_page="+storagePageSize, func(p *actionsCaches) {
		for _, e := range p.ActionsCaches {
			caches = append(caches, audit.Cache{
				Key: e.Key, Ref: e.Ref, SizeBytes: e.SizeInBytes, CreatedAt: e.CreatedAt.UTC(),
			})
		}
	})
	if err != nil {
		return err
	}
	// GitHub lists caches by last access, which changes on every restore, so
	// its order is no fact about them. Two entries can share a key and a ref,
	// differing only in version, which is why creation time is the last word.
	slices.SortFunc(caches, func(a, b audit.Cache) int {
		return cmp.Or(
			strings.Compare(a.Key, b.Key),
			strings.Compare(a.Ref, b.Ref),
			a.CreatedAt.Compare(b.CreatedAt),
			cmp.Compare(a.SizeBytes, b.SizeBytes),
		)
	})
	s.Caches = caches

	var usage actionsCacheUsage
	if _, err := c.getJSON(ctx, base+"/actions/cache/usage", &usage); err != nil {
		return err
	}
	s.CacheUsageBytes = usage.ActiveCachesSizeInBytes

	var limit actionsCacheLimit
	if _, err := c.getJSON(ctx, base+"/actions/cache/storage-limit", &limit); err != nil {
		return err
	}
	s.CacheLimitBytes = limit.MaxCacheSizeGB * bytesPerGB

	return getPages(ctx, c, base+"/actions/artifacts?per_page="+storagePageSize, func(p *actionsArtifacts) {
		for _, a := range p.Artifacts {
			if a.Expired {
				continue
			}
			s.ArtifactCount++
			s.ArtifactBytes += a.SizeInBytes
		}
	})
}

// getPages reads path and every page its Link header leads to, handing each
// decoded page to visit. The Link GitHub sends for these lists names the
// repository by numeric id (/repositories/{id}/…) rather than by owner and
// name; nextPage keeps that path and drops the host, so the request still
// goes to the client's own base URL.
func getPages[P any](ctx context.Context, c *Client, path string, visit func(*P)) error {
	first := path
	for page := 1; path != ""; page++ {
		if page > maxStoragePages {
			return fmt.Errorf("%s: more than %d pages", first, maxStoragePages)
		}
		var p P
		resp, err := c.getJSON(ctx, path, &p)
		if err != nil {
			return err
		}
		visit(&p)
		path = nextPage(resp.header.Get("Link"))
	}
	return nil
}
