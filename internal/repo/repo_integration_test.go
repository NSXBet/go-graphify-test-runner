//go:build integration

package repo

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// gitRepo builds a throwaway repository with one base commit.
func gitRepo(t *testing.T) (dir, base string) {
	t.Helper()

	dir = t.TempDir()

	run := func(args ...string) {
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

	run("init", "-q", "-b", "main")
	run("config", "commit.gpgsign", "false")

	write(t, dir, "pkg/keep.go", "package pkg\n\nfunc Keep() int { return 1 }\n")
	write(t, dir, "pkg/gone.go", "package pkg\n\nfunc Gone() int { return 2 }\n")
	write(t, dir, "README.md", "hi\n")

	run("add", "-A")
	run("commit", "-qm", "base")

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

// gitAdd stages a path so it appears in `git diff <mb>`.
func gitAdd(t *testing.T, dir, rel string) {
	t.Helper()

	cmd := exec.CommandContext(context.Background(), "git", "add", rel)
	cmd.Dir = dir

	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
}

func TestChangedLinesRealRepo(t *testing.T) {
	ctx := context.Background()
	dir, base := gitRepo(t)

	mb, err := MergeBase(ctx, dir, base)
	if err != nil {
		t.Fatal(err)
	}

	// Modify a tracked Go file, delete another, and add a new one. The new
	// file must be staged: `git diff <mb>` covers tracked changes only.
	write(t, dir, "pkg/keep.go", "package pkg\n\nfunc Keep() int { return 1 }\n\nfunc Extra() int { return 3 }\n")
	write(t, dir, "pkg/new.go", "package pkg\n\nfunc New() int { return 4 }\n")

	if rerr := os.Remove(filepath.Join(dir, "pkg", "gone.go")); rerr != nil {
		t.Fatal(rerr)
	}

	gitAdd(t, dir, "pkg/new.go")

	changed, err := ChangedLines(ctx, dir, mb)
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := changed["pkg/keep.go"]; !ok {
		t.Fatalf("keep.go not detected: %v", changed)
	}

	if _, ok := changed["pkg/new.go"]; !ok {
		t.Fatalf("new.go not detected: %v", changed)
	}

	if ranges, ok := changed["pkg/gone.go"]; !ok || ranges != nil {
		t.Fatalf("deleted gone.go = %v (present=%v), want present with nil ranges", ranges, ok)
	}

	if _, ok := changed["README.md"]; ok {
		t.Fatalf("non-Go README.md leaked in: %v", changed)
	}
}

func TestMergeBaseUnknownRef(t *testing.T) {
	ctx := context.Background()
	dir, _ := gitRepo(t)

	_, err := MergeBase(ctx, dir, "does-not-exist")
	if err == nil {
		t.Fatal("want error for unknown base ref")
	}
}

func TestDiffTextRealRepo(t *testing.T) {
	ctx := context.Background()
	dir, base := gitRepo(t)

	mb, err := MergeBase(ctx, dir, base)
	if err != nil {
		t.Fatal(err)
	}

	write(t, dir, "pkg/keep.go", "package pkg\n\nfunc Keep() int { return 99 }\n")

	diff, err := DiffText(ctx, dir, mb)
	if err != nil {
		t.Fatal(err)
	}

	if diff == "" {
		t.Fatal("empty diff for a modified file")
	}

	if !strings.Contains(diff, "return 99") {
		t.Fatalf("diff missing change:\n%s", diff)
	}
}

func TestChangedFilesRealRepo(t *testing.T) {
	ctx := context.Background()
	dir, base := gitRepo(t)

	mb, err := MergeBase(ctx, dir, base)
	if err != nil {
		t.Fatal(err)
	}

	write(t, dir, "pkg/keep.go", "package pkg\n\nfunc Keep() int { return 7 }\n")

	changed, err := ChangedLines(ctx, dir, mb)
	if err != nil {
		t.Fatal(err)
	}

	files := ChangedFiles(changed)
	if !reflect.DeepEqual(files, []string{"pkg/keep.go"}) {
		t.Fatalf("files = %v want [pkg/keep.go]", files)
	}
}
