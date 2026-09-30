// Package cmd wires the smart-test-runner Cobra command: it refreshes the
// code graph, selects the tests a change can affect, and runs them.
package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/NSXBet/go-smart-test-runner/internal/decide"
	"github.com/NSXBet/go-smart-test-runner/internal/gotest"
	"github.com/NSXBet/go-smart-test-runner/internal/impact"
	"github.com/NSXBet/go-smart-test-runner/internal/repo"
	"github.com/NSXBet/go-smart-test-runner/internal/version"
)

// Tunables that would otherwise read as bare magic numbers at their call sites.
const (
	defaultThreshold = 0.5
	shaShortLen      = 12
)

// options holds the flag values for one invocation.
type options struct {
	repo      string
	base      string
	threshold float64
	model     string
	endpoint  string
	dryRun    bool
	verbose   bool
	json      bool
	noUpdate  bool
	all       bool
}

// newRootCmd builds the root command with its flags bound to a fresh options.
func newRootCmd() *cobra.Command {
	var opts options

	rootCmd := &cobra.Command{
		Use:   "smart-test-runner [flags] -- [go test flags]",
		Short: "Select and run only the Go tests a change can affect",
		Long: "smart-test-runner indexes the repository with Grove, computes the\n" +
			"diff from merge-base(HEAD, base) to the working tree, then asks SystemOne (one\n" +
			"yes/no question per test file, then one per test function) which tests to run.\n\n" +
			"Any arguments after -- are forwarded to go test, e.g.:\n\n" +
			"  smart-test-runner -base origin/main -- -race -count=1",
		Args:         cobra.ArbitraryArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !opts.noUpdate && !opts.json && os.Getenv("SMART_TEST_RUNNER_NO_UPDATE_CHECK") == "" {
				maybeNudge(cmd.Context())
			}

			// --all is a go test passthrough: no diff, no code graph, no model,
			// so it needs neither an API key nor a grove index.
			if opts.all {
				if code := runAll(cmd.Context(), &opts, args); code != 0 {
					os.Exit(code)
				}

				return nil
			}

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
	f.StringVar(&opts.model, "model", modelFromEnv(), "decision model (env "+envSystemOneModel+")")
	f.StringVar(&opts.endpoint, "endpoint", endpointFromEnv(), "decisions endpoint (env "+envSystemOneURL+")")
	f.BoolVar(&opts.dryRun, "dry-run", false, "print selection and go test commands, do not run")
	f.BoolVar(&opts.verbose, "verbose", false, "print the full decisioning exchange with the decision model to stderr, for auditing")
	f.BoolVar(&opts.json, "json", false, "emit the full result (selection, scores, and — with --verbose — the judging) as JSON on stdout")
	f.BoolVar(&opts.noUpdate, "no-update-check", false, "skip the check for a newer release")
	f.BoolVar(&opts.all, "all", false, "run the whole suite (go test ./...) instead of selecting tests from the diff")

	rootCmd.AddCommand(newVersionCmd(), newUpgradeCmd(), newCheckUpdateCmd())
	rootCmd.Version = version.Get()
	rootCmd.SetVersionTemplate("{{.Version}}\n")

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
	return resolveToken(endpoint)
}

// facts is everything resolved before the decision rounds.
type facts struct {
	root      string
	g         *impact.Graph
	mb        string
	diff      string
	files     []string
	testFiles []string
}

// prepare resolves the repo, the code graph, and the changed set. It returns a
// nil facts when there is nothing to run, with code the process exit status.
func prepare(ctx context.Context, opts *options) (f *facts, code int) {
	root, err := repo.Root(ctx, opts.repo)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)

		return nil, 2
	}

	if uerr := impact.Update(ctx, root); uerr != nil {
		fmt.Fprintln(os.Stderr, uerr)

		return nil, 2
	}

	g, err := impact.Load(ctx, root)
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

	changedFiles := repo.ChangedFiles(changed)
	if ierr := g.IndexChanged(ctx, changedFiles); ierr != nil {
		fmt.Fprintln(os.Stderr, ierr)

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
		root:      root,
		g:         g,
		mb:        mb,
		diff:      diff,
		files:     changedFiles,
		testFiles: testFiles,
	}, 0
}

// runAll runs the whole suite from the repository root, forwarding any args
// after -- to go test. It is the --all path: the tool behaves exactly like
// `go test ./...` so one command covers both "run everything" and "run the
// affected subset".
func runAll(ctx context.Context, opts *options, extra []string) int {
	root, err := repo.Root(ctx, opts.repo)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)

		return 2
	}

	res := gotest.RunAll(ctx, root, extra, opts.dryRun)

	// Under --json stdout carries the document, so go test output goes to
	// stderr to keep it parseable.
	if opts.json {
		rep := &report{Selected: map[string][]string{}, All: true}
		if jerr := renderJSON(os.Stdout, rep); jerr != nil {
			fmt.Fprintln(os.Stderr, jerr)
		}

		fmt.Fprint(os.Stderr, res.Output)
	} else {
		renderHeader(os.Stdout, &report{All: true}, nil, opts.dryRun)
		renderOutcome(os.Stdout, []gotest.Result{res})
	}

	if res.Err != nil {
		return 1
	}

	return 0
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
		if opts.json {
			if jerr := renderJSON(os.Stdout, &report{Selected: map[string][]string{}}); jerr != nil {
				fmt.Fprintln(os.Stderr, jerr)
			}
		}

		return code
	}

	state := buildState(f.mb, f.files, f.diff)

	c := decide.NewClient(opts.endpoint, key, opts.model)
	parsedFiles, r1qs := round1(f.root, f.g, f.testFiles, f.files)

	r1, err := c.Decide(ctx, state, r1qs)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)

		return 2
	}

	r2, r2items, err := round2(ctx, c, state, f.g, parsedFiles, r1, opts.threshold)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)

		return 2
	}

	selected := selectedTests(r2, r2items, opts.threshold)

	rep := &report{
		MergeBase:    short(f.mb),
		ChangedFiles: f.files,
		StateChars:   len(state),
		Rounds: []roundReport{
			newRoundReport("round 1 (files)", r1, opts.threshold),
			newRoundReport("round 2 (tests)", r2, opts.threshold),
		},
		Cost:     c.Cost(),
		Selected: selected,
	}
	if opts.verbose {
		rep.Judging = c.Exchanges()
	}

	// Describe the run before it starts (JSON or the human header), then run it.
	emit(rep, opts, extra)

	if len(selected) == 0 {
		return 0
	}

	results := gotest.Run(ctx, f.root, selected, extra, opts.dryRun)

	// Under --json stdout carries the document, so go test output goes to
	// stderr to keep it parseable.
	if opts.json {
		for i := range results {
			fmt.Fprint(os.Stderr, results[i].Output)
		}
	} else {
		renderOutcome(os.Stdout, results)
	}

	// A skipped package is not a failure; see renderOutcome.
	failed := 0

	for i := range results {
		if results[i].Err != nil {
			failed++
		}
	}

	if failed > 0 {
		return 1
	}

	return 0
}

// emit renders the report: JSON to stdout under --json, otherwise the text
// report to stdout and — under --verbose — the decisioning audit trail to
// stderr, so the report stays parseable.
func emit(rep *report, opts *options, extra []string) {
	if opts.json {
		if err := renderJSON(os.Stdout, rep); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}

		return
	}

	// The human report opens with what is about to run; the outcome follows
	// once the tests finish.
	renderHeader(os.Stdout, rep, gotest.Plan(rep.Selected, extra), opts.dryRun)

	if opts.verbose {
		renderExchangesText(os.Stderr, rep)
	}
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
func round1(root string, g *impact.Graph, testFiles, files []string) ([]parsedFile, []decide.Question) {
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
		reaches := g.Reaches(path)

		q = append(q, decide.Question{
			Key:          path,
			Instructions: truncate(promptR1(path, dir, changedDirs[dir], names, reaches), decide.MaxInstrChars),
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
func round2(ctx context.Context, c *decide.Client, state string, g *impact.Graph, parsedFiles []parsedFile, r1 map[string]float64, threshold float64) (map[string]float64, []r2item, error) {
	var (
		qs    []decide.Question
		items []r2item
	)

	for _, pf := range parsedFiles {
		if r1[pf.path] < threshold {
			continue
		}

		reaches := g.Reaches(pf.path)

		for _, fn := range pf.funcs {
			qs = append(qs, decide.Question{
				Key:          pf.path + "::" + fn.Name,
				Instructions: truncate(promptR2(pf.path, fn, reaches), decide.MaxInstrChars),
			})
			items = append(items, r2item{path: pf.path, fn: fn})
		}
	}

	if len(qs) == 0 {
		return nil, items, nil
	}

	r2, err := c.Decide(ctx, state, qs)
	if err != nil {
		return nil, nil, err
	}

	return r2, items, nil
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

// buildState renders the state shared by both rounds, capped at
// decide.MaxStateChars characters.
func buildState(mb string, files []string, diff string) string {
	var b strings.Builder

	fmt.Fprintf(&b, "Change under review: diff from merge-base %s to working tree.\n", short(mb))
	b.WriteString("Changed files:\n")

	for _, f := range files {
		b.WriteString(f + "\n")
	}

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

// promptR1 renders the per-test-file question.
func promptR1(path, dir string, sameDir bool, names []string, reaches []impact.Reach) string {
	same := "no"
	if sameDir {
		same = "yes"
	}

	var b strings.Builder

	reach := renderReaches(reaches)

	fmt.Fprintf(&b, "Should Go test file `%s` be run to validate this change? Answer yes if any test in it likely exercises changed code or behavior.\n", path)
	fmt.Fprintf(&b, "Package dir: %s (same directory as a changed file: %s).\n", dir, same)
	// The reach evidence is the signal; the test-name list is filler. Budget it
	// against the room left after the fixed parts, so a package with many tests
	// cannot push the evidence out of the prompt.
	fmt.Fprintf(&b, "Tests: %s.\n", fitNames(names, decide.MaxInstrChars-(b.Len()+len(reach)+len("Tests: .\n"))))
	b.WriteString(reach)

	return b.String()
}

// fitNames joins names to fit within budget bytes, dropping names (and marking
// the elision) rather than letting the list push other prompt content out.
func fitNames(names []string, budget int) string {
	if budget <= 0 {
		return "…"
	}

	var b strings.Builder

	for i, n := range names {
		add := n
		if i > 0 {
			add = ", " + n
		}

		if b.Len()+len(add) > budget {
			return b.String() + ", …"
		}

		b.WriteString(add)
	}

	return b.String()
}

// promptR2 renders the per-test-function question.
func promptR2(path string, fn gotest.Func, reaches []impact.Reach) string {
	var b strings.Builder

	fmt.Fprintf(&b, "Should test `%s` in `%s` be run to validate this change? Answer yes if it likely exercises changed code or behavior.\n", fn.Name, path)
	b.WriteString(renderReaches(reaches))
	b.WriteString("Source:\n")
	b.WriteString(fn.Src)

	return b.String()
}

// renderReaches renders how a test file reaches the changed files. Direct call
// edges are the strong signal and are listed per changed file; transitive-only
// reaches are summarised, since on a leaf package nearly every dependent
// transitively reaches it.
func renderReaches(reaches []impact.Reach) string {
	if len(reaches) == 0 {
		return "Does not reach any changed file (call graph, transitively).\n"
	}

	var b strings.Builder

	var direct, transitive []string

	for _, r := range reaches {
		switch {
		case len(r.Direct) > 0:
			direct = append(direct, fmt.Sprintf("%s via %s", r.File, strings.Join(r.Direct, ", ")))
		default:
			transitive = append(transitive, r.File)
		}
	}

	if len(direct) > 0 {
		fmt.Fprintf(&b, "Calls directly into changed files: %s.\n", strings.Join(direct, "; "))
	} else {
		b.WriteString("Calls directly into changed files: none.\n")
	}

	if len(transitive) > 0 {
		fmt.Fprintf(&b, "Reaches (transitively only, no direct call): %s.\n", strings.Join(transitive, ", "))
	}

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
