//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// stubDecisions starts a local decisions server that answers yes for any key
// containing "alpha" and no otherwise, standing in for OpenRouter.
func stubDecisions(t *testing.T) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Questions map[string]json.RawMessage `json:"questions"`
		}

		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode: %v", err)

			return
		}

		answers := map[string]map[string]float64{}

		for k := range req.Questions {
			noul := 0.1
			if strings.Contains(k, "alpha") {
				noul = 0.9
			}

			answers[k] = map[string]float64{"noul": noul}
		}

		if err := json.NewEncoder(w).Encode(map[string]any{"answers": answers, "usage": map[string]float64{"cost": 0.001}}); err != nil {
			t.Errorf("encode: %v", err)
		}
	}))
	t.Cleanup(srv.Close)

	return srv
}

// buildBinary compiles the CLI into a temp path.
func buildBinary(t *testing.T) string {
	t.Helper()

	bin := filepath.Join(t.TempDir(), "gtr")

	cmd := exec.CommandContext(context.Background(), "go", "build", "-o", bin, "./cmd/graphify-test-runner")
	cmd.Dir = repoRoot(t)

	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	return bin
}

// repoRoot finds this module's root by walking up to a go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}

		dir = parent
	}
}

// fixture builds a git repo with two packages and returns the dir and base ref.
func fixture(t *testing.T) (dir, base string) {
	t.Helper()

	dir = t.TempDir()

	git := func(args ...string) {
		t.Helper()

		cmd := exec.CommandContext(context.Background(), "git", args...)
		cmd.Dir = dir

		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")

		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	write(t, dir, "go.mod", "module example.com/fx\n\ngo 1.21\n")
	write(t, dir, "pkg/alpha/alpha.go", "package alpha\n\nfunc Add(a, b int) int { return a + b }\n")
	write(t, dir, "pkg/alpha/alpha_test.go", "package alpha\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {}\n")
	write(t, dir, "pkg/beta/beta.go", "package beta\n\nfunc Greet() string { return \"hi\" }\n")
	write(t, dir, "pkg/beta/beta_test.go", "package beta\n\nimport \"testing\"\n\nfunc TestGreet(t *testing.T) { t.Fatal(\"must not run\") }\n")

	git("init", "-q", "-b", "main")
	git("config", "commit.gpgsign", "false")
	git("add", "-A")
	git("commit", "-qm", "base")

	return dir, "HEAD"
}

func write(t *testing.T, dir, rel, content string) {
	t.Helper()

	full := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestCLIDryRunSelectsOnlyAlpha proves the assembled binary picks the alpha
// tests for an alpha change and never the beta tests (beta's test would fail
// if it ran).
func TestCLIDryRunSelectsOnlyAlpha(t *testing.T) {
	bin := buildBinary(t)
	srv := stubDecisions(t)

	dir, base := fixture(t)

	// A behavioural touch to the alpha package.
	write(t, dir, "pkg/alpha/alpha.go", "package alpha\n\nfunc Add(a, b int) int { return a + b + 0 }\n")

	cmd := exec.CommandContext(context.Background(), bin, "--repo", dir, "--base", base, "--dry-run", "--endpoint", srv.URL)

	cmd.Env = append(os.Environ(), "OPENROUTER_API_KEY=test")

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}

	got := string(out)

	if !strings.Contains(got, "pkg/alpha/alpha_test.go") {
		t.Fatalf("alpha test file not selected:\n%s", got)
	}

	if strings.Contains(got, "YES pkg/beta/beta_test.go") {
		t.Fatalf("beta selected despite no change:\n%s", got)
	}

	if !strings.Contains(got, "go test") || !strings.Contains(got, "./pkg/alpha") {
		t.Fatalf("dry-run did not print the go test command:\n%s", got)
	}

	// Proof the targeted run actually runs: the real run must not execute
	// beta's failing test.
	ran := exec.CommandContext(context.Background(), bin, "--repo", dir, "--base", base, "--endpoint", srv.URL)

	ran.Env = append(os.Environ(), "OPENROUTER_API_KEY=test")

	if out, err := ran.CombinedOutput(); err != nil {
		t.Fatalf("real run failed (beta test leaked in?): %v\n%s", err, out)
	}
}

// TestCLINoChanges proves an empty diff short-circuits with exit 0.
func TestCLINoChanges(t *testing.T) {
	bin := buildBinary(t)

	dir, base := fixture(t)

	cmd := exec.CommandContext(context.Background(), bin, "--repo", dir, "--base", base, "--endpoint", "http://127.0.0.1:0")

	cmd.Env = append(os.Environ(), "OPENROUTER_API_KEY=test")

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}

	if !strings.Contains(string(out), "no changes") {
		t.Fatalf("expected a no-changes message:\n%s", out)
	}
}

// TestCLIMissingKey proves the CLI refuses to run without any token, and the
// error names the env vars that supply one.
func TestCLIMissingKey(t *testing.T) {
	bin := buildBinary(t)

	dir, base := fixture(t)

	cmd := exec.CommandContext(context.Background(), bin, "--repo", dir, "--base", base)

	cmd.Env = append(os.Environ(),
		"OPENROUTER_API_KEY=",
		"AIHUB_TOKEN=",
		"GOGRAPHIFYTESTRUNNER_SYSTEMONE_TOKEN=")

	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("want a nonzero exit without a token; out=%s", out)
	}

	for _, want := range []string{"GOGRAPHIFYTESTRUNNER_SYSTEMONE_TOKEN", "OPENROUTER_API_KEY"} {
		if !strings.Contains(string(out), want) {
			t.Fatalf("error does not mention %s:\n%s", want, out)
		}
	}

	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 2 {
		t.Fatalf("exit code = %v want 2", err)
	}
}

// TestCLIJSONIsPureAndComplete proves --json emits a parseable document on
// stdout covering the rounds and the selection — even when selected tests run
// and write to stderr.
func TestCLIJSONIsPureAndComplete(t *testing.T) {
	bin := buildBinary(t)
	srv := stubDecisions(t)

	dir, base := fixture(t)

	write(t, dir, "pkg/alpha/alpha.go", "package alpha\n\nfunc Add(a, b int) int { return a + b + 0 }\n")

	// Threshold below every score so both rounds select, forcing the real run.
	cmd := exec.CommandContext(context.Background(), bin, "--repo", dir, "--base", base, "--json", "--endpoint", srv.URL)

	cmd.Env = append(os.Environ(), "OPENROUTER_API_KEY=test")

	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	var doc struct {
		MergeBase    string   `json:"merge_base"`
		ChangedFiles []string `json:"changed_files"`
		Rounds       []struct {
			Name     string   `json:"name"`
			Selected []string `json:"selected"`
		} `json:"rounds"`
		Cost     float64             `json:"cost_usd"`
		Selected map[string][]string `json:"selected"`
		Judging  []any               `json:"judging"`
	}

	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("stdout is not clean JSON: %v\n%s", err, out)
	}

	if doc.MergeBase == "" || len(doc.Rounds) != 2 {
		t.Fatalf("incomplete document: %+v", doc)
	}

	if len(doc.Selected["pkg/alpha"]) == 0 {
		t.Fatalf("selection missing: %+v", doc.Selected)
	}

	if doc.Judging != nil {
		t.Fatalf("judging present without --verbose: %+v", doc.Judging)
	}

	// With --verbose the judging block must appear.
	verbose := exec.CommandContext(context.Background(), bin, "--repo", dir, "--base", base, "--json", "--verbose", "--endpoint", srv.URL)

	verbose.Env = append(os.Environ(), "OPENROUTER_API_KEY=test")

	vout, verr := verbose.Output()
	if verr != nil {
		t.Fatalf("verbose run: %v", verr)
	}

	if uerr := json.Unmarshal(vout, &doc); uerr != nil {
		t.Fatalf("verbose stdout is not clean JSON: %v\n%s", uerr, vout)
	}

	if len(doc.Judging) == 0 {
		t.Fatal("judging missing with --verbose")
	}
}
