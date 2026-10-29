package rules

import (
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/rvenutolo/github-repos-audit/internal/audit"
)

// maxLockAge is how old the nixpkgs lock may be before nix_lock_age fails.
// nixpkgs channels move daily and carry security fixes, so a month without a
// refresh means a month of fixes the flake does not have; the age is measured
// to the second, so exactly 30 days still passes.
const maxLockAge = 30 * 24 * time.Hour

// observeLockAge judges how long ago the flake's nixpkgs was locked, against
// now, the snapshot's instant, so the same audit.json always reads the same
// way. The date is the lock's `lastModified` for the node the root's nixpkgs
// input resolves to; the cell carries it and the renderer shows its age
// beside the mark, so a stale lock reads as how stale.
//
// There is nothing to date without a flake.lock, or in a lock with no
// nixpkgs input, so both are n/a. A lock the collector could not read fails
// as unreadable: it recorded no date, and passing on a missing date would
// hide exactly the lock that most needs looking at.
func observeLockAge(r audit.Repo, now time.Time) observation {
	switch {
	case !r.Flake.HasLock:
		return observation{unknown: true}
	case r.Flake.LockError != "":
		return observation{value: "unreadable flake.lock", mark: true}
	case r.Flake.NixpkgsLastModified.IsZero():
		return observation{unknown: true}
	}
	at := r.Flake.NixpkgsLastModified
	return observation{at: at, ok: now.Sub(at) <= maxLockAge, mark: true, scalar: true}
}

// updateFlakeLockAction is the published action that opens a pull request
// refreshing flake.lock, matched by usesAction.
const updateFlakeLockAction = "DeterminateSystems/update-flake-lock"

// usesAction reports whether a step's `uses:` names the published action
// ownerRepo at some version: the part before the "@" that pins it is
// ownerRepo, in any case, since GitHub matches owner and repository names
// without regard to case. The version after the "@" is not compared. A path
// inside the repository ("owner/repo/sub@v1") is a different action, and a
// reference with no "@" names no version, which GitHub refuses, so neither
// matches.
func usesAction(uses, ownerRepo string) bool {
	name, _, pinned := strings.Cut(uses, "@")
	return pinned && strings.EqualFold(name, ownerRepo)
}

// observeLockRefresh judges whether something refreshes flake.lock without a
// person remembering to: Renovate, or a scheduled workflow. The cell names
// each source that does, so a pass says which one to look at.
//
// Renovate counts only with a configuration, its nix manager running, and
// either lock-file maintenance or every input pinned by rev. The manager
// alone updates a rev-pinned input's digest, but opens nothing for an input
// that follows a branch; lock-file maintenance rewrites lock files only for
// the managers that run, so without the nix manager it never touches
// flake.lock.
//
// A workflow counts only when it runs on a schedule: one run by hand or on a
// push refreshes the lock no more often than someone thinks to. Its job must
// run `nix flake update`, use the update-flake-lock action, or use a local
// composite that runs `nix flake update`.
//
// With no source found, every workflow that could not be read, and every
// unreadable composite a scheduled workflow uses, is named: any of them
// might be the refresh the cell cannot see, so it fails saying where to look
// rather than bare. n/a without a flake.lock, as there is nothing to refresh.
func observeLockRefresh(r audit.Repo) observation {
	if !r.Flake.HasLock {
		return observation{unknown: true}
	}
	var sources []string
	if r.Files.RenovateConfig != "" && r.Renovate.NixManager &&
		(r.Renovate.LockFileMaintenance || r.Flake.AllRevPinned) {
		sources = append(sources, "Renovate")
	}
	refreshes, unreadable := scheduledFlakeUpdate(r)
	if refreshes {
		sources = append(sources, "workflow")
	}
	if len(sources) > 0 {
		return observation{ok: true, value: strings.Join(sources, ", "), mark: true}
	}
	if len(unreadable) > 0 {
		return observation{value: strings.Join(unreadable, ", "), mark: true}
	}
	return observation{}
}

// scheduledFlakeUpdate reports whether a scheduled workflow refreshes
// flake.lock (see observeLockRefresh), and, as "unreadable <path>", every
// workflow it could not read and every composite a scheduled job uses that it
// could not read, each once, in the order it first met them: a composite
// several jobs use is still one file to look at.
func scheduledFlakeUpdate(r audit.Repo) (bool, []string) {
	var unreadable []string
	note := func(path string) {
		if item := "unreadable " + path; !slices.Contains(unreadable, item) {
			unreadable = append(unreadable, item)
		}
	}
	for _, w := range r.Workflows {
		if w.ParseError != "" {
			note(w.Path)
			continue
		}
		if !slices.Contains(w.Events, "schedule") {
			continue
		}
		for _, j := range w.Jobs {
			if j.FlakeUpdate {
				return true, nil
			}
			for _, u := range j.Uses {
				if usesAction(u.Action, updateFlakeLockAction) {
					return true, nil
				}
				ca, local := resolveComposite(r, u.Action)
				switch {
				case !local:
				case ca.ParseError != "":
					note(ca.Path)
				case ca.FlakeUpdate:
					return true, nil
				}
			}
		}
	}
	return false, unreadable
}

// nixCacheCeiling bounds the newest Nix cache entry of each family from
// above, as cacheFloor does from below. Past it, every run spends longer
// downloading and unpacking the store than a cache is meant to save, and it
// crowds the repository's other caches out of its storage.
const nixCacheCeiling = 1_500_000_000

// cacheNixAction is the action that saves and restores the Nix store,
// matched by usesAction.
const cacheNixAction = "nix-community/cache-nix-action"

// observeNixCache judges whether the default branch's Nix store is cached,
// at a useful size, by a cache-nix-action configured not to grow without
// bound. Each runner OS restores its own family of entries (P4 in
// cacheFamilies), and only the newest entry of each is restored, so that is
// the one judged; the cell names the size of the worst family, the first
// that is out of range or else the largest, then every setting problem. A
// size over the ceiling is shown rounded up, so it never reads as the passing
// bound; every other size is truncated.
//
// It is n/a without a flake.nix, which has no store to cache, and when the
// default branch has not run CI within the activity window, since GitHub has
// then evicted the caches whatever the configuration. Unlike the size, the
// settings are read even when there is no cache to measure, so "none on
// main" says what else to fix too.
func observeNixCache(r audit.Repo, now time.Time) observation {
	if !r.Files.FlakeNix || !recentDefaultRun(r, now) {
		return observation{unknown: true}
	}
	problems := cacheNixProblems(r)
	families := cacheFamilies(r, "nix-")
	if len(families) == 0 {
		return observation{value: strings.Join(append([]string{"none on main"}, problems...), ", "), mark: true}
	}
	worst, inRange := families[0].Newest.SizeBytes, true
	for _, f := range families {
		size := f.Newest.SizeBytes
		if size < cacheFloor || size > nixCacheCeiling {
			worst, inRange = size, false
			break
		}
		worst = max(worst, size)
	}
	value := formatBytes(worst)
	switch {
	case worst < cacheFloor:
		value = "empty (" + value + ")"
	case worst > nixCacheCeiling:
		value = formatBytesUp(worst)
	}
	return observation{
		ok:    inRange && len(problems) == 0,
		value: strings.Join(append([]string{value}, problems...), ", "),
		mark:  true,
	}
}

// cacheNixProblems lists what is wrong with the repository's
// cache-nix-action steps, each problem once and in a fixed order, then every
// workflow, and every composite a job uses, that could not be read, as
// "unreadable <path>" in path order: any of them may hold a step the list
// cannot see. A step counts whether a job runs it directly or through a
// local composite, which runs under the calling job's permissions.
//
//   - "no gc-max-store-size": no store bound, so the saved store grows by
//     every build until it passes the ceiling.
//   - "no purge": old entries are left for GitHub's own eviction, which
//     goes by last access rather than by kind, and so may take another
//     cache before them.
//   - "no purge selector": nothing says which entries to purge (by key
//     prefix, age or primary key), so purging has nothing to act on.
//   - "no actions: write": purging deletes caches through the API, which the
//     job's token may not do without that permission.
func cacheNixProblems(r audit.Repo) []string {
	var noGC, noPurge, noSelector, noWrite bool
	var unreadable []string
	judge := func(u audit.StepUse, write bool) {
		if !usesAction(u.Action, cacheNixAction) {
			return
		}
		noGC = noGC || !anySet(u.With, "gc-max-store-size", "gc-max-store-size-linux", "gc-max-store-size-macos")
		// YAML reads True and TRUE as the same boolean as true.
		noPurge = noPurge || !strings.EqualFold(u.With["purge"], "true")
		noSelector = noSelector || !anySet(u.With, "purge-prefixes", "purge-created", "purge-last-accessed", "purge-primary-key")
		noWrite = noWrite || !write
	}
	for _, w := range r.Workflows {
		if w.ParseError != "" {
			unreadable = append(unreadable, w.Path)
			continue
		}
		for _, j := range w.Jobs {
			write := grantsActionsWrite(r, w, j)
			for _, u := range j.Uses {
				judge(u, write)
				ca, local := resolveComposite(r, u.Action)
				switch {
				case !local:
				case ca.ParseError != "":
					unreadable = append(unreadable, ca.Path)
				default:
					for _, cu := range ca.Uses {
						judge(cu, write)
					}
				}
			}
		}
	}
	var out []string
	for _, p := range []struct {
		on   bool
		text string
	}{
		{noGC, "no gc-max-store-size"},
		{noPurge, "no purge"},
		{noSelector, "no purge selector"},
		{noWrite, "no actions: write"},
	} {
		if p.on {
			out = append(out, p.text)
		}
	}
	slices.Sort(unreadable)
	for _, path := range slices.Compact(unreadable) {
		out = append(out, "unreadable "+path)
	}
	return out
}

// determinateNixAction is the installer every flake repository's CI is held
// to, matched by usesAction. It installs Determinate Nix and nothing else:
// unlike nix-installer-action it has no input that turns it to upstream Nix,
// so the step's name alone says which Nix the job gets.
const determinateNixAction = "DeterminateSystems/determinate-nix-action"

// telemetryOff is the variable that stops a determinateOwner action
// reporting to Determinate Systems over OpenTelemetry. Only the step's own
// env counts: a job's or workflow's env would reach it too, but the standard
// is one place, on the step itself, where moving the step cannot leave it
// behind. An empty OTEL_EXPORTER_OTLP_ENDPOINT also stops the export, but is
// not accepted: one spelling keeps every repository's steps alike.
const telemetryOff = "OTEL_SDK_DISABLED"

// determinateOwner publishes determinateNixAction and the other Nix actions
// a flake repository's CI may run, such as update-flake-lock and
// magic-nix-cache-action. Their JavaScript actions report over
// OpenTelemetry unless telemetryOff is set, so every action under the owner
// is held to it rather than a list that a new action would slip past. Some
// also report through inputs of their own: installer diagnostics, judged
// through diagnosticActions, and flakeCheckerAction's statistics, judged
// through sendStatistics.
const determinateOwner = "DeterminateSystems"

// diagnosticEndpoint is the input through which a diagnosticActions step
// sends diagnostics to Determinate Systems. Its default reports to the host
// the installer itself downloads from, so an egress allowlist cannot stop
// the one without the other, and OTEL_SDK_DISABLED does not reach it: only
// the input given exactly empty turns it off.
const diagnosticEndpoint = "diagnostic-endpoint"

// nixInstallerAction is the older Determinate installer: another installer
// to otherNixInstallers, and one of diagnosticActions.
const nixInstallerAction = "DeterminateSystems/nix-installer-action"

// flakeCheckerAction reports the issues it finds in a flake to Determinate
// Systems unless its sendStatistics input is false.
const flakeCheckerAction = "DeterminateSystems/flake-checker-action"

// sendStatistics is flakeCheckerAction's reporting switch. It defaults to
// true, so only the step's own with: setting it false turns it off.
const sendStatistics = "send-statistics"

// diagnosticActions are the determinateOwner actions whose action.yml
// declares diagnosticEndpoint. It is a list, unlike the owner-wide
// telemetryOff rule, because GitHub warns on an input an action does not
// declare: holding every action under the owner to it would demand an input
// most of them cannot take. An action that gains the input is not judged
// until it is added here.
//
//nolint:gochecknoglobals // immutable lookup table
var diagnosticActions = []string{
	determinateNixAction,
	nixInstallerAction,
	"DeterminateSystems/magic-nix-cache-action",
	"DeterminateSystems/flakehub-cache-action",
}

// diagnosticsOff reports whether with turns diagnosticEndpoint off: the key
// is there and its value is exactly empty. Leaving it out is the action's
// default, which reports, and any other value is an endpoint that reports
// somewhere. Whitespace is not empty: the actions read the input untrimmed,
// and a value that is no URL falls back to the default endpoint. Only the
// step's own with: counts, since that is the one place GitHub reads an
// action's inputs from.
func diagnosticsOff(with map[string]string) bool {
	v, ok := input(with, diagnosticEndpoint)
	return ok && v == ""
}

// statisticsOff reports whether with sets sendStatistics false, in one of
// the spellings the action's boolean input accepts, which it reads trimmed.
// Any other value is true, an expression the audit cannot resolve, or one
// the action rejects; none of them is a step known not to report.
func statisticsOff(with map[string]string) bool {
	v, _ := input(with, sendStatistics)
	switch strings.TrimSpace(v) {
	case "false", "False", "FALSE":
		return true
	default:
		return false
	}
}

// input returns the value with gives the action input name, and whether it
// gives one. GitHub matches input names without regard to case, so
// `Send-Statistics:` sets send-statistics.
func input(with map[string]string, name string) (string, bool) {
	for k, v := range with {
		if strings.EqualFold(k, name) {
			return v, true
		}
	}
	return "", false
}

// appendFiles appends one entry per distinct path in paths, in path order,
// each as prefix followed by the path.
func appendFiles(problems []string, prefix string, paths []string) []string {
	slices.Sort(paths)
	for _, path := range slices.Compact(paths) {
		problems = append(problems, prefix+path)
	}
	return problems
}

// fromDeterminate reports whether uses names a published action of
// determinateOwner, at a version, matched as usesAction matches: GitHub
// compares the owner without regard to case. An action in a subdirectory of
// one of its repositories counts too.
func fromDeterminate(uses string) bool {
	name, _, pinned := strings.Cut(uses, "@")
	owner, repo, _ := strings.Cut(name, "/")
	return pinned && repo != "" && strings.EqualFold(owner, determinateOwner)
}

// telemetryDisabled reports whether env turns telemetryOff on. The value is
// trimmed and read without regard to case, as the actions' OpenTelemetry SDK
// reads it; an expression is not, since the audit cannot resolve it.
func telemetryDisabled(env map[string]string) bool {
	return strings.EqualFold(strings.TrimSpace(env[telemetryOff]), "true")
}

// otherNixInstallers are the published actions known to install Nix other
// than determinateNixAction, each as the cell names it. An install through
// an action not listed here, or by a `run:` step, is not seen.
//
//nolint:gochecknoglobals // immutable lookup table
var otherNixInstallers = []string{
	nixInstallerAction,
	"cachix/install-nix-action",
	"nixbuild/nix-quick-install-action",
}

// observeNixInstaller judges whether every Nix install in the repository's
// CI uses determinateNixAction, every determinateOwner step, the install
// included, sets telemetryOff, every diagnosticActions step turns
// diagnosticEndpoint off, and every flakeCheckerAction step turns
// sendStatistics off, so every flake repository runs one Nix, installed one
// way, with nothing reporting home. A step counts whether a job runs it
// directly or through a local composite. The cell names each other
// installer found, once and in list order, then each file holding a
// determinateOwner step without telemetryOff, as "telemetry on in <path>"
// in path order, then each file holding a diagnosticActions step without
// diagnostics off, as "diagnostics on in <path>" in path order, then each
// file holding a flakeCheckerAction step without statistics off, as
// "statistics on in <path>" in path order, then every workflow, and every
// composite a job uses, that could not be read, as "unreadable <path>" in
// path order: any of them may hold an install the check cannot see, so one
// fails it even beside a passing install.
//
// It is n/a without a flake.nix, and when nothing readable installs Nix,
// nothing leaves telemetry, diagnostics or statistics on, and nothing is
// unreadable: a repository whose CI never installs Nix has no installer to
// standardize, but a determinateOwner step reporting home any way is a
// gap wherever it runs, whether or not the check sees the install it relies
// on.
func observeNixInstaller(r audit.Repo) observation {
	if !r.Files.FlakeNix {
		return observation{unknown: true}
	}
	var installs bool
	others := map[string]bool{}
	var telemetry, diagnostics, statistics, unreadable []string
	judge := func(u audit.StepUse, path string) {
		if usesAction(u.Action, determinateNixAction) {
			installs = true
		}
		if fromDeterminate(u.Action) && !telemetryDisabled(u.Env) {
			telemetry = append(telemetry, path)
		}
		takesDiagnostics := slices.ContainsFunc(diagnosticActions, func(name string) bool {
			return usesAction(u.Action, name)
		})
		if takesDiagnostics && !diagnosticsOff(u.With) {
			diagnostics = append(diagnostics, path)
		}
		if usesAction(u.Action, flakeCheckerAction) && !statisticsOff(u.With) {
			statistics = append(statistics, path)
		}
		for _, name := range otherNixInstallers {
			if usesAction(u.Action, name) {
				installs, others[name] = true, true
			}
		}
	}
	for _, w := range r.Workflows {
		if w.ParseError != "" {
			unreadable = append(unreadable, w.Path)
			continue
		}
		for _, j := range w.Jobs {
			for _, u := range j.Uses {
				judge(u, w.Path)
				ca, local := resolveComposite(r, u.Action)
				switch {
				case !local:
				case ca.ParseError != "":
					unreadable = append(unreadable, ca.Path)
				default:
					for _, cu := range ca.Uses {
						judge(cu, ca.Path)
					}
				}
			}
		}
	}
	if !installs && len(telemetry) == 0 && len(diagnostics) == 0 && len(statistics) == 0 && len(unreadable) == 0 {
		return observation{unknown: true}
	}
	var problems []string
	for _, name := range otherNixInstallers {
		if others[name] {
			problems = append(problems, name)
		}
	}
	problems = appendFiles(problems, "telemetry on in ", telemetry)
	problems = appendFiles(problems, "diagnostics on in ", diagnostics)
	problems = appendFiles(problems, "statistics on in ", statistics)
	problems = appendFiles(problems, "unreadable ", unreadable)
	return observation{ok: len(problems) == 0, value: strings.Join(problems, ", "), mark: true}
}

// anySet reports whether any of keys is given a non-empty value. For the
// inputs it is asked about, an input given as empty is the action's default,
// which is the same as leaving it out; that is not true of every input, as
// diagnosticEndpoint shows.
func anySet(with map[string]string, keys ...string) bool {
	return slices.ContainsFunc(keys, func(k string) bool { return with[k] != "" })
}

// grantsActionsWrite reports whether job j of workflow w runs with
// actions: write. GitHub takes the job's `permissions:` when it has one,
// else the workflow's, else the repository's default token permissions, and
// a `permissions:` that is set replaces the one above it outright rather
// than adding to it. "write-all" grants every scope and "read-all" none for
// writing; the map form grants only what it names, so a map without
// actions: write leaves the token without it.
func grantsActionsWrite(r audit.Repo, w audit.Workflow, j audit.Job) bool {
	p := j.Permissions
	if !p.Set {
		p = w.Permissions
	}
	if !p.Set {
		return r.Settings.DefaultWorkflowPermissions == "write"
	}
	return p.All == "write-all" || p.Scopes["actions"] == "write"
}

// resolveComposite finds the composite action a step's `uses:` names, when
// it names one in this repository. "./x" and "$/x" resolve alike, both
// against the repository root, to the directory x, whose action.yml (or,
// failing that, action.yaml) the collector recorded by its repository path,
// such as ".github/actions/setup/action.yml". Anything
// else, a published action or a Docker image, is not local, and a local path
// with no recorded composite is not found: both report false.
func resolveComposite(r audit.Repo, action string) (audit.CompositeAction, bool) {
	dir, ok := strings.CutPrefix(action, "./")
	if !ok {
		dir, ok = strings.CutPrefix(action, "$/")
	}
	if !ok {
		return audit.CompositeAction{}, false
	}
	// "./x/" names the same directory as "./x"; left alone it would build
	// "x//action.yml", which no recorded path matches.
	dir = strings.TrimSuffix(dir, "/")
	for _, name := range []string{"action.yml", "action.yaml"} {
		i := slices.IndexFunc(r.Actions, func(a audit.CompositeAction) bool { return a.Path == dir+"/"+name })
		if i >= 0 {
			return r.Actions[i], true
		}
	}
	return audit.CompositeAction{}, false
}

// urlSchemeArg matches a flake reference with a URL scheme, which Nix
// fetches rather than reads from the working tree.
var urlSchemeArg = regexp.MustCompile(`^[a-z][a-z0-9+.-]*:`)

// registryArg matches a registry reference such as `nixpkgs` or
// `nixpkgs/nixos-24.05`, which Nix resolves to a fetched flake.
var registryArg = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*(/[A-Za-z0-9._-]+)?$`)

// getFlakeThroughGit reports whether a normalized builtins.getFlake argument
// makes Nix read the flake through git or a fetcher rather than the working
// tree. Only a string does: a path, `toString` of one, an identifier or any
// string that is a path (including `path:`, which reads the directory as is)
// reads the working tree, and an expression the tool cannot read is held to
// the same standard because it may.
func getFlakeThroughGit(arg string) bool {
	if len(arg) < 2 || arg[0] != '"' || arg[len(arg)-1] != '"' {
		return false
	}
	s := arg[1 : len(arg)-1]
	if urlSchemeArg.MatchString(s) {
		return !strings.HasPrefix(s, "path:")
	}
	return registryArg.MatchString(s)
}

// observeNixGetflakeGit judges whether every builtins.getFlake call in the
// repository's Nix, .ci and workflow files names its flake in a way that
// goes through git, so a flake evaluated from a script sees the committed
// tree and not stray working-tree files. The cell names each file holding a
// call that does not, in path order, then each file that could not be read,
// since it may hold one; too many files to read is a problem of its own.
//
// It is n/a when the repository tracks no Nix file. A repository whose files
// hold no call passes.
func observeNixGetflakeGit(r audit.Repo) observation {
	g := r.NixGetFlake
	if !g.NixFiles {
		return observation{unknown: true}
	}
	var problems []string
	if g.TooManyFiles {
		problems = []string{"too many Nix files"}
	} else {
		var bad, unreadable []string
		for _, f := range g.Files {
			if f.Unreadable != "" {
				unreadable = append(unreadable, f.Path)
				continue
			}
			if slices.ContainsFunc(f.Args, func(a string) bool { return !getFlakeThroughGit(a) }) {
				bad = append(bad, f.Path)
			}
		}
		problems = appendFiles(problems, "not via git in ", bad)
		problems = appendFiles(problems, "unreadable ", unreadable)
	}
	return observation{ok: len(problems) == 0, value: strings.Join(problems, ", "), mark: true}
}
