package gotest

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestFuncs(t *testing.T) {
	src := `package x

import "testing"

func Test(t *testing.T) {}
func TestA(t *testing.T) {}
func Test_b(t *testing.T) {}
func Testfoo(t *testing.T) {}
func TestMain(m *testing.M) {}
func helper() {}
func (s S) TestM(t *testing.T) {}
`
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "x_test.go"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}

	funcs, err := Funcs(dir, "x_test.go")
	if err != nil {
		t.Fatal(err)
	}

	names := make([]string, 0, len(funcs))
	for _, f := range funcs {
		names = append(names, f.Name)
	}

	sort.Strings(names)

	want := []string{"Test", "TestA", "Test_b"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("got %v want %v", names, want)
	}
}

func TestIsTestNameTable(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"Test", true},
		{"TestA", true},
		{"Test_1", true},
		{"Testfoo", false},
		{"Testing", false},
		{"Test1", true},
		{"helper", false},
		{"", false},
		{"Test_main", true}, // '_' is not a lowercase letter
	}

	for _, tt := range tests {
		if got := IsTestName(tt.name); got != tt.want {
			t.Errorf("IsTestName(%q) = %v want %v", tt.name, got, tt.want)
		}
	}
}

func TestFuncsParseError(t *testing.T) {
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "bad_test.go"), []byte("package x\nfunc ("), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Funcs(dir, "bad_test.go"); err == nil {
		t.Fatal("want parse error")
	}
}

func TestFuncsSnippetTruncated(t *testing.T) {
	var b strings.Builder
	b.WriteString("package x\n\nfunc TestBig(t *testing.T) {\n")

	for range 200 {
		b.WriteString("\t_ = 1\n")
	}

	b.WriteString("}\n")

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "big_test.go"), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}

	funcs, err := Funcs(dir, "big_test.go")
	if err != nil {
		t.Fatal(err)
	}

	if len(funcs) != 1 {
		t.Fatalf("funcs = %d want 1", len(funcs))
	}

	if len(funcs[0].Src) != snippetLimit {
		t.Fatalf("snippet len = %d want %d", len(funcs[0].Src), snippetLimit)
	}
}

func TestModuleRootNestedModule(t *testing.T) {
	root := t.TempDir()

	nested := filepath.Join(root, "sub", "pkg")
	if err := os.MkdirAll(nested, 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module root\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(root, "sub", "go.mod"), []byte("module sub\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if got := ModuleRoot(root, filepath.Join("sub", "pkg")); got != filepath.Join(root, "sub") {
		t.Fatalf("ModuleRoot = %q want %q", got, filepath.Join(root, "sub"))
	}

	if got := ModuleRoot(root, "sub"); got != filepath.Join(root, "sub") {
		t.Fatalf("ModuleRoot(sub) = %q", got)
	}
}

func TestModuleRootFallback(t *testing.T) {
	root := t.TempDir()

	if got := ModuleRoot(root, "."); got != root {
		t.Fatalf("ModuleRoot = %q want %q", got, root)
	}
}

func TestIsExcluded(t *testing.T) {
	tests := map[string]bool{
		"pkg/x_test.go":             false,
		"vendor/a/x_test.go":        true,
		"pkg/vendor/a/x_test.go":    true,
		"pkg/testdata/x_test.go":    true,
		"testdata/a/x_test.go":      true,
		"pkg/testdata_helpers/x.go": false,
	}

	for path, want := range tests {
		if got := isExcluded(path); got != want {
			t.Errorf("isExcluded(%q) = %v want %v", path, got, want)
		}
	}
}
