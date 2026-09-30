package gotest

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
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
	if err := os.WriteFile(filepath.Join(dir, "x_test.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	funcs, err := Funcs(dir, "x_test.go")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range funcs {
		names = append(names, f.Name)
	}
	sort.Strings(names)
	want := []string{"Test", "TestA", "Test_b"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("got %v want %v", names, want)
	}
}
