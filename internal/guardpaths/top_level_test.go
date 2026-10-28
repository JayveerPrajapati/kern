package guardpaths

import (
	"path/filepath"
	"testing"
)

// TestRecursiveRmTopLevelAllSpellings pins the 2026-10-26 bypass: the guard
// gated `rm -rf internal` but allowed `rm -rf ./internal`, `rm -rf internal/`
// and the absolute form because the check tested the SPELLING for a slash
// instead of the resolved depth. All spellings of a top-level directory must
// gate, while a nested path stays a safe in-tree write.
func TestRecursiveRmTopLevelAllSpellings(t *testing.T) {
	root := filepath.Clean("/workspace/repo")
	cases := []struct {
		seg  string
		safe bool
	}{
		{"rm -rf internal", false},
		{"rm -rf ./internal", false},
		{"rm -rf internal/", false},
		{"rm -rf ./internal/", false},
		{"rm -rf /workspace/repo/internal", false},
		{"rm -r -f internal", false},
		{"rm -rf .", false},
		{"rm -rf ./", false},
		{"rm -rf internal/app", true},
		{"rm -rf ./internal/app", true},
		{"rm -rf /workspace/repo/internal/app", true},
		{"rm -f internal/app/x.go", true},
		{"rm -rf ../outside", false},
		{"rm -rf /etc", false},
	}
	for _, c := range cases {
		if got := SafeWriteSegment(c.seg, root, nil); got != c.safe {
			t.Errorf("SafeWriteSegment(%q) = %v, want %v", c.seg, got, c.safe)
		}
	}
}

// TestRecursiveRmTopLevelExtraRoot keeps the second-root contract: a direct
// child of a configured extra root is a top-level victim too (gates), while a
// nested path under it is safe.
func TestRecursiveRmTopLevelExtraRoot(t *testing.T) {
	root := filepath.Clean("/workspace/repo1")
	extra := filepath.Clean("/workspace/repo2")
	if SafeWriteSegment("rm -rf /workspace/repo2/internal", root, []string{extra}) {
		t.Fatal("direct child of an extra root must gate")
	}
	if !SafeWriteSegment("rm -rf /workspace/repo2/internal/app", root, []string{extra}) {
		t.Fatal("nested path under an extra root must stay safe")
	}
}
