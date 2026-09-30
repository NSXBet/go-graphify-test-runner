package repo

import (
	"reflect"
	"testing"
)

func TestParseUnifiedDiff(t *testing.T) {
	diff := `diff --git a/pkg/a.go b/pkg/a.go
--- a/pkg/a.go
+++ b/pkg/a.go
@@ -8,0 +10,3 @@ func A() {
+x
+y
+z
@@ -20,2 +23,0 @@ func B() {
-old
@@ -30 +31 @@ func C() {
+new
diff --git a/pkg/old.go b/pkg/old.go
--- a/pkg/old.go
+++ /dev/null
@@ -1,3 +0,0 @@
-a
diff --git a/README.md b/README.md
--- a/README.md
+++ b/README.md
@@ -1 +1 @@
-old
+new
`
	got := ParseUnifiedDiff(diff)
	want := map[string][][2]int{
		"pkg/a.go":   {{10, 12}, {23, 23}, {31, 31}},
		"pkg/old.go": nil,
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}

	if _, ok := got["README.md"]; ok {
		t.Fatalf("non-Go file kept")
	}
}

func TestParseUnifiedDiffRanges(t *testing.T) {
	tests := []struct {
		name string
		diff string
		want map[string][][2]int
	}{
		{
			name: "omitted count means a single line",
			diff: "+++ b/x.go\n@@ -1 +5 @@\n+a\n",
			want: map[string][][2]int{"x.go": {{5, 5}}},
		},
		{
			name: "explicit multi-line hunk",
			diff: "+++ b/x.go\n@@ -0,0 +10,3 @@\n+a\n+b\n+c\n",
			want: map[string][][2]int{"x.go": {{10, 12}}},
		},
		{
			name: "pure deletion defaults count to one",
			diff: "+++ b/x.go\n@@ -7 +7,0 @@\n-a\n",
			want: map[string][][2]int{"x.go": {{7, 7}}},
		},
		{
			name: "deleted file recorded with nil ranges",
			diff: "--- a/gone.go\n+++ /dev/null\n@@ -1,3 +0,0 @@\n-a\n",
			want: map[string][][2]int{"gone.go": nil},
		},
		{
			name: "non-Go path dropped",
			diff: "+++ b/README.md\n@@ -1 +1 @@\n-a\n+b\n",
			want: map[string][][2]int{},
		},
		{
			name: "no trailing newline keeps the last hunk",
			diff: "+++ b/x.go\n@@ -0,0 +4,2 @@\n+a\n+b",
			want: map[string][][2]int{"x.go": {{4, 5}}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseUnifiedDiff(tt.diff)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %v want %v", got, tt.want)
			}
		})
	}
}

func TestChangedFilesSorted(t *testing.T) {
	got := ChangedFiles(map[string][][2]int{"pkg/z.go": nil, "pkg/a.go": {{1, 1}}, "pkg/m.go": nil})
	want := []string{"pkg/a.go", "pkg/m.go", "pkg/z.go"}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}

	files := ChangedFiles(map[string][][2]int{"gone.go": nil})
	if !reflect.DeepEqual(files, []string{"gone.go"}) {
		t.Fatalf("deleted file missing: %v", files)
	}
}

// TestParseUnifiedDiffNewFile covers a brand-new Go file, whose --- side is
// /dev/null so the +++ path must still be recorded.
func TestParseUnifiedDiffNewFile(t *testing.T) {
	diff := "--- /dev/null\n+++ b/new.go\n@@ -0,0 +1,2 @@\n+a\n+b\n"

	got := ParseUnifiedDiff(diff)
	want := map[string][][2]int{"new.go": {{1, 2}}}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestParseUnifiedDiffPathsWithSpaces(t *testing.T) {
	diff := "+++ b/pkg/my file.go\n@@ -0,0 +3,1 @@\n+x\n"

	got := ParseUnifiedDiff(diff)
	want := map[string][][2]int{"pkg/my file.go": {{3, 3}}}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestParseHunkRejectsMalformedHeader(t *testing.T) {
	if _, ok := parseHunk("@@ not a hunk @@"); ok {
		t.Fatal("malformed header accepted")
	}

	hunk, ok := parseHunk("@@ -1 +9,4 @@")
	if !ok || hunk != [2]int{9, 12} {
		t.Fatalf("got %v ok=%v want [9 12]", hunk, ok)
	}
}
