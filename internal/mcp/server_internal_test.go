package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// resolveRoot / withinRoot / validateRoot are the root-confinement primitives
// that guard index-using tools. This file tests them directly (package mcp =
// internal test package) so the unexported functions are exercised without
// going through tool dispatch.

func TestResolveRootEmptyFallsBackToCwd(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if got := resolveRoot(""); got != cwd {
		t.Fatalf("resolveRoot(\"\") = %q, want cwd %q", got, cwd)
	}
}

func TestResolveRootCleansToAbsolute(t *testing.T) {
	dir := t.TempDir()
	// Relative input resolves against cwd.
	got := resolveRoot(filepath.Join("a", "b"))
	want, err := filepath.Abs(filepath.Join("a", "b"))
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Clean(want) {
		t.Fatalf("resolveRoot(relative) = %q, want %q", got, filepath.Clean(want))
	}
	// Trailing slash is cleaned away.
	got = resolveRoot(dir + string(filepath.Separator))
	if strings.HasSuffix(got, string(filepath.Separator)) {
		t.Fatalf("resolveRoot must clean the trailing slash, got %q", got)
	}
	// `..` components are resolved lexically.
	nested := filepath.Join(dir, "sub", "..", "x")
	got = resolveRoot(nested)
	want = filepath.Join(dir, "x")
	if got != want {
		t.Fatalf("resolveRoot(%q) = %q, want %q", nested, got, want)
	}
}

func TestResolveRootDoesNotResolveSymlinks(t *testing.T) {
	// SENTINEL: resolveRoot only does Abs+Clean — it deliberately has no
	// symlink awareness (EvalSymlinks lives in withinRoot, which resolves
	// both sides before the confinement check). A symlinked root therefore
	// stays the link path. If resolveRoot ever starts resolving symlinks,
	// this assertion fails, flagging the change.
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "linked")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}
	if got := resolveRoot(link); got != filepath.Clean(link) {
		t.Fatalf("resolveRoot(symlink) = %q, want the unresolved link path %q", got, filepath.Clean(link))
	}
}

func TestResolveRootMissingPathStillResolves(t *testing.T) {
	// resolveRoot performs no existence check; a nonexistent root still
	// resolves to a cleaned absolute path (validateRoot allows nonexistent
	// roots because several tools create the directory before indexing).
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	if got := resolveRoot(missing); got != missing {
		t.Fatalf("resolveRoot(missing) = %q, want %q", got, missing)
	}
}

func TestWithinRoot(t *testing.T) {
	root := t.TempDir()

	// Normal relative path inside the root passes.
	p, err := withinRoot(root, filepath.Join("sub", "file.go"))
	if err != nil {
		t.Fatalf("within-root path rejected: %v", err)
	}
	if p != filepath.Join(root, "sub", "file.go") {
		t.Fatalf("resolved = %q", p)
	}

	// Nonexistent descendants are allowed (lexical fallback when the target
	// does not exist yet).
	if _, err := withinRoot(root, filepath.Join("new", "dir", "x.go")); err != nil {
		t.Fatalf("nonexistent descendant rejected: %v", err)
	}

	// `..` escape rejected.
	if _, err := withinRoot(root, ".."); err == nil {
		t.Fatal("`..` escape must be rejected")
	}
	if _, err := withinRoot(root, filepath.Join("..", "outside")); err == nil {
		t.Fatal("`../outside` escape must be rejected")
	}

	// Absolute path inside the root passes.
	inside := filepath.Join(root, "inside.go")
	if _, err := withinRoot(root, inside); err != nil {
		t.Fatalf("absolute path inside root rejected: %v", err)
	}

	// Absolute path outside the root rejected.
	outside := t.TempDir()
	if _, err := withinRoot(root, outside); err == nil {
		t.Fatal("absolute path outside root must be rejected")
	}

	// Symlink inside the root pointing outside rejected (EvalSymlinks).
	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err == nil {
		if _, err := withinRoot(root, link); err == nil {
			t.Fatal("symlink escaping the root must be rejected")
		}
	}

	// Symlink outside pointing back into the root stays allowed.
	back := filepath.Join(outside, "back")
	if err := os.Symlink(root, back); err == nil {
		if _, err := withinRoot(root, back); err != nil {
			t.Fatalf("symlink into the root must be allowed: %v", err)
		}
	}
}

func TestValidateRoot(t *testing.T) {
	// Filesystem root is never a project.
	if err := validateRoot("/"); err == nil {
		t.Fatal("filesystem root / must be rejected")
	}
	// Valid directory passes.
	dir := t.TempDir()
	if err := validateRoot(dir); err != nil {
		t.Fatalf("valid dir rejected: %v", err)
	}
	// A file (not a directory) is rejected.
	f := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validateRoot(f); err == nil {
		t.Fatal("non-directory root must be rejected")
	}
	// Nonexistent root is allowed by design (tools create it before indexing).
	if err := validateRoot(filepath.Join(t.TempDir(), "missing")); err != nil {
		t.Fatalf("nonexistent root should be allowed: %v", err)
	}
}

func TestIsFilesystemRoot(t *testing.T) {
	if !isFilesystemRoot("/") {
		t.Fatal("`/` must be a filesystem root")
	}
	if isFilesystemRoot(t.TempDir()) {
		t.Fatal("a temp dir must not be a filesystem root")
	}
}
