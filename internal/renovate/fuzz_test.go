package renovate_test

import (
	"context"
	"slices"
	"testing"

	"github.com/rvenutolo/github-repos-audit/internal/renovate"
)

// FuzzResolve holds the invariants every input must keep: Resolve never
// panics, an unresolved result carries nothing else, and a resolved one
// carries exactly one of the nine batching outcomes and only schedule lists
// audit.json's schema accepts: nil, or non-empty with no empty entry.
func FuzzResolve(f *testing.F) {
	for _, seed := range []string{
		`{}`, `{"minimumReleaseAge": "7 days"}`, `{"extends": ["github>gh-owner/x"]}`,
		`{minimumReleaseAge: '3 days',}`, `{"stabilityDays": 2}`, `[`,
		`{"extends": ["group:all", "schedule:weekly"], "separateMajorMinor": true}`,
		`{"schedule": 5, "packageRules": [{"matchPackageNames": ["*"], "groupName": "g"}]}`,
		`{"lockFileMaintenance": {"enabled": true, "schedule": "weekly"}, "groupName": "g"}`,
		`{"schedule": ["at any time", ""], "lockFileMaintenance": {"enabled": true, "schedule": []}, "groupName": "g"}`,
		`{"groupName": "{{parentDir}}", "groupSlug": null, "additionalBranchPrefix": "{{x}}", "minor": {"groupName": null}}`,
		`{"major": {"separateMajorMinor": false}, "packageRules": [{"matchUpdateTypes": ["lockFileMaintenance"], "schedule": "on monday", "extends": [":group(monorepo:x, x)"]}]}`,
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
		for _, sched := range [][]string{res.BatchingSchedule, res.LockFileSchedule} {
			if sched != nil && (len(sched) == 0 || slices.Contains(sched, "")) {
				t.Errorf("Resolve() recorded schedule %q, want nil or non-empty entries", sched)
			}
		}
	})
}
