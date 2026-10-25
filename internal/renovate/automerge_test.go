package renovate_test

import "testing"

func TestResolve_automergeMajor(t *testing.T) {
	t.Parallel()

	store := files{
		"preset-store/default.json@": `{"packageRules":[{"matchUpdateTypes":["major"],"automerge":true}]}`,
		"preset-store/bad.json@":     `{"automerge": "yes"}`,
	}
	tests := []struct {
		name string
		root string
		want bool
	}{
		{"empty", `{}`, false},
		{"top-level automerge", `{"automerge": true}`, true},
		{"major turns it off", `{"automerge": true, "major": {"automerge": false}}`, false},
		{"major turns it on", `{"major": {"automerge": true}}`, true},
		{
			"rule for major and minor",
			`{"packageRules": [{"matchUpdateTypes": ["major","minor"], "automerge": true}]}`, true,
		},
		{
			"description is not a matcher",
			`{"packageRules": [{"description": "x", "matchUpdateTypes": ["major","minor"], "automerge": true}]}`, true,
		},
		// Renovate rejects a rule with no selector, so it is not read as matching
		// everything.
		{"rule without selector", `{"packageRules": [{"automerge": true}]}`, false},
		{"legacy updateTypes without major", `{"packageRules": [{"updateTypes": ["minor","patch"], "automerge": true}]}`, false},
		{"legacy updateTypes major", `{"packageRules": [{"updateTypes": ["major"], "automerge": true}]}`, true},
		{"legacy packageNames narrow", `{"packageRules": [{"packageNames": ["x"], "automerge": true}]}`, false},
		{
			"legacy narrowed hold",
			`{"automerge": true, "packageRules": [{"updateTypes": ["major"], "packageNames": ["x"], "automerge": false}]}`, false,
		},
		{
			"glob update type reaches majors and holds",
			`{"automerge": true, "packageRules": [{"matchUpdateTypes": ["*"], "enabled": false}]}`, false,
		},
		{
			"negated update type with narrowing hold",
			`{"automerge": true, "packageRules": [{"matchUpdateTypes": ["!patch"], "matchPackageNames": ["x"], "automerge": false}]}`,
			false,
		},
		{
			"glob update type is narrowed: automerge on is ignored",
			`{"packageRules": [{"matchUpdateTypes": ["!patch"], "automerge": true}]}`, false,
		},
		{"non-string update type", `{"automerge": true, "packageRules": [{"matchUpdateTypes": [1], "automerge": true}]}`, false},
		{"top-level enabled false", `{"automerge": true, "enabled": false}`, false},
		{"top-level enabled wrong type", `{"automerge": true, "enabled": "no"}`, false},
		{"major.enabled overrides top-level", `{"automerge": true, "enabled": false, "major": {"enabled": true}}`, true},
		{"disableRenovate", `{"extends": [":automergeAll", ":disableRenovate"]}`, false},
		{"enableRenovate after disableRenovate", `{"extends": [":automergeAll", ":disableRenovate", ":enableRenovate"]}`, true},
		// A narrowed hold fails wherever it sits. Renovate would let the later
		// broad rule override it; failing is the safe misreading.
		{
			"narrowed hold before a broad rule",
			`{"automerge": true, "packageRules": [{"matchPackageNames": ["x"], "automerge": false}, {"matchUpdateTypes": ["major"], "automerge": true}]}`,
			false,
		},
		{
			"two broad rules: last wins, off",
			`{"packageRules": [{"matchUpdateTypes": ["major"], "automerge": true}, {"matchUpdateTypes": ["major"], "automerge": false}]}`,
			false,
		},
		{
			"two broad rules: last wins, on",
			`{"packageRules": [{"matchUpdateTypes": ["major"], "automerge": false}, {"matchUpdateTypes": ["major"], "automerge": true}]}`,
			true,
		},
		{
			"wrong-typed preset value stays held",
			`{"extends": ["local>gh-owner/preset-store:bad"], "automerge": true}`, false,
		},
		{
			"narrowed rule does not help",
			`{"packageRules": [{"matchUpdateTypes": ["major"], "matchPackageNames": ["x"], "automerge": true}]}`, false,
		},
		{
			"rule beats major",
			`{"major": {"automerge": false}, "packageRules": [{"matchUpdateTypes": ["major"], "automerge": true}]}`, true,
		},
		{
			"narrowed hold",
			`{"automerge": true, "packageRules": [{"matchUpdateTypes": ["major"], "matchDatasources": ["docker"], "automerge": false}]}`,
			false,
		},
		{
			"narrowed exclude hold",
			`{"automerge": true, "packageRules": [{"excludePackageNames": ["x"], "automerge": false}]}`, false,
		},
		{
			"narrowed rule via extends hold",
			`{"automerge": true, "packageRules": [{"extends": ["packages:linters"], "automerge": false}]}`, false,
		},
		{
			"narrowed rule for minor only is ignored",
			`{"automerge": true, "packageRules": [{"matchUpdateTypes": ["minor"], "matchPackageNames": ["x"], "automerge": false}]}`,
			true,
		},
		{"major disabled", `{"automerge": true, "major": {"enabled": false}}`, false},
		{
			"rule disables major",
			`{"automerge": true, "packageRules": [{"matchUpdateTypes": ["major"], "enabled": false}]}`, false,
		},
		{"major approval", `{"automerge": true, "major": {"dependencyDashboardApproval": true}}`, false},
		{"top-level approval", `{"automerge": true, "dependencyDashboardApproval": true}`, false},
		{
			"rule approval",
			`{"automerge": true, "packageRules": [{"matchUpdateTypes": ["major"], "dependencyDashboardApproval": true}]}`, false,
		},
		{"approveMajorUpdates", `{"extends": [":approveMajorUpdates"], "automerge": true}`, false},
		{"automergeAll", `{"extends": [":automergeAll"]}`, true},
		{"automergeMajor", `{"extends": [":automergeMajor"]}`, true},
		{"automergeMajor long name", `{"extends": ["default:automergeMajor"]}`, true},
		{"automergeDisabled", `{"extends": [":automergeAll", ":automergeDisabled"]}`, false},
		{"automergeDisabled then own", `{"extends": [":automergeDisabled"], "automerge": true}`, true},
		{"disableMajorUpdates", `{"extends": [":automergeAll", ":disableMajorUpdates"]}`, false},
		{"dependencyDashboardApproval preset", `{"extends": [":automergeAll", ":dependencyDashboardApproval"]}`, false},
		{"automergeMinor does not cover major", `{"extends": [":automergeMinor"]}`, false},
		{"wrong type automerge", `{"automerge": "yes"}`, false},
		{"wrong type major", `{"automerge": true, "major": "x"}`, false},
		{"wrong type major.automerge", `{"major": {"automerge": "true"}}`, false},
		{"wrong type enabled", `{"automerge": true, "major": {"enabled": "false"}}`, false},
		{"wrong type matchUpdateTypes", `{"automerge": true, "packageRules": [{"matchUpdateTypes": "major", "automerge": true}]}`, false},
		{"malformed packageRules", `{"packageRules": "x", "automerge": true}`, false},
		{"malformed rule", `{"packageRules": ["x"], "automerge": true}`, false},
		{"wrong type rule automerge", `{"automerge": true, "packageRules": [{"automerge": "no"}]}`, false},
		{
			"preset rule then own narrowed hold",
			`{"extends":["local>gh-owner/preset-store"],"packageRules":[{"matchUpdateTypes":["major"],"matchPackageNames":["y"],"automerge":false}]}`,
			false,
		},
		{"preset rule alone", `{"extends":["local>gh-owner/preset-store"]}`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := resolve(t, tt.root, store).AutomergeMajor; got != tt.want {
				t.Errorf("Resolve(%s).AutomergeMajor = %v, want %v", tt.root, got, tt.want)
			}
		})
	}
}
