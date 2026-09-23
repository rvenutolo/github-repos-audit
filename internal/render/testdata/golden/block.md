## Gaps

- **No README** (1) — go-linter
- **No description** (1) — go-linter
- **No license** (1) — cipher-lib
- **No .gitignore** (2) — go-linter, web-app
- **No CI workflows** (1) — go-linter
- **No required checks** (1) — github-repos-audit
- **CI not green on the default branch** (1) — go-linter
- **No Renovate config** (1) — go-linter
- **Renovate minimum release age unset, unresolved or too short** (2) — mixedCase-flake, web-app
- **No .editorconfig** (1) — go-linter
- **No flake.nix** (1) — go-linter
- **No .justfile** (1) — go-linter
- **No CONTRIBUTING.md** (1) — mixedCase-flake
- **No CODE_OF_CONDUCT.md** (1) — mixedCase-flake
- **No tag ruleset** (1) — go-linter
- **Direct push to the default branch allowed** (1) — go-linter
- **Direct push to the default branch blocked** (1) — cipher-lib
- **Secret scanning off** (1) — cipher-lib

## Metadata

| Repository                                                           | Type     | Visibility | Published | Description                               | Homepage | Topics | License | README |
| -------------------------------------------------------------------- | -------- | ---------- | --------- | ----------------------------------------- | -------- | ------ | ------- | ------ |
| [cipher-lib](https://github.com/gh-owner/cipher-lib)                 | content  | public     | no        | the cipher-lib repository                 | n/a      | 6      | ✗       | ✓      |
| [github-repos-audit](https://github.com/gh-owner/github-repos-audit) | tools    | private    | no        | the github-repos-audit repository         | n/a      | 0      | n/a     | ✓      |
| [go-linter](https://github.com/gh-owner/go-linter)                   | config   | private    | no        |                                           | n/a      | 0      | n/a     | ✗      |
| [mixedCase-flake](https://github.com/gh-owner/mixedCase-flake)       | software | public     | yes       | a flake \| with a pipe in its description | ✓        | 4      | MIT     | ✓      |
| [web-app](https://github.com/gh-owner/web-app)                       | config   | private    | no        | the web-app repository                    | n/a      | 0      | n/a     | ✓      |

## Tree

| Repository                                                           | Renovate                 | Min. release age | .gitignore | .editorconfig | flake.nix | .justfile | CHANGELOG | Community files |
| -------------------------------------------------------------------- | ------------------------ | ---------------- | ---------- | ------------- | --------- | --------- | --------- | --------------- |
| [cipher-lib](https://github.com/gh-owner/cipher-lib)                 | n/a                      | n/a              | ✓          | ✓             | n/a       | n/a       | n/a       | n/a             |
| [github-repos-audit](https://github.com/gh-owner/github-repos-audit) | `renovate.json`          | ✓ 7 days         | ✓          | ✓             | ✓         | ✓         | n/a       | n/a             |
| [go-linter](https://github.com/gh-owner/go-linter)                   | ✗                        | n/a              | ✗          | ✗             | ✗         | ✗         | n/a       | n/a             |
| [mixedCase-flake](https://github.com/gh-owner/mixedCase-flake)       | `.github/renovate.json5` | ✗ 3 days         | ✓          | ✓             | ✓         | ✓         | ✓         | 1/3             |
| [web-app](https://github.com/gh-owner/web-app)                       | `renovate.json`          | ✗ unresolved     | ✗          | ✓             | n/a       | ✓         | n/a       | n/a             |

## Policy

| Repository                                                           | Direct push | Signed | CI workflows | Required checks | Tag ruleset  | Secret scanning | Vuln. reporting |
| -------------------------------------------------------------------- | ----------- | ------ | ------------ | --------------- | ------------ | --------------- | --------------- |
| [cipher-lib](https://github.com/gh-owner/cipher-lib)                 | ✗ blocked   | ✓      | n/a          | 2               | default      | ✗               | n/a             |
| [github-repos-audit](https://github.com/gh-owner/github-repos-audit) | ✓ blocked   | ✓      | ✓            | ✗               | default      | n/a             | n/a             |
| [go-linter](https://github.com/gh-owner/go-linter)                   | ✗ allowed   | n/a    | ✗            | n/a             | ✗            | n/a             | n/a             |
| [mixedCase-flake](https://github.com/gh-owner/mixedCase-flake)       | ✓ blocked   | ✓      | ✓            | 2               | `ruleset-02` | ✓               | ✓               |
| [web-app](https://github.com/gh-owner/web-app)                       | ✓ blocked   | ✓      | ✓            | 2               | default      | n/a             | n/a             |

## Activity

| Repository                                                           | Last push | CI      | Open PRs | Branches | Releases | Last release |
| -------------------------------------------------------------------- | --------- | ------- | -------- | -------- | -------- | ------------ |
| [cipher-lib](https://github.com/gh-owner/cipher-lib)                 | 11d       | n/a     | 0        | 0        | 0        | n/a          |
| [github-repos-audit](https://github.com/gh-owner/github-repos-audit) | 11d       | PENDING | 0        | 3        | 0        | n/a          |
| [go-linter](https://github.com/gh-owner/go-linter)                   | never     | ✗       | 0        | 0        | 0        | n/a          |
| [mixedCase-flake](https://github.com/gh-owner/mixedCase-flake)       | 11d       | SUCCESS | 0        | 0        | 3        | 5mo          |
| [web-app](https://github.com/gh-owner/web-app)                       | 11d       | SUCCESS | 0        | 0        | 0        | n/a          |

## Settings exceptions

- **mixedCase-flake** — projects `enabled`, usually `disabled`
- **web-app** — wiki `enabled`, usually `disabled`

### No consensus

The most common value covers half or fewer of the repositories these apply to,
so there is no norm to measure against.

- secret-scanning push protection

## Overrides

- **web-app** — `flake_nix` is `not_required`

_Read from GitHub on 2026-09-05._
