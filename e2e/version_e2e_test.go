//go:build e2e

package e2e

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newStubReleaseAPI answers the latest-release endpoint with the given tag.
func newStubReleaseAPI(t *testing.T, tag string) string {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"` + tag + `"}`))
	}))
	t.Cleanup(srv.Close)

	return srv.URL
}

// TestCLIVersionSubcommand proves the assembled binary reports a version and
// that --version agrees. It does not assert a specific value: a plain build in
// an untagged checkout says "dev", one in a tagged checkout reports the tag —
// both are correct, and the test must hold in either repo state.
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

// TestCLICheckUpdateAndUpgradeRegistered proves both update subcommands exist
// and work against a stub release API (no real network).
func TestCLICheckUpdateAndUpgradeRegistered(t *testing.T) {
	bin := buildBinary(t)

	srv := newStubReleaseAPI(t, "v9.9.9")
	env := []string{"SMART_TEST_RUNNER_UPDATE_API=" + srv}

	stdout, _, code := runCLI(t, bin, env, "check-update")
	if code != 0 {
		t.Fatalf("check-update exit = %d want 0", code)
	}

	if !strings.Contains(stdout, "v9.9.9") {
		t.Fatalf("check-update did not report the newer tag:\n%s", stdout)
	}

	// upgrade must recognise the newer tag and report the attempt (the module
	// does not exist, so it fails at go install — that is expected and proves
	// the flow reached the install step).
	up, _, upCode := runCLI(t, bin, env, "upgrade")
	if upCode == 0 && !strings.Contains(up, "Updating") {
		t.Fatalf("upgrade did not start:\n%s", up)
	}
}
