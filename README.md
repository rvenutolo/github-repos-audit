# github-repos-audit

A Go tool that reads live GitHub for every repository an account owns that is
neither a fork nor archived, judges each one against a standard declared in
`repos.toml`, and writes a report: a Markdown file for people and a JSON file
for anything that wants to diff it.

The tool is read-only. Every call it makes is a `GET` request or a single
GraphQL `query`; it never issues a GraphQL `mutation` or a write-verb REST
call. All of that reading happens in one command, `audit render`. Writing the
report back to a repository — creating a branch, committing to it, opening a
pull request — happens entirely inside the reusable workflow described below,
never in the tool's own code path.

## Using this with your own account

1. Create a private repository to hold your configuration and report. This
   guide calls it the report repository; the tool never needs write access to
   anything else.

2. Copy `examples/repos.toml` from this repository into the report repository
   as `repos.toml`, then replace every `[repos.<name>]` table with your own
   repositories. `audit render` refuses to run while the config and the
   account disagree: every repository the account has needs an entry, and
   every entry needs a repository the account still has. See the reference
   below for what the file can say.

3. Add three workflow files to the report repository's `.github/workflows/`,
   each calling one of this repository's reusable workflows at a pinned
   commit. Pin the `uses:` line to a released tag's commit SHA, not to a
   branch name, so a change here cannot alter what runs in your repository
   without your say:

   Every caller job below sets `permissions: contents: read` explicitly,
   because a called workflow's own `permissions: {}` only caps what it can be
   granted — the caller job still starts from its repository's default token
   permissions unless it says otherwise. The reusable workflows themselves
   declare no `concurrency:` — inside a called workflow, `github.workflow` is
   the _caller's_ workflow name, so a concurrency group keyed on it there
   would collide with whatever else in your repository shares that name, and
   you set it in the caller instead, where the name is unambiguous.

   ```yaml
   # .github/workflows/audit.yml — daily report refresh, opens a pull request
   # when something changed.
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
       uses: rvenutolo/github-repos-audit/.github/workflows/audit.yml@0123456789abcdef0123456789abcdef01234567 # v0.1.0
       permissions:
         contents: read
       secrets:
         AUDIT_TOKEN: ${{ secrets.AUDIT_TOKEN }}
         REFRESH_TOKEN: ${{ secrets.REFRESH_TOKEN }}
   ```

   ```yaml
   # .github/workflows/validate.yml — checks repos.toml and the committed
   # report on every pull request, without changing anything.
   name: validate
   on: pull_request
   # A superseded push should not keep an old validation run alive: cancel it
   # in favour of the new one, keyed per pull-request ref.
   concurrency:
     group: ${{ github.workflow }}-${{ github.ref }}
     cancel-in-progress: true
   jobs:
     validate:
       uses: rvenutolo/github-repos-audit/.github/workflows/validate.yml@0123456789abcdef0123456789abcdef01234567 # v0.1.0
       permissions:
         contents: read
   ```

   ```yaml
   # .github/workflows/smoke.yml — the tool's own live integration suite,
   # run against two fixture repositories of yours. Optional; skip it if you
   # would rather not maintain fixture repositories.
   name: smoke
   on:
     schedule:
       - cron: "0 5 * * 1"
     workflow_dispatch: {}
   jobs:
     smoke:
       uses: rvenutolo/github-repos-audit/.github/workflows/smoke.yml@0123456789abcdef0123456789abcdef01234567 # v0.1.0
       permissions:
         contents: read
       with:
         owner: ${{ vars.SMOKE_OWNER }}
         public-repo: ${{ vars.SMOKE_PUBLIC_REPO }}
         private-repo: ${{ vars.SMOKE_PRIVATE_REPO }}
       secrets:
         AUDIT_TOKEN: ${{ secrets.AUDIT_TOKEN }}
   ```

4. Set two repository secrets on the report repository:
   - `AUDIT_TOKEN` — a token that can read every repository on the account:
     contents, metadata, actions, and administration, all read-only. This is
     the token `audit render` uses — `audit validate` is offline and needs no
     token at all — and it is the only one the tool's own code ever sees. It
     cannot write anything, on this repository or any other.
   - `REFRESH_TOKEN` — a token scoped to the report repository alone, with
     Contents and Pull requests write access. It is used only by the refresh
     workflow, after `audit render` has finished, to commit the regenerated
     report to a branch and open the pull request.

5. If you added `smoke.yml`, set three repository variables: `SMOKE_OWNER`,
   `SMOKE_PUBLIC_REPO` and `SMOKE_PRIVATE_REPO`, naming an account and one
   public and one private repository on it that the tool is allowed to probe.
   The suite only reads them, but it is not indifferent to which two you
   pick — `internal/github/live_integration_test.go` asserts real facts about
   each one, and a fixture missing one of these fails the weekly run with no
   further explanation:
   - The public repository needs a description, at least one workflow file,
     at least one ruleset, readable branch protection rules, and a non-empty
     Actions permissions policy.
   - The private repository needs to genuinely be private, so the suite can
     observe the settings that only differ on a private repository (secret
     scanning and private vulnerability reporting both report as absent
     there, and the Actions access-level setting only exists there).

## `repos.toml` reference

`repos.toml` is the only file you hand-maintain. It has three kinds of table.

`[types.<name>]` defines a category of repository and what it expects. A type
lists every check the tool knows, each set to one of:

| Word               | Meaning                                                   |
| ------------------ | --------------------------------------------------------- |
| `required`         | expected                                                  |
| `not_required`     | does not apply and never counts as a gap                  |
| `public`           | expected only when the repository is public               |
| `public_published` | expected only when the repository is public and published |
| `info`             | shown, never judged                                       |

`direct_push` takes `blocked` or `allowed` instead, and the four value-only
checks — `last_release_age`, `last_push`, `open_prs` and `branches` — take
only `info` or `not_required`. `renovate_min_release_age` takes a duration
instead — `"7 days"`, `"48 hours"`, `"1 week"` (a whole number, then minute,
hour, day or week, singular or plural) — or `not_required` or `info`; an
override may give it a different duration. A type that leaves a check out is
an error, so a check added in a later version of the tool is decided for every
type before `audit validate` passes. Type names use lowercase letters, digits,
`_` and `-`, and a type no repository uses is allowed.

Each `[repos.<name>]` entry declares a `type`, and optionally `published` and
per-repository `overrides`. Visibility is never declared; it is always read
live from GitHub.

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

An override that names an unknown check is an error, and so is an override
that agrees with what the repository's type already derives — a dead
override is deleted rather than left to rot. `required` is never accepted on
a value-only check. `examples/repos.invalid.toml` is a worked example of the
dead-override error: it is a copy of `examples/repos.toml` with one override
restated — and with `git_identity` judged by no type, so it needs no
`[identity]` table — and `audit validate` is expected to reject it.

## The checks

- **readme**, **description**, **license**, **gitignore**, **editorconfig** —
  whether the file or metadata field exists at all.
- **ci_workflows**, **required_checks**, **ci_green** — whether
  `.github/workflows` has anything in it, whether the default branch has any
  required status checks, and whether the head commit's checks last reported
  success.
- **renovate**, **flake_nix**, **justfile** — whether the corresponding
  tooling file exists.
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
- **signed_commits**, **tag_ruleset**, **direct_push** — whether commit
  signing is required, whether tags are protected by a ruleset, and whether
  a push straight to the default branch is possible at all.
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
  100 commits beyond the first hundred. A commit made through GitHub's web UI
  is recorded under the account's noreply identity (a
  `…@users.noreply.github.com` address); if `match` catches it, list it under
  `accepted`.
- **secret_scanning**, **vuln_reporting** — the two security features
  GitHub can enable on a repository.
- **last_release_age**, **last_push**, **open_prs**, **branches** — plain
  facts, always shown and never judged: how long since the last release and
  the last push, how many pull requests are open, and how many branches
  exist.

## Development

Every recipe below runs inside a Nix devshell (`nix develop`, or `direnv
allow`, which does the same), so a green local run is a green CI run.
`just check` is exactly what CI runs; nothing but Nix is expected to be
installed on the host.

| Command             | Does                                                      |
| ------------------- | --------------------------------------------------------- |
| `just check`        | the whole verification gate, exactly what CI runs         |
| `just test`         | the offline test suite, with the race detector            |
| `just lint`         | the lint suite alone (golangci-lint, actionlint, ...)     |
| `just fmt`          | format every file (gofumpt, prettier, nixfmt, ...)        |
| `just cover`        | print per-package statement coverage against the baseline |
| `just cover-update` | record current coverage as the new baseline               |
| `just validate`     | check `examples/repos.toml` alone; offline, no token      |
| `just smoke`        | the live suite against real GitHub; read-only             |
| `just vuln`         | report vulnerabilities with govulncheck                   |
| `just fuzz`         | fuzz every target briefly; never part of the gate         |
| `just mutate`       | mutation-test a package; slow, never part of the gate     |

Coverage is held three ways, all inside `just check`. Each package must stay
at or above its figure in `.ci/coverage-baseline.txt`. Every Go block a
change touches (everything since the branch left `origin/main`) must be run
by the tests. When one can't be (a defensive branch no test can reach),
mark it with a line comment inside the block:

```go
if err != nil { // coverage-exempt: the reader never fails on a bytes.Buffer
```

A marker needs a reason and must still mark unrun code; a stale one fails.
And a commit that lowers a figure in the baseline must say why, with a
trailer in its message's last paragraph, next to the `Co-Authored-By` lines
(a paragraph of its own is not a trailer to git):

```text
Coverage-Drop: internal/github - the retry path it covered was removed
```

Run `just` on its own to list every recipe with its one-line description.
