// Package selfupdate checks whether a newer release exists and, when asked,
// updates the binary in place.
package selfupdate

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path"
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

// DefaultReleaseRoot is the web root whose /releases/latest redirects to the
// newest non-prerelease tag - unmetered, unlike the API.
const DefaultReleaseRoot = "https://github.com"

// APIBaseEnv overrides the API root, for enterprise mirrors and tests.
const APIBaseEnv = "SMART_TEST_RUNNER_UPDATE_API"

// httpTimeout bounds the version-check request.
const httpTimeout = 5 * time.Second

// versionParts is how many dotted components a version must have (x.y.z).
const versionParts = 3

// Options controls a check.
type Options struct {
	// APIBase overrides the GitHub API root (tests, enterprise mirrors).
	APIBase string
	// Current is the running version ("v1.2.3" or "dev").
	Current string
	// HTTPClient overrides the client (tests).
	HTTPClient *http.Client
}

// Check reports the latest release tag and whether it is newer than current.
// It returns the latest tag ("" when unknown) and whether an upgrade is
// available. A "dev" or empty current is treated as always-outdated, so a local
// build is told to install a release.
//
// Resolution uses the releases/latest redirect, not the GitHub API: the API is
// rate-limited for unauthenticated callers (a 403 silently breaks the check),
// while the redirect is unmetered and needs no token. APIBase, when set, is
// reduced to its host part so tests and mirrors keep working.
func Check(ctx context.Context, opts Options) (latest string, outdated bool, err error) {
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: httpTimeout}
	}

	tag, err := latestViaRedirect(ctx, client, opts.APIBase)
	if err != nil {
		return "", false, err
	}

	if tag == "" {
		return "", false, nil
	}

	return tag, IsNewer(opts.Current, tag), nil
}

// latestViaRedirect resolves the latest release tag from the redirect target of
// <root>/<repo>/releases/latest.
func latestViaRedirect(ctx context.Context, client *http.Client, apiBase string) (string, error) {
	base := apiBase
	if base == "" {
		base = os.Getenv(APIBaseEnv)
	}

	root := DefaultReleaseRoot
	if base != "" {
		root = strings.TrimSuffix(base, "/")
		// An API root ends in /repos...; the releases root is its host part.
		if i := strings.Index(root, "/repos"); i > 0 {
			root = root[:i]
		}
	}

	url := root + "/" + Repo + "/releases/latest"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody) //nolint:gosec // operator-configured root
	if err != nil {
		return "", err
	}

	// Do not follow the redirect: the Location header carries the tag.
	noRedirect := *client
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	resp, err := noRedirect.Do(req) //nolint:gosec // operator-configured root
	if err != nil {
		return "", err
	}

	defer func() { _ = resp.Body.Close() }()

	loc := resp.Header.Get("Location")
	if loc == "" {
		return "", fmt.Errorf("latest release check: no redirect from %s (HTTP %d)", url, resp.StatusCode)
	}

	tag := path.Base(loc)
	if tag == "" || tag == "latest" {
		return "", fmt.Errorf("latest release check: could not parse a tag from %q", loc)
	}

	return tag, nil
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
//
// The tap is refreshed first: a third-party tap is not auto-updated, so without
// `brew update` the local clone can still carry the previous cask and brew will
// consider the install already current.
func brewUpgrade(ctx context.Context) (string, error) {
	if _, err := exec.LookPath("brew"); err != nil {
		return "", errors.New("brew not found in PATH (this binary was installed with Homebrew)")
	}

	var out strings.Builder

	if update, uerr := runBrew(ctx, "update"); uerr == nil {
		out.WriteString(update)
	}
	// A failed `brew update` is not fatal on its own; the upgrade below decides.

	upgraded, err := runBrew(ctx, "upgrade", Formula)

	out.WriteString(upgraded)

	return out.String(), err
}

// runBrew runs one brew subcommand and returns its combined output.
func runBrew(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "brew", args...)

	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("brew %s failed: %w", strings.Join(args, " "), err)
	}

	return string(out), nil
}
