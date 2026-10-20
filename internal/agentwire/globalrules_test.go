package agentwire

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/version"
)

// testEnv redirects every user-global path under a temp HOME (and a temp
// XDG_CONFIG_HOME for the opencode global path) so the real ~/.claude,
// ~/.codex and ~/.config/opencode are NEVER touched by these tests.
func testEnv(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	return home
}

// TestGlobalRulesPaths pins the six host global instruction slots.
func TestGlobalRulesPaths(t *testing.T) {
	home := testEnv(t)
	paths := GlobalRulesPaths()
	want := []string{
		filepath.Join(home, ".claude", "CLAUDE.md"),
		filepath.Join(home, ".codex", "AGENTS.md"),
		filepath.Join(home, ".config", "opencode", "AGENTS.md"),
		filepath.Join(home, ".cursor", "rules", "kern.mdc"),
		filepath.Join(home, ".gemini", "GEMINI.md"),
		filepath.Join(home, ".codeium", "windsurf", "memories", "global_rules.md"),
		filepath.Join(home, ".github", "copilot-instructions.md"),
	}
	if len(paths) != len(want) {
		t.Fatalf("GlobalRulesPaths() = %d entries, want %d", len(paths), len(want))
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Errorf("path[%d] = %q, want %q", i, paths[i], want[i])
		}
	}
}

// TestWireGlobalRulesCreatesMissing verifies missing global instruction files
// are created with the canonical marker-delimited block (plus the Cursor
// frontmatter for .mdc paths).
func TestWireGlobalRulesCreatesMissing(t *testing.T) {
	testEnv(t)
	sts := WireGlobalRules()
	if len(sts) != 7 {
		t.Fatalf("WireGlobalRules() = %d statuses, want 7", len(sts))
	}
	want, _ := CondenseGlobalRules()
	stampedWant := InsertManagedStamp(string(want))
	for _, p := range GlobalRulesPaths() {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("global rules not written for %s: %v", p, err)
		}
		content := string(b)
		if !strings.Contains(content, GlobalRulesMarkerOpen) || !strings.Contains(content, GlobalRulesMarkerClose) {
			t.Fatalf("%s missing markers", p)
		}
		if !strings.Contains(content, StampKernVersion()) {
			t.Fatalf("%s missing the managed version stamp", p)
		}
		if strings.HasSuffix(p, ".mdc") {
			if !strings.HasPrefix(content, CursorFrontmatter+"\n\n") {
				t.Fatalf("%s missing cursor frontmatter on line 1:\n%s", p, content)
			}
		} else if content != stampedWant {
			t.Fatalf("%s content differs from canonical block", p)
		}
	}
	for _, s := range sts {
		if !s.Installed {
			t.Errorf("status not installed: %+v", s)
		}
	}
}

// TestWireGlobalRulesPreservesUserContent verifies content outside the
// markers survives a re-run verbatim on every host path.
func TestWireGlobalRulesPreservesUserContent(t *testing.T) {
	testEnv(t)
	WireGlobalRules()
	userPrefs := "# my personal notes\n\nalways use python3 for scripts\n"
	for _, p := range GlobalRulesPaths() {
		orig, _ := os.ReadFile(p)
		if err := os.WriteFile(p, []byte(userPrefs+string(orig)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	WireGlobalRules()
	for _, p := range GlobalRulesPaths() {
		b, _ := os.ReadFile(p)
		content := string(b)
		if !strings.HasPrefix(content, userPrefs) {
			t.Fatalf("%s: user content outside markers not preserved verbatim:\n%s", p, content)
		}
		if strings.Count(content, GlobalRulesMarkerOpen) != 1 {
			t.Fatalf("%s: kern block duplicated:\n%s", p, content)
		}
	}
}

// TestWireGlobalRulesIdempotent verifies a re-run with nothing else changed
// produces identical bytes.
func TestWireGlobalRulesIdempotent(t *testing.T) {
	testEnv(t)
	WireGlobalRules()
	first := map[string]string{}
	for _, p := range GlobalRulesPaths() {
		b, _ := os.ReadFile(p)
		first[p] = string(b)
	}
	WireGlobalRules()
	for _, p := range GlobalRulesPaths() {
		b, _ := os.ReadFile(p)
		if string(b) != first[p] {
			t.Fatalf("%s changed on re-run", p)
		}
	}
}

// TestWireGlobalRulesReplacesStaleBlock verifies a stale kern-managed block
// is replaced in place while content outside the markers survives.
func TestWireGlobalRulesReplacesStaleBlock(t *testing.T) {
	testEnv(t)
	p := GlobalRulesPaths()[0]
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	stale := GlobalRulesMarkerOpen + "\nOLD STALE RULES\n" + GlobalRulesMarkerClose + "\n"
	if err := os.WriteFile(p, []byte("keep me\n\n"+stale), 0o644); err != nil {
		t.Fatal(err)
	}
	WireGlobalRules()
	b, _ := os.ReadFile(p)
	content := string(b)
	if strings.Contains(content, "OLD STALE RULES") {
		t.Fatalf("stale block not replaced:\n%s", content)
	}
	if !strings.HasPrefix(content, "keep me\n") {
		t.Fatalf("content outside markers lost:\n%s", content)
	}
	want, _ := CondenseGlobalRules()
	if !strings.Contains(content, InsertManagedStamp(string(want))) {
		t.Fatalf("fresh canonical block missing:\n%s", content)
	}
}

// TestWireGlobalRulesSkipsNewerStamp (C7): a managed block stamped with a
// NEWER kern version than the running binary is left untouched — the
// stale-release-binary clobber path (install.sh post-install running the
// RELEASE binary's setup over newer local wiring) must be a no-op.
func TestWireGlobalRulesSkipsNewerStamp(t *testing.T) {
	testEnv(t)
	// The tests run with Version "dev" (Local, uncomparable); pin the running
	// binary to a release so the release-vs-release ordering is exercised.
	orig := version.Version
	version.Version = "1.0.0"
	t.Cleanup(func() { version.Version = orig })
	p := GlobalRulesPaths()[0]
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	// A strictly newer release stamp is skipped.
	newer := GlobalRulesMarkerOpen + "\n<!-- kern-version: 9.9.9 -->\nPRECIOUS NEWER RULES\n" + GlobalRulesMarkerClose + "\n"
	if err := os.WriteFile(p, []byte("user header\n\n"+newer), 0o644); err != nil {
		t.Fatal(err)
	}
	sts := WireGlobalRules()
	var s Status
	for _, st := range sts {
		if st.Path == p {
			s = st
		}
	}
	if !s.Installed || !strings.Contains(s.Note, "newer") {
		t.Fatalf("expected the newer-stamped path to be skipped with a note, got %+v", s)
	}
	b, _ := os.ReadFile(p)
	if strings.Contains(string(b), "kern usage rules for agents") {
		t.Fatalf("newer-stamped block was rewritten:\n%s", b)
	}
	if !strings.Contains(string(b), "PRECIOUS NEWER RULES") {
		t.Fatalf("newer-stamped content lost:\n%s", b)
	}
	// A LOCAL stamp (dev / git hash) is also skipped by a release binary —
	// the install.sh footgun: local wiring is always newer than a release.
	local := GlobalRulesMarkerOpen + "\n<!-- kern-version: dev -->\nPRECIOUS LOCAL RULES\n" + GlobalRulesMarkerClose + "\n"
	if err := os.WriteFile(p, []byte("user header\n\n"+local), 0o644); err != nil {
		t.Fatal(err)
	}
	WireGlobalRules()
	b, _ = os.ReadFile(p)
	if !strings.Contains(string(b), "PRECIOUS LOCAL RULES") {
		t.Fatalf("locally-stamped content lost to a release binary:\n%s", b)
	}
	// An OLDER stamp is rewritten normally (upgrade path).
	older := GlobalRulesMarkerOpen + "\n<!-- kern-version: 0.0.1 -->\nOLD RULES\n" + GlobalRulesMarkerClose + "\n"
	if err := os.WriteFile(p, []byte("user header\n\n"+older), 0o644); err != nil {
		t.Fatal(err)
	}
	WireGlobalRules()
	b, _ = os.ReadFile(p)
	if strings.Contains(string(b), "OLD RULES") {
		t.Fatalf("older-stamped block not rewritten:\n%s", b)
	}
}

// TestWireGlobalRulesAppendsWithoutMarkers verifies a file with no markers
// gets the block appended behind a blank separator, user content intact.
func TestWireGlobalRulesAppendsWithoutMarkers(t *testing.T) {
	testEnv(t)
	p := GlobalRulesPaths()[1]
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("user rules without markers\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	WireGlobalRules()
	b, _ := os.ReadFile(p)
	content := string(b)
	if !strings.HasPrefix(content, "user rules without markers\n\n") {
		t.Fatalf("user content + blank separator missing:\n%s", content)
	}
	if !strings.Contains(content, GlobalRulesMarkerOpen) {
		t.Fatalf("kern block not appended:\n%s", content)
	}
}

// TestCursorMDCFrontmatter verifies the Cursor .mdc rule file carries valid
// YAML frontmatter with alwaysApply: true on line 1 — outside the managed
// marker block, with a blank line separating them — and that a re-run is
// byte-for-byte idempotent.
func TestCursorMDCFrontmatter(t *testing.T) {
	testEnv(t)
	WireGlobalRules()
	p := ""
	for _, path := range GlobalRulesPaths() {
		if strings.HasSuffix(path, ".mdc") {
			p = path
			break
		}
	}
	if p == "" {
		t.Fatal("no .mdc path in GlobalRulesPaths()")
	}
	b, _ := os.ReadFile(p)
	content := string(b)
	if !strings.HasPrefix(content, CursorFrontmatter) {
		t.Fatalf("frontmatter not on line 1:\n%s", content)
	}
	if !strings.Contains(content, "alwaysApply: true") {
		t.Fatalf("frontmatter missing alwaysApply: true:\n%s", content)
	}
	marker := strings.Index(content, GlobalRulesMarkerOpen)
	if marker <= len(CursorFrontmatter) {
		t.Fatalf("managed block not after frontmatter:\n%s", content)
	}
	if content[marker-2:marker] != "\n\n" {
		t.Fatalf("no blank line between frontmatter and managed block:\n%q", content[marker-4:marker])
	}
	want, _ := CondenseGlobalRules()
	if !strings.Contains(content, InsertManagedStamp(string(want))) {
		t.Fatalf("canonical block missing after frontmatter:\n%s", content)
	}
	// Idempotent across two runs.
	WireGlobalRules()
	b2, _ := os.ReadFile(p)
	if string(b) != string(b2) {
		t.Fatalf("cursor .mdc re-run not byte-identical:\n--- first ---\n%s--- second ---\n%s", b, b2)
	}
}

// TestCursorMDCPreservesFrontmatterOnRewire verifies that when the managed
// block is stale and replaced, the existing frontmatter is kept in place —
// not duplicated — and stays on line 1.
func TestCursorMDCPreservesFrontmatterOnRewire(t *testing.T) {
	testEnv(t)
	p := ""
	for _, path := range GlobalRulesPaths() {
		if strings.HasSuffix(path, ".mdc") {
			p = path
			break
		}
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	stale := GlobalRulesMarkerOpen + "\nOLD STALE RULES\n" + GlobalRulesMarkerClose + "\n"
	if err := os.WriteFile(p, []byte(CursorFrontmatter+"\n\n"+stale), 0o644); err != nil {
		t.Fatal(err)
	}
	WireGlobalRules()
	b, _ := os.ReadFile(p)
	content := string(b)
	if strings.Contains(content, "OLD STALE RULES") {
		t.Fatalf("stale block not replaced:\n%s", content)
	}
	if !strings.HasPrefix(content, CursorFrontmatter+"\n\n") {
		t.Fatalf("frontmatter lost or displaced:\n%s", content)
	}
	if strings.Count(content, CursorFrontmatter) != 1 {
		t.Fatalf("frontmatter duplicated:\n%s", content)
	}
}

// TestGeminiGlobalPath pins the Gemini CLI global instructions slot to
// ~/.gemini/GEMINI.md — regression against the wrong ~/.gemini/config/
// variant claimed by ADR-0011.
func TestGeminiGlobalPath(t *testing.T) {
	home := testEnv(t)
	gemini := ""
	for _, p := range GlobalRulesPaths() {
		if strings.Contains(p, ".gemini") {
			gemini = p
		}
	}
	want := filepath.Join(home, ".gemini", "GEMINI.md")
	if gemini != want {
		t.Fatalf("Gemini global path = %q, want %q", gemini, want)
	}
}

// TestWindsurfGlobalPath pins the Windsurf global memories file to
// ~/.codeium/windsurf/memories/global_rules.md — regression against the
// legacy ~/.windsurf/rules/ workspace location.
func TestWindsurfGlobalPath(t *testing.T) {
	home := testEnv(t)
	windsurf := ""
	for _, p := range GlobalRulesPaths() {
		if strings.Contains(p, "windsurf") {
			windsurf = p
		}
	}
	want := filepath.Join(home, ".codeium", "windsurf", "memories", "global_rules.md")
	if windsurf != want {
		t.Fatalf("Windsurf global path = %q, want %q", windsurf, want)
	}
}

// TestManagedBlockUnderWindsurfCap guards the Windsurf 6000-char cap on
// global_rules.md: the managed block must fit inside it, else Windsurf would
// truncate the file — and with it the kern markers, breaking later re-runs.
func TestManagedBlockUnderWindsurfCap(t *testing.T) {
	b, err := CondenseGlobalRules()
	if err != nil {
		t.Fatal(err)
	}
	if n := len(b); n >= 6000 {
		t.Fatalf("managed block is %d bytes, exceeds the Windsurf 6000-char cap", n)
	}
}

// TestGlobalRulesDerivedFromFull pins the L1 derivation contract: the
// condensed global block is derived deterministically from the single
// canonical rules file (assets/AGENTS.md), carries the global-rules markers,
// leaks no omit-marker text, stays under the Windsurf 6000-char cap, and the
// source markers are balanced.
func TestGlobalRulesDerivedFromFull(t *testing.T) {
	b, err := CondenseGlobalRules()
	if err != nil {
		t.Fatalf("CondenseGlobalRules() failed: %v", err)
	}
	// Determinism: a second call yields identical bytes.
	b2, err := CondenseGlobalRules()
	if err != nil {
		t.Fatalf("CondenseGlobalRules() (second call) failed: %v", err)
	}
	if string(b) != string(b2) {
		t.Fatal("CondenseGlobalRules() not deterministic")
	}
	// Wrapped in the global-rules markers.
	if !strings.HasPrefix(string(b), GlobalRulesMarkerOpen) {
		t.Fatalf("derived block does not start with GlobalRulesMarkerOpen:\n%s", b)
	}
	if !strings.HasSuffix(string(b), GlobalRulesMarkerClose+"\n") {
		t.Fatalf("derived block does not end with GlobalRulesMarkerClose")
	}
	// No omit-marker text survives the strip.
	if strings.Contains(string(b), "kern:global-omit") {
		t.Fatal("derived block contains kern:global-omit marker text")
	}
	// Windsurf cap: the derived block must fit the 6000-char whole-file cap.
	if n := len(b); n >= 6000 {
		t.Fatalf("derived block is %d bytes, exceeds the Windsurf 6000-char cap", n)
	}
	// Source marker balance: every begin marker has a matching end marker.
	content, err := RulesFS().ReadFile("assets/AGENTS.md")
	if err != nil {
		t.Fatalf("read assets/AGENTS.md: %v", err)
	}
	if strings.Count(string(content), GlobalOmitBegin) != strings.Count(string(content), GlobalOmitEnd) {
		t.Fatal("unbalanced global-omit markers in assets/AGENTS.md")
	}
}

// TestGlobalSlotCarriesCanonicalRules pins the L5-8 fix: marker presence
// alone must not count as a managed slot — the block between the markers
// must match the canonical condensed rules (modulo the version stamp), or
// the repo AGENTS.md must fall back to the full variant so rules still load
// somewhere.
func TestGlobalSlotCarriesCanonicalRules(t *testing.T) {
	canonical, err := CondenseGlobalRules()
	if err != nil {
		t.Fatalf("CondenseGlobalRules: %v", err)
	}
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}

	// Intact slot, exactly as WireGlobalRulesFile writes it (canonical + the
	// running binary's stamp inserted after the open marker).
	intact := InsertManagedStamp(string(canonical))
	if !GlobalSlotCarriesCanonicalRules(write("intact.md", intact)) {
		t.Error("intact canonical slot (with version stamp) must count as managed")
	}

	// Same rules content, older writer's stamp — still managed (the check
	// compares rules content, not the writing binary).
	older := KernVersionStampRe.ReplaceAllString(intact, "<!-- kern-version: 0.0.1 -->")
	if older == intact {
		t.Fatal("test setup: stamp replacement was a no-op")
	}
	if !GlobalSlotCarriesCanonicalRules(write("older.md", older)) {
		t.Error("slot with a different version stamp but identical rules content must count as managed")
	}

	// Gutted-but-marked slot (L5-8's silent failure): markers and stamp
	// survive, rules content deleted — must NOT count.
	gutted := GlobalRulesMarkerOpen + "\n<!-- kern-version: 0.0.1 -->\n" + GlobalRulesMarkerClose + "\n"
	if GlobalSlotCarriesCanonicalRules(write("gutted.md", gutted)) {
		t.Error("gutted-but-marked slot must NOT count as managed (repo file must fall back to full)")
	}

	// Truncated rules body: must NOT count.
	trunc := GlobalRulesMarkerOpen + "\n" + string(canonical)[len(GlobalRulesMarkerOpen)+1:len(GlobalRulesMarkerOpen)+200] + "\n" + GlobalRulesMarkerClose + "\n"
	if GlobalSlotCarriesCanonicalRules(write("trunc.md", trunc)) {
		t.Error("truncated rules block must NOT count as managed")
	}

	// No markers at all: must NOT count.
	if GlobalSlotCarriesCanonicalRules(write("plain.md", "# user notes\n")) {
		t.Error("slot without markers must not count as managed")
	}
}
