package mcpgate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/config"
)

// TestGateBrokenSymlinkEscapeDenied pins the L-batch fix: a BROKEN symlink
// lexically inside an allowed root whose target directory is outside must be
// denied. Before the fix, gatePath's EvalSymlinks failure fell back to the
// raw absolute path (the symlink's lexical location, inside the root) and
// admitted it — a write through the symlink would create files outside the
// root. The ancestor walk now resolves the symlink itself (it exists) to its
// real target before the missing leaf is appended, so the real location is
// judged.
func TestGateBrokenSymlinkEscapeDenied(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()

	// A broken symlink inside root pointing at a missing path under outside:
	// root/link -> outside/not-there-yet. A write of root/link/new.go would
	// create outside/not-there-yet/new.go if admitted.
	if err := os.Symlink(filepath.Join(outside, "not-there-yet"), filepath.Join(root, "link")); err != nil {
		t.Skipf("symlink unavailable on this platform: %v", err)
	}

	g := NewGateForRoots([]string{root})
	if err := g.gatePath("root", filepath.Join(root, "link", "new.go")); err == nil {
		t.Fatal("a broken symlink into an outside directory must be denied (escape)")
	}

	// The legit case must still pass: a NEW file under the root itself
	// (not via a symlink) is a normal write-new-file request.
	if err := g.gatePath("root", filepath.Join(root, "fresh", "new.go")); err != nil {
		t.Fatalf("a new file inside the root must be admitted, got: %v", err)
	}
}

// TestGateFromEnvIgnoresConfigRootsWithoutOptIn pins F-CRIT (2026-10-26
// black-box audit) at the gate layer: mcp.roots in .kern/config.json under
// the process cwd does NOT widen the gate's allowed roots unless the
// operator opted in with KERN_MCP_ALLOW_CONFIG_ROOTS=1. Before the fix the
// config roots fed NewGateFromEnv directly, so a server started inside an
// untrusted repo gated against attacker-named roots.
func TestGateFromEnvIgnoresConfigRootsWithoutOptIn(t *testing.T) {
	attacker := t.TempDir()
	victim := t.TempDir()
	if err := os.MkdirAll(filepath.Join(attacker, ".kern"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := `{"mcp": {"roots": ["` + victim + `"]}}`
	if err := os.WriteFile(filepath.Join(attacker, ".kern", "config.json"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KERN_MCP_ROOTS", "")
	t.Setenv("KERN_ROOTS", "")
	t.Setenv("KERN_MCP_ALLOW_CONFIG_ROOTS", "")
	config.Reset()
	defer config.Reset()

	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(attacker); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)

	g := NewGateFromEnv()
	if err := g.gatePath("root", filepath.Join(victim, "planted.go")); err == nil {
		t.Fatal("gate must refuse the config-named victim root without the operator opt-in (F-CRIT)")
	}
	// Control: a path inside the startup cwd (the fail-closed default root)
	// is still admitted.
	if err := g.gatePath("root", filepath.Join(attacker, "inside.go")); err != nil {
		t.Fatalf("a path inside the startup cwd must be admitted, got: %v", err)
	}

	// The operator opt-in restores the old widened behavior.
	t.Setenv("KERN_MCP_ALLOW_CONFIG_ROOTS", "1")
	g = NewGateFromEnv()
	if err := g.gatePath("root", filepath.Join(victim, "planted.go")); err != nil {
		t.Fatalf("opt-in must honor config roots, got: %v", err)
	}
}
func TestGateErrorNamesAllowedRootsAndEscapes(t *testing.T) {
	t.Parallel()
	secretRoot := t.TempDir()
	g := &Gate{roots: []string{secretRoot}, enabled: true}
	err := g.gatePath("root", "/tmp/kern-outside-dir")
	if err == nil {
		t.Fatal("path outside the allowed roots must be denied")
	}
	if !strings.Contains(err.Error(), secretRoot) {
		t.Fatalf("denial must name the allowed roots, got: %v", err)
	}
	if !strings.Contains(err.Error(), "outside allowed roots") {
		t.Fatalf("denial should carry the confinement wording, got: %v", err)
	}
	if !strings.Contains(err.Error(), `"root"`) {
		t.Fatalf("denial should name the denied key, got: %v", err)
	}
	for _, escape := range []string{"KERN_MCP_ROOTS", "KERN_MCP_PERMISSIVE", "KERN_MCP_NO_CONFINE"} {
		if !strings.Contains(err.Error(), escape) {
			t.Fatalf("denial should document the %s escape, got: %v", escape, err)
		}
	}
}

// TestGateTildeExpansionDenied pins the 2026-10-25 audit finding R1: gatePath
// must reject "~"-prefixed values outright instead of silently admitting them
// as a non-existent in-root literal (filepath.Abs performs no tilde expansion,
// so the guard's intent — valueCannotTraverse flags "~" — and its effect
// diverged). valueCannotTraverse itself keeps flagging "~" so the fast-path
// still routes the value into validation.
func TestGateTildeExpansionDenied(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	g := &Gate{roots: []string{ws}, enabled: true}

	for _, v := range []string{"~/.ssh/id_rsa", "~user/x", "~/x"} {
		err := g.gatePath("path", v)
		if err == nil {
			t.Fatalf("path %q must be denied (tilde expansion is not supported), got nil", v)
		}
		if !strings.Contains(err.Error(), "'~' expansion is not supported") {
			t.Fatalf("path %q: expected the tilde-specific denial, got: %v", v, err)
		}
	}

	// The value-shape classifier still treats "~" as a traverse marker so
	// every "~"-prefixed value is routed into full validation.
	if valueCannotTraverse("~x") {
		t.Fatal("valueCannotTraverse must keep flagging '~'-prefixed values")
	}
}

// TestGateCheckConfinesRepoArg locks R3: the raw `repo` argument (the root
// name blueprint tools use) must be confined exactly like root/dir — a client
// passing `repo` directly must not bypass raw-arg confinement.
func TestGateCheckConfinesRepoArg(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	sub := filepath.Join(ws, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	g := &Gate{roots: []string{ws}, enabled: true}
	if err := g.Check("kern_validate_proposed", map[string]any{"repo": sub}); err != nil {
		t.Fatalf("repo arg inside the root must be allowed: %v", err)
	}
	outside := t.TempDir()
	err := g.Check("kern_validate_proposed", map[string]any{"repo": outside})
	if err == nil {
		t.Fatal("repo arg outside the root must be denied")
	}
	if !strings.Contains(err.Error(), "outside allowed roots") {
		t.Fatalf("expected a confinement denial, got %v", err)
	}
}

// TestGateConfinesStringLists pins the confinement-gate shape hardening: a
// plain string list under a path-typed key ("files", "linked_repos") is
// confined element-by-element — as a Go []string value, as JSON-decoded
// []any of strings, and nested inside arrays — so a path list can never
// bypass the gate by arriving as an array. Non-path keys carrying string
// lists are untouched.
func TestGateConfinesStringLists(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	sub := filepath.Join(ws, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	inA, inB := filepath.Join(ws, "a.go"), filepath.Join(sub, "b.go")
	// The paths must EXIST so symlink resolution succeeds on both sides
	// (macOS /var -> /private/var): an unresolvable path is denied by
	// gatePath even when lexically inside the root.
	for _, p := range []string{inA, inB} {
		if err := os.WriteFile(p, []byte("package p\n"), 0o644); err != nil {
			t.Fatalf("setup: %v", err)
		}
	}
	g := &Gate{roots: []string{ws}, enabled: true}

	// Inside the root: allowed in every shape.
	for name, args := range map[string]map[string]any{
		"go []string":     {"files": []string{inA, inB}},
		"json []any":      {"files": []any{inA, inB}},
		"linked_repos":    {"linked_repos": []string{inA, inB}},
		"nested array":    {"files": []any{[]any{inA}}},
		"nested []string": {"files": []any{[]string{inA}}},
		"non-path key":    {"symbols": []string{inA}},
	} {
		if err := g.Check("kern_x", args); err != nil {
			t.Errorf("%s: inside-root list must be allowed: %v", name, err)
		}
	}

	outside := t.TempDir()
	for name, args := range map[string]map[string]any{
		"go []string":     {"files": []string{inA, outside}},
		"json []any":      {"files": []any{inA, outside}},
		"linked_repos":    {"linked_repos": []string{outside}},
		"nested array":    {"files": []any{[]any{outside}}},
		"nested []string": {"files": []any{[]string{outside}}},
	} {
		if err := g.Check("kern_x", args); err == nil {
			t.Errorf("%s: outside-root element must be denied", name)
		} else if !strings.Contains(err.Error(), "outside allowed roots") {
			t.Errorf("%s: expected a confinement denial, got: %v", name, err)
		}
	}
	// A non-path key with an outside string is NOT confined (unchanged).
	if err := g.Check("kern_x", map[string]any{"symbols": []string{outside}}); err != nil {
		t.Errorf("non-path key string list must stay unconfined: %v", err)
	}
}

// TestGateOutputModeSelectorNotDenied pins the value-shape refinement (FIX
// 1): "output" is a path-typed key (write-capable tools like evidence
// export use it as a real file path), but kern_verify's "output" argument
// is a MODE selector (summary|failures|tail:N|lines:A-B|full), not a
// filesystem path. A mode selector cannot traverse — no "/" or "\\", no
// "~" prefix, no ".." path segment — so it must pass the gate, while every
// escape-shaped value under the same key is still validated and denied.
func TestGateOutputModeSelectorNotDenied(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	g := &Gate{roots: []string{ws}, enabled: true}

	// (a) mode selectors are NOT denied — this is the false-positive fix.
	for _, mode := range []string{"summary", "failures", "full", "tail:50", "lines:1-10"} {
		if err := g.Check("kern_verify", map[string]any{"command": "go test ./...", "output": mode}); err != nil {
			t.Errorf("output=%q (mode selector) must not be denied: %v", mode, err)
		}
	}

	// (b) absolute escapes are still denied.
	if err := g.Check("kern_verify", map[string]any{"output": "/etc/passwd"}); err == nil {
		t.Fatal("output=/etc/passwd must be denied")
	}
	if err := g.Check("kern_verify", map[string]any{"output": "/tmp/x.json"}); err == nil {
		t.Fatal("output=/tmp/x.json must be denied")
	}

	// (c) ".." escapes are still denied — both a bare parent segment and a
	// mixed relative path.
	if err := g.Check("kern_verify", map[string]any{"output": "../outside"}); err == nil {
		t.Fatal("output=../outside must be denied")
	}
	if err := g.Check("kern_verify", map[string]any{"output": "a/../b"}); err == nil {
		t.Fatal("output=a/../b must be denied")
	}

	// "~"-prefixed escapes are still denied.
	if err := g.Check("kern_verify", map[string]any{"output": "~/etc/passwd"}); err == nil {
		t.Fatal("output=~/etc/passwd must be denied")
	}

	// The genuine path-typed use of "output" is unchanged: a real file path
	// inside the root is allowed, outside the root is denied. The inside
	// file must EXIST so symlink resolution succeeds on both sides
	// (macOS /var -> /private/var): an unresolvable path is denied by
	// gatePath even when lexically inside the root.
	inside := filepath.Join(ws, "evidence.json")
	if err := os.WriteFile(inside, []byte("{}"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := g.Check("kern_evidence_export", map[string]any{"output": inside}); err != nil {
		t.Errorf("output inside the root must be allowed: %v", err)
	}
	if err := g.Check("kern_evidence_export", map[string]any{"output": "/tmp/evidence.json"}); err == nil {
		t.Fatal("output outside the root must be denied")
	}
}

// TestNewGateForRootsIgnoresEnv pins the per-App confinement rule at the
// gate level: a gate built FOR a specific App root (NewGateForRoots, the
// root-aware factory) confines to that root ONLY — KERN_MCP_ROOTS naming a
// second root cannot widen it. NewGateFromEnv keeps the env semantics for
// the single-root stdio server.
func TestNewGateForRootsIgnoresEnv(t *testing.T) {
	appA := t.TempDir()
	appB := t.TempDir()
	t.Setenv("KERN_MCP_ROOTS", appA+","+appB)
	t.Setenv("KERN_ROOTS", "")

	g := NewGateForRoots([]string{appA})
	if err := g.Check("kern_search", map[string]any{"root": appA}); err != nil {
		t.Fatalf("App A's own root must be allowed on its own gate: %v", err)
	}
	if err := g.Check("kern_search", map[string]any{"root": appB}); err == nil {
		t.Fatal("App B's root must be denied on App A's gate even with KERN_MCP_ROOTS naming both")
	}

	// Control: the env-driven gate (single-root stdio semantics) honors the
	// env and admits both roots.
	envGate := NewGateFromEnv()
	if err := envGate.Check("kern_search", map[string]any{"root": appB}); err != nil {
		t.Fatalf("NewGateFromEnv must keep honoring KERN_MCP_ROOTS: %v", err)
	}
}
