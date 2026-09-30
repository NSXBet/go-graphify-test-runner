// Command graphify-test-runner selects and runs only the Go tests a change can
// affect, using the graphify code graph and SystemOne decisions.
package main

import "github.com/NSXBet/go-graphify-test-runner/cmd"

func main() {
	cmd.Execute()
}
