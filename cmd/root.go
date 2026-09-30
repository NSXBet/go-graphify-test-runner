// Package cmd wires the graphify-test-runner Cobra command: it refreshes the
// code graph, selects the tests a change can affect, and runs them.
package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/NSXBet/go-graphify-test-runner/internal/decide"
	"github.com/NSXBet/go-graphify-test-runner/internal/gotest"
	"github.com/NSXBet/go-graphify-test-runner/internal/graph"
	"github.com/NSXBet/go-graphify-test-runner/internal/repo"
)

// Tunables that would otherwise read as bare magic numbers at their call sites.
const (
	defaultThreshold = 0.5
	shaShortLen      = 12
	maxNamesShown    = 40
)

// options holds the flag values for one invocation.
type options struct {
	repo      string
	base      string
	threshold float64
	model     string
	endpoint  string
	dryRun    bool
}

// newRootCmd builds the root command with its flags bound to a fresh options.
func newRootCmd() *cobra.Command {
	var opts options

	rootCmd := &cobra.Command{
		Use:   "graphify-test-runner [flags] -- [go test flags]",
		Short: "Select and run only the Go tests a change can affect",
		Long: "graphify-test-runner refreshes the local graphify code graph, computes the\n" +
			"diff from merge-base(HEAD, base) to the working tree, then asks SystemOne (one\n" +
			"yes/no question per test file, then one per test function) which tests to run.\n\n" +
			"Any arguments after -- are forwarded to go test, e.g.:\n\n" +
			"  graphify-test-runner -base origin/main -- -race -count=1",
		Args:         cobra.ArbitraryArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if code := runSelection(cmd.Context(), &opts, args); code != 0 {
				os.Exit(code)
			}

			return nil
		},
	}

	f := rootCmd.Flags()
	f.StringVar(&opts.repo, "repo", ".", "repository path")
	f.StringVar(&opts.base, "base", "origin/main", "base ref for merge-base")
	f.Float64Var(&opts.threshold, "threshold", defaultThreshold, "noul >= threshold means yes")
	f.StringVar(&opts.model, "model", "jev-latest", "decision model")
	f.StringVar(&opts.endpoint, "endpoint", "https://openrouter.ai/api/alpha/decisions", "decisions endpoint")
	f.BoolVar(&opts.dryRun, "dry-run", false, "print selection and go test commands, do not run")

	return rootCmd
}

// Execute runs the root command and exits with its status code.
func Execute() {
	if err := newRootCmd().Execute(); err != nil {
		os.Exit(2)
	}
}

// apiKey resolves the bearer token for the configured endpoint.
func apiKey(endpoint string) (string, error) {
	key := os.Getenv("OPENROUTER_API_KEY")
	if key == "" && strings.Contains(endpoint, "ai-llm-gateway.fbr.land") {
		key = os.Getenv("AIHUB_TOKEN")
	}

	if key == "" {
		return "", errors.New("OPENROUTER_API_KEY not set")
	}

	return key, nil
}

// facts is everything resolved before the decision rounds.
type facts struct {
	root       string
	g          *graph.Graph
	mb         string
	diff       string
	files      []string
	changedIDs map[string]bool
	testFiles  []string
}

// prepare resolves the repo, the code graph, and the changed set. It returns a
// nil facts when there is nothing to run, with code the process exit status.
func prepare(ctx context.Context, opts *options) (f *facts, code int) {
	root, err := repo.Root(ctx, opts.repo)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)

		return nil, 2
	}

	if uerr := graph.Update(ctx, root); uerr != nil {
		fmt.Fprintln(os.Stderr, uerr)

		return nil, 2
	}

	g, err := graph.Load(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)

		return nil, 2
	}

	changed, mb, err := changedSet(ctx, root, opts.base)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)

		return nil, 2
	}

	if len(changed) == 0 {
		fmt.Fprintf(os.Stdout, "no changes vs merge-base %s\n", short(mb))

		return nil, 0
	}

	diff, err := repo.DiffText(ctx, root, mb)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)

		return nil, 2
	}

	testFiles, err := gotest.ListFiles(ctx, root)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)

		return nil, 2
	}

	if len(testFiles) == 0 {
		fmt.Fprintln(os.Stdout, "no Go test files")

		return nil, 0
	}

	return &facts{
		root:       root,
		g:          g,
		mb:         mb,
		diff:       diff,
		files:      repo.ChangedFiles(changed),
		changedIDs: g.ChangedSymbols(changed),
		testFiles:  testFiles,
	}, 0
}

// runSelection performs the two decision rounds and runs the selected tests.
// It returns the process exit code.
func runSelection(ctx context.Context, opts *options, extra []string) int {
	key, err := apiKey(opts.endpoint)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)

		return 2
	}

	f, code := prepare(ctx, opts)
	if f == nil {
		return code
	}

	state := buildState(f.mb, f.files, f.g, f.changedIDs, f.diff)
	fmt.Fprintf(os.Stderr, "state: %d chars, changed files: %d, test files: %d\n", len(state), len(f.files), len(f.testFiles))

	c := decide.NewClient(opts.endpoint, key, opts.model)
	parsedFiles, r1qs := round1(f.root, f.g, f.changedIDs, f.testFiles, f.files)

	r1, err := c.Decide(ctx, state, r1qs)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)

		return 2
	}

	reportRound("round 1 (files)", r1, opts.threshold)

	r2, r2items, err := round2(ctx, c, state, f.g, f.changedIDs, parsedFiles, r1, opts.threshold)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)

		return 2
	}

	fmt.Fprintf(os.Stdout, "cost: $%.6f\n", c.Cost())

	selected := selectedTests(r2, r2items, opts.threshold)
	if len(selected) == 0 {
		fmt.Fprintln(os.Stdout, "no tests selected")

		return 0
	}

	return gotest.Run(ctx, f.root, selected, extra, opts.dryRun)
}

// changedSet returns the changed-line map and the merge-base sha.
func changedSet(ctx context.Context, root, base string) (changed map[string][][2]int, mb string, err error) {
	mb, err = repo.MergeBase(ctx, root, base)
	if err != nil {
		return nil, "", err
	}

	changed, err = repo.ChangedLines(ctx, root, mb)
	if err != nil {
		return nil, "", err
	}

	return changed, mb, nil
}

// parsedFile pairs a test file with the functions it declares.
type parsedFile struct {
	path  string
	funcs []gotest.Func
}

// round1 builds the per-test-file questions.
func round1(root string, g *graph.Graph, changedIDs map[string]bool, testFiles, files []string) ([]parsedFile, []decide.Question) {
	changedDirs := map[string]bool{}
	for _, f := range files {
		changedDirs[filepath.Dir(f)] = true
	}

	var (
		parsedFiles []parsedFile
		q           []decide.Question
	)

	for _, path := range testFiles {
		funcs, err := gotest.Funcs(root, path)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)

			return nil, nil
		}

		parsedFiles = append(parsedFiles, parsedFile{path: path, funcs: funcs})

		names := make([]string, 0, len(funcs))
		for _, fn := range funcs {
			names = append(names, fn.Name)
		}

		dir := filepath.Dir(path)
		direct, indirect := g.Evidence(g.FileCallers(path), changedIDs)

		q = append(q, decide.Question{
			Key:          path,
			Instructions: truncate(promptR1(path, dir, changedDirs[dir], names, direct, indirect), decide.MaxInstrChars),
		})
	}

	return parsedFiles, q
}

// r2item pairs a test function with its file path.
type r2item struct {
	path string
	fn   gotest.Func
}

// round2 asks about every test function in each selected file.
func round2(ctx context.Context, c *decide.Client, state string, g *graph.Graph, changedIDs map[string]bool, parsedFiles []parsedFile, r1 map[string]float64, threshold float64) (map[string]float64, []r2item, error) {
	var (
		qs    []decide.Question
		items []r2item
	)

	for _, pf := range parsedFiles {
		if r1[pf.path] < threshold {
			continue
		}

		for _, fn := range pf.funcs {
			direct, indirect := g.Evidence(testCallers(g, pf.path, fn.Name), changedIDs)

			qs = append(qs, decide.Question{
				Key:          pf.path + "::" + fn.Name,
				Instructions: truncate(promptR2(pf.path, fn, direct, indirect), decide.MaxInstrChars),
			})
			items = append(items, r2item{path: pf.path, fn: fn})
		}
	}

	if len(qs) == 0 {
		fmt.Fprintln(os.Stdout, "round 2 (tests): 0 asked, 0 selected")

		return nil, items, nil
	}

	r2, err := c.Decide(ctx, state, qs)
	if err != nil {
		return nil, nil, err
	}

	reportRound("round 2 (tests)", r2, threshold)

	return r2, items, nil
}

// testCallers returns the graph node for a test function, if any.
func testCallers(g *graph.Graph, path, name string) []string {
	if id := g.NodeIDFor(path, name); id != "" {
		return []string{id}
	}

	return nil
}

// selectedTests groups the accepted tests by package directory.
func selectedTests(r2 map[string]float64, items []r2item, threshold float64) map[string][]string {
	selected := map[string][]string{}

	for _, it := range items {
		if r2[it.path+"::"+it.fn.Name] >= threshold {
			dir := filepath.Dir(it.path)
			selected[dir] = append(selected[dir], it.fn.Name)
		}
	}

	return selected
}

// short truncates a commit sha for display.
func short(sha string) string {
	if len(sha) > shaShortLen {
		return sha[:shaShortLen]
	}

	return sha
}

// reportRound prints one round's verdict lines, sorted by key.
func reportRound(label string, scores map[string]float64, threshold float64) {
	keys := make([]string, 0, len(scores))
	for k := range scores {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	sel := 0

	for _, k := range keys {
		if scores[k] >= threshold {
			sel++
		}
	}

	fmt.Fprintf(os.Stdout, "%s: %d asked, %d selected\n", label, len(keys), sel)

	for _, k := range keys {
		mark := "no "
		if scores[k] >= threshold {
			mark = "YES"
		}

		fmt.Fprintf(os.Stdout, "  %.2f %s %s\n", scores[k], mark, k)
	}
}

// buildState renders the state shared by both rounds, capped at
// decide.MaxStateChars characters.
func buildState(mb string, files []string, g *graph.Graph, changedIDs map[string]bool, diff string) string {
	var b strings.Builder

	fmt.Fprintf(&b, "Change under review: diff from merge-base %s to working tree.\n", short(mb))
	b.WriteString("Changed files:\n")

	for _, f := range files {
		b.WriteString(f + "\n")
	}

	b.WriteString("Changed symbols (from code graph):\n")
	writeSymbols(&b, g.Labels(changedIDs))
	b.WriteString("Diff:\n")

	head := b.String()

	room := max(decide.MaxStateChars-len(head), 0)
	if len(diff) > room {
		cut := max(room-len("\n[diff truncated]"), 0)
		b.WriteString(diff[:cut])
		b.WriteString("\n[diff truncated]")

		return b.String()
	}

	b.WriteString(diff)

	return b.String()
}

// writeSymbols writes the per-file changed-symbol lines, sorted for stability.
func writeSymbols(b *strings.Builder, byFile map[string][]string) {
	ff := make([]string, 0, len(byFile))
	for f := range byFile {
		ff = append(ff, f)
	}

	sort.Strings(ff)

	for _, f := range ff {
		labels := byFile[f]
		sort.Strings(labels)
		fmt.Fprintf(b, "%s: %s\n", f, strings.Join(labels, ", "))
	}
}

// promptR1 renders the per-test-file question.
func promptR1(path, dir string, sameDir bool, names, direct, indirect []string) string {
	same := "no"
	if sameDir {
		same = "yes"
	}

	if len(names) > maxNamesShown {
		names = append(names[:maxNamesShown], "…")
	}

	var b strings.Builder

	fmt.Fprintf(&b, "Should Go test file `%s` be run to validate this change? Answer yes if any test in it likely exercises changed code or behavior.\n", path)
	fmt.Fprintf(&b, "Package dir: %s (same directory as a changed file: %s).\n", dir, same)
	fmt.Fprintf(&b, "Tests: %s.\n", strings.Join(names, ", "))
	fmt.Fprintf(&b, "Calls changed symbols directly: %s.\n", orNone(direct))
	fmt.Fprintf(&b, "Calls changed symbols indirectly: %s.\n", orNone(indirect))

	return b.String()
}

// promptR2 renders the per-test-function question.
func promptR2(path string, fn gotest.Func, direct, indirect []string) string {
	var b strings.Builder

	fmt.Fprintf(&b, "Should test `%s` in `%s` be run to validate this change? Answer yes if it likely exercises changed code or behavior.\n", fn.Name, path)
	fmt.Fprintf(&b, "Calls changed symbols directly: %s.\n", orNone(direct))
	fmt.Fprintf(&b, "Calls changed symbols indirectly: %s.\n", orNone(indirect))
	b.WriteString("Source:\n")
	b.WriteString(fn.Src)

	return b.String()
}

// orNone renders an evidence list, or "none" when empty.
func orNone(s []string) string {
	if len(s) == 0 {
		return "none"
	}

	return strings.Join(s, ", ")
}

// truncate caps s to n bytes.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}

	return s[:n]
}
