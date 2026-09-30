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
