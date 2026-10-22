package renovate

import "strings"

// builtinLayer returns what a Renovate built-in preset says, for the few
// built-ins that set lockFileMaintenance.enabled. Built-ins live in Renovate's
// source rather than on GitHub, so they cannot be fetched; this fixed table is
// the only knowledge of them, and it models that one key alone — no schedule,
// no other setting. Every built-in not listed reads as setting nothing, and
// config:recommended and config:base are deliberately absent because they set
// no lock-file maintenance.
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
	var enabled bool
	switch name {
	case "default:maintainLockFilesWeekly",
		"default:maintainLockFilesMonthly",
		// config:best-practices extends :maintainLockFilesWeekly from Renovate
		// v43 on; an older Renovate does not, which this table cannot see.
		"config:best-practices",
		"config:semverAllWeekly",
		"config:semverAllMonthly":
		enabled = true
	case "default:maintainLockFilesDisabled":
		enabled = false
	default:
		return layered{}, false
	}
	return layered{lockFile: flag{set: true, value: enabled}}, true
}
