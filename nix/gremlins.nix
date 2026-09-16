# gremlins, the mutation-testing tool `just mutate` drives.
#
# Hand-packaged because nixpkgs does not ship it — checked against both the
# registry's nixpkgs and this flake's pinned input. Everything else in the
# devShell comes from nixpkgs; this is the one exception, and it is confined to
# a shell the gate never enters.
#
# On the vendorHash, which is the thing that killed the flake package in #33:
# that hash was derived from THIS repo's go.mod, so every Renovate dependency
# bump invalidated it and turned `checks.build` red. This one is derived from
# gremlins' own go.mod, frozen at the tag below, so nothing this repo does can
# invalidate it. It moves only when the version moves, and
# .github/workflows/gremlins-bump.yml moves both together with `nix-update`.
# Never edit either hash by hand — run that workflow, or `just gremlins-bump`.
{
  lib,
  buildGoModule,
  fetchFromGitHub,
}:
buildGoModule (finalAttrs: {
  pname = "gremlins";
  version = "0.6.0";

  src = fetchFromGitHub {
    owner = "go-gremlins";
    repo = "gremlins";
    tag = "v${finalAttrs.version}";
    hash = "sha256-QwMj7aA4eafMT25gBLAomZMliCbueoEsDHD/nxtnmk4=";
  };

  vendorHash = "sha256-TYbbDN2V6GLj+YRNQIKggCnNspk3M96cP1DSe8P9qlY=";

  # Only the CLI. The repo's other main packages are its own tooling.
  subPackages = [ "cmd/gremlins" ];

  # `main.version` is a plain var defaulting to "dev"; without this the tool
  # reports `gremlins version dev`, and a mutation report that cannot say which
  # tool produced it is not worth much when it is read months later.
  ldflags = [
    "-s"
    "-w"
    "-X main.version=${finalAttrs.version}"
  ];

  # Upstream's own suite, not ours. It shells out to `go` and to git, which a
  # sandboxed build does not have, so it is not a signal we could act on. What
  # we actually depend on is checked in .github/workflows/gremlins-bump.yml,
  # which builds this package and then runs a real mutation pass with it.
  doCheck = false;

  meta = {
    description = "Mutation testing tool for Go";
    homepage = "https://github.com/go-gremlins/gremlins";
    license = lib.licenses.asl20;
    mainProgram = "gremlins";
  };
})
