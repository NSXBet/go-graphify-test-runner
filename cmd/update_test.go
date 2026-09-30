package cmd

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NSXBet/go-graphify-test-runner/internal/version"
)

// newStubReleases starts a server that answers the latest-release endpoint with
// the given tag and returns its base URL.
func newStubReleases(t *testing.T, tag string) string {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"` + tag + `"}`))
	}))
	t.Cleanup(srv.Close)

	return srv.URL
}

func TestUpdateHintMentionsBothPaths(t *testing.T) {
	got := updateHint("v1.0.0", "v2.0.0")

	for _, want := range []string{"v2.0.0", "v1.0.0", "graphify-test-runner upgrade", "go install"} {
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
	t.Setenv("GRAPHIFY_TEST_RUNNER_UPDATE_API", srv)

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
