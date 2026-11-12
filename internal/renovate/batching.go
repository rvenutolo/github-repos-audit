package renovate

import "slices"

// Outcomes of batching, the verdict on whether a repository's non-major
// updates arrive as one weekly (or rarer) pull request. The names are the
// values the audit publishes.
const (
	batchOK                = "ok"
	batchNoGroup           = "no_group"
	batchDigestsUngrouped  = "digests_ungrouped"
	batchMajorsGrouped     = "majors_grouped"
	batchSplitGroup        = "split_group"
	batchNoSchedule        = "no_schedule"
	batchTooOften          = "too_often"
	batchUnreadable        = "unreadable_schedule"
	batchLockFilesOffTimes = "lock_files_off_schedule"
)

// defaultLockFileSchedule is what Renovate runs lock-file maintenance on when
// no layer sets lockFileMaintenance.schedule. It is not the top-level
// schedule: the lockFileMaintenance object is merged over the top-level
// config, so its own schedule always wins.
// A function rather than a package-level slice, so nothing can mutate it.
// https://github.com/renovatebot/renovate/blob/main/lib/config/options/index.ts
// https://github.com/renovatebot/renovate/blob/main/lib/workers/repository/updates/flatten.ts
func defaultLockFileSchedule() []string { return []string{"before 4am on monday"} }

// updateTypes is a set of the update types a batch must cover. Major is not
// one: whether majors share the batch is decided from separateMajorMinor.
type updateTypes uint8

const (
	typeMinor updateTypes = 1 << iota
	typePatch
	typeDigest

	allUpdateTypes = typeMinor | typePatch | typeDigest
)

func updateTypeBit(s string) updateTypes {
	switch s {
	case "minor":
		return typeMinor
	case "patch":
		return typePatch
	case "digest":
		return typeDigest
	}
	return 0
}

// groupName is what one rule or layer says about groupName.
type groupName struct {
	set      bool
	nonEmpty bool
}

// ownGroupName reads groupName. Only a non-empty string groups; a null, an
// empty string or any other type is a layer that ungroups, which can only
// fail the check.
func ownGroupName(cfg map[string]any) groupName {
	v, has := cfg["groupName"]
	if !has {
		return groupName{}
	}
	s, ok := v.(string)
	return groupName{set: true, nonEmpty: ok && s != ""}
}

// scheduleValue is what one layer says about a schedule key.
type scheduleValue struct {
	set     bool
	entries []string
	// bad is a value that is neither a string nor a list of strings. It
	// reads as one entry no schedule parser accepts.
	bad bool
}

// ownSchedule reads a schedule. A bare string is a one-element list, as
// Renovate's config massaging makes it.
// https://github.com/renovatebot/renovate/blob/main/lib/config/massage.ts
func ownSchedule(cfg map[string]any) scheduleValue {
	v, has := cfg["schedule"]
	if !has {
		return scheduleValue{}
	}
	switch v := v.(type) {
	case string:
		return scheduleValue{set: true, entries: []string{v}}
	case []any:
		entries := make([]string, 0, len(v))
		for _, e := range v {
			s, ok := e.(string)
			if !ok {
				return scheduleValue{set: true, bad: true}
			}
			entries = append(entries, s)
		}
		return scheduleValue{set: true, entries: entries}
	}
	return scheduleValue{set: true, bad: true}
}

// overlay returns o when o says something, else v.
func (v scheduleValue) overlay(o scheduleValue) scheduleValue {
	if o.set {
		return o
	}
	return v
}

// ownSwitches reads separateMajorMinor, separateMinorPatch and
// separateMultipleMinor. A wrong type reads the way that fails the check:
// majors together for the first, a split for the other two.
func ownSwitches(cfg map[string]any) (majorMinor, minorPatch, multipleMinor tri) {
	var bad bool
	majorMinor, bad = ownTri(cfg, "separateMajorMinor")
	if bad {
		majorMinor = tri{set: true}
	}
	minorPatch, bad = ownTri(cfg, "separateMinorPatch")
	if bad {
		minorPatch = tri{set: true, value: true}
	}
	multipleMinor, bad = ownTri(cfg, "separateMultipleMinor")
	if bad {
		multipleMinor = tri{set: true, value: true}
	}
	return majorMinor, minorPatch, multipleMinor
}

// matchesEverything reports a matcher that every package satisfies, so the
// rule it sits in is not selective: a matchPackageNames of exactly "*" (what
// group:all uses) or a legacy matchPackagePatterns of ".*" or "*".
func matchesEverything(key string, v any) bool {
	var want []string
	switch key {
	case "matchPackageNames":
		want = []string{"*"}
	case "matchPackagePatterns":
		want = []string{".*", "*"}
	default:
		return false
	}
	var entries []any
	switch v := v.(type) {
	case string:
		entries = []any{v}
	case []any:
		entries = v
	default:
		return false
	}
	if len(entries) != 1 {
		return false
	}
	s, ok := entries[0].(string)
	if !ok {
		return false
	}
	return slices.Contains(want, s)
}

// batching decides whether the merged configuration batches non-major
// updates into one infrequent pull request, taking the first failure in this
// order. group is the schedule judged for the group (nil until the verdict
// reaches that step), lock the lock-file maintenance schedule judged (nil
// when it is off or unreadable).
//
// The group is the last rule with no selector beyond "every package" that
// sets a non-empty groupName and reaches at least one of minor, patch or
// digest: Renovate applies rules in order, so the last one decides the group
// name. A top-level groupName stands in for a rule that comes before all of
// them, because Renovate applies it to every dependency.
// https://github.com/renovatebot/renovate/blob/main/lib/util/package-rules/index.ts
func batching(l layered) (outcome string, group, lock []string) {
	var lockWindow window
	lockOK := true
	if l.lockFile.value {
		sched := scheduleValue{entries: defaultLockFileSchedule()}.overlay(l.lockSchedule)
		w, ok := parseSchedule(sched.entries)
		if sched.bad || !ok {
			lockOK = false
		} else {
			lockWindow, lock = w, sched.entries
		}
	}

	rules := l.rules
	// groupAt is the group rule's index, or -1 for a top-level groupName,
	// which sits before every rule.
	var g pkgRule
	groupAt, found := -1, l.group.nonEmpty
	if found {
		// A top-level groupName reaches every dependency and every update
		// type, and has no matchUpdateTypes of its own.
		g = pkgRule{updateTypes: allUpdateTypes, group: l.group}
	}
	for i, r := range rules {
		if !r.selective && r.group.nonEmpty && r.updateTypes != 0 {
			g, groupAt, found = r, i, true
		}
	}
	// A disabled group rule switches its updates off rather than batching them.
	if !found || (g.enabled.set && !g.enabled.value) {
		return batchNoGroup, nil, lock
	}
	if g.updateTypes != allUpdateTypes {
		if g.updateTypes == typeMinor|typePatch {
			return batchDigestsUngrouped, nil, lock
		}
		return batchNoGroup, nil, lock
	}

	// Whether a major shares the group's branch depends on separateMajorMinor
	// alone: while it is true (Renovate's default) a major is bucketed apart
	// and its group slug gets a "major-" prefix, whatever update types the
	// group rule lists. The switch can come from the top level, the group
	// rule or any rule that reaches non-major updates.
	// https://github.com/renovatebot/renovate/blob/main/lib/workers/repository/process/lookup/bucket.ts
	// https://github.com/renovatebot/renovate/blob/main/lib/workers/repository/updates/branch-name.ts
	if majorsShareBranch(l, g, rules) {
		return batchMajorsGrouped, nil, lock
	}

	if splits(l, rules, groupAt) {
		return batchSplitGroup, nil, lock
	}

	sched := l.schedule
	if g.schedule.set {
		sched = g.schedule
	}
	if sched.bad {
		return batchUnreadable, nil, lock
	}
	w, ok := parseSchedule(sched.entries)
	if !ok {
		return batchUnreadable, nil, lock
	}
	if sched.set {
		group = sched.entries
	}
	if v := w.judge(); v != batchOK {
		return v, group, lock
	}

	if l.lockFile.value {
		if !lockOK {
			return batchUnreadable, group, lock
		}
		if !lockWindow.within(w) {
			return batchLockFilesOffTimes, group, lock
		}
	}
	return batchOK, group, lock
}

// majorsShareBranch reports an effective separateMajorMinor of false.
func majorsShareBranch(l layered, g pkgRule, rules []pkgRule) bool {
	off := func(t tri) bool { return t.set && !t.value }
	if off(l.separateMajorMinor.overlay(g.separateMajorMinor)) {
		return true
	}
	for _, r := range rules {
		if r.updateTypes != 0 && off(r.separateMajorMinor) {
			return true
		}
	}
	return false
}

// splits reports a setting that breaks the batch into several pull requests:
// a rule after the group's that sets groupName (to anything, null included)
// for update types the batch covers, or a separateMinorPatch or
// separateMultipleMinor of true, at the top level or in any rule that reaches
// non-major updates, or a rule after the group's that sets its own schedule
// for them, which takes part of the group off the group's schedule. The
// switches add a branch-name prefix, so minors and patches do not share one
// branch.
// https://github.com/renovatebot/renovate/blob/main/lib/workers/repository/updates/branch-name.ts
func splits(l layered, rules []pkgRule, groupAt int) bool {
	if (l.separateMinorPatch.set && l.separateMinorPatch.value) ||
		(l.separateMultipleMinor.set && l.separateMultipleMinor.value) {
		return true
	}
	for i, r := range rules {
		if r.updateTypes != 0 &&
			((r.separateMinorPatch.set && r.separateMinorPatch.value) ||
				(r.separateMultipleMinor.set && r.separateMultipleMinor.value)) {
			return true
		}
		if i > groupAt && (r.group.set || r.schedule.set) && r.updateTypes != 0 {
			return true
		}
	}
	return false
}
