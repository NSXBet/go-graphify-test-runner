package repo

import (
	"bytes"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Root resolves the absolute repository root for dir.
func Root(dir string) (string, error) {
	out, err := Run(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// Run runs a git command in dir and returns stdout; on failure the error
// carries the trimmed stderr.
func Run(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
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
func MergeBase(root, base string) (string, error) {
	out, err := Run(root, "merge-base", "HEAD", base)
	if err != nil {
		return "", fmt.Errorf("merge-base HEAD %s failed: %s", base, err)
	}
	return strings.TrimSpace(out), nil
}

// DiffText returns `git diff <mb>` (working tree vs merge-base).
func DiffText(root, mb string) (string, error) {
	return Run(root, "diff", mb)
}

var hunkRe = regexp.MustCompile(`^@@ -\S+ \+(\d+)(?:,(\d+))? @@`)

// ChangedLines parses `git diff -U0` into added line ranges per Go file.
// A nil range slice marks a deleted file; non-Go paths are dropped.
func ChangedLines(root, mb string) (map[string][][2]int, error) {
	out, err := Run(root, "diff", "-U0", "--no-color", mb)
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
	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "--- "):
			pending = ""
			if p := strings.TrimPrefix(line, "--- "); p != "/dev/null" {
				pending = strings.TrimPrefix(p, "a/")
			}
		case strings.HasPrefix(line, "+++ "):
			path := strings.TrimPrefix(line, "+++ ")
			if path == "/dev/null" {
				// Deletion: the path comes from the `--- a/<path>` line.
				if pending != "" {
					res[pending] = nil
				}
				file = ""
				continue
			}
			path = strings.TrimPrefix(path, "b/")
			if !strings.HasSuffix(path, ".go") {
				file = ""
				continue
			}
			file = path
			if _, ok := res[file]; !ok {
				res[file] = nil
			}
		case strings.HasPrefix(line, "@@ "):
			if file == "" {
				continue
			}
			m := hunkRe.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			start, _ := strconv.Atoi(m[1])
			count := 1
			if m[2] != "" {
				count, _ = strconv.Atoi(m[2])
			}
			if count == 0 {
				count = 1
			}
			res[file] = append(res[file], [2]int{start, start + count - 1})
		}
	}
	return res
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
