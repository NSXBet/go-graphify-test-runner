package impact

import (
	"context"
	"os/exec"
	"testing"
)

// TestUpdateMissingGrove proves the package reports a clear error when grove is
// not on PATH, rather than failing obscurely.
func TestUpdateMissingGrove(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	err := Update(context.Background(), t.TempDir())
	if err == nil {
		t.Fatal("want an error when grove is absent")
	}

	if _, lookErr := exec.LookPath("grove"); lookErr == nil {
		t.Skip("grove present on PATH; cannot exercise the missing case")
	}
}

// TestLoadMissingIndex proves Load names the missing index instead of returning
// a graph that will fail on first query.
func TestLoadMissingIndex(t *testing.T) {
	_, err := Load(context.Background(), t.TempDir())
	if err == nil {
		t.Fatal("want an error when the grove index is missing")
	}
}

func TestCappedSortsDedupsAndTruncates(t *testing.T) {
	in := []string{"b", "a", "b"}
	if got := capped(in); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("capped = %v want [a b]", got)
	}

	many := make([]string, 0, evidenceCap+5)
	for i := range evidenceCap + 5 {
		many = append(many, string(rune('a'+i)))
	}

	got := capped(many)
	if len(got) != evidenceCap+1 || got[len(got)-1] != "…" {
		t.Fatalf("capped did not truncate with an ellipsis: len=%d last=%q", len(got), got[len(got)-1])
	}
}
