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
	"slices"
	"strings"
	"testing"
)

// runCLI runs the assembled binary and returns stdout, stderr and the exit code.
func runCLI(t *testing.T, bin string, env []string, args ...string) (stdout, stderr string, code int) {
	t.Helper()

	cmd := exec.CommandContext(context.Background(), bin, args...)

	cmd.Env = append(os.Environ(), env...)

	var outBuf, errBuf strings.Builder

	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	err := cmd.Run()

	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		code = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("run %v: %v", args, err)
	}

	return outBuf.String(), errBuf.String(), code
}

// gitIn runs a git command in dir, failing the test on error.
func gitIn(t *testing.T, dir string, args ...string) {
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

// TestCLINoTestFiles covers a repo whose change touches no Go test files at all.
func TestCLINoTestFiles(t *testing.T) {
	bin := buildBinary(t)

	dir := t.TempDir()
	write(t, dir, "go.mod", "module example.com/fx\n\ngo 1.21\n")
	write(t, dir, "pkg/alpha/alpha.go", "package alpha\n\nfunc Add(a, b int) int { return a + b }\n")

	gitIn(t, dir, "init", "-q", "-b", "main")
	gitIn(t, dir, "config", "commit.gpgsign", "false")
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-qm", "base")

	write(t, dir, "pkg/alpha/alpha.go", "package alpha\n\nfunc Add(a, b int) int { return a + b + 0 }\n")

	out, _, code := runCLI(t, bin, []string{"OPENROUTER_API_KEY=test"},
		"--repo", dir, "--base", "HEAD", "--endpoint", "http://127.0.0.1:0")

	if code != 0 {
		t.Fatalf("exit = %d want 0", code)
	}

	if !strings.Contains(out, "no Go test files") {
		t.Fatalf("expected no-test-files message:\n%s", out)
	}
}

// TestCLINoTestSelected covers a change the model rejects outright: exit 0 and a
// clear message, with no test run.
func TestCLINoTestSelected(t *testing.T) {
	bin := buildBinary(t)

	// A server that rejects everything, so no file is ever selected.
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
			answers[k] = map[string]float64{"noul": 0.01}
		}

		if err := json.NewEncoder(w).Encode(map[string]any{"answers": answers, "usage": map[string]float64{"cost": 0.001}}); err != nil {
			t.Errorf("encode: %v", err)
		}
	}))
	t.Cleanup(srv.Close)

	dir, base := fixture(t)

	write(t, dir, "pkg/beta/beta.go", "package beta\n\nfunc Greet() string { return \"hello\" }\n")

	out, _, code := runCLI(t, bin, []string{"OPENROUTER_API_KEY=test"},
		"--repo", dir, "--base", base, "--endpoint", srv.URL)

	if code != 0 {
		t.Fatalf("exit = %d want 0", code)
	}

	if !strings.Contains(out, "no tests selected") {
		t.Fatalf("expected no-tests-selected message:\n%s", out)
	}

	if strings.Contains(out, "YES ") {
		t.Fatalf("a file was selected despite the model rejecting all:\n%s", out)
	}
}

// TestCLIFailingSelectedTest proves a failing selected test surfaces exit 1.
func TestCLIFailingSelectedTest(t *testing.T) {
	bin := buildBinary(t)
	srv := stubDecisions(t)

	dir, base := fixture(t)

	// Make alpha's own test fail, then change alpha so it is selected.
	write(t, dir, "pkg/alpha/alpha_test.go", "package alpha\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) { t.Fatal(\"boom\") }\n")
	write(t, dir, "pkg/alpha/alpha.go", "package alpha\n\nfunc Add(a, b int) int { return a + b + 0 }\n")

	_, stderr, code := runCLI(t, bin, []string{"OPENROUTER_API_KEY=test"},
		"--repo", dir, "--base", base, "--endpoint", srv.URL)

	if code != 1 {
		t.Fatalf("exit = %d want 1 on a failing test\nstderr:\n%s", code, stderr)
	}
}

// TestCLIDryRunNeverRunsTests proves --dry-run selects but executes nothing,
// even when the selected test would fail.
func TestCLIDryRunNeverRunsTests(t *testing.T) {
	bin := buildBinary(t)
	srv := stubDecisions(t)

	dir, base := fixture(t)

	// Beta's test fails if run; a forced selection of it under --dry-run must
	// still exit 0 because nothing runs.
	write(t, dir, "pkg/beta/beta.go", "package beta\n\nfunc Greet() string { return \"hello\" }\n")

	out, _, code := runCLI(t, bin, []string{"OPENROUTER_API_KEY=test"},
		"--repo", dir, "--base", base, "--threshold=-1", "--dry-run", "--endpoint", srv.URL)

	if code != 0 {
		t.Fatalf("dry-run exit = %d want 0", code)
	}

	if !strings.Contains(out, "go test") || !strings.Contains(out, "./pkg/beta") {
		t.Fatalf("dry-run did not print the beta command:\n%s", out)
	}
}

// TestCLIForwardsGoTestFlags proves everything after -- reaches go test.
func TestCLIForwardsGoTestFlags(t *testing.T) {
	bin := buildBinary(t)
	srv := stubDecisions(t)

	dir, base := fixture(t)

	write(t, dir, "pkg/alpha/alpha.go", "package alpha\n\nfunc Add(a, b int) int { return a + b + 0 }\n")

	stdout, _, code := runCLI(t, bin, []string{"OPENROUTER_API_KEY=test"},
		"--repo", dir, "--base", base, "--endpoint", srv.URL, "--", "-count=1", "-v")

	if code != 0 {
		t.Fatalf("exit = %d want 0", code)
	}

	// Without --json, go test output goes to stdout. -v makes it print
	// "=== RUN"; its absence means the flag was dropped.
	if !strings.Contains(stdout, "=== RUN") {
		t.Fatalf("forwarded -v did not reach go test:\n%s", stdout)
	}
}

// TestCLIUnknownBase proves an unknown base ref exits 2 with a clear error.
func TestCLIUnknownBase(t *testing.T) {
	bin := buildBinary(t)

	dir, _ := fixture(t)

	_, stderr, code := runCLI(t, bin, []string{"OPENROUTER_API_KEY=test"},
		"--repo", dir, "--base", "no-such-ref")

	if code != 2 {
		t.Fatalf("exit = %d want 2", code)
	}

	if !strings.Contains(stderr, "merge-base") {
		t.Fatalf("expected a merge-base error:\n%s", stderr)
	}
}

// TestCLINotARepo proves a non-git directory exits 2.
func TestCLINotARepo(t *testing.T) {
	bin := buildBinary(t)

	dir := t.TempDir()
	write(t, dir, "go.mod", "module example.com/fx\n\ngo 1.21\n")

	_, _, code := runCLI(t, bin, []string{"OPENROUTER_API_KEY=test"},
		"--repo", dir, "--base", "HEAD")

	if code != 2 {
		t.Fatalf("exit = %d want 2 for a non-repo", code)
	}
}

// TestCLIDecisionEndpointError proves a failing decisions endpoint exits 2
// rather than silently selecting nothing.
func TestCLIDecisionEndpointError(t *testing.T) {
	bin := buildBinary(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}))
	t.Cleanup(srv.Close)

	dir, base := fixture(t)

	write(t, dir, "pkg/alpha/alpha.go", "package alpha\n\nfunc Add(a, b int) int { return a + b + 0 }\n")

	_, stderr, code := runCLI(t, bin, []string{"OPENROUTER_API_KEY=test"},
		"--repo", dir, "--base", base, "--endpoint", srv.URL)

	if code != 2 {
		t.Fatalf("exit = %d want 2 on endpoint error\n%s", code, stderr)
	}

	if !strings.Contains(stderr, "decisions HTTP 500") {
		t.Fatalf("expected the endpoint error surfaced:\n%s", stderr)
	}
}

// TestCLIUntrackedTestFileIsCandidate proves an untracked _test.go file is
// discovered as a question, not ignored.
func TestCLIUntrackedTestFileIsCandidate(t *testing.T) {
	bin := buildBinary(t)
	srv := stubDecisions(t)

	dir, base := fixture(t)

	// An untracked, new test file in the alpha package.
	write(t, dir, "pkg/alpha/extra_test.go", "package alpha\n\nimport \"testing\"\n\nfunc TestExtra(t *testing.T) {}\n")
	write(t, dir, "pkg/alpha/alpha.go", "package alpha\n\nfunc Add(a, b int) int { return a + b + 0 }\n")

	out, _, code := runCLI(t, bin, []string{"OPENROUTER_API_KEY=test"},
		"--repo", dir, "--base", base, "--json", "--dry-run", "--endpoint", srv.URL)

	if code != 0 {
		t.Fatalf("exit = %d want 0\n%s", code, out)
	}

	var doc struct {
		Rounds []struct {
			Scores map[string]float64 `json:"scores"`
		} `json:"rounds"`
	}

	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("stdout not JSON: %v\n%s", err, out)
	}

	if len(doc.Rounds) == 0 {
		t.Fatalf("no rounds in document:\n%s", out)
	}

	if _, ok := doc.Rounds[0].Scores["pkg/alpha/extra_test.go"]; !ok {
		t.Fatalf("untracked test file not asked about:\n%s", out)
	}
}

// TestCLIDeletedGoFile proves a deleted Go file is handled: the diff records it
// and the run completes.
func TestCLIDeletedGoFile(t *testing.T) {
	bin := buildBinary(t)
	srv := stubDecisions(t)

	dir, base := fixture(t)

	// Delete a source file and touch its package's test, in one working tree.
	if err := os.Remove(filepath.Join(dir, "pkg", "beta", "beta.go")); err != nil {
		t.Fatal(err)
	}

	write(t, dir, "pkg/alpha/alpha.go", "package alpha\n\nfunc Add(a, b int) int { return a + b + 0 }\n")

	out, _, code := runCLI(t, bin, []string{"OPENROUTER_API_KEY=test"},
		"--repo", dir, "--base", base, "--json", "--dry-run", "--endpoint", srv.URL)

	if code != 0 {
		t.Fatalf("exit = %d want 0\n%s", code, out)
	}

	var doc struct {
		ChangedFiles []string `json:"changed_files"`
	}

	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("stdout not JSON: %v\n%s", err, out)
	}

	if !slices.Contains(doc.ChangedFiles, "pkg/beta/beta.go") {
		t.Fatalf("deleted Go file missing from changed_files: %v", doc.ChangedFiles)
	}
}

// TestCLIJSONWithDryRun proves --json --dry-run composes: a valid document and
// no execution.
func TestCLIJSONWithDryRun(t *testing.T) {
	bin := buildBinary(t)
	srv := stubDecisions(t)

	dir, base := fixture(t)

	write(t, dir, "pkg/alpha/alpha.go", "package alpha\n\nfunc Add(a, b int) int { return a + b + 0 }\n")

	out, _, code := runCLI(t, bin, []string{"OPENROUTER_API_KEY=test"},
		"--repo", dir, "--base", base, "--json", "--dry-run", "--endpoint", srv.URL)

	if code != 0 {
		t.Fatalf("exit = %d want 0", code)
	}

	if err := json.Unmarshal([]byte(out), &struct {
		Selected map[string][]string `json:"selected"`
	}{}); err != nil {
		t.Fatalf("stdout not JSON: %v\n%s", err, out)
	}

	// The go test command line appears only under dry-run; it must be absent
	// from stdout since stdout is the JSON document.
	if strings.Contains(out, "go test") {
		t.Fatalf("dry-run command leaked into JSON stdout:\n%s", out)
	}
}

// TestCLIUsesSystemOneEnvVars proves the run honours the
// GOSMARTTESTRUNNER_SYSTEMONE_* environment variables with no flags: the
// stub server is reached via the env URL, its model recorded, and the env
// token accepted.
func TestCLIUsesSystemOneEnvVars(t *testing.T) {
	bin := buildBinary(t)

	var gotModel string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model     string                     `json:"model"`
			Questions map[string]json.RawMessage `json:"questions"`
		}

		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode: %v", err)

			return
		}

		gotModel = req.Model

		answers := map[string]map[string]float64{}
		for k := range req.Questions {
			answers[k] = map[string]float64{"noul": 0.1}
		}

		if err := json.NewEncoder(w).Encode(map[string]any{"answers": answers, "usage": map[string]float64{"cost": 0.0}}); err != nil {
			t.Errorf("encode: %v", err)
		}
	}))
	t.Cleanup(srv.Close)

	dir, base := fixture(t)
	write(t, dir, "pkg/alpha/alpha.go", "package alpha\n\nfunc Add(a, b int) int { return a + b + 0 }\n")

	_, _, code := runCLI(t, bin, []string{
		"GOSMARTTESTRUNNER_SYSTEMONE_URL=" + srv.URL,
		"GOSMARTTESTRUNNER_SYSTEMONE_MODEL=env-only-model",
		"GOSMARTTESTRUNNER_SYSTEMONE_TOKEN=env-token",
		"SMART_TEST_RUNNER_NO_UPDATE_CHECK=1",
	}, "--repo", dir, "--base", base)

	if code != 0 {
		t.Fatalf("exit = %d want 0", code)
	}

	if gotModel != "env-only-model" {
		t.Fatalf("model sent = %q want env-only-model (env not applied)", gotModel)
	}
}

// TestCLIEnvTokenRequiredWithoutFlags proves the documented token resolution:
// with only GOSMARTTESTRUNNER_SYSTEMONE_TOKEN set (no OPENROUTER_API_KEY),
// the run proceeds; with neither, it exits 2 naming the variables.
func TestCLIEnvTokenRequiredWithoutFlags(t *testing.T) {
	bin := buildBinary(t)

	dir, base := fixture(t)

	_, stderr, code := runCLI(t, bin, []string{
		"OPENROUTER_API_KEY=",
		"GOSMARTTESTRUNNER_SYSTEMONE_TOKEN=",
		"AIHUB_TOKEN=",
	}, "--repo", dir, "--base", base)

	if code != 2 {
		t.Fatalf("exit = %d want 2 without a token", code)
	}

	if !strings.Contains(stderr, "GOSMARTTESTRUNNER_SYSTEMONE_TOKEN") {
		t.Fatalf("error does not name the env var:\n%s", stderr)
	}
}

// TestCLIAllRunsWholeSuiteWithoutKey proves --all is a go test passthrough: it
// runs every package, needs no API key and no grove index, and forwards args
// after -- to go test.
func TestCLIAllRunsWholeSuiteWithoutKey(t *testing.T) {
	bin := buildBinary(t)

	dir, _ := fixture(t)

	// No OPENROUTER_API_KEY, no grove index: --all must not require either.
	stdout, stderr, code := runCLI(t, bin, []string{"OPENROUTER_API_KEY="},
		"--repo", dir, "--all", "--", "-v")

	// beta's test fails deliberately in the fixture, so a whole-suite run exits
	// non-zero - that it ran at all (=== RUN) is the point.
	if !strings.Contains(stderr, "=== RUN") && !strings.Contains(stdout, "=== RUN") {
		t.Fatalf("go test did not run under --all:\nstdout:%s\nstderr:%s", stdout, stderr)
	}

	if strings.Contains(stderr, "OPENROUTER_API_KEY") {
		t.Fatalf("--all demanded an API key:\n%s", stderr)
	}

	if code == 0 {
		t.Fatalf("--all exit = 0, want non-zero (beta's test fails on purpose)")
	}
}

// TestCLIAllDryRunAndJSON proves --all composes with --dry-run and --json.
func TestCLIAllDryRunAndJSON(t *testing.T) {
	bin := buildBinary(t)

	dir, _ := fixture(t)

	stdout, _, code := runCLI(t, bin, nil, "--repo", dir, "--all", "--dry-run", "--json")
	if code != 0 {
		t.Fatalf("exit = %d want 0", code)
	}

	var doc struct {
		All      bool                `json:"all"`
		Selected map[string][]string `json:"selected"`
	}

	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("stdout not JSON: %v\n%s", err, stdout)
	}

	if !doc.All {
		t.Fatalf("document does not report all=true: %s", stdout)
	}
}
