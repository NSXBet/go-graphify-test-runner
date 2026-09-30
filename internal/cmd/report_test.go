package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/NSXBet/go-smart-test-runner/internal/decide"
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

func TestRenderText(t *testing.T) {
	var buf bytes.Buffer

	renderText(&buf, sampleReport())

	out := buf.String()
	for _, want := range []string{"round 1 (files): 2 asked, 1 selected", "0.90 YES pkg/a_test.go", "0.10 no  pkg/b_test.go", "cost: $0.001000"} {
		if !strings.Contains(out, want) {
			t.Fatalf("text report missing %q:\n%s", want, out)
		}
	}

	if strings.Contains(out, "no tests selected") {
		t.Fatal("text report claims nothing selected when tests were selected")
	}
}

func TestRenderTextNoSelection(t *testing.T) {
	rep := sampleReport()
	rep.Selected = map[string][]string{}

	var buf bytes.Buffer

	renderText(&buf, rep)

	if !strings.Contains(buf.String(), "no tests selected") {
		t.Fatalf("missing no-selection line:\n%s", buf.String())
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
