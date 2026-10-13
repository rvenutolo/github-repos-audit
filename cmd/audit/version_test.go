package main

import (
	"runtime/debug"
	"testing"
)

func TestResolveVersion(t *testing.T) {
	t.Parallel()

	info := func(v string) func() (*debug.BuildInfo, bool) {
		return func() (*debug.BuildInfo, bool) {
			return &debug.BuildInfo{Main: debug.Module{Version: v}}, true
		}
	}
	none := func() (*debug.BuildInfo, bool) { return nil, false }

	tests := []struct {
		name   string
		linked string
		read   func() (*debug.BuildInfo, bool)
		want   string
	}{
		{"a linked-in version wins over build info", "v1.2.3", info("v9.9.9"), "v1.2.3"},
		{"go install reports the module version", "dev", info("v1.0.0"), "v1.0.0"},
		{"a checkout build has no module version", "dev", info("(devel)"), "dev"},
		{"an empty module version is not a version", "dev", info(""), "dev"},
		{"no build info at all", "dev", none, "dev"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := resolveVersion(tt.linked, tt.read); got != tt.want {
				t.Errorf("resolveVersion(%q) = %q, want %q", tt.linked, got, tt.want)
			}
		})
	}
}
