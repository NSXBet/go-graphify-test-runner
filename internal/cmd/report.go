package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"github.com/NSXBet/go-smart-test-runner/internal/decide"
)

// roundReport is one decision round's outcome.
type roundReport struct {
	Name      string             `json:"name"`
	Threshold float64            `json:"threshold"`
	Scores    map[string]float64 `json:"scores"`
	Selected  []string           `json:"selected"`
}

// report is the whole run's audit record, rendered as text or JSON.
type report struct {
	MergeBase    string              `json:"merge_base"`
	ChangedFiles []string            `json:"changed_files"`
	StateChars   int                 `json:"state_chars"`
	Rounds       []roundReport       `json:"rounds"`
	Cost         float64             `json:"cost_usd"`
	Selected     map[string][]string `json:"selected"`
	// Judging is the raw exchange with the decision model; present only when
	// --verbose was given.
	Judging []decide.Exchange `json:"judging,omitempty"`
}

// newRoundReport scores one round against the threshold.
func newRoundReport(name string, scores map[string]float64, threshold float64) roundReport {
	selected := []string{}

	for k, v := range scores {
		if v >= threshold {
			selected = append(selected, k)
		}
	}

	sort.Strings(selected)

	return roundReport{Name: name, Threshold: threshold, Scores: scores, Selected: selected}
}

// renderJSON writes the report as indented JSON.
func renderJSON(w io.Writer, rep *report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")

	return enc.Encode(rep)
}

// renderText writes the human-readable report, mirroring the historical output.
func renderText(w io.Writer, rep *report) {
	for _, r := range rep.Rounds {
		keys := make([]string, 0, len(r.Scores))
		for k := range r.Scores {
			keys = append(keys, k)
		}

		sort.Strings(keys)

		fmt.Fprintf(w, "%s: %d asked, %d selected\n", r.Name, len(keys), len(r.Selected))

		for _, k := range keys {
			mark := "no "
			if r.Scores[k] >= r.Threshold {
				mark = "YES"
			}

			fmt.Fprintf(w, "  %.2f %s %s\n", r.Scores[k], mark, k)
		}
	}

	fmt.Fprintf(w, "cost: $%.6f\n", rep.Cost)

	if len(rep.Selected) == 0 {
		fmt.Fprintln(w, "no tests selected")
	}
}

// renderExchangesText writes the verbose decisioning audit trail.
func renderExchangesText(w io.Writer, rep *report) {
	fmt.Fprintf(w, "state (%d chars):\n", rep.StateChars)

	for i := range rep.Judging {
		ex := &rep.Judging[i]

		fmt.Fprintf(w, "[decide] POST %s model=%s state=%d chars questions=%d %v\n",
			ex.Endpoint, ex.Model, ex.StateChars, len(ex.QuestionKeys), ex.QuestionKeys)

		for _, k := range ex.QuestionKeys {
			fmt.Fprintf(w, "[decide] question %q:\n%s\n", k, ex.Instructions[k])
		}

		fmt.Fprintf(w, "[decide] HTTP %d: %s\n", ex.Status, ex.RawResponse)

		if ex.AnswerModel != "" {
			fmt.Fprintf(w, "[decide] model=%s provider=%s id=%s tokens in/out=%d/%d cost=$%.6f\n",
				ex.AnswerModel, ex.Provider, ex.ID, ex.InputTokens, ex.OutputTokens, ex.Cost)
		}

		keys := append([]string(nil), ex.QuestionKeys...)
		sort.Strings(keys)

		for _, k := range keys {
			fmt.Fprintf(w, "[decide]   %-40s noul=%.4f\n", k, ex.Answers[k])
		}
	}
}
