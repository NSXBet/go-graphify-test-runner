package graph

import (
	"reflect"
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
