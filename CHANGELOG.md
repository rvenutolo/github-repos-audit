# Changelog

Every release of this tool, newest first. Sections list the changes merged
since the release before them. What a version number means is in the
[Versioning section of the README](README.md#versioning).

## v8.0.0

### Breaking changes

- add the nix_getflake_git check

### Features

- extract builtins.getFlake arguments from text
- collect builtins.getFlake arguments from Nix, .ci and workflow files

### Fixes

- read the flake through git in check-devshell-provides
- read multi-line and indented-string getFlake calls

## v7.0.0

### Breaking changes

- report committed Claude Code MCP config and settings files

## v6.3.0

### Features

- judge diagnostic-endpoint in the nix_installer check
- judge send-statistics in the nix_installer check

## v6.2.0

### Features

- hold every DeterminateSystems action to telemetry off

## v6.1.0

### Features

- fail nix_installer when determinate-nix-action telemetry is on

## v6.0.1

### Fixes

- honour every type word in a per-repository override

## v6.0.0

### Breaking changes

- add the nix_installer check

## v5.0.0

### Breaking changes

- add the hermetic_nix check and [hermetic] table
- add the nix_lock_age check
- add the nix_lock_refresh check
- add the nix_cache check
- add the go_cache check
- add the cache_usage check
- add the ci_cancel_superseded check
- add the job_timeouts check
- add the artifact_storage check
- check that CI runs on pull requests and the default branch
- check that pull requests must be up to date to merge
- remove the hermetic_nix check and [hermetic] table
- add the renovate_automerge_major check
- add the skill checks and the Claude report section
- add the hook checks
- add the plugin manifest and release checks
- add the marketplace pin and listing checks
- fail marketplace entries that point at a non-plugin repository

  marketplace_entries_valid now fails an entry whose
  source repository is not a plugin, so a marketplace that lists one turns
  from passing to failing.

### Features

- parse workflow and composite-action facts
- read flake inputs and lock facts
- collect workflow, composite-action and flake facts
- collect Actions cache and artifact storage
- record the latest default-branch run
- resolve Renovate nix manager and lock-file maintenance
- list unrecognised, action-owned and pull-request caches
- record an unreadable flake.nix
- record pull request trigger types
- record the push trigger's branch filters
- record whether required checks must be up to date
- record the pull_request triggers' branch filters
- record whether Renovate auto-merges major updates
- parse Claude Code skills, commands, hooks and plugin manifests
- collect Claude Code skills, commands, hooks and plugin facts
- record each repository's latest release tag
- read the configured Claude plugin marketplace
- record an inline command's plugin.json description
- record hook command and args in audit.json
- record a source command's plugin.json description

### Fixes

- keep Renovate manager facts out of the release-age error
- read the last default-branch run with its own request
- give the artifacts list its own page cap
- list info repositories behind the CI efficiency counts
- recognise trivy-action's cache-trivy- cache keys
- show the ref of each listed cache
- keep listed workflow paths on one line
- match YAML booleans and action names in any case
- reject duplicate keys in workflow files
- drop blank workflow event names
- keep older report list values on one line
- recognise Renovate's built-in lock-file presets
- skip PR workflows that cannot be superseded
- ignore fork runs when dating the default branch
- ignore comment-only lint mentions
- read a false expression and the github scheme exactly
- show a failing cache size above the ceiling
- memoise flake.lock follows resolution
- trim the trailing slash of a local composite path
- treat an empty push tag filter as a tag filter
- require push_tag_filter to be true when present
- require pull_request branch filters to admit the default branch
- read selector-less, legacy and glob packageRules as Renovate does
- resolve interpreter scripts, nested commands and hook files correctly
- count unreadable skills as wrappable by commands
- read a skill or command file without frontmatter as having no keys
- resolve the script of a hook handler that sets no type
- require a description with text for skill_description_length
- keep paths plugin.json declares that the tree does not hold
- skip an unreadable non-plugin hooks file in hook_plugin_root
- keep marketplace pin lookups from aborting the run
- judge the marketplace row despite unreadable Claude files
- name the non-semver case in the plugin version gap label
- pass a marketplace pin ahead of the latest release
- read a string matchUpdateTypes as a one-element list
- treat a rule with both update-type keys as narrowed
- match a wrapped skill name without compiling a regex
- look up interpreter flags with switches, not per-call tables
- name a refused preset repository as an invalid preset name
- tell numeric semver identifiers by their digits
- read a version equal to its tag in precedence as a match
- reject plugin.json component paths without the ./ prefix
- say a declared Claude file that is not tracked is missing
- word the unloadable declared-path reason to fit every case
- name an object-map source command by its key
- match branch filter patterns without compiling a regexp
- bump nixpkgs for Go 1.26.10 (GO-2026-6809)

## v4.0.5

### Fixes

- drop the Nix store cache and fix the Go cache
- build with Go 1.26.9 for standard-library security fixes

## v4.0.4

### Fixes

- quote run-lint-checks' temp-dir trap with printf %q

## v4.0.3

### Fixes

- send the refresh commit's files in the request body
- keep the refresh script to gh and coreutils

## v4.0.2

### Fixes

- read CI timing from the Actions API
- count only change-triggered runs in CI timing

## v4.0.1

### Fixes

- size the CI query pages to GitHub's resource limit

## v4.0.0

### Breaking changes

- write a fresh report on every render run

  render prints nothing to stdout on success; the
  material-change=\<bool> line is gone, and so is the workflow step that
  appended it to $GITHUB_OUTPUT.

### Features

- collect recent CI timing per repository
- compute CI timing lines for the report
- show CI timing in the report

### Fixes

- widen the CI suite page and correct its comments
- retry a GraphQL query that timed out

## v3.0.0

### Breaking changes

- add file-path checks for tooling and community files

### Features

- add accepted-name tables for multi-path files
- report file paths in their own section

### Fixes

- resolve nix-update from the flake lock, not the registry
- match justfile names case-insensitively, as just does

## v2.0.1

### Fixes

- fetch each preset at most once even when collectors race
- never reset a pull request's branch onto its base
- name the refused field in GraphQL error messages

## v2.0.0

### Breaking changes

- run the live smoke suite from this repository

  smoke.yml is not a reusable workflow. A report repository
  deletes its smoke caller and its SMOKE_* variables.

- run the release binary in the reusable workflows

  the pinned commit must be a release's commit; any other
  fails the run. validate.yml checks repos.toml and audit.json and does not
  lint the caller's README.

### Features

- validate audit.json against its schema with validate --audit
- fetch and verify the release binary for a pinned commit

### Fixes

- **deps:** update module golang.org/x/text to v0.43.0

## v1.0.1

### Fixes

- refuse to publish a withdrawn release commit
- name the released binary after the repository

## v1.0.0

The first stable release. From here on the version number is a promise about
the tool's public interface: the keys and values `repos.toml` accepts, the
keys in `audit.json`, the exit codes, the commands and flags, and the inputs
and secrets of the reusable workflows.

### What the tool does

- Reads every non-fork, non-archived repository an account owns and judges
  each against the standard its `repos.toml` type declares.
- Writes a Markdown report spliced into a README, and `audit.json`, validated
  by `audit.schema.json`.
- Reports configuration drift between `repos.toml` and the account without
  failing the run.
- Runs from three reusable workflows (`audit.yml`, `validate.yml`,
  `smoke.yml`) pinned by commit SHA, or locally through `go install` or a
  prebuilt binary.

### Release artifacts

- Binaries for Linux and macOS on amd64 and arm64, and Windows on amd64.
- `SHA256SUMS`, signed with cosign, a CycloneDX SBOM, and a build-provenance
  attestation for each binary.
