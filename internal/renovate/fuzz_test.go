package renovate_test

import (
	"context"
	"testing"

	"github.com/rvenutolo/github-repos-audit/internal/renovate"
)

// FuzzResolve holds the invariants every input must keep: Resolve never
// panics, an unresolved result carries nothing else, and a resolved one
// carries exactly one of the nine batching outcomes.
func FuzzResolve(f *testing.F) {
	for _, seed := range []string{
		`{}`, `{"minimumReleaseAge": "7 days"}`, `{"extends": ["github>gh-owner/x"]}`,
		`{minimumReleaseAge: '3 days',}`, `{"stabilityDays": 2}`, `[`,
		`{"extends": ["group:all", "schedule:weekly"], "separateMajorMinor": true}`,
		`{"schedule": 5, "packageRules": [{"matchPackageNames": ["*"], "groupName": "g"}]}`,
		`{"lockFileMaintenance": {"enabled": true, "schedule": "weekly"}, "groupName": "g"}`,
	} {
		f.Add(seed)
	}
	fetch := func(context.Context, renovate.Preset) (string, string, error) {
		return "", renovate.ProblemNotFound, nil
	}
	f.Fuzz(func(t *testing.T, root string) {
		res, err := renovate.Resolve(context.Background(), "gh-owner", "renovate.json", root, fetch)
		if err != nil {
			t.Fatalf("Resolve() error = %v; a fetch that never errors must not produce one", err)
		}
		if res.Unresolved != "" && (res.MinReleaseAge != "" || res.Source != "") {
			t.Errorf("Resolve() = %+v, want nothing but Unresolved", res)
		}
		if res.Unresolved != "" {
			if res.Batching != "" || res.BatchingSchedule != nil || res.LockFileSchedule != nil {
				t.Errorf("Resolve() = %+v, want no batching facts when unresolved", res)
			}
			return
		}
		switch res.Batching {
		case "ok", "no_group", "digests_ungrouped", "majors_grouped", "split_group",
			"no_schedule", "too_often", "unreadable_schedule", "lock_files_off_schedule":
		default:
			t.Errorf("Resolve().Batching = %q, want one of the nine outcomes", res.Batching)
		}
	})
}
