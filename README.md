# github-repos-audit

> **A personal tool.** I wrote this to audit my own GitHub account against my
> own standard, and it is not intended for general use. It is unsupported and
> may change or break in any release. I don't expect issues or pull requests
> from others and may close them without response. The license lets you fork
> it if it happens to be useful.

A Go tool that reads live GitHub for every repository an account owns that is
neither a fork nor archived, judges each one against a standard declared in
`repos.toml`, and writes a report: a Markdown file for people and a JSON file
for anything that wants to diff it.

The tool is read-only. Every call it makes is a `GET` request or a single
GraphQL `query`; it never issues a GraphQL `mutation` or a write-verb REST
call. All of that reading happens in `github-repos-audit render` (or `github-repos-audit json`, which
prints the same snapshot to stdout instead). Writing the report back to a
repository — creating a branch, committing to it, opening a pull request and
merging it once its checks are green — happens entirely inside the reusable
workflow described below, never in the tool's own code path.

Both reading commands take their token from `GITHUB_TOKEN` and, when it is
unset, from `gh auth token --hostname github.com`, the one subprocess the tool
ever starts. `github-repos-audit validate` checks `repos.toml` offline, with no
token; with `--audit <path>` it also checks an `audit.json` against the schema
embedded in the binary. `github-repos-audit --help` lists the flags
(`--config`, `--audit`, `--dir`, `--verbose`, `--debug`) and
`github-repos-audit --version` prints the version. The exit codes are part of
the interface:

- `0` — success.
- `1` — runtime failure: an unexpected API response, a malformed
  `repos.toml`, an `audit.json` that does not match its schema, or a
  `repos.toml` naming no repository the account has. The invocation was fine;
  something else was not.
- `2` — usage error: an unknown subcommand, a flag that does not parse, or a
  missing or invalid argument.
- `130` — interrupted by SIGINT or SIGTERM.

## Setting up the report repository

The tool runs from a separate private repository that holds the configuration
and the generated report. These steps describe how that repository is wired up.

1. Create a private repository to hold the configuration and report. This
   guide calls it the report repository; the tool never needs write access to
   anything else.

2. Copy `examples/repos.toml` from this repository into the report repository
   as `repos.toml`, then replace every `[repos.<name>]` table with your own
   repositories. The config and the account are meant to name the same
   repositories: every repository the account has needs an entry, and every
   entry needs a repository the account has. Where they disagree,
   `github-repos-audit render` reports on the repositories both name and opens the report
   with a **Configuration drift** section listing the repositories that need
   an entry and the entries that name no repository — deleted, archived or
   turned into a fork. The same two lists are in `audit.json` under
   `config_drift`, and the run logs each as a warning. Only a `repos.toml`
   that names no repository the account has stops the run. See the reference
   below for what the file can say.

   Give the report repository a `README.md` that carries these two lines,
   in this order:

   ```markdown
   <!-- BEGIN GENERATED: audit -->
   <!-- END GENERATED: audit -->
   ```

   `github-repos-audit render` rewrites only the text between them and leaves the rest of
   the file alone; a README with either marker missing, repeated or out of
   order is refused before anything is written.

3. Add two workflow files to the report repository's `.github/workflows/`,
   each calling one of this repository's reusable workflows at a pinned
   commit. Pin the `uses:` line to a released tag's commit SHA, not to a
   branch name, so a change here cannot alter what runs in your repository
   without your say.

   The reusable workflows run the release binary built from the pinned
   commit. Each run downloads it, checks it against the release's
   `SHA256SUMS`, and verifies that its build provenance names this
   repository's release workflow on `main` and that exact commit. A run fails
   if any of that fails, including when the attestation service is
   unreachable. The pin must therefore be a release's commit: a commit between
   releases fails with a message saying so.

   Every caller job below sets `permissions: contents: read` explicitly,
   because a called workflow's own `permissions: {}` only caps what it can be
   granted — the caller job still starts from its repository's default token
   permissions unless it says otherwise. The reusable workflows themselves
   declare no `concurrency:` — inside a called workflow, `github.workflow` is
   the _caller's_ workflow name, so a concurrency group keyed on it there
   would collide with whatever else in your repository shares that name, and
   you set it in the caller instead, where the name is unambiguous.

   The SHA in each `uses:` line below is a placeholder, not a real commit.
   The commit SHA and the version comment to pin are in the latest release's
   notes on the Releases page.

   ```yaml
   # .github/workflows/audit.yml — daily report refresh. Every run opens a
   # pull request (or updates the existing one) and enables auto-merge, so
   # the report lands once its checks pass.
   name: audit
   on:
     schedule:
       - cron: "17 6 * * *"
     workflow_dispatch: {}
   # Keyed on the workflow rather than the ref, and never cancelled: a refresh
   # in flight must finish and commit before the next one resets the branch,
   # or two runs can race on chore/refresh-audit.
   concurrency:
     group: ${{ github.workflow }}
   jobs:
     refresh:
       uses: rvenutolo/github-repos-audit/.github/workflows/audit.yml@0123456789abcdef0123456789abcdef01234567 # vX.Y.Z
       permissions:
         contents: read
       secrets:
         AUDIT_TOKEN: ${{ secrets.AUDIT_TOKEN }}
         REFRESH_TOKEN: ${{ secrets.REFRESH_TOKEN }}
   ```

   ```yaml
   # .github/workflows/validate.yml — checks repos.toml and, when it exists,
   # audit.json against its schema on every pull request, without changing
   # anything.
   name: validate
   on: pull_request
   # A superseded push should not keep an old validation run alive: cancel it
   # in favour of the new one, keyed per pull-request ref.
   concurrency:
     group: ${{ github.workflow }}-${{ github.ref }}
     cancel-in-progress: true
   jobs:
     validate:
       uses: rvenutolo/github-repos-audit/.github/workflows/validate.yml@0123456789abcdef0123456789abcdef01234567 # vX.Y.Z
       permissions:
         contents: read
   ```

4. Set two repository secrets on the report repository:
   - `AUDIT_TOKEN` — a token that can read every repository on the account:
     contents, metadata, actions, administration, and pull requests, all
     read-only. Without pull requests the open pull request count of a private
     repository is unreadable, and actions is what the CI timing is read
     from. The workflow hands it to `github-repos-audit render` as `GITHUB_TOKEN`, and inside the
     workflow it is the only token the tool's own code ever sees (a local run
     without `GITHUB_TOKEN` falls back to whatever `gh auth token` holds).
     `github-repos-audit validate` is offline and needs no token at all, whether
     it checks `repos.toml` alone or `audit.json` too. It cannot write
     anything, on this repository or any other.
   - `REFRESH_TOKEN` — a token scoped to the report repository alone, with
     Contents and Pull requests write access. It is used only by the refresh
     workflow, after `github-repos-audit render` has finished, to commit the regenerated
     report to a branch, open (or update) the pull request and enable
     auto-merge on it — or merge it outright when every check has already
     reported — so it is the token that merges.

## `repos.toml` reference

`repos.toml` is the only file you hand-maintain. It has four kinds of table,
and needs at least one `[types.<name>]` and one `[repos.<name>]`. A key the
tool does not know, anywhere in the file, is an error rather than ignored, so
a misspelling such as `publish = true` cannot pass unnoticed.

`[types.<name>]` defines a category of repository and what it expects. A type
lists every check the tool knows, each set to one of:

| Word               | Meaning                                                   |
| ------------------ | --------------------------------------------------------- |
| `required`         | expected                                                  |
| `not_required`     | does not apply and never counts as a gap                  |
| `public`           | expected only when the repository is public               |
| `public_published` | expected only when the repository is public and published |
| `info`             | shown, never judged                                       |

`direct_push` takes `blocked` or `allowed` instead, and the five value-only
checks — `last_release_age`, `last_push`, `open_prs`, `branches` and
`artifact_storage` — take only `info` or `not_required`. `renovate_min_release_age` takes a duration
instead — `"7 days"`, `"48 hours"`, `"1 week"` (a positive whole number with
no leading zero, a single space, then minute, hour, day or week, singular or
plural) — or `not_required` or `info`; an
override may give it a different duration. A type that leaves a check out is
an error, so a check added in a later version of the tool is decided for every
type before `github-repos-audit validate` passes. Type names use lowercase letters, digits,
`_` and `-`, and a type no repository uses is allowed.

Each `[repos.<name>]` entry declares a `type`, and optionally `published` and
per-repository `overrides`. `<name>` is the repository's name alone, without
the owner, and has to be one GitHub could have: 1 to 100 letters, digits, `.`,
`_` and `-`, and not `.` or `..`. Anything else is a typo no repository will
ever match, and `github-repos-audit validate` rejects it. Visibility is never declared; it
is always read live from GitHub.

`[identity]` declares your git identity, for the `git_identity` check:

```toml
[identity]
canonical = "Pat Example <pat@example.com>"
accepted = ["Pat Example <12345+pat@users.noreply.github.com>"]
match = " Example <"
```

- `canonical` is the one identity your commits should carry, written
  `Name <email>` the way git prints it.
- `match` is a Go (RE2) regular expression, unanchored, tested against each
  commit author's and committer's `Name <email>`; whatever it matches is
  yours. A plain word means "contains": `'(?i)yourname'` matches your name in
  any case, anywhere, and `' Yourname <'` matches a name whose last word is
  Yourname (the leading space rules out a one-word name). It can
  match on the address instead, which survives a change of name:
  `'@yourdomain\.example>$'`. `canonical` must itself match.
- `accepted` is optional: identities of yours, each written `Name <email>`,
  that are fine to find in a history although they are not canonical —
  typically GitHub's noreply address for your account, which GitHub records
  on every merge and edit made in its web UI, so it comes back however often
  history is rewritten. Each is compared like `canonical` (the address without
  regard to case). An entry `match` does not match, one that is `canonical`
  itself, a repeated one, and one not written `Name <email>` are errors; an
  entry no repository carries is not, since the next web-UI merge may need it.

The table is required once any type or override judges `git_identity` (with
any word but `not_required`), and refused when nothing does, for the same
reason a dead override is.

`[hermetic]` declares the hermetic-lint standard, for the `hermetic_nix`
check:

```toml
[hermetic]
flake = "github:gh-owner/lint-flake"
lint = "check-workflow-hermetic"
```

- `flake` is the flake that provides the lint, written as `flake.nix` writes
  the input's `url`. Only a `github:<owner>/<repo>` reference can be found in
  `flake.lock`, since the tool records only flake.lock's GitHub-type nodes;
  any other kind (`git+https:`, say) is always reported `not locked`.
- `lint` is the lint's command name, looked for in `flake.nix` and the gate
  files.

Both keys are required and must not be empty. Like `[identity]`, the table is
required once any type or override judges `hermetic_nix`, and refused when
nothing does.

An override that names an unknown check is an error, and so is an override
that agrees with what the repository's type already derives — a dead
override is deleted rather than left to rot. `required` is never accepted on
a value-only check, and an override with an empty value is an error.
`examples/repos.invalid.toml` is a worked example of the
dead-override error: it is a copy of `examples/repos.toml` with one override
restated — and with `git_identity` judged by no type, so it needs no
`[identity]` table (neither file judges `hermetic_nix`, so neither has a
`[hermetic]` table) — and `github-repos-audit validate` is expected to reject
it.

## The checks

Grouped by theme here; the report lists them in the fixed worklist order
`internal/rules/check.go` declares, most consequential first.

- **readme**, **description**, **gitignore**, **editorconfig** — whether the
  file or metadata field exists at all.
- **license** — whether the repository has a license GitHub can identify; a
  license file GitHub cannot recognise (`NOASSERTION`) fails.
- **ci_workflows**, **required_checks**, **ci_green** — whether
  `.github/workflows` has anything but the always-green `merge-gate.yml`
  placeholder in it, whether the default branch has any required status
  check other than the `merge-gate` placeholder (n/a wherever a direct push
  is allowed, since there is no gate to require anything), and whether the
  head commit's checks last reported success.
- **renovate**, **flake_nix** — whether the corresponding tooling file
  exists. The Renovate file is looked for at every path Renovate itself
  reads, in Renovate's order, and the first one found is the one reported and
  judged, as it is the one Renovate uses. The deprecated `renovate` key in
  `package.json` is not read.
- **justfile** — whether `justfile` or `.justfile` exists at the root, in any
  letter case, as `just` itself matches it; the path is reported as the
  repository spells it.
- **renovate_min_release_age** — Renovate's effective `minimumReleaseAge`,
  held to the type's duration. The value is read from the repository's own
  Renovate file merged with any presets it extends from the same account;
  Renovate's built-in presets and other accounts' presets are not read, so a
  value that only comes from one of those shows as `none`. Shows `unresolved`
  when a preset is missing or does not parse, and `audit.json`'s
  `min_release_age_error` says why; it also shows `unresolved` for a value
  Renovate itself cannot read (say `"7 dayz"`), which `audit.json` records
  verbatim as `min_release_age`. n/a without a Renovate configuration, which
  the renovate row already reports.
- **homepage**, **topics**, **community_files** — repository metadata that
  only matters once a repository is meant to be found: a homepage URL, any
  topics, and how many of a security policy, contributing guide and code of
  conduct are present.
- **releases**, **changelog** — whether the repository has published
  releases and keeps a changelog.
- **license_file** — the license text, a root file matched in any letter
  case, in this order: bare `LICENSE`, `LICENCE` or `UNLICENSE`; the same
  with a `.md`, `.markdown`, `.txt` or `.html` extension; `COPYING`;
  `COPYING.<extension>`; the same three stems with any other extension but
  `.spdx` and `.header`; `LICENSE-*`, `LICENSE_*`, `LICENCE-*` or
  `LICENCE_*`; `COPYING-*`; and last the REUSE `LICENSES/` directory. Unlike
  `license`, which asks GitHub which license it recognised, this asks where
  the text lives.
- **security_policy** — `.github/SECURITY.md`, `SECURITY.md` or
  `docs/SECURITY.md`, in any letter case.
- **codeowners** — `.github/CODEOWNERS`, `CODEOWNERS` or `docs/CODEOWNERS`.
- **pr_template** — a file named `pull_request_template`, with any extension
  or none, or a directory of that name, in `.github/`, the root or `docs/`, in
  any letter case.
- **claude_md** — `CLAUDE.md` or `.claude/CLAUDE.md`.
- **commitlint** — `.commitlintrc`, `.commitlintrc.json`, `.commitlintrc.yaml`,
  `.commitlintrc.yml`, `.commitlintrc.js`, `.commitlintrc.cjs`,
  `.commitlintrc.mjs`, `commitlint.config.js`, `commitlint.config.cjs`,
  `commitlint.config.mjs`, `.commitlintrc.ts`, `.commitlintrc.cts`,
  `.commitlintrc.mts`, `commitlint.config.ts`, `commitlint.config.cts` or
  `commitlint.config.mts`.
- **git_cliff** — `cliff.toml`, `.cliff.toml` or `.config/cliff.toml`.
- **treefmt** — `treefmt.toml`, `.treefmt.toml`, `.config/treefmt.toml`,
  `treefmt.nix` or `.treefmt.nix`.
- **compose** — every distinct file name among `compose.yaml`, `compose.yml`,
  `docker-compose.yml` and `docker-compose.yaml`, found at any depth, because
  compose files live one per stack. The cell lists the names, sorted.
- **typos** — `typos.toml`, `_typos.toml` or `.typos.toml`.
- **yamllint** — `.yamllint`, `.yamllint.yaml` or `.yamllint.yml`.
- **markdownlint** — `.markdownlint-cli2.jsonc`, `.markdownlint-cli2.yaml`,
  `.markdownlint-cli2.cjs`, `.markdownlint-cli2.mjs`, `.markdownlint.jsonc`,
  `.markdownlint.json`, `.markdownlint.yaml`, `.markdownlint.yml`,
  `.markdownlint.cjs`, `.markdownlint.mjs` or `.markdownlintrc`.
- **prettier** — `.prettierrc`, `.prettierrc.json`, `.prettierrc.yml`,
  `.prettierrc.yaml`, `.prettierrc.json5`, `.prettierrc.js`,
  `prettier.config.js`, `.prettierrc.ts`, `prettier.config.ts`,
  `.prettierrc.mjs`, `prettier.config.mjs`, `.prettierrc.mts`,
  `prettier.config.mts`, `.prettierrc.cjs`, `prettier.config.cjs`,
  `.prettierrc.cts`, `prettier.config.cts` or `.prettierrc.toml`.
- **editorconfig_checker** — `.editorconfig-checker.json`, the only name the
  tool's source reads.
- **lychee** — `lychee.toml`.
- **zizmor** — `.github/zizmor.yml`, `.github/zizmor.yaml`, `zizmor.yml` or
  `zizmor.yaml`.
- **codecov** — `codecov.yml`, `.codecov.yml`, `codecov.yaml` or
  `.codecov.yaml`, in the root, `dev/` or `.github/`.
- **mkdocs** — `mkdocs.yml` or `mkdocs.yaml`.
- **signed_commits**, **tag_ruleset**, **direct_push** — whether commit
  signing is required, whether tags are protected by an active ruleset
  targeting tags, and whether a push straight to the default branch is
  possible at all.
- **git_identity** — every author and committer on the default branch's full
  history whose `Name <email>` matches `[identity].match` must be
  `[identity].canonical` or one of `[identity].accepted`, the address compared
  without regard to case. Fails with the number of wrong identities — those
  that are neither; a history that always used a wrong one fails as surely as
  one that switched. n/a for an empty repository, or one with none of your
  commits at all. Other people's and bots' identities are recorded in
  `audit.json` but never judged. The Git identities section of the report
  lists, for every repository with any of your identities, which ones its
  history carries, tagging the canonical and accepted ones and marking a
  history mixed (two or more identities that are canonical or wrong) or not
  canonical (any wrong one) — overridden repositories included, since it is
  the list to rewrite from. Reading the history costs one extra request per
  100 commits beyond the first hundred. Web-UI commits carry your noreply
  identity; see `accepted` above.
- **secret_scanning**, **vuln_reporting** — the two security features
  GitHub can enable on a repository.
- **hermetic_nix** — whether the repository runs the hermetic lint named in
  `[hermetic]`. It passes when all of these hold, and otherwise names each
  one that does not: `flake.nix` declares an input whose `url` is
  `[hermetic].flake` exactly (`no input`); `flake.lock` has a node for it,
  matched on `github:<owner>/<repo>` without regard to case, so a `git+https:`
  or other non-GitHub input is never found (`not locked`); `flake.nix`
  mentions `[hermetic].lint`, a text stand-in for the default devShell
  carrying it, since the tool never evaluates Nix (`not in devShell`); and
  `run-all-checks`, a file directly under `.ci/`, or the `justfile` mentions
  it (`not in gate`). The gate part shows the wiring exists, not that the
  call is reachable. A `flake.nix` the tool could not read records nothing,
  so it reads as `no input` and `not in devShell`. n/a without a `flake.nix`,
  which the flake_nix row already reports. The report's Nix section shows it.
- **nix_lock_age** — how long ago `flake.lock` locked nixpkgs: the
  `lastModified` of the node the root's `nixpkgs` input resolves to,
  following `follows`, so a lock that names it `nixpkgs_2` is still found.
  It passes at 30 days or less, measured to the second against the moment
  the audit was read, and the cell shows the age beside its mark (`✓ 4d`,
  `✗ 47d`). A `flake.lock` the tool could not read fails as
  `unreadable flake.lock`. n/a without a `flake.lock`, or when the root has
  no input named `nixpkgs`. The report's Nix section shows it.
- **nix_lock_refresh** — whether something refreshes `flake.lock` without a
  person remembering to, and the cell names each source that does
  (`✓ Renovate`, `✓ workflow`). Renovate counts when there is a Renovate
  configuration, its `nix` manager runs, and either `lockFileMaintenance` is
  enabled or every `flake.lock` input is pinned by `rev` (the manager alone
  then moves each digest), all read from the effective configuration as
  `renovate_min_release_age` is. A workflow counts when it runs on
  `schedule` and a job runs `nix flake update`, uses
  `DeterminateSystems/update-flake-lock`, or uses a local composite action
  (`./` or `$/`) that runs `nix flake update`. With neither, it fails; a
  workflow the tool could not read, or an unreadable composite a scheduled
  workflow uses, is then named as `unreadable <path>`, since it may be the
  refresh the tool cannot see. n/a without a `flake.lock`. The report's Nix
  section shows it.
- **nix_cache** — whether the default branch's Nix store is cached at a
  useful size by a `nix-community/cache-nix-action` set up to stay bounded.
  Caches are grouped into families by the first two `-`-separated segments of
  their key (`nix-Linux`, `nix-macOS`), counting only caches saved from the
  default branch, and the newest entry of each family must hold between
  1 MB and 1.5 GB (decimal units). The cell shows the size of the first
  family out of range, or else the largest (`✓ 206 MB`, `✗ 2.2 GB`,
  `✗ empty (430 B)`), or `none on main`. Every `cache-nix-action` step a job
  runs, directly or through a local composite action, is then read, and the
  cell names each problem once: `no gc-max-store-size` (none of
  `gc-max-store-size`, `-linux` or `-macos`), `no purge` (`purge` is not
  `true`), `no purge selector` (none of `purge-prefixes`, `purge-created`,
  `purge-last-accessed` or `purge-primary-key`), and `no actions: write`
  (the job's `permissions:`, else the workflow's, else the repository's
  default workflow permissions, do not grant it; `write-all` does). A
  workflow, or a composite a job uses, that the tool could not read is
  named as `unreadable <path>`. It passes only with every size in range and
  no problem. n/a without a `flake.nix`, or when no workflow has run on the
  default branch in the last 7 days, since GitHub evicts a cache nothing has
  restored for that long. The report's Nix section shows it.
- **go_cache** — whether the default branch's Go cache holds something.
  Caches whose key starts `go-` are grouped into families as for nix_cache
  (`go-Linux`, `go-macOS`), counting only caches saved from the default
  branch, and the newest entry of each family must hold at least 1 MB
  (decimal units): the newest is the one the next run restores, so a job
  that built nothing but saved last fails it however full the older entries
  are. There is no upper bound. The cell shows the first family below the
  floor (`✗ empty (430 B)`), or else the largest (`✓ 206 MB`), or
  `none on main`. n/a without a root `go.mod`, or when no workflow has run
  on the default branch in the last 7 days. The report's CI efficiency
  section shows it.
- **cache_usage** — whether the repository's Actions caches stay at or
  below 80% of the cache storage limit GitHub reports for it. Past the limit
  GitHub evicts the entries restored longest ago to make room for each new
  save, so caches push each other out and runs miss the caches they
  expected. The cell shows the usage in decimal units (`✓ 2.3 GB`); the
  comparison is exact, so a byte over 80% fails even where the size reads
  the same. n/a when no limit was read, as for an empty repository. The
  report's CI efficiency section shows it.
- **ci_cancel_superseded** — whether every workflow triggered by
  `pull_request` or `pull_request_target` cancels the run a newer push to
  the same pull request supersedes, so a run whose result nobody will read
  does not hold a runner. A workflow passes with a workflow-level
  `concurrency` that sets `cancel-in-progress`, or when every one of its
  jobs sets it in a job-level `concurrency`; an expression counts as set,
  a literal `false` does not. A workflow file that does not parse fails it
  as unreadable. The cell counts the workflows that fail
  (`✗ 2 workflows`), and the report's CI efficiency section lists them per
  repository under _Workflows without cancel-in-progress_, for every
  repository whose cell fails or, under `info`, shows a count; a
  `not_required` repository is never listed. n/a when no workflow is
  triggered by a pull request.
- **job_timeouts** — whether every workflow job sets `timeout-minutes`, so a
  job that hangs is stopped well before GitHub's six-hour default rather
  than holding its runner, and every run queued behind it, for that long.
  An expression counts as set. A job that calls a reusable workflow (a
  job-level `uses:`) is left out, since GitHub does not accept the setting
  there and the called workflow's own jobs carry it. A workflow file that
  does not parse fails it as unreadable. The cell counts the jobs that fail
  (`✗ 3 jobs`), and the report's CI efficiency section lists them per
  repository under _Jobs without timeouts_, as `<workflow file> / <job id>`
  (`ci.yml / build`), for every repository whose cell fails or, under
  `info`, shows a count; a `not_required` repository is never listed. n/a
  when no workflow holds a job of its own.
- **artifact_storage** — a plain fact, always shown and never judged: how
  many unexpired Actions artifacts the repository holds and their total
  size in decimal units (`12 · 206 MB`), or `0` with none. Artifacts share
  the account's Actions storage with every other repository, so the figure
  sits beside the caches in the report's CI efficiency section. It takes
  only `info` or `not_required`.
- **last_release_age**, **last_push**, **open_prs**, **branches** — plain
  facts, always shown and never judged: how long since the last release and
  the last push, how many pull requests are open, and how many branches
  exist besides the default branch (`audit.json` records the raw count).

These file-path checks, with `justfile` and `renovate`, take any of the
expectation words, report the path they found as code, and keep showing it
where the type does not ask for the file. Where several accepted names are
present, the first in the order listed for that check is the one reported. A name
with a directory is a repository-relative path, and a directory match ends
in `/`. The File paths section of the report marks a path that differs from
the one most of the account uses for the same check; with no single most
common path, nothing is marked.

## Cache lists

Under its table, after the lists behind the ci_cancel_superseded and
job_timeouts counts, the report's CI efficiency section lists the Actions
caches no check judges. Like `artifact_storage` they are plain facts: every
repository with an entry is listed, whatever its cells say, one bullet per
repository in report order, and a list with no entries is left out.

- **Unrecognised caches** — caches under a key that starts with neither
  `go-` nor `nix-` and is not action-owned, saved from any ref, each as
  `` `key` (size) `` in decimal units, sorted by key. Nothing checks that
  such a cache holds anything or stays bounded, and it shares the cache
  storage limit with the caches that are checked.
- **Action-owned caches** — caches common actions save for themselves,
  under a key starting `harden-runner-cacheKey`,
  `determinatesystem-nix-installer-`, `gitleaks-`, `cache-trivy-` or
  `trivy-`, listed the same way. Their size is the action's doing, not the workflow's.
- **Pull-request caches** — how many `go-` and `nix-` caches pull-request
  refs saved, and their total size (`3 caches · 302 MB`). A pull request's
  cache never serves the default branch, which is why go_cache and
  nix_cache leave it out, but it counts against the same storage limit
  until GitHub evicts it.

## CI timing

The CI timing section of the report shows how long CI takes, one row per
repository with at least one finished sample. Rows with a PR sample come
first, slowest PR CI first, followed by the rows with no PR sample, slowest
Main CI first. Like `last_push` and `open_prs` it is a plain fact: shown,
never judged, and the raw per-commit facts are in `audit.json` under `ci`.

- **PR CI** — the mean wait over the head commits of the repository's last 5
  merged pull requests whose workflow runs all finished. Pull requests opened by a
  bot (a dependency bump, say) are left out: they run a narrower set of
  checks than the work a person waits on. `n/a` when no pull request
  qualifies.
- **Main CI** — the mean wait over the last 5 default-branch commits whose
  workflow runs all finished. `n/a` when none did.
- **Slowest job** and **Slowest job mean** — the job, shown as
  `workflow / job`, with the highest mean duration across those same runs.
  A job that took 0 seconds was skipped and is not averaged in.

Only GitHub Actions workflow runs are timed; a check another app posts
(Codecov, say) does not count. Only runs the change itself triggered count:
`pull_request`, `pull_request_target` and `push` runs for a pull request's
head commit, and `push` and `merge_group` runs for a default-branch commit.
Runs of any other event (`schedule`, `workflow_dispatch`, `workflow_run` and
the like) are ignored, so a nightly run on the head commit does not stretch
its wait, and a commit with only such runs reads as having no CI. The wait runs from the first run's creation
until every job has finished, so time spent queued for a runner counts,
parallel workflows are counted once, and a re-run counts toward the wait. A
commit whose runs are still going, or one of whose jobs lists was too long to
read in full, is skipped rather than counted short, and so is a commit older
than the repository's latest 100 workflow runs, which are all that are read.
A mean with fewer than 5 samples averages what exists. Durations read `45s`,
`9m40s` and `1h05m`; from an hour up the seconds are dropped.

## Running locally

Three ways to get an `github-repos-audit` binary:

- **`go install`**, which needs Go:
  `go install github.com/rvenutolo/github-repos-audit/cmd/github-repos-audit@latest`.
- **A prebuilt binary** from the repository's Releases page. Each release
  carries one binary per platform, named `github-repos-audit-<os>-<arch>`
  (`github-repos-audit-linux-amd64`, `github-repos-audit-linux-arm64`, `github-repos-audit-darwin-amd64`,
  `github-repos-audit-darwin-arm64` and `github-repos-audit-windows-amd64.exe`), a CycloneDX SBOM
  `github-repos-audit.cdx.json`, the checksum list `SHA256SUMS` and its signature
  `SHA256SUMS.sigstore.json`. On Linux and macOS, make the download
  executable first: `chmod +x github-repos-audit-<os>-<arch>`.
- **A clone**, which needs only Nix: `./.ci/in-devshell go run ./cmd/github-repos-audit`.

`github-repos-audit validate --config repos.toml` is offline and needs no
token; add `--audit audit.json` to check that file against the embedded schema
too. `github-repos-audit render` and `github-repos-audit json` read your
account from GitHub and need `GITHUB_TOKEN` in the environment, or a logged-in
`gh` (the token comes from `gh auth token`).

## Verifying a release

Download the release's files from the Releases page, or with
`gh release download <tag> --repo rvenutolo/github-repos-audit`, into one
directory, and run the commands there. `github-repos-audit.cdx.json` is the CycloneDX
SBOM (software bill of materials) for the binaries.

```sh
sha256sum --check --ignore-missing SHA256SUMS
```

This ties each downloaded file to the checksum list, so a corrupted or
swapped file fails. Stock macOS has no `sha256sum`; there, with every file
of the release downloaded, run `shasum --algorithm 256 --check SHA256SUMS`.
The list is only as trustworthy as its signature, which the next command
checks.

```sh
cosign verify-blob \
  --bundle SHA256SUMS.sigstore.json \
  --certificate-identity https://github.com/rvenutolo/github-repos-audit/.github/workflows/release.yml@refs/heads/main \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  SHA256SUMS
```

This proves `SHA256SUMS` was signed by this repository's release workflow
running on `main`: the certificate identity in the command pins that workflow
on `refs/heads/main`, so a signature made by any other workflow, or by this
one on another branch, fails. The issuer is GitHub's OIDC token service.
There is no long-lived signing key to steal. The bundle is written by
cosign 3, and the command is for cosign 3.

```sh
gh attestation verify github-repos-audit-linux-amd64 \
  --repo rvenutolo/github-repos-audit \
  --signer-workflow rvenutolo/github-repos-audit/.github/workflows/release.yml \
  --source-ref refs/heads/main \
  --source-digest <commit>
```

This proves which workflow run, on which commit, built the binary (its build
provenance). `--source-digest` adds the commit: the attestation must name the
commit the release was built from (the release's commit SHA), so a binary
built from any other commit fails. The reusable workflows run this same check.
`--signer-workflow` and `--source-ref` pin the same workflow on `main` that
the certificate identity does: the repository also holds attestations for
trial builds made from other branches, and without the two flags one of those
would pass. Run it for each binary you use.

## Versioning

Releases follow [semantic versioning](https://semver.org/). The public
interface a version number speaks for is:

- the keys and values of `repos.toml`,
- the keys of `audit.json`,
- the exit codes,
- the commands and flags of `github-repos-audit`,
- the inputs and secrets of the reusable workflows.

A **major** release makes an incompatible change to any of these. Every new
check is one, because a type in `repos.toml` that omits a check is a
configuration error, so every consumer's `repos.toml` must change. The gate
enforces this: it fails when the check list in `audit.schema.json` changes
without a breaking-change marker on some commit in the change. A **minor**
release is additive and a **patch** release is a fix. On a major bump, read
the release notes; for a new check, add its key to every type and regenerate
`audit.json`.

## Development

Every recipe below runs its tools through `.ci/in-devshell`, a hermetic
`nix develop`, so a green local run is a green CI run — except `fmt` and
`format-check` (and the `nix fmt` half of `fix`), which drive the host's `nix`
directly; `mutate`, which enters its own `nix develop .#mutate` shell;
`gremlins-bump`, which is `nix run` on the host; and `hooks`, which runs
before the devshell exists. `just check` is exactly what CI runs; nothing but
Nix is expected to be installed on the host.

| Command              | Does                                                      |
| -------------------- | --------------------------------------------------------- |
| `just check`         | the whole verification gate, exactly what CI runs         |
| `just test`          | the offline test suite, with the race detector            |
| `just lint`          | the lint suite alone (golangci-lint, actionlint, ...)     |
| `just fmt`           | format every file (gofumpt, prettier, nixfmt, ...)        |
| `just fix`           | run every auto-fixer: `nix fmt`, then `.ci/run-fixers`    |
| `just format-check`  | verify formatting without writing changes                 |
| `just cover`         | print per-package statement coverage against the baseline |
| `just cover-update`  | record current coverage as the new baseline               |
| `just validate`      | check `examples/repos.toml` alone; offline, no token      |
| `just hooks`         | point `core.hooksPath` at `.githooks`                     |
| `just smoke`         | the live suite against real GitHub; read-only             |
| `just vuln`          | report vulnerabilities with govulncheck                   |
| `just fuzz`          | fuzz every target briefly; never part of the gate         |
| `just mutate`        | mutation-test a package; slow, never part of the gate     |
| `just gremlins-bump` | bump gremlins to its latest release; needs network        |
| `just build-release` | cross-compile the release binaries, SBOM and checksums    |

`.github/workflows/smoke.yml` runs the live suite on `main` once a week. Its
token and its three `SMOKE_*` values belong to the `smoke` environment, which
only `main` may deploy to, so a run from any other branch is refused before
the token is in scope. `SMOKE_TOKEN` is a fine-grained token limited to the
two fixture repositories, with Actions, Administration, Contents, Metadata and
Pull requests read-only. It carries an expiry: once it lapses the run fails on
its first request, and the fix is a new token in the environment's secret.

`just smoke` runs the suite with your own `gh` login. To run it with another
token, call the command the recipe wraps:

```sh
GITHUB_TOKEN=<token> SMOKE_OWNER=<owner> SMOKE_PUBLIC_REPO=<repo> SMOKE_PRIVATE_REPO=<repo> \
  ./.ci/in-devshell go test -tags=integration ./internal/github/... -count=1
```

`SMOKE_OWNER` must be the account the token authenticates as; the suite fails
outright on a mismatch. The suite only reads the two repositories, but it is
not indifferent to which two they are:
`internal/github/live_integration_test.go` asserts real facts about each, and
a fixture missing one fails the run with no further explanation:

- The public repository needs a description, a README, a license file at
  the root, at least one workflow file, at least one ruleset, readable branch protection rules, a
  non-empty Actions permissions policy, a default workflow permissions
  setting, at least one allowed merge strategy, a commit history whose
  authors carry both a name and an email, a recorded last push, a
  secret-scanning status, a private-vulnerability-reporting answer, and at
  least one CI check run on its default-branch head (a workflow file alone
  is not enough; the head commit must have been checked).
- The private repository needs to genuinely be private and to have at least
  one commit, so the suite can observe the settings that only differ on a
  private repository (secret scanning and private vulnerability reporting
  both report as absent there, and the Actions access-level setting only
  exists there).

A release is proposed by `.github/workflows/release.yml`, which runs on every
push to `main`. Whenever something releasable has merged since the last
release, it holds one pull request open, titled `chore(release): vX.Y.Z`,
that changes only `CHANGELOG.md`. The version is
computed from the Conventional Commit types merged since the last tag
(breaking is major, `feat` minor, `fix`, `perf` and `revert` patch), or given
by hand through the workflow's `version` input. Merging that pull request
makes the workflow build the binaries, sign `SHA256SUMS`, attest each
binary's provenance and publish the release, which creates the tag on that
merge commit. Only the workflow run of that commit publishes it, because the
signature and the attestation name the commit their run started from; if
that run is cancelled or fails, re-run it from the Actions page. A manual
run with `dry-run` builds without tagging or releasing. Adding `sign` signs
the dry run's checksums and attests its binaries too, for a trial from a
branch other than `main`; on `main` such a run does nothing.

Coverage is held three ways, all inside `just check`. Each package must stay
at or above its figure in `.ci/coverage-baseline.txt`. Every Go block a
change touches (everything since the branch left `origin/main`) must be run
by the tests. When one can't be (a defensive branch no test can reach),
mark it with a line comment inside the block:

```go
if err != nil { // coverage-exempt: the reader never fails on a bytes.Buffer
```

A marker needs a reason and must still mark unrun code; a stale one fails.
Markers are judged in every tracked `.go` file, not only changed ones, so a
stale marker fails even a change that never touched it. And a commit that
lowers a figure in the baseline must say why, with a trailer in its message's
last paragraph, next to the `Co-Authored-By` lines; the header of
`.ci/check-coverage-drop` says why it must sit there:

```text
Coverage-Drop: internal/github - the retry path it covered was removed
```

The gate scans for known vulnerabilities with govulncheck and counts only
those the code actually reaches. One in a dependency module fails the gate,
because raising the module's version fixes it. One in the Go standard library
only warns: the fix is a newer Go, which arrives with a `flake.lock` update
and not with any change here. `just vuln` prints govulncheck's full report
either way.

Run `just` on its own to list every recipe with its one-line description.
