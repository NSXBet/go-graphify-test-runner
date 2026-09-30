// Package version reports the running binary's version.
package version

import (
	"runtime/debug"
	"strings"
)

// Version is injected at build time by the release tooling:
//
//	go build -ldflags "-X github.com/NSXBet/go-graphify-test-runner/internal/version.Version=v1.2.3"
//
// It is empty for a plain `go build`.
var Version string

// Get returns the version, in priority order:
//  1. the linker-injected Version (release binaries built by goreleaser);
//  2. the module version the Go toolchain embeds — which is the tag when the
//     binary was installed with `go install ...@<tag>` or `...@latest`;
//  3. "dev" for a local build with neither.
//
// A pseudo-version (`v0.0.0-<ts>-<sha>`, what `go build` stamps in a git tree)
// and the toolchain's `(devel)` marker both count as local builds, not releases.
func Get() string {
	if Version != "" {
		return Version
	}

	if bi, ok := debug.ReadBuildInfo(); ok {
		if v := bi.Main.Version; v != "" && v != "(devel)" && !strings.HasPrefix(v, "v0.0.0-") {
			return v
		}
	}

	return "dev"
}
