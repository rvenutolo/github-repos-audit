{ pkgs, ... }:
{
  projectRootFile = "flake.nix";

  programs = {
    # gofumpt, not gofmt: a strict superset, and the marginal rules it adds are
    # the ones code review would otherwise spend comments on.
    gofumpt.enable = true;
    # Reads .prettierrc.yaml from the repo root. Every *.md is formatted; the
    # render goldens fall under the testdata exclusion below, so prettier
    # never touches `audit render` output.
    prettier = {
      enable = true;
      includes = [
        "*.json"
        "*.md"
        "*.yaml"
        "*.yml"
      ];
    };
    nixfmt.enable = true;
    just.enable = true;
    taplo.enable = true;
  };

  # shfmt via explicit entries rather than programs.shfmt: that module only
  # exposes indent and simplify, and the flags below must match the ones
  # .ci/run-fixers and .ci/run-lint-checks pass by hand.
  settings = {
    formatter.shfmt = {
      command = "${pkgs.shfmt}/bin/shfmt";
      options = [
        "--write"
        "--indent"
        "2"
        "--case-indent"
        "--binary-next-line"
        "--space-redirects"
      ];
      includes = [
        "*.sh"
        "*.bash"
        "*.envrc"
      ];
    };

    global.excludes = [
      "flake.lock"
      # Recorded API captures and golden files: byte-exact fixtures. Both
      # spellings, because treefmt matches the path from the project root and
      # every testdata directory here is nested under a package.
      "testdata/**"
      "**/testdata/**"
    ];
  };
}
