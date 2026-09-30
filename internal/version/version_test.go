package version

import "testing"

func TestGetDefaultsToDev(t *testing.T) {
	// A unit-test build has no injected Version and the test binary's module
	// version is "(devel)", so Get must fall back to "dev".
	Version = ""

	got := Get()
	if got != "dev" {
		t.Fatalf("Get() = %q want dev", got)
	}
}

func TestGetPrefersInjectedVersion(t *testing.T) {
	Version = "v1.2.3"

	t.Cleanup(func() { Version = "" })

	if got := Get(); got != "v1.2.3" {
		t.Fatalf("Get() = %q want v1.2.3", got)
	}
}
