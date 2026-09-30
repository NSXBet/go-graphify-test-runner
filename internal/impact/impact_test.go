package impact

import (
	"context"
	"os/exec"
	"testing"
)

// TestUpdateMissingGrove proves the package reports a clear error when grove is
// not on PATH, rather than failing obscurely.
func TestUpdateMissingGrove(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	err := Update(context.Background(), t.TempDir())
	if err == nil {
		t.Fatal("want an error when grove is absent")
	}

	if _, lookErr := exec.LookPath("grove"); lookErr == nil {
		t.Skip("grove present on PATH; cannot exercise the missing case")
	}
}

// TestLoadMissingIndex proves Load names the missing index instead of returning
// a graph that will fail on first query.
func TestLoadMissingIndex(t *testing.T) {
	_, err := Load(context.Background(), t.TempDir())
	if err == nil {
		t.Fatal("want an error when the grove index is missing")
	}
}

func TestCappedSortsDedupsAndTruncates(t *testing.T) {
	in := []string{"b", "a", "b"}
	if got := capped(in); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("capped = %v want [a b]", got)
	}

	many := make([]string, 0, evidenceCap+5)
	for i := range evidenceCap + 5 {
		many = append(many, string(rune('a'+i)))
	}

	got := capped(many)
	if len(got) != evidenceCap+1 || got[len(got)-1] != "…" {
		t.Fatalf("capped did not truncate with an ellipsis: len=%d last=%q", len(got), got[len(got)-1])
	}
}

func TestDirectCallsMatchesPackagePrefix(t *testing.T) {
	n := IndexedNode{
		FilePath:  "pkg/logx/default.go",
		CallSites: []callSite{{Callee: "envx.IsDeployed"}, {Callee: "os.Getenv"}, {Callee: "envx.DetectSkin"}},
	}

	got := packageCalls(&n, "envx")
	if len(got) != 2 || got[0] != "envx.IsDeployed" || got[1] != "envx.DetectSkin" {
		t.Fatalf("directCalls = %v want the two envx calls", got)
	}

	if other := packageCalls(&n, "other"); len(other) != 0 {
		t.Fatalf("directCalls for an unrelated package = %v want none", other)
	}
}

// TestIndexChangedGradesSiblingPackageCallDirect is the regression guard for a
// discarded map write: the propagation of a package's direct calls to its other
// impacted files appended to a copy and never stored it back, so every reacher
// was graded transitive-only.
func TestIndexChangedGradesSiblingPackageCallDirect(t *testing.T) {
	g := &Graph{reachers: map[string]map[string]Reach{}}

	nodes := []IndexedNode{
		{FilePath: "pkg/awsx/session.go", CallSites: []callSite{{Callee: "envx.IsDeployed"}}},
		{FilePath: "pkg/awsx/session_test.go"},
		{FilePath: "pkg/other/thing.go"},
	}

	g.indexOne("pkg/envx/detector.go", nodes)

	// The test file itself has no call site, but its package does.
	got := g.Reaches("pkg/awsx/session_test.go")
	if len(got) != 1 || got[0].File != "pkg/envx/detector.go" {
		t.Fatalf("Reaches = %+v want one reach for pkg/envx/detector.go", got)
	}

	if len(got[0].Direct) == 0 {
		t.Fatalf("sibling-package call not graded direct: %+v", got[0])
	}

	if got[0].Direct[0] != "envx.IsDeployed" {
		t.Fatalf("Direct = %v want [envx.IsDeployed]", got[0].Direct)
	}

	// A file in an unrelated package must stay transitive-only.
	other := g.Reaches("pkg/other/thing.go")
	if len(other) != 1 || len(other[0].Direct) != 0 {
		t.Fatalf("unrelated package graded direct: %+v", other)
	}
}
