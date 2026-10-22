# Changelog

Every release of this tool, newest first. Sections list the changes merged
since the release before them. What a version number means is in the
[Versioning section of the README](README.md#versioning).

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
