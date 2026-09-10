package host

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInjectAll(t *testing.T) {
	dir := t.TempDir()
	// Only CLAUDE.md exists → only the claude adapter is detected.
	if err := writeTestFile(dir, "CLAUDE.md", "# claude rules\n"); err != nil {
		t.Fatal(err)
	}
	pipeline := NewPipeline(NewRegistry())
	warns := pipeline.InjectAll(dir, testPacket(), 1200)
	if len(warns) != 0 {
		t.Fatalf("InjectAll warnings = %v, want none", warns)
	}
	// Exactly one file modified: CLAUDE.md gained the block; the other
	// adapters' files must NOT be created.
	claude, _ := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if !strings.Contains(string(claude), "kern-host:claude:start") {
		t.Error("CLAUDE.md missing injected block")
	}
	for _, rel := range []string{"AGENTS.md", ".cursor/rules/kern-context.mdc", ".github/copilot-instructions.md"} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err == nil {
			t.Errorf("%s was created by InjectAll but was not detected", rel)
		}
	}
}

func TestInjectAllErrorPath(t *testing.T) {
	dir := t.TempDir()
	// CLAUDE.md is a DIRECTORY → the claude adapter's Inject fails; the
	// opencode/codex adapters (AGENTS.md file) still succeed.
	if err := os.MkdirAll(filepath.Join(dir, "CLAUDE.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeTestFile(dir, "AGENTS.md", "# rules\n"); err != nil {
		t.Fatal(err)
	}
	pipeline := NewPipeline(NewRegistry())
	warns := pipeline.InjectAll(dir, testPacket(), 1200)
	if len(warns) != 1 {
		t.Fatalf("InjectAll warnings = %d, want 1 (claude)", len(warns))
	}
	if warns[0].Adapter != "claude" {
		t.Errorf("warning adapter = %q, want claude", warns[0].Adapter)
	}
	// AGENTS.md got both opencode and codex blocks (write still succeeded).
	agents, _ := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if !strings.Contains(string(agents), "kern-host:opencode:start") || !strings.Contains(string(agents), "kern-host:codex:start") {
		t.Error("open/codex blocks missing on AGENTS.md after claude failed")
	}
}

func TestDryRun(t *testing.T) {
	dir := t.TempDir()
	if err := writeTestFile(dir, "CLAUDE.md", "# rules\n"); err != nil {
		t.Fatal(err)
	}
	pipeline := NewPipeline(NewRegistry())
	out := pipeline.DryRun(dir, testPacket(), 1200)
	for _, name := range []string{"opencode", "claude", "cursor", "copilot", "codex"} {
		if !strings.Contains(out, name+":") {
			t.Errorf("DryRun missing %s", name)
		}
	}
	if !strings.Contains(out, "[detected]") || !strings.Contains(out, "[not detected]") {
		t.Error("DryRun missing detected/not detected states")
	}
	if !strings.Contains(out, "kern context") {
		t.Error("DryRun with pkt missing rendered block")
	}
	// Without a packet there is no rendered block.
	out2 := pipeline.DryRun(dir, nil, 1200)
	if strings.Contains(out2, "kern context") {
		t.Error("DryRun without pkt should not render a block")
	}
}

func TestCheck(t *testing.T) {
	dir := t.TempDir()
	if err := writeTestFile(dir, "CLAUDE.md", "# rules\n"); err != nil {
		t.Fatal(err)
	}
	pipeline := NewPipeline(NewRegistry())
	out := pipeline.Check(dir)
	if !strings.Contains(out, "claude: ") || !strings.Contains(out, "[detected]") {
		t.Errorf("Check claude line wrong: %q", out)
	}
	if !strings.Contains(out, "opencode: ") || !strings.Contains(out, "[missing]") {
		t.Errorf("Check opencode line wrong: %q", out)
	}
	// Before inject: claude clean.
	if !strings.Contains(out, "clean") {
		t.Errorf("Check should show clean before inject: %q", out)
	}
	pipeline.InjectAll(dir, testPacket(), 1200)
	out = pipeline.Check(dir)
	if !strings.Contains(out, "injected:") {
		t.Errorf("Check should show injected bytes after inject: %q", out)
	}
}

func TestUninstallAll(t *testing.T) {
	dir := t.TempDir()
	if err := writeTestFile(dir, "CLAUDE.md", "# claude rules\n"); err != nil {
		t.Fatal(err)
	}
	pipeline := NewPipeline(NewRegistry())
	pipeline.InjectAll(dir, testPacket(), 1200)
	if blk, _ := NewClaudeAdapter().Extract(dir); blk == "" {
		t.Fatal("precondition: claude block should exist")
	}
	warns := pipeline.UninstallAll(dir)
	if len(warns) != 0 {
		t.Fatalf("UninstallAll warnings = %v, want none", warns)
	}
	if blk, _ := NewClaudeAdapter().Extract(dir); blk != "" {
		t.Error("claude block still present after UninstallAll")
	}
	full, _ := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if string(full) != "# claude rules\n" {
		t.Errorf("CLAUDE.md after UninstallAll = %q, want original", string(full))
	}
	// Idempotent: no warnings on a second run with nothing to remove.
	if warns := pipeline.UninstallAll(dir); len(warns) != 0 {
		t.Errorf("second UninstallAll warnings = %v, want none", warns)
	}
}
