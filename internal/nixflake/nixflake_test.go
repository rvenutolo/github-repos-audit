package nixflake_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/rvenutolo/github-repos-audit/internal/nixflake"
)

// parse fails the test when the lock did not parse, so each spec test below
// can compare whole values without first ruling out a problem.
func parse(t *testing.T, text string) nixflake.LockFacts {
	t.Helper()
	lock, problem := nixflake.ParseLock(text)
	if problem != "" {
		t.Fatalf("ParseLock() problem = %q, want none", problem)
	}
	return lock
}

func TestInputs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		text string
		want []string
	}{
		{
			"attribute form, sorted",
			`{
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    lint.url = "github:gh-owner/lint-flake";
  };
}`,
			[]string{"github:NixOS/nixpkgs/nixos-unstable", "github:gh-owner/lint-flake"},
		},
		{
			"nested set with free spacing",
			"{ inputs.lint = { url   =\"github:gh-owner/lint-flake\"; inputs.nixpkgs.follows = \"nixpkgs\"; }; }",
			[]string{"github:gh-owner/lint-flake"},
		},
		{
			"repeated",
			`{ inputs.a.url = "github:gh-owner/lint-flake"; inputs.b.url = "github:gh-owner/lint-flake"; }`,
			[]string{"github:gh-owner/lint-flake"},
		},
		{"none", `{ outputs = _: { }; }`, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if diff := cmp.Diff(tt.want, nixflake.Inputs(tt.text)); diff != "" {
				t.Errorf("Inputs() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestParseLock_directNixpkgs(t *testing.T) {
	t.Parallel()

	got := parse(t, `{
  "nodes": {
    "nixpkgs": {
      "locked": {"lastModified": 1760918400, "owner": "NixOS", "repo": "nixpkgs", "rev": "abc", "type": "github"},
      "original": {"owner": "NixOS", "ref": "nixos-unstable", "repo": "nixpkgs", "type": "github"}
    },
    "root": {"inputs": {"nixpkgs": "nixpkgs"}}
  },
  "root": "root",
  "version": 7
}`)
	if want := time.Unix(1760918400, 0).UTC(); !got.NixpkgsLastModified.Equal(want) ||
		got.NixpkgsLastModified.Location() != time.UTC {
		t.Errorf("NixpkgsLastModified = %v, want %v", got.NixpkgsLastModified, want)
	}
}

func TestParseLock_followsChain(t *testing.T) {
	t.Parallel()

	// The root's own nixpkgs follows lint's, which is the node nixpkgs_2.
	// The node named "nixpkgs" is a decoy: resolving by name would pick the
	// wrong date.
	got := parse(t, `{
  "nodes": {
    "lint": {
      "inputs": {"nixpkgs": "nixpkgs_2"},
      "locked": {"lastModified": 1700000000, "owner": "gh-owner", "repo": "lint-flake", "type": "github"},
      "original": {"owner": "gh-owner", "repo": "lint-flake", "type": "github"}
    },
    "nixpkgs": {
      "locked": {"lastModified": 1600000000, "type": "github"},
      "original": {"owner": "NixOS", "repo": "nixpkgs", "type": "github"}
    },
    "nixpkgs_2": {
      "locked": {"lastModified": 1760918400, "type": "github"},
      "original": {"owner": "NixOS", "repo": "nixpkgs", "type": "github"}
    },
    "root": {"inputs": {"lint": "lint", "nixpkgs": ["lint", "nixpkgs"]}}
  },
  "root": "root",
  "version": 7
}`)
	if want := time.Unix(1760918400, 0).UTC(); !got.NixpkgsLastModified.Equal(want) {
		t.Errorf("NixpkgsLastModified = %v, want %v", got.NixpkgsLastModified, want)
	}
}

func TestParseLock_followsAFollow(t *testing.T) {
	t.Parallel()

	// lint's nixpkgs itself follows tools' nixpkgs, so the path has to be
	// resolved again from the root.
	got := parse(t, `{
  "nodes": {
    "lint": {"inputs": {"nixpkgs": ["tools", "nixpkgs"]}, "original": {"type": "github", "owner": "gh-owner", "repo": "lint-flake"}},
    "tools": {"inputs": {"nixpkgs": "nixpkgs_3"}, "original": {"type": "github", "owner": "gh-owner", "repo": "lint-flake"}},
    "nixpkgs_3": {"locked": {"lastModified": 1760918400}, "original": {"type": "github", "owner": "NixOS", "repo": "nixpkgs"}},
    "root": {"inputs": {"lint": "lint", "tools": "tools", "nixpkgs": ["lint", "nixpkgs"]}}
  },
  "root": "root",
  "version": 7
}`)
	if want := time.Unix(1760918400, 0).UTC(); !got.NixpkgsLastModified.Equal(want) {
		t.Errorf("NixpkgsLastModified = %v, want %v", got.NixpkgsLastModified, want)
	}
}

func TestParseLock_customRootName(t *testing.T) {
	t.Parallel()

	got := parse(t, `{
  "nodes": {
    "nixpkgs": {"locked": {"lastModified": 1760918400}, "original": {"type": "github", "owner": "NixOS", "repo": "nixpkgs"}},
    "top": {"inputs": {"nixpkgs": "nixpkgs"}}
  },
  "root": "top",
  "version": 7
}`)
	if want := time.Unix(1760918400, 0).UTC(); !got.NixpkgsLastModified.Equal(want) {
		t.Errorf("NixpkgsLastModified = %v, want %v", got.NixpkgsLastModified, want)
	}
}

func TestParseLock_rootNameDefaults(t *testing.T) {
	t.Parallel()

	// A lock that leaves out `root` has its root node at the name Nix
	// itself uses.
	got := parse(t, `{
  "nodes": {
    "nixpkgs": {"locked": {"lastModified": 1760918400}, "original": {"type": "github", "owner": "NixOS", "repo": "nixpkgs"}},
    "root": {"inputs": {"nixpkgs": "nixpkgs"}}
  },
  "version": 7
}`)
	if want := time.Unix(1760918400, 0).UTC(); !got.NixpkgsLastModified.Equal(want) {
		t.Errorf("NixpkgsLastModified = %v, want %v", got.NixpkgsLastModified, want)
	}
}

func TestParseLock_noNixpkgs(t *testing.T) {
	t.Parallel()

	got := parse(t, `{
  "nodes": {
    "lint": {"locked": {"lastModified": 1760918400}, "original": {"type": "github", "owner": "gh-owner", "repo": "lint-flake"}},
    "root": {"inputs": {"lint": "lint"}}
  },
  "root": "root",
  "version": 7
}`)
	if !got.NixpkgsLastModified.IsZero() {
		t.Errorf("NixpkgsLastModified = %v, want zero", got.NixpkgsLastModified)
	}
}

func TestParseLock_noInputsAtAll(t *testing.T) {
	t.Parallel()

	got := parse(t, `{"nodes": {"root": {}}, "root": "root", "version": 7}`)
	if diff := cmp.Diff(nixflake.LockFacts{}, got); diff != "" {
		t.Errorf("ParseLock() mismatch (-want +got):\n%s", diff)
	}
}

func TestParseLock_lockedInputs(t *testing.T) {
	t.Parallel()

	got := parse(t, `{
  "nodes": {
    "lint": {"original": {"type": "github", "owner": "GH-Owner", "repo": "Lint-Flake"}},
    "lint_2": {"original": {"type": "github", "owner": "gh-owner", "repo": "lint-flake", "ref": "main"}},
    "nixpkgs": {"locked": {"lastModified": 1760918400}, "original": {"type": "github", "owner": "NixOS", "repo": "nixpkgs"}},
    "local": {"original": {"type": "path", "path": "./sub"}},
    "tarball": {"original": {"type": "tarball", "url": "https://example.com/x.tar.gz"}},
    "root": {"inputs": {"lint": "lint", "nixpkgs": "nixpkgs", "local": "local", "tarball": "tarball"}}
  },
  "root": "root",
  "version": 7
}`)
	want := []string{"github:gh-owner/lint-flake", "github:nixos/nixpkgs"}
	if diff := cmp.Diff(want, got.LockedInputs); diff != "" {
		t.Errorf("LockedInputs mismatch (-want +got):\n%s", diff)
	}
}

func TestParseLock_allRevPinned(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		text string
		want bool
	}{
		{
			"every input has rev",
			`{"nodes": {
  "lint": {"original": {"type": "github", "owner": "gh-owner", "repo": "lint-flake", "rev": "abc"}},
  "nixpkgs": {"locked": {"lastModified": 1760918400}, "original": {"type": "github", "owner": "NixOS", "repo": "nixpkgs", "rev": "def"}},
  "root": {"inputs": {"lint": "lint", "nixpkgs": "nixpkgs"}}
}, "root": "root", "version": 7}`,
			true,
		},
		{
			// A transitive input counts too: it is a node of this lock, and
			// whatever refreshes the lock refreshes it.
			"a transitive input follows a branch",
			`{"nodes": {
  "lint": {"inputs": {"systems": "systems"}, "original": {"type": "github", "owner": "gh-owner", "repo": "lint-flake", "rev": "abc"}},
  "systems": {"original": {"type": "github", "owner": "nix-systems", "repo": "default"}},
  "root": {"inputs": {"lint": "lint"}}
}, "root": "root", "version": 7}`,
			false,
		},
		{
			"an input with no original",
			`{"nodes": {
  "lint": {"locked": {"lastModified": 1}},
  "root": {"inputs": {"lint": "lint"}}
}, "root": "root", "version": 7}`,
			false,
		},
		{
			// Nothing pinned is not the same as everything pinned.
			"no inputs",
			`{"nodes": {"root": {}}, "root": "root", "version": 7}`,
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := parse(t, tt.text).AllRevPinned; got != tt.want {
				t.Errorf("AllRevPinned = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseLock_invalid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		text string
	}{
		{"not JSON", "{"},
		{"not an object", "[]"},
		{"no nodes", `{"root": "root", "version": 7}`},
		{"root node missing", `{"nodes": {}, "root": "root", "version": 7}`},
		{"input names a missing node", `{"nodes": {"root": {"inputs": {"nixpkgs": "gone"}}}, "root": "root", "version": 7}`},
		{"input is neither a name nor a path", `{"nodes": {"root": {"inputs": {"nixpkgs": 7}}}, "root": "root", "version": 7}`},
		{
			"follows path through a missing input",
			`{"nodes": {"lint": {}, "root": {"inputs": {"lint": "lint", "nixpkgs": ["lint", "nixpkgs"]}}}, "root": "root", "version": 7}`,
		},
		{
			"follows cycle",
			`{"nodes": {"root": {"inputs": {"a": ["b"], "b": ["a"], "nixpkgs": ["a"]}}}, "root": "root", "version": 7}`,
		},
		{
			"nixpkgs has no lastModified",
			`{"nodes": {"nixpkgs": {"locked": {}}, "root": {"inputs": {"nixpkgs": "nixpkgs"}}}, "root": "root", "version": 7}`,
		},
		{
			"nixpkgs follows the root",
			`{"nodes": {"root": {"inputs": {"nixpkgs": []}}}, "root": "root", "version": 7}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			lock, problem := nixflake.ParseLock(tt.text)
			if problem == "" {
				t.Errorf("ParseLock(%q) problem is empty, want one", tt.text)
			}
			// A lock that cannot be read in full yields nothing, so a check
			// cannot pass on the part that was read.
			if diff := cmp.Diff(nixflake.LockFacts{}, lock); diff != "" {
				t.Errorf("ParseLock() carries facts beside the problem (-want +got):\n%s", diff)
			}
		})
	}
}

func TestParseLock_fanInFollowsIsFast(t *testing.T) {
	t.Parallel()

	// Every hub input h_k follows a path whose last two steps both resolve
	// h_(k-1) again, so resolving without remembering results costs twice as
	// much per level and never finishes at this depth. Each input resolves
	// to the hub itself, which carries the date.
	const levels = 30
	inputs := []string{`"h_0": "hub"`}
	for k := 1; k <= levels; k++ {
		inputs = append(inputs, fmt.Sprintf(`"h_%d": ["hub", "h_%d", "h_%d"]`, k, k-1, k-1))
	}
	got := parse(t, fmt.Sprintf(`{
  "nodes": {
    "hub": {
      "inputs": {%s},
      "locked": {"lastModified": 1760918400, "type": "github"},
      "original": {"owner": "NixOS", "repo": "nixpkgs", "type": "github"}
    },
    "root": {"inputs": {"hub": "hub", "nixpkgs": ["hub", "h_%d"]}}
  },
  "root": "root",
  "version": 7
}`, strings.Join(inputs, ", "), levels))
	if want := time.Unix(1760918400, 0).UTC(); !got.NixpkgsLastModified.Equal(want) {
		t.Errorf("NixpkgsLastModified = %v, want %v", got.NixpkgsLastModified, want)
	}
}
