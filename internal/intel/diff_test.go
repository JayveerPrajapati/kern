package intel

import (
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

func TestParseDiffOutput(t *testing.T) {
	out := `diff --git a/lib/lib.go b/lib/lib.go
index abc..def 100644
--- a/lib/lib.go
+++ b/lib/lib.go
@@ -3,4 +3,5 @@ func Public() string {
 	return inner()
 }
+// added comment
 }

diff --git a/client/client.go b/client/client.go
new file mode 100644
--- /dev/null
+++ b/client/client.go
@@ -0,0 +1,6 @@
+package client
+
+func NewThing() {
+}
+
+func Other() {
+}
`
	changes := parseDiffOutput(out)
	if len(changes) != 2 {
		t.Fatalf("expected 2 files, got %d", len(changes))
	}
	if changes[0].File != "lib/lib.go" {
		t.Fatalf("expected lib/lib.go, got %q", changes[0].File)
	}
	// Body: 2 context lines (3,4), then the "+" line lands on new line 5.
	if len(changes[0].Ranges) != 1 || changes[0].Ranges[0] != (LineRange{5, 5}) {
		t.Fatalf("expected range 5-5 for lib/lib.go, got %v", changes[0].Ranges)
	}
	if changes[1].File != "client/client.go" {
		t.Fatalf("expected client/client.go, got %q", changes[1].File)
	}
	if len(changes[1].Ranges) != 1 || changes[1].Ranges[0] != (LineRange{1, 7}) {
		t.Fatalf("expected range 1-7 for client/client.go, got %v", changes[1].Ranges)
	}
}

func TestParseDiffBinaryAndPathSpaces(t *testing.T) {
	out := `diff --git a/im age.png b/im age.png
index a6a3e7f..6735744 100644
Binary files a/im age.png and b/im age.png differ

diff --git a/gone.png b/gone.png
deleted file mode 100644
index 0000000..0000000
Binary files a/gone.png and /dev/null differ
`
	changes := parseDiffOutput(out)
	if len(changes) != 1 {
		t.Fatalf("expected 1 file (deletion dropped), got %v", changes)
	}
	if changes[0].File != "im age.png" {
		t.Fatalf("expected binary path preserved, got %q", changes[0].File)
	}
	if len(changes[0].Ranges) != 0 {
		t.Fatalf("expected whole-file (empty ranges) for binary, got %v", changes[0].Ranges)
	}
}

func TestParseHunk(t *testing.T) {
	cases := []struct {
		line  string
		start int
		count int
		ok    bool
	}{
		{"@@ -1,2 +3,4 @@", 3, 4, true},
		{"@@ -1 +1 @@", 1, 1, true},
		{"@@ -10,5 +20,0 @@", 20, 0, false},
		{"@@ -0,0 +1,3 @@", 1, 3, true},
	}
	for _, c := range cases {
		start, count, ok := hunkNewRange(c.line)
		if ok != c.ok || start != c.start || count != c.count {
			t.Errorf("hunkNewRange(%q) = %d,%d,%v want %d,%d,%v",
				c.line, start, count, ok, c.start, c.count, c.ok)
		}
	}
}

func TestParseDiffAddedLines(t *testing.T) {
	out := `--- a/lib/lib.go
+++ b/lib/lib.go
@@ -3,4 +3,5 @@ func Public() string {
 	return inner()
 }
+// added comment
 }
+
+func NewFunc() {
`
	changes := parseDiffOutput(out)
	if len(changes) != 1 || changes[0].File != "lib/lib.go" {
		t.Fatalf("unexpected changes: %v", changes)
	}
	// Only the "+" body lines count (new lines 5, 7-8), not the hunk window.
	if len(changes[0].Ranges) != 2 ||
		changes[0].Ranges[0] != (LineRange{5, 5}) ||
		changes[0].Ranges[1] != (LineRange{7, 8}) {
		t.Fatalf("expected added lines 5 and 7-8, got %v", changes[0].Ranges)
	}
}

func TestLineAwareChanges(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"lib/lib.go":       srcLib,
		"client/client.go": srcClient,
		"lib/lib_test.go":  srcTest,
	})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Whole-file analysis still flags every symbol in the file.
	whole := AnalyzeChanges(ix, []string{"lib/lib.go"})
	if len(whole.Changes[0].Symbols) != 4 {
		t.Fatalf("expected 4 symbols whole-file, got %v", whole.Changes[0].Symbols)
	}

	// A single changed line flags only the symbol that spans it.
	one := AnalyzeChangesRanged(ix, []FileChange{
		{File: "lib/lib.go", Ranges: []LineRange{{5, 5}}},
	})
	if got := one.Changes[0].Symbols; len(got) != 1 || got[0] != "Public" {
		t.Fatalf("expected only Public changed for line 5, got %v", got)
	}

	// Line 13 lands inside Deep (span 11-13), so only Deep is flagged.
	deep := AnalyzeChangesRanged(ix, []FileChange{
		{File: "lib/lib.go", Ranges: []LineRange{{13, 13}}},
	})
	if got := deep.Changes[0].Symbols; len(got) != 1 || got[0] != "Deep" {
		t.Fatalf("expected only Deep changed for line 13, got %v", got)
	}
}

func TestHubCalleeRisk(t *testing.T) {
	srcA := `package lib

func Public() string {
	return inner()
}

func inner() string {
	return "x"
}

func C1() { Public() }
func C2() { Public() }
func C3() { Public() }
func C4() { Public() }
func C5() { Public() }
func C6() { Public() }
func C7() { Public() }
`
	srcB := `package lib

func Wrapper() string {
	return Public()
}
`
	srcC := `package lib

func LocalOnly() {}
`
	dir := writeTree(t, map[string]string{
		"lib/a.go": srcA,
		"lib/b.go": srcB,
		"lib/c.go": srcC,
	})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	report := AnalyzeChangesRanged(ix, []FileChange{
		{File: "lib/b.go", Ranges: []LineRange{{3, 3}}}, // Wrapper only
		{File: "lib/c.go", Ranges: []LineRange{{3, 3}}}, // LocalOnly only
	})
	if len(report.Changes) != 2 {
		t.Fatalf("expected 2 changes, got %d", len(report.Changes))
	}
	risk := map[string]float64{}
	for _, c := range report.Changes {
		risk[c.File] = c.Risk
	}
	if risk["lib/b.go"] <= risk["lib/c.go"] {
		t.Fatalf("expected Wrapper (calls hub Public) to outrank LocalOnly: %v", risk)
	}
}

func TestReviewRangedShowsLines(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"lib/lib.go":       srcLib,
		"client/client.go": srcClient,
		"lib/lib_test.go":  srcTest,
	})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := ReviewRanged(ix, []FileChange{
		{File: "lib/lib.go", Ranges: []LineRange{{5, 5}}},
	}, 2000)
	if !strings.Contains(out, "(lib/lib.go:3-5)") {
		t.Errorf("expected Public with file:line span, got:\n%s", out)
	}
	if !strings.Contains(out, "changed (1): Public") {
		t.Errorf("expected exactly one changed symbol, got:\n%s", out)
	}
	if strings.Contains(out, "UntestedHot") {
		t.Errorf("line-aware review must not flag UntestedHot for a line-5 edit:\n%s", out)
	}
}
