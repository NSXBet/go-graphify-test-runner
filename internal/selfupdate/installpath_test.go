package selfupdate

import (
	"path"
	"strings"
	"testing"
)

// TestInstallPathNamesBinaryCorrectly guards the reason the main package moved
// to cmd/smart-test-runner: the Go toolchain names an installed binary after
// the last element of the import path, and that must be `smart-test-runner`.
func TestInstallPathNamesBinaryCorrectly(t *testing.T) {
	if got := path.Base(InstallPath); got != "smart-test-runner" {
		t.Fatalf("InstallPath base = %q want smart-test-runner — "+
			"`go install %s@tag` would otherwise produce the wrong binary name", got, InstallPath)
	}

	if !strings.HasPrefix(InstallPath, ModuleRoot) {
		t.Fatalf("InstallPath %q is not under the module %q", InstallPath, ModuleRoot)
	}
}
