package renovate

import "strings"

// builtinLayer returns what a Renovate built-in preset says, for the few
// built-ins that set lockFileMaintenance.enabled or the automerge, approval
// and enablement of major updates. Built-ins live in Renovate's source rather
// than on GitHub, so they cannot be fetched; this fixed table is the only
// knowledge of them, and it models those keys alone: no schedule, no other
// setting. Every built-in not listed reads as setting nothing, and
// config:recommended and config:base are deliberately absent because they set
// none of these keys. The major-update presets are as defined in
// https://github.com/renovatebot/renovate/blob/main/lib/config/presets/internal/default.preset.ts
//
// A switch rather than a package-level map, so nothing can mutate the table.
func builtinLayer(ref string) (layered, bool) {
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
	switch name {
	case "default:maintainLockFilesWeekly",
		"default:maintainLockFilesMonthly",
		// config:best-practices extends :maintainLockFilesWeekly from Renovate
		// v43 on; an older Renovate does not, which this table cannot see.
		"config:best-practices",
		"config:semverAllWeekly",
		"config:semverAllMonthly":
		return layered{lockFile: flag{set: true, value: true}}, true
	case "default:maintainLockFilesDisabled":
		return layered{lockFile: flag{set: true}}, true
	case "default:automergeAll", "default:automergeMajor":
		// Both set a top-level automerge true.
		return layered{automerge: automergeLayer{automerge: yes}}, true
	case "default:automergeDisabled":
		return layered{automerge: automergeLayer{automerge: no}}, true
	case "default:dependencyDashboardApproval":
		return layered{automerge: automergeLayer{approval: yes}}, true
	case "default:approveMajorUpdates":
		// A packageRule for major with no other matcher.
		return layered{rules: []pkgRule{{appliesToMajor: true, approval: yes}}}, true
	case "default:disableMajorUpdates":
		return layered{automerge: automergeLayer{majorEnabled: no}}, true
	default:
		return layered{}, false
	}
}
