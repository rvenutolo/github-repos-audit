package audit

import "time"

// ActionsStorage is what a repository holds in GitHub Actions storage: its
// caches and its artifacts. Whether a cache is the right size, or the usage
// too close to the limit, is internal/rules' decision. Sizes are bytes as
// GitHub reports them.
type ActionsStorage struct {
	// Caches is every cache entry, sorted by key, then ref, then creation
	// time. last_accessed_at is deliberately not recorded: it changes on
	// every run that restores the cache, and recording it would make
	// audit.json churn without any fact having changed.
	Caches []Cache `json:"caches,omitzero"`
	// CacheUsageBytes is the total size of the active caches, as GitHub's
	// own usage figure gives it rather than as a sum over Caches.
	CacheUsageBytes int64 `json:"cache_usage_bytes"`
	// CacheLimitBytes is the repository's cache storage limit, the
	// storage-limit endpoint's max_cache_size_gb taken as decimal gigabytes
	// (× 1,000,000,000). Zero when no limit was read.
	CacheLimitBytes int64 `json:"cache_limit_bytes,omitzero"`
	// ArtifactCount is how many artifacts have not expired. An expired
	// artifact is still listed by GitHub but holds no storage.
	ArtifactCount int `json:"artifact_count"`
	// ArtifactBytes is the total size of those artifacts.
	ArtifactBytes int64 `json:"artifact_bytes"`
}

// Cache is one Actions cache entry.
type Cache struct {
	// Key is the cache key as the saving step wrote it.
	Key string `json:"key"`
	// Ref is the ref the cache was saved from: "refs/heads/<branch>" or
	// "refs/pull/<number>/merge". A cache is restorable only from its own
	// ref and the refs that branch from it, so a pull request's caches never
	// serve the default branch.
	Ref string `json:"ref"`
	// SizeBytes is the stored size.
	SizeBytes int64 `json:"size_bytes"`
	// CreatedAt is when the entry was saved, in UTC.
	CreatedAt time.Time `json:"created_at"`
}
