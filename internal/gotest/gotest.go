// Package gotest discovers Go test files and functions and runs a selected set
// of tests grouped by package directory.
package gotest

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
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

// Run runs the selected tests grouped by package directory. Command lines and
// subprocess output are written to out (os.Stdout in normal use; os.Stderr when
// --json reserves stdout for the JSON document). It returns the process exit
// code (0 ok, 1 any failure).
func Run(ctx context.Context, root string, selected map[string][]string, extra []string, dryRun bool, out io.Writer) int {
	if out == nil {
		out = os.Stdout
	}

	dirs := make([]string, 0, len(selected))
	for d := range selected {
		dirs = append(dirs, d)
	}

	sort.Strings(dirs)

	code := 0

	for _, dir := range dirs {
		if !runDir(ctx, root, dir, selected[dir], extra, dryRun, out) {
			code = 1
		}
	}

	return code
}

// RunAll runs the test suite from the repository root with no diff, graph or
// -run filter — the --all path, where the tool is a passthrough to go test
// rather than a selector.
//
// Args are forwarded verbatim so the caller keeps every go test capability:
// flags (`-race -count=1`), a -run filter, package patterns, or a specific
// directory. Only when no package target is given does it default to `./...`,
// which is what makes a bare `--all` mean "everything".
func RunAll(ctx context.Context, root string, extra []string, dryRun bool, out io.Writer) int {
	if out == nil {
		out = os.Stdout
	}

	args := append([]string{"test"}, extra...)
	if !hasPackageTarget(extra) {
		args = append(args, "./...")
	}

	if dryRun {
		fmt.Fprintln(out, "go "+strings.Join(args, " "))

		return 0
	}

	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = ModuleRoot(root, ".")
	cmd.Stdout = out
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return 1
	}

	return 0
}

// hasPackageTarget reports whether the go test args already name what to run.
//
// Deliberately narrow: a package pattern is an absolute or relative path
// (starts with . / ~ or contains a /), or a single .go file. A bare word is NOT
// treated as a target, because that is how a flag value looks —
// `-run TestFoo` must still get a `./...` appended, and `-race` must not be
// mistaken for a package. This is what keeps `--all -- ./pkg/mine` verbatim
// while a bare `--all` or `--all -- -race` means everything.
func hasPackageTarget(args []string) bool {
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}

		if strings.HasPrefix(a, ".") || strings.HasPrefix(a, "/") || strings.HasPrefix(a, "~") ||
			strings.Contains(a, "/") || strings.HasSuffix(a, ".go") {
			return true
		}
	}

	return false
}

// runDir runs one package's selected tests; returns false on failure.
func runDir(ctx context.Context, root, dir string, names, extra []string, dryRun bool, out io.Writer) bool {
	names = append([]string(nil), names...)
	sort.Strings(names)

	pkgDir := filepath.Join(root, dir)
	m := ModuleRoot(root, dir)

	rel, err := filepath.Rel(m, pkgDir)
	if err != nil {
		rel = pkgDir
	}

	pattern := "./" + filepath.ToSlash(rel)
	run := `^(` + strings.Join(names, "|") + `)$`

	args := append([]string{"test"}, extra...)
	args = append(args, "-run", run, pattern)

	if dryRun {
		fmt.Fprintln(out, "go "+strings.Join(args, " "))

		return true
	}

	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = m
	cmd.Stdout = out
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "go test %s failed: %v\n", pattern, err)

		return false
	}

	return true
}
