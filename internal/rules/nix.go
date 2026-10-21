package rules

import (
	"slices"
	"strings"
	"time"

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

// maxLockAge is how old the nixpkgs lock may be before nix_lock_age fails.
// nixpkgs channels move daily and carry security fixes, so a month without a
// refresh means a month of fixes the flake does not have; the age is measured
// to the second, so exactly 30 days still passes.
const maxLockAge = 30 * 24 * time.Hour

// observeLockAge judges how long ago the flake's nixpkgs was locked, against
// now, the snapshot's instant, so the same audit.json always reads the same
// way. The date is the lock's `lastModified` for the node the root's nixpkgs
// input resolves to; the cell carries it and the renderer shows its age
// beside the mark, so a stale lock reads as how stale.
//
// There is nothing to date without a flake.lock, or in a lock with no
// nixpkgs input, so both are n/a. A lock the collector could not read fails
// as unreadable: it recorded no date, and passing on a missing date would
// hide exactly the lock that most needs looking at.
func observeLockAge(r audit.Repo, now time.Time) observation {
	switch {
	case !r.Flake.HasLock:
		return observation{unknown: true}
	case r.Flake.LockError != "":
		return observation{value: "unreadable flake.lock", mark: true}
	case r.Flake.NixpkgsLastModified.IsZero():
		return observation{unknown: true}
	}
	at := r.Flake.NixpkgsLastModified
	return observation{at: at, ok: now.Sub(at) <= maxLockAge, mark: true, scalar: true}
}

// updateFlakeLockAction is the published action that opens a pull request
// refreshing flake.lock, matched up to the "@" that pins its version.
const updateFlakeLockAction = "DeterminateSystems/update-flake-lock@"

// observeLockRefresh judges whether something refreshes flake.lock without a
// person remembering to: Renovate, or a scheduled workflow. The cell names
// each source that does, so a pass says which one to look at.
//
// Renovate counts only with a configuration, its nix manager running, and
// either lock-file maintenance or every input pinned by rev. The manager
// alone updates a rev-pinned input's digest, but opens nothing for an input
// that follows a branch; lock-file maintenance rewrites lock files only for
// the managers that run, so without the nix manager it never touches
// flake.lock.
//
// A workflow counts only when it runs on a schedule: one run by hand or on a
// push refreshes the lock no more often than someone thinks to. Its job must
// run `nix flake update`, use the update-flake-lock action, or use a local
// composite that runs `nix flake update`.
//
// With no source found, every workflow that could not be read, and every
// unreadable composite a scheduled workflow uses, is named: any of them
// might be the refresh the cell cannot see, so it fails saying where to look
// rather than bare. n/a without a flake.lock, as there is nothing to refresh.
func observeLockRefresh(r audit.Repo) observation {
	if !r.Flake.HasLock {
		return observation{unknown: true}
	}
	var sources []string
	if r.Files.RenovateConfig != "" && r.Renovate.NixManager &&
		(r.Renovate.LockFileMaintenance || r.Flake.AllRevPinned) {
		sources = append(sources, "Renovate")
	}
	refreshes, unreadable := scheduledFlakeUpdate(r)
	if refreshes {
		sources = append(sources, "workflow")
	}
	if len(sources) > 0 {
		return observation{ok: true, value: strings.Join(sources, ", "), mark: true}
	}
	if len(unreadable) > 0 {
		return observation{value: strings.Join(unreadable, ", "), mark: true}
	}
	return observation{}
}

// scheduledFlakeUpdate reports whether a scheduled workflow refreshes
// flake.lock (see observeLockRefresh), and, as "unreadable <path>", every
// workflow it could not read and every composite a scheduled job uses that it
// could not read, in the order it met them.
func scheduledFlakeUpdate(r audit.Repo) (bool, []string) {
	var unreadable []string
	for _, w := range r.Workflows {
		if w.ParseError != "" {
			unreadable = append(unreadable, "unreadable "+w.Path)
			continue
		}
		if !slices.Contains(w.Events, "schedule") {
			continue
		}
		for _, j := range w.Jobs {
			if j.FlakeUpdate {
				return true, nil
			}
			for _, u := range j.Uses {
				if strings.HasPrefix(u.Action, updateFlakeLockAction) {
					return true, nil
				}
				ca, local := resolveComposite(r, u.Action)
				switch {
				case !local:
				case ca.ParseError != "":
					unreadable = append(unreadable, "unreadable "+ca.Path)
				case ca.FlakeUpdate:
					return true, nil
				}
			}
		}
	}
	return false, unreadable
}

// resolveComposite finds the composite action a step's `uses:` names, when
// it names one in this repository. "./x" and "$/x" resolve alike, both
// against the repository root, to the directory x, whose action.yml (or,
// failing that, action.yaml) the collector recorded by its repository path,
// such as ".github/actions/setup/action.yml". Anything
// else, a published action or a Docker image, is not local, and a local path
// with no recorded composite is not found: both report false.
func resolveComposite(r audit.Repo, action string) (audit.CompositeAction, bool) {
	dir, ok := strings.CutPrefix(action, "./")
	if !ok {
		dir, ok = strings.CutPrefix(action, "$/")
	}
	if !ok {
		return audit.CompositeAction{}, false
	}
	for _, name := range []string{"action.yml", "action.yaml"} {
		i := slices.IndexFunc(r.Actions, func(a audit.CompositeAction) bool { return a.Path == dir+"/"+name })
		if i >= 0 {
			return r.Actions[i], true
		}
	}
	return audit.CompositeAction{}, false
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
