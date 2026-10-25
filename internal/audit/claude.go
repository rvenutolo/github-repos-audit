package audit

// Claude is what a repository's Claude Code tooling says about itself: its
// skills, command files, hooks and plugin manifest. The zero value is a
// repository with none of them. Which files count, and why, is the layout
// internal/claude decides from the tree; whether any of it is right is
// internal/rules' decision.
type Claude struct {
	// Skills is every SKILL.md Claude Code would load, sorted by path.
	Skills []ClaudeSkill `json:"skills,omitzero"`
	// Commands is every command file, sorted by path (by name for one file
	// that several commands object map keys name), then every inline
	// command a plugin.json declares, sorted by name.
	Commands []ClaudeSkill `json:"commands,omitzero"`
	// Hooks is every hook handler, from hook files, an inline plugin.json
	// hooks object and skill or command frontmatter, sorted by source then
	// event, declaration order kept within one.
	Hooks []ClaudeHook `json:"hooks,omitzero"`
	// Plugin is what .claude-plugin/plugin.json says. Zero when there is
	// none.
	Plugin ClaudePlugin `json:"plugin,omitzero"`
	// Unreadable is a problem with the repository's Claude files as a whole:
	// too many to read, a plugin manifest whose layout cannot be decided, or
	// one that declares a path Claude Code cannot load (a directory that is
	// not there, a command that is not a .md file, or a path outside the
	// repository).
	// When it is set nothing else but Plugin is, so every check reading
	// these facts fails as unreadable rather than judging part of them.
	Unreadable string `json:"unreadable,omitzero"`
}

// ClaudeSkill is one skill or command file. A command file is a skill in all
// but layout, so both are recorded in the same shape.
type ClaudeSkill struct {
	// Path is the file's path, or .claude-plugin/plugin.json#commands/<name>
	// for an inline command.
	Path string `json:"path"`
	// Dir is the name Claude Code knows it by: the skill's directory, the
	// plugin's name for a root SKILL.md, or a command's name (its key, for an
	// entry in plugin.json's commands object map). Empty for the root
	// SKILL.md of a plugin whose manifest sets no name.
	Dir string `json:"dir"`
	// Name is the frontmatter `name`, empty when absent.
	Name string `json:"name,omitzero"`
	// Keys is every frontmatter key, mapped to the YAML tag of its value
	// ("!!str", "!!bool", "!!map", ...), so a quoted "true" stays a string.
	Keys map[string]string `json:"keys,omitzero"`
	// HasDescription reports a `description` that is a YAML string holding
	// more than whitespace. A key present with no value, an empty string or
	// a list says nothing in the skill listing, so it does not count.
	HasDescription bool `json:"has_description,omitzero"`
	// DescriptionChars is the combined length in characters of
	// `description` and `when_to_use`.
	DescriptionChars int `json:"description_chars,omitzero"`
	// DisableModelInvocation is `disable-model-invocation` set to the YAML
	// boolean true, and nothing else.
	DisableModelInvocation bool `json:"disable_model_invocation,omitzero"`
	// UserInvocableFalse is `user-invocable` set to the YAML boolean false.
	UserInvocableFalse bool `json:"user_invocable_false,omitzero"`
	// Lines is the file's line count.
	Lines int `json:"lines,omitzero"`
	// WrapsSkill is, for a command, the one skill its body names and so
	// does nothing but wrap. Empty for a skill, and for a command naming no
	// skill or several.
	WrapsSkill string `json:"wraps_skill,omitzero"`
	// ManifestDescription is, for an inline command, the `description` its
	// plugin.json entry sets, which Claude Code shows for the command. It is
	// recorded beside the frontmatter's rather than merged into
	// HasDescription, so no check's verdict depends on it yet. Empty when
	// the entry sets none, and for a file.
	ManifestDescription string `json:"manifest_description,omitzero"`
	// Unreadable is why the file could not be read in full or parsed. When it
	// is set only Path and Dir are.
	Unreadable string `json:"unreadable,omitzero"`
}

// ClaudeHook is one handler of one hook event.
type ClaudeHook struct {
	// Source is the file the hook is declared in.
	Source string `json:"source"`
	// Plugin reports a hook a plugin declares: in a plugin hooks file, the
	// manifest, or a plugin skill's frontmatter.
	Plugin bool `json:"plugin,omitzero"`
	// Event is the hook event name as written.
	Event string `json:"event,omitzero"`
	// Type is the handler type as written: command, http, mcp_tool, prompt
	// or agent.
	Type string `json:"type,omitzero"`
	// Matcher is the group's matcher, empty when absent.
	Matcher string `json:"matcher,omitzero"`
	// Timeout is the handler's timeout in seconds; TimeoutSet tells an
	// explicit zero from an absent key.
	Timeout    float64 `json:"timeout,omitzero"`
	TimeoutSet bool    `json:"timeout_set,omitzero"`
	// Command is a command handler's `command` as written, empty when
	// absent and for other handler types, whose command key is not theirs
	// to run.
	Command string `json:"command,omitzero"`
	// Args is a command handler's `args` as written, which makes it the
	// exec form: Command is the program and Args its arguments, with no
	// shell between. An empty list is kept, since it still selects the exec
	// form; absent means the shell form.
	Args []string `json:"args,omitzero"`
	// Script is the repository file a command handler runs, empty when it
	// names none.
	Script string `json:"script,omitzero"`
	// Interpreter reports a Script handed to an interpreter, which reads it
	// rather than executing it.
	Interpreter bool `json:"interpreter,omitzero"`
	// PathForm is how the command names its program: plugin_root,
	// project_dir, relative or absolute. Empty for a bare command on PATH.
	PathForm string `json:"path_form,omitzero"`
	// ScriptMode is the git mode of the tracked file at Script, such as
	// "100755". Empty when Script is not a tracked file.
	ScriptMode string `json:"script_mode,omitzero"`
	// Unreadable is why the hooks could not be read. A source that cannot be
	// read or does not have the hooks shape is one entry carrying only
	// Source, Plugin and this.
	Unreadable string `json:"unreadable,omitzero"`
}

// ClaudePlugin is what .claude-plugin/plugin.json says.
type ClaudePlugin struct {
	// Present reports that .claude-plugin/plugin.json is tracked.
	Present bool `json:"present,omitzero"`
	// Name and Version are the manifest's, empty when absent.
	Name    string `json:"name,omitzero"`
	Version string `json:"version,omitzero"`
	// Fields is every required manifest field present, sorted.
	Fields []string `json:"fields,omitzero"`
	// OwnMarketplace reports that .claude-plugin/marketplace.json is tracked
	// beside the manifest.
	OwnMarketplace bool `json:"own_marketplace,omitzero"`
	// Unreadable is why the manifest could not be read in full or parsed.
	// When it is set Name, Version and Fields are empty.
	Unreadable string `json:"unreadable,omitzero"`
}

// ClaudeMarketplace is what the configured marketplace repository's
// .claude-plugin/marketplace.json lists, read once per run, with what GitHub
// answered about each entry's pin. Zero, and absent from audit.json, when
// repos.toml configures no marketplace.
type ClaudeMarketplace struct {
	// Repo is the marketplace repository's name, as repos.toml's [claude]
	// table gives it.
	Repo string `json:"repo"`
	// Unreadable is why the marketplace could not be read: the repository
	// is not among the audited repositories, or its marketplace.json is
	// absent or not in the shape Claude Code loads. When it is set there are
	// no entries, so every marketplace check fails as unreadable rather than
	// judging part of the file.
	Unreadable string `json:"unreadable,omitzero"`
	// Entries is every plugins entry, in the file's order.
	Entries []MarketplaceEntry `json:"entries,omitzero"`
}

// MarketplaceEntry is one plugins entry and the answers about its pin. The
// lookups are made only for an entry whose source is a GitHub repository of
// the audited account; any other entry carries what the file says and a
// Problem.
type MarketplaceEntry struct {
	// Name is the entry's name, the plugin's install id.
	Name string `json:"name"`
	// Repo is the source's owner/name as written, empty for a source that is
	// not a GitHub repository.
	Repo string `json:"repo,omitzero"`
	// Ref and SHA are the source's pin as written, empty when absent.
	Ref string `json:"ref,omitzero"`
	SHA string `json:"sha,omitzero"`
	// HasVersion reports a version key on the entry, which plugin.json
	// should own instead.
	HasVersion bool `json:"has_version,omitzero"`
	// ReleaseTag reports that Ref names a release of Repo.
	ReleaseTag bool `json:"release_tag,omitzero"`
	// TagCommit is the commit the tag Ref resolves to, annotated tags
	// dereferenced. Empty when Repo has no such tag.
	TagCommit string `json:"tag_commit,omitzero"`
	// PluginName is .claude-plugin/plugin.json's name at SHA.
	PluginName string `json:"plugin_name,omitzero"`
	// Problem is why a lookup could not be made or gave no usable answer:
	// the source is not an audited repository or is an empty one, the ref
	// is not safe to request (a ".", ".." or empty path segment), the tag
	// resolves to no commit, or there is no readable manifest at SHA.
	Problem string `json:"problem,omitzero"`
}
