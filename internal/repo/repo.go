// Package repo reads the working tree's git state: the repository root, the
// merge-base with a base ref, and the changed-line ranges of a diff.
package repo

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Root resolves the absolute repository root for dir.
func Root(ctx context.Context, dir string) (string, error) {
	out, err := Run(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(out), nil
}

// Run runs a git command in dir and returns stdout. On failure the error
// carries the trimmed stderr.
func Run(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir

	var stdout, stderr bytes.Buffer

	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(stderr.String()))
	}

	return stdout.String(), nil
}

// MergeBase returns the merge-base of HEAD and base.
func MergeBase(ctx context.Context, root, base string) (string, error) {
	out, err := Run(ctx, root, "merge-base", "HEAD", base)
	if err != nil {
		return "", fmt.Errorf("merge-base HEAD %s failed: %w", base, err)
	}

	return strings.TrimSpace(out), nil
}

// DiffText returns `git diff <mb>` (working tree versus merge-base).
func DiffText(ctx context.Context, root, mb string) (string, error) {
	return Run(ctx, root, "diff", mb)
}

// hunkRe matches a `@@` hunk header and captures the post-image start and count.
var hunkRe = regexp.MustCompile(`^@@ -\S+ \+(\d+)(?:,(\d+))? @@`)

// ChangedLines parses `git diff -U0` into added line ranges per Go file.
// A nil range slice marks a deleted file; non-Go paths are dropped.
func ChangedLines(ctx context.Context, root, mb string) (map[string][][2]int, error) {
	out, err := Run(ctx, root, "diff", "-U0", "--no-color", mb)
	if err != nil {
		return nil, err
	}

	return ParseUnifiedDiff(out), nil
}

// ParseUnifiedDiff parses a `-U0` unified diff into added line ranges per Go
// file. A nil range slice marks a deleted file; non-Go paths are dropped.
func ParseUnifiedDiff(diff string) map[string][][2]int {
	res := map[string][][2]int{}

	var file, pending string

	for line := range strings.SplitSeq(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "--- "):
			pending = oldPath(line)
		case strings.HasPrefix(line, "+++ "):
			file = newPath(line, pending, res)
		case strings.HasPrefix(line, "@@ "):
			if file == "" {
				continue
			}

			if hunk, ok := parseHunk(line); ok {
				res[file] = append(res[file], hunk)
			}
		}
	}

	return res
}

// oldPath extracts the pre-image path from a `---` line, or "" for /dev/null.
func oldPath(line string) string {
	p := strings.TrimPrefix(line, "--- ")
	if p == "/dev/null" {
		return ""
	}

	return strings.TrimPrefix(p, "a/")
}

// newPath resolves the post-image path from a `+++` line, records a deletion
// under pending, and returns the file to attribute following hunks to.
func newPath(line, pending string, res map[string][][2]int) string {
	path := strings.TrimPrefix(line, "+++ ")
	if path == "/dev/null" {
		if pending != "" {
			res[pending] = nil
		}

		return ""
	}

	path = strings.TrimPrefix(path, "b/")
	if !strings.HasSuffix(path, ".go") {
		return ""
	}

	if _, ok := res[path]; !ok {
		res[path] = nil
	}

	return path
}

// parseHunk extracts the added-line range from a `@@` header.
func parseHunk(line string) ([2]int, bool) {
	m := hunkRe.FindStringSubmatch(line)
	if m == nil {
		return [2]int{}, false
	}

	start, err := strconv.Atoi(m[1])
	if err != nil {
		return [2]int{}, false
	}

	count := 1
	if m[2] != "" {
		count, err = strconv.Atoi(m[2])
		if err != nil {
			return [2]int{}, false
		}
	}

	if count == 0 {
		count = 1
	}

	return [2]int{start, start + count - 1}, true
}

// ChangedFiles returns the sorted file paths present in parsed diff ranges.
func ChangedFiles(changed map[string][][2]int) []string {
	files := make([]string, 0, len(changed))
	for f := range changed {
		files = append(files, f)
	}

	sort.Strings(files)

	return files
}
