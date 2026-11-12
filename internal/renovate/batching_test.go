package renovate_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
	"github.com/rvenutolo/github-repos-audit/internal/schema"
)

// groupRule groups minor, patch and digest updates of every package, the shape
// a hand-written weekly batch takes. It leaves majors alone, so a row built on
// it exercises the steps after the majors check. Its groupSlug replaces any a
// preset's narrower group rule set earlier; bareGroupRule is the same rule
// without one.
const (
	groupRule     = `{"matchPackageNames":["*"],"matchUpdateTypes":["minor","patch","digest"],"groupName":"all non-major","groupSlug":"all-non-major"}`
	bareGroupRule = `{"matchPackageNames":["*"],"matchUpdateTypes":["minor","patch","digest"],"groupName":"all non-major"}`
	// anyGroupRule reaches every update type, majors included.
	anyGroupRule = `{"matchPackageNames":["*"],"groupName":"all","groupSlug":"all"}`
)

const (
	weeklyCron  = "* 0-3 * * 1"
	monthlyCron = "* 0-3 1 * *"
)

func TestResolve_batching(t *testing.T) {
	t.Parallel()

	store := files{
		"preset-store/group.json@":    `{"packageRules": [` + groupRule + `]}`,
		"preset-store/schedule.json@": `{"schedule": ["before 4am on monday"]}`,
		"preset-store/prefix.json@":   `{"additionalBranchPrefix": "{{parentDir}}-"}`,
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
		// groupSlug: Renovate names a group's branch from groupSlug ?? groupName.
		{"group:allNonMajor then a catch-all without a slug", `{"extends":["group:allNonMajor","schedule:weekly"],"packageRules":[` + bareGroupRule + `]}`, "split_group", nil, nil},
		{"group:allDigest then a catch-all without a slug", `{"extends":["group:allDigest","schedule:weekly"],"packageRules":[` + bareGroupRule + `]}`, "split_group", nil, nil},
		{"config:recommended then a catch-all without a slug", `{"extends":["config:recommended","schedule:weekly"],"packageRules":[` + bareGroupRule + `]}`, "split_group", nil, nil},
		{"group:allNonMajor then a catch-all with its own slug", `{"extends":["group:allNonMajor","schedule:weekly"],"packageRules":[` + groupRule + `]}`, "ok", []string{weeklyCron}, nil},
		{"a later rule setting only a slug", `{"extends":["schedule:weekly"],"packageRules":[` + groupRule + `,{"matchManagers":["npm"],"groupSlug":"npm"}]}`, "split_group", nil, nil},
		{"an earlier rule's slug inherited by a catch-all without one", `{"extends":["schedule:weekly"],"packageRules":[{"matchManagers":["npm"],"groupSlug":"npm"},` + bareGroupRule + `]}`, "split_group", nil, nil},
		{"an earlier rule's slug replaced by the catch-all's own", `{"extends":["schedule:weekly"],"packageRules":[{"matchManagers":["npm"],"groupSlug":"npm"},` + groupRule + `]}`, "ok", []string{weeklyCron}, nil},
		{"a templated group name without a slug", `{"extends":["schedule:weekly"],"packageRules":[{"matchPackageNames":["*"],"matchUpdateTypes":["minor","patch","digest"],"groupName":"{{parentDir}}"}]}`, "split_group", nil, nil},
		{"a templated slug", `{"extends":["schedule:weekly"],"packageRules":[{"matchPackageNames":["*"],"matchUpdateTypes":["minor","patch","digest"],"groupName":"g","groupSlug":"{{parentDir}}"}]}`, "split_group", nil, nil},
		{"a null slug on the group rule", `{"extends":["schedule:weekly"],"packageRules":[{"matchPackageNames":["*"],"matchUpdateTypes":["minor","patch","digest"],"groupName":"g","groupSlug":null}]}`, "split_group", nil, nil},
		{"a top-level slug with a top-level group name", `{"groupName":"all","groupSlug":"all","separateMajorMinor":true,"schedule":["before 4am on monday"]}`, "ok", []string{"before 4am on monday"}, nil},
		{"a top-level slug under a catch-all rule without one", `{"groupSlug":"x","schedule":["before 4am on monday"],"packageRules":[` + bareGroupRule + `]}`, "split_group", nil, nil},
		// Branch names: branchPrefix + additionalBranchPrefix + branchTopic.
		{"a templated top-level additionalBranchPrefix", `{"additionalBranchPrefix":"{{parentDir}}-","extends":["schedule:weekly"],"packageRules":[` + groupRule + `]}`, "split_group", nil, nil},
		{"a templated top-level branchPrefix", `{"branchPrefix":"deps/{{manager}}/","extends":["schedule:weekly"],"packageRules":[` + groupRule + `]}`, "split_group", nil, nil},
		{"a fixed top-level additionalBranchPrefix", `{"additionalBranchPrefix":"deps-","branchPrefix":"bot/","extends":["schedule:weekly"],"packageRules":[` + groupRule + `]}`, "ok", []string{weeklyCron}, nil},
		{"a templated additionalBranchPrefix undone by a later layer", `{"extends":["github>gh-owner/preset-store:prefix","schedule:weekly"],"additionalBranchPrefix":"","packageRules":[` + groupRule + `]}`, "ok", []string{weeklyCron}, nil},
		{"a top-level branchName", `{"branchName":"renovate/{{depNameSanitized}}","extends":["schedule:weekly"],"packageRules":[` + groupRule + `]}`, "split_group", nil, nil},
		{"a rule's additionalBranchPrefix", `{"extends":["schedule:weekly"],"packageRules":[` + groupRule + `,{"matchManagers":["npm"],"additionalBranchPrefix":"npm-"}]}`, "split_group", nil, nil},
		{"an earlier rule's branchPrefix", `{"extends":["schedule:weekly"],"packageRules":[{"matchManagers":["npm"],"branchPrefix":"npm/"},` + groupRule + `]}`, "split_group", nil, nil},
		{"the group rule's templated additionalBranchPrefix", `{"extends":["schedule:weekly"],"packageRules":[{"matchPackageNames":["*"],"matchUpdateTypes":["minor","patch","digest"],"groupName":"g","groupSlug":"g","additionalBranchPrefix":"{{parentDir}}-"}]}`, "split_group", nil, nil},
		{"the group rule's fixed additionalBranchPrefix", `{"extends":["schedule:weekly"],"packageRules":[{"matchPackageNames":["*"],"matchUpdateTypes":["minor","patch","digest"],"groupName":"g","groupSlug":"g","additionalBranchPrefix":"deps-"}]}`, "ok", []string{weeklyCron}, nil},
		{"a top-level group branchTopic", `{"group":{"branchTopic":"{{depNameSanitized}}"},"extends":["schedule:weekly"],"packageRules":[` + groupRule + `]}`, "split_group", nil, nil},
		{"a rule's group branchName", `{"extends":["schedule:weekly"],"packageRules":[` + groupRule + `,{"matchManagers":["npm"],"group":{"branchName":"npm"}}]}`, "split_group", nil, nil},
		{"a top-level group with no branch keys", `{"group":{"commitMessageTopic":"deps"},"extends":["schedule:weekly"],"packageRules":[` + groupRule + `]}`, "ok", []string{weeklyCron}, nil},
		// A rule's own extends is not expanded, so it can set anything.
		{"a later rule extending a preset", `{"extends":["schedule:weekly"],"packageRules":[` + groupRule + `,{"matchManagers":["npm"],"extends":["schedule:daily"]}]}`, "split_group", nil, nil},
		{"an earlier rule extending a preset", `{"extends":["schedule:weekly"],"packageRules":[{"matchManagers":["npm"],"extends":["schedule:daily"]},` + groupRule + `]}`, "split_group", nil, nil},
		// minor, patch and digest objects merge into every update of that type.
		{"a patch object's schedule and switch", `{"extends":["schedule:weekly"],"patch":{"schedule":["at any time"],"separateMinorPatch":true},"packageRules":[` + groupRule + `]}`, "split_group", nil, nil},
		{"a digest object ungrouping a top-level group", `{"groupName":"all","separateMajorMinor":true,"schedule":["before 4am on monday"],"digest":{"groupName":null}}`, "split_group", nil, nil},
		{"a minor object's branch prefix", `{"extends":["schedule:weekly"],"minor":{"additionalBranchPrefix":"minor-"},"packageRules":[` + groupRule + `]}`, "split_group", nil, nil},
		{"a minor object of a wrong type", `{"extends":["schedule:weekly"],"minor":5,"packageRules":[` + groupRule + `]}`, "split_group", nil, nil},
		{"a digest object that only automerges", `{"extends":["schedule:weekly",":automergeDigest"],"digest":{"automerge":true},"packageRules":[` + groupRule + `]}`, "ok", []string{weeklyCron}, nil},
		{"a rule's minor object", `{"extends":["schedule:weekly"],"packageRules":[` + groupRule + `,{"matchManagers":["npm"],"minor":{"schedule":["every weekend"]}}]}`, "split_group", nil, nil},
		{"a minor object's separateMajorMinor is inert", `{"extends":["schedule:weekly"],"minor":{"separateMajorMinor":false},"packageRules":[` + groupRule + `]}`, "ok", []string{weeklyCron}, nil},
		{"a major object turning majors together", `{"extends":["schedule:weekly"],"major":{"separateMajorMinor":false},"packageRules":[` + anyGroupRule + `]}`, "majors_grouped", nil, nil},
		{"a major-only rule turning majors together under a group that reaches them", `{"extends":["schedule:weekly"],"packageRules":[` + anyGroupRule + `,{"matchUpdateTypes":["major"],"separateMajorMinor":false}]}`, "majors_grouped", nil, nil},
		{"a rule's major object turning majors together", `{"extends":["schedule:weekly"],"packageRules":[` + anyGroupRule + `,{"matchManagers":["npm"],"major":{"separateMajorMinor":false}}]}`, "majors_grouped", nil, nil},
		{"a major object under a group that leaves majors out", `{"extends":["schedule:weekly"],"major":{"separateMajorMinor":false},"packageRules":[` + groupRule + `]}`, "ok", []string{weeklyCron}, nil},
		{"a top-level group of a wrong type", `{"group":5,"extends":["schedule:weekly"],"packageRules":[` + groupRule + `]}`, "split_group", nil, nil},
		{"a patch object's separateMinorPatch", `{"extends":["schedule:weekly"],"patch":{"separateMinorPatch":true},"packageRules":[` + groupRule + `]}`, "split_group", nil, nil},
		{"a major of a wrong type", `{"extends":["schedule:weekly"],"major":5,"packageRules":[` + anyGroupRule + `]}`, "majors_grouped", nil, nil},
		{"a wrongly typed major.separateMajorMinor", `{"extends":["schedule:weekly"],"major":{"separateMajorMinor":"no"},"packageRules":[` + anyGroupRule + `]}`, "majors_grouped", nil, nil},
		{"a major object leaving majors apart", `{"extends":["schedule:weekly"],"major":{"separateMajorMinor":true},"packageRules":[` + anyGroupRule + `]}`, "ok", []string{weeklyCron}, nil},
		// Package rules apply again after lockFileMaintenance is merged.
		{"a lock-file rule's schedule off the group's", `{"extends":["schedule:weekly",":maintainLockFilesWeekly"],"packageRules":[` + groupRule + `,{"matchUpdateTypes":["lockFileMaintenance"],"schedule":["every weekend"]}]}`, "lock_files_off_schedule", []string{weeklyCron}, nil},
		{"a lock-file rule's schedule inside the group's", `{"extends":["schedule:weekly",":maintainLockFilesWeekly"],"packageRules":[` + groupRule + `,{"matchUpdateTypes":["lockFileMaintenance"],"schedule":["before 2am on monday"]}]}`, "ok", []string{weeklyCron}, nil},
		{"a lock-file rule's unreadable schedule", `{"extends":["schedule:weekly",":maintainLockFilesWeekly"],"packageRules":[` + groupRule + `,{"matchUpdateTypes":["lockFileMaintenance"],"schedule":["whenever"]}]}`, "unreadable_schedule", []string{weeklyCron}, nil},
		{"a lock-file rule extending a preset", `{"extends":["schedule:weekly",":maintainLockFilesWeekly"],"packageRules":[` + groupRule + `,{"matchUpdateTypes":["lockFileMaintenance"],"extends":["schedule:monthly"]}]}`, "unreadable_schedule", []string{weeklyCron}, nil},
		{"a major-only rule's schedule leaves lock files alone", `{"extends":["schedule:weekly",":maintainLockFilesWeekly"],"packageRules":[` + groupRule + `,{"matchUpdateTypes":["major"],"schedule":["every weekend"]}]}`, "ok", []string{weeklyCron}, []string{weeklyCron}},
		{"a lock-file rule while maintenance is off", `{"extends":["schedule:weekly"],"packageRules":[` + groupRule + `,{"matchUpdateTypes":["lockFileMaintenance"],"schedule":["every weekend"]}]}`, "ok", []string{weeklyCron}, nil},
		// Built-ins.
		{"schedule:officeHours", `{"extends":["schedule:officeHours"],"packageRules":[` + groupRule + `]}`, "too_often", []string{"* 8-17 * * 1-5"}, nil},
		{"schedule:yearly", `{"extends":["schedule:yearly"],"packageRules":[` + groupRule + `]}`, "ok", []string{"* * 1 */12 *"}, nil},
		{"a catch-all then :group of a monorepo", `{"extends":["github>gh-owner/preset-store:group",":group(monorepo:react, react)","schedule:weekly"]}`, "split_group", nil, nil},
		{":group of a monorepo then an own catch-all", `{"extends":[":group(monorepo:react, react)","schedule:weekly"],"packageRules":[` + groupRule + `]}`, "ok", []string{weeklyCron}, nil},
		{":group of another preset then an own catch-all", `{"extends":["default:group(schedule:daily, daily)","schedule:weekly"],"packageRules":[` + groupRule + `]}`, "split_group", nil, nil},
		{":group alone", `{"extends":[":group(packages:linters, linters)","schedule:weekly"]}`, "no_group", nil, nil},
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

// TestResolve_batchingSchedulesFitTheSchema: every schedule list Resolve
// records reaches audit.json, whose schema forbids an empty list or an empty
// entry, so an empty schedule must not be recorded as one.
func TestResolve_batchingSchedulesFitTheSchema(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(filepath.Join("..", "render", "testdata", "golden", "audit.json"))
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	tests := []struct {
		name, root string
	}{
		{"an empty schedule", `{"schedule":[],"packageRules":[` + groupRule + `]}`},
		{"an empty schedule entry", `{"schedule":["at any time",""],"packageRules":[` + groupRule + `]}`},
		{"an empty lock-file schedule", `{"schedule":["before 4am on monday"],"lockFileMaintenance":{"enabled":true,"schedule":[]},"packageRules":[` + groupRule + `]}`},
		{"an empty lock-file schedule entry", `{"schedule":["before 4am on monday"],"lockFileMaintenance":{"enabled":true,"schedule":["at any time",""]},"packageRules":[` + groupRule + `]}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := resolve(t, tt.root, nil)
			facts, err := json.Marshal(audit.Renovate{
				Batching:         got.Batching,
				BatchingSchedule: got.BatchingSchedule,
				LockFileSchedule: got.LockFileSchedule,
			})
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var doc map[string]any
			if err := json.Unmarshal(raw, &doc); err != nil {
				t.Fatalf("parse golden: %v", err)
			}
			repos, ok := doc["repos"].([]any)
			if !ok || len(repos) == 0 {
				t.Fatalf("golden repos = %v, want a non-empty array", doc["repos"])
			}
			repo, ok := repos[0].(map[string]any)
			if !ok {
				t.Fatalf("golden repos[0] = %v, want an object", repos[0])
			}
			repo["renovate"] = json.RawMessage(facts)
			out, err := json.Marshal(doc)
			if err != nil {
				t.Fatalf("marshal doc: %v", err)
			}
			if err := schema.Validate(out); err != nil {
				t.Errorf("Validate(renovate %s) = %v, want nil", facts, err)
			}
		})
	}
}
