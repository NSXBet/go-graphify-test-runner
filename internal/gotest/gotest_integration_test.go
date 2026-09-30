//go:build integration

package gotest

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// goModule builds a throwaway Go module with two packages and tests that
// record their execution by writing a marker file the assertions can read.
func goModule(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()

	writeFile(t, dir, "go.mod", "module example.com/fx\n\ngo 1.21\n")
	writeFile(t, dir, "pkg/alpha/alpha.go", "package alpha\n\nfunc Add(a, b int) int { return a + b }\n")
	writeFile(t, dir, "pkg/alpha/alpha_test.go", `package alpha

import (
	"os"
	"testing"
)

func TestAdd(t *testing.T) {
	_ = os.WriteFile(os.Getenv("MARKER"), []byte("TestAdd"), 0o600)
}

func TestOther(t *testing.T) {
	_ = os.WriteFile(os.Getenv("MARKER"), []byte("TestOther"), 0o600)
}
`)
	writeFile(t, dir, "pkg/beta/beta.go", "package beta\n\nfunc Greet() string { return \"hi\" }\n")
	writeFile(t, dir, "pkg/beta/beta_test.go", `package beta

import "testing"

func TestGreet(t *testing.T) {}
`)

	return dir
}

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()

	full := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRunSelectsExactTest(t *testing.T) {
	ctx := context.Background()
	dir := goModule(t)

	marker := filepath.Join(t.TempDir(), "marker")
	t.Setenv("MARKER", marker)

	// Only TestAdd: the -run pattern must not drag in TestOther.
	code := Run(ctx, dir, map[string][]string{"pkg/alpha": {"TestAdd"}}, nil, false, nil)
	if code != 0 {
		t.Fatalf("Run code = %d want 0", code)
	}

	got, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("marker not written: %v", err)
	}

	if string(got) != "TestAdd" {
		t.Fatalf("ran %q want TestAdd", got)
	}
}

func TestRunGroupsByDirectory(t *testing.T) {
	ctx := context.Background()
	dir := goModule(t)

	// Two directories: both must run without a package-pattern error.
	code := Run(ctx, dir, map[string][]string{
		"pkg/alpha": {"TestAdd"},
		"pkg/beta":  {"TestGreet"},
	}, nil, false, nil)
	if code != 0 {
		t.Fatalf("Run code = %d want 0", code)
	}
}

func TestRunReportsFailure(t *testing.T) {
	ctx := context.Background()
	dir := goModule(t)

	writeFile(t, dir, "pkg/beta/beta_test.go", "package beta\n\nimport \"testing\"\n\nfunc TestGreet(t *testing.T) { t.Fatal(\"boom\") }\n")

	code := Run(ctx, dir, map[string][]string{"pkg/beta": {"TestGreet"}}, nil, false, nil)
	if code != 1 {
		t.Fatalf("Run code = %d want 1 on failing test", code)
	}
}

func TestRunFindsNestedModuleRoot(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	// A nested module: the package pattern must be resolved from it, not the
	// outer directory.
	writeFile(t, dir, "go.mod", "module example.com/outer\n\ngo 1.21\n")
	writeFile(t, dir, "nested/go.mod", "module example.com/nested\n\ngo 1.21\n")
	writeFile(t, dir, "nested/p/x.go", "package p\n\nfunc F() int { return 1 }\n")
	writeFile(t, dir, "nested/p/x_test.go", "package p\n\nimport \"testing\"\n\nfunc TestF(t *testing.T) {}\n")

	code := Run(ctx, dir, map[string][]string{"nested/p": {"TestF"}}, nil, false, nil)
	if code != 0 {
		t.Fatalf("Run code = %d want 0", code)
	}
}

func TestModuleRootRealTree(t *testing.T) {
	dir := goModule(t)

	if got := ModuleRoot(dir, "pkg/alpha"); got != dir {
		t.Fatalf("ModuleRoot = %q want %q", got, dir)
	}
}

func TestRunForwardsExtraGoTestArgs(t *testing.T) {
	ctx := context.Background()
	dir := goModule(t)

	// A test that fails unless -run reached go test with our args. -v writes
	// "=== RUN" to the run output; use -count=1 to defeat the cache and prove
	// the arg was honoured rather than short-circuited.
	code := Run(ctx, dir, map[string][]string{"pkg/alpha": {"TestAdd"}}, []string{"-count=1"}, false, nil)
	if code != 0 {
		t.Fatalf("Run with extra args code = %d want 0", code)
	}

	// An invalid flag forwarded through must reach go test and make it fail —
	// proof the args are not silently dropped.
	if code := Run(ctx, dir, map[string][]string{"pkg/alpha": {"TestAdd"}}, []string{"-this-flag-does-not-exist"}, false, nil); code == 0 {
		t.Fatal("Run swallowed an invalid go test flag")
	}
}

func TestRunDryRunPrintsForwardedArgs(t *testing.T) {
	ctx := context.Background()
	dir := goModule(t)

	// Dry-run writes the exact command to the given writer; confirm the
	// forwarded args appear in order before -run.
	var buf bytes.Buffer

	code := Run(ctx, dir, map[string][]string{"pkg/alpha": {"TestAdd"}}, []string{"-race", "-count=1"}, true, &buf)
	if code != 0 {
		t.Fatalf("dry-run code = %d want 0", code)
	}

	if got := buf.String(); !strings.Contains(got, "go test -race -count=1 -run") {
		t.Fatalf("forwarded args not in printed command:\n%s", got)
	}
}

// TestRunWriterRedirectsOutput proves subprocess output goes to the supplied
// writer, which is what keeps --json stdout clean.
func TestRunWriterRedirectsOutput(t *testing.T) {
	ctx := context.Background()
	dir := goModule(t)

	writeFile(t, dir, "pkg/alpha/alpha_test.go", `package alpha

import "testing"

func TestAdd(t *testing.T) { t.Log("OUTPUT-MARKER") }
`)

	var buf bytes.Buffer

	if code := Run(ctx, dir, map[string][]string{"pkg/alpha": {"TestAdd"}}, []string{"-v"}, false, &buf); code != 0 {
		t.Fatalf("code = %d want 0", code)
	}

	if !strings.Contains(buf.String(), "OUTPUT-MARKER") {
		t.Fatalf("subprocess output not captured by the writer:\n%s", buf.String())
	}
}
