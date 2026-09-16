package render_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/rvenutolo/github-repos-audit/internal/render"
)

// TestEndToEnd_theRenderedReadmeIsPrettierClean is the assertion that catches
// what padding alone cannot. prettier rewrites separator rows to full width and
// normalises list markers and escapes, so a table this renderer padded
// correctly can still differ from what prettier would write. Without this, the
// first pull request after any cron commit fails `nix flake check` on a file no
// human touched.
func TestEndToEnd_theRenderedReadmeIsPrettierClean(t *testing.T) {
	t.Parallel()

	prettier, err := exec.LookPath("prettier")
	if err != nil {
		// The gate always runs inside the devshell, where .ci/required-tools
		// declares prettier and .ci/check-devshell-provides asserts it
		// resolves. Outside it, there is nothing to check against.
		t.Skip("prettier is not on PATH; run this through .ci/in-devshell")
	}

	block, err := render.Block(evaluate(t, fullSnapshot()), clock(renderedAt))
	if err != nil {
		t.Fatalf("Block() error = %v, want nil", err)
	}
	readme, err := render.Splice(readmeWithMarkers, block)
	if err != nil {
		t.Fatalf("Splice() error = %v, want nil", err)
	}

	dir := t.TempDir()
	// prettier reads its configuration by walking up from the file, so the
	// repository's own .prettierrc.yaml has to come along.
	cfg, err := os.ReadFile(filepath.Join("..", "..", ".prettierrc.yaml"))
	if err != nil {
		t.Fatalf("read .prettierrc.yaml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".prettierrc.yaml"), cfg, 0o600); err != nil {
		t.Fatalf("write .prettierrc.yaml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte(readme), 0o600); err != nil {
		t.Fatalf("write README.md: %v", err)
	}

	check := exec.CommandContext(t.Context(), prettier, "--check", "README.md")
	check.Dir = dir
	out, err := check.CombinedOutput()
	if err == nil {
		return
	}

	// Show what prettier would have written, so the failure names the bytes
	// rather than only the file.
	format := exec.CommandContext(t.Context(), prettier, "README.md")
	format.Dir = dir
	want, ferr := format.Output()
	if ferr != nil {
		t.Fatalf("prettier --check failed: %s", out)
	}
	t.Errorf("prettier --check failed: %s\n--- prettier would write ---\n%s", out, want)
}

// TestEndToEnd_pipelineProducesTheGoldenReadme drives the whole thing —
// snapshot through rules, render, splice and the atomic write — and compares
// the file on disk against the golden README.
//
// This is the layer that catches what unit tests structurally cannot. Every
// package is otherwise tested in isolation, so a field that the collector
// gathers and the model carries but the rules never read passes every unit
// test while being silently missing from the report: the most likely bug in a
// program that is mostly plumbing between thirty fields and thirty cells.
func TestEndToEnd_pipelineProducesTheGoldenReadme(t *testing.T) {
	t.Parallel()

	snap := fullSnapshot()
	block, err := render.Block(evaluate(t, snap), clock(renderedAt))
	if err != nil {
		t.Fatalf("Block() error = %v, want nil", err)
	}
	readme, err := render.Splice(readmeWithMarkers, block)
	if err != nil {
		t.Fatalf("Splice() error = %v, want nil", err)
	}
	data, err := render.JSON(snap)
	if err != nil {
		t.Fatalf("JSON() error = %v, want nil", err)
	}

	dir := t.TempDir()
	readmePath := filepath.Join(dir, "README.md")
	jsonPath := filepath.Join(dir, "audit.json")
	if err := render.WriteFiles(readmePath, readme, jsonPath, data); err != nil {
		t.Fatalf("WriteFiles() error = %v, want nil", err)
	}

	onDisk, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	golden(t, "README.md", string(onDisk))

	onDiskJSON, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("read audit.json: %v", err)
	}
	golden(t, "audit.json", string(onDiskJSON))
}
