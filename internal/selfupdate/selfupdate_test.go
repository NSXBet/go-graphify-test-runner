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

func TestCheckFindsNewer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/"+Repo+"/releases/latest" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}

		_, _ = w.Write([]byte(`{"tag_name":"v2.0.0"}`))
	}))
	defer srv.Close()

	latest, outdated, err := Check(context.Background(), Options{APIBase: srv.URL, Current: "v1.0.0"})
	if err != nil {
		t.Fatal(err)
	}

	if latest != "v2.0.0" || !outdated {
		t.Fatalf("latest=%q outdated=%v want v2.0.0/true", latest, outdated)
	}
}

func TestCheckUpToDate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"v1.0.0"}`))
	}))
	defer srv.Close()

	latest, outdated, err := Check(context.Background(), Options{APIBase: srv.URL, Current: "v1.0.0"})
	if err != nil {
		t.Fatal(err)
	}

	if latest != "v1.0.0" || outdated {
		t.Fatalf("latest=%q outdated=%v want v1.0.0/false", latest, outdated)
	}
}

func TestCheckErrorStatus(t *testing.T) {
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
