package audit

// GoPaths is what a Go repository's Nix files say about GOPATH and
// GOMODCACHE, the two variables that decide where its devShell's `go` keeps
// modules. Without them Go falls back to ~/go. The zero value is a
// repository that is not a Go flake (no root go.mod or no flake.nix), whose
// files are not searched. Whether the lines found are the expected ones is
// internal/rules' decision.
type GoPaths struct {
	// TooManyFiles is set when the files to read exceeded the bound the
	// builtins.getFlake read shares, so none was read and Files is empty.
	TooManyFiles bool `json:"too_many_files,omitzero"`
	// Files is every tracked *.nix file that assigns either variable or
	// could not be read in full, sorted by path. A file that does neither
	// is not listed.
	Files []GoPathsFile `json:"files,omitzero"`
}

// GoPathsFile is one Nix file that assigns GOPATH or GOMODCACHE, or could
// not be read.
type GoPathsFile struct {
	// Path is the file's path in the repository.
	Path string `json:"path"`
	// Lines is each line assigning either variable, in file order, with
	// leading and trailing whitespace removed.
	Lines []string `json:"lines,omitzero"`
	// Unreadable is why the file's text was not read in full. When set,
	// Lines is absent.
	Unreadable string `json:"unreadable,omitzero"`
}
