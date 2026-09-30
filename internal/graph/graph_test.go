package graph

import (
	"reflect"
	"strconv"
	"testing"
)

func testGraph() *Graph {
	nodes := []node{
		{ID: "f", Label: "a_test.go", SourceFile: "pkg/a_test.go", SourceLocation: "L1"},
		{ID: "A", Label: "A()", SourceFile: "pkg/a_test.go", SourceLocation: "L10"},
		{ID: "B", Label: "B()", SourceFile: "pkg/a_test.go", SourceLocation: "L30"},
		{ID: "T", Label: "TestT()", SourceFile: "pkg/a_test.go", SourceLocation: "L50"},
		{ID: "X", Label: "X()", SourceFile: "pkg/a.go", SourceLocation: "L5"},
	}
	links := []link{
		{Relation: "calls", Source: "T", Target: "X"},
		{Relation: "calls", Source: "X", Target: "A"},
	}

	g := &Graph{byID: map[string]*node{}, byFile: map[string][]*node{}, callees: map[string][]string{}}

	for i := range nodes {
		n := &nodes[i]
		g.byID[n.ID] = n
		g.byFile[n.SourceFile] = append(g.byFile[n.SourceFile], n)
	}

	for _, l := range links {
		g.callees[l.Source] = append(g.callees[l.Source], l.Target)
	}

	return g
}

func TestChangedSymbols(t *testing.T) {
	g := testGraph()

	changed := g.ChangedSymbols(map[string][][2]int{"pkg/a_test.go": {{12, 14}}})
	if !changed["A"] {
		t.Fatalf("A not marked: %v", changed)
	}

	if changed["B"] || changed["f"] {
		t.Fatalf("B or file node wrongly marked: %v", changed)
	}
}

func TestEvidence(t *testing.T) {
	g := testGraph()

	direct, indirect := g.Evidence([]string{"T"}, map[string]bool{"A": true})
	if len(direct) != 0 {
		t.Fatalf("direct = %v want empty", direct)
	}

	if !reflect.DeepEqual(indirect, []string{"A() via X()"}) {
		t.Fatalf("indirect = %v", indirect)
	}
}

func TestEvidenceDirect(t *testing.T) {
	g := testGraph()

	direct, indirect := g.Evidence([]string{"X"}, map[string]bool{"A": true})
	if !reflect.DeepEqual(direct, []string{"A()"}) {
		t.Fatalf("direct = %v want [A()]", direct)
	}

	if len(indirect) != 0 {
		t.Fatalf("indirect = %v want empty", indirect)
	}
}

func TestEvidenceCap(t *testing.T) {
	g := &Graph{byID: map[string]*node{}, byFile: map[string][]*node{}, callees: map[string][]string{}}

	changed := map[string]bool{}

	for i := range 25 {
		id := "n" + strconv.Itoa(i)
		g.byID[id] = &node{ID: id, Label: "Fn" + strconv.Itoa(i) + "()"}
		g.callees["caller"] = append(g.callees["caller"], id)
		changed[id] = true
	}

	direct, _ := g.Evidence([]string{"caller"}, changed)
	if len(direct) != evidenceCap+1 {
		t.Fatalf("direct len = %d want %d (cap + ellipsis)", len(direct), evidenceCap+1)
	}

	if direct[len(direct)-1] != "…" {
		t.Fatalf("last entry = %q want ellipsis", direct[len(direct)-1])
	}
}

func TestNodeLineUnparsable(t *testing.T) {
	if got := nodeLine(&node{SourceLocation: "nope"}); got != -1 {
		t.Fatalf("nodeLine = %d want -1", got)
	}

	// A node with an unparsable line must never be marked changed.
	g := &Graph{
		byID:    map[string]*node{"bad": {ID: "bad", Label: "Bad()", SourceFile: "pkg/x.go", SourceLocation: "nope"}},
		byFile:  map[string][]*node{"pkg/x.go": {{ID: "bad", Label: "Bad()", SourceFile: "pkg/x.go", SourceLocation: "nope"}}},
		callees: map[string][]string{},
	}

	if changed := g.ChangedSymbols(map[string][][2]int{"pkg/x.go": {{1, 50}}}); len(changed) != 0 {
		t.Fatalf("unparsable-line node marked changed: %v", changed)
	}
}

func TestEnclosingNodeMarked(t *testing.T) {
	// A change inside a function body (between B and the next declaration)
	// must mark the enclosing function, not just the exact hit node.
	g := testGraph()

	changed := g.ChangedSymbols(map[string][][2]int{"pkg/a_test.go": {{31, 31}}})
	if !changed["B"] {
		t.Fatalf("enclosing node B not marked: %v", changed)
	}
}

func TestNodeIDForAndFileCallers(t *testing.T) {
	g := testGraph()

	if id := g.NodeIDFor("pkg/a_test.go", "TestT"); id != "T" {
		t.Fatalf("NodeIDFor = %q want T", id)
	}

	if id := g.NodeIDFor("pkg/a_test.go", "Missing"); id != "" {
		t.Fatalf("NodeIDFor missing = %q want empty", id)
	}

	callers := g.FileCallers("pkg/a_test.go")
	if len(callers) != 4 {
		t.Fatalf("FileCallers = %v want 4 ids", callers)
	}
}

func TestLabelsMissingNodeSkipped(t *testing.T) {
	g := testGraph()

	got := g.Labels(map[string]bool{"A": true, "ghost": true})
	want := map[string][]string{"pkg/a_test.go": {"A()"}}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}
