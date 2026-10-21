package audit

import "time"

// Flake is what a repository's flake.nix and flake.lock say, as far as the
// checks need it. The zero value is a repository with neither file. Whether
// the inputs name the right flake or the lock is fresh enough is
// internal/rules' decision.
type Flake struct {
	// Inputs is every `url = "…"` flake.nix binds, as written, sorted and
	// without repeats. flake.nix is read as text, not evaluated.
	Inputs []string `json:"inputs,omitzero"`
	// HasLock reports that flake.lock exists.
	HasLock bool `json:"has_lock,omitzero"`
	// LockedInputs is "github:<owner>/<repo>", lower-cased, for every
	// GitHub-type node of flake.lock, taken from the node's `original`,
	// sorted and without repeats.
	LockedInputs []string `json:"locked_inputs,omitzero"`
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
	// NamesLint reports that flake.nix's text contains the configured
	// hermetic-lint command name.
	NamesLint bool `json:"names_lint,omitzero"`
	// GateFiles is the gate files whose text contains the configured
	// hermetic-lint command name, by path.
	GateFiles []string `json:"gate_files,omitzero"`
}
