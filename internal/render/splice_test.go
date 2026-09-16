package render_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/rvenutolo/github-repos-audit/internal/render"
)

const readmeWithMarkers = `# title

Hand-written prose.

` + render.BeginMarker + `

_No report yet._

` + render.EndMarker + `

More prose below.
`

func TestSplice_replacesOnlyTheGeneratedBlock(t *testing.T) {
	t.Parallel()

	got, err := render.Splice(readmeWithMarkers, "## Gaps\n\n- nothing\n")
	if err != nil {
		t.Fatalf("Splice() error = %v, want nil", err)
	}
	for _, want := range []string{"# title", "Hand-written prose.", "More prose below.", "- nothing"} {
		if !strings.Contains(got, want) {
			t.Errorf("Splice() dropped %q from:\n%s", want, got)
		}
	}
	if strings.Contains(got, "_No report yet._") {
		t.Error("Splice() left the old block behind")
	}
}

func TestSplice_isIdempotent(t *testing.T) {
	t.Parallel()

	block := "## Gaps\n\n- nothing\n"
	once, err := render.Splice(readmeWithMarkers, block)
	if err != nil {
		t.Fatalf("Splice() error = %v, want nil", err)
	}
	twice, err := render.Splice(once, block)
	if err != nil {
		t.Fatalf("Splice() error = %v, want nil", err)
	}
	// The daily run splices into its own previous output. If that were not a
	// no-op, every refresh would drift the file by a blank line.
	if once != twice {
		t.Errorf("Splice() is not idempotent:\n--- once ---\n%s\n--- twice ---\n%s", once, twice)
	}
}

// brokenReadmes is every marker layout Splice must refuse. FuzzSplice seeds
// from the same table.
var brokenReadmes = []struct {
	name   string
	readme string
}{
	{name: "no markers at all", readme: "# title\n"},
	{name: "no closing marker", readme: "# title\n\n" + render.BeginMarker + "\n"},
	{name: "closing marker first", readme: render.EndMarker + "\n" + render.BeginMarker + "\n"},
	{
		name:   "the opening marker twice",
		readme: render.BeginMarker + "\n" + render.BeginMarker + "\n" + render.EndMarker + "\n",
	},
	{
		name:   "the closing marker twice",
		readme: render.BeginMarker + "\n" + render.EndMarker + "\n" + render.EndMarker + "\n",
	},
	{
		name: "a stray closing marker before the block",
		readme: "# title\n\n" + render.EndMarker + "\n\nprose\n\n" + render.BeginMarker +
			"\n\nbody\n\n" + render.EndMarker + "\n\nprose\n",
	},
}

func TestSplice_rejectsABrokenReadme(t *testing.T) {
	t.Parallel()

	for _, tc := range brokenReadmes {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Guessing where the block belongs would either duplicate the
			// report or overwrite prose somebody wrote, so this is fatal.
			_, err := render.Splice(tc.readme, "block")
			if err == nil {
				t.Fatal("Splice() error = nil, want a marker error")
			}
			if !errors.Is(err, render.ErrMarkers) {
				t.Errorf("Splice() error = %v, want it to wrap ErrMarkers", err)
			}
		})
	}
}

func TestSplice_rejectsABlockContainingAMarker(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		block string
	}{
		{name: "the opening marker", block: "## Gaps\n\n" + render.BeginMarker + "\n"},
		{name: "the closing marker", block: "## Gaps\n\n" + render.EndMarker + "\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Writing such a block would produce a README the next run refuses
			// to read, so it is refused up front instead.
			got, err := render.Splice(readmeWithMarkers, tc.block)
			if err == nil {
				t.Fatal("Splice() error = nil, want a marker error")
			}
			if !errors.Is(err, render.ErrMarkers) {
				t.Errorf("Splice() error = %v, want it to wrap ErrMarkers", err)
			}
			if got != "" {
				t.Errorf("Splice() = %q alongside an error, want empty", got)
			}
		})
	}
}

func TestGeneratedBlock(t *testing.T) {
	t.Parallel()

	got, err := render.GeneratedBlock(readmeWithMarkers)
	if err != nil {
		t.Fatalf("GeneratedBlock() error = %v, want nil", err)
	}
	if !strings.Contains(got, "_No report yet._") {
		t.Errorf("GeneratedBlock() = %q, want the block between the markers", got)
	}
	if strings.Contains(got, "Hand-written prose.") {
		t.Error("GeneratedBlock() reached outside the markers")
	}
}

// TestGeneratedBlock_rejectsABrokenReadme holds the reader to the same marker
// rules as the writer. A layout Splice refuses is one whose block boundaries
// are ambiguous, and reading it leniently would hand the change guard a block
// chosen by guesswork from a README nobody can safely regenerate.
func TestGeneratedBlock_rejectsABrokenReadme(t *testing.T) {
	t.Parallel()

	for _, tc := range brokenReadmes {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := render.GeneratedBlock(tc.readme)
			if err == nil {
				t.Fatal("GeneratedBlock() error = nil, want a marker error")
			}
			if !errors.Is(err, render.ErrMarkers) {
				t.Errorf("GeneratedBlock() error = %v, want it to wrap ErrMarkers", err)
			}
			if got != "" {
				t.Errorf("GeneratedBlock() = %q alongside an error, want empty", got)
			}
		})
	}
}

// FuzzSplice holds Splice to its invariants over arbitrary input: it never
// panics; an error comes with no output; and a success carries exactly the
// block between the markers, with the prose on either side untouched, and is
// a fixed point — splicing the same block into its own output changes nothing,
// which is what keeps the daily refresh from drifting the file.
func FuzzSplice(f *testing.F) {
	f.Add(readmeWithMarkers, "## Gaps\n\n- nothing\n")
	f.Add(readmeWithMarkers, "")
	f.Add(readmeWithMarkers, render.BeginMarker)
	f.Add(readmeWithMarkers, render.EndMarker)
	for _, tc := range brokenReadmes {
		f.Add(tc.readme, "block")
	}

	f.Fuzz(func(t *testing.T, readme, block string) {
		out, err := render.Splice(readme, block)
		// A block carrying a marker of its own would produce a README the next
		// run refuses to read, so it must be refused up front.
		if strings.Contains(block, render.BeginMarker) || strings.Contains(block, render.EndMarker) {
			if !errors.Is(err, render.ErrMarkers) {
				t.Errorf("Splice(block with a marker) error = %v, want ErrMarkers", err)
			}
		}
		if err != nil {
			if out != "" {
				t.Errorf("Splice() = %q alongside error %v, want empty", out, err)
			}
			return
		}

		before, _, _ := strings.Cut(readme, render.BeginMarker)
		if !strings.HasPrefix(out, before+render.BeginMarker) {
			t.Errorf("Splice() = %q, want it to keep the prose before the block", out)
		}
		// A success means each marker appears exactly once, so the closing
		// marker's position is unambiguous.
		end := strings.Index(readme, render.EndMarker)
		if !strings.HasSuffix(out, readme[end:]) {
			t.Errorf("Splice() = %q, want it to keep the prose after the block", out)
		}
		got, err := render.GeneratedBlock(out)
		if err != nil {
			t.Fatalf("GeneratedBlock(Splice()) error = %v, want nil", err)
		}
		if want := "\n\n" + strings.TrimSpace(block) + "\n\n"; got != want {
			t.Errorf("GeneratedBlock(Splice()) = %q, want %q", got, want)
		}
		again, err := render.Splice(out, block)
		if err != nil {
			t.Fatalf("Splice(Splice()) error = %v, want nil", err)
		}
		if again != out {
			t.Errorf("Splice() is not a fixed point:\n--- once ---\n%s\n--- twice ---\n%s", out, again)
		}
	})
}
