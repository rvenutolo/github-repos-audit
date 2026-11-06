package audit

// Devshell is what a repository's shell scripts and its .ci/in-devshell
// wrapper say about running inside the Nix devShell and removing the wrapper's
// temp directory. It records the matched lines and decides nothing: whether a
// script self-wraps or the wrapper cleans up is internal/rules' decision. The
// zero value is a repository with no executable file and no wrapper.
type Devshell struct {
	// TooManyFiles is set when the executable files to read exceeded the
	// bound the builtins.getFlake read shares, so no script was read and
	// Scripts is empty. The wrapper is still read.
	TooManyFiles bool `json:"too_many_files,omitzero"`
	// Wrapper is the .ci/in-devshell file. Nil when the repository has none.
	Wrapper *DevshellWrapper `json:"wrapper,omitzero"`
	// Scripts is every executable shell script other than the wrapper, and
	// every executable file that could not be read in full, sorted by path.
	// A file that is not a shell script is not listed.
	Scripts []DevshellScript `json:"scripts,omitzero"`
}

// DevshellWrapper is the .ci/in-devshell file: the lines matching each marker
// of a wrapper that removes its temp directory.
type DevshellWrapper struct {
	// Present is always true. It keeps a wrapper that matches no marker
	// from serializing as an empty object, which would read as no wrapper.
	Present bool `json:"present"`
	// Wait is each non-comment line that waits for a background child.
	Wait []string `json:"wait,omitzero"`
	// Mktemp is each non-comment line that makes a temp directory.
	Mktemp []string `json:"mktemp,omitzero"`
	// KeepTmpdir is each non-comment line that keeps TMPDIR through the
	// devShell's environment filter.
	KeepTmpdir []string `json:"keep_tmpdir,omitzero"`
	// ExitTrap is each non-comment line that traps EXIT.
	ExitTrap []string `json:"exit_trap,omitzero"`
	// Unreadable is why the wrapper's text was not read in full, or that it
	// is not a regular file. When set, the line lists are absent.
	Unreadable string `json:"unreadable,omitzero"`
}

// DevshellScript is one executable shell script, or an executable file that
// could not be read in full and may be one.
type DevshellScript struct {
	// Path is the file's path in the repository.
	Path string `json:"path"`
	// InDevshellTest is each non-comment line that tests IN_DEVSHELL, in
	// file order, trimmed.
	InDevshellTest []string `json:"in_devshell_test,omitzero"`
	// ExecInDevshell is each non-comment line that execs the wrapper, in
	// file order, trimmed.
	ExecInDevshell []string `json:"exec_in_devshell,omitzero"`
	// Exempt is the reason text of each devshell-exempt marker comment. A
	// reason may be empty, which is a marker without a reason.
	Exempt []string `json:"exempt,omitzero"`
	// Unreadable is why the file's text was not read in full. When set, the
	// other lists are absent.
	Unreadable string `json:"unreadable,omitzero"`
}
