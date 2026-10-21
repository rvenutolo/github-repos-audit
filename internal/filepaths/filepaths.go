// Package filepaths matches a repository's tree listing against the file
// names each tool accepts for its configuration. It is pure: it decides which
// listed path, if any, is the file for each tool and nothing else, so the
// accepted-name tables are testable without a network.
package filepaths

import (
	"slices"
	"sort"
	"strings"
)

// Entry is one path in a repository's tree at HEAD, slash-separated, relative
// to the root.
type Entry struct {
	Path string
	Dir  bool
}

// Paths is where each file was found; "" means absent. The path is reported
// as it appears in the tree (so case-insensitive matches keep their spelling),
// and a directory match carries a trailing slash. Compose lists the distinct
// compose file base names found at any depth, sorted. GoMod reports a root
// file named exactly go.mod: the module a root `go` command builds, which a
// nested module or a directory of that name is not.
type Paths struct {
	Justfile, Commitlint, GitCliff, Treefmt                                                       string
	Compose                                                                                       []string
	Typos, Yamllint, Markdownlint, Prettier, EditorconfigChecker, Lychee, Zizmor, Codecov, MkDocs string
	LicenseFile, SecurityPolicy, Codeowners, PRTemplate, ClaudeMD                                 string
	GoMod                                                                                         bool
}

// Every table below is in lookup order: when several accepted names exist the
// first one in the table is reported, which is also the tool's own precedence
// where the tool has one. Matching is exact unless a table is documented as
// case-insensitive.

// justfileNames: casey/just src/search.rs. just matches the file name
// case-insensitively and itself errors when two names match, so the order here
// is only a deterministic tie-break.
func justfileNames() []string { return []string{"justfile", ".justfile"} }

// commitlintNames: commitlint @commitlint/load src/utils/load-config.ts (the
// cosmiconfig search places).
func commitlintNames() []string {
	return []string{
		".commitlintrc", ".commitlintrc.json", ".commitlintrc.yaml", ".commitlintrc.yml",
		".commitlintrc.js", ".commitlintrc.cjs", ".commitlintrc.mjs",
		"commitlint.config.js", "commitlint.config.cjs", "commitlint.config.mjs",
		".commitlintrc.ts", ".commitlintrc.cts", ".commitlintrc.mts",
		"commitlint.config.ts", "commitlint.config.cts", "commitlint.config.mts",
	}
}

// gitCliffNames: git-cliff-core CONFIG_FILES.
func gitCliffNames() []string { return []string{"cliff.toml", ".cliff.toml", ".config/cliff.toml"} }

// treefmtNames: numtide/treefmt cmd/root.go for the .toml names; the two .nix
// names are the treefmt-nix convention.
func treefmtNames() []string {
	return []string{
		"treefmt.toml", ".treefmt.toml", ".config/treefmt.toml", "treefmt.nix", ".treefmt.nix",
	}
}

// composeNames: compose-spec/compose-go cli/options.go. Compose files live one
// per stack, so they are collected at any depth rather than looked up.
func composeNames() []string {
	return []string{"compose.yaml", "compose.yml", "docker-compose.yml", "docker-compose.yaml"}
}

// typosNames: crate-ci/typos config.rs.
func typosNames() []string { return []string{"typos.toml", "_typos.toml", ".typos.toml"} }

// yamllintNames: yamllint cli.py.
func yamllintNames() []string { return []string{".yamllint", ".yamllint.yaml", ".yamllint.yml"} }

// markdownlintNames: markdownlint-cli2 README (options files, then config
// files), then markdownlint-cli markdownlint.js (.markdownlintrc).
func markdownlintNames() []string {
	return []string{
		".markdownlint-cli2.jsonc", ".markdownlint-cli2.yaml", ".markdownlint-cli2.cjs", ".markdownlint-cli2.mjs",
		".markdownlint.jsonc", ".markdownlint.json", ".markdownlint.yaml", ".markdownlint.yml",
		".markdownlint.cjs", ".markdownlint.mjs",
		".markdownlintrc",
	}
}

// prettierNames: https://prettier.io/docs/configuration
func prettierNames() []string {
	return []string{
		".prettierrc", ".prettierrc.json", ".prettierrc.yml", ".prettierrc.yaml", ".prettierrc.json5",
		".prettierrc.js", "prettier.config.js", ".prettierrc.ts", "prettier.config.ts",
		".prettierrc.mjs", "prettier.config.mjs", ".prettierrc.mts", "prettier.config.mts",
		".prettierrc.cjs", "prettier.config.cjs", ".prettierrc.cts", "prettier.config.cts",
		".prettierrc.toml",
	}
}

// editorconfigCheckerNames: defaultConfigFileNames in
// https://github.com/editorconfig-checker/editorconfig-checker
// cmd/editorconfig-checker/main.go. The source reads only this name, so
// .ecrc is not accepted: where docs and source disagree, source wins.
func editorconfigCheckerNames() []string { return []string{".editorconfig-checker.json"} }

// lycheeNames: lycheeverse/lychee config/loaders/mod.rs.
func lycheeNames() []string { return []string{"lychee.toml"} }

// zizmorNames: https://docs.zizmor.sh/configuration
func zizmorNames() []string {
	return []string{".github/zizmor.yml", ".github/zizmor.yaml", "zizmor.yml", "zizmor.yaml"}
}

// codecovNames and codecovDirs: determine_commit_yaml_location in
// https://github.com/codecov/shared shared/yaml/fetcher.py searches the root
// first, then dev/ and .github/ (an unordered set in the source, so the order
// here is a deterministic tie-break), for these four names.
func codecovNames() []string {
	return []string{"codecov.yml", ".codecov.yml", "codecov.yaml", ".codecov.yaml"}
}

func codecovDirs() []string { return []string{"", "dev/", ".github/"} }

// mkdocsNames: the default config lookup (paths_to_try) in
// https://github.com/mkdocs/mkdocs mkdocs/config/base.py.
func mkdocsNames() []string { return []string{"mkdocs.yml", "mkdocs.yaml"} }

// securityNames: GitHub community health file docs
// https://docs.github.com/en/code-security/getting-started/adding-a-security-policy-to-your-repository
// Matched case-insensitively.
func securityNames() []string {
	return []string{".github/SECURITY.md", "SECURITY.md", "docs/SECURITY.md"}
}

// codeownersNames: https://docs.github.com/en/repositories/managing-your-repositorys-settings-and-features/customizing-your-repository/about-code-owners
func codeownersNames() []string {
	return []string{".github/CODEOWNERS", "CODEOWNERS", "docs/CODEOWNERS"}
}

// prTemplateDirs: https://docs.github.com/en/communities/using-templates-to-encourage-useful-issues-and-pull-requests/creating-a-pull-request-template-for-your-repository
// in GitHub's lookup order.
func prTemplateDirs() []string { return []string{".github/", "", "docs/"} }

// claudeMDNames: Claude Code memory docs, https://docs.claude.com/en/docs/claude-code/memory
func claudeMDNames() []string { return []string{"CLAUDE.md", ".claude/CLAUDE.md"} }

const (
	// goModName: the Go command's module file, matched exactly; Go itself
	// reads no other spelling.
	goModName      = "go.mod"
	prTemplateName = "pull_request_template"
	licensesDir    = "LICENSES"
)

// Find matches entries against every tool's accepted names. Only the root and
// each tool's named subdirectories are searched, except compose files.
func Find(entries []Entry) Paths {
	idx := newIndex(entries)
	return Paths{
		Justfile:            idx.first(justfileNames(), true),
		Commitlint:          idx.first(commitlintNames(), false),
		GitCliff:            idx.first(gitCliffNames(), false),
		Treefmt:             idx.first(treefmtNames(), false),
		Compose:             idx.compose(),
		Typos:               idx.first(typosNames(), false),
		Yamllint:            idx.first(yamllintNames(), false),
		Markdownlint:        idx.first(markdownlintNames(), false),
		Prettier:            idx.first(prettierNames(), false),
		EditorconfigChecker: idx.first(editorconfigCheckerNames(), false),
		Lychee:              idx.first(lycheeNames(), false),
		Zizmor:              idx.first(zizmorNames(), false),
		Codecov:             idx.codecov(),
		MkDocs:              idx.first(mkdocsNames(), false),
		LicenseFile:         idx.license(),
		SecurityPolicy:      idx.first(securityNames(), true),
		Codeowners:          idx.first(codeownersNames(), false),
		PRTemplate:          idx.prTemplate(),
		ClaudeMD:            idx.first(claudeMDNames(), false),
		GoMod:               idx.file(goModName, false) != "",
	}
}

// index holds file and directory paths separately because a directory never
// satisfies a file name and a file never satisfies a directory name.
type index struct {
	files []string
	dirs  []string
	set   map[string]struct{}
}

func newIndex(entries []Entry) index {
	idx := index{set: make(map[string]struct{}, len(entries))}
	for _, e := range entries {
		if e.Dir {
			idx.dirs = append(idx.dirs, e.Path)
			continue
		}
		idx.files = append(idx.files, e.Path)
		idx.set[e.Path] = struct{}{}
	}
	// A stable order makes ties between several matching paths deterministic
	// regardless of the order the API listed them in.
	sort.Strings(idx.files)
	sort.Strings(idx.dirs)
	return idx
}

// first returns the file matching the earliest name in names. With fold, the
// match ignores case and returns the path as listed.
func (x index) first(names []string, fold bool) string {
	for _, n := range names {
		if p := x.file(n, fold); p != "" {
			return p
		}
	}
	return ""
}

func (x index) file(name string, fold bool) string {
	if !fold {
		if _, ok := x.set[name]; ok {
			return name
		}
		return ""
	}
	for _, p := range x.files {
		if strings.EqualFold(p, name) {
			return p
		}
	}
	return ""
}

func (x index) codecov() string {
	for _, dir := range codecovDirs() {
		for _, n := range codecovNames() {
			if p := x.file(dir+n, false); p != "" {
				return p
			}
		}
	}
	return ""
}

// compose returns the distinct base names of compose files at any depth.
func (x index) compose() []string {
	var out []string
	for _, p := range x.files {
		base := p[strings.LastIndex(p, "/")+1:]
		if slices.Contains(composeNames(), base) && !slices.Contains(out, base) {
			out = append(out, base)
		}
	}
	sort.Strings(out)
	return out
}

// license follows licensee's project_files/license_file.rb score order at the
// root, case-insensitively: bare LICENSE/LICENCE/UNLICENSE, the same with a
// documentation extension, COPYING, COPYING.<ext>, LICENSE.<other ext>,
// LICENSE-* / LICENSE_*, COPYING-*. A REUSE LICENSES/ directory is the last
// resort and is reported with a trailing slash.
func (x index) license() string {
	rank := func(name string) int {
		stem, ext := splitExt(strings.ToLower(name))
		switch {
		case isLicenseStem(stem) && ext == "":
			return 1
		case isLicenseStem(stem) && slices.Contains([]string{".md", ".markdown", ".txt", ".html"}, ext):
			return 2
		case stem == "copying" && ext == "":
			return 3
		case stem == "copying":
			return 4
		case isLicenseStem(stem) && !slices.Contains([]string{".spdx", ".header"}, ext):
			return 5
		}
		lower := strings.ToLower(name)
		switch {
		case hasAnyPrefix(lower, "license-", "license_", "licence-", "licence_"):
			return 6
		case strings.HasPrefix(lower, "copying-"):
			return 7
		}
		return 0
	}
	best, bestRank := "", 0
	for _, p := range x.files {
		if strings.Contains(p, "/") {
			continue
		}
		if r := rank(p); r != 0 && (bestRank == 0 || r < bestRank) {
			best, bestRank = p, r
		}
	}
	if best != "" {
		return best
	}
	for _, d := range x.dirs {
		if d == licensesDir {
			return d + "/"
		}
	}
	return ""
}

func isLicenseStem(s string) bool {
	return s == "license" || s == "licence" || s == "unlicense"
}

func hasAnyPrefix(s string, prefixes ...string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// splitExt splits at the first dot, so "license.md" is ("license", ".md") and
// a name with no dot has an empty extension.
func splitExt(name string) (string, string) {
	if i := strings.Index(name, "."); i > 0 {
		return name[:i], name[i:]
	}
	return name, ""
}

// prTemplate searches .github/, the root, then docs/ for a file named
// pull_request_template with any extension or none, or a directory of that
// name, case-insensitively. Within one location a file beats the directory.
func (x index) prTemplate() string {
	for _, dir := range prTemplateDirs() {
		for _, p := range x.files {
			if rest, ok := strings.CutPrefix(p, dir); ok && !strings.Contains(rest, "/") && isPRTemplate(rest, true) {
				return p
			}
		}
		for _, p := range x.dirs {
			if rest, ok := strings.CutPrefix(p, dir); ok && !strings.Contains(rest, "/") && isPRTemplate(rest, false) {
				return p + "/"
			}
		}
	}
	return ""
}

func isPRTemplate(base string, allowExt bool) bool {
	lower := strings.ToLower(base)
	if lower == prTemplateName {
		return true
	}
	return allowExt && strings.HasPrefix(lower, prTemplateName+".")
}
