//go:build !windows

// guard_pause_scope_test.go pins the bash-steering pause scope (P2.2 of the
// guard-consent campaign): with an active bash-steering pause for the root,
// the Go hook passes serve-class bash commands RAW (exit 0, empty stdout —
// no serve redirection); without the pause the same payload serves. GATE
// verdicts are never paused by the scope.
package setup

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// quoteJSON is defined in guard_pause_test.go (same package).

func TestGuardHookBashSteeringPause(t *testing.T) {
	bin := buildCurrentKern(t)
	root := t.TempDir()
	home := t.TempDir()
	payload := `{"tool_name":"Bash","tool_input":{"command":"grep -rn kern ."}}`

	// Without the pause: the payload serves (non-empty stdout, exit 0).
	code, out, _ := runGoHook(t, bin, payload, []string{"PWD=" + root, "CLAUDE_PROJECT_DIR=" + root, "HOME=" + home})
	if code != 0 {
		t.Fatalf("no-pause serve exit = %d, want 0", code)
	}
	if out == "" {
		t.Fatal("no-pause serve produced no stdout — the read must serve")
	}

	// Mint the bash-steering pause record directly (the interactive tty
	// confirmation flow is covered by the guardpause unit tests; the hook's
	// consumption of the record is what this test pins).
	regDir := filepath.Join(home, ".kern", "guard")
	if err := os.MkdirAll(regDir, 0o755); err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	record := `{"schema_version":"kern-guard-pause","scope":"bash-steering","root":` + quoteJSON(canonical) +
		`,"reason":"serve misbehaving","expires":` + quoteJSON(now.Add(5*time.Minute).Format(time.RFC3339)) +
		`,"recorded":` + quoteJSON(now.Format(time.RFC3339)) + "}\n"
	if err := os.WriteFile(filepath.Join(regDir, "pauses.jsonl"), []byte(record), 0o644); err != nil {
		t.Fatal(err)
	}

	// With the pause: same serve-class payload passes RAW — exit 0, empty
	// stdout (no serve output; the host's own bash runs it untouched).
	code, out, _ = runGoHook(t, bin, payload, []string{"PWD=" + root, "CLAUDE_PROJECT_DIR=" + root, "HOME=" + home})
	if code != 0 {
		t.Fatalf("paused raw-pass exit = %d, want 0", code)
	}
	if out != "" {
		t.Fatalf("paused run must not serve, got stdout: %.200s", out)
	}

	// GATE verdicts are never paused: a write/destructive command still
	// denies (exit 2) under the same pause.
	gatePayload := `{"tool_name":"Bash","tool_input":{"command":"rm -rf /"}}`
	code, _, errOut := runGoHook(t, bin, gatePayload, []string{"PWD=" + root, "CLAUDE_PROJECT_DIR=" + root, "HOME=" + home})
	if code != 2 {
		t.Fatalf("paused gate exit = %d, want 2", code)
	}
	if errOut == "" {
		t.Fatal("paused gate deny produced no stderr")
	}
}
