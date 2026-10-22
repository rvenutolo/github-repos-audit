package rules_test

import (
	"testing"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
	"github.com/rvenutolo/github-repos-audit/internal/rules"
)

// cacheUsage judges one tools repository, which standard-types.toml holds to
// cache_usage = "required", with the given Actions cache usage and limit, and
// returns its cache_usage cell.
func cacheUsage(t *testing.T, usage, limit int64) rules.Cell {
	t.Helper()
	r := fixture("alpha", "tools")
	r.Storage = audit.ActionsStorage{CacheUsageBytes: usage, CacheLimitBytes: limit}
	rep, err := rules.Evaluate(&audit.Snapshot{
		GeneratedAt: cacheNow, Types: standardTypes(), Identity: fixtureIdentity(),
		Repos: []audit.Repo{r},
	})
	if err != nil {
		t.Fatalf("Evaluate() error = %v, want nil", err)
	}
	return rep.Repos[0].Cell(rules.CheckCacheUsage)
}

// TestEvaluate_cacheUsage_boundary: usage may reach 80% of the limit and no
// further. The comparison is exact, so 8.0 GB of a 10 GB limit passes and a
// single byte more fails, though both display as the same size.
func TestEvaluate_cacheUsage_boundary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		usage   int64
		verdict rules.Verdict
		value   string
	}{
		{"well under", 430_000_000, rules.Pass, "430 MB"},
		{"exactly 80%", 8_000_000_000, rules.Pass, "8.0 GB"},
		{"a byte over 80%", 8_000_000_001, rules.Fail, "8.0 GB"},
		{"at the limit", 10_000_000_000, rules.Fail, "10.0 GB"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := cacheUsage(t, tc.usage, 10_000_000_000)
			wantCell(t, got, tc.verdict, tc.value)
			if !got.Scalar {
				t.Errorf("cell Scalar = false, want true: the usage is a fact worth showing on n/a")
			}
		})
	}
}

// TestEvaluate_cacheUsage_noLimitIsNA: with no limit read, as for an empty
// repository whose storage is never collected, there is nothing to measure
// the usage against.
func TestEvaluate_cacheUsage_noLimitIsNA(t *testing.T) {
	t.Parallel()

	if got := cacheUsage(t, 0, 0); got.Verdict != rules.NA {
		t.Errorf("cell = %v %q, want n/a", got.Verdict, got.Value)
	}
}
