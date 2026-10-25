package github

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
	"github.com/rvenutolo/github-repos-audit/internal/claude"
	"github.com/rvenutolo/github-repos-audit/internal/filepaths"
)

// The Claude file query's bounds. Every file is one aliased object() with its
// whole text, and GitHub bounds what one query may cost and how long it may
// run (https://docs.github.com/en/graphql/overview/rate-limits-and-query-limits-for-the-graphql-api),
// so a repository's files are read claudeFilesPerQuery at a time.
// claudeFilesMax counts every file read or asked for, across both rounds;
// past it the files are not read any further: a repository with that many
// is recorded as unreadable rather than costing a run a stack of heavy
// queries. Both numbers are the design's starting point, not a measured
// limit; the live render against every repository is what confirms them.
const (
	claudeFilesPerQuery = 50
	claudeFilesMax      = 200
)

// tooManyClaudeFiles is the repository-wide problem past claudeFilesMax.
const tooManyClaudeFiles = "too many Claude files"

// missingDeclared prefixes the repository-wide problem for a path plugin.json
// declares that the tree cannot answer.
const missingDeclared = "plugin.json declares missing "

// inlineCommandPath is the path recorded for a command whose text lives in
// plugin.json itself, so it reads as a place in the manifest.
const inlineCommandPath = ".claude-plugin/plugin.json#commands/"

// claudeFacts reads and parses the repository's Claude Code files: the ones
// internal/claude's layout names from the tree. A repository with none costs
// no request. One with a plugin manifest costs at least two rounds, because
// the manifest decides which further files belong to the plugin: the first
// reads the manifest with every file the default layout already names, the
// second only what the manifest adds. Each round is one query per
// claudeFilesPerQuery files.
//
// It decides nothing. A file that cannot be read in full or parsed is
// recorded as unreadable with its reason, and a query that fails fails the
// run, as the file query does.
func (c *Client) claudeFacts(ctx context.Context, name string, entries []filepaths.Entry) (audit.Claude, error) {
	layout := claude.Discover(entries, nil)
	if layout.PluginManifest == "" && len(layout.Skills) == 0 && len(layout.Commands) == 0 && len(layout.HookFiles) == 0 {
		return audit.Claude{}, nil
	}

	var out audit.Claude
	if layout.PluginManifest != "" {
		out.Plugin = audit.ClaudePlugin{Present: true, OwnMarketplace: layout.OwnMarketplace}
	}
	paths := layoutPaths(layout)
	if len(paths) > claudeFilesMax {
		out.Unreadable = tooManyClaudeFiles
		return out, nil
	}
	texts, err := c.readClaudeFiles(ctx, name, paths)
	if err != nil {
		return audit.Claude{}, err
	}

	modes := make(map[string]string, len(entries))
	for _, e := range entries {
		if !e.Dir {
			modes[e.Path] = e.Mode
		}
	}

	var manifest claude.Plugin
	if layout.PluginManifest != "" {
		text, problem := claudeText(texts, modes, layout.PluginManifest)
		if problem == "" {
			manifest = claude.ParsePlugin(text)
			problem = manifest.Unreadable
		}
		// Without the manifest the plugin's layout cannot be decided, and
		// judging only the files the default layout found would pass checks
		// on a repository whose other files were never looked at.
		if problem != "" {
			out.Plugin.Unreadable = problem
			out.Unreadable = layout.PluginManifest + ": " + strings.TrimPrefix(problem, layout.PluginManifest+": ")
			return out, nil
		}
		out.Plugin.Name = manifest.Name
		out.Plugin.Version = manifest.Version
		out.Plugin.Fields = manifest.Fields

		// A repository with no manifest must not be run through the plugin
		// rules, which would load a top-level skills/ Claude Code ignores.
		layout = claude.Discover(entries, &manifest)
		// A declared directory that is not there has no file to record as
		// unreadable in its place, and judging the rest would pass a plugin
		// Claude Code cannot load; so, as with a manifest that cannot be
		// read, the whole repository is unreadable.
		if len(layout.Missing) > 0 {
			out.Unreadable = missingDeclared + layout.Missing[0]
			return out, nil
		}
		var more []string
		for _, p := range layoutPaths(layout) {
			if _, read := texts[p]; !read {
				more = append(more, p)
			}
		}
		if len(paths)+len(more) > claudeFilesMax {
			out.Unreadable = tooManyClaudeFiles
			return out, nil
		}
		moreTexts, err := c.readClaudeFiles(ctx, name, more)
		if err != nil {
			return audit.Claude{}, err
		}
		maps.Copy(texts, moreTexts)
	}

	isPlugin := func(p string) bool { return layout.PluginManifest != "" && !isProjectPath(p) }

	var skills []claude.Skill
	for _, p := range layout.Skills {
		skills = append(skills, parseSkillFile(texts, modes, p, claude.SkillName(p, manifest.Name), isPlugin(p)))
	}
	commands := make([]claude.Skill, 0, len(layout.Commands)+len(manifest.InlineCommands))
	for _, p := range layout.Commands {
		commands = append(commands, parseSkillFile(texts, modes, p, claude.CommandName(p), isPlugin(p)))
	}
	for _, n := range manifest.InlineCommands {
		commands = append(commands, claude.ParseSkill(inlineCommandPath+n, n, manifest.InlineContent[n], true))
	}

	hooks := slices.Clone(manifest.Hooks)
	for _, p := range layout.HookFiles {
		text, problem := claudeText(texts, modes, p)
		if problem != "" {
			hooks = append(hooks, claude.Hook{Source: p, Plugin: isPlugin(p), Unreadable: problem})
			continue
		}
		hooks = append(hooks, claude.ParseHooks(p, text, isPlugin(p))...)
	}

	skillNames := make([]string, 0, len(skills))
	for _, s := range skills {
		hooks = append(hooks, s.Hooks...)
		out.Skills = append(out.Skills, skillFact(s, ""))
		// A skill's name comes from its path, so an unreadable skill can
		// still be the one a command wraps.
		skillNames = append(skillNames, s.Dir)
	}
	for _, s := range commands {
		hooks = append(hooks, s.Hooks...)
		wraps := ""
		if s.Unreadable == "" {
			wraps = claude.WrappedSkill(s.Body, skillNames)
		}
		out.Commands = append(out.Commands, skillFact(s, wraps))
	}

	// Stable, so the handlers of one event in one file keep the order they
	// are declared in.
	slices.SortStableFunc(hooks, func(a, b claude.Hook) int {
		return cmp.Or(cmp.Compare(a.Source, b.Source), cmp.Compare(a.Event, b.Event))
	})
	for _, h := range hooks {
		out.Hooks = append(out.Hooks, hookFact(h, modes))
	}
	return out, nil
}

// layoutPaths is every file a layout names, sorted.
func layoutPaths(l claude.Layout) []string {
	var paths []string
	if l.PluginManifest != "" {
		paths = append(paths, l.PluginManifest)
	}
	paths = append(paths, l.Skills...)
	paths = append(paths, l.Commands...)
	paths = append(paths, l.HookFiles...)
	slices.Sort(paths)
	return slices.Compact(paths)
}

// isProjectPath reports a file under a .claude directory: the project's own
// configuration, which is not part of a plugin even in a plugin's
// repository.
func isProjectPath(p string) bool {
	return strings.HasPrefix(p, ".claude/") || strings.Contains(p, "/.claude/")
}

// parseSkillFile parses one skill or command file from what was read.
func parseSkillFile(texts map[string]*fileBlob, modes map[string]string, path, dir string, plugin bool) claude.Skill {
	text, problem := claudeText(texts, modes, path)
	if problem != "" {
		return claude.Skill{Path: path, Dir: dir, Unreadable: problem}
	}
	return claude.ParseSkill(path, dir, text, plugin)
}

func skillFact(s claude.Skill, wraps string) audit.ClaudeSkill {
	return audit.ClaudeSkill{
		Path:                   s.Path,
		Dir:                    s.Dir,
		Name:                   s.Name,
		Keys:                   s.Keys,
		HasDescription:         s.HasDescription,
		DescriptionChars:       s.DescriptionChars,
		DisableModelInvocation: s.DisableModelInvocation,
		UserInvocableFalse:     s.UserInvocableFalse,
		Lines:                  s.Lines,
		WrapsSkill:             wraps,
		Unreadable:             s.Unreadable,
	}
}

// hookFact records one handler. The script's mode comes from the tree, so a
// script that is not a tracked file (or is a directory) has none.
func hookFact(h claude.Hook, modes map[string]string) audit.ClaudeHook {
	fact := audit.ClaudeHook{
		Source:      h.Source,
		Plugin:      h.Plugin,
		Event:       h.Event,
		Type:        h.Type,
		Matcher:     h.Matcher,
		Timeout:     h.Timeout,
		TimeoutSet:  h.TimeoutSet,
		Script:      h.Script,
		Interpreter: h.Interpreter,
		PathForm:    h.PathForm,
		Unreadable:  h.Unreadable,
	}
	if h.Script != "" {
		fact.ScriptMode = modes[h.Script]
	}
	return fact
}

// claudeText is blobText for a Claude file, with one reason of its own: a
// path plugin.json declares that is not a tracked file (absent, or a
// directory) has no such file, which says more than "not a text file" about
// a file that is not there to be text or not. modes holds every tracked
// file.
func claudeText(texts map[string]*fileBlob, modes map[string]string, path string) (text, problem string) {
	if _, tracked := modes[path]; !tracked {
		return "", path + ": no such file"
	}
	return blobText(texts[path], path)
}

// readClaudeFiles reads paths at HEAD, claudeFilesPerQuery to a query, and
// returns each path's blob. A path the query found nothing at maps to nil,
// which blobText reports as not a text file.
func (c *Client) readClaudeFiles(ctx context.Context, name string, paths []string) (map[string]*fileBlob, error) {
	out := make(map[string]*fileBlob, len(paths))
	for chunk := range slices.Chunk(paths, claudeFilesPerQuery) {
		var data struct {
			Repository map[string]*fileBlob `json:"repository"`
		}
		query, vars := claudeFilesQuery(c.owner, name, chunk)
		if err := c.postGraphQL(ctx, query, vars, &data); err != nil {
			return nil, err
		}
		if data.Repository == nil {
			return nil, fmt.Errorf("%s: %w: no repository in the Claude file query's response", graphQLPath, errGraphQL)
		}
		for i, p := range chunk {
			out[p] = data.Repository["f"+strconv.Itoa(i)]
		}
	}
	return out, nil
}

// claudeFilesQuery builds the query for one chunk: an aliased object() per
// path, fN reading $eN. The paths travel only as variables, never in the
// document, for the reason graphQLRequest gives; only the aliases and
// variable names, which this function makes up, are written into it.
func claudeFilesQuery(owner, name string, paths []string) (string, map[string]string) {
	vars := map[string]string{"owner": owner, "name": name}
	var params, fields strings.Builder
	for i, p := range paths {
		n := strconv.Itoa(i)
		vars["e"+n] = "HEAD:" + p
		fmt.Fprintf(&params, ", $e%s: String!", n)
		fmt.Fprintf(&fields, "    f%s: object(expression: $e%s) { %s }\n", n, n, blobFields)
	}
	query := "query ClaudeFiles($owner: String!, $name: String!" + params.String() + ") {\n" +
		"  repository(owner: $owner, name: $name) {\n" + fields.String() + "  }\n}"
	return query, vars
}
