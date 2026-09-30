// Command smart-test-runner selects and runs only the Go tests a change can
// affect, using the Grove code graph and SystemOne decisions.
//
// The main package lives here so `go install
// github.com/NSXBet/go-smart-test-runner/cmd/smart-test-runner@latest`
// produces a binary named `smart-test-runner` — the Go toolchain names a
// binary after the last element of its import path, so a root main package
// would yield `go-smart-test-runner`.
package main

import "github.com/NSXBet/go-smart-test-runner/internal/cmd"

func main() {
	cmd.Execute()
}
