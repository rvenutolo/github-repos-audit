package filepaths_test

import (
	"reflect"
	"testing"

	"github.com/rvenutolo/github-repos-audit/internal/filepaths"
)

func files(paths ...string) []filepaths.Entry {
	out := make([]filepaths.Entry, 0, len(paths))
	for _, p := range paths {
		out = append(out, filepaths.Entry{Path: p})
	}
	return out
}

type fieldCase struct {
	name    string
	entries []filepaths.Entry
	want    string
}

func runField(t *testing.T, get func(filepaths.Paths) string, cases []fieldCase) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := get(filepaths.Find(c.entries)); got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}

// each accepts every name alone and, for the given pair, prefers the first.
func accepted(names ...string) []fieldCase {
	cs := make([]fieldCase, 0, len(names)+4)
	for _, n := range names {
		cs = append(cs, fieldCase{"alone " + n, files(n), n})
	}
	return cs
}

func reversePair(a, b string) fieldCase {
	return fieldCase{"order " + a + " over " + b, files(b, a), a}
}

func TestFind_justfile(t *testing.T) {
	t.Parallel()
	cs := accepted("justfile", ".justfile")
	cs = append(cs, reversePair("justfile", ".justfile"))
	runField(t, func(p filepaths.Paths) string { return p.Justfile }, cs)
}

func TestFind_commitlint(t *testing.T) {
	t.Parallel()
	names := []string{
		".commitlintrc", ".commitlintrc.json", ".commitlintrc.yaml", ".commitlintrc.yml",
		".commitlintrc.js", ".commitlintrc.cjs", ".commitlintrc.mjs",
		"commitlint.config.js", "commitlint.config.cjs", "commitlint.config.mjs",
		".commitlintrc.ts", ".commitlintrc.cts", ".commitlintrc.mts",
		"commitlint.config.ts", "commitlint.config.cts", "commitlint.config.mts",
	}
	cs := accepted(names...)
	cs = append(cs, reversePair(".commitlintrc", "commitlint.config.ts"))
	runField(t, func(p filepaths.Paths) string { return p.Commitlint }, cs)
}

func TestFind_gitCliff(t *testing.T) {
	t.Parallel()
	cs := accepted("cliff.toml", ".cliff.toml", ".config/cliff.toml")
	cs = append(cs, reversePair("cliff.toml", ".config/cliff.toml"))
	runField(t, func(p filepaths.Paths) string { return p.GitCliff }, cs)
}

func TestFind_treefmt(t *testing.T) {
	t.Parallel()
	cs := accepted("treefmt.toml", ".treefmt.toml", ".config/treefmt.toml", "treefmt.nix", ".treefmt.nix")
	cs = append(cs, reversePair("treefmt.toml", ".treefmt.nix"))
	runField(t, func(p filepaths.Paths) string { return p.Treefmt }, cs)
}

func TestFind_typos(t *testing.T) {
	t.Parallel()
	cs := accepted("typos.toml", "_typos.toml", ".typos.toml")
	cs = append(cs, reversePair("typos.toml", ".typos.toml"))
	runField(t, func(p filepaths.Paths) string { return p.Typos }, cs)
}

func TestFind_yamllint(t *testing.T) {
	t.Parallel()
	cs := accepted(".yamllint", ".yamllint.yaml", ".yamllint.yml")
	cs = append(cs, reversePair(".yamllint", ".yamllint.yml"))
	runField(t, func(p filepaths.Paths) string { return p.Yamllint }, cs)
}

func TestFind_markdownlint(t *testing.T) {
	t.Parallel()
	names := []string{
		".markdownlint-cli2.jsonc", ".markdownlint-cli2.yaml", ".markdownlint-cli2.cjs", ".markdownlint-cli2.mjs",
		".markdownlint.jsonc", ".markdownlint.json", ".markdownlint.yaml", ".markdownlint.yml",
		".markdownlint.cjs", ".markdownlint.mjs", ".markdownlintrc",
	}
	cs := accepted(names...)
	cs = append(cs, reversePair(".markdownlint-cli2.jsonc", ".markdownlintrc"))
	runField(t, func(p filepaths.Paths) string { return p.Markdownlint }, cs)
}

func TestFind_prettier(t *testing.T) {
	t.Parallel()
	names := []string{
		".prettierrc", ".prettierrc.json", ".prettierrc.yml", ".prettierrc.yaml", ".prettierrc.json5",
		".prettierrc.js", "prettier.config.js", ".prettierrc.ts", "prettier.config.ts",
		".prettierrc.mjs", "prettier.config.mjs", ".prettierrc.mts", "prettier.config.mts",
		".prettierrc.cjs", "prettier.config.cjs", ".prettierrc.cts", "prettier.config.cts", ".prettierrc.toml",
	}
	cs := accepted(names...)
	cs = append(cs, reversePair(".prettierrc", ".prettierrc.toml"))
	runField(t, func(p filepaths.Paths) string { return p.Prettier }, cs)
}

func TestFind_editorconfigChecker(t *testing.T) {
	t.Parallel()
	cs := accepted(".editorconfig-checker.json")
	// The tool's source reads only this one name; .ecrc is not read.
	cs = append(cs, fieldCase{"ecrc is not accepted", files(".ecrc"), ""})
	runField(t, func(p filepaths.Paths) string { return p.EditorconfigChecker }, cs)
}

func TestFind_lychee(t *testing.T) {
	t.Parallel()
	runField(t, func(p filepaths.Paths) string { return p.Lychee }, accepted("lychee.toml"))
}

func TestFind_zizmor(t *testing.T) {
	t.Parallel()
	cs := accepted(".github/zizmor.yml", ".github/zizmor.yaml", "zizmor.yml", "zizmor.yaml")
	cs = append(cs, reversePair(".github/zizmor.yml", "zizmor.yaml"))
	runField(t, func(p filepaths.Paths) string { return p.Zizmor }, cs)
}

func TestFind_codecov(t *testing.T) {
	t.Parallel()
	names := make([]string, 0, 12)
	for _, dir := range []string{"", "dev/", ".github/"} {
		for _, n := range []string{"codecov.yml", ".codecov.yml", "codecov.yaml", ".codecov.yaml"} {
			names = append(names, dir+n)
		}
	}
	cs := accepted(names...)
	cs = append(cs,
		reversePair("codecov.yml", ".codecov.yaml"),
		reversePair(".codecov.yaml", "dev/codecov.yml"),
		reversePair("dev/codecov.yml", ".github/codecov.yml"),
	)
	runField(t, func(p filepaths.Paths) string { return p.Codecov }, cs)
}

func TestFind_mkdocs(t *testing.T) {
	t.Parallel()
	cs := accepted("mkdocs.yml", "mkdocs.yaml")
	cs = append(cs, reversePair("mkdocs.yml", "mkdocs.yaml"))
	runField(t, func(p filepaths.Paths) string { return p.MkDocs }, cs)
}

func TestFind_licenseFile(t *testing.T) {
	t.Parallel()
	cs := accepted("LICENSE", "LICENCE", "UNLICENSE", "LICENSE.md", "LICENSE.markdown", "LICENSE.txt",
		"LICENSE.html", "COPYING", "COPYING.md", "LICENSE.rst", "LICENSE-MIT", "LICENSE_APACHE", "COPYING-GPL")
	runField(t, func(p filepaths.Paths) string { return p.LicenseFile }, cs)
}

func TestFind_licenseOrder(t *testing.T) {
	t.Parallel()
	runField(t, func(p filepaths.Paths) string { return p.LicenseFile }, []fieldCase{
		{"extensionless beats extension", files("LICENSE.txt", "LICENSE"), "LICENSE"},
		{"license beats copying", files("COPYING", "LICENSE.txt"), "LICENSE.txt"},
		{"copying ext beats license-suffix", files("LICENSE-MIT", "COPYING.md"), "COPYING.md"},
		{"spdx is not a license file", files("LICENSE.spdx"), ""},
		{"header is not a license file", files("LICENSE.header"), ""},
		{"other ext beats suffix", files("LICENSE-MIT", "LICENSE.rst"), "LICENSE.rst"},
		{"suffix beats copying suffix", files("COPYING-GPL", "LICENSE-MIT"), "LICENSE-MIT"},
		{"file beats LICENSES dir", []filepaths.Entry{{Path: "LICENSES", Dir: true}, {Path: "COPYING-GPL"}}, "COPYING-GPL"},
		{"root only", files("sub/LICENSE", "docs/COPYING"), ""},
	})
}

func TestFind_securityPolicy(t *testing.T) {
	t.Parallel()
	cs := accepted(".github/SECURITY.md", "SECURITY.md", "docs/SECURITY.md")
	cs = append(cs, reversePair(".github/SECURITY.md", "docs/SECURITY.md"))
	runField(t, func(p filepaths.Paths) string { return p.SecurityPolicy }, cs)
}

func TestFind_codeowners(t *testing.T) {
	t.Parallel()
	cs := accepted(".github/CODEOWNERS", "CODEOWNERS", "docs/CODEOWNERS")
	cs = append(cs, reversePair(".github/CODEOWNERS", "docs/CODEOWNERS"),
		fieldCase{"codeowners is case-sensitive", files(".github/codeowners"), ""})
	runField(t, func(p filepaths.Paths) string { return p.Codeowners }, cs)
}

func TestFind_prTemplate(t *testing.T) {
	t.Parallel()
	cs := accepted(".github/PULL_REQUEST_TEMPLATE.md", "pull_request_template.md", "docs/pull_request_template.md",
		".github/pull_request_template", "docs/Pull_Request_Template.txt")
	runField(t, func(p filepaths.Paths) string { return p.PRTemplate }, cs)
}

func TestFind_prTemplateLocationOrder(t *testing.T) {
	t.Parallel()
	runField(t, func(p filepaths.Paths) string { return p.PRTemplate }, []fieldCase{
		{".github beats root", files("pull_request_template.md", ".github/pull_request_template.md"), ".github/pull_request_template.md"},
		{"root beats docs", files("docs/pull_request_template.md", "pull_request_template.md"), "pull_request_template.md"},
		{".github beats docs", files("docs/pull_request_template.md", ".github/pull_request_template.md"), ".github/pull_request_template.md"},
		{"other names do not match", files(".github/pull_request_template_old.md", ".github/other.md"), ""},
	})
}

func TestFind_claudeMD(t *testing.T) {
	t.Parallel()
	cs := accepted("CLAUDE.md", ".claude/CLAUDE.md")
	cs = append(cs, reversePair("CLAUDE.md", ".claude/CLAUDE.md"),
		fieldCase{"claude.md is case-sensitive", files("claude.md"), ""})
	runField(t, func(p filepaths.Paths) string { return p.ClaudeMD }, cs)
}

func TestFind_goMod(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		entries []filepaths.Entry
		want    bool
	}{
		{"root go.mod", files("go.mod"), true},
		{"nested go.mod is another module", files("sub/go.mod"), false},
		{"exact name only", files("Go.mod"), false},
		{"a directory is not the file", []filepaths.Entry{{Path: "go.mod", Dir: true}}, false},
		{"absent", files("go.sum"), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := filepaths.Find(tc.entries).GoMod; got != tc.want {
				t.Fatalf("GoMod = %t, want %t", got, tc.want)
			}
		})
	}
}

func TestFind_caseInsensitive(t *testing.T) {
	t.Parallel()
	runField(t, func(p filepaths.Paths) string { return p.Justfile }, []fieldCase{
		{"Justfile", files("Justfile"), "Justfile"},
		{".JUSTFILE", files(".JUSTFILE"), ".JUSTFILE"},
	})
	runField(t, func(p filepaths.Paths) string { return p.LicenseFile }, []fieldCase{
		{"license.md", files("license.md"), "license.md"},
	})
	runField(t, func(p filepaths.Paths) string { return p.SecurityPolicy }, []fieldCase{
		{".github/security.md", files(".github/security.md"), ".github/security.md"},
	})
	runField(t, func(p filepaths.Paths) string { return p.PRTemplate }, []fieldCase{
		{"docs Pull_Request_Template.txt", files("docs/Pull_Request_Template.txt"), "docs/Pull_Request_Template.txt"},
	})
	runField(t, func(p filepaths.Paths) string { return p.Yamllint }, []fieldCase{
		{".Yamllint exact only", files(".Yamllint"), ""},
	})
	runField(t, func(p filepaths.Paths) string { return p.GitCliff }, []fieldCase{
		{"Cliff.toml exact only", files("Cliff.toml"), ""},
	})
}

func TestFind_directoriesAreNotFiles(t *testing.T) {
	t.Parallel()
	dir := func(p string) []filepaths.Entry { return []filepaths.Entry{{Path: p, Dir: true}} }
	runField(t, func(p filepaths.Paths) string { return p.Justfile }, []fieldCase{
		{"dir named justfile", dir("justfile"), ""},
	})
	runField(t, func(p filepaths.Paths) string { return p.LicenseFile }, []fieldCase{
		{"LICENSES dir", dir("LICENSES"), "LICENSES/"},
		{"LICENSES file", files("LICENSES"), ""},
		{"LICENSE dir", dir("LICENSE"), ""},
	})
	runField(t, func(p filepaths.Paths) string { return p.PRTemplate }, []fieldCase{
		{"template dir", dir(".github/PULL_REQUEST_TEMPLATE"), ".github/PULL_REQUEST_TEMPLATE/"},
		{"template dir lowercase at docs", dir("docs/pull_request_template"), "docs/pull_request_template/"},
		{"template file beats dir at same location", []filepaths.Entry{
			{Path: ".github/PULL_REQUEST_TEMPLATE", Dir: true}, {Path: ".github/pull_request_template.md"},
		}, ".github/pull_request_template.md"},
		{"dir in .github beats file at root", []filepaths.Entry{
			{Path: ".github/PULL_REQUEST_TEMPLATE", Dir: true}, {Path: "pull_request_template.md"},
		}, ".github/PULL_REQUEST_TEMPLATE/"},
	})
}

func TestFind_rootOnly(t *testing.T) {
	t.Parallel()
	got := filepaths.Find(files("sub/justfile", "sub/.yamllint", "sub/cliff.toml", "sub/CLAUDE.md"))
	if !reflect.DeepEqual(got, filepaths.Paths{}) {
		t.Fatalf("nested files must not count, got %+v", got)
	}
}

func TestFind_compose(t *testing.T) {
	t.Parallel()
	got := filepaths.Find(files("a/compose.yaml", "b/compose.yaml", "c/docker-compose.yml", "compose.yml")).Compose
	want := []string{"compose.yaml", "compose.yml", "docker-compose.yml"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if got := filepaths.Find(files("docker-compose.yaml")).Compose; !reflect.DeepEqual(got, []string{"docker-compose.yaml"}) {
		t.Fatalf("docker-compose.yaml: got %v", got)
	}
	if got := filepaths.Find(files("a/justfile", "compose.yaml.bak")).Compose; got != nil {
		t.Fatalf("none: got %v, want nil", got)
	}
	if got := filepaths.Find([]filepaths.Entry{{Path: "compose.yaml", Dir: true}}).Compose; got != nil {
		t.Fatalf("directory: got %v, want nil", got)
	}
}

func TestFind_empty(t *testing.T) {
	t.Parallel()
	if got := filepaths.Find(nil); !reflect.DeepEqual(got, filepaths.Paths{}) {
		t.Fatalf("got %+v", got)
	}
}
