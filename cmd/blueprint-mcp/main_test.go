package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestConfinementGate_AllowsInsideRoot verifies a repo inside the workspace
// roots passes the gate.
func TestConfinementGate_AllowsInsideRoot(t *testing.T) {
	root := t.TempDir()
	t.Setenv("BLUEPRINT_ROOTS", root)

	gate := confinementGate()
	repo := filepath.Join(root, "myapp")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := gate("blueprint_validate_staged", map[string]any{"repo": repo}); err != nil {
		t.Fatalf("repo inside root must be allowed, got: %v", err)
	}
}

// TestConfinementGate_DeniesOutsideRoot verifies a repo outside the workspace
// roots is denied with an explanatory error.
func TestConfinementGate_DeniesOutsideRoot(t *testing.T) {
	root := t.TempDir()
	t.Setenv("BLUEPRINT_ROOTS", root)

	gate := confinementGate()
	outside := t.TempDir() // sibling temp dir, not under root

	err := gate("blueprint_validate_staged", map[string]any{"repo": outside})
	if err == nil {
		t.Fatal("repo outside root must be denied")
	}
	if !strings.Contains(err.Error(), "outside the allowed workspace roots") {
		t.Fatalf("denial must explain why, got: %v", err)
	}
}

// TestConfinementGate_SiblingPathNotConfused verifies path containment is not
// fooled by a sibling that merely shares a prefix (e.g. /tmp/root-evil is NOT
// inside /tmp/root).
func TestConfinementGate_SiblingPathNotConfused(t *testing.T) {
	root := t.TempDir() // e.g. .../root
	t.Setenv("BLUEPRINT_ROOTS", root)

	gate := confinementGate()
	sibling := root + "-evil" // shares prefix but is NOT a child
	if err := os.MkdirAll(sibling, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(sibling) })

	if err := gate("blueprint_validate_staged", map[string]any{"repo": sibling}); err == nil {
		t.Fatal("prefix-sibling path must be denied")
	}
}

// TestConfinementGate_NoRepoArgIsNoop verifies calls without a repo argument
// are not confined (the gate only restricts repo targets).
func TestConfinementGate_NoRepoArgIsNoop(t *testing.T) {
	t.Setenv("BLUEPRINT_ROOTS", t.TempDir())
	gate := confinementGate()
	if err := gate("blueprint_explain_finding", map[string]any{}); err != nil {
		t.Fatalf("call without repo must pass, got: %v", err)
	}
}

// TestWorkspaceRoots_DefaultIsCwd verifies the roots default to the process
// working directory when BLUEPRINT_ROOTS is unset.
func TestWorkspaceRoots_DefaultIsCwd(t *testing.T) {
	t.Setenv("BLUEPRINT_ROOTS", "")
	roots := workspaceRoots()
	cwd, _ := os.Getwd()
	if len(roots) != 1 || roots[0] != cwd {
		t.Fatalf("expected roots=[%s], got %v", cwd, roots)
	}
}

// TestWorkspaceRoots_EnvParsing verifies colon/comma separated roots parse.
func TestWorkspaceRoots_EnvParsing(t *testing.T) {
	a := t.TempDir()
	b := t.TempDir()
	t.Setenv("BLUEPRINT_ROOTS", a+":"+b)

	roots := workspaceRoots()
	if len(roots) != 2 {
		t.Fatalf("expected 2 roots, got %v", roots)
	}
}

// TestConfinementGate_SymlinkEscape: a symlink that lives INSIDE an allowed
// root but points OUTSIDE it must be denied — the gate resolves symlinks
// before judging containment.
func TestConfinementGate_SymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir() // real target, outside the root
	t.Setenv("BLUEPRINT_ROOTS", root)

	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks not supported on this platform: %v", err)
	}

	gate := confinementGate()
	err := gate("blueprint_validate_staged", map[string]any{"repo": link})
	if err == nil {
		t.Fatal("symlink pointing outside the root must be denied")
	}
	if !strings.Contains(err.Error(), "outside the allowed workspace roots") {
		t.Fatalf("denial must explain why, got: %v", err)
	}
}

// TestConfinementGate_GatesPathTypedArgs: any path-typed argument key (not
// just "repo") is confined by the same gate, so future tools with path
// arguments are covered.
func TestConfinementGate_GatesPathTypedArgs(t *testing.T) {
	root := t.TempDir()
	t.Setenv("BLUEPRINT_ROOTS", root)

	gate := confinementGate()
	outside := t.TempDir()

	// A fabricated "targetPath" arg pointing outside must be denied.
	if err := gate("blueprint_some_future_tool", map[string]any{"targetPath": outside}); err == nil {
		t.Fatal("path-typed arg outside the root must be denied")
	}
	// And an inside-root targetPath must be allowed.
	inside := filepath.Join(root, "target")
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := gate("blueprint_some_future_tool", map[string]any{"targetPath": inside}); err != nil {
		t.Fatalf("inside-root path-typed arg must be allowed, got: %v", err)
	}
	// Non-path keys stay unconfined (existing no-op behavior).
	if err := gate("blueprint_some_future_tool", map[string]any{"mode": "full", "force": true}); err != nil {
		t.Fatalf("non-path args must pass the gate, got: %v", err)
	}
}

// TestConfinementGate_NonexistentPathDenied: a path that cannot be resolved
// (EvalSymlinks failure) is denied rather than passed through.
func TestConfinementGate_NonexistentPathDenied(t *testing.T) {
	root := t.TempDir()
	t.Setenv("BLUEPRINT_ROOTS", root)

	gate := confinementGate()

	err := gate("blueprint_validate_staged", map[string]any{"repo": filepath.Join(root, "does-not-exist")})
	if err == nil {
		t.Fatal("unresolvable path must be denied")
	}
}

// TestConfinementGate_NestedProposedPaths: blueprint_validate_proposed passes
// files as [{path, content}] — the gate must recursively confine files[].path
// to the workspace roots (denying paths outside, allowing paths inside) while
// never treating non-path-typed values (file content) as paths.
func TestConfinementGate_NestedProposedPaths(t *testing.T) {
	root := t.TempDir()
	t.Setenv("BLUEPRINT_ROOTS", root)

	gate := confinementGate()

	// Inside the root: must pass (the path must exist so EvalSymlinks
	// resolves it, mirroring the existing gate tests).
	inside := filepath.Join(root, "web")
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Outside the root: must be denied.
	outside := t.TempDir() // sibling temp dir, not under root

	// Deny: files[].path outside the roots.
	err := gate("blueprint_validate_proposed", map[string]any{
		"repo":   root,
		"source": "agent",
		"files": []any{
			map[string]any{"path": outside, "content": "package main\n"},
		},
	})
	if err == nil {
		t.Fatal("files[].path outside the root must be denied")
	}
	if !strings.Contains(err.Error(), "outside the allowed workspace roots") {
		t.Fatalf("denial must explain why, got: %v", err)
	}

	// Allow: files[].path inside the root.
	err = gate("blueprint_validate_proposed", map[string]any{
		"repo":   root,
		"source": "agent",
		"files": []any{
			map[string]any{"path": inside, "content": "package web\n"},
		},
	})
	if err != nil {
		t.Fatalf("files[].path inside the root must be allowed, got: %v", err)
	}

	// Content strings are never treated as paths: even a content value that
	// LOOKS like an outside path passes, because only path-typed keys are
	// confined (a "content" key is not path-typed).
	err = gate("blueprint_validate_proposed", map[string]any{
		"repo": root,
		"files": []any{
			map[string]any{"path": inside, "content": outside},
		},
	})
	if err != nil {
		t.Fatalf("content strings must not be confined as paths, got: %v", err)
	}

	// Top-level behavior is unchanged: a repo arg outside the roots is still
	// denied even when the files[].path values are inside.
	err = gate("blueprint_validate_proposed", map[string]any{
		"repo": outside,
		"files": []any{
			map[string]any{"path": inside, "content": "package web\n"},
		},
	})
	if err == nil {
		t.Fatal("top-level repo outside the root must still be denied")
	}
}

// TestConfinementGate_NestedProposedPathsAllowMissing: blueprint_validate_proposed
// validates file content that is NOT yet on disk, so files[].path entries that
// do not exist must be allowed as long as they stay inside the workspace roots
// (the pre-tool-use gate used to deny them with "cannot be resolved: no such
// file or directory"). The symlink-escape defense must still hold for the
// existing prefix, and paths that lexically escape the root via ‘..’ stay denied.
func TestConfinementGate_NestedProposedPathsAllowMissing(t *testing.T) {
	root := t.TempDir()
	t.Setenv("BLUEPRINT_ROOTS", root)
	gate := confinementGate()

	// A symlink inside the root pointing outside: a proposed file written
	// through it must be denied even though the file itself does not exist.
	outside := t.TempDir() // real target, outside the root
	symlinksOK := os.Symlink(outside, filepath.Join(root, "link")) == nil

	tests := []struct {
		name         string
		path         string
		denied       bool
		wantErrPart  string
		needsSymlink bool
	}{
		{
			name:   "new file inside root is allowed",
			path:   filepath.Join(root, "newproposed.go"),
			denied: false,
		},
		{
			name:        "new file escaping root via dotdot is denied",
			path:        "../escape/new.go",
			denied:      true,
			wantErrPart: "outside the allowed workspace roots",
		},
		{
			name:         "new file through symlink pointing outside root is denied",
			path:         filepath.Join(root, "link", "newfile.go"),
			denied:       true,
			wantErrPart:  "outside the allowed workspace roots",
			needsSymlink: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.needsSymlink && !symlinksOK {
				t.Skip("symlinks not supported on this platform")
			}
			err := gate("blueprint_validate_proposed", map[string]any{
				"repo":   root,
				"source": "agent",
				"files": []any{
					map[string]any{"path": tt.path, "content": "package main\n"},
				},
			})
			if tt.denied {
				if err == nil {
					t.Fatalf("files[].path %q must be denied", tt.path)
				}
				if !strings.Contains(err.Error(), tt.wantErrPart) {
					t.Fatalf("denial must explain why, got: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("files[].path %q must be allowed, got: %v", tt.path, err)
			}
		})
	}
}
