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
