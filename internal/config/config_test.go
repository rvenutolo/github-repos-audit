package config_test

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
	"github.com/rvenutolo/github-repos-audit/internal/config"
	"github.com/rvenutolo/github-repos-audit/internal/rules"
)

// typeTable writes one complete [types.<name>] table: every check at a word it
// accepts — required, blocked for direct push, info for the value-only checks,
// "7 days" for the release-age threshold, not_required for git_identity —
// with set replacing words (a key that is not a check is written too) and drop
// leaving checks out. git_identity defaults to not_required because most cases
// are about something else, and a judged git_identity would make each of them
// carry an [identity] table it has no interest in.
func typeTable(name string, set map[string]string, drop ...string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[types.%s]\n", name)
	for _, c := range rules.Checks() {
		key := c.String()
		if slices.Contains(drop, key) {
			continue
		}
		word := rules.OverrideRequired
		switch {
		case c == rules.CheckDirectPush:
			word = "blocked"
		case c.ValueOnly():
			word = "info"
		case c.Threshold():
			word = "7 days"
		case c == rules.CheckGitIdentity:
			word = rules.OverrideNotRequired
		}
		if w, ok := set[key]; ok {
			word = w
		}
		fmt.Fprintf(&b, "%s = %q\n", key, word)
	}
	for _, key := range slices.Sorted(maps.Keys(set)) {
		if _, known := rules.ParseCheck(key); !known {
			fmt.Fprintf(&b, "%s = %q\n", key, set[key])
		}
	}
	return b.String() + "\n"
}

// toolsTypes is the one type most rejection cases need defined.
var toolsTypes = typeTable("tools", nil) + typeTable("infra", nil)

var validFile = typeTable("software", nil) + typeTable("content", nil) + typeTable("tools", nil) + `
[repos.alpha]
type = "software"
published = true

[repos.Bravo]
type = "content"
overrides = { secret_scanning = "not_required" }

[repos.charlie]
type = "tools"
`

// unknownKeyFile is one typo away from valid: `publish` for `published`.
var unknownKeyFile = toolsTypes + "[repos.alpha]\ntype = \"tools\"\npublish = true\n"

// twoProblemsFile is broken in two places at once.
var twoProblemsFile = toolsTypes + `
[repos.alpha]
type = "widget"

[repos.bravo]
type = "tools"
overrides = { nonesuch = "required" }
`

func TestParse_acceptsAValidFile(t *testing.T) {
	t.Parallel()

	cfg, err := config.Parse(strings.NewReader(validFile))
	if err != nil {
		t.Fatalf("Parse() error = %v, want nil", err)
	}

	want := map[string]config.Repo{
		"alpha":   {Name: "alpha", Type: "software", Published: true},
		"Bravo":   {Name: "Bravo", Type: "content", Overrides: map[string]string{"secret_scanning": rules.OverrideNotRequired}},
		"charlie": {Name: "charlie", Type: "tools"},
	}
	if diff := cmp.Diff(want, cfg.Repos); diff != "" {
		t.Errorf("Parse() repos mismatch (-want +got):\n%s", diff)
	}
}

// TestParse_acceptsAThresholdOverride: a repository may hold the minimum
// release age to a duration other than its type's.
func TestParse_acceptsAThresholdOverride(t *testing.T) {
	t.Parallel()

	in := toolsTypes + "[repos.alpha]\ntype = \"tools\"\noverrides = { renovate_min_release_age = \"3 days\" }\n"
	cfg, err := config.Parse(strings.NewReader(in))
	if err != nil {
		t.Fatalf("Parse() error = %v, want nil", err)
	}
	want := map[string]string{"renovate_min_release_age": "3 days"}
	if diff := cmp.Diff(want, cfg.Repos["alpha"].Overrides); diff != "" {
		t.Errorf("Parse() overrides mismatch (-want +got):\n%s", diff)
	}
}

// identityTable is the fixture [identity] table, the vocabulary
// internal/fixturevocab allows.
const identityTable = "[identity]\ncanonical = \"Pat Example <pat@example.com>\"\nmatch = \" Example <\"\n\n"

// judgedTools is a tools type that judges git_identity.
var judgedTools = typeTable("tools", map[string]string{"git_identity": rules.OverrideRequired})

func TestParse_keepsTheIdentityStandard(t *testing.T) {
	t.Parallel()

	in := judgedTools + identityTable + "[repos.alpha]\ntype = \"tools\"\n"
	cfg, err := config.Parse(strings.NewReader(in))
	if err != nil {
		t.Fatalf("Parse() error = %v, want nil", err)
	}
	want := audit.IdentityStandard{Canonical: "Pat Example <pat@example.com>", Match: " Example <"}
	if diff := cmp.Diff(want, cfg.Identity); diff != "" {
		t.Errorf("Parse() identity mismatch (-want +got):\n%s", diff)
	}
}

// TestParse_identityOverrideMakesTheTableNeeded: one repository opting in is
// enough to need the table, and then the table is not dead.
func TestParse_identityOverrideMakesTheTableNeeded(t *testing.T) {
	t.Parallel()

	in := typeTable("tools", nil) + identityTable + "[repos.alpha]\ntype = \"tools\"\noverrides = { git_identity = \"required\" }\n"
	if _, err := config.Parse(strings.NewReader(in)); err != nil {
		t.Errorf("Parse() error = %v, want nil", err)
	}
}

// TestParse_anEmptyIdentityTableIsPresent: [identity] with nothing under it is
// a table written wrong, not a table left out, so the errors name its fields
// rather than claiming it is missing.
func TestParse_anEmptyIdentityTableIsPresent(t *testing.T) {
	t.Parallel()

	in := judgedTools + "[identity]\n\n[repos.alpha]\ntype = \"tools\"\n"
	_, err := config.Parse(strings.NewReader(in))
	if err == nil {
		t.Fatal("Parse() error = nil, want the empty table's problems")
	}
	if !errors.Is(err, config.ErrInvalid) {
		t.Errorf("Parse() error = %v, want it to wrap ErrInvalid", err)
	}
	for _, want := range []string{`identity.canonical = ""`, "identity.match: missing"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Parse() error = %q, want it to contain %q", err.Error(), want)
		}
	}
	if strings.Contains(err.Error(), "no [identity] table") {
		t.Errorf("Parse() error = %q, want no claim that the table is missing", err.Error())
	}
}

func TestParse_keepsTheTypesTable(t *testing.T) {
	t.Parallel()

	cfg, err := config.Parse(strings.NewReader(validFile))
	if err != nil {
		t.Fatalf("Parse() error = %v, want nil", err)
	}
	want := rules.Types{}
	for _, name := range []string{"software", "content", "tools"} {
		entries := map[string]string{}
		for _, c := range rules.Checks() {
			switch {
			case c == rules.CheckDirectPush:
				entries[c.String()] = "blocked"
			case c.ValueOnly():
				entries[c.String()] = "info"
			case c.Threshold():
				entries[c.String()] = "7 days"
			case c == rules.CheckGitIdentity:
				entries[c.String()] = rules.OverrideNotRequired
			default:
				entries[c.String()] = rules.OverrideRequired
			}
		}
		want[name] = entries
	}
	if diff := cmp.Diff(want, cfg.Types); diff != "" {
		t.Errorf("Parse() types mismatch (-want +got):\n%s", diff)
	}
}

func TestParse_acceptsATypeNoRepositoryUses(t *testing.T) {
	t.Parallel()

	in := typeTable("tools", nil) + typeTable("unused", nil) + "[repos.alpha]\ntype = \"tools\"\n"
	if _, err := config.Parse(strings.NewReader(in)); err != nil {
		t.Errorf("Parse() error = %v, want nil", err)
	}
}

// TestParse_reportsTypeProblemsBeforeRepositoryProblems keeps `audit validate`
// output in a stable, readable order: the tables first, then the entries that
// use them.
func TestParse_reportsTypeProblemsBeforeRepositoryProblems(t *testing.T) {
	t.Parallel()

	in := typeTable("tools", nil, "branches") + "[repos.alpha]\ntype = \"widget\"\n"
	_, err := config.Parse(strings.NewReader(in))
	if err == nil {
		t.Fatal("Parse() error = nil, want two problems")
	}
	msg := err.Error()
	typeAt := strings.Index(msg, `types.tools: missing check "branches"`)
	repoAt := strings.Index(msg, `alpha: unknown type "widget"`)
	if typeAt < 0 || repoAt < 0 || typeAt > repoAt {
		t.Errorf("Parse() error = %q, want the type problem reported before the repository problem", msg)
	}
}

func TestConfig_Names_sortsCaseInsensitively(t *testing.T) {
	t.Parallel()

	cfg, err := config.Parse(strings.NewReader(validFile))
	if err != nil {
		t.Fatalf("Parse() error = %v, want nil", err)
	}

	// "Bravo" sorts after "alpha" only when case is folded; a plain byte sort
	// would put every capitalised name first and reorder the report.
	want := []string{"alpha", "Bravo", "charlie"}
	if diff := cmp.Diff(want, cfg.Names()); diff != "" {
		t.Errorf("Names() mismatch (-want +got):\n%s", diff)
	}
}

func TestConfig_Declarations(t *testing.T) {
	t.Parallel()

	cfg, err := config.Parse(strings.NewReader(validFile))
	if err != nil {
		t.Fatalf("Parse() error = %v, want nil", err)
	}

	// The handoff to the rules package carries every declared field, in the
	// report's order, so a repository is judged on exactly what its entry says.
	want := []rules.Declaration{
		{Name: "alpha", Type: "software", Published: true},
		{Name: "Bravo", Type: "content", Overrides: map[string]string{"secret_scanning": rules.OverrideNotRequired}},
		{Name: "charlie", Type: "tools"},
	}
	if diff := cmp.Diff(want, cfg.Declarations()); diff != "" {
		t.Errorf("Declarations() mismatch (-want +got):\n%s", diff)
	}
}

// rejectedFiles is every document Parse must refuse, with the text the
// user-facing error has to carry. FuzzParse seeds from the same table.
var rejectedFiles = []struct {
	name     string
	in       string
	contains string
}{
	{
		name:     "unknown type",
		in:       toolsTypes + "[repos.alpha]\ntype = \"widget\"\n",
		contains: `unknown type "widget" (want one of infra, tools)`,
	},
	{
		name:     "missing type",
		in:       toolsTypes + "[repos.alpha]\npublished = true\n",
		contains: "missing type",
	},
	{
		name:     "duplicate repo key",
		in:       "[repos.alpha]\ntype = \"tools\"\n\n[repos.alpha]\ntype = \"config\"\n",
		contains: "alpha",
	},
	{
		name:     "override names an unknown check",
		in:       toolsTypes + "[repos.alpha]\ntype = \"tools\"\noverrides = { readmes = \"required\" }\n",
		contains: `unknown check "readmes"`,
	},
	{
		name:     "override with an empty value",
		in:       toolsTypes + "[repos.alpha]\ntype = \"tools\"\noverrides = { readme = \"\" }\n",
		contains: "empty value",
	},
	{
		name:     "required override on a value-only check",
		in:       toolsTypes + "[repos.alpha]\ntype = \"infra\"\noverrides = { last_release_age = \"required\" }\n",
		contains: `alpha: override last_release_age = "required" names a value-only check (want not_required)`,
	},
	{
		name:     "a threshold override that is not a duration",
		in:       toolsTypes + "[repos.alpha]\ntype = \"tools\"\noverrides = { renovate_min_release_age = \"7 dayz\" }\n",
		contains: `alpha: override renovate_min_release_age = "7 dayz" (want a duration like "7 days", not_required or info)`,
	},
	{
		name:     "required on the threshold check",
		in:       toolsTypes + "[repos.alpha]\ntype = \"tools\"\noverrides = { renovate_min_release_age = \"required\" }\n",
		contains: `alpha: override renovate_min_release_age = "required" (want a duration like "7 days", not_required or info)`,
	},
	{
		name:     "git_identity required with no [identity] table",
		in:       judgedTools + "[repos.alpha]\ntype = \"tools\"\n",
		contains: "git_identity is judged but repos.toml has no [identity] table",
	},
	{
		name:     "git_identity info with no [identity] table",
		in:       typeTable("tools", map[string]string{"git_identity": "info"}) + "[repos.alpha]\ntype = \"tools\"\n",
		contains: "git_identity is judged but repos.toml has no [identity] table",
	},
	{
		name:     "a git_identity override with no [identity] table",
		in:       typeTable("tools", nil) + "[repos.alpha]\ntype = \"tools\"\noverrides = { git_identity = \"required\" }\n",
		contains: "git_identity is judged but repos.toml has no [identity] table",
	},
	{
		name:     "an [identity] table nothing judges",
		in:       typeTable("tools", nil) + identityTable + "[repos.alpha]\ntype = \"tools\"\n",
		contains: "[identity] is set but no type or override judges git_identity",
	},
	{
		name:     "an unknown key under [identity]",
		in:       judgedTools + "[identity]\ncanonical = \"Pat Example <pat@example.com>\"\nmatch = \" Example <\"\nname = \"x\"\n\n[repos.alpha]\ntype = \"tools\"\n",
		contains: "identity.name",
	},
	{
		name:     "a canonical identity the match expression rejects",
		in:       judgedTools + "[identity]\ncanonical = \"Pat Example <pat@example.com>\"\nmatch = \"Robin\"\n\n[repos.alpha]\ntype = \"tools\"\n",
		contains: "identity.canonical does not match identity.match",
	},
	{
		name:     "no entries at all",
		in:       "# nothing here\n",
		contains: "no [repos.*] entries",
	},
	{
		name:     "malformed toml",
		in:       "[repos.alpha\ntype = \"tools\"\n",
		contains: "expected",
	},
	{
		name:     "no types at all",
		in:       "[repos.alpha]\ntype = \"tools\"\n",
		contains: "no [types.*] entries",
	},
	{
		name:     "a type missing a check",
		in:       typeTable("tools", nil, "branches") + "[repos.alpha]\ntype = \"tools\"\n",
		contains: `types.tools: missing check "branches"`,
	},
	{
		name:     "a type naming an unknown check",
		in:       typeTable("tools", map[string]string{"licence": "required"}) + "[repos.alpha]\ntype = \"tools\"\n",
		contains: `types.tools: unknown check "licence"`,
	},
	{
		name:     "a type with a word the check does not accept",
		in:       typeTable("tools", map[string]string{"readme": "yes"}) + "[repos.alpha]\ntype = \"tools\"\n",
		contains: `types.tools: readme = "yes" (want one of required, not_required, public, public_published, info)`,
	},
	{
		name:     "a type name outside the pattern",
		in:       typeTable("Tools", nil) + "[repos.alpha]\ntype = \"Tools\"\n",
		contains: "types.Tools: type name must match [a-z0-9_-]+",
	},
	{
		name:     "a type word that is not a string",
		in:       "[types.tools]\nreadme = true\n\n[repos.alpha]\ntype = \"tools\"\n",
		contains: `types.tools.readme`,
	},
}

func TestParse_rejects(t *testing.T) {
	t.Parallel()

	for _, tc := range rejectedFiles {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := config.Parse(strings.NewReader(tc.in))
			if err == nil {
				t.Fatalf("Parse() error = nil, want an error mentioning %q", tc.contains)
			}
			if !errors.Is(err, config.ErrInvalid) {
				t.Errorf("Parse() error = %v, want it to wrap ErrInvalid", err)
			}
			if !strings.Contains(err.Error(), tc.contains) {
				t.Errorf("Parse() error = %q, want it to contain %q", err.Error(), tc.contains)
			}
		})
	}
}

func TestParse_rejectsUnknownKeys(t *testing.T) {
	t.Parallel()

	// A key that is almost right would otherwise decode into nothing and be
	// silently dropped: `publish = true` reads as "not published" and the file
	// looks valid. Naming the key is what turns that into a one-line fix.
	_, err := config.Parse(strings.NewReader(unknownKeyFile))
	if err == nil {
		t.Fatal("Parse() error = nil, want an unknown-key error")
	}
	if !errors.Is(err, config.ErrInvalid) {
		t.Errorf("Parse() error = %v, want it to wrap ErrInvalid", err)
	}
	// User-facing `audit validate` output, so the message text is asserted.
	if !strings.Contains(err.Error(), "repos.alpha.publish") {
		t.Errorf("Parse() error = %q, want it to name the unknown key", err.Error())
	}
}

func TestParse_reportsEveryProblemAtOnce(t *testing.T) {
	t.Parallel()

	_, err := config.Parse(strings.NewReader(twoProblemsFile))
	if err == nil {
		t.Fatal("Parse() error = nil, want two problems reported")
	}
	// One run of `audit validate` must show the whole file, not the first
	// broken line; a reader who fixes one problem and re-runs pays a full
	// round trip for each.
	for _, want := range []string{`unknown type "widget"`, `unknown check "nonesuch"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Parse() error = %q, want it to contain %q", err.Error(), want)
		}
	}
}

func TestParse_reportsBothMissingTablesAtOnce(t *testing.T) {
	t.Parallel()

	_, err := config.Parse(strings.NewReader("# nothing here\n"))
	if err == nil {
		t.Fatal("Parse() error = nil, want both missing-table problems reported")
	}
	if !errors.Is(err, config.ErrInvalid) {
		t.Errorf("Parse() error = %v, want it to wrap ErrInvalid", err)
	}
	msg := err.Error()
	typesIdx := strings.Index(msg, "no [types.*] entries")
	reposIdx := strings.Index(msg, "no [repos.*] entries")
	if typesIdx == -1 || reposIdx == -1 {
		t.Fatalf("Parse() error = %q, want it to contain both missing-table messages", msg)
	}
	if typesIdx > reposIdx {
		t.Errorf("Parse() error = %q, want the types message before the repos message", msg)
	}
}

func TestLoad_reportsAMissingFile(t *testing.T) {
	t.Parallel()

	_, err := config.Load(t.TempDir() + "/absent.toml")
	if err == nil {
		t.Fatal("Load() error = nil, want a not-exist error")
	}
	if errors.Is(err, config.ErrInvalid) {
		t.Errorf("Load() error = %v, want an I/O error rather than ErrInvalid", err)
	}
}

func TestLoad_acceptsTheExampleConfig(t *testing.T) {
	t.Parallel()

	// examples/repos.toml is the file the gate validates: this repository
	// carries no repos.toml of its own. Keeping it in the unit suite means a
	// broken example fails `go test` as well as `audit validate`.
	cfg, err := config.Load("../../examples/repos.toml")
	if err != nil {
		t.Fatalf("Load(examples/repos.toml) error = %v, want nil", err)
	}
	if len(cfg.Repos) == 0 {
		t.Error("Load(examples/repos.toml) returned no repositories")
	}
}

// FuzzParse holds Parse to its invariants over arbitrary input: it never
// panics, an error comes with no Config, and a Config it does return has a
// name list that is sorted and free of duplicates, because the report's order
// is built on it.
func FuzzParse(f *testing.F) {
	f.Add(validFile)
	f.Add(unknownKeyFile)
	f.Add(twoProblemsFile)
	for _, tc := range rejectedFiles {
		f.Add(tc.in)
	}

	f.Fuzz(func(t *testing.T, in string) {
		cfg, err := config.Parse(strings.NewReader(in))
		if err != nil {
			if cfg != nil {
				t.Errorf("Parse() = %+v alongside error %v, want nil", cfg, err)
			}
			return
		}
		if cfg == nil {
			t.Fatal("Parse() = nil with a nil error")
		}

		names := cfg.Names()
		if len(names) != len(cfg.Repos) {
			t.Errorf("Names() has %d entries, want one per repository (%d)", len(names), len(cfg.Repos))
		}
		fold := func(a, b string) int { return strings.Compare(strings.ToLower(a), strings.ToLower(b)) }
		if !slices.IsSortedFunc(names, fold) {
			t.Errorf("Names() = %q, want it sorted case-insensitively", names)
		}
		if unique := slices.Compact(slices.Clone(names)); len(unique) != len(names) {
			t.Errorf("Names() = %q, want no duplicates", names)
		}
	})
}

// TestLoad_namesTheFileItRejected keeps a parse failure attributable. Load is
// given a path where Parse is given a reader, so it is the only layer that can
// say which file was wrong — and `audit render --config` means there is more
// than one candidate.
func TestLoad_namesTheFileItRejected(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "broken.toml")
	if err := os.WriteFile(path, []byte("this is not toml"), 0o600); err != nil {
		t.Fatalf("write the fixture: %v", err)
	}

	_, err := config.Load(path)
	if err == nil {
		t.Fatal("Load() error = nil, want a parse error")
	}
	if !errors.Is(err, config.ErrInvalid) {
		t.Errorf("Load() error = %v, want it to wrap ErrInvalid", err)
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("Load() error = %q, want it to name %s", err, path)
	}
}
