//go:build e2e

package e2e

import (
	"strings"
	"testing"
)

// TestCLIVersionSubcommand proves the assembled binary reports a version —
// "dev" for a plain local build — and that --version agrees.
func TestCLIVersionSubcommand(t *testing.T) {
	bin := buildBinary(t)

	stdout, _, code := runCLI(t, bin, nil, "version")
	if code != 0 {
		t.Fatalf("version exit = %d want 0", code)
	}

	got := strings.TrimSpace(stdout)
	if got == "" {
		t.Fatal("version subcommand printed nothing")
	}

	// A local `go build` has no injected version, so it must say "dev".
	if got != "dev" {
		t.Fatalf("version = %q want dev for a local build", got)
	}

	fout, _, fcode := runCLI(t, bin, nil, "--version")
	if fcode != 0 {
		t.Fatalf("--version exit = %d want 0", fcode)
	}

	if strings.TrimSpace(fout) != got {
		t.Fatalf("--version = %q disagrees with `version` = %q", strings.TrimSpace(fout), got)
	}
}

// TestCLIVersionNeedsNoKey proves `version` runs without OPENROUTER_API_KEY.
func TestCLIVersionNeedsNoKey(t *testing.T) {
	bin := buildBinary(t)

	stdout, _, code := runCLI(t, bin, []string{"OPENROUTER_API_KEY="}, "version")
	if code != 0 {
		t.Fatalf("version exit = %d want 0", code)
	}

	if strings.TrimSpace(stdout) == "" {
		t.Fatal("version printed nothing without a key")
	}
}
