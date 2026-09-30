// Package gotest lists a repository's Go test files and functions, and runs a
// selected subset of them grouped by package.
package gotest

import (
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/NSXBet/go-smart-test-runner/internal/repo"
)

// snippetLimit caps a test function's source snippet, in bytes.
const snippetLimit = 1000

// Func is a top-level Go test function and its source snippet.
type Func struct {
	Name string
	Src  string
}

// Result is the outcome of running one package's tests.
type Result struct {
	// Dir is the repository-relative package directory.
	Dir string
	// Funcs are the tests that were requested (nil under --all, where no -run
	// filter is applied).
	Funcs []string
	// Output is the combined stdout/stderr of the go test invocation. It is
	// held so the caller can decide how to present it: the human report shows
	// it only for failures, keeping a green run quiet.
	Output string
	// Err is non-nil when the package failed to build or a test failed.
	Err error
	// Skipped is set when the package was not run at all (no files the current
	// build tags select); Err is nil and it is not a failure.
	Skipped bool
	// Duration is how long the package's go test invocation took.
	Duration time.Duration
	// DryRun marks a result that was planned but not executed; Output then
	// holds the command line that would have run.
	DryRun bool
}

// Ok reports whether the package's tests passed.
func (r Result) Ok() bool { return r.Err == nil && !r.Skipped }

// ListFiles returns tracked and untracked Go test files, excluding vendor and
// testdata trees.
func ListFiles(ctx context.Context, root string) ([]string, error) {
	out, err := repo.Run(ctx, root, "ls-files", "-co", "--exclude-standard", "--", "*_test.go")
	if err != nil {
		return nil, err
	}

	var files []string

	for line := range strings.SplitSeq(out, "\n") {
		p := strings.TrimSpace(line)
		if p == "" || isExcluded(p) {
			continue
		}

		files = append(files, p)
	}

	sort.Strings(files)

	return files, nil
}

// isExcluded reports whether a path lives under vendor or testdata.
func isExcluded(p string) bool {
	return strings.Contains(p, "/vendor/") || strings.HasPrefix(p, "vendor/") ||
		strings.Contains(p, "/testdata/") || strings.HasPrefix(p, "testdata/")
}

// IsTestName applies Go's test-function naming rule.
func IsTestName(name string) bool {
	if name == "Test" {
		return true
	}

	if !strings.HasPrefix(name, "Test") {
		return false
	}

	r, _ := utf8.DecodeRuneInString(name[len("Test"):])

	return !unicode.IsLower(r)
}

// Funcs parses root/path and returns top-level test functions with source
// snippets (truncated to snippetLimit bytes).
func Funcs(root, path string) ([]Func, error) {
	fset := token.NewFileSet()

	f, err := parser.ParseFile(fset, filepath.Join(root, path), nil, parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	src, err := os.ReadFile(filepath.Join(root, path))
	if err != nil {
		return nil, err
	}

	var out []Func

	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || fn.Name == nil || fn.Name.Name == "TestMain" || !IsTestName(fn.Name.Name) {
			continue
		}

		if snippet, ok := funcSnippet(fset, src, fn); ok {
			out = append(out, Func{Name: fn.Name.Name, Src: snippet})
		}
	}

	return out, nil
}

// funcSnippet slices a function's source, truncated to snippetLimit bytes.
func funcSnippet(fset *token.FileSet, src []byte, fn *ast.FuncDecl) (string, bool) {
	start := fset.Position(fn.Pos()).Offset
	end := fset.Position(fn.End()).Offset

	if start < 0 || end > len(src) || start >= end {
		return "", false
	}

	snippet := string(src[start:end])
	if len(snippet) > snippetLimit {
		snippet = snippet[:snippetLimit]
	}

	return snippet, true
}

// ModuleRoot walks up from root/dir looking for the nearest go.mod; returns
// root as a fallback.
func ModuleRoot(root, dir string) string {
	cur := filepath.Join(root, dir)

	for strings.HasPrefix(cur, root) {
		if _, err := os.Stat(filepath.Join(cur, "go.mod")); err == nil {
			return cur
		}

		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}

		cur = parent
	}

	return root
}

// Plan resolves what Run would do — which selected directories survive the
// caller's targets, which of those the current build tags can build, and which
// tests each one requests — without running any tests. Run consumes it, and
// callers describe the run from it, so the description and the outcome cannot
// disagree about how many packages are involved.
//
// The buildability probe is one `go list` per directory. It is paid here rather
// than discovered mid-run so that a skipped package is known before the header
// counts it.
func Plan(ctx context.Context, root string, selected map[string][]string, extra []string) []Result {
	// Separate the caller's go test flags from any package targets they named.
	// Flags are forwarded to each invocation; a target narrows which selected
	// directories run at all. Appending the target as well would run the whole
	// named tree on every invocation (and make the per-directory package
	// argument redundant), which is the bug this split exists to prevent.
	_, targets := splitArgs(extra)

	dirs := make([]string, 0, len(selected))
	for d := range selected {
		if !matchesTargets(d, targets) {
			continue
		}

		dirs = append(dirs, d)
	}

	sort.Strings(dirs)

	results := make([]Result, 0, len(dirs))

	for _, dir := range dirs {
		names := append([]string(nil), selected[dir]...)
		sort.Strings(names)

		res := Result{Dir: dir, Funcs: names}

		if !buildable(ctx, ModuleRoot(root, dir), patternFor(root, dir)) {
			res.Skipped = true
			res.Output = "no buildable Go files with the current flags"
		}

		results = append(results, res)
	}

	return results
}

// patternFor renders the go test package pattern for dir.
func patternFor(root, dir string) string {
	m := ModuleRoot(root, dir)

	rel, err := filepath.Rel(m, filepath.Join(root, dir))
	if err != nil {
		rel = dir
	}

	return "./" + filepath.ToSlash(rel)
}

// Run runs the selected tests grouped by package directory and returns one
// Result per package, in directory order. Callers render the results; nothing
// is written to the terminal here, so a green run can stay quiet.
func Run(ctx context.Context, root string, selected map[string][]string, extra []string, dryRun bool) []Result {
	flags, _ := splitArgs(extra)

	results := Plan(ctx, root, selected, extra)

	for i := range results {
		if results[i].Skipped {
			continue
		}

		results[i] = runDir(ctx, root, results[i].Dir, results[i].Funcs, flags, dryRun)
	}

	return results
}

// RunAll runs the test suite from the repository root with no diff, graph or
// -run filter — the --all path, where the tool is a passthrough to go test
// rather than a selector.
//
// Args are forwarded verbatim so the caller keeps every go test capability:
// flags (`-race -count=1`), a -run filter, package patterns, or a specific
// directory. Only when no package target is given does it default to `./...`,
// which is what makes a bare `--all` mean "everything".
func RunAll(ctx context.Context, root string, extra []string, dryRun bool) Result {
	args := append([]string{"test"}, extra...)
	if !hasTarget(extra) {
		args = append(args, "./...")
	}

	res := Result{Dir: "."}

	if dryRun {
		res.DryRun = true
		res.Output = "go " + strings.Join(args, " ")

		return res
	}

	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = ModuleRoot(root, ".")

	var buf bytes.Buffer

	cmd.Stdout = &buf
	cmd.Stderr = &buf

	if err := cmd.Run(); err != nil {
		res.Err = err
	}

	res.Output = buf.String()

	return res
}

// runDir runs one package's selected tests and captures the outcome.
func runDir(ctx context.Context, root, dir string, names, flags []string, dryRun bool) Result {
	names = append([]string(nil), names...)
	sort.Strings(names)

	res := Result{Dir: dir, Funcs: names}

	m := ModuleRoot(root, dir)
	pattern := patternFor(root, dir)

	args := append([]string{"test"}, flags...)
	if len(names) > 0 {
		args = append(args, "-run", `^(`+strings.Join(names, "|")+`)$`)
	}

	args = append(args, pattern)

	if dryRun {
		res.DryRun = true
		res.Output = "go " + strings.Join(args, " ")

		return res
	}

	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = m

	var buf bytes.Buffer

	cmd.Stdout = &buf
	cmd.Stderr = &buf

	start := time.Now()

	if rerr := cmd.Run(); rerr != nil {
		res.Err = rerr
	}

	res.Duration = time.Since(start)
	res.Output = buf.String()

	return res
}

// splitArgs separates go test flags from package targets. A target is a path
// (starts with . / ~, contains /, or ends in .go); anything else non-dash is a
// flag value and stays with the flags.
func splitArgs(extra []string) (flags, targets []string) {
	for _, a := range extra {
		if isTarget(a) {
			targets = append(targets, a)

			continue
		}

		flags = append(flags, a)
	}

	return flags, targets
}

// hasTarget reports whether any arg names a package or file to run.
func hasTarget(args []string) bool {
	return slices.ContainsFunc(args, isTarget)
}

// isTarget reports whether a non-flag argument names a package or file.
func isTarget(a string) bool {
	if strings.HasPrefix(a, "-") {
		return false
	}

	return strings.HasPrefix(a, ".") || strings.HasPrefix(a, "/") || strings.HasPrefix(a, "~") ||
		strings.Contains(a, "/") || strings.HasSuffix(a, ".go")
}

// matchesTargets reports whether a selected directory is covered by the
// caller's targets. With no targets, every selected directory runs.
func matchesTargets(dir string, targets []string) bool {
	if len(targets) == 0 {
		return true
	}

	for _, t := range targets {
		// ".", "./...", or a path containing the directory covers it.
		if t == "." || t == "./..." || strings.Contains(t, dir) {
			return true
		}
	}

	return false
}

// buildable reports whether a package directory has any Go file the current
// build tags select. A directory whose every file sits behind a tag the run does
// not enable (e2e/, integration/) makes `go test <dir>` fail with "build
// constraints exclude all Go files", while `go test ./...` silently skips it —
// naming the directory is the difference, so selection must not emit a command
// that cannot succeed.
//
// The caller's flags are deliberately NOT passed here: an invalid flag would
// make `go list` fail and be misread as "unbuildable", silently skipping a
// package instead of surfacing the bad flag to the user.
func buildable(ctx context.Context, m, pattern string) bool {
	cmd := exec.CommandContext(ctx, "go", "list", "-find", pattern)
	cmd.Dir = m

	var out bytes.Buffer

	cmd.Stdout = &out
	cmd.Stderr = io.Discard

	if err := cmd.Run(); err != nil {
		return false
	}

	// `go list -find` prints the package when it has buildable files, and
	// nothing when the build constraints exclude them all.
	return strings.TrimSpace(out.String()) != ""
}
