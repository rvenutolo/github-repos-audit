package main

import "runtime/debug"

// unlinkedVersion is what main.version holds when no build passed
// -ldflags "-X main.version=...".
const unlinkedVersion = "dev"

// resolveVersion decides what --version prints. A release build links its
// version in, and that always wins. Without one, `go install module@vX.Y.Z`
// still records the module version in the binary's build info, so that is
// used. `go run` and test binaries record "(devel)", which is not a version,
// and stay "dev". A plain `go build` in a regular clone records a VCS
// pseudo-version, which is reported as is; in a linked git worktree Go stamps
// no VCS information, so the build stays "dev". read is debug.ReadBuildInfo,
// injected so the outcomes are tested without building binaries.
func resolveVersion(linked string, read func() (*debug.BuildInfo, bool)) string {
	if linked != unlinkedVersion {
		return linked
	}
	info, ok := read()
	if !ok || info.Main.Version == "" || info.Main.Version == "(devel)" {
		return linked
	}
	return info.Main.Version
}
