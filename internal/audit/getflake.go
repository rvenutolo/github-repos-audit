package audit

// NixGetFlake is what a repository's files say about builtins.getFlake, the
// call whose argument decides whether Nix reads a flake through git or from
// the working tree. The zero value is a repository with no Nix file. Whether
// any call is acceptable is internal/rules' decision.
type NixGetFlake struct {
	// NixFiles is set when the tree tracks at least one *.nix file, at any
	// depth. Without one there is nothing for the check to judge.
	NixFiles bool `json:"nix_files,omitzero"`
	// TooManyFiles is set when the files to read exceeded the bound that
	// keeps a run's queries affordable. Files is then empty: nothing was read.
	TooManyFiles bool `json:"too_many_files,omitzero"`
	// Files is every file that holds a call or could not be read, sorted by
	// path. A file with no call is not listed.
	Files []GetFlakeFile `json:"files,omitzero"`
}

// GetFlakeFile is one file that holds builtins.getFlake calls or could not be
// read.
type GetFlakeFile struct {
	// Path is the file's path in the repository.
	Path string `json:"path"`
	// Args is the argument of each call, in file order, as internal/getflake
	// normalizes it.
	Args []string `json:"args,omitzero"`
	// Unreadable is why the file's text was not read in full. When set, Args
	// is absent.
	Unreadable string `json:"unreadable,omitzero"`
}
