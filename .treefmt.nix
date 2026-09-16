{ pkgs, ... }:
{
  projectRootFile = "flake.nix";

  programs = {
    # gofumpt, not gofmt: a strict superset, and the marginal rules it adds are
    # the ones code review would otherwise spend comments on.
    gofumpt.enable = true;
    # Reads .prettierrc.yaml from the repo root. README.md is deliberately NOT
    # excluded: the cron commits `audit render` output unattended, so the
    # renderer must produce prettier-clean markdown.
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

  # shfmt via explicit entries rather than programs.shfmt: that module
  # hardcodes `-s` (simplify), which strips quotes inside `[[ ]]`.
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
