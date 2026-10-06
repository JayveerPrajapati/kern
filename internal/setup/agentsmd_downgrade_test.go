package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fullRulesFixture is a minimal stand-in for the full AGENTS.md variant: it
// must start with the exact full header (the downgrade-detection marker) and
// carry a marker-managed block like the real generated file.
func fullRulesFixture() string {
	return instructionMarkerOpen + "\n" +
		fullRulesHeader + "\n" +
		"full usage rules body — kern_meta routing, kern-first policy, tool list\n" +
		instructionMarkerClose + "\n"
}

func writeFixture(t *testing.T, root, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestThinRulesRefuseFullDowngradeByDefault pins the watcher-bug fix: with
// NO explicit thin choice (no --agents-md, no persisted .kern/config.json
// key), a repo whose AGENTS.md already carries the FULL rules must keep
// them — the absent-key thin default must not silently delete them. This is
// the exact mechanism that repeatedly clobbered the kern repo's tracked
// AGENTS.md down to the 6-line stub.
func TestThinRulesRefuseFullDowngradeByDefault(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "AGENTS.md", fullRulesFixture())

	status := wireThinRulesFile(root, "AGENTS.md", "all", false)

	if !strings.Contains(status.Note, "kept existing full rules") {
		t.Fatalf("expected downgrade refusal, got status %+v", status)
	}
	after, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != fullRulesFixture() {
		t.Fatalf("file must be untouched, got:\n%s", after)
	}
}

// TestThinRulesDowngradeWhenExplicit pins that an explicit choice (the
// --agents-md thin flag, or a previously persisted thin preference in
// .kern/config.json — the recorded user decision) DOES switch the file to
// the thin variant.
func TestThinRulesDowngradeWhenExplicit(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "AGENTS.md", fullRulesFixture())

	status := wireThinRulesFile(root, "AGENTS.md", "all", true)

	if !strings.Contains(status.Note, "thin rules written") {
		t.Fatalf("expected thin write, got status %+v", status)
	}
	after, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(after), fullRulesHeader) {
		t.Fatalf("full header must be gone after an explicit downgrade, got:\n%s", after)
	}
	if !strings.Contains(string(after), thinAGENTSmd("all")) {
		t.Fatalf("thin variant must be present after an explicit downgrade, got:\n%s", after)
	}
}

// TestThinRulesWrittenWhenNoFullFile pins that fresh repos (no AGENTS.md or
// only a thin one) still get the thin default — the guard only protects an
// existing FULL rules file.
func TestThinRulesWrittenWhenNoFullFile(t *testing.T) {
	root := t.TempDir()

	status := wireThinRulesFile(root, "AGENTS.md", "all", false)

	if !strings.Contains(status.Note, "thin rules written") {
		t.Fatalf("expected thin write on fresh repo, got status %+v", status)
	}
}

// TestAgentsMDPersistedKeyDetection pins the explicitness probe: the
// agents_md key present in .kern/config.json (either variant) counts as a
// recorded user decision; a missing file or missing key does not.
func TestAgentsMDPersistedKeyDetection(t *testing.T) {
	root := t.TempDir()
	if agentsMDPersisted(root) {
		t.Fatal("no .kern/config.json: not persisted")
	}
	if err := os.MkdirAll(filepath.Join(root, ".kern"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".kern", "config.json"),
		[]byte(`{"other": true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if agentsMDPersisted(root) {
		t.Fatal("agents_md key absent: not persisted")
	}
	if err := os.WriteFile(filepath.Join(root, ".kern", "config.json"),
		[]byte(`{"agents_md": "thin"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if !agentsMDPersisted(root) {
		t.Fatal("agents_md key present: persisted")
	}
}

// TestThinAGENTSmdCarriesEssentialRouting pins the thin-variant adequacy
// fix: the thin repo AGENTS.md (what non-Claude agents read) must carry the
// critical kern-first routing facts — the decision-table essentials
// (read→kern_explore, grep→kern_search, build/test→kern_verify,
// unsure→kern_meta), the session-start kern_buddy call, and the default
// 6-tool surface note — while staying thin by design.
func TestThinAGENTSmdCarriesEssentialRouting(t *testing.T) {
	thin := thinAGENTSmd("all")
	for _, want := range []string{
		"kern_meta",    // unsure → kern_meta
		"kern_explore", // read → kern_explore
		"kern_search",  // grep → kern_search
		"kern_verify",  // build/test → kern_verify
		"kern_buddy",   // session-start onboarding call
		"KERN_MCP_FULL=1",
		"read",
		"grep",
		"build/test",
	} {
		if !strings.Contains(thin, want) {
			t.Errorf("thin AGENTS.md missing essential routing fact %q:\n%s", want, thin)
		}
	}
	// Still thin: the thin variant must stay far below the full template
	// (13KB+); thin-by-design is the point of the variant.
	if len(thin) > 1024 {
		t.Errorf("thin AGENTS.md is %d bytes, want <= 1024 (thin by design)", len(thin))
	}
}
