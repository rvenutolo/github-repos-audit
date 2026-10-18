## Configuration drift

`repos.toml` and the account disagree. Add a `[repos.<name>]` table for each repository that needs one, and remove each stale entry: its repository was deleted, archived or turned into a fork. Everything below covers only the repositories both name.

- **Needs a `repos.toml` entry** (1) — [hook-guard](https://github.com/gh-owner/hook-guard)
- **Stale `repos.toml` entry** (1) — `media-server`

## Metadata

| Repository                                 | Type  | Visibility | Published | Description          | Homepage | Topics | License | README |
| ------------------------------------------ | ----- | ---------- | --------- | -------------------- | -------- | ------ | ------- | ------ |
| [alpha](https://github.com/gh-owner/alpha) | tools | private    | no        | the alpha repository | n/a      | 0      | n/a     | ✓      |
| [bravo](https://github.com/gh-owner/bravo) | tools | private    | no        | the bravo repository | n/a      | 0      | n/a     | ✓      |

## Tree

| Repository                                 | Renovate        | Min. release age | .gitignore | .editorconfig | flake.nix | .justfile  | CHANGELOG | Community files |
| ------------------------------------------ | --------------- | ---------------- | ---------- | ------------- | --------- | ---------- | --------- | --------------- |
| [alpha](https://github.com/gh-owner/alpha) | `renovate.json` | ✓ 7 days         | ✓          | ✓             | ✓         | `justfile` | n/a       | n/a             |
| [bravo](https://github.com/gh-owner/bravo) | `renovate.json` | ✓ 7 days         | ✓          | ✓             | ✓         | `justfile` | n/a       | n/a             |

## Policy

| Repository                                 | Direct push | Signed | Identity | CI workflows | Required checks | Tag ruleset | Secret scanning | Vuln. reporting |
| ------------------------------------------ | ----------- | ------ | -------- | ------------ | --------------- | ----------- | --------------- | --------------- |
| [alpha](https://github.com/gh-owner/alpha) | ✓ blocked   | ✓      | ✓        | ✓            | 2               | default     | n/a             | n/a             |
| [bravo](https://github.com/gh-owner/bravo) | ✓ blocked   | ✓      | ✓        | ✓            | 2               | default     | n/a             | n/a             |

## Activity

| Repository                                 | Last push | CI      | Open PRs | Branches | Releases | Last release |
| ------------------------------------------ | --------- | ------- | -------- | -------- | -------- | ------------ |
| [alpha](https://github.com/gh-owner/alpha) | 11d       | SUCCESS | 0        | 0        | 0        | n/a          |
| [bravo](https://github.com/gh-owner/bravo) | 11d       | SUCCESS | 0        | 0        | 0        | n/a          |

## Git identities

- **alpha** — `Pat Example <pat@example.com>` (canonical)
- **bravo** — `Pat Example <pat@example.com>` (canonical)

_Read from GitHub on 2026-09-05._
