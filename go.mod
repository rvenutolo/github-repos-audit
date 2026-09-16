module github.com/rvenutolo/github-repos-audit

// The go directive is the ceiling on language features. The toolchain
// directive pins the patched release without raising it.
go 1.26.0

toolchain go1.27.1

require (
	github.com/BurntSushi/toml v1.6.0
	github.com/google/go-cmp v0.7.0
)
