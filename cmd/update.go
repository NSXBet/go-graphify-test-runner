package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/NSXBet/go-graphify-test-runner/internal/selfupdate"
	"github.com/NSXBet/go-graphify-test-runner/internal/version"
)

// updateCheckTimeout bounds the pre-run version check.
const updateCheckTimeout = 3 * time.Second

// updateHint is the printed update hint, kept in one place and tailored to how
// the binary was installed.
func updateHint(current, latest string) string {
	how := "graphify-test-runner upgrade"

	if selfupdate.DetectMethod() == selfupdate.MethodBrew {
		how = "brew upgrade " + selfupdate.Formula
	} else {
		how += fmt.Sprintf("\n         or:  go install %s@%s", selfupdate.ModulePath, latest)
	}

	return fmt.Sprintf(
		"A new version of graphify-test-runner is available: %s (you have %s)\nUpdate with:  %s",
		latest, current, how,
	)
}

// maybeNudge checks for a newer release and prints a one-line hint to stderr.
// It is best-effort: any failure is silent, and the result is cached for a day
// so it never slows down or breaks a normal run.
func maybeNudge(ctx context.Context) {
	current := version.Get()

	now := time.Now()

	if _, fresh := selfupdate.ReadCache(now); fresh {
		return
	}

	checkCtx, cancel := context.WithTimeout(ctx, updateCheckTimeout)
	defer cancel()

	latest, outdated, err := selfupdate.Check(checkCtx, selfupdate.Options{Current: current})
	if err != nil || latest == "" {
		// Do not cache a failed check — retry on the next run.
		return
	}

	selfupdate.WriteCache(latest, now)

	if outdated {
		fmt.Fprintln(os.Stderr, updateHint(current, latest))
	}
}

// newUpgradeCmd builds the `upgrade` subcommand: check, then reinstall.
func newUpgradeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "upgrade",
		Short: "Update the binary to the latest release",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			current := version.Get()

			latest, outdated, err := selfupdate.Check(ctx, selfupdate.Options{Current: current})
			if err != nil {
				return fmt.Errorf("could not check the latest release: %w", err)
			}

			if latest == "" {
				return errors.New("no releases found")
			}

			if !outdated {
				fmt.Fprintf(cmd.OutOrStdout(), "Already up to date (%s)\n", current)

				return nil
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Updating %s -> %s ...\n", current, latest)

			used, out, err := selfupdate.Upgrade(ctx, latest)
			if err != nil {
				return err
			}

			if out != "" {
				fmt.Fprint(cmd.OutOrStdout(), out)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Updated to %s via %s. Re-run to use it.\n", latest, used)

			return nil
		},
	}
}

// newCheckUpdateCmd builds the `check-update` subcommand: report only.
func newCheckUpdateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "check-update",
		Short: "Check whether a newer release is available",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			current := version.Get()

			latest, outdated, err := selfupdate.Check(cmd.Context(), selfupdate.Options{Current: current})
			if err != nil {
				return fmt.Errorf("could not check the latest release: %w", err)
			}

			switch {
			case latest == "":
				fmt.Fprintln(cmd.OutOrStdout(), "No releases found.")
			case outdated:
				fmt.Fprintln(cmd.OutOrStdout(), updateHint(current, latest))
			default:
				fmt.Fprintf(cmd.OutOrStdout(), "Up to date (%s)\n", current)
			}

			return nil
		},
	}
}
