package exec

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/execution"
)

// Testexecution.ShapeExecOutputModes pins the kern_verify-equivalent contract on
// script stdout: full (default) is byte-exact, summary is deterministic
// stats + head/tail, tail:N and lines:A-B are numbered windows, failures is
// rejected (no test semantics), and unknown modes are usage errors.
func TestShapeExecOutputModes(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 20; i++ {
		b.WriteString("line-")
		b.WriteString(string(rune('a' + i - 1)))
		b.WriteString("\n")
	}
	out := b.String()
	m := execution.ExecMeta{Runtime: "bash", DurMS: 12, Exit: 0, Anchor: "anchor-abc123def456"}

	if got, err := execution.ShapeExecOutput("", out, m); err != nil || got != out {
		t.Fatalf("default mode must be byte-exact full: err=%v equal=%v", err, got == out)
	}
	if got, err := execution.ShapeExecOutput("full", out, m); err != nil || got != out {
		t.Fatalf("full mode must be byte-exact: err=%v equal=%v", err, got == out)
	}
	sum, err := execution.ShapeExecOutput("summary", out, m)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	for _, want := range []string{"runtime bash", "stdout 140 bytes (20 lines)", "line-a", "line-t", "anchor-abc123def456"} {
		if !strings.Contains(sum, want) {
			t.Errorf("summary missing %q:\n%s", want, sum)
		}
	}
	tail, err := execution.ShapeExecOutput("tail:3", out, m)
	if err != nil {
		t.Fatalf("tail:3: %v", err)
	}
	if !strings.Contains(tail, "3\tline-t") || !strings.Contains(tail, "1\tline-r") {
		t.Errorf("tail:3 wrong window:\n%s", tail)
	}
	win, err := execution.ShapeExecOutput("lines:5-7", out, m)
	if err != nil {
		t.Fatalf("lines:5-7: %v", err)
	}
	for _, want := range []string{"5\tline-e", "7\tline-g"} {
		if !strings.Contains(win, want) {
			t.Errorf("lines:5-7 missing %q:\n%s", want, win)
		}
	}
	if strings.Contains(win, "line-d\n") && strings.Contains(win, "4\t") {
		t.Errorf("lines:5-7 leaked line 4:\n%s", win)
	}
	if _, err := execution.ShapeExecOutput("failures", out, m); err == nil || !strings.Contains(err.Error(), "no test semantics") {
		t.Errorf("failures must be rejected with a clear reason, got: %v", err)
	}
	if _, err := execution.ShapeExecOutput("bogus", out, m); err == nil || !strings.Contains(err.Error(), "unknown mode") {
		t.Errorf("unknown mode must error, got: %v", err)
	}
	if _, err := execution.ShapeExecOutput("tail:0", out, m); err == nil {
		t.Error("tail:0 must error")
	}
}

// TestExecAnchorRoundTrip pins the kept-run contract: every run is stored
// (in-memory + disk under the project root) and LoadExecRun finds it;
// crafted anchor ids cannot traverse paths.
func TestExecAnchorRoundTrip(t *testing.T) {
	root := t.TempDir()
	const out = "kept-output-123"
	anchor := storeForTest(t, root, out)
	if anchor == "" {
		t.Fatal("StoreAnchor returned empty")
	}
	got, err := execution.LoadExecRun(root, anchor)
	if err != nil || got != out {
		t.Fatalf("round-trip: err=%v got=%q", err, got)
	}
	if _, err := execution.LoadExecRun(root, "anchor-nope"); err == nil {
		t.Error("missing anchor must error")
	}
	if _, err := execution.LoadExecRun(root, "../../../etc/passwd"); err == nil {
		t.Error("path-shaped anchor must be rejected")
	}
}

func storeForTest(t *testing.T, root, out string) string {
	t.Helper()
	anchor := "anchor-" + strings.Repeat("f", 12)
	execution.SaveExecRun(root, anchor, out)
	return anchor
}

// Testexecution.DumpSteer pins the post-hoc footer: fires ONLY when the script
// statically reads files AND the stdout is large; computed answers (small,
// or big without a read primitive) stay untouched.
func TestDumpSteer(t *testing.T) {
	big := strings.Repeat("x", 5000)
	if execution.DumpSteer("print(open('f').read())", big, "anchor-1") == "" {
		t.Error("footer must fire for a file read returning 5KB")
	}
	if execution.DumpSteer("print(1+1)", big, "anchor-1") != "" {
		t.Error("footer must NOT fire without a read primitive")
	}
	if execution.DumpSteer("print(open('f').read())", "small", "anchor-1") != "" {
		t.Error("footer must NOT fire for small stdout")
	}
}

// TestExecEndToEndShapingAndAnchor exercises the full MCP path: a script run
// with output=summary returns the summary + anchor hint; the anchor then
// re-slices WITHOUT a code argument (proving no re-execution — a re-run
// would fail the missing-code check).
func TestExecEndToEndShapingAndAnchor(t *testing.T) {
	t.Setenv("KERN_ALLOW_EXEC", "1")
	// macOS has no unprivileged user namespaces, so script.RunScript
	// fail-closes unless the operator opts into unisolated runs — this is a
	// no-op on hosts where network isolation works (allowUnisolated() is only
	// consulted when networkNS() is nil). The heredoc touches no network, so
	// unisolated execution is harmless; this keeps the e2e shaping+anchor
	// coverage on macOS.
	t.Setenv("KERN_ALLOW_UNISOLATED", "1")
	root := t.TempDir()
	ctx := context.Background()

	var b strings.Builder
	for i := 0; i < 200; i++ {
		b.WriteString("padding line for size\n")
	}
	code := "cat <<'EOF'\n" + b.String() + "EOF\n"
	sum, err := Exec(ctx, map[string]any{
		"code":   code,
		"lang":   "bash",
		"root":   root,
		"output": "summary",
	})
	if err != nil {
		t.Fatalf("Exec summary: %v", err)
	}
	if !strings.Contains(sum, "stdout") || !strings.Contains(sum, "padding line for size") {
		t.Fatalf("summary shape wrong:\n%.300s", sum)
	}
	const anchorPrefix = "anchor-"
	i := strings.Index(sum, anchorPrefix)
	if i < 0 {
		t.Fatalf("summary must carry the anchor id:\n%.300s", sum)
	}
	anchor := sum[i:]
	if j := strings.IndexAny(anchor, " \n)"); j >= 0 {
		anchor = anchor[:j]
	}
	tail, err := Exec(ctx, map[string]any{
		"root":   root,
		"anchor": anchor,
		"output": "tail:2",
	})
	if err != nil {
		t.Fatalf("re-slice via anchor must not re-execute: %v", err)
	}
	if !strings.Contains(tail, "padding line for size") {
		t.Fatalf("tail re-slice wrong:\n%s", tail)
	}
	// The disk copy must exist for cross-process survival.
	if _, err := os.Stat(filepath.Join(root, ".kern", "exec-runs", anchor+".log")); err != nil {
		t.Errorf("kept run not persisted to disk: %v", err)
	}
}

// TestExecFailsClosedWhenIsolationUnavailable pins the MCP-layer fail-closed
// contract: with exec allowed but NO unisolated override, an Exec call must
// refuse to run on hosts where network isolation is unavailable (darwin,
// windows). On hosts where isolation works the fail-closed path is simply
// unreachable and the test skips.
func TestExecFailsClosedWhenIsolationUnavailable(t *testing.T) {
	t.Setenv("KERN_ALLOW_EXEC", "1")
	t.Setenv("KERN_ALLOW_UNISOLATED", "")
	t.Setenv("KERN_ALLOW_NET", "")
	ctx := context.Background()
	_, err := Exec(ctx, map[string]any{
		"code": "echo hi",
		"lang": "bash",
		"root": t.TempDir(),
	})
	if err == nil {
		t.Skip("network isolation available on this host; fail-closed path not reachable")
	}
	if !strings.Contains(err.Error(), "network isolation not available") {
		t.Fatalf("fail-closed error must name the missing isolation, got: %v", err)
	}
}
