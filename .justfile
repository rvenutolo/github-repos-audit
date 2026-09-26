# Local development tasks. Run `just` on its own to list them.
#
# Every recipe that runs a tool goes through .ci/in-devshell — except `fmt`,
# `format-check` and the `nix fmt` half of `fix`, which drive the host's
# `nix`, the one tool the devshell cannot provide to itself — so it runs
# under the flake's pinned tools with the host environment stripped, the
# same way CI runs. `just check` IS what CI runs.
#
# gh runs OUTSIDE the devshell so it sees the host's auth; the token is passed
# in through GITHUB_TOKEN, which .ci/in-devshell keeps.
#
# Only the single comment line directly above a recipe shows in `just --list`,
# so keep those to one line and put any longer note in the recipe body.

# List the available recipes.
default:
    @just --list

# Run everything CI checks: formatting, lints, build, tests, coverage, vuln.
check:
    ./.ci/in-devshell ./run-all-checks

# Run the lint suite only (golangci-lint, shellcheck, actionlint, lychee, ...).
lint:
    ./.ci/in-devshell ./.ci/run-lint-checks

# Run the offline test suite with the race detector and coverage.
test:
    ./.ci/in-devshell go test ./... -race -shuffle=on -cover

# Print per-package statement coverage against the recorded baseline.
cover:
    ./.ci/in-devshell go test ./... -race -shuffle=on -coverprofile=coverage.out
    ./.ci/in-devshell ./.ci/check-coverage --print

# Record current coverage as the new baseline. Commit the diff deliberately.
cover-update:
    # A line that goes DOWN in the resulting diff is a loss of coverage. The
    # commit that lowers it must say why, with a trailer in the message's
    # last paragraph (the one carrying Co-Authored-By):
    # `Coverage-Drop: <package dir> - <reason>`, which
    # .ci/check-coverage-drop enforces. The gate fails on any drop without a
    # baseline change; see .ci/check-coverage's header for why there is no
    # tolerance.
    ./.ci/in-devshell go test ./... -race -shuffle=on -coverprofile=coverage.out
    ./.ci/in-devshell ./.ci/check-coverage --update

# Fuzz every target for a bounded time. Never part of the gate.
fuzz duration="30s":
    ./.ci/in-devshell ./.ci/run-fuzz {{ duration }}

# Mutation-test a package under a memory cap. Slow; never part of the gate.
mutate package="./internal/rules/":
    # `nix develop .#mutate`, NOT ./.ci/in-devshell, which hardcodes the
    # default shell. gremlins lives in a separate shell so the gate never
    # builds it -- see the comment on devShells.mutate in flake.nix. This is
    # the same documented exception as `fmt` and `format-check` above: a
    # recipe that cannot go through the single gate entrypoint.
    #
    # Deliberately absent from run-all-checks. See .ci/run-mutation's header
    # for why this must never become a gate, and for what the memory cap is
    # protecting against.
    nix develop .#mutate --ignore-environment --keep HOME --keep TERM \
      --command ./.ci/run-mutation {{ package }}

# Bump gremlins to its latest release, rewriting both hashes. Needs network.
gremlins-bump:
    # The same command .github/workflows/gremlins-bump.yml runs. Never edit the
    # version or either hash in nix/gremlins.nix by hand; vendorHash in
    # particular cannot be computed by reading anything.
    nix run nixpkgs#nix-update -- --flake --version=stable gremlins

# Report vulnerabilities in the code paths this binary reaches.
vuln:
    ./.ci/in-devshell govulncheck ./...

# Format every file via treefmt (gofumpt, prettier, nixfmt, taplo, shfmt, just).
fmt:
    nix fmt

# Run every auto-fixer.
fix:
    nix fmt
    ./.ci/in-devshell ./.ci/run-fixers

# Verify formatting without writing changes.
format-check:
    nix flake check --no-eval-cache

# Check examples/repos.toml alone. Offline, no token.
validate:
    ./.ci/in-devshell go run ./cmd/audit validate --config examples/repos.toml

# Point core.hooksPath at .githooks (the devShell does this on entry too).
hooks:
    ./.ci/activate-githooks

# Run the live, read-only suite. Needs SMOKE_OWNER, SMOKE_PUBLIC_REPO and
# SMOKE_PRIVATE_REPO exported; every test skips without them.
smoke:
    GITHUB_TOKEN="$(gh auth token --hostname github.com)" \
      ./.ci/in-devshell go test -tags=integration ./internal/github/... -count=1
