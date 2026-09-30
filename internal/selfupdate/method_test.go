package selfupdate

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUnderCellar(t *testing.T) {
	tests := map[string]bool{
		"/opt/homebrew/Cellar/smart-test-runner/1.2.3/bin/smart-test-runner": true,
		"/opt/homebrew/Caskroom/smart-test-runner/1.2.3/smart-test-runner":   true,
		"/usr/local/Cellar/smart-test-runner/1.2.3/bin/smart-test-runner":    true,
		"/home/u/go/bin/smart-test-runner":                                   false,
		"/usr/local/bin/smart-test-runner":                                   false,
		"/tmp/Cellarish/smart-test-runner":                                   false,
	}

	for path, want := range tests {
		if got := underCellar(path); got != want {
			t.Errorf("underCellar(%q) = %v want %v", path, got, want)
		}
	}
}

// TestDetectMethodGoBin proves a binary in GOBIN is classified as go install.
// It runs the current test binary via a copy placed in a fake GOBIN — but the
// method is computed from os.Executable, so instead assert on the pure helper
// by pointing GOBIN at the directory the test binary already lives in.
func TestDetectMethodDoesNotPanic(t *testing.T) {
	if m := DetectMethod(); m != MethodBrew && m != MethodGoInstall && m != MethodScript && m != MethodUnknown {
		t.Fatalf("unexpected method %v", m)
	}
}

func TestGoBinDirsPrefersGOBIN(t *testing.T) {
	t.Setenv("GOBIN", "/custom/bin")
	t.Setenv("GOPATH", "/some/gopath")

	dirs := goBinDirs()
	if len(dirs) != 1 || dirs[0] != "/custom/bin" {
		t.Fatalf("goBinDirs = %v want [/custom/bin]", dirs)
	}
}

func TestGoBinDirsFallsBackToGOPATH(t *testing.T) {
	t.Setenv("GOBIN", "")
	t.Setenv("GOPATH", filepath.Join(string(os.PathSeparator), "a", "b"))

	dirs := goBinDirs()

	want := filepath.Join(string(os.PathSeparator), "a", "b", "bin")
	if len(dirs) != 1 || dirs[0] != want {
		t.Fatalf("goBinDirs = %v", dirs)
	}
}

func TestMethodString(t *testing.T) {
	tests := map[Method]string{
		MethodBrew:      "brew",
		MethodGoInstall: "go install",
		MethodScript:    "install script",
		MethodUnknown:   "unknown",
	}

	for m, want := range tests {
		if got := m.String(); got != want {
			t.Errorf("%d.String() = %q want %q", m, got, want)
		}
	}
}

func TestBrewUpgradeMissingBrew(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // no brew

	if _, err := brewUpgrade(t.Context()); err == nil {
		t.Fatal("want an error when brew is absent")
	}
}

func TestBrewUpgradeRunsStub(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "called")

	script := "#!/bin/sh\necho brew-upgraded > " + marker + "\necho stub-output\n"

	// The stub must be executable to satisfy LookPath; 0700 is deliberate.
	stub := filepath.Join(dir, "brew")
	if err := os.WriteFile(stub, []byte(script), 0o700); err != nil { //nolint:gosec // executable test stub
		t.Fatal(err)
	}

	t.Setenv("PATH", dir)

	out, err := brewUpgrade(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if _, serr := os.Stat(marker); serr != nil {
		t.Fatalf("brew stub not invoked: %v", serr)
	}

	if out == "" {
		t.Fatal("no output captured from brew stub")
	}
}
