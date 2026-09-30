// Package selfupdate checks whether a newer release exists and, when asked,
// updates the binary in place.
package selfupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Repo is the GitHub repository releases are published to.
const Repo = "NSXBet/go-smart-test-runner"

// ModuleRoot is the repository's module path.
const ModuleRoot = "github.com/NSXBet/go-smart-test-runner"

// InstallPath is the package `go install` fetches for the upgrade path. It is
// the main package under cmd/, so the installed binary is named
// `smart-test-runner` (the toolchain names a binary after the import path's
// last element).
const InstallPath = ModuleRoot + "/cmd/smart-test-runner"

// Tap is the Homebrew tap that publishes the formula.
const Tap = "NSXBet/tap"

// Formula is the Homebrew formula name (`brew install NSXBet/tap/smart-test-runner`).
const Formula = "smart-test-runner"

// DefaultAPIBase is the GitHub API root; the /releases/latest endpoint returns
// the newest non-prerelease release.
const DefaultAPIBase = "https://api.github.com"

// APIBaseEnv overrides the API root, for enterprise mirrors and tests.
const APIBaseEnv = "SMART_TEST_RUNNER_UPDATE_API"

// httpTimeout bounds the version-check request.
const httpTimeout = 5 * time.Second

// maxResponseBytes caps how much of the release payload we read.
const maxResponseBytes = 1 << 20

// versionParts is how many dotted components a version must have (x.y.z).
const versionParts = 3

// release is the subset of the GitHub release payload we need.
type release struct {
	TagName string `json:"tag_name"`
}

// Options controls a check.
type Options struct {
	// APIBase overrides the GitHub API root (tests, enterprise mirrors).
	APIBase string
	// Current is the running version ("v1.2.3" or "dev").
	Current string
	// HTTPClient overrides the client (tests).
	HTTPClient *http.Client
}

// Check queries the latest release tag and reports whether it is newer than
// current. It returns the latest tag ("" when unknown) and whether an upgrade
// is available. A "dev" or empty current is treated as always-outdated, so a
// local build is told to install a release. Network failures return an error
// the caller may ignore — the check is informational, never fatal.
func Check(ctx context.Context, opts Options) (latest string, outdated bool, err error) {
	base := opts.APIBase
	if base == "" {
		base = os.Getenv(APIBaseEnv)
	}

	if base == "" {
		base = DefaultAPIBase
	}

	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: httpTimeout}
	}

	// base is the API root: GitHub by default, overridable via opts/env for
	// mirrors and tests. SSRF is not a threat here — the operator sets it.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/repos/"+Repo+"/releases/latest", http.NoBody) //nolint:gosec // base is operator-controlled, not remote input
	if err != nil {
		return "", false, err
	}

	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := client.Do(req) //nolint:gosec // request URL is the operator-configured API root
	if err != nil {
		return "", false, err
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", false, fmt.Errorf("latest release check: HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return "", false, err
	}

	var rel release
	if err := json.Unmarshal(body, &rel); err != nil {
		return "", false, fmt.Errorf("latest release check: %w", err)
	}

	if rel.TagName == "" {
		return "", false, nil
	}

	return rel.TagName, IsNewer(opts.Current, rel.TagName), nil
}

// IsNewer reports whether candidate is a strictly higher version than current.
// A current that is empty or "dev" is always outdated; unparsable versions
// compare as equal (unknown, so no nudge).
func IsNewer(current, candidate string) bool {
	if current == "" || current == "dev" {
		return candidate != ""
	}

	cur, ok := parseVersion(current)
	if !ok {
		return false
	}

	cand, ok := parseVersion(candidate)
	if !ok {
		return false
	}

	for i := range cur {
		if cand[i] != cur[i] {
			return cand[i] > cur[i]
		}
	}

	return false
}

// parseVersion parses a leading vX.Y.Z (ignoring any -suffix) into three ints.
func parseVersion(v string) ([3]int, bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	// Drop pre-release / build metadata.
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}

	parts := strings.Split(v, ".")
	if len(parts) != versionParts {
		return [3]int{}, false
	}

	var out [3]int

	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return [3]int{}, false
		}

		out[i] = n
	}

	return out, true
}

// validTag reports whether tag is a plausible release tag (vX.Y.Z with an
// optional -suffix). Rejecting anything else keeps a hostile release payload
// from steering `go install` at an arbitrary module query.
func validTag(tag string) bool {
	if !strings.HasPrefix(tag, "v") || len(tag) > 64 {
		return false
	}

	for _, r := range strings.TrimPrefix(tag, "v") {
		if (r < '0' || r > '9') && r != '.' && r != '-' && r != '+' &&
			(r < 'a' || r > 'z') && (r < 'A' || r > 'Z') {
			return false
		}
	}

	return true
}

// Upgrade refreshes the binary to the given tag, choosing the mechanism that
// matches how it was installed: Homebrew-managed binaries use `brew upgrade`;
// everything else reinstalls via `go install`. It returns the method used and
// the combined command output for reporting.
func Upgrade(ctx context.Context, tag string) (used Method, output string, err error) {
	if DetectMethod() == MethodBrew {
		out, berr := brewUpgrade(ctx)

		return MethodBrew, out, berr
	}

	out, gerr := goInstall(ctx, tag)

	return MethodGoInstall, out, gerr
}

// goInstall reinstalls InstallPath at tag via `go install`.
func goInstall(ctx context.Context, tag string) (string, error) {
	if !validTag(tag) {
		return "", fmt.Errorf("refusing to install invalid tag %q", tag)
	}

	if _, err := exec.LookPath("go"); err != nil {
		return "", errors.New("go toolchain not found in PATH (install it, or re-run install.sh)")
	}

	cmd := exec.CommandContext(ctx, "go", "install", InstallPath+"@"+tag) //nolint:gosec // tag is validated by validTag

	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("go install %s@%s failed: %w", InstallPath, tag, err)
	}

	return string(out), nil
}

// brewUpgrade updates a Homebrew-managed install.
func brewUpgrade(ctx context.Context) (string, error) {
	if _, err := exec.LookPath("brew"); err != nil {
		return "", errors.New("brew not found in PATH (this binary was installed with Homebrew)")
	}

	cmd := exec.CommandContext(ctx, "brew", "upgrade", Formula)

	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("brew upgrade %s failed: %w", Formula, err)
	}

	return string(out), nil
}
