package github

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
	"github.com/rvenutolo/github-repos-audit/internal/filepaths"
)

// claudeServer answers ClaudeFiles queries from blobs, keyed by path: each
// alias fN is answered with the blob for the path its $eN variable names, or
// null when there is none. It records the paths of every query so a test can
// say what was asked for, and answers body instead when that is set.
type claudeServer struct {
	t     *testing.T
	blobs map[string]*fileBlob
	body  string
	// failAfter, when set, makes every query after that many answer body
	// instead of the blobs.
	failAfter int

	mu      sync.Mutex
	queries [][]string
}

func (s *claudeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return
	}
	var req graphQLRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		s.t.Errorf("decode request: %v", err)
		return
	}
	if !strings.HasPrefix(req.Query, "query ClaudeFiles(") {
		s.t.Errorf("unexpected query:\n%s", req.Query)
	}
	answer := map[string]*fileBlob{}
	var paths []string
	for i := 0; ; i++ {
		expr, ok := req.Variables["e"+strconv.Itoa(i)]
		if !ok {
			break
		}
		path := strings.TrimPrefix(expr, "HEAD:")
		paths = append(paths, path)
		answer["f"+strconv.Itoa(i)] = s.blobs[path]
	}
	s.mu.Lock()
	s.queries = append(s.queries, paths)
	n := len(s.queries)
	s.mu.Unlock()

	body := s.body
	if s.failAfter > 0 && n <= s.failAfter {
		body = ""
	}
	if body == "" {
		data, err := json.Marshal(map[string]any{"data": map[string]any{"repository": answer}})
		if err != nil {
			s.t.Errorf("encode answer: %v", err)
			return
		}
		body = string(data)
	}
	if _, err := w.Write([]byte(body)); err != nil {
		s.t.Errorf("write answer: %v", err)
	}
}

func (s *claudeServer) asked() [][]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([][]string(nil), s.queries...)
}

// newClaudeClient serves s and returns a client pointed at it.
func newClaudeClient(t *testing.T, s *claudeServer) *Client {
	t.Helper()
	s.t = t
	server := httptest.NewServer(s)
	t.Cleanup(server.Close)
	c, err := New(Options{Owner: "gh-owner", Token: "token", BaseURL: server.URL})
	if err != nil {
		t.Fatalf("New() error = %v, want nil", err)
	}
	return c
}

// files turns paths into tree entries, each with an ordinary file mode.
func files(paths ...string) []filepaths.Entry {
	out := make([]filepaths.Entry, 0, len(paths))
	for _, p := range paths {
		out = append(out, filepaths.Entry{Path: p, Mode: "100644"})
	}
	return out
}

const skillText = "---\nname: %s\ndescription: Placeholder description.\n---\n\nPlaceholder body.\n"

// TestClaudeFacts_noTooling: a repository with none of the files has nothing
// to read, so it costs no query at all.
func TestClaudeFacts_noTooling(t *testing.T) {
	t.Parallel()

	s := &claudeServer{}
	c := newClaudeClient(t, s)
	// A top-level skills/ without a plugin manifest is not loaded by Claude
	// Code, so it is not a reason to read anything either.
	got, err := c.claudeFacts(t.Context(), "shell-scripts", files("README.md", "skills/skill-01/SKILL.md"))
	if err != nil {
		t.Fatalf("claudeFacts() error = %v, want nil", err)
	}
	if diff := cmp.Diff(audit.Claude{}, got); diff != "" {
		t.Errorf("claudeFacts() mismatch (-want +got):\n%s", diff)
	}
	if q := s.asked(); len(q) != 0 {
		t.Errorf("sent %d Claude queries, want none", len(q))
	}
}

// TestClaudeFacts_tooManyFiles: past claudeFilesMax the files are not read at
// all, and the repository as a whole is unreadable.
func TestClaudeFacts_tooManyFiles(t *testing.T) {
	t.Parallel()

	paths := make([]string, 0, claudeFilesMax+1)
	for i := range claudeFilesMax + 1 {
		paths = append(paths, fmt.Sprintf(".claude/skills/skill-%03d/SKILL.md", i))
	}
	s := &claudeServer{}
	got, err := newClaudeClient(t, s).claudeFacts(t.Context(), "skill-pack", files(paths...))
	if err != nil {
		t.Fatalf("claudeFacts() error = %v, want nil", err)
	}
	if diff := cmp.Diff(audit.Claude{Unreadable: "too many Claude files"}, got); diff != "" {
		t.Errorf("claudeFacts() mismatch (-want +got):\n%s", diff)
	}
	if q := s.asked(); len(q) != 0 {
		t.Errorf("sent %d Claude queries, want none", len(q))
	}
}

// TestClaudeFacts_tooManyFilesAfterTheManifest: the bound counts every file,
// the ones only the manifest's layout reveals included, so a plugin whose
// skills push it past the bound is read no further than its manifest.
func TestClaudeFacts_tooManyFilesAfterTheManifest(t *testing.T) {
	t.Parallel()

	paths := make([]string, 0, 1+claudeFilesMax)
	paths = append(paths, ".claude-plugin/plugin.json")
	for i := range claudeFilesMax {
		paths = append(paths, fmt.Sprintf("skills/skill-%03d/SKILL.md", i))
	}
	s := &claudeServer{blobs: map[string]*fileBlob{
		".claude-plugin/plugin.json": text(`{"name": "plugin-kit", "version": "1.0.0"}`),
	}}
	got, err := newClaudeClient(t, s).claudeFacts(t.Context(), "plugin-kit", files(paths...))
	if err != nil {
		t.Fatalf("claudeFacts() error = %v, want nil", err)
	}
	want := audit.Claude{
		Plugin: audit.ClaudePlugin{
			Present: true, Name: "plugin-kit", Version: "1.0.0", Fields: []string{"name", "version"},
		},
		Unreadable: "too many Claude files",
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("claudeFacts() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([][]string{{".claude-plugin/plugin.json"}}, s.asked()); diff != "" {
		t.Errorf("queries mismatch (-want +got):\n%s", diff)
	}
}

// TestClaudeFacts_batches: one query carries at most claudeFilesPerQuery
// files, and the rest go in further queries.
func TestClaudeFacts_batches(t *testing.T) {
	t.Parallel()

	blobs := map[string]*fileBlob{}
	paths := make([]string, 0, claudeFilesPerQuery+1)
	for i := range claudeFilesPerQuery + 1 {
		name := fmt.Sprintf("skill-%03d", i)
		p := ".claude/skills/" + name + "/SKILL.md"
		paths = append(paths, p)
		blobs[p] = text(fmt.Sprintf(skillText, name))
	}
	s := &claudeServer{blobs: blobs}
	got, err := newClaudeClient(t, s).claudeFacts(t.Context(), "skill-pack", files(paths...))
	if err != nil {
		t.Fatalf("claudeFacts() error = %v, want nil", err)
	}
	asked := s.asked()
	if len(asked) != 2 || len(asked[0]) != claudeFilesPerQuery || len(asked[1]) != 1 {
		t.Errorf("query sizes = %v, want %d then 1", sizes(asked), claudeFilesPerQuery)
	}
	if len(got.Skills) != claudeFilesPerQuery+1 {
		t.Fatalf("got %d skills, want %d", len(got.Skills), claudeFilesPerQuery+1)
	}
	for _, sk := range got.Skills {
		if sk.Unreadable != "" || sk.Name != sk.Dir {
			t.Errorf("skill %s = %+v, want it read with its own name", sk.Path, sk)
		}
	}
}

func sizes(q [][]string) []int {
	out := make([]int, 0, len(q))
	for _, paths := range q {
		out = append(out, len(paths))
	}
	return out
}

// TestClaudeFacts_truncatedBlob: a file not read in full, or not text, is
// unreadable in blobText's words, whichever kind of file it is.
func TestClaudeFacts_truncatedBlob(t *testing.T) {
	t.Parallel()

	truncated := text("---\nname: skill-01\n")
	truncated.IsTruncated = true
	binary := &fileBlob{IsBinary: true}

	t.Run("skill", func(t *testing.T) {
		t.Parallel()
		const p = ".claude/skills/skill-01/SKILL.md"
		s := &claudeServer{blobs: map[string]*fileBlob{p: truncated}}
		got, err := newClaudeClient(t, s).claudeFacts(t.Context(), "skill-pack", files(p))
		if err != nil {
			t.Fatalf("claudeFacts() error = %v, want nil", err)
		}
		_, problem := blobText(truncated, p)
		want := audit.Claude{Skills: []audit.ClaudeSkill{{Path: p, Dir: "skill-01", Unreadable: problem}}}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("claudeFacts() mismatch (-want +got):\n%s", diff)
		}
	})
	t.Run("command and hooks file", func(t *testing.T) {
		t.Parallel()
		const command, settings = ".claude/commands/command-01.md", ".claude/settings.json"
		// settings.json is missing from the answer altogether: a path the
		// tree lists that the query cannot find is no more readable.
		s := &claudeServer{blobs: map[string]*fileBlob{command: binary}}
		got, err := newClaudeClient(t, s).claudeFacts(t.Context(), "skill-pack", files(command, settings))
		if err != nil {
			t.Fatalf("claudeFacts() error = %v, want nil", err)
		}
		_, commandProblem := blobText(binary, command)
		_, settingsProblem := blobText(nil, settings)
		want := audit.Claude{
			Commands: []audit.ClaudeSkill{{Path: command, Dir: "command-01", Unreadable: commandProblem}},
			Hooks:    []audit.ClaudeHook{{Source: settings, Unreadable: settingsProblem}},
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("claudeFacts() mismatch (-want +got):\n%s", diff)
		}
	})
}

// TestClaudeFacts_unreadableManifest: without a manifest that can be read, the
// plugin's layout cannot be decided, so no file it would add is read and the
// repository as a whole is unreadable rather than judged on the files the
// default layout happens to find.
func TestClaudeFacts_unreadableManifest(t *testing.T) {
	t.Parallel()

	const manifest = ".claude-plugin/plugin.json"
	truncated := text(`{"name": "plug`)
	truncated.IsTruncated = true
	tests := []struct {
		name    string
		blob    *fileBlob
		problem string
	}{
		{name: "truncated", blob: truncated, problem: manifest + ": too large to read in full"},
		{name: "not JSON", blob: text(`{"name":`), problem: "not valid JSON: unexpected end of JSON input"},
		{name: "bad layout", blob: text(`{"skills": 3}`), problem: "skills is not a path or a list of paths"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := &claudeServer{blobs: map[string]*fileBlob{
				manifest:                           tc.blob,
				".claude/skills/skill-01/SKILL.md": text(fmt.Sprintf(skillText, "skill-01")),
			}}
			entries := files(manifest, ".claude-plugin/marketplace.json", ".claude/skills/skill-01/SKILL.md", "skills/skill-02/SKILL.md")
			got, err := newClaudeClient(t, s).claudeFacts(t.Context(), "plugin-kit", entries)
			if err != nil {
				t.Fatalf("claudeFacts() error = %v, want nil", err)
			}
			want := audit.Claude{
				Plugin: audit.ClaudePlugin{Present: true, OwnMarketplace: true, Unreadable: tc.problem},
				// The repository-wide problem names the manifest whichever
				// way it failed.
				Unreadable: manifest + ": " + strings.TrimPrefix(tc.problem, manifest+": "),
			}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("claudeFacts() mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff([][]string{{manifest, ".claude/skills/skill-01/SKILL.md"}}, s.asked()); diff != "" {
				t.Errorf("queries mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestClaudeFacts_pluginAndProjectFiles covers a plugin that also carries
// project files: the manifest's inline commands and hooks, a declared hooks
// file, project settings hooks, and a project skill, whose hooks are not the
// plugin's. A script that is not a tracked file has no mode, and a skill
// already read in the first query is not asked for again.
func TestClaudeFacts_pluginAndProjectFiles(t *testing.T) {
	t.Parallel()

	manifest := `{
		"name": "plugin-kit",
		"commands": {
			"command-02": {"content": "---\ndescription: Placeholder description.\n---\nRun skill-01 then skill-03.\n"},
			"command-01": {"content": "---\ndescription: Placeholder description.\n---\nUse skill-01.\n"}
		},
		"hooks": [
			"./extra/hooks.json",
			{"Stop": [{"hooks": [{"type": "prompt"}]}]}
		]
	}`
	projectSkill := "---\nname: skill-03\ndescription: Placeholder description.\nhooks:\n  SessionStart:\n" +
		"    - hooks:\n        - type: command\n          command: ./hooks/missing.sh\n---\nPlaceholder body.\n"
	settings := `{"hooks": {"PreToolUse": [{"matcher": "Edit", "hooks": [{"type": "command", "command": "bash hooks/hook-01.sh"}]}]}}`
	extra := `{"hooks": {"Notification": [{"hooks": [{"type": "command", "command": "${CLAUDE_PLUGIN_ROOT}/hooks/hook-01.sh"}]}]}}`
	s := &claudeServer{blobs: map[string]*fileBlob{
		".claude-plugin/plugin.json":       text(manifest),
		".claude/settings.json":            text(settings),
		".claude/skills/skill-03/SKILL.md": text(projectSkill),
		"extra/hooks.json":                 text(extra),
		"skills/skill-01/SKILL.md":         text(fmt.Sprintf(skillText, "skill-01")),
	}}
	entries := files(
		".claude-plugin/plugin.json", ".claude/settings.json", ".claude/skills/skill-03/SKILL.md",
		"extra/hooks.json", "skills/skill-01/SKILL.md", "commands/ignored.md",
	)
	entries = append(entries, filepaths.Entry{Path: "hooks/hook-01.sh", Mode: "100755"}, filepaths.Entry{Path: "hooks", Dir: true})

	got, err := newClaudeClient(t, s).claudeFacts(t.Context(), "plugin-kit", entries)
	if err != nil {
		t.Fatalf("claudeFacts() error = %v, want nil", err)
	}
	inline := func(name string, lines int, wraps string) audit.ClaudeSkill {
		return audit.ClaudeSkill{
			Path: ".claude-plugin/plugin.json#commands/" + name, Dir: name,
			Keys: map[string]string{"description": "!!str"}, DescriptionChars: 24, Lines: lines, WrapsSkill: wraps,
		}
	}
	skill := func(p, name string, lines int, keys ...string) audit.ClaudeSkill {
		k := map[string]string{"name": "!!str", "description": "!!str"}
		for _, key := range keys {
			k[key] = "!!map"
		}
		return audit.ClaudeSkill{Path: p, Dir: name, Name: name, Keys: k, DescriptionChars: 24, Lines: lines}
	}
	want := audit.Claude{
		Skills: []audit.ClaudeSkill{
			skill(".claude/skills/skill-03/SKILL.md", "skill-03", 10, "hooks"),
			skill("skills/skill-01/SKILL.md", "skill-01", 6),
		},
		// The default commands/ is not scanned: the manifest's commands key
		// replaces it. command-02 names two skills, so it wraps neither.
		Commands: []audit.ClaudeSkill{inline("command-01", 4, "skill-01"), inline("command-02", 4, "")},
		Hooks: []audit.ClaudeHook{
			{Source: ".claude-plugin/plugin.json", Plugin: true, Event: "Stop", Type: "prompt"},
			{
				Source: ".claude/settings.json", Event: "PreToolUse", Type: "command", Matcher: "Edit",
				Script: "hooks/hook-01.sh", Interpreter: true, PathForm: "relative", ScriptMode: "100755",
			},
			{
				Source: ".claude/skills/skill-03/SKILL.md", Event: "SessionStart", Type: "command",
				Script: "hooks/missing.sh", PathForm: "relative",
			},
			{
				Source: "extra/hooks.json", Plugin: true, Event: "Notification", Type: "command",
				Script: "hooks/hook-01.sh", PathForm: "plugin_root", ScriptMode: "100755",
			},
		},
		Plugin: audit.ClaudePlugin{Present: true, Name: "plugin-kit", Fields: []string{"name"}},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("claudeFacts() mismatch (-want +got):\n%s", diff)
	}
	wantAsked := [][]string{
		{".claude-plugin/plugin.json", ".claude/settings.json", ".claude/skills/skill-03/SKILL.md"},
		{"extra/hooks.json", "skills/skill-01/SKILL.md"},
	}
	if diff := cmp.Diff(wantAsked, s.asked()); diff != "" {
		t.Errorf("queries mismatch (-want +got):\n%s", diff)
	}
}

// TestClaudeFacts_queryFailure: a Claude query that fails fails the run, the
// way the file query does, rather than recording every file as unreadable.
func TestClaudeFacts_queryFailure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		body      string
		want      string
		failAfter int
	}{
		{name: "GraphQL error", body: `{"errors": [{"type": "NOT_FOUND", "message": "gone"}]}`, want: "gone"},
		{name: "no repository", body: `{"data": {"repository": null}}`, want: "no repository in the Claude file query's response"},
		// The manifest's round answers; the round it decides fails.
		{name: "second round", body: `{"errors": [{"type": "NOT_FOUND", "message": "gone"}]}`, want: "gone", failAfter: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := &claudeServer{body: tc.body, failAfter: tc.failAfter, blobs: map[string]*fileBlob{
				".claude-plugin/plugin.json": text(`{"name": "plugin-kit"}`),
			}}
			entries := files(".claude-plugin/plugin.json", "skills/skill-01/SKILL.md")
			if tc.failAfter == 0 {
				entries = files(".claude/skills/skill-01/SKILL.md")
			}
			_, err := newClaudeClient(t, s).claudeFacts(t.Context(), "skill-pack", entries)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("claudeFacts() error = %v, want one containing %q", err, tc.want)
			}
		})
	}
}
