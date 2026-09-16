package render

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
)

// JSON marshals a snapshot the way audit.json is committed: indented, with a
// trailing newline, and with HTML escaping off so a description containing an
// ampersand reads as itself rather than as an entity.
//
// The field order comes from the struct, which is deliberate: the file is
// diffed, so a stable order is what makes `git log -p audit.json` legible.
func JSON(snap *audit.Snapshot) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(snap); err != nil {
		return nil, fmt.Errorf("marshal the snapshot: %w", err)
	}
	return buf.Bytes(), nil
}

// WriteFiles writes both artefacts. Each goes through a temp file beside its
// destination and a rename, so a reader never sees half a file; and both are
// staged before either is renamed, so a failure to produce one leaves the
// other's committed copy alone.
func WriteFiles(readmePath, readme, jsonPath string, snapshot []byte) error {
	readmeTmp, err := stage(readmePath, []byte(readme))
	if err != nil {
		return err
	}
	// Best effort: on the happy path the rename has already removed it.
	defer func() { _ = os.Remove(readmeTmp) }()
	jsonTmp, err := stage(jsonPath, snapshot)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(jsonTmp) }()

	// A staged rename fails only when the destination is something a file
	// cannot replace. README.md must already be a file for there to have been
	// anything to splice into; audit.json need not exist at all, so it is the
	// destination less is known about and it goes first — a failure there
	// leaves the README a reader sees untouched.
	if err := commit(jsonTmp, jsonPath); err != nil {
		return err
	}
	return commit(readmeTmp, readmePath)
}

// stage writes data to a fresh temp file in path's directory, so the later
// rename is on the same filesystem and therefore atomic, and returns the temp
// file's name. The caller removes it once it is renamed or abandoned.
func stage(path string, data []byte) (string, error) {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*")
	if err != nil {
		return "", fmt.Errorf("create a temp file beside %s: %w", path, err)
	}
	tmp := f.Name()

	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return "", fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("close %s: %w", tmp, err)
	}
	// CreateTemp makes the file 0600; these are committed artefacts, so give
	// them the mode a checkout would.
	if err := os.Chmod(tmp, 0o644); err != nil { //nolint:gosec // a generated, committed report is world-readable by design
		_ = os.Remove(tmp)
		return "", fmt.Errorf("chmod %s: %w", tmp, err)
	}
	return tmp, nil
}

// commit moves a staged file onto its destination in one step.
func commit(tmp, path string) error {
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename %s to %s: %w", tmp, path, err)
	}
	return nil
}
