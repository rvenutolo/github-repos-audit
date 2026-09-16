package render_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
	"github.com/rvenutolo/github-repos-audit/internal/render"
)

func TestJSON_isDeterministic(t *testing.T) {
	t.Parallel()

	snap := fullSnapshot()
	first, err := render.JSON(snap)
	if err != nil {
		t.Fatalf("JSON() error = %v, want nil", err)
	}
	second, err := render.JSON(snap)
	if err != nil {
		t.Fatalf("JSON() error = %v, want nil", err)
	}
	// The file is committed and diffed, so two renders of one snapshot must be
	// byte-identical or every run shows a spurious change.
	if string(first) != string(second) {
		t.Error("JSON() is not deterministic")
	}
}

func TestJSON_endsWithNewline(t *testing.T) {
	t.Parallel()

	data, err := render.JSON(fullSnapshot())
	if err != nil {
		t.Fatalf("JSON() error = %v, want nil", err)
	}
	// A committed file without a final newline shows as a change on every
	// editor that adds one.
	if !strings.HasSuffix(string(data), "\n") {
		t.Error("JSON() should end with a newline")
	}
}

func TestJSON_doesNotEscapeHTML(t *testing.T) {
	t.Parallel()

	// HTML escaping off, so a description containing an ampersand reads as
	// itself rather than as an entity.
	esc := &audit.Snapshot{Repos: []audit.Repo{{Name: "a", Description: "this & that <b>"}}}
	data, err := render.JSON(esc)
	if err != nil {
		t.Fatalf("JSON() error = %v, want nil", err)
	}
	if !strings.Contains(string(data), "this & that <b>") {
		t.Errorf("JSON() escaped HTML in:\n%s", data)
	}
}

func TestJSON_usesSnakeCaseKeys(t *testing.T) {
	t.Parallel()

	data, err := render.JSON(fullSnapshot())
	if err != nil {
		t.Fatalf("JSON() error = %v, want nil", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("the output is not valid JSON: %v", err)
	}
	// The key names are an interface: audit.json is committed, so a rename
	// rewrites the history of the account this repository keeps.
	for _, key := range []string{"generated_at", "types", "repos"} {
		if _, ok := decoded[key]; !ok {
			t.Errorf("audit.json has no %q key", key)
		}
	}
}

func TestWriteFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	readmePath := filepath.Join(dir, "README.md")
	jsonPath := filepath.Join(dir, "audit.json")

	if err := render.WriteFiles(readmePath, "hello\n", jsonPath, []byte("{}\n")); err != nil {
		t.Fatalf("WriteFiles() error = %v, want nil", err)
	}
	for path, want := range map[string]string{readmePath: "hello\n", jsonPath: "{}\n"} {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if string(got) != want {
			t.Errorf("%s = %q, want %q", path, got, want)
		}
	}

	// Overwriting works, and leaves no temp file behind: the write goes
	// through a temp file in the same directory so the rename is atomic.
	if err := render.WriteFiles(readmePath, "again\n", jsonPath, []byte("[]\n")); err != nil {
		t.Fatalf("WriteFiles() error = %v, want nil", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	if len(entries) != 2 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("directory holds %v, want exactly the two artefacts", names)
	}
}

func TestWriteFiles_leavesTheReadmeAloneWhenTheSnapshotCannotBeWritten(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// jsonPath returns the audit.json destination for a temp dir, after
		// arranging for the write to it to fail.
		jsonPath func(t *testing.T, dir string) string
	}{
		{
			name: "destination is a directory so the rename fails",
			jsonPath: func(t *testing.T, dir string) string {
				t.Helper()
				path := filepath.Join(dir, "audit.json")
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatalf("mkdir %s: %v", path, err)
				}
				return path
			},
		},
		{
			name: "directory is missing so the temp file cannot be created",
			jsonPath: func(t *testing.T, dir string) string {
				t.Helper()
				return filepath.Join(dir, "no-such-dir", "audit.json")
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			readmePath := filepath.Join(dir, "README.md")
			const before = "committed\n"
			if err := os.WriteFile(readmePath, []byte(before), 0o600); err != nil {
				t.Fatalf("write README.md: %v", err)
			}
			jsonPath := tc.jsonPath(t, dir)

			if err := render.WriteFiles(readmePath, "rendered\n", jsonPath, []byte("{}\n")); err == nil {
				t.Fatal("WriteFiles() error = nil, want the audit.json failure")
			}

			// The two artefacts are committed together or not at all: a README
			// describing a snapshot that was never written is worse than a
			// stale one.
			after, err := os.ReadFile(readmePath)
			if err != nil {
				t.Fatalf("read README.md: %v", err)
			}
			if string(after) != before {
				t.Errorf("README.md = %q, want it untouched at %q", after, before)
			}

			// And the staged temp files are cleaned up, so a failed run leaves
			// nothing for git status to show.
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatalf("read %s: %v", dir, err)
			}
			for _, e := range entries {
				if name := e.Name(); name != "README.md" && name != "audit.json" {
					t.Errorf("directory holds stray file %q after a failed write", name)
				}
			}
		})
	}
}

func TestWriteFiles_reportsAnUnwritableDestination(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), "no-such-dir", "README.md")
	if err := render.WriteFiles(missing, "x", missing, []byte("{}")); err == nil {
		t.Error("WriteFiles() error = nil, want a create error")
	}
}
