package renovate

import "strings"

// tri is what one layer says about a boolean key: nothing, or true/false.
type tri struct {
	set   bool
	value bool
}

// overlay returns o when o says something, else t.
func (t tri) overlay(o tri) tri {
	if o.set {
		return o
	}
	return t
}

// automergeLayer is what one layer says about the keys that decide whether
// major updates merge on their own. Each key merges on its own, like the
// others in layered; the package rules, which concatenate, are kept apart in
// layered.rules.
type automergeLayer struct {
	automerge      tri // top-level automerge
	enabled        tri // top-level enabled
	approval       tri // top-level dependencyDashboardApproval
	majorAutomerge tri // major.automerge
	majorEnabled   tri // major.enabled
	majorApproval  tri // major.dependencyDashboardApproval
	// bad is a value of the wrong type, or a major that is not an object.
	// It is sticky across layers and reads as a hold: a misread can then only
	// fail the check, never pass it.
	bad bool
}

func (a *automergeLayer) over(o automergeLayer) {
	a.automerge = a.automerge.overlay(o.automerge)
	a.enabled = a.enabled.overlay(o.enabled)
	a.approval = a.approval.overlay(o.approval)
	a.majorAutomerge = a.majorAutomerge.overlay(o.majorAutomerge)
	a.majorEnabled = a.majorEnabled.overlay(o.majorEnabled)
	a.majorApproval = a.majorApproval.overlay(o.majorApproval)
	a.bad = a.bad || o.bad
}

// pkgRule is one packageRules entry, reduced to what decides majors.
type pkgRule struct {
	// appliesToMajor is update types that are absent or list "major".
	appliesToMajor bool
	// narrowed is any other matcher: the rule reaches only some majors.
	narrowed  bool
	automerge tri
	enabled   tri
	approval  tri
	// malformed is a rule or value this tool cannot read as Renovate would.
	malformed bool
}

// ownTri reads a boolean key; bad reports a present value that is not one.
func ownTri(cfg map[string]any, key string) (t tri, bad bool) {
	v, has := cfg[key]
	if !has {
		return tri{}, false
	}
	b, ok := v.(bool)
	if !ok {
		return tri{}, true
	}
	return tri{set: true, value: b}, false
}

// ownAutomerge reads the top-level and major.* keys.
func ownAutomerge(cfg map[string]any) automergeLayer {
	var a automergeLayer
	var b1, b2, b6 bool
	a.automerge, b1 = ownTri(cfg, "automerge")
	a.approval, b2 = ownTri(cfg, "dependencyDashboardApproval")
	a.enabled, b6 = ownTri(cfg, "enabled")
	a.bad = b1 || b2 || b6
	m, has := cfg["major"]
	if !has {
		return a
	}
	obj, ok := m.(map[string]any)
	if !ok {
		a.bad = true
		return a
	}
	var b3, b4, b5 bool
	a.majorAutomerge, b3 = ownTri(obj, "automerge")
	a.majorEnabled, b4 = ownTri(obj, "enabled")
	a.majorApproval, b5 = ownTri(obj, "dependencyDashboardApproval")
	a.bad = a.bad || b3 || b4 || b5
	return a
}

// ownRules reads packageRules. Anything that is not an array of objects reads
// as one malformed rule, which holds the result.
func ownRules(cfg map[string]any) []pkgRule {
	v, has := cfg["packageRules"]
	if !has {
		return nil
	}
	list, ok := v.([]any)
	if !ok {
		return []pkgRule{{malformed: true}}
	}
	rules := make([]pkgRule, 0, len(list))
	for _, e := range list {
		obj, ok := e.(map[string]any)
		if !ok {
			rules = append(rules, pkgRule{malformed: true})
			continue
		}
		rules = append(rules, ownRule(obj))
	}
	return rules
}

// isLegacySelector reports the pre-match* names of rule selectors, which
// Renovate migrates to their match* forms; each narrows a rule exactly as its
// modern name does. updateTypes is not one: ownRule reads it with
// matchUpdateTypes.
// A switch rather than a package-level slice, so nothing can mutate it.
func isLegacySelector(key string) bool {
	switch key {
	case "packageNames", "packagePatterns", "packagePrefixes", "managers",
		"datasources", "depTypeList", "paths", "languages", "baseBranchList",
		"sourceUrlPrefixes", "sourceUrls":
		return true
	}
	return false
}

// ownRule reduces one packageRules entry. A rule's own extends is not
// expanded, nor is a major block inside it; extends is read as a narrowing
// matcher because the preset it names usually carries package selectors (as
// :automergeLinters does), and the safe misreading is the one that stops a
// rule from helping a pass.
func ownRule(obj map[string]any) pkgRule {
	var r pkgRule
	selector := false
	for key := range obj {
		switch {
		case key == "matchUpdateTypes" || key == "updateTypes":
			selector = true
		case key == "extends", strings.HasPrefix(key, "match"),
			strings.HasPrefix(key, "exclude"), isLegacySelector(key):
			selector = true
			r.narrowed = true
		}
	}
	// Renovate rejects a rule with no selector rather than applying it to
	// everything, so it is not read as matching all majors.
	r.malformed = !selector
	// Renovate migrates updateTypes to matchUpdateTypes key by key, in the
	// object's own order, so with both present the one written later
	// replaces the other. The decoded object keeps no key order, so which
	// list Renovate keeps cannot be told: their union is read and the rule
	// is narrowed, so a hold in either list fails and automerge-on via the
	// rule is ignored.
	// https://github.com/renovatebot/renovate/blob/main/lib/config/migrations/custom/package-rules-migration.ts
	var types []any
	present := 0
	for _, key := range []string{"matchUpdateTypes", "updateTypes"} {
		v, has := obj[key]
		if !has {
			continue
		}
		present++
		list, ok := updateTypeList(v)
		if !ok || len(list) == 0 {
			r.malformed = true
		}
		types = append(types, list...)
	}
	if present == 2 {
		r.narrowed = true
	}
	for _, e := range types {
		s, ok := e.(string)
		switch {
		case !ok:
			r.malformed = true
		case s == "major":
			r.appliesToMajor = true
		case strings.ContainsAny(s, "*!/?{"):
			// Renovate matches update types as globs, regexes and
			// negations. Which majors such an entry reaches is not
			// decided here: it reaches majors and is narrowed, so a
			// hold in the rule fails and automerge-on via it is ignored.
			r.appliesToMajor, r.narrowed = true, true
		}
	}
	if present == 0 {
		r.appliesToMajor = true
	}
	var b1, b2, b3 bool
	r.automerge, b1 = ownTri(obj, "automerge")
	r.enabled, b2 = ownTri(obj, "enabled")
	r.approval, b3 = ownTri(obj, "dependencyDashboardApproval")
	r.malformed = r.malformed || b1 || b2 || b3
	return r
}

// updateTypeList reads a matchUpdateTypes or legacy updateTypes value. The
// option allows a string, which Renovate's config massaging turns into a
// one-element list before any rule matches, so a bare "major" reaches majors
// exactly as ["major"] does; ok is false for any other non-list.
// https://github.com/renovatebot/renovate/blob/main/lib/config/massage.ts
func updateTypeList(v any) (list []any, ok bool) {
	switch v := v.(type) {
	case []any:
		return v, true
	case string:
		return []any{v}, true
	}
	return nil, false
}

// majorsAutoMerge follows the order Renovate applies them in: top-level
// values, then major.*, then every packageRule that reaches majors, in order,
// so a rule always beats major.*. A rule narrowed to some majors cannot make
// them all merge, but one that holds some of them (automerge false, disabled,
// or needing approval) fails the result wherever it sits.
// https://github.com/renovatebot/renovate/blob/main/lib/workers/repository/updates/flatten.ts
func majorsAutoMerge(a automergeLayer, rules []pkgRule) bool {
	if a.bad {
		return false
	}
	automerge := a.automerge.value
	approval := a.approval.value
	enabled := !a.enabled.set || a.enabled.value
	if a.majorAutomerge.set {
		automerge = a.majorAutomerge.value
	}
	if a.majorEnabled.set {
		enabled = a.majorEnabled.value
	}
	if a.majorApproval.set {
		approval = a.majorApproval.value
	}
	for _, r := range rules {
		if r.malformed {
			return false
		}
		if !r.appliesToMajor {
			continue
		}
		if r.narrowed {
			if (r.automerge.set && !r.automerge.value) ||
				(r.enabled.set && !r.enabled.value) ||
				(r.approval.set && r.approval.value) {
				return false
			}
			continue
		}
		if r.automerge.set {
			automerge = r.automerge.value
		}
		if r.enabled.set {
			enabled = r.enabled.value
		}
		if r.approval.set {
			approval = r.approval.value
		}
	}
	return automerge && enabled && !approval
}
