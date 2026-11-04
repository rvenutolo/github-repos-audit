package github

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
	"github.com/rvenutolo/github-repos-audit/internal/filepaths"
)

// goFlake is the Files of a repository with a root go.mod and a flake.nix,
// the only kind whose Nix files are searched.
var goFlake = audit.Files{GoMod: true, FlakeNix: true}

// goPathsServer is a claudeServer for the GoPathsFiles operation.
func goPathsServer(blobs map[string]*fileBlob) *claudeServer {
	return &claudeServer{op: "GoPathsFiles", blobs: blobs}
}

// TestGoPathsFacts_notAGoFlakeNoQuery: without both a root go.mod and a
// flake.nix there is no Go devShell, so nothing is read or recorded.
func TestGoPathsFacts_notAGoFlakeNoQuery(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		files audit.Files
	}{
		{"neither", audit.Files{}},
		{"go.mod only", audit.Files{GoMod: true}},
		{"flake.nix only", audit.Files{FlakeNix: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := goPathsServer(nil)
			got, err := newClaudeClient(t, s).goPathsFacts(t.Context(), "web-app", files("flake.nix", "nix/shell.nix"), tc.files)
			if err != nil {
				t.Fatalf("goPathsFacts() error = %v, want nil", err)
			}
			if diff := cmp.Diff(audit.GoPaths{}, got); diff != "" {
				t.Errorf("goPathsFacts() mismatch (-want +got):\n%s", diff)
			}
			if q := s.asked(); len(q) != 0 {
				t.Errorf("sent %d queries, want none", len(q))
			}
		})
	}
}

// TestGoPathsFacts_recordsAssignments: every tracked Nix file at any depth is
// read, never a symlink, a directory or a file that is not Nix, and each line
// assigning either variable is recorded trimmed, in file order and whatever
// its value. A file that assigns neither, and a name that merely ends in
// GOPATH, are not recorded.
func TestGoPathsFacts_recordsAssignments(t *testing.T) {
	t.Parallel()

	entries := []filepaths.Entry{
		{Path: "nix/shell.nix", Mode: "100644"},
		{Path: "flake.nix", Mode: "100644"},
		{Path: "nix/link.nix", Mode: "120000"},
		{Path: "nix", Mode: "040000", Dir: true},
		{Path: ".envrc", Mode: "100644"},
		{Path: "default.nix", Mode: "100644"},
	}
	s := goPathsServer(map[string]*fileBlob{
		"flake.nix": text("{\n  env.GOPATH = \"/x\";\n  MY_GOPATH=1\n}\n"),
		"nix/shell.nix": text("''\n" +
			"    export GOPATH=\"$HOME/.cache/go\"\n" +
			"    echo between\n" +
			"\texport GOMODCACHE=\"$HOME/.cache/go/mod\"  \n" +
			"''\n"),
		"default.nix": text("{ }\n"),
	})
	got, err := newClaudeClient(t, s).goPathsFacts(t.Context(), "web-app", entries, goFlake)
	if err != nil {
		t.Fatalf("goPathsFacts() error = %v, want nil", err)
	}
	want := audit.GoPaths{Files: []audit.GoPathsFile{
		{Path: "flake.nix", Lines: []string{`env.GOPATH = "/x";`}},
		{Path: "nix/shell.nix", Lines: []string{`export GOPATH="$HOME/.cache/go"`, `export GOMODCACHE="$HOME/.cache/go/mod"`}},
	}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("goPathsFacts() mismatch (-want +got):\n%s", diff)
	}
	asked := s.asked()
	if len(asked) != 1 {
		t.Fatalf("sent %d queries, want 1", len(asked))
	}
	if diff := cmp.Diff([]string{"default.nix", "flake.nix", "nix/shell.nix"}, asked[0]); diff != "" {
		t.Errorf("query paths mismatch (-want +got):\n%s", diff)
	}
}

// TestGoPathsFacts_unreadableAndBinary: a binary file sets nothing and is
// skipped; a file cut short, or one the query found nothing at, may set
// either variable in the part not read, so it is recorded as unreadable.
func TestGoPathsFacts_unreadableAndBinary(t *testing.T) {
	t.Parallel()

	s := goPathsServer(map[string]*fileBlob{
		"flake.nix": {IsBinary: true},
		"big.nix":   {Text: new("export GOPATH=x\n"), IsTruncated: true},
	})
	got, err := newClaudeClient(t, s).goPathsFacts(t.Context(), "web-app", files("flake.nix", "big.nix", "gone.nix"), goFlake)
	if err != nil {
		t.Fatalf("goPathsFacts() error = %v, want nil", err)
	}
	want := audit.GoPaths{Files: []audit.GoPathsFile{
		{Path: "big.nix", Unreadable: "big.nix: too large to read in full"},
		{Path: "gone.nix", Unreadable: "gone.nix: not a text file"},
	}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("goPathsFacts() mismatch (-want +got):\n%s", diff)
	}
}

// TestGoPathsFacts_tooManyFiles: past the bound nothing is read, and the
// repository is recorded as having too many files rather than none set.
func TestGoPathsFacts_tooManyFiles(t *testing.T) {
	t.Parallel()

	paths := make([]string, 0, getFlakeFilesMax+1)
	for i := range getFlakeFilesMax + 1 {
		paths = append(paths, fmt.Sprintf("nix/f%03d.nix", i))
	}
	s := goPathsServer(nil)
	got, err := newClaudeClient(t, s).goPathsFacts(t.Context(), "web-app", files(paths...), goFlake)
	if err != nil {
		t.Fatalf("goPathsFacts() error = %v, want nil", err)
	}
	if diff := cmp.Diff(audit.GoPaths{TooManyFiles: true}, got); diff != "" {
		t.Errorf("goPathsFacts() mismatch (-want +got):\n%s", diff)
	}
	if q := s.asked(); len(q) != 0 {
		t.Errorf("sent %d queries, want none", len(q))
	}
}

// TestGoPathsFacts_queryFailureFails: a query that fails fails the run
// rather than recording the files as unreadable.
func TestGoPathsFacts_queryFailureFails(t *testing.T) {
	t.Parallel()

	s := &claudeServer{op: "GoPathsFiles", body: `{"errors": [{"type": "NOT_FOUND", "message": "gone"}]}`}
	_, err := newClaudeClient(t, s).goPathsFacts(t.Context(), "web-app", files("flake.nix"), goFlake)
	if err == nil || !strings.Contains(err.Error(), "gone") {
		t.Errorf("goPathsFacts() error = %v, want one containing %q", err, "gone")
	}
}
