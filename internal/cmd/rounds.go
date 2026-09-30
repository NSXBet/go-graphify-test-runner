package cmd

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
)

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

	// One blank line separates the outcome above from the decisions below.
	fmt.Fprintln(w)

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
