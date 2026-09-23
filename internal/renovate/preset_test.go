package renovate_test

import (
	"testing"

	"github.com/rvenutolo/github-repos-audit/internal/renovate"
)

func TestParsePreset(t *testing.T) {
	t.Parallel()

	tests := []struct {
		ref          string
		want         renovate.Preset
		wantFallback bool
		wantFollow   bool
		wantProblem  bool
	}{
		{"github>gh-owner/preset-store", renovate.Preset{Repo: "preset-store", Path: "default.json"}, true, true, false},
		{"local>gh-owner/preset-store", renovate.Preset{Repo: "preset-store", Path: "default.json"}, true, true, false},
		{"gh-owner/preset-store", renovate.Preset{Repo: "preset-store", Path: "default.json"}, true, true, false},
		{"github>GH-Owner/preset-store", renovate.Preset{Repo: "preset-store", Path: "default.json"}, true, true, false},
		{"github>gh-owner/preset-store:go", renovate.Preset{Repo: "preset-store", Path: "go.json"}, false, true, false},
		{"github>gh-owner/preset-store:go.json5", renovate.Preset{Repo: "preset-store", Path: "go.json5"}, false, true, false},
		{"github>gh-owner/preset-store//dir/go", renovate.Preset{Repo: "preset-store", Path: "dir/go.json"}, false, true, false},
		{"github>gh-owner/preset-store#v1.2.3", renovate.Preset{Repo: "preset-store", Path: "default.json", Ref: "v1.2.3"}, true, true, false},
		{"github>gh-owner/preset-store:go#main", renovate.Preset{Repo: "preset-store", Path: "go.json", Ref: "main"}, false, true, false},
		{"github>gh-owner/preset-store:labels(a,b)", renovate.Preset{Repo: "preset-store", Path: "labels.json"}, false, true, false},

		// Not in the account: skipped, contributes nothing.
		{"config:recommended", renovate.Preset{}, false, false, false},
		{":semanticCommits", renovate.Preset{}, false, false, false},
		{"security:minimumReleaseAgeNpm", renovate.Preset{}, false, false, false},
		{"github>someone-else/presets", renovate.Preset{}, false, false, false},
		{"gitlab>gh-owner/preset-store", renovate.Preset{}, false, false, false},
		{"npm>renovate-config-x", renovate.Preset{}, false, false, false},
		{"@scope/renovate-config", renovate.Preset{}, false, false, false},
		{"https://example.com/preset.json", renovate.Preset{}, false, false, false},

		// In the account but unresolvable.
		{"github>gh-owner/preset-store//dir/go:sub", renovate.Preset{}, false, true, true},
		{"github>gh-owner/preset-store:go/sub", renovate.Preset{}, false, true, true},
		{"github>gh-owner", renovate.Preset{}, false, false, false},
	}
	for _, tt := range tests {
		p, fallback, follow, problem := renovate.ParsePreset(tt.ref, "gh-owner")
		if follow != tt.wantFollow || (problem != "") != tt.wantProblem {
			t.Errorf("ParsePreset(%q) follow = %t, problem = %q; want follow %t, problem %t",
				tt.ref, follow, problem, tt.wantFollow, tt.wantProblem)
			continue
		}
		if follow && !tt.wantProblem && (p != tt.want || fallback != tt.wantFallback) {
			t.Errorf("ParsePreset(%q) = %+v, fallback %t; want %+v, fallback %t", tt.ref, p, fallback, tt.want, tt.wantFallback)
		}
	}
}
