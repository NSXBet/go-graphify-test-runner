package cmd

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NSXBet/go-smart-test-runner/internal/version"
)

// newStubReleases starts a server whose releases/latest redirects to the given
// tag — the redirect the version check follows, not the rate-limited API. It
// returns the server's base URL for Options.APIBase.
func newStubReleases(t *testing.T, tag string) string {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Set Location directly: http.Redirect trips gosec's open-redirect rule
		// on a handler that echoes the request path.
		w.Header().Set("Location", r.URL.Path+"/../tag/"+tag)
		w.WriteHeader(http.StatusFound)
	}))
	t.Cleanup(srv.Close)

	return srv.URL
}

func TestUpdateHintMentionsBothPaths(t *testing.T) {
	got := updateHint("v1.0.0", "v2.0.0")

	for _, want := range []string{"v2.0.0", "v1.0.0", "smart-test-runner upgrade", "go install"} {
		if !strings.Contains(got, want) {
			t.Fatalf("hint missing %q:\n%s", want, got)
		}
	}
}

func TestCheckUpdateUpToDate(t *testing.T) {
	version.Version = "v1.0.0"

	t.Cleanup(func() { version.Version = "" })

	// A stub HTTP server is installed via the env override the package reads.
	srv := newStubReleases(t, "v1.0.0")
	t.Setenv("SMART_TEST_RUNNER_UPDATE_API", srv)

	cmd := newCheckUpdateCmd()

	var buf bytes.Buffer

	cmd.SetOut(&buf)

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(buf.String(), "Up to date") {
		t.Fatalf("want an up-to-date message:\n%s", buf.String())
	}
}
