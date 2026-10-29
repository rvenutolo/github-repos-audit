package github

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
	"github.com/rvenutolo/github-repos-audit/internal/filepaths"
)

// getFlakeServer is a claudeServer for the GetFlakeFiles operation.
func getFlakeServer(blobs map[string]*fileBlob) *claudeServer {
	return &claudeServer{op: "GetFlakeFiles", blobs: blobs}
}

// TestGetFlakeFacts_noNixNoQuery: without a Nix file the call has no
// meaning, so a .ci/ file alone costs no query and records nothing.
func TestGetFlakeFacts_noNixNoQuery(t *testing.T) {
	t.Parallel()

	s := getFlakeServer(nil)
	got, err := newClaudeClient(t, s).getFlakeFacts(t.Context(), "web-app", files(".ci/x", "README.md"), nil)
	if err != nil {
		t.Fatalf("getFlakeFacts() error = %v, want nil", err)
	}
	if diff := cmp.Diff(audit.NixGetFlake{}, got); diff != "" {
		t.Errorf("getFlakeFacts() mismatch (-want +got):\n%s", diff)
	}
	if q := s.asked(); len(q) != 0 {
		t.Errorf("sent %d queries, want none", len(q))
	}
}

// TestGetFlakeFacts_nestedNixApplies: a Nix file at any depth makes the
// check apply, even when it holds no call.
func TestGetFlakeFacts_nestedNixApplies(t *testing.T) {
	t.Parallel()

	s := getFlakeServer(map[string]*fileBlob{"nix/pkg.nix": text("{ }\n")})
	got, err := newClaudeClient(t, s).getFlakeFacts(t.Context(), "web-app", files("nix/pkg.nix"), nil)
	if err != nil {
		t.Fatalf("getFlakeFacts() error = %v, want nil", err)
	}
	if diff := cmp.Diff(audit.NixGetFlake{NixFiles: true}, got); diff != "" {
		t.Errorf("getFlakeFacts() mismatch (-want +got):\n%s", diff)
	}
}

// TestGetFlakeFacts_recordsCalls: only a file with a call is listed, and a
// symlink or a directory is never read.
func TestGetFlakeFacts_recordsCalls(t *testing.T) {
	t.Parallel()

	entries := []filepaths.Entry{
		{Path: "flake.nix", Mode: "100644"},
		{Path: ".ci/check", Mode: "100755"},
		{Path: ".ci/link.nix", Mode: "120000"},
		{Path: ".ci", Mode: "040000", Dir: true},
		{Path: "src/.ci/other", Mode: "100644"},
	}
	s := getFlakeServer(map[string]*fileBlob{
		"flake.nix": text("{ }\n"),
		".ci/check": text("nix eval 'builtins.getFlake (toString REPO_PLACEHOLDER)'\n"),
	})
	got, err := newClaudeClient(t, s).getFlakeFacts(t.Context(), "web-app", entries, nil)
	if err != nil {
		t.Fatalf("getFlakeFacts() error = %v, want nil", err)
	}
	want := audit.NixGetFlake{NixFiles: true, Files: []audit.GetFlakeFile{
		{Path: ".ci/check", Args: []string{"toString REPO_PLACEHOLDER"}},
	}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("getFlakeFacts() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([][]string{{"flake.nix", ".ci/check"}}, s.asked()); diff != "" {
		t.Errorf("queries mismatch (-want +got):\n%s", diff)
	}
}

// TestGetFlakeFacts_scansWorkflowText: the workflow and composite-action
// texts fetchFiles read are scanned raw, and a workflow that is not valid
// YAML is searched all the same, so a call is never lost to a parse failure.
func TestGetFlakeFacts_scansWorkflowText(t *testing.T) {
	t.Parallel()

	call := "builtins.getFlake (toString ./.)"
	f := &repoFiles{
		Workflows: &blobTree{Entries: []blobEntry{
			{Name: "ci.yml", Type: "blob", Object: text("run: nix eval '" + call + "'\n")},
			{Name: "broken.yaml", Type: "blob", Object: text("key: [unclosed\n" + call + "\n")},
			{Name: "none.yml", Type: "blob", Object: text("name: none\n")},
			{Name: "README.md", Type: "blob", Object: text(call)},
			{Name: "sub", Type: "tree"},
		}},
		Actions: &actionsTree{Entries: []actionEntry{
			{Name: "setup", Type: "tree", Object: &blobTree{Entries: []blobEntry{
				{Name: "action.yml", Type: "blob", Object: text("run: " + call + "\n")},
			}}},
			{Name: "empty", Type: "tree", Object: &blobTree{}},
			{Name: "file", Type: "blob"},
		}},
	}
	s := getFlakeServer(map[string]*fileBlob{"flake.nix": text("{ }\n")})
	got, err := newClaudeClient(t, s).getFlakeFacts(t.Context(), "web-app", files("flake.nix"), f)
	if err != nil {
		t.Fatalf("getFlakeFacts() error = %v, want nil", err)
	}
	want := audit.NixGetFlake{NixFiles: true, Files: []audit.GetFlakeFile{
		{Path: ".github/actions/setup/action.yml", Args: []string{"toString ./."}},
		{Path: ".github/workflows/broken.yaml", Args: []string{"toString ./."}},
		{Path: ".github/workflows/ci.yml", Args: []string{"toString ./."}},
	}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("getFlakeFacts() mismatch (-want +got):\n%s", diff)
	}
}

// TestGetFlakeFacts_binaryCandidateSkipped: a binary file holds no call.
func TestGetFlakeFacts_binaryCandidateSkipped(t *testing.T) {
	t.Parallel()

	s := getFlakeServer(map[string]*fileBlob{
		"flake.nix": text("{ }\n"),
		".ci/blob":  {IsBinary: true},
	})
	got, err := newClaudeClient(t, s).getFlakeFacts(t.Context(), "web-app", files("flake.nix", ".ci/blob"), nil)
	if err != nil {
		t.Fatalf("getFlakeFacts() error = %v, want nil", err)
	}
	if diff := cmp.Diff(audit.NixGetFlake{NixFiles: true}, got); diff != "" {
		t.Errorf("getFlakeFacts() mismatch (-want +got):\n%s", diff)
	}
}

// TestGetFlakeFacts_truncatedCandidateUnreadable: a file cut short might hold
// a call in the part that is missing, so it is recorded as unreadable. A
// candidate the query found nothing at is unreadable too.
func TestGetFlakeFacts_truncatedCandidateUnreadable(t *testing.T) {
	t.Parallel()

	s := getFlakeServer(map[string]*fileBlob{
		"flake.nix": text("{ }\n"),
		".ci/big":   {Text: new("partial"), IsTruncated: true},
	})
	got, err := newClaudeClient(t, s).getFlakeFacts(t.Context(), "web-app", files("flake.nix", ".ci/big", ".ci/gone"), nil)
	if err != nil {
		t.Fatalf("getFlakeFacts() error = %v, want nil", err)
	}
	want := audit.NixGetFlake{NixFiles: true, Files: []audit.GetFlakeFile{
		{Path: ".ci/big", Unreadable: ".ci/big: too large to read in full"},
		{Path: ".ci/gone", Unreadable: ".ci/gone: not a text file"},
	}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("getFlakeFacts() mismatch (-want +got):\n%s", diff)
	}
}

// TestGetFlakeFacts_truncatedWorkflowUnreadable: a workflow text cut short, or
// a binary one, is unreadable: unlike a candidate it is a file that must
// hold text.
func TestGetFlakeFacts_truncatedWorkflowUnreadable(t *testing.T) {
	t.Parallel()

	f := &repoFiles{Workflows: &blobTree{Entries: []blobEntry{
		{Name: "big.yml", Type: "blob", Object: &fileBlob{Text: new("partial"), IsTruncated: true}},
		{Name: "bin.yml", Type: "blob", Object: &fileBlob{IsBinary: true}},
	}}}
	s := getFlakeServer(map[string]*fileBlob{"flake.nix": text("{ }\n")})
	got, err := newClaudeClient(t, s).getFlakeFacts(t.Context(), "web-app", files("flake.nix"), f)
	if err != nil {
		t.Fatalf("getFlakeFacts() error = %v, want nil", err)
	}
	want := audit.NixGetFlake{NixFiles: true, Files: []audit.GetFlakeFile{
		{Path: ".github/workflows/big.yml", Unreadable: ".github/workflows/big.yml: too large to read in full"},
		{Path: ".github/workflows/bin.yml", Unreadable: ".github/workflows/bin.yml: not a text file"},
	}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("getFlakeFacts() mismatch (-want +got):\n%s", diff)
	}
}

// TestGetFlakeFacts_tooManyFiles: past getFlakeFilesMax nothing is read.
func TestGetFlakeFacts_tooManyFiles(t *testing.T) {
	t.Parallel()

	paths := make([]string, 1, 1+getFlakeFilesMax)
	paths[0] = "flake.nix"
	for i := range getFlakeFilesMax {
		paths = append(paths, fmt.Sprintf(".ci/script-%03d", i))
	}
	s := getFlakeServer(nil)
	got, err := newClaudeClient(t, s).getFlakeFacts(t.Context(), "web-app", files(paths...), nil)
	if err != nil {
		t.Fatalf("getFlakeFacts() error = %v, want nil", err)
	}
	if diff := cmp.Diff(audit.NixGetFlake{NixFiles: true, TooManyFiles: true}, got); diff != "" {
		t.Errorf("getFlakeFacts() mismatch (-want +got):\n%s", diff)
	}
	if q := s.asked(); len(q) != 0 {
		t.Errorf("sent %d queries, want none", len(q))
	}
}

// TestGetFlakeFacts_atTheBound: exactly getFlakeFilesMax candidates are read,
// in queries of blobsPerQuery.
func TestGetFlakeFacts_atTheBound(t *testing.T) {
	t.Parallel()

	paths := make([]string, 1, getFlakeFilesMax)
	paths[0] = "flake.nix"
	for i := 1; i < getFlakeFilesMax; i++ {
		paths = append(paths, fmt.Sprintf(".ci/script-%03d", i))
	}
	s := getFlakeServer(map[string]*fileBlob{})
	got, err := newClaudeClient(t, s).getFlakeFacts(t.Context(), "web-app", files(paths...), nil)
	if err != nil {
		t.Fatalf("getFlakeFacts() error = %v, want nil", err)
	}
	if got.TooManyFiles {
		t.Error("TooManyFiles = true at the bound, want false")
	}
	if n := len(s.asked()); n != getFlakeFilesMax/blobsPerQuery {
		t.Errorf("sent %d queries, want %d", n, getFlakeFilesMax/blobsPerQuery)
	}
}

// TestGetFlakeFacts_queryFailure: a failed query fails the run rather than
// recording a repository with no calls.
func TestGetFlakeFacts_queryFailure(t *testing.T) {
	t.Parallel()

	s := &claudeServer{op: "GetFlakeFiles", body: `{"errors": [{"type": "NOT_FOUND", "message": "gone"}]}`}
	_, err := newClaudeClient(t, s).getFlakeFacts(t.Context(), "web-app", files("flake.nix"), nil)
	if err == nil || !strings.Contains(err.Error(), "gone") {
		t.Errorf("getFlakeFacts() error = %v, want one containing %q", err, "gone")
	}
}
