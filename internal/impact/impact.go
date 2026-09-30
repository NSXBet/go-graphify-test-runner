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
	"path/filepath"
	"sort"
	"strings"
)

// IndexDir is the directory Grove writes its SQLite index into.
const IndexDir = ".grove"

// IndexedNode is one symbol Grove reports as impacted by a file.
type IndexedNode struct {
	FilePath  string `json:"filePath"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Signature string `json:"signature"`
	Span      struct {
		Start int `json:"start"`
		End   int `json:"end"`
	} `json:"span"`
}

// impactResponse is the JSON shape of `grove impact`.
type impactResponse struct {
	Nodes []IndexedNode `json:"nodes"`
}

// Graph is the Grove-backed view of the repository.
type Graph struct {
	root string

	// reachers maps an impacted file to the changed files it reaches, built by
	// running `grove impact` once per changed file. Grove's `impact` answers
	// "what reaches this file", so the query direction is changed-file ->
	// impacted-file; the map is then read backwards for a test file.
	reachers map[string][]string
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

	return &Graph{root: root, reachers: map[string][]string{}}, nil
}

// IndexChanged records which files reach each changed path, by running
// `grove impact` once per changed file. Call it before reading Reaches.
func (g *Graph) IndexChanged(ctx context.Context, changed []string) error {
	for _, file := range changed {
		nodes, err := g.impactFile(ctx, file)
		if err != nil {
			return err
		}

		for _, n := range nodes {
			if n.FilePath == "" || n.FilePath == file {
				continue
			}

			g.reachers[n.FilePath] = append(g.reachers[n.FilePath], file)
		}
	}

	return nil
}

// Reaches returns the changed files that path (transitively) reaches.
func (g *Graph) Reaches(path string) []string {
	return capped(g.reachers[path])
}

// impactFile runs `grove impact <path>` and returns the symbols that reach it.
func (g *Graph) impactFile(ctx context.Context, path string) ([]IndexedNode, error) {
	out, err := g.run(ctx, "impact", path, ".")
	if err != nil {
		return nil, err
	}

	var resp impactResponse
	if jerr := json.Unmarshal(out, &resp); jerr != nil {
		return nil, fmt.Errorf("grove impact %s: decode: %w", path, jerr)
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
