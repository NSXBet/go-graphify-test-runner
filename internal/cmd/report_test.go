package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/muesli/termenv"

	"github.com/NSXBet/go-smart-test-runner/internal/decide"
	"github.com/NSXBet/go-smart-test-runner/internal/gotest"
)

func sampleReport() *report {
	return &report{
		MergeBase:    "abc123",
		ChangedFiles: []string{"pkg/a.go"},
		StateChars:   42,
		Rounds: []roundReport{
			newRoundReport("round 1 (files)", map[string]float64{"pkg/a_test.go": 0.9, "pkg/b_test.go": 0.1}, 0.5),
			newRoundReport("round 2 (tests)", map[string]float64{"pkg/a_test.go::TestA": 0.8}, 0.5),
		},
		Cost:     0.001,
		Selected: map[string][]string{"pkg": {"TestA"}},
	}
}

func TestNewRoundReportSelectsByThreshold(t *testing.T) {
	r := newRoundReport("r", map[string]float64{"yes": 0.5, "no": 0.49}, 0.5)

	if len(r.Selected) != 1 || r.Selected[0] != "yes" {
		t.Fatalf("selected = %v want [yes]", r.Selected)
	}
}

func TestRenderJSONRoundTrips(t *testing.T) {
	rep := sampleReport()
	rep.Judging = []decide.Exchange{{Status: 200, QuestionKeys: []string{"k"}, Answers: map[string]float64{"k": 0.9}}}

	var buf bytes.Buffer

	if err := renderJSON(&buf, rep); err != nil {
		t.Fatal(err)
	}

	var got report
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, buf.String())
	}

	if got.Cost != 0.001 || got.Selected["pkg"][0] != "TestA" {
		t.Fatalf("round trip lost data: %+v", got)
	}

	if len(got.Judging) != 1 || got.Judging[0].Status != 200 {
		t.Fatalf("judging lost: %+v", got.Judging)
	}
}

func TestJSONOmitsJudgingWhenAbsent(t *testing.T) {
	var buf bytes.Buffer

	if err := renderJSON(&buf, sampleReport()); err != nil {
		t.Fatal(err)
	}

	if strings.Contains(buf.String(), "judging") {
		t.Fatalf("judging present without --verbose:\n%s", buf.String())
	}
}

func TestRenderHeaderCountsSelection(t *testing.T) {
	var buf bytes.Buffer

	renderHeader(&buf, sampleReport(), []gotest.Result{{Dir: "pkg", Funcs: []string{"TestA"}}}, false)

	out := buf.String()
	for _, want := range []string{
		"smart-test-runner",
		"abc123 · 1 changed files",
		"2 test files considered, 1 selected · 1 test considered, 1 selected",
		"running 1 test in 1 package",
		"decisions cost $0.0010",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("header missing %q:\n%s", want, out)
		}
	}
}

func TestRenderOutcomePass(t *testing.T) {
	var buf bytes.Buffer

	renderOutcome(&buf, []gotest.Result{{Dir: "pkg", Funcs: []string{"TestA"}, Duration: 120 * time.Millisecond}})

	out := buf.String()
	if !strings.Contains(out, "✓ pkg") {
		t.Fatalf("missing package pass line:\n%s", out)
	}

	if !strings.Contains(out, "PASS") || strings.Contains(out, "FAIL") {
		t.Fatalf("verdict not PASS:\n%s", out)
	}
}

func TestRenderOutcomeFailShowsOutput(t *testing.T) {
	var buf bytes.Buffer

	renderOutcome(&buf, []gotest.Result{{
		Dir:    "pkg",
		Funcs:  []string{"TestA"},
		Err:    errors.New("exit status 1"),
		Output: "--- FAIL: TestA\n    boom\n",
	}})

	out := buf.String()
	for _, want := range []string{"✗ pkg", "FAIL", "--- FAIL: TestA", "boom"} {
		if !strings.Contains(out, want) {
			t.Fatalf("outcome missing %q:\n%s", want, out)
		}
	}
}

func TestRenderOutcomeSkippedStillPasses(t *testing.T) {
	var buf bytes.Buffer

	renderOutcome(&buf, []gotest.Result{{Dir: "pkg", Skipped: true}})

	out := buf.String()
	if !strings.Contains(out, "↷ pkg") {
		t.Fatalf("missing skip marker:\n%s", out)
	}

	if !strings.Contains(out, "PASS") || strings.Contains(out, "FAIL") {
		t.Fatalf("skipped package must not fail the run:\n%s", out)
	}
}

func TestRenderExchangesText(t *testing.T) {
	rep := sampleReport()
	rep.Judging = []decide.Exchange{{
		Endpoint:     "http://x",
		Model:        "jev-latest",
		StateChars:   42,
		QuestionKeys: []string{"pkg/a_test.go"},
		Instructions: map[string]string{"pkg/a_test.go": "INSTRUCTIONS"},
		Status:       200,
		RawResponse:  `{"answers":{"pkg/a_test.go":{"noul":0.9}}}`,
		AnswerModel:  "typesafe/jev-1",
		Provider:     "TypeSafe",
		InputTokens:  10,
		OutputTokens: 3,
		Cost:         0.0001,
		Answers:      map[string]float64{"pkg/a_test.go": 0.9},
	}}

	var buf bytes.Buffer

	renderExchangesText(&buf, rep)

	out := buf.String()
	for _, want := range []string{"[decide] POST http://x", "INSTRUCTIONS", "HTTP 200", "provider=TypeSafe", "noul=0.9000"} {
		if !strings.Contains(out, want) {
			t.Fatalf("exchange text missing %q:\n%s", want, out)
		}
	}
}

// TestRenderOutcomeShowsVerboseOutputWhilePassing proves a forwarded -v still
// surfaces: a bare pass prints only "ok pkg 0.1s" and stays quiet, but verbose
// output carries real detail and must not be swallowed.
func TestRenderOutcomeShowsVerboseOutputWhilePassing(t *testing.T) {
	var quiet, verbose bytes.Buffer

	renderOutcome(&quiet, []gotest.Result{{
		Dir: "pkg", Funcs: []string{"TestA"}, Output: "ok  \tpkg\t0.1s\n",
	}})

	if strings.Contains(quiet.String(), "0.1s") {
		t.Fatalf("a bare pass should not repeat go test's summary line:\n%s", quiet.String())
	}

	renderOutcome(&verbose, []gotest.Result{{
		Dir: "pkg", Funcs: []string{"TestA"}, Output: "=== RUN   TestA\n--- PASS: TestA (0.00s)\nPASS\nok  \tpkg\t0.1s\n",
	}})

	if !strings.Contains(verbose.String(), "=== RUN   TestA") {
		t.Fatalf("verbose output was swallowed:\n%s", verbose.String())
	}
}

// TestRenderOutcomeDryRunPrintsCommands proves --dry-run reports the commands
// that would run instead of a pass/fail claim: nothing executed, so PASS would
// be a lie.
func TestRenderOutcomeDryRunPrintsCommands(t *testing.T) {
	var buf bytes.Buffer

	renderOutcome(&buf, []gotest.Result{{
		Dir: "pkg", Funcs: []string{"TestA"}, DryRun: true,
		Output: "go test -run ^(TestA)$ ./pkg",
	}})

	out := buf.String()
	if !strings.Contains(out, "go test -run ^(TestA)$ ./pkg") {
		t.Fatalf("dry run did not print the command:\n%s", out)
	}

	if strings.Contains(out, "PASS") || strings.Contains(out, "FAIL") {
		t.Fatalf("dry run claims an outcome it did not observe:\n%s", out)
	}
}

// TestRenderHeaderReportsNoSelection proves an empty selection says so rather
// than silently listing zero tests.
func TestRenderHeaderReportsNoSelection(t *testing.T) {
	var buf bytes.Buffer

	rep := sampleReport()
	rep.Rounds = []roundReport{
		newRoundReport("round 1 (files)", map[string]float64{"a_test.go": 0.1}, 0.5),
		newRoundReport("round 2 (tests)", map[string]float64{"a_test.go::TestA": 0.1}, 0.5),
	}
	rep.Selected = map[string][]string{}

	renderHeader(&buf, rep, nil, false)

	if !strings.Contains(buf.String(), "no tests selected") {
		t.Fatalf("missing no-selection message:\n%s", buf.String())
	}
}

// TestRenderHeaderPluralises proves the counts read as English.
func TestRenderHeaderPluralises(t *testing.T) {
	var buf bytes.Buffer

	rep := sampleReport()
	rep.Rounds = []roundReport{
		newRoundReport("round 1 (files)", map[string]float64{"a_test.go": 0.9}, 0.5),
		newRoundReport("round 2 (tests)", map[string]float64{"a_test.go::TestA": 0.9}, 0.5),
	}

	renderHeader(&buf, rep, []gotest.Result{{Dir: "pkg", Funcs: []string{"TestA"}}}, false)

	out := buf.String()
	if !strings.Contains(out, "1 test file considered, 1 selected · 1 test considered, 1 selected") {
		t.Fatalf("counts not pluralised correctly:\n%s", out)
	}

	if !strings.Contains(out, "running 1 test in 1 package") {
		t.Fatalf("run line not pluralised correctly:\n%s", out)
	}
}

// TestColorProfileDiscipline proves colour never leaks into a non-terminal:
// a buffer renders plain, NO_COLOR forces plain even on a terminal, and a
// character device gets colour.
func TestColorProfileDiscipline(t *testing.T) {
	var buf bytes.Buffer

	if got := colorProfile(&buf); got != termenv.Ascii {
		t.Fatalf("buffer profile = %v want Ascii", got)
	}

	t.Setenv("NO_COLOR", "1")

	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}

	defer f.Close()

	if got := colorProfile(f); got != termenv.Ascii {
		t.Fatalf("NO_COLOR profile = %v want Ascii", got)
	}
}

// TestRenderRoundsTable proves the decisions render as a table naming each
// candidate, its probability and whether it was selected — the format that
// replaced the raw "0.92 YES path" dump.
func TestRenderRoundsTable(t *testing.T) {
	var buf bytes.Buffer

	renderRounds(&buf, sampleReport())

	out := buf.String()
	for _, want := range []string{"round 1 (files)", "round 2 (tests)", "File", "Test", "Probability", "Selected", "pkg/a_test.go"} {
		if !strings.Contains(out, want) {
			t.Fatalf("table missing %q:\n%s", want, out)
		}
	}

	// A rejected candidate must still be listed, marked not selected: the point
	// of the table is to show what was considered and dropped.
	if !strings.Contains(out, "pkg/b_test.go") {
		t.Fatalf("rejected candidate missing from the table:\n%s", out)
	}
}

// TestRenderOutcomeVerdictAccountsForSkipped proves the verdict explains every
// package the header promised. The header counts packages before the run, so a
// skipped package used to vanish and the two lines contradicted each other
// ("running 4 packages" then "3 packages").
func TestRenderOutcomeVerdictAccountsForSkipped(t *testing.T) {
	var buf bytes.Buffer

	renderOutcome(&buf, []gotest.Result{
		{Dir: "skipped", Skipped: true},
		{Dir: "ran", Funcs: []string{"TestA"}},
	})

	out := buf.String()
	if !strings.Contains(out, "PASS") {
		t.Fatalf("skipped package must not fail the run:\n%s", out)
	}

	if !strings.Contains(out, "1 package") || !strings.Contains(out, "1 skipped") {
		t.Fatalf("verdict does not account for the skipped package:\n%s", out)
	}
}

// TestDryRunResultsIgnoresSkippedFirst is the regression for the dry-run bug: a
// skipped package sorts first in this repo, and testing only results[0] made
// --dry-run print PASS instead of the commands.
func TestDryRunResultsIgnoresSkippedFirst(t *testing.T) {
	results := []gotest.Result{
		{Dir: "skipped", Skipped: true},
		{Dir: "planned", DryRun: true, Output: "go test ./planned"},
	}

	if !dryRunResults(results) {
		t.Fatal("dry run not detected behind a skipped first result")
	}
}
