package selfupdate

import (
	"path"
	"strings"
	"testing"
)

// TestInstallPathNamesBinaryCorrectly guards the reason the main package moved
// to cmd/graphify-test-runner: the Go toolchain names an installed binary after
// the last element of the import path, and that must be `graphify-test-runner`.
func TestInstallPathNamesBinaryCorrectly(t *testing.T) {
	if got := path.Base(InstallPath); got != "graphify-test-runner" {
		t.Fatalf("InstallPath base = %q want graphify-test-runner — "+
			"`go install %s@tag` would otherwise produce the wrong binary name", got, InstallPath)
	}

	if !strings.HasPrefix(InstallPath, ModuleRoot) {
		t.Fatalf("InstallPath %q is not under the module %q", InstallPath, ModuleRoot)
	}
}
