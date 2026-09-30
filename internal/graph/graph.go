package graph

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type node struct {
	ID             string `json:"id"`
	Label          string `json:"label"`
	SourceFile     string `json:"source_file"`
	SourceLocation string `json:"source_location"`
}

type link struct {
	Relation string `json:"relation"`
	Source   string `json:"source"`
	Target   string `json:"target"`
}

type rawGraph struct {
	Nodes []node `json:"nodes"`
	Links []link `json:"links"`
}

// Graph is an in-memory view of graphify-out/graph.json.
type Graph struct {
	byID    map[string]*node
	byFile  map[string][]*node  // sorted by line
	callees map[string][]string // node id -> callee node ids (calls links only)
}

// Update runs `graphify update .` rooted at root, streaming its output to
// stderr.
func Update(root string) error {
	if _, err := exec.LookPath("graphify"); err != nil {
		return fmt.Errorf("graphify not found in PATH")
	}
	cmd := exec.Command("graphify", "update", ".")
	cmd.Dir = root
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("graphify update failed (if it refused to shrink after deleting code, run: graphify update . --force)")
	}
	return nil
}

// Load reads root/graphify-out/graph.json into a Graph.
func Load(root string) (*Graph, error) {
	data, err := os.ReadFile(filepath.Join(root, "graphify-out", "graph.json"))
	if err != nil {
		return nil, err
	}
	var rg rawGraph
	if err := json.Unmarshal(data, &rg); err != nil {
		return nil, fmt.Errorf("parse graph.json: %w", err)
	}
	g := &Graph{
		byID:    make(map[string]*node, len(rg.Nodes)),
		byFile:  map[string][]*node{},
		callees: map[string][]string{},
	}
	for i := range rg.Nodes {
		n := &rg.Nodes[i]
		g.byID[n.ID] = n
		if n.SourceFile != "" {
			g.byFile[n.SourceFile] = append(g.byFile[n.SourceFile], n)
		}
	}
	for _, ns := range g.byFile {
		sort.SliceStable(ns, func(i, j int) bool { return nodeLine(ns[i]) < nodeLine(ns[j]) })
	}
	for _, l := range rg.Links {
		if l.Relation == "calls" {
			g.callees[l.Source] = append(g.callees[l.Source], l.Target)
		}
	}
	return g, nil
}

func nodeLine(n *node) int {
	v, err := strconv.Atoi(strings.TrimPrefix(n.SourceLocation, "L"))
	if err != nil {
		return -1
	}
	return v
}

// Labels returns the label of every given node ID that exists.
func (g *Graph) Labels(ids map[string]bool) map[string][]string {
	byFile := map[string][]string{}
	for id := range ids {
		if n := g.byID[id]; n != nil {
			byFile[n.SourceFile] = append(byFile[n.SourceFile], n.Label)
		}
	}
	return byFile
}

// ChangedSymbols returns the set of node IDs touched by the change. For each
// changed file it marks every node whose line falls in a changed range plus the
// enclosing node (greatest line <= range start). The file-basename node is
// never marked.
func (g *Graph) ChangedSymbols(changed map[string][][2]int) map[string]bool {
	out := map[string]bool{}
	for file, ranges := range changed {
		nodes := g.byFile[file]
		if nodes == nil {
			continue
		}
		base := filepath.Base(file)
		for _, r := range ranges {
			for _, n := range nodes {
				if n.Label == base {
					continue
				}
				line := nodeLine(n)
				if line < 0 {
					continue
				}
				if line >= r[0] && line <= r[1] {
					out[n.ID] = true
				}
			}
			// enclosing node: greatest line <= r[0]
			var enc *node
			for _, n := range nodes {
				if n.Label == base || nodeLine(n) < 0 {
					continue
				}
				if nodeLine(n) <= r[0] {
					enc = n
				}
			}
			if enc != nil {
				out[enc.ID] = true
			}
		}
	}
	return out
}

const evidenceCap = 20

// Evidence returns direct and indirect changed-symbol labels reachable from the
// given callers. Direct = callee is itself changed; indirect = depth-2 path
// caller -> X -> changed where X is not changed.
func (g *Graph) Evidence(callerIDs []string, changedIDs map[string]bool) (direct, indirect []string) {
	directSet := map[string]bool{}
	indirectSet := map[string]bool{}
	for _, c := range callerIDs {
		for _, callee := range g.callees[c] {
			if changedIDs[callee] {
				if n := g.byID[callee]; n != nil {
					directSet[n.Label] = true
				}
				continue
			}
			mid := g.byID[callee]
			if mid == nil {
				continue
			}
			for _, callee2 := range g.callees[callee] {
				if changedIDs[callee2] {
					if n := g.byID[callee2]; n != nil {
						indirectSet[n.Label+" via "+mid.Label] = true
					}
				}
			}
		}
	}
	return cappedSorted(directSet), cappedSorted(indirectSet)
}

func cappedSorted(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	if len(out) > evidenceCap {
		out = append(out[:evidenceCap], "…")
	}
	return out
}

// NodeIDFor returns the graph node ID for test function name in path, or "".
func (g *Graph) NodeIDFor(path, name string) string {
	for _, n := range g.byFile[path] {
		if n.Label == name+"()" {
			return n.ID
		}
	}
	return ""
}

// FileCallers returns every node ID whose SourceFile is path.
func (g *Graph) FileCallers(path string) []string {
	var ids []string
	for _, n := range g.byFile[path] {
		ids = append(ids, n.ID)
	}
	return ids
}
