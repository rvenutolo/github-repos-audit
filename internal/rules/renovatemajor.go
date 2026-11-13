package rules

import "github.com/rvenutolo/github-repos-audit/internal/audit"

// observeAutomergeMajor judges whether Renovate lands every major update on
// its own. The collector decides that from Renovate's evaluation order; here
// it is only read, so a configuration that could not be resolved reads as
// held and says why in the cell, as the minimum release age cell does.
//
// Without a Renovate configuration there is nothing to judge, and the
// renovate row already carries that gap.
func observeAutomergeMajor(r audit.Repo) observation {
	switch {
	case r.Files.RenovateConfig == "":
		return observation{unknown: true}
	case r.Renovate.AutomergeMajor:
		return observation{ok: true}
	case r.Renovate.MinReleaseAgeError != "":
		return observation{value: releaseAgeUnresolved, mark: true}
	}
	return observation{}
}

// observeRenovateBatching judges whether Renovate batches non-major updates
// into one pull request opened at most weekly, each pull request costing CI
// minutes and a notification email. The collector decides that from the
// merged configuration; here the verdict is only read, so a configuration
// that could not be resolved reads as failing and says why, as the automerge
// cell does.
//
// Without a Renovate configuration there is nothing to judge, and the
// renovate row already carries that gap.
func observeRenovateBatching(r audit.Repo) observation {
	switch {
	case r.Files.RenovateConfig == "":
		return observation{unknown: true}
	case r.Renovate.Batching == "ok":
		return observation{ok: true, mark: true}
	case r.Renovate.Batching == "" && r.Renovate.MinReleaseAgeError != "":
		return observation{value: releaseAgeUnresolved, mark: true}
	}
	return observation{value: batchingCells[r.Renovate.Batching], mark: true}
}

// batchingCells is the text each failing batching verdict shows.
//
//nolint:gochecknoglobals // immutable lookup table
var batchingCells = map[string]string{
	"no_group":                "no group",
	"digests_ungrouped":       "digests ungrouped",
	"majors_grouped":          "majors grouped",
	"split_group":             "split group",
	"no_schedule":             "no schedule",
	"too_often":               "too often",
	"unreadable_schedule":     "unreadable schedule",
	"lock_files_off_schedule": "lock files off-schedule",
}
