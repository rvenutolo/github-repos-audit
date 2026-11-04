package rules

import (
	"regexp"
	"slices"
	"strings"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
)

// GitHub scopes an Actions cache to the ref that saved it, and a run can
// restore only its own ref's caches and its default branch's. So a cache
// saved anywhere but the default branch serves at most the one pull request,
// queue entry or feature branch that saved it, and still counts against the
// repository's storage limit until GitHub evicts it. A Nix store cache is
// about a gigabyte, so a few pull requests fill the limit. The account's
// standard is that only the default branch saves; every other run restores.

// cacheSaver is how one cache action saves, as cache_save_default_branch
// judges it.
type cacheSaver struct {
	// action is the published action, matched by usesAction.
	action string
	// input names the `with:` input that turns saving off, or "" when the
	// action has none and only an `if:` can stop it saving.
	input string
	// savesWhenUnset reports whether the action saves with input absent or
	// empty: cache-nix-action and setup-go cache by default, setup-node and
	// setup-python only once `cache:` names a package manager.
	savesWhenUnset bool
}

// cacheSavers is every cache action the check recognizes. actions/cache/
// restore never saves, so it is not listed; nor is any action the check does
// not know, which it cannot judge.
func cacheSavers() []cacheSaver {
	return []cacheSaver{
		{action: "actions/cache"},
		{action: "actions/cache/save"},
		{action: cacheNixAction, input: "save", savesWhenUnset: true},
		{action: "DeterminateSystems/magic-nix-cache-action"},
		{action: "actions/setup-go", input: "cache", savesWhenUnset: true},
		{action: "actions/setup-node", input: "cache"},
		{action: "actions/setup-python", input: "cache"},
	}
}

// observeCacheSaveDefaultBranch judges whether every recognized cache step a
// workflow can run off the default branch saves only on the default branch.
// A step passes when it is gated to the default branch (see
// gatedToDefault) by its own `if:`, by its job's, by the `if:` of the
// workflow step that uses the composite action it sits in, or, for an action
// with a save input, by that input; or when the input turns saving off.
// Any expression the tool cannot read as such a gate fails, since it may
// let another ref save.
//
// The cell names each file holding a step that saves off the default
// branch, workflow or composite action, in path order, then each file that
// could not be read: an unreadable workflow may be one that can run off the
// default branch, and an unreadable composite one such a workflow uses may
// hold a cache step.
//
// It is n/a when no workflow that can run off the default branch reaches a
// recognized cache step and every file was read.
func observeCacheSaveDefaultBranch(r audit.Repo) observation {
	var (
		found              bool
		saving, unreadable []string
	)
	for _, w := range r.Workflows {
		if w.ParseError != "" {
			unreadable = append(unreadable, w.Path)
			continue
		}
		if defaultBranchOnly(w, r.DefaultBranch) {
			continue
		}
		for _, j := range w.Jobs {
			walkCacheSteps(r, w.Path, j.Uses, []string{j.If}, map[string]bool{}, func(path string, gated bool) {
				found = true
				if !gated {
					saving = append(saving, path)
				}
			}, func(path string) { unreadable = append(unreadable, path) })
		}
	}
	if !found && len(unreadable) == 0 {
		return observation{unknown: true}
	}
	problems := appendFiles(nil, "saves off the default branch in ", saving)
	problems = appendFiles(problems, "unreadable ", unreadable)
	return observation{ok: len(problems) == 0, value: strings.Join(problems, ", "), mark: true}
}

// walkCacheSteps calls step for every recognized cache step in uses, with
// the path of the file it is written in and whether it is gated; conds are
// the `if:` expressions that enclose the steps, the job's and those of any
// workflow steps using the composite actions on the way down. A local
// composite action is followed into, once per path through it so a cycle
// ends, and an unreadable one is reported through unreadable.
func walkCacheSteps(r audit.Repo, path string, uses []audit.StepUse, conds []string, seen map[string]bool,
	step func(path string, gated bool), unreadable func(path string),
) {
	for _, u := range uses {
		enclosing := append(slices.Clone(conds), u.If)
		if ca, local := resolveComposite(r, u.Action); local {
			switch {
			case seen[ca.Path]:
			case ca.ParseError != "":
				unreadable(ca.Path)
			default:
				seen[ca.Path] = true
				walkCacheSteps(r, ca.Path, ca.Uses, enclosing, seen, step, unreadable)
				delete(seen, ca.Path)
			}
			continue
		}
		savers := cacheSavers()
		i := slices.IndexFunc(savers, func(c cacheSaver) bool { return usesAction(u.Action, c.action) })
		if i < 0 {
			continue
		}
		c := savers[i]
		if c.input != "" {
			v := strings.TrimSpace(u.With[c.input])
			switch {
			case v == "false", v == "" && !c.savesWhenUnset:
				continue
			case v != "" && v != "true":
				enclosing = append(enclosing, v)
			}
		}
		step(path, slices.ContainsFunc(enclosing, func(e string) bool { return gatedToDefault(e, r.DefaultBranch) }))
	}
}

// defaultRefTerm matches one comparison that holds only on a branch:
// github.ref against a refs/heads/ ref, or github.ref_name against a name,
// either way round, with the literal in its own capture group. A literal in
// GitHub's expression syntax is single-quoted, a doubled quote standing for
// one, which no branch name the check compares contains.
var defaultRefTerm = regexp.MustCompile(`^(?:github\.(ref|ref_name)\s*==\s*'([^']*)'|'([^']*)'\s*==\s*github\.(ref|ref_name))$`)

// gatedToDefault reports whether the expression e is true only on the
// default branch: written with or without its `${{ }}`, it is a chain of
// `&&` terms, one of which compares github.ref with refs/heads/<branch> or
// github.ref_name with <branch>. An `||` anywhere, or a parenthesis, makes
// it an expression the check does not read, so it is no gate.
func gatedToDefault(e, branch string) bool {
	e = strings.TrimSpace(e)
	if inner, ok := strings.CutPrefix(e, "${{"); ok {
		if inner, ok = strings.CutSuffix(inner, "}}"); ok {
			e = inner
		}
	}
	if branch == "" || strings.ContainsAny(e, "()") && !onlyCallParens(e) || strings.Contains(e, "||") {
		return false
	}
	for term := range strings.SplitSeq(e, "&&") {
		m := defaultRefTerm.FindStringSubmatch(strings.TrimSpace(term))
		if m == nil {
			continue
		}
		field, literal := m[1], m[2]
		if field == "" {
			field, literal = m[4], m[3]
		}
		if field == "ref" && literal == "refs/heads/"+branch || field == "ref_name" && literal == branch {
			return true
		}
	}
	return false
}

// statusCall matches GitHub's status functions, the only calls a gate may
// sit beside: `always() && github.ref == ...` still holds only on the
// default branch.
var statusCall = regexp.MustCompile(`\b(?:always|success|failure|cancelled)\(\)`)

// onlyCallParens reports whether every parenthesis in e belongs to a status
// function call, so the expression is still a flat chain of terms.
func onlyCallParens(e string) bool {
	return !strings.ContainsAny(statusCall.ReplaceAllString(e, ""), "()")
}

// defaultBranchOnly reports whether every trigger of a workflow runs it on
// the default branch alone, so no save it makes can land under another ref:
// a schedule, which always runs on the default branch, or a push whose only
// branch filter is exactly the default branch, with no tag filter. Every
// other event can run on another ref, a manual dispatch on any branch
// included, and so can a workflow with no trigger the tool read.
func defaultBranchOnly(w audit.Workflow, branch string) bool {
	if len(w.Events) == 0 {
		return false
	}
	for _, ev := range w.Events {
		switch ev {
		case "schedule":
		case "push":
			if w.PushTagFilter || len(w.PushBranchesIgnore) > 0 || !slices.Equal(w.PushBranches, []string{branch}) {
				return false
			}
		default:
			return false
		}
	}
	return true
}
