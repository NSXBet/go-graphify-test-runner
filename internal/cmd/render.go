package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/NSXBet/go-smart-test-runner/internal/gotest"
)

// Styles are built per writer because a renderer is bound to one writer's
// colour profile: writing the report to a buffer in a test must not emit ANSI
// escapes, and neither must a pipe or a CI log. The profile is chosen explicitly
// in colorProfile; a non-terminal always renders plain text.
type styles struct {
	title    lipgloss.Style
	ok       lipgloss.Style
	fail     lipgloss.Style
	skip     lipgloss.Style
	dim      lipgloss.Style
	label    lipgloss.Style
	duration lipgloss.Style
	rule     lipgloss.Style
	pkg      lipgloss.Style
}

func newStyles(w io.Writer) styles {
	r := lipgloss.NewRenderer(w)
	// Set the profile explicitly rather than relying on lipgloss's ambient
	// detection: a terminal gets colour, a pipe/file (where the report is often
	// captured or parsed) gets plain text, and NO_COLOR always wins.
	r.SetColorProfile(colorProfile(w))

	return styles{
		title:    r.NewStyle().Bold(true),
		ok:       r.NewStyle().Foreground(lipgloss.Color("42")).Bold(true),
		fail:     r.NewStyle().Foreground(lipgloss.Color("196")).Bold(true),
		skip:     r.NewStyle().Foreground(lipgloss.Color("244")),
		dim:      r.NewStyle().Foreground(lipgloss.Color("244")),
		label:    r.NewStyle().Foreground(lipgloss.Color("245")),
		duration: r.NewStyle().Foreground(lipgloss.Color("245")),
		rule:     r.NewStyle().Foreground(lipgloss.Color("238")),
		pkg:      r.NewStyle().Foreground(lipgloss.Color("252")),
	}
}

// colorProfile picks the colour profile for a writer: ANSI256 for a terminal,
// plain text otherwise. A pipe or a file — where this report is often captured
// or parsed — gets no escape codes, and NO_COLOR always wins.
//
// The terminal test is done here rather than left to termenv's ambient
// detection, which reports no colour even on a pty.
func colorProfile(w io.Writer) termenv.Profile {
	if os.Getenv("NO_COLOR") != "" {
		return termenv.Ascii
	}

	f, ok := w.(*os.File)
	if !ok {
		return termenv.Ascii
	}

	if fi, err := f.Stat(); err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return termenv.Ascii
	}

	if strings.Contains(strings.ToLower(os.Getenv("COLORTERM")), "truecolor") {
		return termenv.TrueColor
	}

	return termenv.ANSI256
}

// selection splits the decision rounds into files considered/selected and tests
// considered/selected. Round 1 scores test files, round 2 scores individual
// tests, so summing them into one "tests" figure would be wrong.
func selection(rep *report) (filesAsked, filesSelected, testsAsked, testsSelected int) {
	if len(rep.Rounds) > 0 {
		filesAsked = len(rep.Rounds[0].Scores)
		filesSelected = len(rep.Rounds[0].Selected)
	}

	if len(rep.Rounds) > 1 {
		testsAsked = len(rep.Rounds[1].Scores)
		testsSelected = len(rep.Rounds[1].Selected)
	}

	return filesAsked, filesSelected, testsAsked, testsSelected
}

// renderHeader prints what is about to run, before the tests start, so the tool
// is not silent while the suite runs.
func renderHeader(w io.Writer, rep *report, results []gotest.Result, dryRun bool) {
	st := newStyles(w)

	packages, tests, skipped := 0, 0, 0

	for i := range results {
		if results[i].Skipped {
			skipped++

			continue
		}

		packages++

		tests += len(results[i].Funcs)
	}

	title := st.title.Render("smart-test-runner")
	if rev := revision(rep); rev != "" {
		title += "  " + st.label.Render(rev)
	}

	fmt.Fprintln(w, title)

	switch {
	case rep.All:
		// The whole suite is a single `go test ./...`, so this tool has no
		// per-package or per-test count to report — only what go test prints.
		fmt.Fprintln(w, st.label.Render("whole suite (--all)"))
	case rep.Rounds != nil:
		filesAsked, filesSelected, testsAsked, testsSelected := selection(rep)
		fmt.Fprintln(w, st.label.Render(fmt.Sprintf("%s considered, %d selected · %s considered, %d selected",
			plural(filesAsked, "test file"), filesSelected, plural(testsAsked, "test"), testsSelected)))
		fmt.Fprintln(w, st.label.Render(planned(tests, packages, skipped, dryRun)))

		if testsSelected == 0 {
			fmt.Fprintln(w, st.label.Render("no tests selected — nothing to run"))
		}
	default:
		fmt.Fprintln(w, st.label.Render(planned(tests, packages, skipped, dryRun)))
	}

	if rep.Cost > 0 {
		fmt.Fprintln(w, st.label.Render(fmt.Sprintf("decisions cost $%.4f", rep.Cost)))
	}

	fmt.Fprintln(w)
}

// planned renders the run's scope, as a plan under --dry-run and as a promise
// otherwise.
//
// The skipped count is named explicitly because it explains why the selected
// count above
// is larger: a selected test in a package the build tags exclude does not run,
// and without saying so the two lines look like they disagree.
func planned(tests, packages, skipped int, dryRun bool) string {
	verb := "running"
	if dryRun {
		verb = "dry run:"
	}

	line := fmt.Sprintf("%s %s in %s", verb, plural(tests, "test"), plural(packages, "package"))
	if skipped > 0 {
		line += " · " + plural(skipped, "package") + " skipped"
	}

	if dryRun {
		line += " would run"
	}

	return line
}

// plural renders "1 test" / "3 tests".
func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}

	return fmt.Sprintf("%d %ss", n, noun)
}

// revision renders the merge base and changed-file count, or nothing under
// --all where there is no diff.
func revision(rep *report) string {
	if rep.MergeBase == "" {
		return ""
	}

	return fmt.Sprintf("%s · %d changed files", rep.MergeBase, len(rep.ChangedFiles))
}

// renderOutcome prints one line per package, the captured output of any failure,
// and the closing verdict. Under dry-run it prints the command line each package
// would have run instead of a pass/fail claim.
func renderOutcome(w io.Writer, results []gotest.Result) {
	st := newStyles(w)

	if len(results) == 0 {
		fmt.Fprintln(w, st.skip.Render("no packages to run"))

		return
	}

	if dryRunResults(results) {
		for i := range results {
			res := &results[i]
			fmt.Fprintln(w, "  "+st.label.Render(res.Dir))
			fmt.Fprintln(w, st.dim.Render("    "+res.Output))
		}

		return
	}

	// A skipped package is not a failure: Ok() is false for it because nothing
	// ran, so the failure test is Err, not !Ok().
	failed := 0

	for i := range results {
		res := &results[i]
		if res.Err != nil {
			failed++
		}

		fmt.Fprintln(w, packageLine(&st, res))
	}

	// go test's own output, per package — but only when it says more than the
	// package line already does. A bare pass prints exactly "ok pkg 0.1s", which
	// would just repeat the line above; a forwarded -v ("=== RUN") or a failure
	// adds real detail, and hiding that would make the flags look dropped.
	for i := range results {
		res := &results[i]
		if res.Skipped || quietOutput(res) {
			continue
		}

		fmt.Fprintln(w)

		title := st.ok.Render("✓ " + res.Dir)
		if res.Err != nil {
			title = st.fail.Render("✗ " + res.Dir)
		}

		fmt.Fprintln(w, title)

		for line := range strings.SplitSeq(strings.TrimRight(res.Output, "\n"), "\n") {
			fmt.Fprintln(w, st.dim.Render("    "+line))
		}
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, verdict(&st, results, failed))
}

// quietOutput reports whether go test printed nothing beyond its one-line
// summary, which the package line already conveys.
func quietOutput(res *gotest.Result) bool {
	lines := 0

	for line := range strings.SplitSeq(strings.TrimSpace(res.Output), "\n") {
		if strings.TrimSpace(line) != "" {
			lines++
		}
	}

	return lines <= 1
}

// dryRunResults reports whether the results describe a plan rather than an
// execution. Nothing ran, so there is no pass/fail to report — only the
// commands. Every result is checked, not just the first: a skipped package sorts
// first in this repo (e2e/), and it is never a dry run.
func dryRunResults(results []gotest.Result) bool {
	for i := range results {
		if !results[i].Skipped && results[i].DryRun {
			return true
		}
	}

	return false
}

// packageLine renders one package's outcome.
func packageLine(st *styles, res *gotest.Result) string {
	switch {
	case res.Skipped:
		return "  " + st.skip.Render("↷ "+res.Dir) + "  " + st.skip.Render("no buildable Go files")
	case res.Err != nil:
		return "  " + st.fail.Render("✗ "+res.Dir) + "  " + st.duration.Render(countAndDuration(res))
	default:
		return "  " + st.ok.Render("✓ "+res.Dir) + "  " + st.duration.Render(countAndDuration(res))
	}
}

// countAndDuration renders "12 tests  1.20s".
func countAndDuration(res *gotest.Result) string {
	parts := make([]string, 0, 2)

	if len(res.Funcs) > 0 {
		parts = append(parts, plural(len(res.Funcs), "test"))
	}

	if res.Duration > 0 {
		parts = append(parts, res.Duration.Round(10*time.Millisecond).String())
	}

	return strings.Join(parts, "  ")
}

// verdict renders the closing line. It accounts for every package the header
// promised: a skipped package is named rather than silently dropped, so the
// numbers cannot appear to disagree ("running 4 packages" then "3").
func verdict(st *styles, results []gotest.Result, failed int) string {
	ran, skipped := 0, 0

	for i := range results {
		if results[i].Skipped {
			skipped++

			continue
		}

		ran++
	}

	skipNote := ""
	if skipped > 0 {
		skipNote = " · " + plural(skipped, "skipped")
	}

	if failed == 0 {
		return st.ok.Render("PASS") + "  " +
			st.label.Render(plural(ran, "package")+skipNote)
	}

	return st.fail.Render("FAIL") + "  " +
		st.label.Render(fmt.Sprintf("%d of %d packages failed", failed, ran)+skipNote)
}
