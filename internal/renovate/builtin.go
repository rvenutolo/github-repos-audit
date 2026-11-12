package renovate

import "strings"

// builtinLayer returns what a Renovate built-in preset says, for the few
// built-ins that set lockFileMaintenance.enabled or its schedule, the
// automerge, approval and enablement of major updates, or the grouping and
// schedule that decide batching (groupName, groupSlug, separateMajorMinor and
// schedule).
// Built-ins live in Renovate's source rather than on GitHub, so they cannot
// be fetched; this fixed table is the only knowledge of them, and it models
// those keys alone. Every built-in not listed reads as setting nothing, except
// that any group:* preset not listed, config:recommended and config:base read
// as one narrowed group rule (see narrowedGroups), since each sets a groupName
// for some packages, and so does :group (see argGroup).
// The major-update, lock-file, separate* and :group presets are as defined in
// https://github.com/renovatebot/renovate/blob/main/lib/config/presets/internal/default.preset.ts
// the schedule presets in
// https://github.com/renovatebot/renovate/blob/main/lib/config/presets/internal/schedule.preset.ts
// and group:*, config:recommended and the config:semverAll presets in
// https://github.com/renovatebot/renovate/blob/main/lib/config/presets/internal/group.preset.ts
// https://github.com/renovatebot/renovate/blob/main/lib/config/presets/internal/config.preset.ts
//
// A switch rather than a package-level map, so nothing can mutate the table.
func builtinLayer(ref string) layered {
	name := ref
	// Arguments parameterise a preset's contents, not which preset it is.
	if i := strings.IndexByte(name, '('); i >= 0 {
		name = name[:i]
	}
	// ":x" is shorthand for "default:x".
	if strings.HasPrefix(name, ":") {
		name = "default" + name
	}
	yes, no := tri{set: true, value: true}, tri{set: true}
	named := groupName{set: true, nonEmpty: true}
	slugged := textValue{set: true, fixed: true}
	switch name {
	case "default:maintainLockFilesWeekly":
		return lockFilesOn(earlyMondaysCron)
	case "config:best-practices":
		// It extends config:recommended and, from Renovate v43 on,
		// :maintainLockFilesWeekly; an older Renovate does not extend the
		// latter, which this table cannot see.
		return stacked("config:recommended", "default:maintainLockFilesWeekly")
	case "config:recommended", "config:base", "group:monorepos", "group:recommended":
		return narrowedGroups()
	case "default:separatePatchReleases", "default:automergePatch":
		// automergePatch sets separateMinorPatch beside its patch automerge.
		return layered{separateMinorPatch: yes}
	case "default:combinePatchMinorReleases":
		return layered{separateMinorPatch: no}
	case "default:separateMultipleMinorReleases":
		return layered{separateMultipleMinor: yes}
	case "default:separateMajorReleases", "default:separateMultipleMajorReleases":
		return layered{separateMajorMinor: yes}
	case "group:allDigest":
		return layered{rules: []pkgRule{{
			narrowed: true, typed: true,
			updateTypes: typeDigest, group: named, slug: slugged,
		}}}
	case "default:maintainLockFilesMonthly":
		return lockFilesOn(monthlyCron)
	case "config:semverAllWeekly":
		// It extends group:all, schedule:weekly and :maintainLockFilesWeekly
		// and sets separateMajorMinor false itself.
		return stacked("group:all", "schedule:weekly", "default:maintainLockFilesWeekly", "config:separateMajorMinorOff")
	case "config:semverAllMonthly":
		return stacked("group:all", "schedule:monthly", "default:maintainLockFilesMonthly", "config:separateMajorMinorOff")
	case "config:separateMajorMinorOff":
		// Not a Renovate preset: the switch the semverAll presets set beside
		// their extends, named here so stacked can apply it. A config can
		// also extend this name, which Renovate would reject as an unknown
		// preset; reading it as the switch only makes the check fail.
		return layered{separateMajorMinor: no}
	case "group:all":
		// A top-level groupName and groupSlug and the same group as a
		// packageRule over every package with no update types, plus
		// separateMajorMinor false: majors share the group's branch.
		return layered{
			group:              named,
			slug:               slugged,
			separateMajorMinor: no,
			rules: []pkgRule{{
				appliesToMajor: true, narrowed: true, reachesLockFiles: true,
				updateTypes: allUpdateTypes, group: named, slug: slugged,
			}},
		}
	case "group:allNonMajor":
		// Minor and patch only: digests stay ungrouped.
		return layered{rules: []pkgRule{{
			narrowed: true, typed: true,
			updateTypes: typeMinor | typePatch, group: named, slug: slugged,
		}}}
	case "schedule:daily":
		return scheduled("* 0-3 * * *")
	case "schedule:weekly", "schedule:earlyMondays":
		return scheduled(earlyMondaysCron)
	case "schedule:weekends":
		return scheduled("* * * * 0,6")
	case "schedule:weekdays":
		return scheduled("* * * * 1-5")
	case "schedule:monthly":
		return scheduled(monthlyCron)
	case "schedule:quarterly":
		return scheduled("* * 1 */3 *")
	case "schedule:nonOfficeHours":
		return scheduled("* 0-4,22-23 * * 1-5", "* * * * 0,6")
	case "schedule:officeHours":
		return scheduled("* 8-17 * * 1-5")
	case "schedule:yearly":
		return scheduled("* * 1 */12 *")
	case "default:group":
		return argGroup(ref)
	case "default:maintainLockFilesDisabled":
		return layered{lockFile: flag{set: true}}
	case "default:automergeAll", "default:automergeMajor":
		// Both set a top-level automerge true.
		return layered{automerge: automergeLayer{automerge: yes}}
	case "default:automergeDisabled":
		return layered{automerge: automergeLayer{automerge: no}}
	case "default:dependencyDashboardApproval":
		return layered{automerge: automergeLayer{approval: yes}}
	case "default:approveMajorUpdates":
		// A packageRule for major with no other matcher.
		return layered{rules: []pkgRule{{appliesToMajor: true, approval: yes}}}
	case "default:disableMajorUpdates":
		return layered{automerge: automergeLayer{majorEnabled: no}}
	case "default:disableRenovate":
		return layered{automerge: automergeLayer{enabled: no}}
	case "default:enableRenovate":
		return layered{automerge: automergeLayer{enabled: yes}}
	default:
		// Every other group:* preset sets a groupName for some packages, so
		// it reads as a narrowed group: a catch-all group before it is split.
		if strings.HasPrefix(name, "group:") {
			return narrowedGroups()
		}
		return layered{}
	}
}

// The cron strings schedule:earlyMondays (which schedule:weekly extends) and
// schedule:monthly set.
const (
	earlyMondaysCron = "* 0-3 * * 1"
	monthlyCron      = "* 0-3 1 * *"
)

// scheduled is a layer whose top-level schedule is the given entries.
func scheduled(entries ...string) layered {
	return layered{schedule: scheduleValue{set: true, entries: entries}}
}

// lockFilesOn is a layer that turns lock-file maintenance on with the given
// schedule, which :maintainLockFiles* set through lockFileMaintenance.extends.
func lockFilesOn(entries ...string) layered {
	return layered{
		lockFile:     flag{set: true, value: true},
		lockSchedule: scheduleValue{set: true, entries: entries},
	}
}

// stacked lays the named built-ins over one another in order, as a preset's
// extends list does.
func stacked(refs ...string) layered {
	var out layered
	for _, ref := range refs {
		l := builtinLayer(ref)
		out.over(l)
	}
	return out
}

// narrowedGroups is what the group rules inside group:monorepos,
// group:recommended and config:recommended amount to: rules that set a
// groupName for some packages and every update type, and a groupSlug, as
// several of group:recommended's do (group:symfony, group:pulumi and
// others). None is a catch-all, but a catch-all group placed before them in
// an extends list is split by them, and one in the repository's own file
// (after every preset) is split by the slug it inherits unless it sets its
// own groupSlug.
func narrowedGroups() layered {
	return layered{rules: []pkgRule{{
		appliesToMajor: true, narrowed: true, selective: true, reachesLockFiles: true,
		updateTypes: allUpdateTypes, group: groupName{set: true, nonEmpty: true},
		slug: textValue{set: true, fixed: true},
	}}}
}

// argGroup is :group(preset, name): one rule that extends the preset named
// by its first argument and sets groupName to its second. A monorepo:* or
// packages:* preset holds only package matchers, so the rule is a narrowed
// group; any other preset is not expanded and can set anything, so the rule
// reads as having its own extends, which splits a batch.
// https://github.com/renovatebot/renovate/blob/main/lib/config/presets/internal/monorepos.preset.ts
// https://github.com/renovatebot/renovate/blob/main/lib/config/presets/internal/packages.preset.ts
func argGroup(ref string) layered {
	var arg0 string
	if open, end := strings.IndexByte(ref, '('), strings.LastIndexByte(ref, ')'); open >= 0 && end > open {
		arg0, _, _ = strings.Cut(ref[open+1:end], ",")
		arg0 = strings.TrimSpace(arg0)
	}
	l := narrowedGroups()
	l.rules[0].slug = textValue{}
	l.rules[0].extends = !strings.HasPrefix(arg0, "monorepo:") && !strings.HasPrefix(arg0, "packages:")
	return l
}
