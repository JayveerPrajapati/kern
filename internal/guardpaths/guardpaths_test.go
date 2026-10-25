package guardpaths

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestExtraRootsFromEnv pins the KERN_MCP_ROOTS parsing: ':' or ','
// separation, whitespace trimming, dedup across both variables, absolute
// resolution against the process cwd, and empty env = no extras.
func TestExtraRootsFromEnv(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	got := ExtraRootsFromEnv(func(k string) string {
		switch k {
		case "KERN_MCP_ROOTS":
			return "/a/b , /c/d:/e/f"
		case "KERN_ROOTS":
			return "/a/b:/g/h"
		}
		return ""
	})
	want := []string{"/a/b", "/c/d", "/e/f", "/g/h"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ExtraRootsFromEnv = %v, want %v", got, want)
	}
	// A relative entry resolves against the process cwd (operator env
	// semantics, parity with the MCP server's workspaceRootsFromEnv).
	got = ExtraRootsFromEnv(func(k string) string {
		if k == "KERN_MCP_ROOTS" {
			return "../sibling"
		}
		return ""
	})
	if len(got) != 1 || got[0] != filepath.Clean(filepath.Join(wd, "..", "sibling")) {
		t.Fatalf("relative root = %v, want cwd-resolved %s", got, filepath.Join(wd, "..", "sibling"))
	}
	// Empty env: no extras.
	if got := ExtraRootsFromEnv(func(string) string { return "" }); len(got) != 0 {
		t.Fatalf("empty env roots = %v, want none", got)
	}
	// Blank segments are dropped, not turned into stray roots.
	if got := ExtraRootsFromEnv(func(k string) string {
		if k == "KERN_MCP_ROOTS" {
			return " , ::, "
		}
		return ""
	}); len(got) != 0 {
		t.Fatalf("blank segments = %v, want none", got)
	}
}

// TestSafeWriteSegmentExtraRoots pins the extra-root threading at the
// segment level: absolute targets under an extra root are safe, the
// extra root itself and unconfigured locations are not, and relative
// paths stay bound to the primary root.
func TestSafeWriteSegmentExtraRoots(t *testing.T) {
	root := "/workspace/repo1"
	root2 := "/workspace/repo2"
	if !SafeWriteSegment("rm /workspace/repo2/scratch.go", root, []string{root2}) {
		t.Fatal("second-root target should be safe with extraRoots")
	}
	if !SafeWriteSegment("cp internal/a.go /workspace/repo2/b.go", root, []string{root2}) {
		t.Fatal("cp into second root should be safe with extraRoots")
	}
	if SafeWriteSegment("rm /workspace/repo2/scratch.go", root, nil) {
		t.Fatal("second-root target without extraRoots must not be safe")
	}
	if SafeWriteSegment("rm -rf /workspace/repo2", root, []string{root2}) {
		t.Fatal("write AT the extra root itself must not be safe")
	}
	if SafeWriteSegment("rm /workspace/repo3/x.go", root, []string{root2}) {
		t.Fatal("unconfigured third location must not be safe")
	}
	if SafeWriteSegment("rm ../repo2/x.go", root, []string{root2}) {
		t.Fatal("relative path must not reach an extra root")
	}
	// cp sources outside every root still gate even when the destination
	// is in scope: the read side is unrestricted, but keeping the argument
	// set fully in-scope keeps the verdict honest.
	if SafeWriteSegment("cp /etc/hosts /workspace/repo2/b.go", root, []string{root2}) {
		t.Fatal("cp with out-of-scope source must not be safe")
	}
}
