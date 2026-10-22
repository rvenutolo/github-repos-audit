package rules_test

import (
	"testing"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
	"github.com/rvenutolo/github-repos-audit/internal/rules"
)

// TestEvaluate_artifactStorage_value: artifact_storage shows how many
// unexpired artifacts a repository holds and their total size, and is never
// judged, so even a large holding is information rather than a fail. With
// none, the cell is a bare count with no size beside it.
func TestEvaluate_artifactStorage_value(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		count int
		bytes int64
		want  string
	}{
		{"none", 0, 0, "0"},
		{"one small", 1, 430, "1 · 430 B"},
		{"several", 12, 206_000_000, "12 · 206 MB"},
		{"large", 40, 2_200_000_000, "40 · 2.2 GB"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := fixture("alpha", "tools")
			r.Storage = audit.ActionsStorage{ArtifactCount: tc.count, ArtifactBytes: tc.bytes}
			rep := mustEvaluateOne(t, r)
			got := rep.Repos[0].Cell(rules.CheckArtifactStorage)
			if got.Verdict != rules.Info || got.Value != tc.want || !got.Scalar || got.Mark {
				t.Errorf("cell = %v %q scalar=%t mark=%t, want an unmarked scalar info %q",
					got.Verdict, got.Value, got.Scalar, got.Mark, tc.want)
			}
			for _, g := range rep.Gaps {
				if g.Check == rules.CheckArtifactStorage {
					t.Errorf("Gaps holds artifact_storage %v, want it never a gap", g)
				}
			}
		})
	}
}
