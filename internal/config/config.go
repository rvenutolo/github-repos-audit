// Package config reads repos.toml, the only hand-maintained file in this
// repository, and checks every property of it that can be decided without
// talking to GitHub. The checks that need live data — both directions of the
// coverage mismatch, published-but-private, and an override that is dead
// against a visibility-dependent row — belong to the render path and are not
// here.
package config

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
	"github.com/rvenutolo/github-repos-audit/internal/rules"
)

// ErrInvalid reports that repos.toml is not usable. Every validation failure
// wraps it, so a caller distinguishes a bad config from an I/O failure with
// errors.Is.
var ErrInvalid = errors.New("invalid repos.toml")

// Repo is one entry of repos.toml, with its overrides resolved to checks.
type Repo struct {
	// Name is the repository's name on GitHub, which is the TOML table key.
	Name string
	// Type decides which checks apply.
	Type rules.Type
	// Published means the repository is meant for a stranger to find and use.
	Published bool
	// Overrides replaces whatever the repository's type would derive for a check, keyed
	// by the check's name. Validated keys, stored as strings so there is one
	// representation of an override across config, audit.json and the report.
	Overrides map[string]string
}

// Config is the whole of repos.toml.
type Config struct {
	// Types is every [types.*] table, keyed by type name.
	Types rules.Types
	// Repos is keyed by repository name.
	Repos map[string]Repo
	// Identity is the [identity] table: the account holder's canonical git
	// identity and the expression saying which identities are theirs. Zero
	// when repos.toml has no [identity] table, which it may omit only when
	// nothing judges git_identity.
	Identity audit.IdentityStandard
}

// Names returns every declared repository name, sorted case-insensitively so
// the report's ordering does not depend on map iteration.
func (c *Config) Names() []string {
	return slices.SortedFunc(maps.Keys(c.Repos), func(a, b string) int {
		return strings.Compare(strings.ToLower(a), strings.ToLower(b))
	})
}

// fileShape mirrors repos.toml exactly. Decoding into it rather than into Repo
// keeps the wire format separate from the validated value.
type fileShape struct {
	Types    rules.Types           `toml:"types"`
	Repos    map[string]entryShape `toml:"repos"`
	Identity *identityShape        `toml:"identity"`
}

// identityShape mirrors [identity]. A pointer in fileShape, so an empty table
// is present-but-wrong rather than indistinguishable from no table at all.
type identityShape struct {
	Canonical string `toml:"canonical"`
	Match     string `toml:"match"`
}

type entryShape struct {
	Type      string            `toml:"type"`
	Published bool              `toml:"published"`
	Overrides map[string]string `toml:"overrides"`
}

// Load reads and validates repos.toml at path.
func Load(path string) (*Config, error) {
	f, err := os.Open(path) //nolint:gosec // the path is a program argument, not user input from a request
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	cfg, err := Parse(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

// Parse reads TOML from r and validates it. Duplicate table keys are rejected
// by the TOML decoder itself, which is why there is no explicit check for
// them here.
func Parse(r io.Reader) (*Config, error) {
	var raw fileShape
	md, err := toml.NewDecoder(r).Decode(&raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	// A key the shape has no field for decodes into nothing, so a near-miss
	// such as `publish` for `published` would silently read as its default.
	// Refusing it is what makes the file mean what it says.
	if keys := md.Undecoded(); len(keys) > 0 {
		return nil, fmt.Errorf("%w: unknown keys: %v", ErrInvalid, keys)
	}
	var missing []error
	if len(raw.Types) == 0 {
		missing = append(missing, errors.New("no [types.*] entries"))
	}
	if len(raw.Repos) == 0 {
		missing = append(missing, errors.New("no [repos.*] entries"))
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, errors.Join(missing...))
	}

	cfg := &Config{Types: raw.Types, Repos: make(map[string]Repo, len(raw.Repos))}
	// The tables first, then the entries that use them, each in a stable
	// order, so a file with several problems reports them the same way every
	// run. [types.*] decodes into string maps, which md.Undecoded cannot see
	// into, so an unknown check name there is TypeProblems' to report.
	problems := rules.TypeProblems(raw.Types)
	for _, name := range slices.Sorted(maps.Keys(raw.Repos)) {
		repo, errs := validate(name, raw.Repos[name], raw.Types)
		problems = append(problems, errs...)
		cfg.Repos[name] = repo
	}
	std, errs := identityProblems(raw)
	problems = append(problems, errs...)
	if len(problems) > 0 {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, errors.Join(problems...))
	}
	cfg.Identity = std
	return cfg, nil
}

// identityProblems checks [identity] against whether anything judges
// git_identity: needed and absent is an error, and so is present and unused —
// a dead setting, rejected for the same reason a dead override is. Any word or
// override other than not_required counts as judging, info included: an
// informational cell still says how many identities are wrong, which needs a
// standard to count against.
func identityProblems(raw fileShape) (audit.IdentityStandard, []error) {
	key := rules.CheckGitIdentity.String()
	needed := false
	for _, entries := range raw.Types {
		if w := entries[key]; w != "" && w != rules.OverrideNotRequired {
			needed = true
		}
	}
	for _, e := range raw.Repos {
		if v, ok := e.Overrides[key]; ok && v != rules.OverrideNotRequired {
			needed = true
		}
	}
	switch {
	case raw.Identity == nil && needed:
		return audit.IdentityStandard{}, []error{errors.New("git_identity is judged but repos.toml has no [identity] table")}
	case raw.Identity == nil:
		return audit.IdentityStandard{}, nil
	case !needed:
		return audit.IdentityStandard{}, []error{errors.New("[identity] is set but no type or override judges git_identity")}
	}
	std := audit.IdentityStandard{Canonical: raw.Identity.Canonical, Match: raw.Identity.Match}
	return std, rules.IdentityProblems(std)
}

// validate turns one raw entry into a Repo, returning every problem it found
// rather than only the first, so one run of `audit validate` reports the whole
// file.
func validate(name string, e entryShape, types rules.Types) (Repo, []error) {
	repo := Repo{Name: name, Published: e.Published}
	var problems []error

	switch _, known := types[e.Type]; {
	case e.Type == "":
		problems = append(problems, fmt.Errorf("%s: missing type", name))
	case !known:
		problems = append(problems, fmt.Errorf("%s: unknown type %q (want one of %s)",
			name, e.Type, strings.Join(slices.Sorted(maps.Keys(types)), ", ")))
	default:
		repo.Type = rules.Type(e.Type)
	}

	if len(e.Overrides) > 0 {
		repo.Overrides = make(map[string]string, len(e.Overrides))
	}
	for _, key := range slices.Sorted(maps.Keys(e.Overrides)) {
		check, ok := rules.ParseCheck(key)
		if !ok {
			problems = append(problems, fmt.Errorf("%s: override names unknown check %q", name, key))
			continue
		}
		value := e.Overrides[key]
		if value == "" {
			problems = append(problems, fmt.Errorf("%s: override %q has an empty value", name, key))
			continue
		}
		// A value-only check has nothing that can be missing, so requiring it
		// would render a cross that no worklist entry ever explains.
		if check.ValueOnly() && value == rules.OverrideRequired {
			problems = append(problems, fmt.Errorf("%s: override %s = %q names a value-only check (want %s)",
				name, key, value, rules.OverrideNotRequired))
			continue
		}
		// The minimum-release-age check is held to a duration, so the only
		// values that mean anything are a duration or the two words that
		// switch the judgement off.
		if check.Threshold() && !rules.ValidThresholdOverride(value) {
			problems = append(problems, fmt.Errorf(
				"%s: override %s = %q (want a duration like \"7 days\", %s or info)",
				name, key, value, rules.OverrideNotRequired))
			continue
		}
		repo.Overrides[key] = value
	}

	return repo, problems
}

// Declarations reduces the configuration to what internal/rules needs, in the
// report's order. It is the handoff to internal/rules, which cannot import
// this package: config depends on rules for the check and type vocabularies,
// so the dependency runs one way only.
func (c *Config) Declarations() []rules.Declaration {
	out := make([]rules.Declaration, 0, len(c.Repos))
	for _, name := range c.Names() {
		r := c.Repos[name]
		out = append(out, rules.Declaration{
			Name:      r.Name,
			Type:      r.Type,
			Published: r.Published,
			Overrides: r.Overrides,
		})
	}
	return out
}
