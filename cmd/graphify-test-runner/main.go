// Command graphify-test-runner selects and runs only the Go tests a change can
// affect, using the graphify code graph and SystemOne decisions.
//
// The main package lives here so `go install
// github.com/NSXBet/go-graphify-test-runner/cmd/graphify-test-runner@latest`
// produces a binary named `graphify-test-runner` — the Go toolchain names a
// binary after the last element of its import path, so a root main package
// would yield `go-graphify-test-runner`.
package main

import "github.com/NSXBet/go-graphify-test-runner/internal/cmd"

func main() {
	cmd.Execute()
}
