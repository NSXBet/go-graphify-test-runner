//go:build integration

package gotest

import (
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

// runOne runs Run over the pkg/alpha selection and returns that package's
// result, the shape nearly every test here exercises.
func runOne(ctx context.Context, dir string, names, extra []string, dryRun bool) Result {
	results := Run(ctx, dir, map[string][]string{"pkg/alpha": names}, extra, dryRun)

	if len(results) != 1 {
		panic("runOne: expected exactly one package result")
	}

	return results[0]
}

func TestRunSelectsExactTest(t *testing.T) {
	dir := goModule(t)

	marker := filepath.Join(t.TempDir(), "marker")
	t.Setenv("MARKER", marker)

	// Only TestAdd: the -run pattern must not drag in TestOther.
	res := runOne(context.Background(), dir, []string{"TestAdd"}, nil, false)

	if !res.Ok() {
		t.Fatalf("pkg/alpha not ok: %v\n%s", res.Err, res.Output)
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
	dir := goModule(t)

	// Two directories: both must run without a package-pattern error.
	results := Run(context.Background(), dir, map[string][]string{
		"pkg/alpha": {"TestAdd"},
		"pkg/beta":  {"TestGreet"},
	}, nil, false)

	if len(results) != 2 {
		t.Fatalf("results = %d want 2 (one per directory)", len(results))
	}

	for _, res := range results {
		if !res.Ok() {
			t.Errorf("%s not ok: %v\n%s", res.Dir, res.Err, res.Output)
		}
	}
}

func TestRunReportsFailure(t *testing.T) {
	dir := goModule(t)

	writeFile(t, dir, "pkg/beta/beta_test.go", "package beta\n\nimport \"testing\"\n\nfunc TestGreet(t *testing.T) { t.Fatal(\"boom\") }\n")

	results := Run(context.Background(), dir, map[string][]string{"pkg/beta": {"TestGreet"}}, nil, false)

	var failed *Result

	for i := range results {
		if results[i].Err != nil {
			failed = &results[i]

			break
		}
	}

	if failed == nil {
		t.Fatalf("no result failed:\n%+v", results)
	}

	if failed.Dir != "pkg/beta" {
		t.Fatalf("failing dir = %q want pkg/beta", failed.Dir)
	}

	if !strings.Contains(failed.Output, "boom") {
		t.Fatalf("failure output does not contain the test message:\n%s", failed.Output)
	}
}

func TestRunFindsNestedModuleRoot(t *testing.T) {
	dir := t.TempDir()

	// A nested module: the package pattern must be resolved from it, not the
	// outer directory.
	writeFile(t, dir, "go.mod", "module example.com/outer\n\ngo 1.21\n")
	writeFile(t, dir, "nested/go.mod", "module example.com/nested\n\ngo 1.21\n")
	writeFile(t, dir, "nested/p/x.go", "package p\n\nfunc F() int { return 1 }\n")
	writeFile(t, dir, "nested/p/x_test.go", "package p\n\nimport \"testing\"\n\nfunc TestF(t *testing.T) {}\n")

	results := Run(context.Background(), dir, map[string][]string{"nested/p": {"TestF"}}, nil, false)

	if len(results) != 1 || !results[0].Ok() {
		t.Fatalf("nested package not ok:\n%+v", results)
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

	// -v -count=1 forces a fresh, verbose run; its "=== RUN" line lands in the
	// captured Output, proving the flags reached go test rather than being
	// silently dropped.
	res := runOne(ctx, dir, []string{"TestAdd"}, []string{"-v", "-count=1"}, false)

	if !res.Ok() {
		t.Fatalf("not ok: %v\n%s", res.Err, res.Output)
	}

	if !strings.Contains(res.Output, "=== RUN") && !strings.Contains(res.Output, "TestAdd") {
		t.Fatalf("verbose output not captured:\n%s", res.Output)
	}

	// An invalid flag forwarded through must reach go test and make it fail —
	// proof the args are neither silently dropped nor mistaken for an
	// unbuildable package and skipped.
	res = runOne(ctx, dir, []string{"TestAdd"}, []string{"-this-flag-does-not-exist"}, false)

	if res.Skipped {
		t.Fatal("invalid flag misread as unbuildable package")
	}

	if res.Err == nil {
		t.Fatal("Run swallowed an invalid go test flag")
	}
}

func TestRunDryRunPrintsForwardedArgs(t *testing.T) {
	dir := goModule(t)

	// Dry-run holds the exact command in Output; confirm the -run pattern
	// precedes the package, and that a dry run never fails.
	res := runOne(context.Background(), dir, []string{"TestAdd"}, nil, true)

	if res.Err != nil {
		t.Fatalf("dry-run must not fail: %v", res.Err)
	}

	if got := res.Output; !strings.Contains(got, "go test -run ^(TestAdd)$ ./pkg/alpha") {
		t.Fatalf("command not in Output:\n%s", got)
	}
}

// TestRunCapturesSubprocessOutput proves subprocess output lands in
// Result.Output, which is what the renderer prints for failures.
func TestRunCapturesSubprocessOutput(t *testing.T) {
	dir := goModule(t)

	writeFile(t, dir, "pkg/alpha/alpha_test.go", `package alpha

import "testing"

func TestAdd(t *testing.T) { t.Log("OUTPUT-MARKER") }
`)

	res := runOne(context.Background(), dir, []string{"TestAdd"}, nil, false)

	if !res.Ok() {
		t.Fatalf("not ok: %v\n%s", res.Err, res.Output)
	}

	// Without -v, go test still reports the package line naming the package.
	if !strings.Contains(res.Output, "ok  \tpkg/alpha") && !strings.Contains(res.Output, "ok  \texample.com/fx/pkg/alpha") {
		t.Fatalf("subprocess output not captured in Result.Output:\n%s", res.Output)
	}
}

func TestRunAllRunsEveryPackage(t *testing.T) {
	dir := goModule(t)

	marker := filepath.Join(t.TempDir(), "marker")
	t.Setenv("MARKER", marker)

	// Both packages must run, with no -run filter.
	res := RunAll(context.Background(), dir, nil, false)

	if !res.Ok() {
		t.Fatalf("RunAll not ok: %v\n%s", res.Err, res.Output)
	}

	// The marker is overwritten by whichever TestAdd/TestOther ran last, so its
	// presence proves at least one ran without a filter.
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("no test ran under RunAll: %v", err)
	}
}

func TestRunAllForwardsArgsAndDryRun(t *testing.T) {
	dir := goModule(t)

	res := RunAll(context.Background(), dir, []string{"-count=1"}, true)

	if got := res.Output; !strings.Contains(got, "go test -count=1 ./...") {
		t.Fatalf("dry-run Output = %q want 'go test -count=1 ./...'", got)
	}
}

func TestRunAllReportsFailure(t *testing.T) {
	dir := goModule(t)

	writeFile(t, dir, "pkg/beta/beta_test.go", "package beta\n\nimport \"testing\"\n\nfunc TestGreet(t *testing.T) { t.Fatal(\"boom\") }\n")

	res := RunAll(context.Background(), dir, nil, false)

	if res.Err == nil {
		t.Fatal("RunAll did not report the failing package")
	}
}

// TestRunSkipsUnbuildablePackage proves a package whose files are all behind a
// build tag the run does not enable is skipped, not reported as a failure:
// naming such a directory makes `go test <dir>` fail with "build constraints
// exclude all Go files" while `go test ./...` silently skips it.
func TestRunSkipsUnbuildablePackage(t *testing.T) {
	dir := goModule(t)

	// A package that only exists under a tag the run does not pass.
	writeFile(t, dir, "pkg/tagged/x_test.go", "//go:build sometag\n\npackage tagged\n\nimport \"testing\"\n\nfunc TestTagged(t *testing.T) {}\n")

	results := Run(context.Background(), dir, map[string][]string{"pkg/tagged": {"TestTagged"}}, nil, false)

	if len(results) != 1 {
		t.Fatalf("results = %d want 1", len(results))
	}

	res := results[0]

	if !res.Skipped {
		t.Fatalf("pkg/tagged not skipped:\n%+v\n%s", res, res.Output)
	}

	// Skipping is not a failure: no error, and the skip is explained in the
	// captured output the renderer shows.
	if res.Err != nil {
		t.Fatalf("skipped package must not carry an error: %v", res.Err)
	}

	if !strings.Contains(res.Output, "no buildable Go files") {
		t.Fatalf("skip not explained in output:\n%s", res.Output)
	}
}
