package claude

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

// Skill is what a SKILL.md or a command file says about itself. Frontmatter is
// decoded into yaml.Node rather than into a struct so the record keeps what the
// file says: `disable-model-invocation: "true"` is a string, not a boolean, and
// only the node's tag tells them apart.
type Skill struct {
	Path                   string
	Dir                    string            // name the skill is known by (directory or file stem)
	Name                   string            // frontmatter name, "" when absent
	Keys                   map[string]string // frontmatter key -> YAML short tag ("!!str", "!!bool", "!!map", ...)
	DescriptionChars       int               // utf8 rune count of description + when_to_use
	DisableModelInvocation bool              // YAML boolean true, nothing else
	UserInvocableFalse     bool              // YAML boolean false
	Lines                  int               // line count of the whole file
	Hooks                  []Hook            // frontmatter hooks, Source = Path
	Body                   string            // text after the frontmatter (not recorded in audit.json)
	Unreadable             string            // reason; when set, every other field is zero except Path and Dir
}

const fence = "---"

// ParseSkill reads one skill or command file. plugin says whether the file is
// part of a plugin, which marks the hooks it declares. A file that does not
// open with a `---` line, never closes it, or whose frontmatter is not a YAML
// mapping is unreadable: Claude Code would load it without its metadata, so
// no check may pass on it.
func ParseSkill(path, dir, text string, plugin bool) Skill {
	bad := func(format string, args ...any) Skill {
		return Skill{Path: path, Dir: dir, Unreadable: fmt.Sprintf(format, args...)}
	}
	// A CRLF file is the same file; normalising keeps the fence match exact.
	norm := strings.ReplaceAll(text, "\r\n", "\n")
	rest, ok := strings.CutPrefix(norm, fence+"\n")
	if !ok {
		return bad("no YAML frontmatter: the file does not start with ---")
	}
	var front, body string
	switch {
	case strings.HasPrefix(rest, fence+"\n"):
		front, body = "", rest[len(fence)+1:]
	case rest == fence:
		front, body = "", ""
	default:
		i := strings.Index(rest, "\n"+fence+"\n")
		switch {
		case i >= 0:
			front, body = rest[:i], rest[i+len(fence)+2:]
		case strings.HasSuffix(rest, "\n"+fence):
			front, body = rest[:len(rest)-len(fence)-1], ""
		default:
			return bad("the frontmatter is not closed by a --- line")
		}
	}

	top, err := mapping(front)
	if err != nil {
		return bad("%v", err)
	}

	s := Skill{
		Path:  path,
		Dir:   dir,
		Keys:  map[string]string{},
		Lines: lineCount(norm),
		Body:  body,
	}
	for i := 0; i+1 < len(top.Content); i += 2 {
		k, v := top.Content[i], deref(top.Content[i+1])
		// yaml.Node accepts duplicate keys, which Claude Code's loader would
		// resolve one way or the other; there is no safe verdict to give.
		if _, dup := s.Keys[k.Value]; dup {
			return bad("the frontmatter repeats the key %q", k.Value)
		}
		s.Keys[k.Value] = v.ShortTag()
		switch k.Value {
		case "name":
			if v.Kind == yaml.ScalarNode && v.ShortTag() != "!!null" {
				s.Name = v.Value
			}
		case "description", "when_to_use":
			if v.Kind == yaml.ScalarNode && v.ShortTag() != "!!null" {
				s.DescriptionChars += utf8.RuneCountInString(v.Value)
			}
		case "disable-model-invocation":
			s.DisableModelInvocation = isBool(v, "true")
		case "user-invocable":
			s.UserInvocableFalse = isBool(v, "false")
		case "hooks":
			var decoded any
			if err := v.Decode(&decoded); err != nil {
				s.Hooks = []Hook{{Source: path, Plugin: plugin, Unreadable: "hooks: " + err.Error()}}
			} else {
				s.Hooks = ParseHooksValue(path, decoded, plugin)
			}
		}
	}
	return s
}

// mapping decodes frontmatter text into its top-level mapping node.
func mapping(text string) (*yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		return nil, fmt.Errorf("frontmatter is not valid YAML: %w", err)
	}
	if len(doc.Content) == 0 {
		return nil, errors.New("the frontmatter is empty, not a mapping")
	}
	top := deref(doc.Content[0])
	if top.Kind != yaml.MappingNode {
		return nil, errors.New("the frontmatter is not a YAML mapping")
	}
	return top, nil
}

func deref(n *yaml.Node) *yaml.Node {
	if n != nil && n.Kind == yaml.AliasNode {
		return n.Alias
	}
	return n
}

// isBool reports whether n is the YAML boolean spelled want. A quoted "true"
// is a string and `yes` is a string in YAML 1.2, so neither counts: Claude Code
// accepts looser spellings, but the verdict must not depend on its version.
func isBool(n *yaml.Node, want string) bool {
	return n.Kind == yaml.ScalarNode && n.ShortTag() == "!!bool" && strings.EqualFold(n.Value, want)
}

// lineCount counts lines the way an editor does: a last line without a
// newline still counts. Its text always opens with a fence, so is never empty.
func lineCount(text string) int {
	n := strings.Count(text, "\n")
	if !strings.HasSuffix(text, "\n") {
		n++
	}
	return n
}

// WrappedSkill returns the one skill name that body names as a whole word, or
// "" when it names none or several. A word is a run of letters, digits, `_`
// and `-`, so `docs-audit-extra` does not name `docs-audit`. Matching is case
// sensitive: skill names are lower-case identifiers.
func WrappedSkill(body string, skills []string) string {
	found := ""
	for _, name := range skills {
		if name == "" || !namesWord(body, name) {
			continue
		}
		if found != "" && found != name {
			return ""
		}
		found = name
	}
	return found
}

func namesWord(body, name string) bool {
	re := regexp.MustCompile(`(?:^|[^A-Za-z0-9_-])` + regexp.QuoteMeta(name) + `(?:$|[^A-Za-z0-9_-])`)
	return re.MatchString(body)
}
