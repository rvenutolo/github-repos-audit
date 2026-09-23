package renovate

import (
	"strings"
)

// ParsePreset classifies one extends entry against the audited account; see
// the package doc for why only in-account presets are followed.
func ParsePreset(ref, owner string) (p Preset, fallback, follow bool, problem string) {
	s := ref
	// Arguments — ":labels(a,b)" — parameterise the preset's contents, not
	// which file it is, so the file is found by the name alone.
	if i := strings.IndexByte(s, '('); i >= 0 {
		s = s[:i]
	}
	switch {
	case strings.HasPrefix(s, "github>"):
		s = strings.TrimPrefix(s, "github>")
	case strings.HasPrefix(s, "local>"):
		s = strings.TrimPrefix(s, "local>")
	case strings.Contains(s, ">"), strings.Contains(s, "://"), strings.HasPrefix(s, "@"):
		return Preset{}, false, false, "" // another platform, a URL, an npm scope
	}

	s, p.Ref, _ = strings.Cut(s, "#")
	var name string
	repoPart, path, hasPath := strings.Cut(s, "//")
	if hasPath {
		if strings.Contains(path, ":") {
			return Preset{}, false, true, "a //path preset cannot also name a sub-preset"
		}
		name = path
	} else {
		repoPart, name, _ = strings.Cut(s, ":")
		if strings.Contains(name, "/") {
			return Preset{}, false, true, "nested sub-presets are not supported"
		}
	}

	// A built-in ("config:recommended", ":semanticCommits") has no owner/repo.
	presetOwner, repo, ok := strings.Cut(repoPart, "/")
	if !ok || presetOwner == "" || repo == "" || strings.Contains(repo, "/") {
		return Preset{}, false, false, ""
	}
	if !strings.EqualFold(presetOwner, owner) {
		return Preset{}, false, false, ""
	}

	p.Repo = repo
	switch {
	case name == "":
		p.Path, fallback = "default.json", true
	case strings.HasSuffix(name, ".json"), strings.HasSuffix(name, ".json5"), strings.HasSuffix(name, ".jsonc"):
		// Renovate's /\.json[5c]?$/: a name already carrying one of these
		// extensions is the file name as written.
		p.Path = name
	default:
		p.Path = name + ".json"
	}
	return p, fallback, true, ""
}
