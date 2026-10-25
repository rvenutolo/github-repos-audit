// Package renovate decides four facts about a Renovate configuration, each as
// Renovate itself would merge it from the repository's own file and the
// presets it extends: the effective top-level minimumReleaseAge, whether the
// nix manager runs, whether lock-file maintenance is on, and whether major
// updates merge on their own.
//
// Only presets in the audited account are followed. A built-in preset lives
// in Renovate's source rather than on GitHub, so it cannot be fetched: a fixed
// table (builtinLayer) knows the few that set lockFileMaintenance.enabled or
// the automerge, approval and enablement of majors, and
// every other built-in is treated as setting nothing. Another owner's preset
// is outside what this tool reads and also sets nothing. A repository relying
// on one of those alone shows "none" or "off", which is conservative and is
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

// Result is the effective configuration, as far as this tool reads it.
type Result struct {
	MinReleaseAge string // raw, e.g. "7 days"; empty = unset (or explicitly null)
	Source        string // config path or preset reference as written; empty when unset
	// NixManager is nix.enabled true, with enabledManagers either unset or
	// listing "nix": Renovate's nix manager is off by default, and an
	// enabledManagers list that leaves it out turns it off again.
	NixManager bool
	// LockFileMaintenance is lockFileMaintenance.enabled true.
	LockFileMaintenance bool
	// AutomergeMajor is true only when, after the merge, every major update
	// is auto-merged and nothing disables or gates it.
	AutomergeMajor bool
	// Unresolved is why the configuration could not be decided; when set,
	// the others are empty or false. A wrong-typed manager key never sets
	// it: that leaves only its own fact false.
	Unresolved string
}

// layered is what one layer, presets included, says about every key this
// package reads. Each key merges on its own: a later layer that is silent on
// one key leaves an earlier layer's value for it standing.
type layered struct {
	releaseAge assignment
	nix        flag
	lockFile   flag
	managers   managerList
	automerge  automergeLayer
	// rules is packageRules. Unlike every other key it is concatenated, not
	// replaced: Renovate's mergeChildConfig appends the child's packageRules
	// to the parent's, preset rules first and the own file's last.
	// https://github.com/renovatebot/renovate/blob/main/lib/config/utils.ts
	rules []pkgRule
}

// over lays o on top of l, key by key, the way a later layer overrides an
// earlier one.
func (l *layered) over(o layered) {
	if o.releaseAge.set {
		l.releaseAge = o.releaseAge
	}
	if o.nix.set {
		l.nix = o.nix
	}
	if o.lockFile.set {
		l.lockFile = o.lockFile
	}
	if o.managers.set {
		l.managers = o.managers
	}
	l.automerge.over(o.automerge)
	l.rules = append(l.rules, o.rules...)
}

// assignment is what one layer says about minimumReleaseAge.
type assignment struct {
	set    bool   // the layer assigned something, possibly null
	value  string // "" with set means null: unset
	source string
}

// flag is what one layer says about an object's "enabled" field. Only that
// field is merged: Renovate merges such objects field by field, so a layer
// that sets nix.fileMatch alone leaves an earlier nix.enabled as it was.
type flag struct {
	set   bool
	value bool
}

// managerList is what one layer says about enabledManagers. Renovate replaces
// this array wholesale rather than appending to it, so a later layer's list is
// the whole list. (packageRules is the array that does append.)
type managerList struct {
	set   bool
	names []string
}

// unresolvedError carries the reason a resolution stopped; it never escapes
// Resolve. (Named XError for errname.)
type unresolvedError struct{ reason string }

func (e unresolvedError) Error() string { return e.reason }

type resolver struct {
	owner string
	fetch Fetch
}

// Resolve returns the effective configuration (see Result: the top-level
// minimumReleaseAge, whether the nix manager runs, whether lock-file
// maintenance is on, and whether majors auto-merge) for a repository whose Renovate configuration at
// rootPath has the text rootText.
func Resolve(ctx context.Context, owner, rootPath, rootText string, fetch Fetch) (Result, error) {
	r := resolver{owner: owner, fetch: fetch}
	l, err := r.layer(ctx, rootPath, rootText, nil)
	if err != nil {
		// errors.AsType, not errors.As: the modernize linter requires it.
		if u, ok := errors.AsType[unresolvedError](err); ok {
			return Result{Unresolved: u.reason}, nil
		}
		return Result{}, err
	}
	res := Result{
		// enabledManagers only narrows the managers Renovate runs; the nix
		// manager defaults to disabled, so listing it there without
		// nix.enabled leaves it off. Upstream declined to change that (see
		// the issue below).
		// doc-rot-exempt: upstream Renovate issue, cited as the reason nix stays off
		// https://github.com/renovatebot/renovate/issues/9408
		NixManager:          l.nix.value && (!l.managers.set || slices.Contains(l.managers.names, "nix")),
		LockFileMaintenance: l.lockFile.value,
		AutomergeMajor:      majorsAutoMerge(l.automerge, l.rules),
	}
	if l.releaseAge.set && l.releaseAge.value != "" {
		res.MinReleaseAge, res.Source = l.releaseAge.value, l.releaseAge.source
	}
	return res, nil
}

// layer merges one configuration file the way Renovate does: each extends
// entry in order, later ones overriding earlier ones, then the file's own
// keys over all of them. stack holds the presets being resolved above this
// one, for cycle detection and the depth limit.
func (r resolver) layer(ctx context.Context, source, text string, stack []string) (layered, error) {
	var cfg map[string]any
	if err := json5.Unmarshal([]byte(text), &cfg); err != nil {
		return layered{}, unresolvedError{fmt.Sprintf("%s: does not parse: %v", source, err)}
	}
	if cfg == nil {
		// json5 decodes a bare null into a nil map without error; a
		// config that is null is not an empty config.
		return layered{}, unresolvedError{source + ": not an object"}
	}

	var out layered
	if ext, has := cfg["extends"]; has {
		list, ok := ext.([]any)
		if !ok {
			return layered{}, unresolvedError{source + ": extends is not an array"}
		}
		for _, e := range list {
			ref, ok := e.(string)
			if !ok {
				return layered{}, unresolvedError{source + ": an extends entry is not a string"}
			}
			l, err := r.preset(ctx, ref, stack)
			if err != nil {
				return layered{}, err
			}
			out.over(l)
		}
	}

	own, err := ownLayer(source, cfg)
	if err != nil {
		return layered{}, err
	}
	out.over(own)
	return out, nil
}

// preset resolves one extends entry.
func (r resolver) preset(ctx context.Context, ref string, stack []string) (layered, error) {
	p, fallback, follow, problem := ParsePreset(ref, r.owner)
	if !follow {
		// Built-ins land here (they have no owner/repo), as do other owners'
		// presets; builtinLayer matches only the exact built-in names.
		l, _ := builtinLayer(ref)
		return l, nil
	}
	if problem != "" {
		return layered{}, unresolvedError{fmt.Sprintf("preset %s: %s", ref, problem)}
	}
	key := strings.ToLower(p.Repo) + "/" + p.Path + "#" + p.Ref
	if slices.Contains(stack, key) {
		return layered{}, unresolvedError{fmt.Sprintf("preset %s: cycle", ref)}
	}
	if len(stack) >= maxDepth {
		return layered{}, unresolvedError{fmt.Sprintf("preset %s: deeper than %d", ref, maxDepth)}
	}

	text, problem, err := r.fetch(ctx, p)
	if err == nil && problem == ProblemNotFound && fallback {
		// Renovate still reads the deprecated renovate.json when a preset
		// repository has no default.json, in the same directory: only the
		// file name changes, so "dir/default.json" falls back to
		// "dir/renovate.json".
		p.Path = strings.TrimSuffix(p.Path, "default.json") + "renovate.json"
		text, problem, err = r.fetch(ctx, p)
	}
	if err != nil {
		return layered{}, fmt.Errorf("renovate preset %s: %w", ref, err)
	}
	if problem != "" {
		return layered{}, unresolvedError{fmt.Sprintf("preset %s: %s", ref, problem)}
	}
	return r.layer(ctx, ref, text, append(slices.Clone(stack), key))
}

// ownLayer reads every key this package merges from the file's own text, not
// its presets. Only minimumReleaseAge can make the layer unresolved: its
// reason is published as min_release_age_error, so the manager and automerge
// keys must not add reasons of their own to it.
func ownLayer(source string, cfg map[string]any) (layered, error) {
	releaseAge, err := ownAssignment(source, cfg)
	if err != nil {
		return layered{}, err
	}
	return layered{
		releaseAge: releaseAge,
		nix:        ownFlag(cfg, "nix"),
		lockFile:   ownFlag(cfg, "lockFileMaintenance"),
		managers:   ownManagers(cfg),
		automerge:  ownAutomerge(cfg),
		rules:      ownRules(cfg),
	}, nil
}

// ownFlag reads key.enabled. A key that is present but not an object, or an
// enabled that is not a boolean (null included), is a value this tool cannot
// read the way Renovate would. It counts as this layer setting the fact
// false: a misread can then only fail a check, never pass one.
func ownFlag(cfg map[string]any, key string) flag {
	v, has := cfg[key]
	if !has {
		return flag{}
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return flag{set: true}
	}
	e, has := obj["enabled"]
	if !has {
		return flag{}
	}
	b, ok := e.(bool)
	return flag{set: true, value: ok && b}
}

// ownManagers reads enabledManagers, an array of manager names. A value that
// is not an array of strings counts as a list naming no manager, which turns
// the nix manager off: as with ownFlag, a misread can only fail a check.
func ownManagers(cfg map[string]any) managerList {
	v, has := cfg["enabledManagers"]
	if !has {
		return managerList{}
	}
	list, ok := v.([]any)
	if !ok {
		return managerList{set: true}
	}
	names := make([]string, 0, len(list))
	for _, e := range list {
		name, ok := e.(string)
		if !ok {
			return managerList{set: true}
		}
		names = append(names, name)
	}
	return managerList{set: true, names: names}
}

// ownAssignment reads the file's own minimumReleaseAge, or its legacy
// stabilityDays migrated as Renovate migrates it.
func ownAssignment(source string, cfg map[string]any) (assignment, error) {
	mra, hasMRA := cfg["minimumReleaseAge"]
	if hasMRA && mra != nil {
		x, ok := mra.(string)
		if !ok {
			return assignment{}, unresolvedError{source + ": minimumReleaseAge is not a string"}
		}
		if strings.Contains(x, "{{") {
			return assignment{}, unresolvedError{source + ": minimumReleaseAge is a {{template}}"}
		}
		return assignment{set: true, value: x, source: source}, nil
	}
	// Renovate migrates stabilityDays with setSafely, which writes unless
	// minimumReleaseAge is already non-null: an explicit null is no bar,
	// so {"minimumReleaseAge": null, "stabilityDays": 3} means "3 days".
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
	if hasMRA {
		// An explicit null with no stabilityDays: set, to nothing, which
		// clears whatever an extended preset said.
		return assignment{set: true, source: source}, nil
	}
	return assignment{}, nil
}
