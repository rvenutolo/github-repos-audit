{
  description = "github-repos-audit - devShell and formatter";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
    systems.url = "github:nix-systems/default";
    treefmt-nix.url = "github:numtide/treefmt-nix";
    treefmt-nix.inputs.nixpkgs.follows = "nixpkgs";
  };

  outputs =
    {
      self,
      nixpkgs,
      systems,
      treefmt-nix,
    }:
    let
      eachSystem = f: nixpkgs.lib.genAttrs (import systems) (system: f nixpkgs.legacyPackages.${system});
      treefmtEval = eachSystem (pkgs: treefmt-nix.lib.evalModule pkgs ./.treefmt.nix);
    in
    {
      formatter = eachSystem (pkgs: treefmtEval.${pkgs.stdenv.hostPlatform.system}.config.build.wrapper);

      checks = eachSystem (pkgs: {
        formatting = treefmtEval.${pkgs.stdenv.hostPlatform.system}.config.build.check self;
      });

      # gremlins is exposed as a package for two reasons: `nix-update --flake`
      # can only target `packages.<system>.<name>`, and it gives the bump
      # workflow a `nix build .#gremlins` to verify against.
      #
      # This is NOT what #33 did. `nix flake check` BUILDS everything under
      # `checks` but only EVALUATES what is under the packages attribute set
      # and `devShells` -- verified with a deliberately failing canary
      # derivation, which flake check reported as `derivation evaluated to
      # ...` and passed. So the gate pays an evaluation here and never a
      # compile, and a Go dependency bump cannot turn it red the way #33's
      # `checks.build` did.
      #
      # The cost of that is real and is handled elsewhere: a gremlins that does
      # not COMPILE would sail through `just check`. Only an evaluation error
      # is caught here. .github/workflows/gremlins-bump.yml is what actually
      # builds and exercises it, because nothing in the gate does.
      packages = eachSystem (pkgs: {
        gremlins = pkgs.callPackage ./nix/gremlins.nix { };
      });

      devShells = eachSystem (pkgs: {
        # A SECOND shell, deliberately separate from `default`, for the one tool
        # the gate never runs. Three things follow from the separation, and all
        # three are why it is a separate shell rather than one more package in
        # `default`:
        #
        #   1. CI never builds gremlins. It is not in the gate, so no gate run
        #      should pay to compile it.
        #   2. No egress allowlist change. Building gremlins fetches its Go
        #      modules; under harden-runner's `egress-policy: block` that would
        #      need proxy.golang.org opened for every gate job. Only
        #      gremlins-bump.yml needs it, and only that workflow opens it.
        #   3. .ci/required-tools stays untouched. .ci/check-devshell-provides
        #      reads `devShells.${system}.default.nativeBuildInputs` and nothing
        #      else, so a tool that lives here is invisible to the two-way drift
        #      lint and correctly needs no entry there.
        #
        # go and git are here because gremlins shells out to both: it runs
        # `go test` per mutant, against a copy of the worktree.
        mutate = pkgs.mkShell {
          packages = [
            pkgs.go
            pkgs.git
            self.packages.${pkgs.stdenv.hostPlatform.system}.gremlins
          ];
          env.GOTOOLCHAIN = "local";
        };

        # mkShell, NOT mkShellNoCC: `go test -race` requires cgo and therefore a
        # C compiler. mkShellNoCC omits stdenv.cc, so the race detector would
        # fail inside the devShell while passing on a developer's host.
        default = pkgs.mkShell {
          packages = with pkgs; [
            # Go toolchain and its static checks.
            go
            golangci-lint
            # gofumpt is driven by treefmt; golangci-lint's formatters block
            # still reports import grouping.
            gofumpt
            govulncheck
            # formatters (also wired into .treefmt.nix)
            shfmt
            prettier
            nixfmt
            taplo
            # linters
            shellcheck
            yamllint
            actionlint
            # zizmor audits the workflows for SECURITY where actionlint audits
            # them for syntax: injection sinks, over-broad permissions,
            # credential persistence. They overlap nowhere.
            zizmor
            markdownlint-cli2
            # editorconfig-checker covers every tracked file no formatter owns:
            # .gitignore, LICENSE, CODEOWNERS. "Cannot be auto-formatted" must
            # not become "unchecked".
            editorconfig-checker
            # lychee checks the links in README.md. This repository carries no
            # generated report, so every link is public and GITHUB_TOKEN is
            # only used opportunistically, to avoid api.github.com's anonymous
            # rate limit.
            lychee
            typos
            gitleaks
            # check-jsonschema validates the golden audit.json fixture against
            # audit.schema.json. Offline: the schema is a local file, so
            # nothing is fetched and no cache is consulted.
            check-jsonschema
            # nix linters: statix for antipatterns, deadnix for unused bindings.
            statix
            deadnix
            # renovate, here only for `renovate-config-validator`. Without it a
            # malformed renovate.json is not a CI failure: Renovate just stops
            # opening PRs, silently.
            renovate
            # runtime
            git
            gh
            just
            # commitlint is invoked by .githooks/commit-msg.
            commitlint
            # nix.out, NOT bare `nix`: the default output is `dev`, which has
            # no bin/, so a bare `nix` puts nothing on PATH and the gate runs
            # whatever nix the host ships.
            nix.out
            # baseline userland the gates shell out to: cp and mktemp from
            # coreutils, xargs from findutils. Nothing here needs GNU grep or
            # sed — the scripts use `git grep` and bash parameter expansion —
            # so neither is declared.
            coreutils
            findutils
          ];

          # GOTOOLCHAIN=local, not the default `auto`: `auto` would let a
          # go.mod toolchain directive ahead of nixpkgs DOWNLOAD a toolchain,
          # which egress-blocked CI cannot do and which would break the rule
          # that every tool comes from this flake. `local` turns that into an
          # honest "go.mod requires a newer Go" instead.
          env.GOTOOLCHAIN = "local";

          # Activate the tracked git hooks for this clone. A manual per-clone
          # step silently never happens; the devShell is the one place
          # onboarding cannot skip. Tolerant of failure on purpose.
          shellHook = ''
            if hooks_repo_root="$(git rev-parse --show-toplevel 2>/dev/null)"; then
              "$hooks_repo_root/.ci/activate-githooks" \
                || echo 'warning: could not activate tracked git hooks' >&2
            fi
          '';
        };
      });
    };
}
