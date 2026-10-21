package audit

// Workflow is what one GitHub Actions workflow file says, as far as the
// checks need it. Values are kept as written rather than interpreted: whether
// `cancel-in-progress: ${{ … }}` is good enough, or whether 30 minutes is a
// sensible timeout, is internal/rules' decision, and an expression cannot be
// evaluated without the event that triggers it anyway.
type Workflow struct {
	// Path is the file's path in the repository, such as
	// ".github/workflows/ci.yml".
	Path string `json:"path"`
	// ParseError is why the file could not be read as a workflow: invalid
	// YAML, or YAML in a shape GitHub would not accept. When it is set
	// nothing else but Path is, so a check cannot pass on the part it did
	// manage to read; every check reading the file fails as unreadable.
	ParseError string `json:"parse_error,omitzero"`
	// Events is the trigger names under `on:`, sorted and without repeats.
	// The string, list and map forms all reduce to the same names.
	Events []string `json:"events,omitzero"`
	// Concurrency is the workflow-level `concurrency:`.
	Concurrency Concurrency `json:"concurrency,omitzero"`
	// Permissions is the workflow-level `permissions:`.
	Permissions Permissions `json:"permissions,omitzero"`
	// Jobs is every job, in file order: the order a reader of the file sees
	// them in, so a report naming the first offending job names the one
	// they will find first.
	Jobs []Job `json:"jobs,omitzero"`
}

// Concurrency is a `concurrency:` setting, at workflow or job level.
type Concurrency struct {
	// Set reports that `concurrency:` is present in either form. An explicit
	// null is not presence.
	Set bool `json:"set"`
	// CancelInProgress is `cancel-in-progress:` exactly as written: "true",
	// "false" or an expression such as "${{ github.event_name ==
	// 'pull_request' }}". Empty when the key is absent, and always empty for
	// the string form, which names a group and cannot cancel anything.
	CancelInProgress string `json:"cancel_in_progress,omitzero"`
}

// Permissions is a `permissions:` setting, at workflow or job level.
type Permissions struct {
	// Set reports that `permissions:` is present in either form. An explicit
	// null is not presence.
	Set bool `json:"set"`
	// All is the string form, such as "read-all" or "write-all".
	All string `json:"all,omitzero"`
	// Scopes is the map form, scope name to access as written, such as
	// {"actions": "write"}. `permissions: {}` grants nothing, which is not
	// the same as leaving permissions unset, so it is an empty map with Set.
	Scopes map[string]string `json:"scopes,omitzero"`
}

// Job is one entry under `jobs:`.
type Job struct {
	// ID is the job's key under `jobs:`.
	ID string `json:"id"`
	// Reusable reports a job-level `uses:`: the job calls a reusable
	// workflow, so it has no steps of its own and its settings live in the
	// called file.
	Reusable bool `json:"reusable,omitzero"`
	// TimeoutMinutes is `timeout-minutes:` as written, an expression
	// included. Empty when absent, which leaves GitHub's six-hour default.
	TimeoutMinutes string `json:"timeout_minutes,omitzero"`
	// Concurrency is the job-level `concurrency:`.
	Concurrency Concurrency `json:"concurrency,omitzero"`
	// Permissions is the job-level `permissions:`.
	Permissions Permissions `json:"permissions,omitzero"`
	// Uses is every step's `uses:`, in step order.
	Uses []StepUse `json:"uses,omitzero"`
	// FlakeUpdate reports that some step's `run:` contains the text
	// "nix flake update".
	FlakeUpdate bool `json:"flake_update,omitzero"`
}

// StepUse is one step that runs an action.
type StepUse struct {
	// Action is `uses:` as written, such as
	// "nix-community/cache-nix-action@<sha>" or "./.github/actions/setup".
	// Resolving a local path to its composite action is the caller's job.
	Action string `json:"action"`
	// With is the step's `with:` inputs. Every value is a scalar, kept as
	// written: `purge: true` is "true" and `gc-max-store-size-linux: 0` is
	// "0", which is also what the action itself receives, since GitHub hands
	// every input over as a string.
	With map[string]string `json:"with,omitzero"`
}

// CompositeAction is what one composite action's `action.yml` says. A
// workflow step that uses a local action runs these steps too, so a check
// looking for, say, a cache action has to look inside it.
type CompositeAction struct {
	// Path is the file's path in the repository, such as
	// ".github/actions/setup/action.yml".
	Path string `json:"path"`
	// ParseError is as Workflow.ParseError: when it is set nothing else but
	// Path is.
	ParseError string `json:"parse_error,omitzero"`
	// Uses is every step's `uses:` under `runs.steps`, in step order. Empty
	// for a JavaScript or Docker action, which has no steps.
	Uses []StepUse `json:"uses,omitzero"`
	// FlakeUpdate is as Job.FlakeUpdate.
	FlakeUpdate bool `json:"flake_update,omitzero"`
}
