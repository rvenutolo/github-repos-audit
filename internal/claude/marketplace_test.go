package claude_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/rvenutolo/github-repos-audit/internal/claude"
)

func TestParseMarketplace(t *testing.T) {
	t.Parallel()

	const sha = "0123456789abcdef0123456789abcdef01234567"
	tests := []struct {
		name string
		text string
		want claude.Marketplace
	}{
		{
			name: "github source with ref and sha",
			text: `{"name": "m", "owner": {"name": "o"}, "plugins": [
				{"name": "plugin-kit", "source": {"source": "github", "repo": "gh-owner/plugin-kit", "ref": "v1.2.0", "sha": "` + sha + `"}}
			]}`,
			want: claude.Marketplace{Entries: []claude.MarketplaceEntry{
				{Name: "plugin-kit", Repo: "gh-owner/plugin-kit", Ref: "v1.2.0", SHA: sha},
			}},
		},
		{
			name: "an entry carrying a version",
			text: `{"plugins": [{"name": "plugin-kit", "version": "1.2.0", "source": {"source": "github", "repo": "gh-owner/plugin-kit"}}]}`,
			want: claude.Marketplace{Entries: []claude.MarketplaceEntry{
				{Name: "plugin-kit", Repo: "gh-owner/plugin-kit", HasVersion: true},
			}},
		},
		{
			name: "a relative path source",
			text: `{"plugins": [{"name": "skill-pack", "source": "./x"}]}`,
			want: claude.Marketplace{Entries: []claude.MarketplaceEntry{{Name: "skill-pack"}}},
		},
		{
			name: "a non-github object source",
			text: `{"plugins": [{"name": "skill-pack", "source": {"source": "url", "url": "https://example.invalid/x.git", "ref": "main"}}]}`,
			want: claude.Marketplace{Entries: []claude.MarketplaceEntry{{Name: "skill-pack"}}},
		},
		{
			name: "entries keep their declared order",
			text: `{"plugins": [{"name": "b", "source": "./b"}, {"name": "a", "source": "./a"}]}`,
			want: claude.Marketplace{Entries: []claude.MarketplaceEntry{{Name: "b"}, {Name: "a"}}},
		},
		{
			name: "no plugins at all is an empty list",
			text: `{"plugins": []}`,
			want: claude.Marketplace{},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if diff := cmp.Diff(tc.want, claude.ParseMarketplace(tc.text)); diff != "" {
				t.Errorf("ParseMarketplace() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestParseMarketplace_unreadable: a file not in the shape Claude Code loads
// yields no entries at all, only the reason, so no marketplace check can pass
// on the part that happened to parse.
func TestParseMarketplace_unreadable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, text, want string
	}{
		{name: "invalid JSON", text: `{"plugins": [`, want: "not valid JSON"},
		{name: "not an object", text: `[]`, want: "marketplace.json is not a JSON object"},
		{name: "plugins absent", text: `{"name": "m"}`, want: "plugins is not a list"},
		{name: "plugins not an array", text: `{"plugins": {"name": "x"}}`, want: "plugins is not a list"},
		{name: "an entry not an object", text: `{"plugins": ["x"]}`, want: "plugins[0] is not an object"},
		{name: "an entry with no name", text: `{"plugins": [{"source": "./x"}]}`, want: "plugins[0] has no name"},
		{name: "an entry with an empty name", text: `{"plugins": [{"name": "", "source": "./x"}]}`, want: "plugins[0] has no name"},
		{name: "an entry with no source", text: `{"plugins": [{"name": "x"}]}`, want: "plugins[0] has no source"},
		{name: "a source of the wrong type", text: `{"plugins": [{"name": "x", "source": 3}]}`, want: "plugins[0] has no source"},
		{
			name: "a github source whose repo is not a string",
			text: `{"plugins": [{"name": "x", "source": {"source": "github", "repo": 3}}]}`,
			want: "plugins[0].source.repo is not a string",
		},
		{
			name: "a github source whose ref is not a string",
			text: `{"plugins": [{"name": "x", "source": {"source": "github", "repo": "gh-owner/plugin-kit", "ref": 1}}]}`,
			want: "plugins[0].source.ref is not a string",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := claude.ParseMarketplace(tc.text)
			if !strings.HasPrefix(got.Unreadable, tc.want) {
				t.Errorf("Unreadable = %q, want it to start with %q", got.Unreadable, tc.want)
			}
			if got.Entries != nil {
				t.Errorf("Entries = %v, want none alongside Unreadable", got.Entries)
			}
		})
	}
}
