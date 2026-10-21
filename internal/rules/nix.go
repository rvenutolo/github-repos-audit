package rules

import (
	"slices"
	"strings"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
)

// observeHermetic judges the hermetic-lint contract: flake.nix declares the
// configured flake as an input, flake.lock has a node for it, flake.nix names
// the lint (a stand-in for the default devShell carrying it, since flake.nix
// is read as text and never evaluated), and a gate file names the lint too.
// The gate-file part shows the wiring exists, not that the call is reachable:
// gates reach their sub-runners indirectly, and following that is more than a
// text search can promise.
//
// A pass is a plain tick, as there is nothing more to say about it. A fail
// names every missing part, in the contract's order, so the cell is the fix
// list. A repository with no flake.nix is n/a: there is no Nix to be hermetic
// about, and flake_nix already reports the missing file.
//
// An unreadable flake.nix records no inputs and no lint, so it reads as "no
// input, …" here; the collector does not tell it apart from a silent one.
func observeHermetic(r audit.Repo, std audit.HermeticStandard) observation {
	if !r.Files.FlakeNix {
		return observation{unknown: true}
	}
	var missing []string
	if !slices.Contains(r.Flake.Inputs, std.Flake) {
		missing = append(missing, "no input")
	}
	if ref := lockedRef(std.Flake); ref == "" || !slices.Contains(r.Flake.LockedInputs, ref) {
		missing = append(missing, "not locked")
	}
	if !r.Flake.NamesLint {
		missing = append(missing, "not in devShell")
	}
	if len(r.Flake.GateFiles) == 0 {
		missing = append(missing, "not in gate")
	}
	if len(missing) == 0 {
		return observation{ok: true}
	}
	return observation{value: strings.Join(missing, ", "), mark: true}
}

// lockedRef is the form audit.Flake.LockedInputs records a flake reference
// in: "github:<owner>/<repo>", lower-cased, without the ref, directory or
// query flake.nix may add after it. flake.lock spells the owner and the
// repository as GitHub answered them, which need not be the case flake.nix
// wrote, and GitHub matches both without regard to case.
//
// Only github: references have that form. Any other kind returns "", and
// observeHermetic counts such a flake as never locked: the collector records
// GitHub-type nodes alone, and a check that cannot see the node says so
// rather than passing.
func lockedRef(flake string) string {
	const scheme = "github:"
	if len(flake) < len(scheme) || !strings.EqualFold(flake[:len(scheme)], scheme) {
		return ""
	}
	rest := flake[len(scheme):]
	if i := strings.IndexAny(rest, "?#"); i >= 0 {
		rest = rest[:i]
	}
	owner, repo, ok := strings.Cut(rest, "/")
	if !ok || owner == "" {
		return ""
	}
	repo, _, _ = strings.Cut(repo, "/")
	if repo == "" {
		return ""
	}
	return strings.ToLower(scheme + owner + "/" + repo)
}
