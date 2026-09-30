// Package impact answers "what does this change affect?" using the Grove code
// graph (https://github.com/provasign/grove).
//
// Grove indexes a repository into a local SQLite store and exposes
// `grove impact <file>`, which returns the reverse-dependency closure of a file
// — the symbols (and test functions) that reach it, transitively. That closure
// is exactly the evidence the decision model needs, so this package replaces
// the previous hand-rolled graph traversal.
package impact

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// IndexDir is the directory Grove writes its SQLite index into.
const IndexDir = ".grove"

// callSite is one call Grove recorded inside a symbol.
type callSite struct {
	Callee string `json:"callee"`
}

// IndexedNode is one symbol Grove reports as impacted by a file.
type IndexedNode struct {
	FilePath      string     `json:"filePath"`
	Name          string     `json:"name"`
	Kind          string     `json:"kind"`
	Signature     string     `json:"signature"`
	Imports       []string   `json:"imports"`
	CallSites     []callSite `json:"callSites"`
	RawText       string     `json:"rawText"`
	QualifiedName string     `json:"qualifiedName"`
}

// impactResponse is the JSON shape of `grove impact`.
type impactResponse struct {
	Nodes []IndexedNode `json:"nodes"`
}

// Reach records how a file reaches a changed file: directly (a named call into
// the changed package) or transitively (via other symbols).
type Reach struct {
	File string
	// Direct lists the call edges into the changed file's package, e.g.
	// "envx.IsDeployed". Empty means the reach is transitive only.
	Direct []string
}

// Graph is the Grove-backed view of the repository.
type Graph struct {
	root string

	// reachers maps an impacted file to how it reaches each changed file, built
	// by running `grove impact` once per changed file. Grove's `impact` answers
	// "what reaches this file", so the query direction is changed-file ->
	// impacted-file; the map is then read backwards for a test file.
	reachers map[string]map[string]Reach
}

// NativeEnv opts into Grove's native type analyzer. It is off by default: the
// analyzer panics on any module whose dependencies export Go 1.27 type data
// ("export data version 4 is greater than maximum supported version 2"), and
// even a trivial fixture trips it. The tree-sitter path still resolves
// cross-package calls, so nothing is lost until that is fixed upstream.
const NativeEnv = "SMART_TEST_RUNNER_GROVE_NATIVE"

// indexArgs builds the `grove index` argument list.
func indexArgs() []string {
	args := []string{"index"}
	if os.Getenv(NativeEnv) == "" {
		args = append(args, "--no-native")
	}

	return append(args, ".")
}

// Update runs `grove index .` rooted at root, streaming its output to stderr.
// Grove indexes incrementally (by content hash), so a warm run is a no-op.
func Update(ctx context.Context, root string) error {
	if _, err := exec.LookPath("grove"); err != nil {
		return errors.New("grove not found in PATH (install: https://github.com/provasign/grove)")
	}

	// argv, never a shell; indexArgs() is a fixed list.
	cmd := exec.CommandContext(ctx, "grove", indexArgs()...) //nolint:gosec // fixed argv, no shell
	cmd.Dir = root
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("grove index failed: %w", err)
	}

	return nil
}

// Load prepares a graph handle for root, ensuring an index exists.
func Load(_ context.Context, root string) (*Graph, error) {
	if _, err := os.Stat(filepath.Join(root, IndexDir)); err != nil {
		return nil, fmt.Errorf("grove index missing under %s (%s) - run grove index", root, IndexDir)
	}

	return &Graph{root: root, reachers: map[string]map[string]Reach{}}, nil
}

// IndexChanged records which files reach each changed path, by running
// `grove impact` once per changed file. Call it before reading Reaches.
//
// A node counts as a *direct* reacher when one of its call sites names a symbol
// in the changed file's package (envx.IsDeployed); otherwise it reaches the
// file transitively through other symbols. Keeping the two apart is the point:
// on a leaf package like envx nearly every dependent transitively reaches it,
// so a flat list buries the few hundred direct callers.
func (g *Graph) IndexChanged(ctx context.Context, changed []string) error {
	for _, file := range changed {
		nodes, err := g.impactFile(ctx, file)
		if err != nil {
			return err
		}

		g.indexOne(file, nodes)
	}

	return nil
}

// indexOne folds one changed file's impact into the reachers map.
func (g *Graph) indexOne(file string, nodes []IndexedNode) {
	// Call sites name the *package* (envx.IsDeployed), not the file
	// (detector.go), so the caller's directory identifies the calling package.
	base := path.Base(path.Dir(file))
	callingPkgs := map[string][]string{}

	for i := range nodes {
		n := &nodes[i]
		if n.FilePath == "" || n.FilePath == file {
			continue
		}

		if calls := packageCalls(n, base); len(calls) > 0 {
			dir := path.Dir(n.FilePath)
			callingPkgs[dir] = append(callingPkgs[dir], calls...)
		}

		g.reach(n.FilePath, file)
	}

	// A reacher in a calling package is direct even when the call site sits in a
	// sibling file: awsx/session_test.go exercises awsx/session.go, which calls
	// envx.IsDeployed, so the test must not be graded transitive-only.
	for impacted := range g.reachers {
		calls := callingPkgs[path.Dir(impacted)]
		if len(calls) == 0 {
			continue
		}

		r := g.reachers[impacted][file]
		r.Direct = append(r.Direct, calls...)
		g.reachers[impacted][file] = r
	}
}

// reach records that impacted reaches file (Direct filled in by indexOne).
func (g *Graph) reach(impacted, file string) {
	if g.reachers[impacted] == nil {
		g.reachers[impacted] = map[string]Reach{}
	}

	r := g.reachers[impacted][file]
	r.File = file
	g.reachers[impacted][file] = r
}

// packageCalls returns the call sites in n that name a symbol in package base
// (e.g. "envx.IsDeployed" for base == "envx").
func packageCalls(n *IndexedNode, base string) []string {
	var out []string

	for _, c := range n.CallSites {
		if strings.HasPrefix(c.Callee, base+".") {
			out = append(out, c.Callee)
		}
	}

	return out
}

// Reaches returns, per changed file, how path reaches it. Sorted for stability.
func (g *Graph) Reaches(file string) []Reach {
	byFile := g.reachers[file]
	if byFile == nil {
		return nil
	}

	out := make([]Reach, 0, len(byFile))
	for _, r := range byFile {
		r.Direct = dedupSorted(r.Direct)
		out = append(out, r)
	}

	sort.Slice(out, func(i, j int) bool {
		// Direct reachers first: they are the stronger evidence.
		if (len(out[i].Direct) > 0) != (len(out[j].Direct) > 0) {
			return len(out[i].Direct) > 0
		}

		return out[i].File < out[j].File
	})

	if len(out) > evidenceCap {
		out = append(out[:evidenceCap], Reach{File: "…"})
	}

	return out
}

// dedupSorted sorts, de-duplicates and truncates a label list.
func dedupSorted(in []string) []string {
	if len(in) == 0 {
		return nil
	}

	sort.Strings(in)

	out := in[:1]

	for _, v := range in[1:] {
		if v != out[len(out)-1] {
			out = append(out, v)
		}
	}

	if len(out) > evidenceCap {
		out = append(out[:evidenceCap], "…")
	}

	return out
}

// impactFile runs `grove impact <path>` and returns the symbols that reach it.
func (g *Graph) impactFile(ctx context.Context, file string) ([]IndexedNode, error) {
	out, err := g.run(ctx, "impact", file, ".")
	if err != nil {
		return nil, err
	}

	var resp impactResponse
	if jerr := json.Unmarshal(out, &resp); jerr != nil {
		return nil, fmt.Errorf("grove impact %s: decode: %w", file, jerr)
	}

	return resp.Nodes, nil
}

// run executes a grove subcommand rooted at the repo and returns stdout.
//
// The repo-relative path is passed as a separate argv element (never a shell),
// so a path cannot inject a command; gosec's G204 taint heuristic cannot see
// that through the variadic args.
func (g *Graph) run(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "grove", args...)
	cmd.Dir = g.root

	var stdout, stderr bytes.Buffer

	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("grove %s: %s", strings.Join(args, " "), strings.TrimSpace(stderr.String()))
	}

	return stdout.Bytes(), nil
}

// evidenceCap bounds a single evidence list.
const evidenceCap = 20

// capped sorts, de-duplicates and truncates labels.
func capped(labels []string) []string {
	sort.Strings(labels)

	out := labels[:0]

	seen := map[string]bool{}

	for _, l := range labels {
		if seen[l] {
			continue
		}

		seen[l] = true
		out = append(out, l)
	}

	if len(out) > evidenceCap {
		out = append(out[:evidenceCap], "…")
	}

	return out
}
