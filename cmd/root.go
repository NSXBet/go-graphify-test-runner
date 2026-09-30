package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/NSXBet/go-graphify-test-runner/internal/decide"
	"github.com/NSXBet/go-graphify-test-runner/internal/gotest"
	"github.com/NSXBet/go-graphify-test-runner/internal/graph"
	"github.com/NSXBet/go-graphify-test-runner/internal/repo"
	"github.com/spf13/cobra"
)

type options struct {
	repo      string
	base      string
	threshold float64
	model     string
	endpoint  string
	dryRun    bool
}

var opts options

var rootCmd = &cobra.Command{
	Use:   "graphify-test-runner [flags] -- [go test flags]",
	Short: "Select and run only the Go tests a change can affect",
	Long: `graphify-test-runner refreshes the local graphify code graph, computes the
diff from merge-base(HEAD, base) to the working tree, then asks SystemOne (one
yes/no question per test file, then one per test function) which tests to run.

Any arguments after -- are forwarded to go test, e.g.:

  graphify-test-runner -base origin/main -- -race -count=1`,
	Args:         cobra.ArbitraryArgs,
	SilenceUsage: true,
	RunE:         run,
}

// Execute runs the root command and exits with its status code.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(2)
	}
}

func init() {
	f := rootCmd.Flags()
	f.StringVar(&opts.repo, "repo", ".", "repository path")
	f.StringVar(&opts.base, "base", "origin/main", "base ref for merge-base")
	f.Float64Var(&opts.threshold, "threshold", 0.5, "noul >= threshold means yes")
	f.StringVar(&opts.model, "model", "jev-latest", "decision model")
	f.StringVar(&opts.endpoint, "endpoint", "https://openrouter.ai/api/alpha/decisions", "decisions endpoint")
	f.BoolVar(&opts.dryRun, "dry-run", false, "print selection and go test commands, do not run")
}

func run(cmd *cobra.Command, args []string) error {
	code := runSelection(cmd.Context(), args)
	if code != 0 {
		os.Exit(code)
	}
	return nil
}

func runSelection(ctx context.Context, extra []string) int {
	key := os.Getenv("OPENROUTER_API_KEY")
	if key == "" && strings.Contains(opts.endpoint, "ai-llm-gateway.fbr.land") {
		key = os.Getenv("AIHUB_TOKEN")
	}
	if key == "" {
		fmt.Fprintln(os.Stderr, "OPENROUTER_API_KEY not set")
		return 2
	}

	root, err := repo.Root(opts.repo)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if err := graph.Update(root); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	g, err := graph.Load(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	mb, err := repo.MergeBase(root, opts.base)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	changed, err := repo.ChangedLines(root, mb)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if len(changed) == 0 {
		fmt.Printf("no changes vs merge-base %s\n", short(mb))
		return 0
	}
	diff, err := repo.DiffText(root, mb)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	changedIDs := g.ChangedSymbols(changed)
	files := repo.ChangedFiles(changed)

	testFiles, err := gotest.ListFiles(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if len(testFiles) == 0 {
		fmt.Println("no Go test files")
		return 0
	}

	state := buildState(mb, files, g, changedIDs, diff)
	fmt.Fprintf(os.Stderr, "state: %d chars, changed files: %d, test files: %d\n", len(state), len(files), len(testFiles))

	c := decide.NewClient(opts.endpoint, key, opts.model)

	// Round 1: one question per test file.
	type parsed struct {
		path  string
		funcs []gotest.Func
	}
	changedDirs := map[string]bool{}
	for _, f := range files {
		changedDirs[filepath.Dir(f)] = true
	}
	var parsedFiles []parsed
	var r1qs []decide.Question
	for _, path := range testFiles {
		funcs, err := gotest.Funcs(root, path)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		parsedFiles = append(parsedFiles, parsed{path: path, funcs: funcs})
		names := make([]string, 0, len(funcs))
		for _, f := range funcs {
			names = append(names, f.Name)
		}
		dir := filepath.Dir(path)
		direct, indirect := g.Evidence(g.FileCallers(path), changedIDs)
		r1qs = append(r1qs, decide.Question{
			Key:          path,
			Instructions: truncate(promptR1(path, dir, changedDirs[dir], names, direct, indirect), decide.MaxInstrChars),
		})
	}
	r1, err := c.Decide(ctx, state, r1qs)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}

	reportRound("round 1 (files)", r1, opts.threshold)

	// Round 2: one question per test func in each selected file.
	type r2item struct {
		path string
		fn   gotest.Func
	}
	var r2qs []decide.Question
	var r2items []r2item
	for _, pf := range parsedFiles {
		if r1[pf.path] < opts.threshold {
			continue
		}
		for _, fn := range pf.funcs {
			var callers []string
			if id := g.NodeIDFor(pf.path, fn.Name); id != "" {
				callers = []string{id}
			}
			direct, indirect := g.Evidence(callers, changedIDs)
			r2qs = append(r2qs, decide.Question{
				Key:          pf.path + "::" + fn.Name,
				Instructions: truncate(promptR2(pf.path, fn, direct, indirect), decide.MaxInstrChars),
			})
			r2items = append(r2items, r2item{path: pf.path, fn: fn})
		}
	}
	var r2 map[string]float64
	if len(r2qs) > 0 {
		r2, err = c.Decide(ctx, state, r2qs)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		reportRound("round 2 (tests)", r2, opts.threshold)
	} else {
		fmt.Println("round 2 (tests): 0 asked, 0 selected")
	}

	fmt.Printf("cost: $%.6f\n", c.Cost())

	// Group selected tests by package dir.
	selected := map[string][]string{}
	for _, it := range r2items {
		if r2[it.path+"::"+it.fn.Name] >= opts.threshold {
			dir := filepath.Dir(it.path)
			selected[dir] = append(selected[dir], it.fn.Name)
		}
	}
	if len(selected) == 0 {
		fmt.Println("no tests selected")
		return 0
	}
	return gotest.Run(root, selected, extra, opts.dryRun)
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

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
	fmt.Printf("%s: %d asked, %d selected\n", label, len(keys), sel)
	for _, k := range keys {
		mark := "no "
		if scores[k] >= threshold {
			mark = "YES"
		}
		fmt.Printf("  %.2f %s %s\n", scores[k], mark, k)
	}
}

func buildState(mb string, files []string, g *graph.Graph, changedIDs map[string]bool, diff string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Change under review: diff from merge-base %s to working tree.\n", short(mb))
	b.WriteString("Changed files:\n")
	for _, f := range files {
		b.WriteString(f + "\n")
	}
	b.WriteString("Changed symbols (from code graph):\n")
	byFile := g.Labels(changedIDs)
	ff := make([]string, 0, len(byFile))
	for f := range byFile {
		ff = append(ff, f)
	}
	sort.Strings(ff)
	for _, f := range ff {
		labels := byFile[f]
		sort.Strings(labels)
		fmt.Fprintf(&b, "%s: %s\n", f, strings.Join(labels, ", "))
	}
	b.WriteString("Diff:\n")
	head := b.String()
	room := 30000 - len(head)
	if room < 0 {
		room = 0
	}
	if len(diff) > room {
		cut := room - len("\n[diff truncated]")
		if cut < 0 {
			cut = 0
		}
		b.WriteString(diff[:cut])
		b.WriteString("\n[diff truncated]")
	} else {
		b.WriteString(diff)
	}
	return b.String()
}

func promptR1(path, dir string, sameDir bool, names, direct, indirect []string) string {
	same := "no"
	if sameDir {
		same = "yes"
	}
	if len(names) > 40 {
		names = append(names[:40], "…")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Should Go test file `%s` be run to validate this change? Answer yes if any test in it likely exercises changed code or behavior.\n", path)
	fmt.Fprintf(&b, "Package dir: %s (same directory as a changed file: %s).\n", dir, same)
	fmt.Fprintf(&b, "Tests: %s.\n", strings.Join(names, ", "))
	fmt.Fprintf(&b, "Calls changed symbols directly: %s.\n", orNone(direct))
	fmt.Fprintf(&b, "Calls changed symbols indirectly: %s.\n", orNone(indirect))
	return b.String()
}

func promptR2(path string, fn gotest.Func, direct, indirect []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Should test `%s` in `%s` be run to validate this change? Answer yes if it likely exercises changed code or behavior.\n", fn.Name, path)
	fmt.Fprintf(&b, "Calls changed symbols directly: %s.\n", orNone(direct))
	fmt.Fprintf(&b, "Calls changed symbols indirectly: %s.\n", orNone(indirect))
	b.WriteString("Source:\n")
	b.WriteString(fn.Src)
	return b.String()
}

func orNone(s []string) string {
	if len(s) == 0 {
		return "none"
	}
	return strings.Join(s, ", ")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
