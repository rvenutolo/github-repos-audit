# Changelog

Every release of this tool, newest first. Sections list the changes merged
since the release before them. What a version number means is in the
[Versioning section of the README](README.md#versioning).

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
