package rules_test

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/rvenutolo/github-repos-audit/internal/rules"
)

type ref struct {
	Ref string `json:"$ref"`
}

type enum struct {
	Enum []string `json:"enum"`
}

// schema is only as much of audit.schema.json as this file asserts on. The
// gate validates the whole document with check-jsonschema; what a Go test adds
// is the one thing an external validator structurally cannot know — that the
// vocabularies the schema publishes are the ones this package defines.
type schema struct {
	Required   []string `json:"required"`
	Properties struct {
		Types struct {
			PropertyNames struct {
				Pattern string `json:"pattern"`
			} `json:"propertyNames"` //nolint:tagliatelle // a JSON Schema keyword; the spec spells it, this project does not
			AdditionalProperties ref `json:"additionalProperties"` //nolint:tagliatelle // a JSON Schema keyword; the spec spells it, this project does not
		} `json:"types"`
	} `json:"properties"`
	Defs struct {
		Repo struct {
			Properties struct {
				Type      enum `json:"type"`
				Overrides struct {
					PropertyNames enum `json:"propertyNames"` //nolint:tagliatelle // a JSON Schema keyword; the spec spells it, this project does not
				} `json:"overrides"`
			} `json:"properties"`
		} `json:"repo"`
		Type struct {
			Required   []string       `json:"required"`
			Properties map[string]ref `json:"properties"`
		} `json:"type"`
		Expectation     enum `json:"expectation"`
		DirectPush      enum `json:"direct_push"`
		InfoExpectation enum `json:"info_expectation"`
	} `json:"$defs"`
}

// loadSchema reads audit.schema.json from the repository root, beside
// audit.json. A relative path rather than go:embed, which cannot reach outside
// the package directory.
func loadSchema(t *testing.T) schema {
	t.Helper()

	schemaPath := filepath.Join("..", "..", "audit.schema.json")
	data, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("read %s: %v", schemaPath, err)
	}
	var s schema
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("parse %s: %v", schemaPath, err)
	}
	return s
}

// TestSchema_checkEnumMatchesTheChecks is the assertion that gives the schema
// teeth. audit.json's overrides map is keyed by check name, so the schema
// publishes that vocabulary to consumers who never read Go; without this test
// a check added to check.go would leave the published contract quietly stale,
// and check-jsonschema would keep passing because no repository happens to
// override the new check yet.
//
// The comparison is ordered. JSON Schema attaches no meaning to an enum's
// order, but check.go fixes one — the order the gap worklist reports — and
// pinning it here keeps the schema readable as the same list rather than as a
// permutation of it.
func TestSchema_checkEnumMatchesTheChecks(t *testing.T) {
	t.Parallel()

	want := make([]string, 0, len(rules.Checks()))
	for _, c := range rules.Checks() {
		want = append(want, c.String())
	}

	got := loadSchema(t).Defs.Repo.Properties.Overrides.PropertyNames.Enum
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("$defs.repo.properties.overrides.propertyNames.enum differs from rules.Checks() (-want +got):\n%s", diff)
	}
}

// TestSchema_typesAreRequired: every audit.json names the standard it was
// judged against, not only each repository's type name.
func TestSchema_typesAreRequired(t *testing.T) {
	t.Parallel()

	s := loadSchema(t)
	if !slices.Contains(s.Required, "types") {
		t.Errorf("required = %q, want it to include types", s.Required)
	}
	if got, want := s.Properties.Types.AdditionalProperties.Ref, "#/$defs/type"; got != want {
		t.Errorf("properties.types.additionalProperties.$ref = %q, want %q", got, want)
	}
	if got, want := s.Properties.Types.PropertyNames.Pattern, "^[a-z0-9_-]+$"; got != want {
		t.Errorf("properties.types.propertyNames.pattern = %q, want %q", got, want)
	}
}

// TestSchema_ownerIsRequired: every audit.json names the account it read.
func TestSchema_ownerIsRequired(t *testing.T) {
	t.Parallel()

	s := loadSchema(t)
	if !slices.Contains(s.Required, "owner") {
		t.Errorf("schema required = %v, want it to include owner", s.Required)
	}
}

// TestSchema_aTypeListsEveryCheck mirrors TypeProblems' every-check rule, in
// worklist order so the schema reads as the same list as check.go.
func TestSchema_aTypeListsEveryCheck(t *testing.T) {
	t.Parallel()

	want := make([]string, 0, len(rules.Checks()))
	for _, c := range rules.Checks() {
		want = append(want, c.String())
	}
	s := loadSchema(t)
	if diff := cmp.Diff(want, s.Defs.Type.Required); diff != "" {
		t.Errorf("$defs.type.required differs from rules.Checks() (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(slices.Sorted(slices.Values(want)), slices.Sorted(maps.Keys(s.Defs.Type.Properties))); diff != "" {
		t.Errorf("$defs.type.properties keys differ from rules.Checks() (-want +got):\n%s", diff)
	}
}

// TestSchema_eachCheckTakesTheWordsTheRulesAccept holds each check's $ref, and
// each referenced enum, to what TypeProblems accepts.
func TestSchema_eachCheckTakesTheWordsTheRulesAccept(t *testing.T) {
	t.Parallel()

	s := loadSchema(t)
	enums := map[string][]string{
		"#/$defs/expectation":      s.Defs.Expectation.Enum,
		"#/$defs/direct_push":      s.Defs.DirectPush.Enum,
		"#/$defs/info_expectation": s.Defs.InfoExpectation.Enum,
	}
	for _, c := range rules.Checks() {
		wantRef := "#/$defs/expectation"
		switch {
		case c == rules.CheckDirectPush:
			wantRef = "#/$defs/direct_push"
		case c.ValueOnly():
			wantRef = "#/$defs/info_expectation"
		}
		got := s.Defs.Type.Properties[c.String()].Ref
		if got != wantRef {
			t.Errorf("$defs.type.properties.%s.$ref = %q, want %q", c, got, wantRef)
			continue
		}
		words := enums[got]
		if len(words) == 0 {
			t.Errorf("%s has an empty enum", got)
		}
		for _, word := range words {
			if problems := rules.TypeProblems(rules.Types{"t": table(map[string]string{c.String(): word})}); len(problems) != 0 {
				t.Errorf("schema allows %s = %q, but TypeProblems rejects it: %v", c, word, problems)
			}
		}
	}
	if diff := cmp.Diff([]string{"required", "not_required", "public", "public_published", "info"}, s.Defs.Expectation.Enum); diff != "" {
		t.Errorf("$defs.expectation.enum mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"blocked", "allowed"}, s.Defs.DirectPush.Enum); diff != "" {
		t.Errorf("$defs.direct_push.enum mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"info", "not_required"}, s.Defs.InfoExpectation.Enum); diff != "" {
		t.Errorf("$defs.info_expectation.enum mismatch (-want +got):\n%s", diff)
	}
}

// TestSchema_aRepositoryTypeIsAnyDefinedName: types are defined per file, so
// the schema cannot enumerate them. "Names a key of types" is enforced in Go.
func TestSchema_aRepositoryTypeIsAnyDefinedName(t *testing.T) {
	t.Parallel()

	if got := loadSchema(t).Defs.Repo.Properties.Type.Enum; len(got) != 0 {
		t.Errorf("$defs.repo.properties.type.enum = %q, want no enum", got)
	}
}
