package gotest

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/NSXBet/go-graphify-test-runner/internal/repo"
)

// Func is a top-level Go test function and its source snippet.
type Func struct {
	Name string
	Src  string
}

// ListFiles returns tracked and untracked Go test files, excluding vendor and
// testdata trees.
func ListFiles(root string) ([]string, error) {
	out, err := repo.Run(root, "ls-files", "-co", "--exclude-standard", "--", "*_test.go")
	if err != nil {
		return nil, err
	}
	var files []string
	for _, line := range strings.Split(out, "\n") {
		p := strings.TrimSpace(line)
		if p == "" {
			continue
		}
		if strings.Contains(p, "/vendor/") || strings.HasPrefix(p, "vendor/") || strings.Contains(p, "/testdata/") || strings.HasPrefix(p, "testdata/") {
			continue
		}
		files = append(files, p)
	}
	sort.Strings(files)
	return files, nil
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
// snippets (truncated to 1000 bytes).
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
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Recv != nil || fd.Name == nil {
			continue
		}
		name := fd.Name.Name
		if name == "TestMain" || !IsTestName(name) {
			continue
		}
		start := fset.Position(fd.Pos()).Offset
		end := fset.Position(fd.End()).Offset
		if start < 0 || end > len(src) || start >= end {
			continue
		}
		snippet := string(src[start:end])
		if len(snippet) > 1000 {
			snippet = snippet[:1000]
		}
		out = append(out, Func{Name: name, Src: snippet})
	}
	return out, nil
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

// Run runs the selected tests grouped by package directory. Returns the process
// exit code (0 ok, 1 any failure).
func Run(root string, selected map[string][]string, extra []string, dryRun bool) int {
	dirs := make([]string, 0, len(selected))
	for d := range selected {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	code := 0
	for _, dir := range dirs {
		names := append([]string(nil), selected[dir]...)
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
			fmt.Println("go " + strings.Join(args, " "))
			continue
		}
		cmd := exec.Command("go", args...)
		cmd.Dir = m
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "go test %s failed: %v\n", pattern, err)
			code = 1
		}
	}
	return code
}
