package renovate_test

import (
	"slices"
	"testing"
)

// groupRule groups minor, patch and digest updates of every package, the shape
// a hand-written weekly batch takes. It leaves majors alone, so a row built on
// it exercises the steps after the majors check.
const groupRule = `{"matchPackageNames":["*"],"matchUpdateTypes":["minor","patch","digest"],"groupName":"all non-major"}`

const (
	weeklyCron  = "* 0-3 * * 1"
	monthlyCron = "* 0-3 1 * *"
)

func TestResolve_batching(t *testing.T) {
	t.Parallel()

	store := files{
		"preset-store/group.json@":    `{"packageRules": [` + groupRule + `]}`,
		"preset-store/schedule.json@": `{"schedule": ["before 4am on monday"]}`,
	}
	tests := []struct {
		name         string
		root         string
		want         string
		wantSchedule []string
		wantLock     []string
	}{
		{"group:all groups majors", `{"extends":["group:all","schedule:weekly"]}`, "majors_grouped", nil, nil},
		{"semverAllWeekly groups majors", `{"extends":["config:semverAllWeekly"]}`, "majors_grouped", nil, []string{weeklyCron}},
		{"semverAllMonthly groups majors", `{"extends":["config:semverAllMonthly"]}`, "majors_grouped", nil, []string{monthlyCron}},
		{
			"group:all with majors separated is batched", `{"extends":["group:all","schedule:weekly"],"separateMajorMinor":true}`,
			"ok",
			[]string{weeklyCron},
			nil,
		},
		{
			"semverAllWeekly with majors separated is batched", `{"extends":["config:semverAllWeekly"],"separateMajorMinor":true}`,
			"ok",
			[]string{weeklyCron},
			[]string{weeklyCron},
		},
		{"digests left out", `{"extends":["group:allNonMajor","schedule:weekly"]}`, "digests_ungrouped", nil, nil},
		{"nothing grouped", `{}`, "no_group", nil, nil},
		{
			"a group narrowed to one manager",
			`{"packageRules":[{"matchManagers":["gomod"],"groupName":"go"}],"schedule":["before 4am on monday"]}`,
			"no_group", nil, nil,
		},
		{
			"a group of only minor and patch", `{"packageRules":[{"matchPackageNames":["*"],"matchUpdateTypes":["minor","patch"],"groupName":"x"}]}`,
			"digests_ungrouped", nil, nil,
		},
		{
			"a group of only major", `{"packageRules":[{"matchPackageNames":["*"],"matchUpdateTypes":["major"],"groupName":"x"}]}`,
			"no_group", nil, nil,
		},
		{
			"a group of only minor and digest", `{"packageRules":[{"matchPackageNames":["*"],"matchUpdateTypes":["minor","digest"],"groupName":"x"}]}`,
			"no_group", nil, nil,
		},
		{
			"a rule naming every update type, with a string schedule",
			`{"packageRules":[{"matchUpdateTypes":["major","minor","patch","digest"],"groupName":"all"}],"schedule":"before 4am on monday"}`,
			"ok",
			[]string{"before 4am on monday"},
			nil,
		},
		{
			"a rule naming every update type, with majors together",
			`{"separateMajorMinor":false,"packageRules":[{"matchUpdateTypes":["major","minor","patch","digest"],"groupName":"all"}],"schedule":"before 4am on monday"}`,
			"majors_grouped", nil, nil,
		},
		{
			"a rule naming every update type, with its own majors switch",
			`{"packageRules":[{"matchUpdateTypes":["major","minor","patch","digest"],"groupName":"all","separateMajorMinor":false}]}`,
			"majors_grouped", nil, nil,
		},
		{
			"a later narrowed rule turns majors together",
			`{"schedule":"before 4am on monday","packageRules":[` + groupRule + `,{"matchManagers":["npm"],"separateMajorMinor":false}]}`,
			"majors_grouped", nil, nil,
		},
		{
			"a later rule turning majors together for majors only is ignored",
			`{"schedule":"before 4am on monday","packageRules":[` + groupRule + `,{"matchUpdateTypes":["major"],"separateMajorMinor":false}]}`,
			"ok",
			[]string{"before 4am on monday"},
			nil,
		},
		{
			"a later rule with its own schedule splits the group",
			`{"schedule":"before 4am on monday","packageRules":[` + groupRule + `,{"matchManagers":["npm"],"schedule":["every weekend"]}]}`,
			"split_group", nil, nil,
		},
		{
			"an earlier rule with its own schedule splits the group",
			`{"schedule":"before 4am on monday","packageRules":[{"matchManagers":["npm"],"schedule":["every weekend"]},` + groupRule + `]}`,
			"split_group", nil, nil,
		},
		{
			"an earlier major-only rule with its own schedule is ignored",
			`{"schedule":"before 4am on monday","packageRules":[{"matchUpdateTypes":["major"],"schedule":["every weekend"]},` + groupRule + `]}`,
			"ok",
			[]string{"before 4am on monday"},
			nil,
		},
		{
			"a catch-all then group:linters",
			`{"extends":["github>gh-owner/preset-store:group","group:linters","schedule:weekly"]}`,
			"split_group", nil, nil,
		},
		{
			"group:linters then an own catch-all",
			`{"extends":["group:linters","schedule:weekly"],"packageRules":[` + groupRule + `]}`,
			"ok",
			[]string{weeklyCron},
			nil,
		},
		{
			"a catch-all then group:nodeJs",
			`{"extends":["github>gh-owner/preset-store:group","group:nodeJs","schedule:weekly"]}`,
			"split_group", nil, nil,
		},
		{
			"a later major-only rule with its own schedule is ignored",
			`{"schedule":"before 4am on monday","packageRules":[` + groupRule + `,{"matchUpdateTypes":["major"],"schedule":["every weekend"]}]}`,
			"ok",
			[]string{"before 4am on monday"},
			nil,
		},
		{
			"a narrowed rule with separateMultipleMinor splits the group",
			`{"packageRules":[` + groupRule + `,{"matchManagers":["npm"],"matchUpdateTypes":["minor"],"separateMultipleMinor":true}]}`,
			"split_group", nil, nil,
		},
		{
			"an earlier narrowed rule with separateMinorPatch splits the group",
			`{"packageRules":[{"matchManagers":["npm"],"separateMinorPatch":true},` + groupRule + `]}`,
			"split_group", nil, nil,
		},
		{
			"a disabled group rule", `{"schedule":"before 4am on monday","packageRules":[{"matchPackageNames":["*"],"matchUpdateTypes":["minor","patch","digest"],"groupName":"g","enabled":false}]}`,
			"no_group", nil, nil,
		},
		{
			"an enabled group rule", `{"schedule":"before 4am on monday","packageRules":[{"matchPackageNames":["*"],"matchUpdateTypes":["minor","patch","digest"],"groupName":"g","enabled":true}]}`,
			"ok",
			[]string{"before 4am on monday"},
			nil,
		},
		{
			"lock files extending a preset are unreadable",
			`{"extends":["schedule:weekly"],"lockFileMaintenance":{"enabled":true,"extends":["schedule:monthly"]},"packageRules":[` + groupRule + `]}`,
			"unreadable_schedule",
			[]string{weeklyCron},
			nil,
		},
		{
			"lock files off extending a preset are ignored",
			`{"extends":["schedule:weekly"],"lockFileMaintenance":{"enabled":false,"extends":["schedule:monthly"]},"packageRules":[` + groupRule + `]}`,
			"ok",
			[]string{weeklyCron},
			nil,
		},
		{
			":separatePatchReleases", `{"extends":["schedule:weekly",":separatePatchReleases"],"packageRules":[` + groupRule + `]}`,
			"split_group", nil, nil,
		},
		{
			"default:separatePatchReleases", `{"extends":["schedule:weekly","default:separatePatchReleases"],"packageRules":[` + groupRule + `]}`,
			"split_group", nil, nil,
		},
		{
			":automergePatch", `{"extends":["schedule:weekly",":automergePatch"],"packageRules":[` + groupRule + `]}`,
			"split_group", nil, nil,
		},
		{
			":separateMultipleMinorReleases", `{"extends":["schedule:weekly",":separateMultipleMinorReleases"],"packageRules":[` + groupRule + `]}`,
			"split_group", nil, nil,
		},
		{
			"default:separateMultipleMinorReleases", `{"extends":["schedule:weekly","default:separateMultipleMinorReleases"],"packageRules":[` + groupRule + `]}`,
			"split_group", nil, nil,
		},
		{
			":combinePatchMinorReleases undoes a split", `{"extends":["schedule:weekly",":separatePatchReleases",":combinePatchMinorReleases"],"packageRules":[` + groupRule + `]}`,
			"ok",
			[]string{weeklyCron},
			nil,
		},
		{
			":separateMajorReleases undoes majors together", `{"extends":["group:all","schedule:weekly",":separateMajorReleases"]}`,
			"ok",
			[]string{weeklyCron},
			nil,
		},
		{
			":separateMultipleMajorReleases undoes majors together", `{"extends":["group:all","schedule:weekly",":separateMultipleMajorReleases"]}`,
			"ok",
			[]string{weeklyCron},
			nil,
		},
		{
			"config:recommended splits an earlier catch-all",
			`{"extends":["github>gh-owner/preset-store:group","config:recommended","schedule:weekly"]}`,
			"split_group", nil, nil,
		},
		{
			"group:monorepos splits an earlier catch-all",
			`{"extends":["github>gh-owner/preset-store:group","group:monorepos","schedule:weekly"]}`,
			"split_group", nil, nil,
		},
		{
			"group:recommended splits an earlier catch-all",
			`{"extends":["github>gh-owner/preset-store:group","group:recommended","schedule:weekly"]}`,
			"split_group", nil, nil,
		},
		{
			"config:best-practices splits an earlier catch-all",
			`{"extends":["github>gh-owner/preset-store:group","config:best-practices","schedule:weekly"]}`,
			"split_group", nil,
			[]string{weeklyCron},
		},
		{
			"group:allDigest after a catch-all becomes the group and leaves minors out",
			`{"extends":["github>gh-owner/preset-store:group","group:allDigest","schedule:weekly"]}`,
			"no_group", nil, nil,
		},
		{
			"config:recommended before an own catch-all",
			`{"extends":["config:recommended","schedule:weekly"],"packageRules":[` + groupRule + `]}`,
			"ok",
			[]string{weeklyCron},
			nil,
		},
		{"config:recommended alone", `{"extends":["config:recommended"]}`, "no_group", nil, nil},
		{"config:base alone", `{"extends":["config:base"]}`, "no_group", nil, nil},
		{"majors left to the default", `{"packageRules":[` + groupRule + `],"schedule":"before 4am on monday"}`, "ok", []string{"before 4am on monday"}, nil},
		{
			"majors grouped through the top-level switch",
			`{"separateMajorMinor":false,"packageRules":[{"matchPackageNames":["*"],"groupName":"all"}]}`,
			"majors_grouped", nil, nil,
		},
		{
			"a wrongly typed separateMajorMinor reads as false",
			`{"separateMajorMinor":"no","packageRules":[{"matchPackageNames":["*"],"groupName":"all"}]}`,
			"majors_grouped", nil, nil,
		},
		{
			"a group rule's own separateMajorMinor false",
			`{"packageRules":[{"matchPackageNames":["*"],"groupName":"all","separateMajorMinor":false}]}`,
			"majors_grouped", nil, nil,
		},
		{
			"a later rule ungroups a package",
			`{"extends":["group:all","schedule:weekly"],"separateMajorMinor":true,"packageRules":[{"matchPackageNames":["foo"],"groupName":null}]}`,
			"split_group", nil, nil,
		},
		{
			"a later rule groups only majors",
			`{"schedule":"before 4am on monday","packageRules":[` + groupRule + `,{"matchUpdateTypes":["major"],"groupName":"majors"}]}`,
			"ok",
			[]string{"before 4am on monday"},
			nil,
		},
		{
			"an earlier rule groups a package differently",
			`{"schedule":"before 4am on monday","packageRules":[{"matchPackageNames":["foo"],"groupName":"foo"},` + groupRule + `]}`,
			"ok",
			[]string{"before 4am on monday"},
			nil,
		},
		{
			"a later rule naming no selector is unreadable and splits",
			`{"packageRules":[` + groupRule + `,{"groupName":"x"}]}`, "split_group", nil, nil,
		},
		{
			"a later rule of a wrong shape splits",
			`{"packageRules":[` + groupRule + `,"x"]}`, "split_group", nil, nil,
		},
		{
			"a later rule with glob update types splits",
			`{"packageRules":[` + groupRule + `,{"matchUpdateTypes":["!major"],"groupName":"x"}]}`, "split_group", nil, nil,
		},
		{
			"separateMinorPatch splits the group", `{"separateMinorPatch":true,"packageRules":[` + groupRule + `]}`,
			"split_group", nil, nil,
		},
		{
			"separateMultipleMinor splits the group", `{"separateMultipleMinor":true,"packageRules":[` + groupRule + `]}`,
			"split_group", nil, nil,
		},
		{
			"a wrongly typed separateMinorPatch reads as true", `{"separateMinorPatch":"yes","packageRules":[` + groupRule + `]}`,
			"split_group", nil, nil,
		},
		{
			"separateMinorPatch inside a rule", `{"packageRules":[` + groupRule + `,{"matchPackageNames":["foo"],"separateMinorPatch":true}]}`,
			"split_group", nil, nil,
		},
		{
			"separateMinorPatch off is no split", `{"separateMinorPatch":false,"schedule":"before 4am on monday","packageRules":[` + groupRule + `]}`,
			"ok",
			[]string{"before 4am on monday"},
			nil,
		},
		{"no schedule", `{"packageRules":[` + groupRule + `]}`, "no_schedule", nil, nil},
		{"group:all with majors separated and no schedule", `{"extends":["group:all"],"separateMajorMinor":true}`, "no_schedule", nil, nil},
		{
			"too often", `{"schedule":["every weekday"],"packageRules":[` + groupRule + `]}`,
			"too_often",
			[]string{"every weekday"},
			nil,
		},
		{
			"unreadable text", `{"schedule":["whenever"],"packageRules":[` + groupRule + `]}`,
			"unreadable_schedule", nil, nil,
		},
		{"schedule of a wrong type", `{"schedule":5,"packageRules":[` + groupRule + `]}`, "unreadable_schedule", nil, nil},
		{"schedule null", `{"schedule":null,"packageRules":[` + groupRule + `]}`, "unreadable_schedule", nil, nil},
		{
			"the rule's schedule wins over the top level",
			`{"schedule":["every weekday"],"packageRules":[{"matchPackageNames":["*"],"matchUpdateTypes":["minor","patch","digest"],"groupName":"g","schedule":"before 4am on monday"}]}`,
			"ok",
			[]string{"before 4am on monday"},
			nil,
		},
		{
			"the rule's schedule is the one judged",
			`{"schedule":"before 4am on monday","packageRules":[{"matchPackageNames":["*"],"matchUpdateTypes":["minor","patch","digest"],"groupName":"g","schedule":["every weekday"]}]}`,
			"too_often",
			[]string{"every weekday"},
			nil,
		},
		{
			"the rule's schedule of a wrong type",
			`{"schedule":"before 4am on monday","packageRules":[{"matchPackageNames":["*"],"matchUpdateTypes":["minor","patch","digest"],"groupName":"g","schedule":{}}]}`,
			"unreadable_schedule", nil, nil,
		},
		{
			"a top-level group name groups every update type",
			`{"groupName":"all","separateMajorMinor":true,"schedule":["before 4am on monday"]}`,
			"ok",
			[]string{"before 4am on monday"},
			nil,
		},
		{"a top-level group name leaves majors apart by default", `{"groupName":"all","schedule":["before 4am on monday"]}`, "ok", []string{"before 4am on monday"}, nil},
		{"a top-level group name with majors together", `{"groupName":"all","separateMajorMinor":false}`, "majors_grouped", nil, nil},
		{"an emptied top-level group name", `{"extends":["group:all"],"groupName":"","packageRules":[]}`, "majors_grouped", nil, nil},
		{
			"a later rule splits a top-level group name",
			`{"groupName":"all","separateMajorMinor":true,"packageRules":[{"matchPackageNames":["foo"],"groupName":"foo"}]}`,
			"split_group", nil, nil,
		},
		{
			"lock files off the group's schedule",
			`{"schedule":["every weekend"],"lockFileMaintenance":{"enabled":true},"packageRules":[` + groupRule + `]}`,
			"lock_files_off_schedule",
			[]string{"every weekend"},
			[]string{"before 4am on monday"},
		},
		{
			"lock files on the group's schedule",
			`{"schedule":["every weekend"],"lockFileMaintenance":{"enabled":true,"schedule":["every weekend"]},"packageRules":[` + groupRule + `]}`,
			"ok",
			[]string{"every weekend"},
			[]string{"every weekend"},
		},
		{
			"lock files default inside a weekly window",
			`{"schedule":"before 4am on monday","lockFileMaintenance":{"enabled":true},"packageRules":[` + groupRule + `]}`,
			"ok",
			[]string{"before 4am on monday"},
			[]string{"before 4am on monday"},
		},
		{
			"lock files unreadable",
			`{"schedule":"before 4am on monday","lockFileMaintenance":{"enabled":true,"schedule":"whenever"},"packageRules":[` + groupRule + `]}`,
			"unreadable_schedule",
			[]string{"before 4am on monday"},
			nil,
		},
		{
			"lock files with a schedule of a wrong type",
			`{"schedule":"before 4am on monday","lockFileMaintenance":{"enabled":true,"schedule":3},"packageRules":[` + groupRule + `]}`,
			"unreadable_schedule",
			[]string{"before 4am on monday"},
			nil,
		},
		{
			"lock files off ignore their schedule",
			`{"schedule":["every weekend"],"lockFileMaintenance":{"enabled":false,"schedule":"whenever"},"packageRules":[` + groupRule + `]}`,
			"ok",
			[]string{"every weekend"},
			nil,
		},
		{
			"lock files from the top-level schedule are not inherited",
			`{"schedule":["every weekend"],"extends":[":maintainLockFilesWeekly"],"packageRules":[` + groupRule + `]}`,
			"lock_files_off_schedule",
			[]string{"every weekend"},
			[]string{weeklyCron},
		},
		{
			"monthly lock files inside a weekly window are not within it",
			`{"extends":["schedule:weekly",":maintainLockFilesMonthly"],"packageRules":[` + groupRule + `]}`,
			"lock_files_off_schedule",
			[]string{weeklyCron},
			[]string{monthlyCron},
		},
		{
			"best-practices lock files",
			`{"extends":["config:best-practices","schedule:weekly"],"packageRules":[` + groupRule + `]}`,
			"ok",
			[]string{weeklyCron},
			[]string{weeklyCron},
		},
		{
			"lock files disabled by a later preset",
			`{"extends":[":maintainLockFilesWeekly",":maintainLockFilesDisabled","schedule:weekly"],"packageRules":[` + groupRule + `]}`,
			"ok",
			[]string{weeklyCron},
			nil,
		},
		{
			"group from a preset, schedule from the own file",
			`{"extends":["github>gh-owner/preset-store:group"],"schedule":"before 4am on monday"}`,
			"ok",
			[]string{"before 4am on monday"},
			nil,
		},
		{
			"schedule from a preset, group from the own file",
			`{"extends":["github>gh-owner/preset-store:schedule"],"packageRules":[` + groupRule + `]}`,
			"ok",
			[]string{"before 4am on monday"},
			nil,
		},
		{"schedule:monthly", `{"extends":["schedule:monthly"],"packageRules":[` + groupRule + `]}`, "ok", []string{monthlyCron}, nil},
		{"schedule:daily", `{"extends":["schedule:daily"],"packageRules":[` + groupRule + `]}`, "too_often", []string{"* 0-3 * * *"}, nil},
		{"schedule:weekends", `{"extends":["schedule:weekends"],"packageRules":[` + groupRule + `]}`, "ok", []string{"* * * * 0,6"}, nil},
		{"schedule:earlyMondays", `{"extends":["schedule:earlyMondays"],"packageRules":[` + groupRule + `]}`, "ok", []string{weeklyCron}, nil},
		{"schedule:quarterly", `{"extends":["schedule:quarterly"],"packageRules":[` + groupRule + `]}`, "ok", []string{"* * 1 */3 *"}, nil},
		{"schedule:weekdays", `{"extends":["schedule:weekdays"],"packageRules":[` + groupRule + `]}`, "too_often", []string{"* * * * 1-5"}, nil},
		{
			"schedule:nonOfficeHours", `{"extends":["schedule:nonOfficeHours"],"packageRules":[` + groupRule + `]}`,
			"too_often",
			[]string{"* 0-4,22-23 * * 1-5", "* * * * 0,6"},
			nil,
		},
		{"legacy match-everything pattern", `{"schedule":"before 4am on monday","packageRules":[{"matchPackagePatterns":[".*"],"matchUpdateTypes":["minor","patch","digest"],"groupName":"g"}]}`, "ok", []string{"before 4am on monday"}, nil},
		{"a bare star pattern", `{"schedule":"before 4am on monday","packageRules":[{"matchPackagePatterns":"*","matchUpdateTypes":["minor","patch","digest"],"groupName":"g"}]}`, "ok", []string{"before 4am on monday"}, nil},
		{"a bare star name", `{"schedule":"before 4am on monday","packageRules":[{"matchPackageNames":"*","matchUpdateTypes":["minor","patch","digest"],"groupName":"g"}]}`, "ok", []string{"before 4am on monday"}, nil},
		{"a group with a second matcher is narrowed", `{"packageRules":[{"matchPackageNames":["*"],"matchManagers":["npm"],"matchUpdateTypes":["minor","patch","digest"],"groupName":"g"}]}`, "no_group", nil, nil},
		{"a group with two package names is narrowed", `{"packageRules":[{"matchPackageNames":["*","foo"],"matchUpdateTypes":["minor","patch","digest"],"groupName":"g"}]}`, "no_group", nil, nil},
		{"a group with a negated name is narrowed", `{"packageRules":[{"matchPackageNames":["*","!foo"],"matchUpdateTypes":["minor","patch","digest"],"groupName":"g"}]}`, "no_group", nil, nil},
		{"a group with a wrongly typed name list is narrowed", `{"packageRules":[{"matchPackageNames":5,"matchUpdateTypes":["minor","patch","digest"],"groupName":"g"}]}`, "no_group", nil, nil},
		{"a group named with a non-string is no group", `{"packageRules":[{"matchPackageNames":["*"],"groupName":5}]}`, "no_group", nil, nil},
		{"a group named with an empty string is no group", `{"packageRules":[{"matchPackageNames":["*"],"groupName":""}]}`, "no_group", nil, nil},
		{"an update type string", `{"schedule":"before 4am on monday","packageRules":[{"matchPackageNames":["*"],"matchUpdateTypes":"minor","groupName":"g"}]}`, "no_group", nil, nil},
		{
			"an update type outside the batch is ignored",
			`{"schedule":"before 4am on monday","packageRules":[{"matchPackageNames":["*"],"matchUpdateTypes":["minor","patch","digest","pin"],"groupName":"g"}]}`,
			"ok",
			[]string{"before 4am on monday"},
			nil,
		},
		{"a schedule list with a non-string", `{"schedule":["before 4am on monday",5],"packageRules":[` + groupRule + `]}`, "unreadable_schedule", nil, nil},
		{"a wrongly typed separateMultipleMinor reads as true", `{"separateMultipleMinor":"yes","packageRules":[` + groupRule + `]}`, "split_group", nil, nil},
		{"a package name list holding a non-string is narrowed", `{"packageRules":[{"matchPackageNames":[5],"matchUpdateTypes":["minor","patch","digest"],"groupName":"g"}]}`, "no_group", nil, nil},
		{"a wrongly typed packageRules", `{"packageRules":5}`, "no_group", nil, nil},
		{"legacy updateTypes", `{"schedule":"before 4am on monday","packageRules":[{"matchPackageNames":["*"],"updateTypes":["minor","patch","digest"],"groupName":"g"}]}`, "ok", []string{"before 4am on monday"}, nil},
		{"both update type spellings are narrowed", `{"packageRules":[{"matchPackageNames":["*"],"updateTypes":["minor","patch","digest"],"matchUpdateTypes":["minor","patch","digest"],"groupName":"g"}]}`, "no_group", nil, nil},
		{"a non-string update type is malformed", `{"packageRules":[{"matchPackageNames":["*"],"matchUpdateTypes":["minor",5],"groupName":"g"}]}`, "no_group", nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := resolve(t, tt.root, store)
			if got.Unresolved != "" {
				t.Fatalf("Resolve().Unresolved = %q, want resolved", got.Unresolved)
			}
			if got.Batching != tt.want {
				t.Errorf("Resolve().Batching = %q, want %q", got.Batching, tt.want)
			}
			if !slices.Equal(got.BatchingSchedule, tt.wantSchedule) {
				t.Errorf("Resolve().BatchingSchedule = %q, want %q", got.BatchingSchedule, tt.wantSchedule)
			}
			if !slices.Equal(got.LockFileSchedule, tt.wantLock) {
				t.Errorf("Resolve().LockFileSchedule = %q, want %q", got.LockFileSchedule, tt.wantLock)
			}
		})
	}
}

func TestResolve_batchingUnresolved(t *testing.T) {
	t.Parallel()

	got := resolve(t, `{"extends":["github>gh-owner/missing"],"packageRules":[`+groupRule+`]}`, nil)
	if got.Unresolved == "" {
		t.Fatal("Resolve().Unresolved is empty, want the missing preset reported")
	}
	if got.Batching != "" || got.BatchingSchedule != nil || got.LockFileSchedule != nil {
		t.Errorf("Resolve() = %+v, want no batching facts when unresolved", got)
	}
}
