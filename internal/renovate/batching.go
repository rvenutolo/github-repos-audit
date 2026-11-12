package renovate

import (
	"slices"
	"strings"
)

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
	// templated is a {{template}}, which Renovate compiles per update, so
	// the slug derived from it can differ between the updates it groups.
	templated bool
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
	return groupName{set: true, nonEmpty: ok && s != "", templated: ok && strings.Contains(s, "{{")}
}

// textValue is what one rule or layer says about a string key that feeds a
// branch name (groupSlug, branchPrefix, additionalBranchPrefix, branchName).
// Every one of them supports templates, compiled per update.
type textValue struct {
	set bool
	// fixed is a string with no {{template}}, so it is the same for every
	// update it reaches. A null or any other type is not fixed: it reads as
	// a value that varies, which can only fail the check.
	fixed bool
	empty bool
}

// varies reports a value that can give the updates it reaches different
// branch names.
func (v textValue) varies() bool { return v.set && !v.fixed }

// overlay returns o when o says something, else v.
func (v textValue) overlay(o textValue) textValue {
	if o.set {
		return o
	}
	return v
}

func ownText(cfg map[string]any, key string) textValue {
	v, has := cfg[key]
	if !has {
		return textValue{}
	}
	s, ok := v.(string)
	return textValue{set: true, fixed: ok && !strings.Contains(s, "{{"), empty: ok && s == ""}
}

// ownGroupBranch reads the group object's branchName and branchTopic, which
// Renovate uses for a grouped update in place of the top-level ones; a group
// that is not an object is unreadable and reads as setting them.
// https://github.com/renovatebot/renovate/blob/main/lib/workers/repository/updates/branch-name.ts
func ownGroupBranch(cfg map[string]any) bool {
	v, has := cfg["group"]
	if !has {
		return false
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return true
	}
	_, name := obj["branchName"]
	_, topic := obj["branchTopic"]
	return name || topic
}

// branchKeys is what one rule or layer says about the keys besides the slug
// that make up a branch name: Renovate's default branchName is
// branchPrefix + additionalBranchPrefix + branchTopic, and a grouped
// update's branchTopic is the group object's, the slug by default.
// https://github.com/renovatebot/renovate/blob/main/lib/config/options/index.ts
type branchKeys struct {
	prefix, additional, name textValue
	// group is a group object that sets branchName or branchTopic. A
	// mergeable object merges field by field, so the layered value sticks
	// once any layer sets one.
	group bool
}

func ownBranchKeys(cfg map[string]any) branchKeys {
	return branchKeys{
		prefix:     ownText(cfg, "branchPrefix"),
		additional: ownText(cfg, "additionalBranchPrefix"),
		name:       ownText(cfg, "branchName"),
		group:      ownGroupBranch(cfg),
	}
}

func (b *branchKeys) over(o branchKeys) {
	b.prefix = b.prefix.overlay(o.prefix)
	b.additional = b.additional.overlay(o.additional)
	b.name = b.name.overlay(o.name)
	b.group = b.group || o.group
}

// any reports a key that is set at all: on a rule that reaches only some
// updates, any value gives them a branch of their own.
func (b branchKeys) any() bool {
	return b.prefix.set || b.additional.set || b.name.set || b.group
}

// varies reports a key that can give the updates it reaches different
// branch names: a templated prefix, or any branchName or group branch key,
// which replace the slug-based topic outright.
func (b branchKeys) varies() bool {
	return b.prefix.varies() || b.additional.varies() || b.name.set || b.group
}

// ownTypeObjects reads the minor, patch and digest objects, which Renovate
// merges into every update of that type before applying the package rules a
// second time. Any key in them that can change a branch name or schedule
// gives that update type its own pull request; one that is not an object is
// unreadable and reads the same way. separateMajorMinor in them is inert:
// branch naming reads it for major updates alone.
// https://github.com/renovatebot/renovate/blob/main/lib/workers/repository/updates/flatten.ts
func ownTypeObjects(cfg map[string]any) bool {
	for _, key := range []string{"minor", "patch", "digest"} {
		v, has := cfg[key]
		if !has {
			continue
		}
		obj, ok := v.(map[string]any)
		if !ok {
			return true
		}
		for _, k := range []string{"schedule", "groupName", "groupSlug", "extends", "packageRules"} {
			if _, has := obj[k]; has {
				return true
			}
		}
		if ownBranchKeys(obj).any() {
			return true
		}
		_, minorPatch, multipleMinor := ownSwitches(obj)
		if minorPatch.on() || multipleMinor.on() {
			return true
		}
	}
	return false
}

// ownMajorSwitch reads major.separateMajorMinor, merged into every major
// update. A wrong type, or a major that is not an object, reads as false, the
// value that fails the check.
func ownMajorSwitch(cfg map[string]any) tri {
	v, has := cfg["major"]
	if !has {
		return tri{}
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return tri{set: true}
	}
	t, bad := ownTri(obj, "separateMajorMinor")
	if bad {
		return tri{set: true}
	}
	return t
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
// when it is off, unreadable or a package rule may replace it). Neither is
// ever an empty list or holds an empty entry: audit.json publishes both.
//
// The group is the last rule with no selector beyond "every package" that
// sets a non-empty groupName and reaches at least one of minor, patch or
// digest: Renovate applies rules in order, so the last one decides the group
// name. A top-level groupName stands in for a rule that comes before all of
// them, because Renovate applies it to every dependency.
// https://github.com/renovatebot/renovate/blob/main/lib/util/package-rules/index.ts
func batching(l layered) (outcome string, group, lock []string) {
	lockWindows, lockOK, lock := lockFileSchedules(l)

	rules := l.rules
	// groupAt is the group rule's index, or -1 for a top-level groupName,
	// which sits before every rule.
	var g pkgRule
	groupAt, found := -1, l.group.nonEmpty
	if found {
		// A top-level groupName reaches every dependency and every update
		// type, majors included, and has no matchUpdateTypes of its own; the
		// top-level groupSlug is its own.
		g = pkgRule{appliesToMajor: true, updateTypes: allUpdateTypes, group: l.group, slug: l.slug}
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

	if splits(l, g, rules, groupAt) {
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
		group = recorded(sched.entries)
	}
	if v := w.judge(); v != batchOK {
		return v, group, lock
	}

	if l.lockFile.value {
		if !lockOK {
			return batchUnreadable, group, lock
		}
		for _, lw := range lockWindows {
			if !lw.within(w) {
				return batchLockFilesOffTimes, group, lock
			}
		}
	}
	return batchOK, group, lock
}

// lockFileSchedules returns every schedule lock-file maintenance can run on
// when it is on, whether all of them are readable, and the one to record.
// Renovate merges lockFileMaintenance over the top-level config and then
// applies the package rules again, so a rule that reaches lock-file
// maintenance and sets a schedule replaces lockFileMaintenance.schedule for
// the package files it matches; each such schedule is judged beside it. A
// rule there with its own extends, or its own lockFileMaintenance object,
// can set one this tool cannot see.
// https://github.com/renovatebot/renovate/blob/main/lib/workers/repository/updates/flatten.ts
func lockFileSchedules(l layered) (windows []window, ok bool, lock []string) {
	if !l.lockFile.value {
		return nil, true, nil
	}
	scheds := []scheduleValue{scheduleValue{entries: defaultLockFileSchedule()}.overlay(l.lockSchedule)}
	ok = true
	for _, r := range l.rules {
		if !r.reachesLockFiles {
			continue
		}
		if r.extends || r.lockObject {
			ok = false
		}
		if r.schedule.set {
			scheds = append(scheds, r.schedule)
		}
	}
	for _, s := range scheds {
		w, parsed := parseSchedule(s.entries)
		if s.bad || !parsed {
			ok = false
			continue
		}
		windows = append(windows, w)
	}
	if ok && len(scheds) == 1 {
		lock = recorded(scheds[0].entries)
	}
	return windows, ok, lock
}

// recorded is a schedule as audit.json publishes it: without empty entries,
// and nil when nothing is left. An empty list means "at any time" to
// Renovate, which the verdict already says; dropping the entries changes
// nothing that is judged.
func recorded(entries []string) []string {
	var out []string
	for _, e := range entries {
		if e != "" {
			out = append(out, e)
		}
	}
	return out
}

// majorsShareBranch reports an effective separateMajorMinor of false. Besides
// the top level and any rule that reaches non-major updates, a switch that
// applies to major updates alone (major.separateMajorMinor, or a rule that
// reaches majors) counts when the group reaches majors too: those majors
// then carry the group's name and an unprefixed slug, the group's branch.
func majorsShareBranch(l layered, g pkgRule, rules []pkgRule) bool {
	if l.separateMajorMinor.overlay(g.separateMajorMinor).off() {
		return true
	}
	if g.appliesToMajor && l.majorSeparateMajorMinor.off() {
		return true
	}
	for _, r := range rules {
		if r.updateTypes != 0 && r.separateMajorMinor.off() {
			return true
		}
		if g.appliesToMajor && r.appliesToMajor &&
			(r.separateMajorMinor.off() || r.majorSeparateMajorMinor.off()) {
			return true
		}
	}
	return false
}

// splits reports a setting that breaks the batch into several pull requests,
// either several branches or updates off the group's schedule: at the top
// level, a separateMinorPatch or separateMultipleMinor of true, a minor,
// patch or digest object that sets a branch or schedule key, or a branch key
// that varies by update; in any rule that reaches non-major updates, the
// same switches or objects; in any other such rule, its own schedule, its
// own extends (not expanded here, so it can set anything) or any branch key,
// or, after the group's rule, a groupName (to anything, null included) or
// groupSlug; and in the group's rule, a branch key that varies by update.
// Renovate names a grouped update's branch from groupSlug, else groupName,
// so the slug decides too (see slugSplits). The separate* switches add a
// prefix to the slug, so minors and patches do not share one branch.
// https://github.com/renovatebot/renovate/blob/main/lib/workers/repository/updates/branch-name.ts
func splits(l layered, g pkgRule, rules []pkgRule, groupAt int) bool {
	if l.separateMinorPatch.on() || l.separateMultipleMinor.on() ||
		l.typeObjects || l.branch.varies() {
		return true
	}
	if slugSplits(l, g, rules, groupAt) {
		return true
	}
	for i, r := range rules {
		if r.updateTypes == 0 {
			continue
		}
		if r.separateMinorPatch.on() || r.separateMultipleMinor.on() || r.typeObjects {
			return true
		}
		if i == groupAt {
			if r.branch.varies() {
				return true
			}
			continue
		}
		// A schedule on any other rule keeps those updates off the group's
		// schedule, wherever the rule sits: before the group rule it still
		// stands when the group rule sets none. A branch key likewise
		// stands, as the group rule never sets one for them.
		if r.schedule.set || r.extends || r.branch.any() ||
			(i > groupAt && (r.group.set || r.slug.set)) {
			return true
		}
	}
	return false
}

// slugSplits reports a group slug that is not the same for every update in
// the group. Renovate uses groupSlug when set, else groupName, compiling
// either as a template per update. The group's own slug must be a fixed,
// non-empty string; a null or empty one falls back to the name, read here
// as a split, a misread that can only fail. Without one, the group inherits
// any slug set before it (at the top level, or by an earlier rule over
// non-major updates, as group:allNonMajor's and group:recommended's are),
// which then names part of the group's branch, and a templated groupName
// varies per update.
// https://github.com/renovatebot/renovate/blob/main/lib/workers/repository/updates/branch-name.ts
func slugSplits(l layered, g pkgRule, rules []pkgRule, groupAt int) bool {
	if g.slug.set {
		return !g.slug.fixed || g.slug.empty
	}
	if g.group.templated {
		return true
	}
	if groupAt < 0 {
		return false
	}
	if l.slug.set {
		return true
	}
	for _, r := range rules[:groupAt] {
		if r.updateTypes != 0 && r.slug.set {
			return true
		}
	}
	return false
}
