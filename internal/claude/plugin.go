package claude

import (
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"slices"
	"strings"
)

// Plugin is what a plugin.json says, reduced to the facts the checks and the
// layout rules need.
type Plugin struct {
	Name, Version  string
	Fields         []string // required fields present by the presence rules, sorted
	SkillDirs      []string // declared skills paths, cleaned, repository-relative
	SkillsSet      bool     // the skills key is present
	CommandPaths   []string // declared commands file/dir paths; nil when the key is absent
	CommandsSet    bool     // the commands key is present (replaces the default scan)
	InlineCommands []string // names of object-map commands with inline content
	HooksFile      string   // first declared hooks path
	HooksFiles     []string // every declared hooks path
	Hooks          []Hook   // inline hooks object
	Unreadable     string
}

// requiredFields lists the keys plugin_manifest_fields asks for.
func requiredFields() []string {
	return []string{
		"$schema", "name", "version", "description", "author", "license", "homepage", "repository",
	}
}

// ParsePlugin reads plugin.json text. Presence follows the Fields table at
// https://code.claude.com/docs/en/plugins-reference: author counts only with a
// non-empty author.name, homepage and repository must parse as http(s) URLs,
// and every other field must be a non-empty string.
//
// A component key in a shape the reference does not accept makes the manifest
// unreadable: the layout it implies cannot be decided, and guessing it would
// hide files from the checks.
func ParsePlugin(text string) Plugin {
	bad := func(format string, args ...any) Plugin {
		return Plugin{Unreadable: fmt.Sprintf(format, args...)}
	}
	var doc any
	if err := json.Unmarshal([]byte(text), &doc); err != nil {
		return bad("not valid JSON: %v", err)
	}
	obj, ok := doc.(map[string]any)
	if !ok {
		return bad("plugin.json is not a JSON object")
	}

	var p Plugin
	if name, ok := obj["name"].(string); ok {
		p.Name = name
	}
	if version, ok := obj["version"].(string); ok {
		p.Version = version
	}
	for _, f := range requiredFields() {
		if fieldPresent(f, obj[f]) {
			p.Fields = append(p.Fields, f)
		}
	}
	slices.Sort(p.Fields)

	if v, present := obj["skills"]; present {
		paths, ok := stringOrList(v)
		if !ok {
			return bad("skills is not a path or a list of paths")
		}
		p.SkillsSet = true
		for _, s := range paths {
			p.SkillDirs = append(p.SkillDirs, cleanDeclared(s))
		}
	}
	if v, present := obj["commands"]; present {
		p.CommandsSet = true
		if err := p.readCommands(v); err != "" {
			return bad("%s", err)
		}
	}
	if v, present := obj["hooks"]; present {
		items, ok := v.([]any)
		if !ok {
			items = []any{v}
		}
		for _, item := range items {
			switch h := item.(type) {
			case string:
				p.HooksFiles = append(p.HooksFiles, cleanDeclared(h))
			case map[string]any:
				p.Hooks = append(p.Hooks, ParseHooksValue(pluginManifestPath, h, true)...)
			default:
				return bad("hooks holds neither a path nor an object")
			}
		}
		if len(p.HooksFiles) > 0 {
			p.HooksFile = p.HooksFiles[0]
		}
	}
	return p
}

func (p *Plugin) readCommands(v any) string {
	if m, ok := v.(map[string]any); ok {
		for _, name := range sortedMapKeys(m) {
			entry, ok := m[name].(map[string]any)
			if !ok {
				return "commands." + name + " is not an object"
			}
			source, hasSource := entry["source"].(string)
			_, hasContent := entry["content"].(string)
			// The reference: an entry sets exactly one of source or content.
			if hasSource == hasContent {
				return "commands." + name + " must set exactly one of source and content"
			}
			if hasContent {
				p.InlineCommands = append(p.InlineCommands, name)
			} else {
				p.CommandPaths = append(p.CommandPaths, cleanDeclared(source))
			}
		}
		return ""
	}
	paths, ok := stringOrList(v)
	if !ok {
		return "commands is not a path, a list of paths or an object"
	}
	for _, s := range paths {
		p.CommandPaths = append(p.CommandPaths, cleanDeclared(s))
	}
	return ""
}

func fieldPresent(name string, v any) bool {
	switch name {
	case "author":
		a, ok := v.(map[string]any)
		if !ok {
			return false
		}
		s, ok := a["name"].(string)
		return ok && strings.TrimSpace(s) != ""
	case "homepage", "repository":
		s, ok := v.(string)
		if !ok {
			return false
		}
		u, err := url.Parse(s)
		return err == nil && (u.Scheme == "http" || u.Scheme == "https")
	}
	s, ok := v.(string)
	return ok && strings.TrimSpace(s) != ""
}

func stringOrList(v any) ([]string, bool) {
	if s, ok := v.(string); ok {
		return []string{s}, true
	}
	list, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		s, ok := item.(string)
		if !ok {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}

// cleanDeclared turns a manifest path ("./extra/") into the repository-relative
// form the tree uses ("extra"); the plugin root is ".".
func cleanDeclared(p string) string { return path.Clean(strings.TrimPrefix(p, "./")) }
