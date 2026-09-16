package render_test

import (
	"strings"
	"testing"
	"time"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
	"github.com/rvenutolo/github-repos-audit/internal/render"
)

// artefacts renders both committed files for a snapshot, the way `audit
// render` does.
func artefacts(t *testing.T, snap *audit.Snapshot, now time.Time) (readme string, data []byte) {
	t.Helper()
	block, err := render.Block(evaluate(t, snap), clock(now))
	if err != nil {
		t.Fatalf("Block() error = %v, want nil", err)
	}
	readme, err = render.Splice(readmeWithMarkers, block)
	if err != nil {
		t.Fatalf("Splice() error = %v, want nil", err)
	}
	snap.GeneratedAt = now
	data, err = render.JSON(snap)
	if err != nil {
		t.Fatalf("JSON() error = %v, want nil", err)
	}
	return readme, data
}

// TestMaterialChange_ignoresTheClock is the assertion the whole guard exists
// for. Three things move on every single run against a live account — the
// footer's date, the relative dates, and an audited repository's own
// pushed_at, which any push at all advances — and none of them is a reason to
// open a pull request and burn a full Nix gate.
func TestMaterialChange_ignoresTheClock(t *testing.T) {
	t.Parallel()

	before := fullSnapshot()
	oldREADME, oldJSON := artefacts(t, before, renderedAt)

	// A day later: every timestamp has moved, one audited repository has had
	// an ordinary push — advancing its own head — and nothing else has.
	later := renderedAt.Add(24 * time.Hour)
	after := fullSnapshot()
	for i := range after.Repos {
		if after.Repos[i].HeadOID != "" {
			after.Repos[i].HeadOID = "89abcdef0123456789abcdef0123456789abcdef"
		}
		// A zero instant means "never", not "the epoch": advancing it would
		// turn a repository that has never been pushed to into one pushed two
		// thousand years ago, which is a real change and not a clock tick.
		if !after.Repos[i].PushedAt.IsZero() {
			after.Repos[i].PushedAt = after.Repos[i].PushedAt.Add(24 * time.Hour)
		}
		if !after.Repos[i].Releases.LastPublishedAt.IsZero() {
			after.Repos[i].Releases.LastPublishedAt = after.Repos[i].Releases.LastPublishedAt.Add(24 * time.Hour)
		}
	}
	newREADME, newJSON := artefacts(t, after, later)

	changed, err := render.MaterialChange(oldJSON, newJSON, oldREADME, newREADME)
	if err != nil {
		t.Fatalf("MaterialChange() error = %v, want nil", err)
	}
	if changed {
		t.Errorf("MaterialChange() = true, want false; only timestamps moved")
	}
}

func TestMaterialChange_seesAChangedVerdict(t *testing.T) {
	t.Parallel()

	before := fullSnapshot()
	oldREADME, oldJSON := artefacts(t, before, renderedAt)

	after := fullSnapshot()
	after.Repos[1].License = "CC0-1.0" // cipher-lib gains a license
	newREADME, newJSON := artefacts(t, after, renderedAt)

	changed, err := render.MaterialChange(oldJSON, newJSON, oldREADME, newREADME)
	if err != nil {
		t.Fatalf("MaterialChange() error = %v, want nil", err)
	}
	if !changed {
		t.Error("MaterialChange() = false, want true; a license appeared")
	}
}

func TestMaterialChange_absentArtefactsCountAsAChange(t *testing.T) {
	t.Parallel()

	newREADME, newJSON := artefacts(t, fullSnapshot(), renderedAt)

	for _, tc := range []struct {
		name    string
		oldJSON []byte
		old     string
	}{
		{name: "no audit.json", oldJSON: nil, old: newREADME},
		{name: "no README", oldJSON: newJSON, old: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			changed, err := render.MaterialChange(tc.oldJSON, newJSON, tc.old, newREADME)
			if err != nil {
				t.Fatalf("MaterialChange() error = %v, want nil", err)
			}
			if !changed {
				t.Error("MaterialChange() = false, want true; there is nothing to compare against")
			}
		})
	}
}

// TestMaterialChange_seesAReadmeOnlyChange covers the case audit.json cannot:
// a change to how something renders rather than to what it is.
func TestMaterialChange_seesAReadmeOnlyChange(t *testing.T) {
	t.Parallel()

	readme, data := artefacts(t, fullSnapshot(), renderedAt)
	edited := strings.Replace(readme, "## Gaps", "## Outstanding", 1)

	changed, err := render.MaterialChange(data, data, readme, edited)
	if err != nil {
		t.Fatalf("MaterialChange() error = %v, want nil", err)
	}
	if !changed {
		t.Error("MaterialChange() = false, want true; the rendered block differs")
	}
}

func TestMaterialChange_reportsBrokenInput(t *testing.T) {
	t.Parallel()

	readme, data := artefacts(t, fullSnapshot(), renderedAt)

	if _, err := render.MaterialChange([]byte("{"), data, readme, readme); err == nil {
		t.Error("MaterialChange() error = nil, want a parse error for the committed JSON")
	}
	if _, err := render.MaterialChange(data, []byte("{"), readme, readme); err == nil {
		t.Error("MaterialChange() error = nil, want a parse error for the rendered JSON")
	}
	if _, err := render.MaterialChange(data, data, "# no markers\n", readme); err == nil {
		t.Error("MaterialChange() error = nil, want a marker error for the committed README")
	}
	if _, err := render.MaterialChange(data, data, readme, "# no markers\n"); err == nil {
		t.Error("MaterialChange() error = nil, want a marker error for the rendered README")
	}
}

// TestMaterialChange_ignoresAMovedHead is the case that turned the daily cron
// into a nightly pull request over a SHA. This repository's own head advances
// with every merge, the previous refresh's own merge included, so a head_oid
// left in the comparison means the guard can never fire.
func TestMaterialChange_ignoresAMovedHead(t *testing.T) {
	t.Parallel()

	before := fullSnapshot()
	oldREADME, oldJSON := artefacts(t, before, renderedAt)

	after := fullSnapshot()
	after.Repos[2].HeadOID = "ffffffffffffffffffffffffffffffffffffffff"
	newREADME, newJSON := artefacts(t, after, renderedAt)

	changed, err := render.MaterialChange(oldJSON, newJSON, oldREADME, newREADME)
	if err != nil {
		t.Fatalf("MaterialChange() error = %v, want nil", err)
	}
	if changed {
		t.Error("MaterialChange() = true, want false; only the head commit moved")
	}
}
