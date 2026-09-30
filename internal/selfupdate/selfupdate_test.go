package selfupdate

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsNewer(t *testing.T) {
	tests := []struct {
		current, candidate string
		want               bool
	}{
		{"v1.2.3", "v1.2.4", true},
		{"v1.2.3", "v1.3.0", true},
		{"v1.2.3", "v2.0.0", true},
		{"v1.2.3", "v1.2.3", false},
		{"v1.3.0", "v1.2.9", false},
		{"v1.2.3", "v1.2.3-rc1", false}, // suffix ignored, same base
		{"dev", "v1.0.0", true},         // a local build is always out of date
		{"", "v1.0.0", true},
		{"v1.0.0", "", false},        // nothing to compare against
		{"garbage", "v1.0.0", false}, // unparsable current: no nudge
		{"v1.0.0", "garbage", false},
		{"1.2.3", "v1.2.4", true}, // tolerate a missing v prefix
	}

	for _, tt := range tests {
		if got := IsNewer(tt.current, tt.candidate); got != tt.want {
			t.Errorf("IsNewer(%q, %q) = %v want %v", tt.current, tt.candidate, got, tt.want)
		}
	}
}

// redirectingRelease starts a server whose /<Repo>/releases/latest redirects to
// the given tag, mimicking GitHub's releases/latest redirect (the path the
// version check follows - it never calls the rate-limited API).
func redirectingRelease(t *testing.T, tag string) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		want := "/" + Repo + "/releases/latest"
		if r.URL.Path != want {
			t.Errorf("unexpected path %q, want %q", r.URL.Path, want)
		}

		http.Redirect(w, r, "/"+Repo+"/releases/tag/"+tag, http.StatusFound)
	}))
	t.Cleanup(srv.Close)

	return srv
}

func TestCheckFindsNewer(t *testing.T) {
	srv := redirectingRelease(t, "v2.0.0")

	latest, outdated, err := Check(context.Background(), Options{APIBase: srv.URL, Current: "v1.0.0"})
	if err != nil {
		t.Fatal(err)
	}

	if latest != "v2.0.0" || !outdated {
		t.Fatalf("latest=%q outdated=%v want v2.0.0/true", latest, outdated)
	}
}

func TestCheckUpToDate(t *testing.T) {
	srv := redirectingRelease(t, "v1.0.0")

	latest, outdated, err := Check(context.Background(), Options{APIBase: srv.URL, Current: "v1.0.0"})
	if err != nil {
		t.Fatal(err)
	}

	if latest != "v1.0.0" || outdated {
		t.Fatalf("latest=%q outdated=%v want v1.0.0/false", latest, outdated)
	}
}

func TestCheckErrorStatus(t *testing.T) {
	// No Location header: the check must error rather than silently report
	// "up to date".
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	if _, _, err := Check(context.Background(), Options{APIBase: srv.URL, Current: "v1.0.0"}); err == nil {
		t.Fatal("want an error for a non-200 response")
	}
}

func TestValidTag(t *testing.T) {
	tests := map[string]bool{
		"v1.2.3":       true,
		"v0.0.1-devel": true,
		"v1.0.0+meta":  true,
		"1.2.3":        false, // must start with v
		"v1.0.0;rm":    false, // shell metacharacter
		"v1.0.0 && x":  false,
		"dev":          false,
	}

	for tag, want := range tests {
		if got := validTag(tag); got != want {
			t.Errorf("validTag(%q) = %v want %v", tag, got, want)
		}
	}
}
