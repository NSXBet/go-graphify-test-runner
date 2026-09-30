package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/NSXBet/go-smart-test-runner/internal/version"
)

// newVersionCmd builds the `version` subcommand.
func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the binary version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), version.Get())

			return nil
		},
	}
}
