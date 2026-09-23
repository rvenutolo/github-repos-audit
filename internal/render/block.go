package render

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/rvenutolo/github-repos-audit/internal/rules"
)

// Cell vocabulary. Everything the report says about one check is one of these
// three marks or a value.
const (
	markPass = "✓"
	markFail = "✗"
	markNA   = "n/a"
)

// footerPrefix opens the footer line. MaterialChange strips the line by this
// prefix, so the two must not drift apart.
const footerPrefix = "_Read from GitHub on "

// Block renders everything that lives between the README's generated markers.
//
// now is injected rather than read from the clock, because the renderer turns
// instants into "12d" and "4mo" — a renderer that called time.Now would make
// its own golden files rot daily.
func Block(rep *rules.Report, now func() time.Time) (string, error) {
	if rep == nil {
		return "", errors.New("render: nil report")
	}
	if now == nil {
		return "", errors.New("render: nil clock")
	}
	if rep.Owner == "" {
		return "", errors.New("render: report has no owner")
	}
	at := now().UTC()

	var b strings.Builder
	writeSection(&b, gapsSection(rep))
	writeSection(&b, metadataSection(rep))
	writeSection(&b, treeSection(rep))
	writeSection(&b, policySection(rep))
	writeSection(&b, activitySection(rep, at))
	writeSection(&b, identitiesSection(rep))
	writeSection(&b, exceptionsSection(rep))
	writeSection(&b, overridesSection(rep))
	b.WriteString(footerPrefix + at.Format(time.DateOnly) + "._\n")
	return b.String(), nil
}

func writeSection(b *strings.Builder, s string) {
	if s == "" {
		return
	}
	b.WriteString(s)
	b.WriteString("\n")
}

// gapsSection is the worklist: one bullet per failing check, naming the
// repositories. A check with no failures is omitted entirely, so an account
// with nothing wrong has no Gaps section at all.
func gapsSection(rep *rules.Report) string {
	if len(rep.Gaps) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Gaps\n\n")
	for _, g := range rep.Gaps {
		fmt.Fprintf(&b, "- **%s** (%d) — %s\n", g.Label, len(g.Repos), strings.Join(g.Repos, ", "))
	}
	return b.String()
}

func metadataSection(rep *rules.Report) string {
	t := newTable("Repository", "Type", "Visibility", "Published", "Description",
		"Homepage", "Topics", "License", "README")
	for i := range rep.Repos {
		r := &rep.Repos[i]
		t.add(
			repoLink(rep.Owner, r.Repo.Name),
			r.Repo.Type,
			r.Repo.Visibility,
			yesNo(r.Repo.Published),
			escape(r.Repo.Description),
			cell(r.Cell(rules.CheckHomepage)),
			cell(r.Cell(rules.CheckTopics)),
			cell(r.Cell(rules.CheckLicense)),
			cell(r.Cell(rules.CheckREADME)),
		)
	}
	return heading("Metadata", t)
}

func treeSection(rep *rules.Report) string {
	t := newTable("Repository", "Renovate", "Min. release age", ".gitignore", ".editorconfig",
		"flake.nix", ".justfile", "CHANGELOG", "Community files")
	for i := range rep.Repos {
		r := &rep.Repos[i]
		t.add(
			repoLink(rep.Owner, r.Repo.Name),
			cell(r.Cell(rules.CheckRenovate)),
			cell(r.Cell(rules.CheckRenovateMinReleaseAge)),
			cell(r.Cell(rules.CheckGitignore)),
			cell(r.Cell(rules.CheckEditorconfig)),
			cell(r.Cell(rules.CheckFlakeNix)),
			cell(r.Cell(rules.CheckJustfile)),
			cell(r.Cell(rules.CheckChangelog)),
			cell(r.Cell(rules.CheckCommunityFiles)),
		)
	}
	return heading("Tree", t)
}

func policySection(rep *rules.Report) string {
	t := newTable("Repository", "Direct push", "Signed", "Identity", "CI workflows", "Required checks",
		"Tag ruleset", "Secret scanning", "Vuln. reporting")
	for i := range rep.Repos {
		r := &rep.Repos[i]
		t.add(
			repoLink(rep.Owner, r.Repo.Name),
			cell(r.Cell(rules.CheckDirectPush)),
			cell(r.Cell(rules.CheckSignedCommits)),
			cell(r.Cell(rules.CheckGitIdentity)),
			cell(r.Cell(rules.CheckCIWorkflows)),
			cell(r.Cell(rules.CheckRequiredChecks)),
			cell(r.Cell(rules.CheckTagRuleset)),
			cell(r.Cell(rules.CheckSecretScanning)),
			cell(r.Cell(rules.CheckVulnReporting)),
		)
	}
	return heading("Policy", t)
}

func activitySection(rep *rules.Report, at time.Time) string {
	t := newTable("Repository", "Last push", "CI", "Open PRs", "Branches", "Releases", "Last release")
	for i := range rep.Repos {
		r := &rep.Repos[i]
		t.add(
			repoLink(rep.Owner, r.Repo.Name),
			dateCell(r.Cell(rules.CheckLastPush), at),
			cell(r.Cell(rules.CheckCIGreen)),
			cell(r.Cell(rules.CheckOpenPRs)),
			cell(r.Cell(rules.CheckBranches)),
			cell(r.Cell(rules.CheckReleases)),
			dateCell(r.Cell(rules.CheckLastReleaseAge), at),
		)
	}
	return heading("Activity", t)
}

// identitiesSection lists, for every repository with any of the account
// holder's identities, which ones its history carries: the working list for a
// history rewrite. The Policy column says whether a repository is wrong; this
// says what to rewrite from. It is omitted when no repository has any of the
// owner's identities, which includes a report with no identity standard.
func identitiesSection(rep *rules.Report) string {
	if len(rep.Identities) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Git identities\n\n")
	for _, line := range rep.Identities {
		parts := make([]string, 0, len(line.Identities))
		for _, id := range line.Identities {
			p := code(id.String())
			if id.Canonical {
				p += " (canonical)"
			}
			parts = append(parts, p)
		}
		fmt.Fprintf(&b, "- **%s** — %s", line.Repo, strings.Join(parts, ", "))
		switch {
		case line.Mixed:
			b.WriteString(" — mixed")
		case !line.Clean:
			b.WriteString(" — not canonical")
		}
		b.WriteString("\n")
	}
	return b.String()
}

// exceptionsSection groups the deviations by repository, so a repository that
// differs on three settings reads as one line rather than three.
func exceptionsSection(rep *rules.Report) string {
	if len(rep.Exceptions) == 0 && len(rep.NoConsensus) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Settings exceptions\n\n")

	if len(rep.Exceptions) == 0 {
		b.WriteString("No repository deviates from the account's usual settings.\n")
	}
	for i := 0; i < len(rep.Exceptions); {
		repo := rep.Exceptions[i].Repo
		var parts []string
		for ; i < len(rep.Exceptions) && rep.Exceptions[i].Repo == repo; i++ {
			e := rep.Exceptions[i]
			parts = append(parts, fmt.Sprintf("%s %s, usually %s", e.Setting, code(e.Value), code(e.Modal)))
		}
		fmt.Fprintf(&b, "- **%s** — %s\n", repo, strings.Join(parts, "; "))
	}

	if len(rep.NoConsensus) > 0 {
		b.WriteString("\n### No consensus\n\n")
		b.WriteString("The most common value covers half or fewer of the repositories these apply to,\n")
		b.WriteString("so there is no norm to measure against.\n\n")
		for _, s := range rep.NoConsensus {
			fmt.Fprintf(&b, "- %s\n", s)
		}
	}
	return b.String()
}

// overridesSection lists every declared override, so an excused gap is visible
// rather than silently absent.
func overridesSection(rep *rules.Report) string {
	if len(rep.Overrides) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Overrides\n\n")
	for _, o := range rep.Overrides {
		fmt.Fprintf(&b, "- **%s** — %s is %s\n", o.Repo, code(o.Check.String()), code(o.Value))
	}
	return b.String()
}

func heading(title string, t *table) string {
	body := t.String()
	if body == "" {
		return ""
	}
	return "## " + title + "\n\n" + body
}

// repoLink links a repository name to its page on GitHub.
func repoLink(owner, name string) string {
	return "[" + name + "](https://github.com/" + owner + "/" + name + ")"
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// cell renders one check's outcome. A value replaces the mark, except on the
// direct-push row, where the value alone would hide the verdict and the
// verdict alone would hide which way round the repository is.
func cell(c rules.Cell) string {
	value := c.Value
	if c.Code {
		value = code(value)
	}

	mark := ""
	switch c.Verdict {
	case rules.Pass:
		mark = markPass
	case rules.Fail:
		mark = markFail
	case rules.NA:
		// An n/a cell that carries a scalar still shows it: the report
		// withholds the judgement, not the fact. A present-or-absent check
		// renders n/a instead, because a bare cross on a non-gap cell is
		// indistinguishable from a gap in the same column — which is the
		// confusion the whole n/a scheme exists to prevent.
		if c.Scalar && value != "" {
			return value
		}
		return markNA
	case rules.Info:
		if value == "" {
			return markNA
		}
		return value
	}

	switch {
	case c.Mark && value != "":
		return mark + " " + value
	case value != "":
		return value
	default:
		return mark
	}
}

// dateCell renders an instant as an age against the render time.
func dateCell(c rules.Cell, at time.Time) string {
	if c.Verdict == rules.NA && c.At.IsZero() {
		return markNA
	}
	if c.At.IsZero() {
		return "never"
	}
	return age(c.At, at)
}

// age is the relative date vocabulary: "today", "12d", "4mo", "3y". Deliberately
// coarse — there is no staleness threshold on any of these, so the number is
// shown to be read rather than compared against anything.
func age(then, now time.Time) string {
	days := int(now.Sub(then).Hours() / 24)
	switch {
	case days < 0:
		// A push timestamped in the future is a clock skew, not an age.
		return "today"
	case days < 1:
		return "today"
	case days < 60:
		return strconv.Itoa(days) + "d"
	case days < 730:
		return strconv.Itoa(days/30) + "mo"
	default:
		return strconv.Itoa(days/365) + "y"
	}
}
