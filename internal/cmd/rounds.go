package cmd

import (
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"

	"github.com/NSXBet/go-smart-test-runner/internal/gotest"
)

// renderSelection reports what the decision rounds chose.
//
// Normally that is one sentence naming each affected test file and how many of
// its tests were picked — the question a user has is "what will run", and the
// per-candidate probabilities are only needed to audit the decision. Under
// --verbose the full tables are printed instead.
func renderSelection(w io.Writer, rep *report, planned []gotest.Result, verbose bool) {
	if verbose {
		// The tables are the selection report under --verbose, so the trailing
		// blank belongs to them.
		renderRounds(w, rep)
		fmt.Fprintln(w)

		return
	}

	if len(rep.Rounds) == 0 {
		return
	}

	st := newStyles(w)
	files := affectedFiles(rep)

	// A file the model selected but whose package the build tags exclude
	// contributes no run, so say so rather than listing tests that will not
	// happen.
	skipped := skippedDirs(planned)

	if len(files) == 0 {
		fmt.Fprintln(w, st.label.Render("no test files affected"))

		return
	}

	parts := make([]wrapped, 0, len(files))

	for _, f := range files {
		// A file in a package the build tags exclude will not run, so it is left
		// out of the list and counted in the trailing note instead — repeating
		// "skipped" per entry would bury the files that do run.
		if skipped[path.Dir(f.name)] {
			continue
		}

		parts = append(parts, wrapped{
			plain:  f.name + " (" + plural(f.tests, "test") + ")",
			styled: st.pkg.Render(f.name) + " " + st.label.Render("("+plural(f.tests, "test")+")"),
		})
	}

	excluded := countExcluded(files, skipped)

	if len(parts) == 0 {
		fmt.Fprintln(w, st.label.Render("affected: ")+
			st.skip.Render(fmt.Sprintf("none that will run (%s in packages the current build tags exclude)",
				plural(excluded, "test file"))))
		fmt.Fprintln(w)

		return
	}

	writeWrapped(w, st.label.Render("affected: "), parts)

	if excluded > 0 {
		fmt.Fprintln(w, st.skip.Render(fmt.Sprintf("  (%s in packages the current build tags exclude)", plural(excluded, "test file"))))
	}

	fmt.Fprintln(w)
}

// countExcluded counts the affected files whose package will not run.
func countExcluded(files []fileTests, skipped map[string]bool) int {
	n := 0

	for _, f := range files {
		if skipped[path.Dir(f.name)] {
			n++
		}
	}

	return n
}

// wrapped is one segment of flowing text with its styled rendering.
type wrapped struct {
	plain  string
	styled string
}

// selectionWidth is where the affected-files paragraph wraps.
const selectionWidth = 100

// writeWrapped flows the segments into indented lines no longer than
// selectionWidth columns. Segments carry their own styles, and each style closes
// with a reset, so a newline may be inserted between them safely.
func writeWrapped(w io.Writer, prefix string, parts []wrapped) {
	const indent = "  "

	line := prefix
	width := utf8.RuneCountInString(prefix)

	for i, p := range parts {
		sep, sepLen := "", 0
		if i > 0 {
			sep, sepLen = " · ", 3
		}

		if width+sepLen+utf8.RuneCountInString(p.plain) > selectionWidth && i > 0 {
			fmt.Fprintln(w, line)
			line, width = indent, len(indent)
			sep, sepLen = "", 0
		}

		line += sep + p.styled
		width += sepLen + utf8.RuneCountInString(p.plain)
	}

	fmt.Fprintln(w, line)
}

// skippedDirs is the set of package directories the plan will not run.
func skippedDirs(planned []gotest.Result) map[string]bool {
	out := map[string]bool{}

	for i := range planned {
		if planned[i].Skipped {
			out[planned[i].Dir] = true
		}
	}

	return out
}

// fileTests is one affected test file and how many of its tests were selected.
type fileTests struct {
	name  string
	tests int
}

// affectedFiles counts the selected tests per file, sorted by path. It reads the
// last round, which selects individual tests — "<file>::<Test>" keys.
//
// A file the model selected whose every test was then rejected contributes no
// run, so it is omitted rather than shown with a zero count.
func affectedFiles(rep *report) []fileTests {
	counts := map[string]int{}

	for _, k := range rep.Rounds[len(rep.Rounds)-1].Selected {
		file, _, ok := strings.Cut(k, "::")
		if !ok {
			// Not a per-test key; nothing to count for this round's shape.
			continue
		}

		counts[file]++
	}

	files := make([]fileTests, 0, len(counts))
	for name, n := range counts {
		files = append(files, fileTests{name: name, tests: n})
	}

	sort.Slice(files, func(i, j int) bool { return files[i].name < files[j].name })

	return files
}

// renderRounds prints the decision rounds as a table: every candidate the model
// scored, its probability, and whether the threshold selected it.
//
// This used to be printed unconditionally, which buried the result under ~90
// lines of scores. It is a table rather than the old "0.92 YES path" libre
// because the three facts are columns, and the eye can then answer "what was
// rejected?" by scanning one of them.
func renderRounds(w io.Writer, rep *report) {
	if len(rep.Rounds) == 0 {
		return
	}

	st := newStyles(w)

	for i, r := range rep.Rounds {
		if i > 0 {
			fmt.Fprintln(w)
		}

		renderRound(w, &st, r)
	}
}

// renderRound renders one round's table.
func renderRound(w io.Writer, st *styles, r roundReport) {
	keys := make([]string, 0, len(r.Scores))

	for k := range r.Scores {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	rows := make([][]string, 0, len(keys))

	// Round 2 lists many tests per file, so the path repeats down the column.
	// Blank the repeats: the eye groups by path instead of re-reading it, and
	// the left column stays narrow.
	lastFile := ""

	for _, k := range keys {
		// A path carries the file, and "::" separates an individual test; show
		// the two parts in their own columns so a long path does not push the
		// test name off the edge.
		file, test, _ := strings.Cut(k, "::")
		if test == "" {
			test = st.dim.Render("—")
		}

		cell := file
		if file == lastFile {
			cell = ""
		} else {
			lastFile = file
		}

		rows = append(rows, []string{
			cell,
			test,
			fmt.Sprintf("%.2f", r.Scores[k]),
			selectionMark(st, r.Scores[k] >= r.Threshold),
		})
	}

	t := table.New().
		Rows(rows...).
		Headers("File", "Test", "Probability", "Selected").
		Border(lipgloss.RoundedBorder()).
		BorderStyle(st.rule).
		StyleFunc(func(row, col int) lipgloss.Style {
			const (
				probCol     = 2
				selectedCol = 3
			)

			switch {
			case row == table.HeaderRow:
				return st.title.Padding(0, 1)
			case col == probCol, col == selectedCol:
				return st.label.Padding(0, 1)
			default:
				return st.pkg.Padding(0, 1)
			}
		})

	fmt.Fprintf(w, "%s %s\n", st.title.Render(r.Name),
		st.label.Render(fmt.Sprintf("%d considered · %d selected · threshold %.2f",
			len(keys), len(r.Selected), r.Threshold)))

	fmt.Fprintln(w, t.Render())
}

// selectionMark renders the Selected column.
func selectionMark(st *styles, selected bool) string {
	if selected {
		return st.ok.Render("yes")
	}

	return st.dim.Render("no")
}
