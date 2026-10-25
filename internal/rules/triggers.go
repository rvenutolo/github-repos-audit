package rules

import (
	"path"
	"slices"
	"strings"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
)

// observeCIOnPR judges whether some workflow runs when a pull request into
// the default branch is opened or updated, which is the only way a pull
// request is tested before it is merged. The predicate is runsOnSynchronize,
// the same one ci_cancel_superseded uses to decide which workflows a pull
// request runs, so the two checks cannot disagree on what a pull-request
// workflow is.
//
// It is judged from configuration only, never from run history: a quiet
// repository whose CI is set up correctly passes.
func observeCIOnPR(r audit.Repo) observation {
	return observeTrigger(r, func(w audit.Workflow) bool { return runsOnSynchronize(w, r.DefaultBranch) })
}

// observeCIOnPush judges whether some workflow runs both on pull requests
// into the default branch and on a push to it. Without the push run, a merge of two green
// pull requests can leave the default branch red with nothing reporting it,
// and the default branch never saves a cache, so every pull request starts
// cold: GitHub scopes caches by ref, and a pull request can read only its
// own and the default branch's.
//
// Asking for both triggers on one workflow is what keeps a release workflow,
// which runs on a push to the default branch and never on a pull request,
// from passing. A merge_group trigger does not count: a merge queue run
// saves its cache under a gh-readonly-queue ref that no pull request reads,
// so it leaves the cold-cache problem where it was.
func observeCIOnPush(r audit.Repo) observation {
	return observeTrigger(r, func(w audit.Workflow) bool {
		return runsOnSynchronize(w, r.DefaultBranch) && pushAdmits(w, r.DefaultBranch)
	})
}

// observeTrigger passes when one readable workflow other than the merge-gate
// placeholder satisfies runs. Failing that, it names the first unreadable
// workflow in path order: that file may be the CI the repository needs, so a
// bare cross would claim a gap nobody has seen. There is no list of the
// others, unlike ci_cancel_superseded's, because the question is whether one
// workflow qualifies, not whether every one does.
func observeTrigger(r audit.Repo, runs func(audit.Workflow) bool) observation {
	ws := slices.Clone(r.Workflows)
	slices.SortFunc(ws, func(a, b audit.Workflow) int { return strings.Compare(a.Path, b.Path) })
	firstUnreadable := ""
	for _, w := range ws {
		switch {
		case isMergeGate(w):
			continue
		case w.ParseError != "":
			if firstUnreadable == "" {
				firstUnreadable = path.Base(w.Path)
			}
		case runs(w):
			return observation{ok: true, mark: true}
		}
	}
	if firstUnreadable != "" {
		return observation{value: "unreadable " + firstUnreadable, mark: true}
	}
	return observation{mark: true}
}

// isMergeGate reports whether a workflow is the always-green merge-gate
// placeholder, which satisfies the required check on a repository with no
// real CI and tests nothing. hasRealWorkflow names the same file from the
// directory listing; both compare against mergeGateCheck so they cannot
// disagree on which file it is.
func isMergeGate(w audit.Workflow) bool {
	return path.Base(w.Path) == mergeGateCheck+".yml"
}

// pushAdmits reports whether a workflow's push trigger runs for a push to
// branch, as GitHub applies its filters. A push trigger with no branch
// filter runs for every branch, unless a tag filter is its only filter: then
// it runs for matching tags and never for a branch. Apart from that,
// branchAdmits decides. Path filters are ignored: they narrow when the workflow runs on
// the branch, not whether it can.
func pushAdmits(w audit.Workflow, branch string) bool {
	if !slices.Contains(w.Events, "push") {
		return false
	}
	if w.PushTagFilter && len(w.PushBranches) == 0 && len(w.PushBranchesIgnore) == 0 {
		return false
	}
	return branchAdmits(w.PushBranches, w.PushBranchesIgnore, branch)
}

// branchAdmits reports whether a trigger's branch filters, branches or
// branches-ignore (GitHub allows only one of the two), admit branch. With
// neither, every branch is admitted. branches-ignore admits what none of its
// patterns match. In branches, a pattern admits what it matches and a
// !pattern excludes it, a later pattern overriding an earlier one, so the
// last that matches decides and a branch none match is not admitted.
func branchAdmits(branches, ignore []string, branch string) bool {
	switch {
	case len(branches) == 0 && len(ignore) == 0:
		return true
	case len(ignore) > 0:
		// A "!" is matched literally here: GitHub documents negation only
		// for branches, so treating it as one would admit or exclude
		// branches the workflow does not.
		return !slices.ContainsFunc(ignore, func(p string) bool { return matchFilter(p, branch) })
	}
	admitted := false
	for _, p := range branches {
		if negated, ok := strings.CutPrefix(p, "!"); ok {
			if matchFilter(negated, branch) {
				admitted = false
			}
		} else if matchFilter(p, branch) {
			admitted = true
		}
	}
	return admitted
}

// matchFilter reports whether name matches pattern in GitHub's filter
// pattern syntax
// (https://docs.github.com/en/actions/writing-workflows/workflow-syntax-for-github-actions#filter-pattern-cheat-sheet):
// ** matches any run of characters, * any run without a slash, ? and + make
// the character before them optional or repeatable, [...] is a class of
// letters, digits and ranges of them, a backslash makes the next character
// literal, and every other character is literal. The whole name must match.
// A pattern this cannot parse matches nothing, so a filter the tool does not
// understand never admits a branch it might not.
//
// It matches by hand rather than through a regular expression: branchAdmits
// runs for every pattern of every workflow each trigger check reads, so a
// compile per call would cost far more than the match, and keeping compiled
// expressions between calls would take mutable package state.
func matchFilter(pattern, name string) bool {
	tokens, ok := parseFilter(pattern)
	if !ok {
		return false
	}
	n := []rune(name)
	m := filterMatch{tokens: tokens, name: n, failed: make([]bool, (len(tokens)+1)*(len(n)+1))}
	return m.at(0, 0)
}

// filterRun is what a star stands for: nothing (the token is one character
// or class), any run without a slash (*), or any run at all (**).
type filterRun int

const (
	noRun filterRun = iota
	segmentRun
	anyRun
)

// runeRange is the characters lo through hi, inclusive.
type runeRange struct{ lo, hi rune }

// filterToken is one element of a parsed pattern: a run, or one character or
// class, which quant ('?' or '+', or 0 for neither) may qualify.
type filterToken struct {
	run    filterRun
	ranges []runeRange
	quant  rune
}

// admits reports whether one character of the name can be consumed by the
// token: for a run, one more character of it.
func (t filterToken) admits(c rune) bool {
	switch t.run {
	case segmentRun:
		return c != '/'
	case anyRun:
		return true
	case noRun:
	}
	return slices.ContainsFunc(t.ranges, func(r runeRange) bool { return r.lo <= c && c <= r.hi })
}

// parseFilter splits a filter pattern into tokens, reporting false for a
// pattern it will not read.
func parseFilter(pattern string) ([]filterToken, bool) {
	p := []rune(pattern)
	var tokens []filterToken
	// quantifiable reports that the last token is a single character or
	// class, which is all GitHub documents ? and + as applying to.
	quantifiable := false
	for i := 0; i < len(p); i++ {
		switch c := p[i]; c {
		case '*':
			run := segmentRun
			if i+1 < len(p) && p[i+1] == '*' {
				run = anyRun
				i++
			}
			tokens = append(tokens, filterToken{run: run})
			quantifiable = false
		case '?', '+':
			// doc-rot-exempt: an open question about GitHub's syntax, answered conservatively
			// TODO: GitHub does not say what ? or + mean after a star, after
			// another ? or +, or with nothing before them; such a pattern
			// matches nothing until it does.
			if !quantifiable {
				return nil, false
			}
			tokens[len(tokens)-1].quant = c
			quantifiable = false
		case '[':
			end := slices.Index(p[i+1:], ']')
			// doc-rot-exempt: an open question about GitHub's syntax, answered conservatively
			// TODO: GitHub documents a class only as letters, digits and
			// ranges of them; an unclosed, empty or other class matches
			// nothing until it says what one means.
			if end <= 0 {
				return nil, false
			}
			ranges, ok := classRanges(p[i+1 : i+1+end])
			if !ok {
				return nil, false
			}
			tokens = append(tokens, filterToken{ranges: ranges})
			i += 1 + end
			quantifiable = true
		case '\\':
			// doc-rot-exempt: an open question about GitHub's syntax, answered conservatively
			// TODO: a trailing backslash escapes nothing, and GitHub does
			// not say what it means; such a pattern matches nothing.
			if i+1 == len(p) {
				return nil, false
			}
			i++
			tokens = append(tokens, filterToken{ranges: []runeRange{{p[i], p[i]}}})
			quantifiable = true
		default:
			tokens = append(tokens, filterToken{ranges: []runeRange{{c, c}}})
			quantifiable = true
		}
	}
	return tokens, true
}

// classRanges reads the inside of a [...] class, which may hold only
// letters, digits and the hyphens of ranges, all GitHub documents. A hyphen
// between two characters makes a range, and one that cannot (first, last, or
// straight after a range) is itself a member; a range that runs backwards
// is refused, since it names no character anyone meant.
func classRanges(s []rune) ([]runeRange, bool) {
	for _, c := range s {
		switch {
		case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9', c == '-':
		default:
			return nil, false
		}
	}
	var ranges []runeRange
	for i := 0; i < len(s); {
		lo := s[i]
		i++
		if i+1 < len(s) && s[i] == '-' {
			hi := s[i+1]
			if hi < lo {
				return nil, false
			}
			ranges = append(ranges, runeRange{lo, hi})
			i += 2
			continue
		}
		ranges = append(ranges, runeRange{lo, lo})
	}
	return ranges, true
}

// filterMatch matches tokens against name. failed records each (token,
// position) pair already known not to match, so stars and + that could
// consume a character either way are tried at most once per position and
// the match stays polynomial in the pattern and name.
type filterMatch struct {
	tokens []filterToken
	name   []rune
	failed []bool
}

// at reports whether tokens[ti:] match name[ni:] in full.
func (m *filterMatch) at(ti, ni int) bool {
	if ti == len(m.tokens) {
		return ni == len(m.name)
	}
	key := ti*(len(m.name)+1) + ni
	if m.failed[key] {
		return false
	}
	t := m.tokens[ti]
	ok := false
	switch {
	case t.run != noRun:
		for k := ni; ; k++ {
			if m.at(ti+1, k) {
				ok = true
				break
			}
			if k == len(m.name) || !t.admits(m.name[k]) {
				break
			}
		}
	case t.quant == '+':
		for k := ni; k < len(m.name) && t.admits(m.name[k]); k++ {
			if m.at(ti+1, k+1) {
				ok = true
				break
			}
		}
	default:
		ok = t.quant == '?' && m.at(ti+1, ni) ||
			ni < len(m.name) && t.admits(m.name[ni]) && m.at(ti+1, ni+1)
	}
	if !ok {
		m.failed[key] = true
	}
	return ok
}
