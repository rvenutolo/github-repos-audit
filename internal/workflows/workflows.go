// Package workflows reads GitHub Actions workflow files and composite-action
// files into the facts internal/audit records about them. It does no I/O and
// judges nothing: the caller supplies the text, and whether what it says is
// good enough is internal/rules' decision.
//
// The text is decoded into yaml.Node rather than into Go structs, for two
// reasons. A struct decode turns `cancel-in-progress: true` into a bool and
// `timeout-minutes: 030` into 30, and the record is meant to say what the file
// says; a node keeps every scalar exactly as written. And a node knows its
// kind, so a value in a shape GitHub would reject (a sequence where a mapping
// belongs) is reported as such instead of being silently dropped by a lenient
// decoder.
//
// A file that is not valid YAML, or is YAML in a shape GitHub would not run,
// comes back with ParseError set and nothing else. Partial results are never
// returned: a check that read half a file could pass on the half it missed,
// and an unreadable file has to fail every check that depends on it.
package workflows

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
)

// flakeUpdate is the command whose presence in a `run:` marks a job or
// composite as one that refreshes flake.lock. A substring match, because the
// command is usually one line of a longer script and is followed by flags
// such as --commit-lock-file.
const flakeUpdate = "nix flake update"

// ParseWorkflow returns what the workflow file at path, whose content is
// text, says. It never fails: a file it cannot read has ParseError set.
func ParseWorkflow(path, text string) audit.Workflow {
	w, err := parseWorkflow(text)
	if err != nil {
		return audit.Workflow{Path: path, ParseError: err.Error()}
	}
	w.Path = path
	return w
}

// ParseComposite returns what the composite-action file at path, whose
// content is text, says. It never fails: a file it cannot read has ParseError
// set. A JavaScript or Docker action is not an error; it has no steps, so it
// comes back with nothing but Path.
func ParseComposite(path, text string) audit.CompositeAction {
	c, err := parseComposite(text)
	if err != nil {
		return audit.CompositeAction{Path: path, ParseError: err.Error()}
	}
	c.Path = path
	return c
}

func parseWorkflow(text string) (audit.Workflow, error) {
	var w audit.Workflow
	top, err := document(text)
	if err != nil {
		return w, err
	}
	t, err := events(lookup(top, "on"))
	if err != nil {
		return w, err
	}
	w.Events, w.PullRequestTypes, w.PullRequestTargetTypes = t.names, t.pullRequest.types, t.pullRequestTarget.types
	w.PullRequestBranches, w.PullRequestBranchesIgnore = t.pullRequest.branches, t.pullRequest.branchesIgnore
	w.PullRequestTargetBranches, w.PullRequestTargetBranchesIgnore = t.pullRequestTarget.branches, t.pullRequestTarget.branchesIgnore
	w.PushBranches, w.PushBranchesIgnore, w.PushTagFilter = t.push.branches, t.push.branchesIgnore, t.push.tagFilter
	if w.Concurrency, err = concurrency(lookup(top, "concurrency"), "concurrency"); err != nil {
		return w, err
	}
	if w.Permissions, err = permissions(lookup(top, "permissions"), "permissions"); err != nil {
		return w, err
	}
	jobs, err := pairs(lookup(top, "jobs"), "jobs")
	if err != nil {
		return w, err
	}
	for _, p := range jobs {
		j, err := job(p.key, p.value)
		if err != nil {
			return w, err
		}
		w.Jobs = append(w.Jobs, j)
	}
	return w, nil
}

func parseComposite(text string) (audit.CompositeAction, error) {
	var c audit.CompositeAction
	top, err := document(text)
	if err != nil {
		return c, err
	}
	runs := lookup(top, "runs")
	if _, err := pairs(runs, "runs"); err != nil {
		return c, err
	}
	c.Uses, c.FlakeUpdate, err = steps(lookup(runs, "steps"), "runs.steps")
	return c, err
}

func job(id string, n *yaml.Node) (audit.Job, error) {
	j := audit.Job{ID: id}
	where := "jobs." + id
	if _, err := pairs(n, where); err != nil {
		return j, err
	}
	uses, err := scalar(lookup(n, "uses"), where+".uses")
	if err != nil {
		return j, err
	}
	j.Reusable = uses != ""
	if j.TimeoutMinutes, err = scalar(lookup(n, "timeout-minutes"), where+".timeout-minutes"); err != nil {
		return j, err
	}
	if j.Concurrency, err = concurrency(lookup(n, "concurrency"), where+".concurrency"); err != nil {
		return j, err
	}
	if j.Permissions, err = permissions(lookup(n, "permissions"), where+".permissions"); err != nil {
		return j, err
	}
	j.Uses, j.FlakeUpdate, err = steps(lookup(n, "steps"), where+".steps")
	return j, err
}

// steps reads a `steps:` sequence, shared by workflow jobs and composite
// actions, which spell a step the same way.
func steps(n *yaml.Node, where string) ([]audit.StepUse, bool, error) {
	var (
		uses   []audit.StepUse
		update bool
	)
	items, err := sequence(n, where)
	if err != nil {
		return nil, false, err
	}
	for i, s := range items {
		at := fmt.Sprintf("%s[%d]", where, i)
		if _, err := pairs(s, at); err != nil {
			return nil, false, err
		}
		action, err := scalar(lookup(s, "uses"), at+".uses")
		if err != nil {
			return nil, false, err
		}
		with, err := stringMap(lookup(s, "with"), at+".with")
		if err != nil {
			return nil, false, err
		}
		if action != "" {
			// Only an action step's env is read: a run step's is never
			// used, and reading it would let a shape no check needs make
			// the whole file unreadable.
			env, err := stepEnv(lookup(s, "env"), at+".env")
			if err != nil {
				return nil, false, err
			}
			uses = append(uses, audit.StepUse{Action: action, With: with, Env: env})
		}
		run, err := scalar(lookup(s, "run"), at+".run")
		if err != nil {
			return nil, false, err
		}
		update = update || strings.Contains(run, flakeUpdate)
	}
	return uses, update, nil
}

// stepEnv reads a step's `env:`. GitHub also takes the whole of it as one
// expression, such as `${{ fromJSON(vars.ENV) }}`; that is a valid step
// whose variables only the runner can know, so it reads as no env rather
// than as a shape error, which would make the whole file unreadable. Any
// other scalar, one with an expression inside other text included, is still
// one.
func stepEnv(n *yaml.Node, where string) (map[string]string, error) {
	if !absent(n) && n.Kind == yaml.ScalarNode && isExpression(n.Value) {
		return nil, nil
	}
	return stringMap(n, where)
}

// isExpression reports whether v is one whole `${{ }}` expression.
func isExpression(v string) bool {
	v = strings.TrimSpace(v)
	return strings.HasPrefix(v, "${{") && strings.HasSuffix(v, "}}")
}

// triggers is what `on:` says: the event names, the `types:` and branch
// filters of the two pull request triggers, and the ref filters of `push`.
type triggers struct {
	names                          []string
	pullRequest, pullRequestTarget pullRequestTrigger
	push                           pushFilter
}

// branchFilter is which branches a trigger admits, as written.
type branchFilter struct {
	branches, branchesIgnore []string
}

// pullRequestTrigger is what one pull request trigger's settings say.
type pullRequestTrigger struct {
	types []string
	branchFilter
}

// pushFilter is which refs `on.push` admits, as written.
type pushFilter struct {
	branchFilter
	tagFilter bool
}

// events reduces the three forms of `on:` (one name, a list of names, a map
// keyed by name) to the sorted, distinct names. A blank or null name is no
// event, so it is dropped; with nothing left the result is nil, the same as
// an absent `on:`. Only the map form can give a trigger settings, so only it
// can list `types:` and branch filters for `pull_request` and
// `pull_request_target`, and ref filters for `push`.
func events(n *yaml.Node) (triggers, error) {
	var t triggers
	switch {
	case absent(n):
		return t, nil
	case n.Kind == yaml.ScalarNode:
		t.names = []string{n.Value}
	case n.Kind == yaml.SequenceNode:
		for i, item := range n.Content {
			name, err := scalar(deref(item), fmt.Sprintf("on[%d]", i))
			if err != nil {
				return t, err
			}
			// scalar reads a null item as "", which distinct drops.
			t.names = append(t.names, name)
		}
	default:
		// lookup has followed any alias, so what is left is a mapping.
		ps, err := pairs(n, "on")
		if err != nil {
			return t, err
		}
		for _, p := range ps {
			t.names = append(t.names, p.key)
			switch p.key {
			case "pull_request":
				t.pullRequest, err = pullRequestSettings(p.value, "on.pull_request")
			case "pull_request_target":
				t.pullRequestTarget, err = pullRequestSettings(p.value, "on.pull_request_target")
			case "push":
				t.push, err = pushFilters(p.value, "on.push")
			}
			if err != nil {
				return t, err
			}
		}
	}
	t.names = distinct(t.names)
	return t, nil
}

// pullRequestSettings reads the `types:` and branch filters of a pull
// request trigger. Its `paths` filters are not read: they narrow when the
// workflow runs for a pull request, not which base branches it can run for.
func pullRequestSettings(n *yaml.Node, where string) (pullRequestTrigger, error) {
	var t pullRequestTrigger
	ps, err := settings(n, where)
	if err != nil {
		return t, err
	}
	if t.types, err = eventTypes(ps, where); err != nil {
		return t, err
	}
	t.branchFilter, err = branchFilters(ps, n.Line, where)
	return t, err
}

// settings returns the keys of one trigger's settings. Settings that are not
// a mapping (null for a bare trigger) have none. pairs, not lookup, so a key
// written twice is rejected rather than read as whichever comes first.
func settings(n *yaml.Node, where string) ([]pair, error) {
	if n.Kind != yaml.MappingNode {
		return nil, nil
	}
	return pairs(n, where)
}

// eventTypes reads the `types:` among one trigger's settings: sorted, distinct,
// and nil when none are listed, `types: []` included, since omitzero would
// write an empty non-nil slice as `[]` and "lists no types" has one spelling.
// A scalar is a one-item list, as GitHub reads it; failing the whole file as
// unreadable for a form GitHub runs would fail every check on it.
//
// Only `types:` is read here, so a trigger's other shapes stay as unjudged as
// they are for every other trigger.
func eventTypes(ps []pair, where string) ([]string, error) {
	where += ".types"
	var types []string
	for _, p := range ps {
		if p.key != "types" {
			continue
		}
		if !absent(p.value) && p.value.Kind == yaml.ScalarNode {
			return distinct([]string{p.value.Value}), nil
		}
		items, err := sequence(p.value, where)
		if err != nil {
			return nil, err
		}
		for i, item := range items {
			v, err := scalar(item, fmt.Sprintf("%s[%d]", where, i))
			if err != nil {
				return nil, err
			}
			types = append(types, v)
		}
	}
	return distinct(types), nil
}

// pushFilters reads the ref filters of `on.push`: its branch filters, as
// branchFilters reads them, and of the tag filters only their presence (a
// non-null `tags:` or `tags-ignore:`, empty included), since what matters
// downstream is whether branch pushes can run the workflow at all.
//
// `paths` and the rest are not read, so they stay as unjudged as they are for
// every other trigger.
func pushFilters(n *yaml.Node, where string) (pushFilter, error) {
	var f pushFilter
	ps, err := settings(n, where)
	if err != nil {
		return f, err
	}
	if f.branchFilter, err = branchFilters(ps, n.Line, where); err != nil {
		return f, err
	}
	for _, p := range ps {
		if p.key != "tags" && p.key != "tags-ignore" {
			continue
		}
		if _, err := patterns(p.value, where+"."+p.key); err != nil {
			return f, err
		}
		// Presence, not a non-empty list: an empty tag list is not a
		// documented form, so it is read as the tag-only trigger it
		// resembles rather than as one that admits every branch.
		f.tagFilter = f.tagFilter || !absent(p.value)
	}
	return f, nil
}

// branchFilters reads the `branches:` and `branches-ignore:` among one
// trigger's settings, which start on line. Patterns are kept exactly as written, unsorted and with
// repeats, because `!` patterns are order-sensitive; a scalar is a one-item
// list, and an empty list is nil, as for eventTypes. Blank patterns (a null
// item) are dropped.
func branchFilters(ps []pair, line int, where string) (branchFilter, error) {
	var (
		f                      branchFilter
		err                    error
		hasBranches, hasIgnore bool
	)
	for _, p := range ps {
		switch p.key {
		case "branches":
			hasBranches = true
			f.branches, err = patterns(p.value, where+".branches")
		case "branches-ignore":
			hasIgnore = true
			f.branchesIgnore, err = patterns(p.value, where+".branches-ignore")
		}
		if err != nil {
			return f, err
		}
	}
	// GitHub refuses the pair, and reading both would hide which of the two
	// the author meant to apply.
	if hasBranches && hasIgnore {
		return f, fmt.Errorf("line %d: %s lists both branches and branches-ignore", line, where)
	}
	return f, nil
}

// patterns reads a filter's pattern list in written order: a scalar is a
// one-item list, blanks are dropped and nothing is sorted or deduplicated.
// Nil when no pattern is left, so omitzero leaves the key out.
func patterns(n *yaml.Node, where string) ([]string, error) {
	if !absent(n) && n.Kind == yaml.ScalarNode {
		if n.Value == "" {
			return nil, nil
		}
		return []string{n.Value}, nil
	}
	items, err := sequence(n, where)
	if err != nil {
		return nil, err
	}
	var out []string
	for i, item := range items {
		v, err := scalar(item, fmt.Sprintf("%s[%d]", where, i))
		if err != nil {
			return nil, err
		}
		if v != "" {
			out = append(out, v)
		}
	}
	return out, nil
}

// distinct sorts names and drops repeats and blanks, returning nil when
// nothing is left, which omitzero then leaves out of audit.json.
func distinct(names []string) []string {
	names = slices.DeleteFunc(names, func(name string) bool { return name == "" })
	if len(names) == 0 {
		return nil
	}
	slices.Sort(names)
	return slices.Compact(names)
}

func concurrency(n *yaml.Node, where string) (audit.Concurrency, error) {
	if absent(n) {
		return audit.Concurrency{}, nil
	}
	if n.Kind == yaml.ScalarNode {
		return audit.Concurrency{Set: true}, nil
	}
	if _, err := pairs(n, where); err != nil {
		return audit.Concurrency{}, err
	}
	cancel, err := scalar(lookup(n, "cancel-in-progress"), where+".cancel-in-progress")
	return audit.Concurrency{Set: true, CancelInProgress: cancel}, err
}

func permissions(n *yaml.Node, where string) (audit.Permissions, error) {
	if absent(n) {
		return audit.Permissions{}, nil
	}
	if n.Kind == yaml.ScalarNode {
		return audit.Permissions{Set: true, All: n.Value}, nil
	}
	scopes, err := stringMap(n, where)
	if err != nil {
		return audit.Permissions{}, err
	}
	// stringMap leaves an empty mapping nil, but `permissions: {}` is a
	// setting of its own (it grants nothing) and has to stay distinguishable
	// from the key being absent.
	if scopes == nil {
		scopes = map[string]string{}
	}
	return audit.Permissions{Set: true, Scopes: scopes}, nil
}

// document decodes text and returns its top-level mapping. Only the first
// YAML document is read; GitHub reads no further either.
func document(text string) (*yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		return nil, err
	}
	// An empty or comment-only file decodes to a node with no content.
	if len(doc.Content) == 0 {
		return nil, errors.New("the document is empty, not a mapping")
	}
	top := deref(doc.Content[0])
	if top.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("line %d: the document is a %s, not a mapping", top.Line, kind(top))
	}
	if _, err := pairs(top, ""); err != nil {
		return nil, err
	}
	return top, nil
}

type pair struct {
	key   string
	value *yaml.Node
}

// pairs returns a mapping's entries in file order. An absent node has none.
// A key written twice is an error: YAML leaves it undefined, GitHub rejects
// the file, and lookup would otherwise read only the first of the two. An
// empty where names the top-level mapping, whose keys need no prefix.
func pairs(n *yaml.Node, where string) ([]pair, error) {
	if absent(n) {
		return nil, nil
	}
	if n.Kind != yaml.MappingNode {
		return nil, shapeError(n, where, "a mapping")
	}
	ps := make([]pair, 0, len(n.Content)/2)
	seen := make(map[string]bool, len(n.Content)/2)
	for i := 0; i+1 < len(n.Content); i += 2 {
		k := deref(n.Content[i])
		if seen[k.Value] {
			path := k.Value
			if where != "" {
				path = where + "." + k.Value
			}
			return nil, fmt.Errorf("line %d: %s is defined twice", k.Line, path)
		}
		seen[k.Value] = true
		ps = append(ps, pair{key: k.Value, value: deref(n.Content[i+1])})
	}
	return ps, nil
}

// sequence returns a sequence's items. An absent node has none.
func sequence(n *yaml.Node, where string) ([]*yaml.Node, error) {
	if absent(n) {
		return nil, nil
	}
	if n.Kind != yaml.SequenceNode {
		return nil, shapeError(n, where, "a sequence")
	}
	items := make([]*yaml.Node, len(n.Content))
	for i, item := range n.Content {
		items[i] = deref(item)
	}
	return items, nil
}

// scalar returns a scalar exactly as written. An absent node is "".
func scalar(n *yaml.Node, where string) (string, error) {
	if absent(n) {
		return "", nil
	}
	if n.Kind != yaml.ScalarNode {
		return "", shapeError(n, where, "a scalar")
	}
	return n.Value, nil
}

// stringMap reads a mapping of scalars, such as `with:` or the map form of
// `permissions:`. It is nil when the mapping is absent or empty.
func stringMap(n *yaml.Node, where string) (map[string]string, error) {
	ps, err := pairs(n, where)
	if err != nil || len(ps) == 0 {
		return nil, err
	}
	m := make(map[string]string, len(ps))
	for _, p := range ps {
		v, err := scalar(p.value, where+"."+p.key)
		if err != nil {
			return nil, err
		}
		m[p.key] = v
	}
	return m, nil
}

// lookup returns the value of key in mapping n, or nil when n is not a
// mapping or has no such key. A missing key and a non-mapping parent both
// read as absent here; the caller has already rejected the parent's shape.
func lookup(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if deref(n.Content[i]).Value == key {
			return deref(n.Content[i+1])
		}
	}
	return nil
}

// deref follows an alias to its anchor, so `*steps` reads as the sequence it
// names. GitHub accepts anchors and aliases in workflow files.
func deref(n *yaml.Node) *yaml.Node {
	if n != nil && n.Kind == yaml.AliasNode {
		return n.Alias
	}
	return n
}

// absent is a missing key or an explicit null, which YAML also spells as an
// empty value (`workflow_dispatch:`): both mean the setting is not there.
func absent(n *yaml.Node) bool {
	return n == nil || (n.Kind == yaml.ScalarNode && n.Tag == "!!null")
}

func shapeError(n *yaml.Node, where, want string) error {
	return fmt.Errorf("line %d: %s is a %s, not %s", n.Line, where, kind(n), want)
}

// kind names a node's kind for an error message. Only the three value kinds
// reach it: deref has followed every alias, and a document node never sits
// below the top.
func kind(n *yaml.Node) string {
	if n.Kind == yaml.MappingNode {
		return "mapping"
	}
	if n.Kind == yaml.SequenceNode {
		return "sequence"
	}
	return "scalar"
}
