package github

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
)

func text(s string) *fileBlob { return &fileBlob{Text: &s} }

// TestFlakeFacts_unreadableLock: flake.lock exists whether or not its text
// can be read, so HasLock holds and LockError says why the lock facts are
// missing. None of them is filled from a lock read in part.
func TestFlakeFacts_unreadableLock(t *testing.T) {
	t.Parallel()

	truncated := text(`{"nodes": {"root": {}}`)
	truncated.IsTruncated = true
	tests := []struct {
		name string
		lock *fileBlob
		want string
	}{
		{name: "binary", lock: &fileBlob{IsBinary: true}, want: "flake.lock: not a text file"},
		{name: "truncated", lock: truncated, want: "flake.lock: too large to read in full"},
		{name: "not JSON", lock: text("{"), want: "flake.lock is not valid JSON"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := flakeFacts(&repoFiles{FlakeLock: tc.lock}, Probe{})
			if !got.HasLock {
				t.Error("HasLock = false, want true: the file exists")
			}
			if !strings.HasPrefix(got.LockError, tc.want) {
				t.Errorf("LockError = %q, want it to start with %q", got.LockError, tc.want)
			}
			if got.LockedInputs != nil || !got.NixpkgsLastModified.IsZero() || got.AllRevPinned {
				t.Errorf("lock facts = %+v, want none beside the error", got)
			}
		})
	}
}

// TestFlakeFacts_unreadableFlakeNix: a flake.nix not read in full is recorded
// as unreadable, and neither an input nor the lint is recorded from part of
// it. A missing flake.nix is not an error: files.flake_nix already reports
// it, so nothing is recorded.
func TestFlakeFacts_unreadableFlakeNix(t *testing.T) {
	t.Parallel()

	partial := text(`inputs.lint.url = "github:gh-owner/lint-flake"; check-workflow-hermetic`)
	partial.IsTruncated = true
	tests := []struct {
		name  string
		flake *fileBlob
		want  audit.Flake
	}{
		{name: "truncated", flake: partial, want: audit.Flake{NixError: "flake.nix: too large to read in full"}},
		{name: "binary", flake: &fileBlob{IsBinary: true}, want: audit.Flake{NixError: "flake.nix: not a text file"}},
		{name: "missing", flake: nil, want: audit.Flake{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := flakeFacts(&repoFiles{FlakeNix: tc.flake}, Probe{Lint: "check-workflow-hermetic"})
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("Flake mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestCompositeFacts covers the shapes a .github/actions listing can take:
// action.yml preferred to action.yaml, either accepted alone, a file not read
// in full recorded as unreadable, and the result in path order whatever order
// the listing came in.
func TestCompositeFacts(t *testing.T) {
	t.Parallel()

	steps := "runs:\n  using: composite\n  steps:\n    - uses: example-org/action-%s@v1\n"
	truncated := text("runs:\n  using: comp")
	truncated.IsTruncated = true
	listing := &actionsTree{Entries: []actionEntry{
		{Name: "action-03", Type: "tree", Object: &blobTree{Entries: []blobEntry{
			{Name: "action.yml", Type: "blob", Object: truncated},
		}}},
		{Name: "action-02", Type: "tree", Object: &blobTree{Entries: []blobEntry{
			{Name: "action.yaml", Type: "blob", Object: text(strings.ReplaceAll(steps, "%s", "yaml"))},
			{Name: "action.yml", Type: "blob", Object: text(strings.ReplaceAll(steps, "%s", "yml"))},
		}}},
		{Name: "action-01", Type: "tree", Object: &blobTree{Entries: []blobEntry{
			{Name: "action.yaml", Type: "blob", Object: text(strings.ReplaceAll(steps, "%s", "01"))},
		}}},
		// A directory named like an action file is not one.
		{Name: "action-04", Type: "tree", Object: &blobTree{Entries: []blobEntry{
			{Name: "action.yml", Type: "tree", Object: &fileBlob{}},
		}}},
		// A submodule has no listing.
		{Name: "action-05", Type: "commit"},
	}}
	want := []audit.CompositeAction{
		{Path: ".github/actions/action-01/action.yaml", Uses: []audit.StepUse{{Action: "example-org/action-01@v1"}}},
		{Path: ".github/actions/action-02/action.yml", Uses: []audit.StepUse{{Action: "example-org/action-yml@v1"}}},
		{
			Path:       ".github/actions/action-03/action.yml",
			ParseError: ".github/actions/action-03/action.yml: too large to read in full",
		},
	}
	if diff := cmp.Diff(want, compositeFacts(listing)); diff != "" {
		t.Errorf("compositeFacts mismatch (-want +got):\n%s", diff)
	}
}

// TestWorkflowFacts_onlyWorkflowFiles: GitHub runs only .yml and .yaml files
// as workflows, and a directory of that name is not one.
func TestWorkflowFacts_onlyWorkflowFiles(t *testing.T) {
	t.Parallel()

	listing := &blobTree{Entries: []blobEntry{
		{Name: "ci.yaml", Type: "blob", Object: text("on: push\njobs: {}\n")},
		{Name: "notes.txt", Type: "blob", Object: text("on: push\n")},
		{Name: "old.yml", Type: "tree", Object: &fileBlob{}},
	}}
	want := []audit.Workflow{{Path: ".github/workflows/ci.yaml", Events: []string{"push"}}}
	if diff := cmp.Diff(want, workflowFacts(listing)); diff != "" {
		t.Errorf("workflowFacts mismatch (-want +got):\n%s", diff)
	}
}

// TestGateFiles_ciSubdirectoryIsNotRead: only files directly under .ci are
// gate files; a directory there has no text of its own.
func TestGateFiles_ciSubdirectoryIsNotRead(t *testing.T) {
	t.Parallel()

	f := &repoFiles{
		DotJustfile: text("check:\n    lint-x\n"),
		CI: &blobTree{Entries: []blobEntry{
			{Name: "sub", Type: "tree", Object: &fileBlob{}},
			{Name: "check-01", Type: "blob", Object: text("lint-x\n")},
		}},
	}
	want := []string{".ci/check-01", ".justfile"}
	if diff := cmp.Diff(want, gateFiles(f, "lint-x")); diff != "" {
		t.Errorf("gateFiles mismatch (-want +got):\n%s", diff)
	}
}
