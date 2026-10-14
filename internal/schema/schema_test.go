package schema_test

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rvenutolo/github-repos-audit/internal/schema"
)

// goldenAuditJSON lists every audit.json the renderer's golden tests hold.
func goldenAuditJSON(t *testing.T) []string {
	t.Helper()

	var paths []string
	root := filepath.Join("..", "render", "testdata")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), "audit.json") {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return paths
}

// minimalDoc is a small document the schema accepts: a golden trimmed to one
// type and one repository, with the optional sections dropped. Trimming a
// recorded document keeps the test from restating the encoder's field list.
func minimalDoc(t *testing.T) map[string]any {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join("..", "render", "testdata", "golden", "audit.json"))
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse golden: %v", err)
	}
	delete(doc, "identity")
	delete(doc, "config_drift")
	types, ok := doc["types"].(map[string]any)
	if !ok || len(types) == 0 {
		t.Fatalf("golden types = %v, want a non-empty object", doc["types"])
	}
	names := make([]string, 0, len(types))
	for name := range types {
		names = append(names, name)
	}
	slices.Sort(names)
	doc["types"] = map[string]any{names[0]: types[names[0]]}
	repos, ok := doc["repos"].([]any)
	if !ok || len(repos) == 0 {
		t.Fatalf("golden repos = %v, want a non-empty array", doc["repos"])
	}
	doc["repos"] = repos[:1]
	return doc
}

func marshal(t *testing.T, doc map[string]any) []byte {
	t.Helper()

	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func TestValidate_acceptsEveryGolden(t *testing.T) {
	t.Parallel()

	paths := goldenAuditJSON(t)
	if len(paths) == 0 {
		t.Fatal("no golden audit.json found under internal/render/testdata")
	}
	for _, path := range paths {
		t.Run(filepath.ToSlash(path), func(t *testing.T) {
			t.Parallel()

			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if err := schema.Validate(raw); err != nil {
				t.Errorf("Validate(%s) = %v, want nil", path, err)
			}
		})
	}
}

func TestValidate_acceptsAMinimalDocument(t *testing.T) {
	t.Parallel()

	if err := schema.Validate(marshal(t, minimalDoc(t))); err != nil {
		t.Errorf("Validate(minimal) = %v, want nil", err)
	}
}

func TestValidate_rejects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// mutate edits the minimal document; raw, when set, replaces it.
		mutate func(map[string]any)
		raw    []byte
		// want are substrings every violation line set must contain.
		want []string
	}{
		{
			name:   "an unknown top-level key",
			mutate: func(d map[string]any) { d["surprise"] = true },
			want:   []string{"surprise"},
		},
		{
			name:   "generated_at as a number",
			mutate: func(d map[string]any) { d["generated_at"] = 12 },
			want:   []string{"/generated_at"},
		},
		{
			name:   "generated_at that is not a timestamp",
			mutate: func(d map[string]any) { d["generated_at"] = "not a timestamp" },
			want:   []string{"/generated_at"},
		},
		{
			name: "two violations in one document",
			mutate: func(d map[string]any) {
				d["generated_at"] = 12
				d["owner"] = ""
			},
			want: []string{"/generated_at", "/owner"},
		},
		{name: "bytes that are not JSON", raw: []byte("this is not json"), want: []string{"JSON"}},
		{name: "an empty input", raw: []byte(""), want: []string{"empty"}},
		{name: "whitespace only", raw: []byte(" \n\t "), want: []string{"empty"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			raw := tc.raw
			if tc.mutate != nil {
				doc := minimalDoc(t)
				tc.mutate(doc)
				raw = marshal(t, doc)
			}
			err := schema.Validate(raw)
			if err == nil {
				t.Fatal("Validate = nil, want an error")
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("Validate error = %q, want it to contain %q", err, w)
				}
			}
		})
	}
}

// TestValidate_listsEachViolationOnItsOwnLine pins the output shape callers
// print: one violation per line, in the same order on every run.
func TestValidate_listsEachViolationOnItsOwnLine(t *testing.T) {
	t.Parallel()

	doc := minimalDoc(t)
	doc["generated_at"] = 12
	doc["owner"] = ""
	raw := marshal(t, doc)

	first := schema.Validate(raw)
	if first == nil {
		t.Fatal("Validate = nil, want an error")
	}
	lines := strings.Split(first.Error(), "\n")
	if len(lines) < 2 {
		t.Fatalf("Validate error = %q, want one line per violation", first)
	}
	for range 5 {
		if again := schema.Validate(raw); again == nil || again.Error() != first.Error() {
			t.Fatalf("Validate error = %v, want the same text as before: %q", again, first)
		}
	}
}

// TestValidate_unknownKeysAreOneStableLineEach: the library lists the unknown
// keys of an object in map order, so the text must be built per key and not
// vary between calls.
func TestValidate_unknownKeysAreOneStableLineEach(t *testing.T) {
	t.Parallel()

	doc := minimalDoc(t)
	for _, k := range []string{"zeta", "alpha", "mid/dle"} {
		doc[k] = true
	}
	raw := marshal(t, doc)

	want := strings.Join([]string{
		"/alpha: unknown key",
		"/mid~1dle: unknown key",
		"/zeta: unknown key",
	}, "\n")
	for i := range 200 {
		err := schema.Validate(raw)
		if err == nil {
			t.Fatal("Validate = nil, want an error")
		}
		if err.Error() != want {
			t.Fatalf("call %d: Validate error = %q, want %q", i, err, want)
		}
	}
}

// TestValidate_nestedViolationHasNoWrapperLines: a $ref wrapper such as
// "/repos/0: validation failed" is not a violation and is not listed.
func TestValidate_nestedViolationHasNoWrapperLines(t *testing.T) {
	t.Parallel()

	doc := minimalDoc(t)
	repos, ok := doc["repos"].([]any)
	if !ok {
		t.Fatalf("repos = %T, want an array", doc["repos"])
	}
	repo, ok := repos[0].(map[string]any)
	if !ok {
		t.Fatalf("repos[0] = %T, want an object", repos[0])
	}
	repo["surprise"] = 1
	raw := marshal(t, doc)

	err := schema.Validate(raw)
	if err == nil {
		t.Fatal("Validate = nil, want an error")
	}
	if want := "/repos/0/surprise: unknown key"; err.Error() != want {
		t.Errorf("Validate error = %q, want exactly %q", err, want)
	}
}

func TestValidate_rootTypeErrorIsReportedAtRoot(t *testing.T) {
	t.Parallel()

	err := schema.Validate([]byte("[]"))
	if err == nil {
		t.Fatal("Validate = nil, want an error")
	}
	if !strings.HasPrefix(err.Error(), "(root): ") || strings.Contains(err.Error(), "\n") {
		t.Errorf("Validate error = %q, want a single line starting with \"(root): \"", err)
	}
}
