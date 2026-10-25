## Gaps

- **No README** (1) — go-linter
- **No description** (1) — go-linter
- **No license** (1) — cipher-lib
- **No .gitignore** (2) — go-linter, web-app
- **No CI workflows** (1) — go-linter
- **No required checks** (1) — github-repos-audit
- **No CI on pull requests** (1) — go-linter
- **No CI on the default branch** (2) — github-repos-audit, go-linter
- **CI not green on the default branch** (1) — go-linter
- **No Renovate config** (1) — go-linter
- **Renovate minimum release age unset, unresolved or too short** (2) — mixedCase-flake, web-app
- **Renovate does not auto-merge major updates** (2) — mixedCase-flake, web-app
- **No .editorconfig** (1) — go-linter
- **No flake.nix** (1) — go-linter
- **No justfile** (1) — go-linter
- **No CONTRIBUTING.md** (1) — mixedCase-flake
- **No CODE_OF_CONDUCT.md** (1) — mixedCase-flake
- **Commits under a non-canonical git identity** (2) — github-repos-audit, mixedCase-flake
- **No tag ruleset** (1) — go-linter
- **Direct push to the default branch allowed** (1) — go-linter
- **Direct push to the default branch blocked** (1) — cipher-lib
- **Secret scanning off** (1) — cipher-lib
- **nixpkgs locked more than 30 days ago** (1) — github-repos-audit
- **No automated flake.lock refresh** (1) — mixedCase-flake
- **Nix cache missing, empty, oversized or misconfigured** (1) — github-repos-audit
- **Go cache missing or empty on the default branch** (1) — github-repos-audit
- **Pull-request workflows without cancel-in-progress** (1) — github-repos-audit
- **Jobs without timeout-minutes** (1) — github-repos-audit
- **Skill can run without its / command** (2) — github-repos-audit, web-app
- **Skill name differs from its directory** (1) — web-app
- **Skill frontmatter has unknown or mistyped keys** (2) — github-repos-audit, web-app
- **Skill description too long** (2) — github-repos-audit, web-app
- **Command file only wraps a skill** (1) — github-repos-audit
- **Hook on an unknown event** (1) — web-app
- **Hook script missing or not executable** (2) — github-repos-audit, web-app
- **Hook without a timeout** (2) — github-repos-audit, web-app
- **Tool hook without a matcher** (2) — github-repos-audit, web-app
- **Plugin manifest missing fields** (2) — github-repos-audit, web-app
- **Plugin repository has its own marketplace.json** (2) — github-repos-audit, web-app
- **Plugin has no release** (1) — web-app
- **Plugin version behind its latest release** (2) — github-repos-audit, web-app
- **Plugin not listed in the marketplace** (1) — plugin-kit
- **Marketplace pin over a week behind the latest release** (1) — cipher-lib
- **Marketplace has invalid entries** (1) — github-repos-audit
- **Marketplace is missing a public plugin** (1) — github-repos-audit

## Metadata

| Repository                                                           | Type     | Visibility | Published | Description                               | Homepage | Topics | License | README |
| -------------------------------------------------------------------- | -------- | ---------- | --------- | ----------------------------------------- | -------- | ------ | ------- | ------ |
| [cipher-lib](https://github.com/gh-owner/cipher-lib)                 | content  | public     | no        | the cipher-lib repository                 | n/a      | 6      | ✗       | ✓      |
| [github-repos-audit](https://github.com/gh-owner/github-repos-audit) | tools    | private    | no        | the github-repos-audit repository         | n/a      | 0      | n/a     | ✓      |
| [go-linter](https://github.com/gh-owner/go-linter)                   | config   | private    | no        |                                           | n/a      | 0      | n/a     | ✗      |
| [mixedCase-flake](https://github.com/gh-owner/mixedCase-flake)       | software | public     | yes       | a flake \| with a pipe in its description | ✓        | 4      | MIT     | ✓      |
| [plugin-kit](https://github.com/gh-owner/plugin-kit)                 | tools    | public     | no        | the plugin-kit repository                 | n/a      | 0      | MIT     | ✓      |
| [web-app](https://github.com/gh-owner/web-app)                       | config   | private    | no        | the web-app repository                    | n/a      | 0      | n/a     | ✓      |

## Tree

| Repository                                                           | Min. release age | Major automerge | .gitignore | .editorconfig | flake.nix | CHANGELOG | Community files |
| -------------------------------------------------------------------- | ---------------- | --------------- | ---------- | ------------- | --------- | --------- | --------------- |
| [cipher-lib](https://github.com/gh-owner/cipher-lib)                 | n/a              | n/a             | ✓          | ✓             | n/a       | n/a       | n/a             |
| [github-repos-audit](https://github.com/gh-owner/github-repos-audit) | ✓ 7 days         | ✓               | ✓          | ✓             | ✓         | n/a       | n/a             |
| [go-linter](https://github.com/gh-owner/go-linter)                   | n/a              | n/a             | ✗          | ✗             | ✗         | n/a       | n/a             |
| [mixedCase-flake](https://github.com/gh-owner/mixedCase-flake)       | ✗ 3 days         | ✗               | ✓          | ✓             | ✓         | ✓         | 1/3             |
| [plugin-kit](https://github.com/gh-owner/plugin-kit)                 | ✓ 7 days         | ✓               | ✓          | ✓             | ✓         | n/a       | n/a             |
| [web-app](https://github.com/gh-owner/web-app)                       | ✗ unresolved     | ✗ unresolved    | ✗          | ✓             | n/a       | n/a       | n/a             |

## File paths

Where each tooling and community file lives. `≠` marks a path that differs from the one most repositories use.

### Build & release

| Repository                                                           | justfile   | Renovate                   | commitlint           | git-cliff | treefmt | Compose |
| -------------------------------------------------------------------- | ---------- | -------------------------- | -------------------- | --------- | ------- | ------- |
| [cipher-lib](https://github.com/gh-owner/cipher-lib)                 | n/a        | n/a                        | `.commitlintrc.yml`  | n/a       | n/a     | n/a     |
| [github-repos-audit](https://github.com/gh-owner/github-repos-audit) | `justfile` | `renovate.json`            | `.commitlintrc.yml`  | n/a       | n/a     | n/a     |
| [go-linter](https://github.com/gh-owner/go-linter)                   | ✗          | ✗                          | n/a                  | n/a       | n/a     | n/a     |
| [mixedCase-flake](https://github.com/gh-owner/mixedCase-flake)       | `justfile` | `.github/renovate.json5` ≠ | `.commitlintrc.json` | n/a       | n/a     | n/a     |
| [plugin-kit](https://github.com/gh-owner/plugin-kit)                 | `justfile` | `renovate.json`            | n/a                  | n/a       | n/a     | n/a     |
| [web-app](https://github.com/gh-owner/web-app)                       | `justfile` | `renovate.json`            | `.commitlintrc.json` | n/a       | n/a     | n/a     |

### Linters

| Repository                                                           | typos | yamllint | markdownlint | Prettier | editorconfig-checker | lychee | zizmor | Codecov | MkDocs |
| -------------------------------------------------------------------- | ----- | -------- | ------------ | -------- | -------------------- | ------ | ------ | ------- | ------ |
| [cipher-lib](https://github.com/gh-owner/cipher-lib)                 | n/a   | n/a      | n/a          | n/a      | n/a                  | n/a    | n/a    | n/a     | n/a    |
| [github-repos-audit](https://github.com/gh-owner/github-repos-audit) | n/a   | n/a      | n/a          | n/a      | n/a                  | n/a    | n/a    | n/a     | n/a    |
| [go-linter](https://github.com/gh-owner/go-linter)                   | n/a   | n/a      | n/a          | n/a      | n/a                  | n/a    | n/a    | n/a     | n/a    |
| [mixedCase-flake](https://github.com/gh-owner/mixedCase-flake)       | n/a   | n/a      | n/a          | n/a      | n/a                  | n/a    | n/a    | n/a     | n/a    |
| [plugin-kit](https://github.com/gh-owner/plugin-kit)                 | n/a   | n/a      | n/a          | n/a      | n/a                  | n/a    | n/a    | n/a     | n/a    |
| [web-app](https://github.com/gh-owner/web-app)                       | n/a   | n/a      | n/a          | n/a      | n/a                  | n/a    | n/a    | n/a     | n/a    |

### GitHub & docs

| Repository                                                           | LICENSE   | SECURITY      | CODEOWNERS | PR template                        | CLAUDE.md |
| -------------------------------------------------------------------- | --------- | ------------- | ---------- | ---------------------------------- | --------- |
| [cipher-lib](https://github.com/gh-owner/cipher-lib)                 | `LICENSE` | `SECURITY.md` | n/a        | `.github/pull_request_template.md` | n/a       |
| [github-repos-audit](https://github.com/gh-owner/github-repos-audit) | `LICENSE` | `SECURITY.md` | n/a        | `.github/pull_request_template.md` | n/a       |
| [go-linter](https://github.com/gh-owner/go-linter)                   | n/a       | n/a           | n/a        | n/a                                | n/a       |
| [mixedCase-flake](https://github.com/gh-owner/mixedCase-flake)       | `LICENSE` | `SECURITY.md` | n/a        | `.github/pull_request_template.md` | n/a       |
| [plugin-kit](https://github.com/gh-owner/plugin-kit)                 | `LICENSE` | `SECURITY.md` | n/a        | `.github/pull_request_template.md` | n/a       |
| [web-app](https://github.com/gh-owner/web-app)                       | `LICENSE` | `SECURITY.md` | n/a        | `.github/pull_request_template.md` | n/a       |

## Claude

Only repositories with Claude Code skills, commands, hooks or a plugin are listed, plus the configured plugin marketplace.

### Skills

| Repository                                                           | Slash-only                                    | Name = dir                                    | Known keys                                    | Description                                   | No wrappers      | Longest SKILL.md                            |
| -------------------------------------------------------------------- | --------------------------------------------- | --------------------------------------------- | --------------------------------------------- | --------------------------------------------- | ---------------- | ------------------------------------------- |
| [github-repos-audit](https://github.com/gh-owner/github-repos-audit) | ✗ 2/3 skill-02                                | 2/2                                           | ✗ 2/3 skill-02                                | ✗ 1/2 skill-02                                | ✗ 0/1 command-01 | 612                                         |
| [mixedCase-flake](https://github.com/gh-owner/mixedCase-flake)       | 1/1                                           | 1/1                                           | 1/1                                           | 1/1                                           | n/a              | 45                                          |
| [web-app](https://github.com/gh-owner/web-app)                       | ✗ unreadable .claude/skills/skill-01/SKILL.md | ✗ unreadable .claude/skills/skill-01/SKILL.md | ✗ unreadable .claude/skills/skill-01/SKILL.md | ✗ unreadable .claude/skills/skill-01/SKILL.md | n/a              | unreadable .claude/skills/skill-01/SKILL.md |

### Hooks

| Repository                                                           | Events                             | Executable                              | Plugin root | Timeout                                 | Matcher                                 |
| -------------------------------------------------------------------- | ---------------------------------- | --------------------------------------- | ----------- | --------------------------------------- | --------------------------------------- |
| [github-repos-audit](https://github.com/gh-owner/github-repos-audit) | 2/2                                | ✗ 1/2 .claude/settings.json PostToolUse | n/a         | ✗ 1/2 .claude/settings.json PostToolUse | ✗ 0/1 .claude/settings.json PostToolUse |
| [mixedCase-flake](https://github.com/gh-owner/mixedCase-flake)       | 1/1                                | 1/1                                     | 1/1         | 1/1                                     | 1/1                                     |
| [web-app](https://github.com/gh-owner/web-app)                       | ✗ unreadable .claude/settings.json | ✗ unreadable .claude/settings.json      | n/a         | ✗ unreadable .claude/settings.json      | ✗ unreadable .claude/settings.json      |

### Plugin

| Repository                                                           | Manifest                                | No own marketplace                      | Releases                                | Version = tag                           | Listed | Pin valid | Pin current       | Entries valid    | Complete     |
| -------------------------------------------------------------------- | --------------------------------------- | --------------------------------------- | --------------------------------------- | --------------------------------------- | ------ | --------- | ----------------- | ---------------- | ------------ |
| [cipher-lib](https://github.com/gh-owner/cipher-lib)                 | ✓                                       | ✓                                       | ✓                                       | 1.2.0                                   | ✓      | v1.1.0    | ✗ v1.1.0 → v1.2.0 | n/a              | n/a          |
| [github-repos-audit](https://github.com/gh-owner/github-repos-audit) | ✗ homepage, license                     | ✗                                       | ✓                                       | ✗ 1.1.0 < v1.2.0                        | n/a    | n/a       | n/a               | ✗ 2/3 skill-pack | ✗ plugin-kit |
| [mixedCase-flake](https://github.com/gh-owner/mixedCase-flake)       | ✓                                       | ✓                                       | ✓                                       | 1.2.0                                   | ✓      | v1.2.0    | v1.2.0            | n/a              | n/a          |
| [plugin-kit](https://github.com/gh-owner/plugin-kit)                 | ✓                                       | ✓                                       | ✓                                       | 1.0.0                                   | ✗      | n/a       | n/a               | n/a              | n/a          |
| [web-app](https://github.com/gh-owner/web-app)                       | ✗ unreadable .claude-plugin/plugin.json | ✗ unreadable .claude-plugin/plugin.json | ✗ unreadable .claude-plugin/plugin.json | ✗ unreadable .claude-plugin/plugin.json | n/a    | n/a       | n/a               | n/a              | n/a          |

## Nix

| Repository                                                           | Lock age | Lock refresh | Nix cache |
| -------------------------------------------------------------------- | -------- | ------------ | --------- |
| [cipher-lib](https://github.com/gh-owner/cipher-lib)                 | n/a      | n/a          | n/a       |
| [github-repos-audit](https://github.com/gh-owner/github-repos-audit) | ✗ 46d    | ✓ Renovate   | ✗ 2.2 GB  |
| [go-linter](https://github.com/gh-owner/go-linter)                   | n/a      | n/a          | n/a       |
| [mixedCase-flake](https://github.com/gh-owner/mixedCase-flake)       | ✓ 3d     | ✗            | ✓ 206 MB  |
| [plugin-kit](https://github.com/gh-owner/plugin-kit)                 | ✓ 3d     | ✓ Renovate   | n/a       |
| [web-app](https://github.com/gh-owner/web-app)                       | n/a      | n/a          | n/a       |

## CI efficiency

| Repository                                                           | Go cache        | Cache use | Cancel superseded | Timeouts | Artifacts   |
| -------------------------------------------------------------------- | --------------- | --------- | ----------------- | -------- | ----------- |
| [cipher-lib](https://github.com/gh-owner/cipher-lib)                 | n/a             | n/a       | n/a               | n/a      | 0           |
| [github-repos-audit](https://github.com/gh-owner/github-repos-audit) | ✗ empty (430 B) | ✓ 2.3 GB  | ✗ 2 workflows     | ✗ 1 job  | 0           |
| [go-linter](https://github.com/gh-owner/go-linter)                   | n/a             | n/a       | n/a               | n/a      | 0           |
| [mixedCase-flake](https://github.com/gh-owner/mixedCase-flake)       | ✓ 95 MB         | ✓ 481 MB  | ✓                 | ✓        | 12 · 206 MB |
| [plugin-kit](https://github.com/gh-owner/plugin-kit)                 | n/a             | n/a       | ✓                 | ✓        | 0           |
| [web-app](https://github.com/gh-owner/web-app)                       | n/a             | n/a       | ✓                 | ✓        | 0           |

### Workflows without cancel-in-progress

- **github-repos-audit** — `.github/workflows/ci.yml`, `.github/workflows/workflow-02.yml`

### Jobs without timeouts

- **github-repos-audit** — `workflow-02.yml / lint`

### Unrecognised caches

- **github-repos-audit** — `custom-1e2f` (2 MB, main), `custom-5c6d` (3 MB, PR 12)

### Action-owned caches

- **github-repos-audit** — `trivy-3a4b` (61 MB, main)

### Pull-request caches

- **mixedCase-flake** — 1 cache · 900 B

## Policy

| Repository                                                           | Direct push | Signed | Identity  | CI workflows | Required checks | PR CI | Push CI | Up to date | Tag ruleset  | Secret scanning | Vuln. reporting |
| -------------------------------------------------------------------- | ----------- | ------ | --------- | ------------ | --------------- | ----- | ------- | ---------- | ------------ | --------------- | --------------- |
| [cipher-lib](https://github.com/gh-owner/cipher-lib)                 | ✗ blocked   | ✓      | n/a       | n/a          | 2               | n/a   | n/a     | ✓          | default      | ✗               | n/a             |
| [github-repos-audit](https://github.com/gh-owner/github-repos-audit) | ✓ blocked   | ✓      | ✗ 1 wrong | ✓            | ✗               | ✓     | ✗       | ✓          | default      | n/a             | n/a             |
| [go-linter](https://github.com/gh-owner/go-linter)                   | ✗ allowed   | n/a    | n/a       | ✗            | n/a             | ✗     | ✗       | n/a        | ✗            | n/a             | n/a             |
| [mixedCase-flake](https://github.com/gh-owner/mixedCase-flake)       | ✓ blocked   | ✓      | ✗ 1 wrong | ✓            | 2               | ✓     | ✓       | ✓          | `ruleset-02` | ✓               | ✓               |
| [plugin-kit](https://github.com/gh-owner/plugin-kit)                 | ✓ blocked   | ✓      | ✓         | ✓            | 2               | ✓     | ✓       | ✓          | default      | ✓               | ✓               |
| [web-app](https://github.com/gh-owner/web-app)                       | ✓ blocked   | ✓      | ✓         | ✓            | 2               | ✓     | ✓       | ✓          | default      | n/a             | n/a             |

## Activity

| Repository                                                           | Last push | CI      | Open PRs | Branches | Releases | Last release |
| -------------------------------------------------------------------- | --------- | ------- | -------- | -------- | -------- | ------------ |
| [cipher-lib](https://github.com/gh-owner/cipher-lib)                 | 11d       | n/a     | 0        | 0        | 2        | 9d           |
| [github-repos-audit](https://github.com/gh-owner/github-repos-audit) | 11d       | PENDING | 0        | 3        | 2        | n/a          |
| [go-linter](https://github.com/gh-owner/go-linter)                   | never     | ✗       | 0        | 0        | 0        | n/a          |
| [mixedCase-flake](https://github.com/gh-owner/mixedCase-flake)       | 11d       | SUCCESS | 0        | 0        | 3        | 5mo          |
| [plugin-kit](https://github.com/gh-owner/plugin-kit)                 | 11d       | SUCCESS | 0        | 0        | 1        | 32d          |
| [web-app](https://github.com/gh-owner/web-app)                       | 11d       | SUCCESS | 0        | 0        | 0        | n/a          |

## CI timing

| Repository                                                           | PR CI | Main CI | Slowest job  | Slowest job mean |
| -------------------------------------------------------------------- | ----- | ------- | ------------ | ---------------- |
| [mixedCase-flake](https://github.com/gh-owner/mixedCase-flake)       | 9m50s | 5m00s   | `ci / check` | 5m00s            |
| [github-repos-audit](https://github.com/gh-owner/github-repos-audit) | n/a   | 1h05m   | `ci / test`  | 1h03m            |

## Git identities

- **github-repos-audit** — `Pat Example <pat@example.org>` — not canonical
- **mixedCase-flake** — `Pat Example <pat@example.com>` (canonical), `Pat Example <12345+pat@users.noreply.github.com>` (accepted), `Patrick Example <patrick@example.org>` — mixed
- **plugin-kit** — `Pat Example <pat@example.com>` (canonical)
- **web-app** — `Pat Example <pat@example.com>` (canonical), `Pat Example <12345+pat@users.noreply.github.com>` (accepted)

## Settings exceptions

- **cipher-lib** — secret-scanning push protection `disabled`, usually `enabled`
- **mixedCase-flake** — projects `enabled`, usually `disabled`
- **web-app** — wiki `enabled`, usually `disabled`

## Overrides

- **web-app** — `flake_nix` is `not_required`

_Read from GitHub on 2026-09-05._
