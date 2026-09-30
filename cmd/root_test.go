package cmd

import (
	"strings"
	"testing"

	"github.com/NSXBet/go-graphify-test-runner/internal/decide"
	"github.com/NSXBet/go-graphify-test-runner/internal/gotest"
	"github.com/NSXBet/go-graphify-test-runner/internal/graph"
)

func TestTruncate(t *testing.T) {
	if got := truncate("hello", 10); got != "hello" {
		t.Fatalf("truncate short = %q", got)
	}

	if got := truncate("hello", 2); got != "he" {
		t.Fatalf("truncate long = %q", got)
	}

	if got := truncate("", 5); got != "" {
		t.Fatalf("truncate empty = %q", got)
	}
}

func TestShort(t *testing.T) {
	full := "0123456789abcdef"

	if got := short(full); got != "0123456789ab" {
		t.Fatalf("short(full) = %q", got)
	}

	if got := short("abc"); got != "abc" {
		t.Fatalf("short(short) = %q", got)
	}
}

func TestOrNone(t *testing.T) {
	if got := orNone(nil); got != "none" {
		t.Fatalf("orNone(nil) = %q", got)
	}

	if got := orNone([]string{"A()", "B()"}); got != "A(), B()" {
		t.Fatalf("orNone = %q", got)
	}
}

func TestBuildStateIncludesSections(t *testing.T) {
	g := &graph.Graph{}
	state := buildState("0123456789abcdef", []string{"pkg/a.go"}, g, map[string]bool{}, "the diff body")

	for _, want := range []string{"merge-base 0123456789ab", "Changed files:", "pkg/a.go", "Changed symbols", "the diff body"} {
		if !strings.Contains(state, want) {
			t.Fatalf("state missing %q:\n%s", want, state)
		}
	}
}

func TestBuildStateTruncatesDiff(t *testing.T) {
	g := &graph.Graph{}
	huge := strings.Repeat("x", decide.MaxStateChars*2)

	state := buildState("abc", []string{"pkg/a.go"}, g, map[string]bool{}, huge)
	if len(state) > decide.MaxStateChars {
		t.Fatalf("state len = %d want <= %d", len(state), decide.MaxStateChars)
	}

	if !strings.Contains(state, "[diff truncated]") {
		t.Fatalf("state missing truncation marker")
	}
}

func TestPromptR1(t *testing.T) {
	got := promptR1("pkg/a_test.go", "pkg", true, []string{"TestA"}, []string{"A()"}, nil)

	for _, want := range []string{"pkg/a_test.go", "same directory as a changed file: yes", "TestA", "A()", "indirectly: none"} {
		if !strings.Contains(got, want) {
			t.Fatalf("promptR1 missing %q:\n%s", want, got)
		}
	}

	no := promptR1("pkg/b_test.go", "pkg", false, nil, nil, nil)
	if !strings.Contains(no, "same directory as a changed file: no") {
		t.Fatalf("promptR1 sameDir=false wrong:\n%s", no)
	}
}

func TestPromptR1CapsNames(t *testing.T) {
	names := make([]string, 0, maxNamesShown+10)
	for i := range maxNamesShown + 10 {
		names = append(names, string(rune('A'+i%26)))
	}

	got := promptR1("pkg/a_test.go", "pkg", false, names, nil, nil)
	if !strings.Contains(got, "…") {
		t.Fatalf("promptR1 did not cap names:\n%s", got)
	}
}

func TestPromptR2(t *testing.T) {
	fn := gotest.Func{Name: "TestAdd", Src: "func TestAdd(t *testing.T) {}"}
	got := promptR2("pkg/a_test.go", fn, []string{"Add()"}, []string{"X() via Y()"})

	for _, want := range []string{"TestAdd", "pkg/a_test.go", "Add()", "X() via Y()", fn.Src} {
		if !strings.Contains(got, want) {
			t.Fatalf("promptR2 missing %q:\n%s", want, got)
		}
	}
}

func TestSelectedTests(t *testing.T) {
	items := []r2item{
		{path: "pkg/a/a_test.go", fn: gotest.Func{Name: "TestA"}},
		{path: "pkg/a/a_test.go", fn: gotest.Func{Name: "TestB"}},
		{path: "pkg/b/b_test.go", fn: gotest.Func{Name: "TestC"}},
	}
	r2 := map[string]float64{
		"pkg/a/a_test.go::TestA": 0.9,
		"pkg/a/a_test.go::TestB": 0.1,
		"pkg/b/b_test.go::TestC": 0.2,
	}

	got := selectedTests(r2, items, 0.5)
	if len(got) != 1 || len(got["pkg/a"]) != 1 || got["pkg/a"][0] != "TestA" {
		t.Fatalf("selected = %v", got)
	}
}
