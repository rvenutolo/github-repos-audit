// Package renovate decides one fact about a Renovate configuration: the
// effective top-level minimumReleaseAge, as Renovate itself would merge it
// from the repository's own file and the presets it extends.
//
// Only presets in the audited account are followed. A built-in preset lives
// in Renovate's source rather than on GitHub, and another owner's preset is
// outside what this tool reads, so both are treated as setting nothing — a
// repository relying on one alone shows "none", which is conservative and is
// fixed by stating the value explicitly.
//
// The package does no I/O: the caller supplies Fetch, and the value is
// returned exactly as written, because turning "7 days" into a verdict is
// internal/rules' job.
package renovate

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/titanous/json5"
)

// maxDepth bounds preset recursion per root, so a pathological chain costs a
// bounded number of requests rather than the rate limit.
const maxDepth = 10

// Preset is one in-account preset file to fetch.
type Preset struct {
	Repo string // repository name within the audited account
	Path string // file path within it, e.g. "default.json"
	Ref  string // git ref; empty means the default branch
}

// ProblemNotFound is the problem a Fetch reports for an ordinary 404.
const ProblemNotFound = "not found"

// Fetch returns a preset file's text. A non-empty problem ("not found",
// "not a file", "too large to read", …) means the file cannot be used and the
// resolution is unresolved; err is reserved for failures that must abort the
// whole run.
type Fetch func(ctx context.Context, p Preset) (text, problem string, err error)

// Result is the effective top-level minimumReleaseAge.
type Result struct {
	MinReleaseAge string // raw, e.g. "7 days"; empty = unset (or explicitly null)
	Source        string // config path or preset reference as written; empty when unset
	Unresolved    string // why it could not be decided; when set, the others are empty
}

// assignment is what one layer, presets included, says about the value.
type assignment struct {
	set    bool   // the layer assigned something, possibly null
	value  string // "" with set means null: unset
	source string
}

// unresolvedError carries the reason a resolution stopped; it never escapes
// Resolve. (Named XError for errname.)
type unresolvedError struct{ reason string }

func (e unresolvedError) Error() string { return e.reason }

type resolver struct {
	owner string
	fetch Fetch
}

// Resolve returns the effective top-level minimumReleaseAge for a repository
// whose Renovate configuration at rootPath has the text rootText.
func Resolve(ctx context.Context, owner, rootPath, rootText string, fetch Fetch) (Result, error) {
	r := resolver{owner: owner, fetch: fetch}
	a, err := r.layer(ctx, rootPath, rootText, nil)
	if err != nil {
		// errors.AsType, not errors.As: the modernize linter requires it.
		if u, ok := errors.AsType[unresolvedError](err); ok {
			return Result{Unresolved: u.reason}, nil
		}
		return Result{}, err
	}
	if !a.set || a.value == "" {
		return Result{}, nil
	}
	return Result{MinReleaseAge: a.value, Source: a.source}, nil
}

// layer merges one configuration file the way Renovate does: each extends
// entry in order, later ones overriding earlier ones, then the file's own
// keys over all of them. stack holds the presets being resolved above this
// one, for cycle detection and the depth limit.
func (r resolver) layer(ctx context.Context, source, text string, stack []string) (assignment, error) {
	var cfg map[string]any
	if err := json5.Unmarshal([]byte(text), &cfg); err != nil {
		return assignment{}, unresolvedError{fmt.Sprintf("%s: does not parse: %v", source, err)}
	}
	if cfg == nil {
		// json5 decodes a bare null into a nil map without error; a
		// config that is null is not an empty config.
		return assignment{}, unresolvedError{source + ": not an object"}
	}

	var out assignment
	if ext, has := cfg["extends"]; has {
		list, ok := ext.([]any)
		if !ok {
			return assignment{}, unresolvedError{source + ": extends is not an array"}
		}
		for _, e := range list {
			ref, ok := e.(string)
			if !ok {
				return assignment{}, unresolvedError{source + ": an extends entry is not a string"}
			}
			a, err := r.preset(ctx, ref, stack)
			if err != nil {
				return assignment{}, err
			}
			if a.set {
				out = a
			}
		}
	}

	own, err := ownAssignment(source, cfg)
	if err != nil {
		return assignment{}, err
	}
	if own.set {
		out = own
	}
	return out, nil
}

// preset resolves one extends entry.
func (r resolver) preset(ctx context.Context, ref string, stack []string) (assignment, error) {
	p, fallback, follow, problem := ParsePreset(ref, r.owner)
	if !follow {
		return assignment{}, nil
	}
	if problem != "" {
		return assignment{}, unresolvedError{fmt.Sprintf("preset %s: %s", ref, problem)}
	}
	key := strings.ToLower(p.Repo) + "/" + p.Path + "#" + p.Ref
	if slices.Contains(stack, key) {
		return assignment{}, unresolvedError{fmt.Sprintf("preset %s: cycle", ref)}
	}
	if len(stack) >= maxDepth {
		return assignment{}, unresolvedError{fmt.Sprintf("preset %s: deeper than %d", ref, maxDepth)}
	}

	text, problem, err := r.fetch(ctx, p)
	if err == nil && problem == ProblemNotFound && fallback {
		// Renovate still reads the deprecated renovate.json when a preset
		// repository has no default.json.
		p.Path = "renovate.json"
		text, problem, err = r.fetch(ctx, p)
	}
	if err != nil {
		return assignment{}, fmt.Errorf("renovate preset %s: %w", ref, err)
	}
	if problem != "" {
		return assignment{}, unresolvedError{fmt.Sprintf("preset %s: %s", ref, problem)}
	}
	return r.layer(ctx, ref, text, append(slices.Clone(stack), key))
}

// ownAssignment reads the file's own minimumReleaseAge, or its legacy
// stabilityDays migrated as Renovate migrates it.
func ownAssignment(source string, cfg map[string]any) (assignment, error) {
	if v, has := cfg["minimumReleaseAge"]; has {
		switch x := v.(type) {
		case nil:
			return assignment{set: true, source: source}, nil
		case string:
			if strings.Contains(x, "{{") {
				return assignment{}, unresolvedError{source + ": minimumReleaseAge is a {{template}}"}
			}
			return assignment{set: true, value: x, source: source}, nil
		default:
			return assignment{}, unresolvedError{source + ": minimumReleaseAge is not a string"}
		}
	}
	if v, has := cfg["stabilityDays"]; has {
		n, ok := v.(float64)
		if !ok || n < 0 || n != math.Trunc(n) {
			return assignment{}, unresolvedError{source + ": stabilityDays is not a whole number"}
		}
		switch n {
		case 0:
			return assignment{set: true, source: source}, nil
		case 1:
			return assignment{set: true, value: "1 day", source: source}, nil
		default:
			return assignment{set: true, value: fmt.Sprintf("%d days", int64(n)), source: source}, nil
		}
	}
	return assignment{}, nil
}
