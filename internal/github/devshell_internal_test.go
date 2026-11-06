package github

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
	"github.com/rvenutolo/github-repos-audit/internal/filepaths"
)

// devshellServer is a claudeServer for the DevshellFiles operation.
func devshellServer(blobs map[string]*fileBlob) *claudeServer {
	return &claudeServer{op: "DevshellFiles", blobs: blobs}
}

// execFiles returns an executable-file tree entry, the only kind read.
func execFiles(paths ...string) []filepaths.Entry {
	out := make([]filepaths.Entry, 0, len(paths))
	for _, p := range paths {
		out = append(out, filepaths.Entry{Path: p, Mode: "100755"})
	}
	return out
}

// TestDevshellFacts_noCandidatesNoQuery: with no executable file and no
// wrapper there is nothing a shebang could be found in, so nothing is read.
func TestDevshellFacts_noCandidatesNoQuery(t *testing.T) {
	t.Parallel()

	entries := append(files("README.md", "lib.sh"), execFiles("mvnw", "sub/gradlew")...)
	s := devshellServer(nil)
	got, err := newClaudeClient(t, s).devshellFacts(t.Context(), "web-app", entries)
	if err != nil {
		t.Fatalf("devshellFacts() error = %v, want nil", err)
	}
	if diff := cmp.Diff(audit.Devshell{}, got); diff != "" {
		t.Errorf("devshellFacts() mismatch (-want +got):\n%s", diff)
	}
	if q := s.asked(); len(q) != 0 {
		t.Errorf("sent %d queries, want none", len(q))
	}
}

// TestDevshellFacts_recordsScriptsAndWrapper: executable files and the
// wrapper are read, the generated wrappers and the non-executable library
// are not, the wrapper is never listed as a script, and the matched lines
// are recorded as the scanners return them.
func TestDevshellFacts_recordsScriptsAndWrapper(t *testing.T) {
	t.Parallel()

	entries := append(execFiles("run-checks", ".ci/lint", ".ci/fetch-tool", ".ci/in-devshell", "mvnw"), files("lib.sh")...)
	s := devshellServer(map[string]*fileBlob{
		"run-checks":      text(selfWrapScript),
		".ci/lint":        text("#!/usr/bin/env bash\necho lint\n"),
		".ci/fetch-tool":  text("#!/usr/bin/env bash\n# devshell-exempt: runs where Nix is absent\necho fetch\n"),
		".ci/in-devshell": text(fixedWrapper),
		"mvnw":            text(selfWrapScript),
		"lib.sh":          text(selfWrapScript),
	})
	got, err := newClaudeClient(t, s).devshellFacts(t.Context(), "shell-scripts", entries)
	if err != nil {
		t.Fatalf("devshellFacts() error = %v, want nil", err)
	}
	want := audit.Devshell{
		Wrapper: &audit.DevshellWrapper{
			Present:    true,
			Wait:       []string{`wait "${child_pid}" || rc="$?"`},
			Mktemp:     []string{`tmp_dir="$(mktemp --directory --tmpdir="${base}" in-devshell.XXXXXX)"`},
			KeepTmpdir: []string{"TMPDIR"},
			ExitTrap:   []string{"trap remove_tmp_dir EXIT"},
		},
		Scripts: []audit.DevshellScript{
			{Path: ".ci/fetch-tool", Exempt: []string{"runs where Nix is absent"}},
			{Path: ".ci/lint"},
			{
				Path:           "run-checks",
				InDevshellTest: []string{`if [[ -z "${IN_DEVSHELL:-}" ]]; then`},
				ExecInDevshell: []string{`exec "${REPO_DIR}/.ci/in-devshell" "${REPO_DIR}/run-all-checks" "$@"`},
			},
		},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("devshellFacts() mismatch (-want +got):\n%s", diff)
	}
	asked := s.asked()
	if len(asked) != 1 {
		t.Fatalf("sent %d queries, want 1", len(asked))
	}
	wantPaths := []string{".ci/fetch-tool", ".ci/in-devshell", ".ci/lint", "run-checks"}
	if diff := cmp.Diff(wantPaths, asked[0]); diff != "" {
		t.Errorf("query paths mismatch (-want +got):\n%s", diff)
	}
}

// TestDevshellFacts_wrapperAloneStillSerializes: a wrapper whose text matches
// no marker is still recorded as present, so it differs from no wrapper.
func TestDevshellFacts_wrapperAloneStillSerializes(t *testing.T) {
	t.Parallel()

	s := devshellServer(map[string]*fileBlob{".ci/in-devshell": text(leakingWrapper)})
	got, err := newClaudeClient(t, s).devshellFacts(t.Context(), "shell-scripts", execFiles(".ci/in-devshell"))
	if err != nil {
		t.Fatalf("devshellFacts() error = %v, want nil", err)
	}
	if diff := cmp.Diff(audit.Devshell{Wrapper: &audit.DevshellWrapper{Present: true}}, got); diff != "" {
		t.Errorf("devshellFacts() mismatch (-want +got):\n%s", diff)
	}
}

// TestDevshellFacts_wrapperNeedNotBeExecutable: the wrapper is read whatever
// its mode, since a non-executable one is still the wrapper.
func TestDevshellFacts_wrapperNeedNotBeExecutable(t *testing.T) {
	t.Parallel()

	s := devshellServer(map[string]*fileBlob{".ci/in-devshell": text(fixedWrapper)})
	got, err := newClaudeClient(t, s).devshellFacts(t.Context(), "shell-scripts", files(".ci/in-devshell"))
	if err != nil {
		t.Fatalf("devshellFacts() error = %v, want nil", err)
	}
	if got.Wrapper == nil || !got.Wrapper.Present || len(got.Wrapper.Wait) == 0 {
		t.Errorf("devshellFacts() wrapper = %+v, want a read wrapper", got.Wrapper)
	}
}

// TestDevshellFacts_skipsNonShellAndBinary: only a shell shebang makes a
// script; another interpreter, no shebang and a binary file are not recorded.
func TestDevshellFacts_skipsNonShellAndBinary(t *testing.T) {
	t.Parallel()

	s := devshellServer(map[string]*fileBlob{
		"tool.py":   text("#!/usr/bin/env python3\nprint('x')\n"),
		"tool.bin":  {IsBinary: true},
		"plain":     text("echo no shebang\n"),
		"tool.bash": text("#!/bin/bash\necho ok\n"),
	})
	got, err := newClaudeClient(t, s).devshellFacts(t.Context(), "shell-scripts", execFiles("tool.py", "tool.bin", "plain", "tool.bash"))
	if err != nil {
		t.Fatalf("devshellFacts() error = %v, want nil", err)
	}
	want := audit.Devshell{Scripts: []audit.DevshellScript{{Path: "tool.bash"}}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("devshellFacts() mismatch (-want +got):\n%s", diff)
	}
}

// TestDevshellFacts_unreadable: a file cut short, or one the query found
// nothing at, may be a script or the wrapper, so it is recorded with
// blobText's reason.
func TestDevshellFacts_unreadable(t *testing.T) {
	t.Parallel()

	s := devshellServer(map[string]*fileBlob{
		"big":             {Text: new("#!/bin/bash\n"), IsTruncated: true},
		".ci/in-devshell": {Text: new("#!/bin/bash\n"), IsTruncated: true},
	})
	got, err := newClaudeClient(t, s).devshellFacts(t.Context(), "shell-scripts", execFiles("big", "gone", ".ci/in-devshell"))
	if err != nil {
		t.Fatalf("devshellFacts() error = %v, want nil", err)
	}
	want := audit.Devshell{
		Wrapper: &audit.DevshellWrapper{Present: true, Unreadable: ".ci/in-devshell: too large to read in full"},
		Scripts: []audit.DevshellScript{
			{Path: "big", Unreadable: "big: too large to read in full"},
			{Path: "gone", Unreadable: "gone: not a text file"},
		},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("devshellFacts() mismatch (-want +got):\n%s", diff)
	}
}

// TestDevshellFacts_symlinkWrapper: a wrapper that is not a regular file is
// unreadable and is not requested, since a symlink's text is its target.
func TestDevshellFacts_symlinkWrapper(t *testing.T) {
	t.Parallel()

	for _, mode := range []string{"120000", "160000"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()

			s := devshellServer(nil)
			entries := []filepaths.Entry{{Path: ".ci/in-devshell", Mode: mode}}
			got, err := newClaudeClient(t, s).devshellFacts(t.Context(), "shell-scripts", entries)
			if err != nil {
				t.Fatalf("devshellFacts() error = %v, want nil", err)
			}
			want := audit.Devshell{Wrapper: &audit.DevshellWrapper{Present: true, Unreadable: ".ci/in-devshell: not a regular file"}}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("devshellFacts() mismatch (-want +got):\n%s", diff)
			}
			if q := s.asked(); len(q) != 0 {
				t.Errorf("sent %d queries, want none", len(q))
			}
		})
	}
}

// TestDevshellFacts_tooManyFiles: past the bound no script is read, the
// repository is recorded as having too many, and the wrapper is still read.
func TestDevshellFacts_tooManyFiles(t *testing.T) {
	t.Parallel()

	paths := make([]string, 0, getFlakeFilesMax+2)
	for i := range getFlakeFilesMax + 1 {
		paths = append(paths, fmt.Sprintf("bin/s%03d", i))
	}
	paths = append(paths, "mvnw", "sub/gradlew")
	s := devshellServer(map[string]*fileBlob{".ci/in-devshell": text(fixedWrapper)})
	entries := append(execFiles(paths...), filepaths.Entry{Path: ".ci/in-devshell", Mode: "100644"})
	got, err := newClaudeClient(t, s).devshellFacts(t.Context(), "shell-scripts", entries)
	if err != nil {
		t.Fatalf("devshellFacts() error = %v, want nil", err)
	}
	if !got.TooManyFiles || len(got.Scripts) != 0 || got.Wrapper == nil || len(got.Wrapper.Wait) == 0 {
		t.Errorf("devshellFacts() = %+v, want too many files, no scripts and a read wrapper", got)
	}
	asked := s.asked()
	if len(asked) != 1 || len(asked[0]) != 1 || asked[0][0] != ".ci/in-devshell" {
		t.Errorf("queries = %v, want one for the wrapper alone", asked)
	}
}

// TestDevshellFacts_atTheBound: exactly the bound of real candidates is still
// read, and the generated wrappers are dropped before the bound, so adding
// them does not tip it over and they are not requested.
func TestDevshellFacts_atTheBound(t *testing.T) {
	t.Parallel()

	paths := make([]string, 0, getFlakeFilesMax)
	for i := range getFlakeFilesMax {
		paths = append(paths, fmt.Sprintf("bin/s%03d", i))
	}
	s := devshellServer(nil)
	entries := append(execFiles(paths...), execFiles("mvnw", "sub/gradlew")...)
	got, err := newClaudeClient(t, s).devshellFacts(t.Context(), "shell-scripts", entries)
	if err != nil {
		t.Fatalf("devshellFacts() error = %v, want nil", err)
	}
	if got.TooManyFiles {
		t.Errorf("devshellFacts() TooManyFiles = true at the bound, want false")
	}
	// The read is chunked, so the requested paths are the queries together.
	var requested []string
	for _, q := range s.asked() {
		requested = append(requested, q...)
	}
	if diff := cmp.Diff(paths, requested); diff != "" {
		t.Errorf("requested paths mismatch (-want +got):\n%s", diff)
	}
}

// TestDevshellFacts_queryFailureFails: a query that fails fails the run
// rather than recording the files as unreadable.
func TestDevshellFacts_queryFailureFails(t *testing.T) {
	t.Parallel()

	s := &claudeServer{op: "DevshellFiles", body: `{"errors": [{"type": "NOT_FOUND", "message": "gone"}]}`}
	_, err := newClaudeClient(t, s).devshellFacts(t.Context(), "shell-scripts", execFiles("run-checks"))
	if err == nil || !strings.Contains(err.Error(), "gone") {
		t.Errorf("devshellFacts() error = %v, want one containing %q", err, "gone")
	}
}
