package audit

import "time"

// Flake is what a repository's flake.lock says, as far as the checks need it.
// The zero value is a repository without a flake.lock. Files.FlakeNix, not
// this, says whether flake.nix exists. Whether the lock is fresh enough is
// internal/rules' decision.
type Flake struct {
	// HasLock reports that flake.lock exists.
	HasLock bool `json:"has_lock,omitzero"`
	// NixpkgsLastModified is the `locked.lastModified` of the node the
	// root's `nixpkgs` input resolves to, following `follows`, in UTC. Zero
	// when there is no lock or no input named nixpkgs.
	NixpkgsLastModified time.Time `json:"nixpkgs_last_modified,omitzero"`
	// AllRevPinned reports that every non-root node's `original` carries
	// `rev`. False for a lock with no inputs.
	AllRevPinned bool `json:"all_rev_pinned,omitzero"`
	// LockError is why flake.lock could not be read. When it is set none of
	// the lock facts above are, so a check reading the lock fails as
	// unreadable rather than passing on what it did manage to read.
	LockError string `json:"lock_error,omitzero"`
}
