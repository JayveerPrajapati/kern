package setup

// F10 regression tests: (1) setup/doctor cursor path alignment — the
// kern-first policy lives in .cursor/rules/kern.mdc (the only dir Cursor
// loads rules from; the project analog of ~/.cursor/rules/kern.mdc), legacy
// .cursor/instructions/kern.mdc installs stay valid and get migrated;
// (2) --dry-run is a true preview — nothing is written and no status may
// claim a write; (3) bare setup writes the policy for every agent whose
// config marker the run itself created (the live "permanent doctor warning
// after a fresh successful setup" repro).

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// snapshotTree records path -> content hash for every file under root.
func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	snap := map[string]string{}
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		rel, _ := filepath.Rel(root, p)
		snap[rel] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func assertTreeUnchanged(t *testing.T, before map[string]string, root string) {
	t.Helper()
	after := snapshotTree(t, root)
	if len(after) != len(before) {
		t.Fatalf("dry-run created/removed files: before %d, after %d", len(before), len(after))
	}
	for p, h := range after {
		if before[p] != h {
			t.Fatalf("dry-run modified %s (%s -> %s)", p, before[p], h)
		}
	}
}

// TestWireDryRunWritesNothing pins the core F10 dry-run contract: a
// dry-run WireWith leaves the project byte-identical.
func TestWireDryRunWritesNothing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("PATH", "/nonexistent")
	dir := t.TempDir()

	before := snapshotTree(t, dir)
	sts := WireWith(dir, nil, false, false, WireOptions{DryRun: true})
	assertTreeUnchanged(t, before, dir)

	// With a populated project (post-setup state) the preview must still
	// write nothing.
	Wire(dir, nil, false, false)
	before = snapshotTree(t, dir)
	WireWith(dir, nil, false, false, WireOptions{DryRun: true})
	assertTreeUnchanged(t, before, dir)
	_ = sts
}

// TestWireDryRunNotesNeverClaimWrite pins the dry-run wording: every
// status is a would-preview ("[dry-run] would …") and no note claims a
// write happened — including failure statuses.
func TestWireDryRunNotesNeverClaimWrite(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("PATH", "/nonexistent")
	dir := t.TempDir()
	// Cursor detected via .cursor/rules so the preview covers its steps too.
	if err := os.MkdirAll(filepath.Join(dir, ".cursor", "rules"), 0o755); err != nil {
		t.Fatal(err)
	}

	sts := WireWith(dir, nil, true, false, WireOptions{DryRun: true})
	if len(sts) == 0 {
		t.Fatal("dry-run returned no statuses")
	}
	sawCursorInstruction := false
	for _, s := range sts {
		if !strings.HasPrefix(s.Note, "[dry-run] would ") {
			t.Errorf("%s: note must start with %q, got %q", s.Agent, "[dry-run] would ", s.Note)
		}
		for _, claim := range []string{" written", " installed", " scaffolded"} {
			if !s.Skipped && strings.HasSuffix(s.Note, claim) {
				t.Errorf("%s: dry-run note claims a write: %q", s.Agent, s.Note)
			}
		}
		if !s.Installed && !s.Skipped {
			t.Errorf("%s: dry-run status must not be a failure: %+v", s.Agent, s)
		}
		if s.Agent == "cursor-instruction" {
			sawCursorInstruction = true
		}
	}
	if !sawCursorInstruction {
		t.Fatalf("preview must include the cursor instruction step: %+v", sts)
	}
}

// TestWireDetectWritesCursorPolicyToRulesDir pins the F10 path alignment:
// the cursor kern-first policy is written to .cursor/rules/kern.mdc (with
// the Cursor frontmatter), never to the legacy .cursor/instructions/ path.
func TestWireDetectWritesCursorPolicyToRulesDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("PATH", "/nonexistent")
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".cursor", "rules"), 0o755); err != nil {
		t.Fatal(err)
	}

	Wire(dir, nil, true, false)
	p := filepath.Join(dir, ".cursor", "rules", "kern.mdc")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("cursor policy not written to %s: %v", p, err)
	}
	if !strings.HasPrefix(string(b), cursorFrontmatter) {
		t.Fatalf("cursor policy missing frontmatter:\n%s", b)
	}
	if !strings.Contains(string(b), "kern-instruction:") {
		t.Fatalf("cursor policy missing kern-instruction marker:\n%s", b)
	}
	if _, err := os.Stat(filepath.Join(dir, ".cursor", "instructions", "kern.mdc")); err == nil {
		t.Fatal("legacy .cursor/instructions/kern.mdc must not be written")
	}

	// Idempotent: a re-run is byte-identical.
	Wire(dir, nil, true, false)
	b2, _ := os.ReadFile(p)
	if string(b) != string(b2) {
		t.Fatal("cursor policy not byte-stable across runs")
	}
}

// TestCheckAcceptsLegacyCursorInstructions pins the doctor-side policy: a
// legacy .cursor/instructions/kern.mdc install still counts as wired.
func TestCheckAcceptsLegacyCursorInstructions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("PATH", "/nonexistent")
	dir := t.TempDir()
	legacy := filepath.Join(dir, ".cursor", "instructions", "kern.mdc")
	if err := os.MkdirAll(filepath.Dir(legacy), 0o755); err != nil {
		t.Fatal(err)
	}
	// Cursor must be detectable: the detector keys on .cursor/mcp.json or
	// .cursor/rules (a legacy install always has one of them).
	if err := os.MkdirAll(filepath.Join(dir, ".cursor", "rules"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := instructionMarkerOpen + "\n\n# kern usage rules (legacy install)\n\n" + instructionMarkerClose + "\n"
	if err := os.WriteFile(legacy, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, s := range Check(dir) {
		if s.Agent == "cursor (detected)" {
			if !s.Installed {
				t.Fatalf("legacy cursor install not accepted: %+v", s)
			}
			if s.Path != legacy {
				t.Fatalf("status must name the matched legacy path %s, got %s", legacy, s.Path)
			}
			return
		}
	}
	t.Fatal("cursor (detected) status missing from Check")
}

// TestWireMigratesLegacyCursorInstructions pins the migration: a legacy
// .cursor/instructions/kern.mdc that holds nothing but the kern-managed
// block is removed once the current .cursor/rules/kern.mdc is written; a
// file with user content is left in place.
func TestWireMigratesLegacyCursorInstructions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("PATH", "/nonexistent")

	t.Run("managed-only", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, ".cursor", "rules"), 0o755); err != nil {
			t.Fatal(err)
		}
		legacy := filepath.Join(dir, ".cursor", "instructions", "kern.mdc")
		if err := os.MkdirAll(filepath.Dir(legacy), 0o755); err != nil {
			t.Fatal(err)
		}
		body := instructionMarkerOpen + "\n\nold rules\n\n" + instructionMarkerClose + "\n"
		if err := os.WriteFile(legacy, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		Wire(dir, nil, true, false)
		if _, err := os.Stat(legacy); err == nil {
			t.Fatal("managed-only legacy file must be migrated away")
		}
		if _, err := os.Stat(filepath.Join(dir, ".cursor", "rules", "kern.mdc")); err != nil {
			t.Fatalf("current policy file missing: %v", err)
		}
	})

	t.Run("user-content-kept", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, ".cursor", "rules"), 0o755); err != nil {
			t.Fatal(err)
		}
		legacy := filepath.Join(dir, ".cursor", "instructions", "kern.mdc")
		if err := os.MkdirAll(filepath.Dir(legacy), 0o755); err != nil {
			t.Fatal(err)
		}
		body := "# my own notes\n\n" + instructionMarkerOpen + "\n\nold rules\n\n" + instructionMarkerClose + "\n"
		if err := os.WriteFile(legacy, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		Wire(dir, nil, true, false)
		b, err := os.ReadFile(legacy)
		if err != nil {
			t.Fatalf("legacy file with user content must be kept: %v", err)
		}
		if !strings.HasPrefix(string(b), "# my own notes") {
			t.Fatalf("user content damaged:\n%s", b)
		}
	})
}

// TestWireBareWiresPolicyForAgentsItEnabled pins the F10 root-cause fix:
// a bare setup that itself creates an agent's config marker (.cursor/rules,
// .vscode/mcp.json, .agents/) must also write that agent's kern-first
// policy in the SAME run — else doctor, which detects post-setup state,
// permanently warns about a half-wired agent.
func TestWireBareWiresPolicyForAgentsItEnabled(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("PATH", "/nonexistent")
	dir := t.TempDir()

	Wire(dir, nil, false, false)
	for _, rel := range []string{
		".cursor/rules/kern.mdc",          // cursor marker created by wireCursorRules
		".github/copilot-instructions.md", // copilot marker created by the .vscode/mcp.json adapter
		".agents/rules/kern.md",           // antigravity marker created by the universal skills pass
	} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("policy for self-enabled agent missing: %s: %v", rel, err)
		}
	}

	// After the wiring, Check must report every detected agent as wired —
	// no not-present instruction statuses.
	var missing []string
	for _, s := range Check(dir) {
		if strings.HasSuffix(s.Agent, " (detected)") && !s.Installed {
			missing = append(missing, s.Agent)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("detected agents still not wired after setup: %v", missing)
	}
}
