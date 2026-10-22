// Package nixflake reads a repository's flake.lock into the facts
// internal/audit records about it. It does no I/O and judges nothing: the
// caller supplies the text, and whether a lock is fresh enough is
// internal/rules' decision.
//
// flake.lock is JSON that Nix itself writes, so ParseLock reads it properly,
// and reports a lock it cannot read in full as a problem instead of a partial
// result: a check that read half a lock could pass on the half it missed.
package nixflake

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// LockFacts is what flake.lock says, as far as the checks need it.
type LockFacts struct {
	// NixpkgsLastModified is the `locked.lastModified` of the node the
	// root's `nixpkgs` input resolves to, in UTC. Zero when the root has no
	// input named nixpkgs.
	NixpkgsLastModified time.Time
	// AllRevPinned reports that every node other than the root has an
	// `original` carrying `rev`, so flake.nix itself names each commit and
	// a tool that updates flake.nix updates the lock with it. False for a
	// lock with no inputs: nothing being pinned is not everything being
	// pinned.
	AllRevPinned bool
}

// lockFile is the part of the flake.lock format ParseLock reads.
type lockFile struct {
	Nodes map[string]node `json:"nodes"`
	Root  string          `json:"root"`
}

type node struct {
	// Inputs maps an input name to either a node name (a JSON string) or,
	// for an input that follows another, a path of input names from the
	// root (a JSON list), so it is decoded per entry.
	Inputs   map[string]json.RawMessage `json:"inputs"`
	Locked   *locked                    `json:"locked"`
	Original *original                  `json:"original"`
}

type locked struct {
	LastModified *int64 `json:"lastModified"` //nolint:tagliatelle // Nix writes flake.lock, and spells the key this way
}

type original struct {
	Rev string `json:"rev"`
}

// ParseLock returns what the flake.lock whose content is text says. A lock
// it cannot read in full comes back as zero facts and a non-empty problem
// saying why.
func ParseLock(text string) (lock LockFacts, problem string) {
	lock, err := parseLock(text)
	if err != nil {
		return LockFacts{}, err.Error()
	}
	return lock, ""
}

func parseLock(text string) (LockFacts, error) {
	var (
		facts LockFacts
		lf    lockFile
	)
	if err := json.Unmarshal([]byte(text), &lf); err != nil {
		return facts, fmt.Errorf("flake.lock is not valid JSON: %w", err)
	}
	if lf.Nodes == nil {
		return facts, errors.New("flake.lock has no nodes")
	}
	if lf.Root == "" {
		lf.Root = "root"
	}
	root, ok := lf.Nodes[lf.Root]
	if !ok {
		return facts, fmt.Errorf("flake.lock has no root node %q", lf.Root)
	}

	if _, ok := root.Inputs["nixpkgs"]; ok {
		name, err := lf.resolve(lf.Root, "nixpkgs", map[string]bool{}, map[string]string{})
		if err != nil {
			return facts, err
		}
		n := lf.Nodes[name]
		if n.Locked == nil || n.Locked.LastModified == nil {
			return facts, fmt.Errorf("nixpkgs node %q has no locked.lastModified", name)
		}
		facts.NixpkgsLastModified = time.Unix(*n.Locked.LastModified, 0).UTC()
	}

	pinned, inputs := true, 0
	for name, n := range lf.Nodes {
		if name == lf.Root {
			continue
		}
		inputs++
		if n.Original == nil || n.Original.Rev == "" {
			pinned = false
		}
	}
	facts.AllRevPinned = pinned && inputs > 0
	return facts, nil
}

// resolve returns the name of the node that input of node from refers to.
// An input that follows another is a path of input names walked from the
// root, and a step of that path may itself follow, so resolution recurses;
// seen holds the (node, input) pairs on the current chain, so a lock whose
// follows loop back on themselves is an error rather than a stack overflow.
// memo holds the answers already found under the same key: paths that fan
// into shared chains would otherwise be walked once per route, which grows
// exponentially with depth. Only successes are kept, since a failure ends
// the whole parse.
func (lf lockFile) resolve(from, input string, seen map[string]bool, memo map[string]string) (string, error) {
	key := from + "\x00" + input
	if name, ok := memo[key]; ok {
		return name, nil
	}
	if seen[key] {
		return "", fmt.Errorf("input %q of node %q follows itself", input, from)
	}
	seen[key] = true
	defer delete(seen, key)

	raw, ok := lf.Nodes[from].Inputs[input]
	if !ok {
		return "", fmt.Errorf("node %q has no input %q", from, input)
	}
	var name string
	if err := json.Unmarshal(raw, &name); err == nil {
		if _, ok := lf.Nodes[name]; !ok {
			return "", fmt.Errorf("input %q of node %q names missing node %q", input, from, name)
		}
		memo[key] = name
		return name, nil
	}
	var path []string
	if err := json.Unmarshal(raw, &path); err != nil {
		return "", fmt.Errorf("input %q of node %q is neither a node name nor a follows path", input, from)
	}
	cur := lf.Root
	for _, step := range path {
		next, err := lf.resolve(cur, step, seen, memo)
		if err != nil {
			return "", err
		}
		cur = next
	}
	memo[key] = cur
	return cur, nil
}
