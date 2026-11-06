package rules

import (
	"slices"
	"strings"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
)

// NoDevshellWrapper is the problem both devshell checks report for a judged
// repository without .ci/in-devshell. The report renders it as plain text, so
// the renderer matches on this constant.
const NoDevshellWrapper = "no .ci/in-devshell"

// DevshellTooManyFiles is the devshell_scripts problem for a repository with
// more candidate scripts than the tool reads. The report renders it as plain
// text, so the renderer matches on this constant.
const DevshellTooManyFiles = "too many files"

// exempted reports whether s carries a devshell-exempt marker with a reason.
// A marker with an empty reason is recorded as found but excuses nothing.
func exempted(s audit.DevshellScript) bool {
	return slices.ContainsFunc(s.Exempt, func(reason string) bool { return reason != "" })
}

// devshellJudged reports whether the devshell checks have anything to judge:
// a script nobody excused, an unreadable candidate, or too many candidates to
// read. Unreadable and too-many count because either may hide a script. A
// repository whose only scripts are exempt has no use for the wrapper, so it
// is treated like one with no scripts.
func devshellJudged(d audit.Devshell) bool {
	return d.TooManyFiles || slices.ContainsFunc(d.Scripts, func(s audit.DevshellScript) bool { return !exempted(s) })
}

// selfWraps reports whether a script tests IN_DEVSHELL and execs the wrapper.
// Both are needed: the test alone never re-enters, and the exec alone loops.
func selfWraps(s audit.DevshellScript) bool {
	return len(s.InDevshellTest) > 0 && len(s.ExecInDevshell) > 0
}

// failingScripts is the path of every script that does not self-wrap, in path
// order. An unreadable script and one whose exempt marker has no reason are
// reported under their own texts instead, so they are left out here.
func failingScripts(d audit.Devshell) []string {
	var out []string
	for _, s := range d.Scripts {
		if s.Unreadable == "" && len(s.Exempt) == 0 && !selfWraps(s) {
			out = append(out, s.Path)
		}
	}
	slices.Sort(out)
	return out
}

// devshellScriptProblems lists what fails devshell_scripts, in the order a
// reader fixes them: too many files (which hides everything else), the
// missing wrapper, each script that does not self-wrap, each exemption
// without a reason, then each unreadable script. It also reports whether
// anything was judged, which is what makes the check apply.
func devshellScriptProblems(r audit.Repo) (problems []string, judged bool) {
	d := r.Devshell
	judged = devshellJudged(d)
	if d.TooManyFiles {
		return []string{DevshellTooManyFiles}, judged
	}
	if judged && d.Wrapper == nil {
		problems = append(problems, NoDevshellWrapper)
	}
	problems = append(problems, failingScripts(d)...)
	var reasonless, unreadable []string
	for _, s := range d.Scripts {
		switch {
		case s.Unreadable != "":
			unreadable = append(unreadable, s.Path)
		case len(s.Exempt) > 0 && !exempted(s):
			reasonless = append(reasonless, s.Path+": exempt without a reason")
		}
	}
	slices.Sort(reasonless)
	problems = append(problems, reasonless...)
	return appendFiles(problems, "unreadable ", unreadable), judged
}

// devshellWrapperProblems lists what fails devshell_tmp_cleanup: a missing
// .ci/in-devshell, an unreadable one, or each marker of a wrapper that
// removes its temp directory that its text lacks. It is judged when the
// wrapper exists or the scripts need one.
func devshellWrapperProblems(r audit.Repo) (problems []string, judged bool) {
	w := r.Devshell.Wrapper
	judged = w != nil || devshellJudged(r.Devshell)
	switch {
	case w == nil:
		if judged {
			problems = []string{NoDevshellWrapper}
		}
	case w.Unreadable != "":
		problems = []string{"unreadable"}
	default:
		for _, m := range []struct {
			lines   []string
			problem string
		}{
			{w.Wait, "no wait for nix develop"},
			{w.Mktemp, "no mktemp --directory"},
			{w.KeepTmpdir, "TMPDIR not kept"},
			{w.ExitTrap, "no EXIT trap"},
		} {
			if len(m.lines) == 0 {
				problems = append(problems, m.problem)
			}
		}
	}
	return problems, judged
}

// observeDevshellScripts judges whether every shell script nobody excused
// enters the Nix devShell itself. It is n/a without a script to judge. The
// cell counts the scripts when they are the only problem, and otherwise
// names the problems, since a missing wrapper or an unreadable file is not
// a script to count.
func observeDevshellScripts(r audit.Repo) observation {
	problems, judged := devshellScriptProblems(r)
	switch {
	case !judged:
		return observation{unknown: true}
	case len(problems) == 0:
		return observation{ok: true}
	}
	if n := len(failingScripts(r.Devshell)); n == len(problems) {
		return observation{value: countOf(n, "script"), mark: true}
	}
	return observation{value: strings.Join(problems, ", "), mark: true}
}

// observeDevshellTmpCleanup judges whether .ci/in-devshell removes the temp
// directory it makes. It is n/a with no wrapper and nothing that needs one.
func observeDevshellTmpCleanup(r audit.Repo) observation {
	problems, judged := devshellWrapperProblems(r)
	if !judged {
		return observation{unknown: true}
	}
	return observation{ok: len(problems) == 0, value: strings.Join(problems, ", "), mark: true}
}
