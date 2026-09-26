package diffgate

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
)

// fakeCatalog returns a small deterministic catalog for the drift checks.
// The diffgate package cannot import internal/mcp (mcp imports diffgate to
// register its provider), so tests supply ToolInfo data directly.
func fakeCatalog() []ToolInfo {
	return []ToolInfo{
		{Name: "kern_alpha", Phase: "meta", RiskLevel: "low", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"a": map[string]any{"type": "string"}}}},
		{Name: "kern_beta", Phase: "explore", RiskLevel: "low", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"b": map[string]any{"type": "string"}}}},
		{Name: "kern_gamma", Phase: "edit", RiskLevel: "high", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"c": map[string]any{"type": "string"}}}},
	}
}

// fakeCatalogNames returns the names of the fake catalog as a set.
func fakeCatalogNames() map[string]bool {
	out := make(map[string]bool)
	for _, t := range fakeCatalog() {
		out[t.Name] = true
	}
	return out
}

// pluginFromCatalog renders the fake catalog in the plugin's `kern_xxx:
// tool(` syntax.
func pluginFromCatalog(toolNames map[string]bool) string {
	var names []string
	for n := range toolNames {
		names = append(names, n)
	}
	sort.Strings(names)
	var sb strings.Builder
	for _, n := range names {
		sb.WriteString(n + ": tool({})\n")
	}
	return sb.String()
}

// changeRequest builds a minimal ChangeRequest for a temp-dir repo.
func changeRequest(root string, paths ...string) domain.ChangeRequest {
	files := make([]domain.FileChange, 0, len(paths))
	for _, p := range paths {
		files = append(files, domain.FileChange{Path: p, Op: domain.OpWrite})
	}
	return domain.ChangeRequest{RepositoryRoot: root, Source: domain.SourceHuman, Operation: domain.OpCommit, Files: files}
}

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	abs := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(abs), err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", abs, err)
	}
}

// ---------------------------------------------------------------------------
// G30 — format:gofmt
// ---------------------------------------------------------------------------

func TestG30_GofmtUnformattedFile(t *testing.T) {
	if _, err := exec.LookPath("gofmt"); err != nil {
		t.Skip("gofmt not installed")
	}
	dir := t.TempDir()
	writeFile(t, dir, "main.go", "package main\nfunc main(){\n}\n")
	chk := NewGofmtCheck()
	res, err := chk.Run(context.Background(), changeRequest(dir, "main.go"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != domain.StatusWarn {
		t.Fatalf("status = %s, want WARN", res.Status)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("findings = %d, want 1", len(res.Findings))
	}
	f := res.Findings[0]
	if f.RuleID != "format:gofmt" {
		t.Errorf("rule = %s, want format:gofmt", f.RuleID)
	}
	if f.File != "main.go" {
		t.Errorf("file = %q, want main.go", f.File)
	}
	if f.Severity != domain.SeverityWarn {
		t.Errorf("severity = %s, want warn (advisory)", f.Severity)
	}
}

func TestG30_GofmtCleanFile(t *testing.T) {
	if _, err := exec.LookPath("gofmt"); err != nil {
		t.Skip("gofmt not installed")
	}
	dir := t.TempDir()
	writeFile(t, dir, "clean.go", "package main\n\nfunc main() {}\n")
	chk := NewGofmtCheck()
	res, err := chk.Run(context.Background(), changeRequest(dir, "clean.go"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != domain.StatusPass {
		t.Fatalf("status = %s, want PASS", res.Status)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("findings = %d, want 0", len(res.Findings))
	}
}

// ---------------------------------------------------------------------------
// G31 — vulnerability:sec
// ---------------------------------------------------------------------------

func TestG31_SecScanWeakCryptoWarns(t *testing.T) {
	dir := t.TempDir()
	// weak-crypto rule (severity warning) fires on md5.New.
	writeFile(t, dir, "weak.go", "package main\nimport \"crypto/md5\"\nfunc main() { _ = md5.New() }\n")
	chk := NewVulnCheck()
	res, err := chk.Run(context.Background(), changeRequest(dir, "weak.go"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != domain.StatusWarn {
		t.Fatalf("status = %s, want WARN", res.Status)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("findings = %d, want 1", len(res.Findings))
	}
	f := res.Findings[0]
	if !strings.HasPrefix(f.RuleID, "vulnerability:") {
		t.Errorf("rule = %s, want vulnerability:* prefix", f.RuleID)
	}
	if f.Severity != domain.SeverityWarn {
		t.Errorf("severity = %s, want warn (warning mapped to WARN)", f.Severity)
	}
	if f.File != "weak.go" {
		t.Errorf("file = %q, want weak.go", f.File)
	}
}

func TestG31_SecScanInjectionBlocks(t *testing.T) {
	dir := t.TempDir()
	// sql-injection rule (severity error) fires on fmt.Sprintf inside Query.
	writeFile(t, dir, "db.go", "package main\nimport (\"database/sql\"; \"fmt\")\nfunc q(db *sql.DB, id string) { rows, _ := db.Query(fmt.Sprintf(\"SELECT * FROM users WHERE id=%s\", id)); _ = rows }\n")
	chk := NewVulnCheck()
	res, err := chk.Run(context.Background(), changeRequest(dir, "db.go"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != domain.StatusBlock {
		t.Fatalf("status = %s, want BLOCK (error severity maps to BLOCK)", res.Status)
	}
	found := false
	for _, f := range res.Findings {
		if f.RuleID == "vulnerability:sql-injection" && f.Severity == domain.SeverityBlock {
			found = true
		}
	}
	if !found {
		t.Errorf("no BLOCK-severity sql-injection finding in %+v", res.Findings)
	}
}

// ---------------------------------------------------------------------------
// G32 — schema:drift
// ---------------------------------------------------------------------------

func TestG32_SchemaBaselineRoundTrip(t *testing.T) {
	SetToolInfos(fakeCatalog())
	dir := t.TempDir()
	root := filepath.Join(dir, "repo")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// init-baseline run: writes the baseline and reports PASS.
	chk := NewSchemaDriftCheck(root, true, ToolInfos())
	res, err := chk.Run(context.Background(), changeRequest(root))
	if err != nil {
		t.Fatalf("Run(init): %v", err)
	}
	if res.Status != domain.StatusPass {
		t.Fatalf("init status = %s, want PASS", res.Status)
	}
	if len(res.Findings) != 1 || res.Findings[0].Message == "" {
		t.Fatalf("init findings: want one informational 'baseline initialized' finding, got %+v", res.Findings)
	}
	if !strings.Contains(res.Findings[0].Message, "baseline initialized") {
		t.Errorf("message = %q, want 'baseline initialized'", res.Findings[0].Message)
	}

	baselinePath := filepath.Join(root, "docs", "mcp", "tool-schemas.json")
	data, err := os.ReadFile(baselinePath)
	if err != nil {
		t.Fatalf("baseline file: %v", err)
	}
	var bl schemaBaseline
	if err := json.Unmarshal(data, &bl); err != nil {
		t.Fatalf("baseline parse: %v", err)
	}
	if bl.SchemaVersion != schemaBaselineVersion {
		t.Errorf("schema version = %d, want %d", bl.SchemaVersion, schemaBaselineVersion)
	}
	if len(bl.Tools) != len(fakeCatalog()) {
		t.Errorf("baseline tools = %d, catalog = %d", len(bl.Tools), len(fakeCatalog()))
	}
	for i := 1; i < len(bl.Tools); i++ {
		if bl.Tools[i-1].Name >= bl.Tools[i].Name {
			t.Errorf("baseline tools not sorted by name at %d: %q >= %q", i, bl.Tools[i-1].Name, bl.Tools[i].Name)
		}
	}

	// Re-run without init on the unchanged catalog: PASS, no drift.
	chk2 := NewSchemaDriftCheck(root, false, ToolInfos())
	res2, err := chk2.Run(context.Background(), changeRequest(root))
	if err != nil {
		t.Fatalf("Run(recheck): %v", err)
	}
	if res2.Status != domain.StatusPass {
		t.Fatalf("recheck status = %s, want PASS (no drift)", res2.Status)
	}
	if len(res2.Findings) != 0 {
		t.Fatalf("recheck findings = %d, want 0", len(res2.Findings))
	}
}

func TestG32_SchemaDriftDetected(t *testing.T) {
	SetToolInfos(fakeCatalog())
	dir := t.TempDir()
	root := filepath.Join(dir, "repo")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Fabricate a stale baseline: one real tool with a tampered fingerprint,
	// one extra tool that no longer exists in the catalog.
	entries := fingerprintCatalog(fakeCatalog())
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	if len(entries) == 0 {
		t.Fatal("empty catalog")
	}
	entries[0].SHA256 = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	fake := append([]toolSchemaEntry(nil), entries...)
	fake = append(fake, toolSchemaEntry{Name: "kern_fake_tool", Phase: "meta", Risk: "low", SHA256: strings.Repeat("ab", 32)})
	sort.Slice(fake, func(i, j int) bool { return fake[i].Name < fake[j].Name })
	if err := writeSchemaBaseline(filepath.Join(root, "docs", "mcp", "tool-schemas.json"), fake); err != nil {
		t.Fatalf("write stale baseline: %v", err)
	}

	chk := NewSchemaDriftCheck(root, false, ToolInfos())
	res, err := chk.Run(context.Background(), changeRequest(root))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != domain.StatusWarn {
		t.Fatalf("status = %s, want WARN on drift", res.Status)
	}
	gotChanged, gotRemoved := false, false
	for _, f := range res.Findings {
		switch {
		case strings.Contains(f.Message, entries[0].Name) && strings.Contains(f.Message, "changed"):
			gotChanged = true
		case strings.Contains(f.Message, "kern_fake_tool") && strings.Contains(f.Message, "removed"):
			gotRemoved = true
		}
	}
	if !gotChanged {
		t.Errorf("no 'changed' drift finding for %s (findings: %+v)", entries[0].Name, res.Findings)
	}
	if !gotRemoved {
		t.Errorf("no 'removed' drift finding for kern_fake_tool (findings: %+v)", res.Findings)
	}
	for _, f := range res.Findings {
		if f.Severity != domain.SeverityWarn {
			t.Errorf("drift finding severity = %s, want warn (advisory)", f.Severity)
		}
	}
}

func TestG32_SchemaBaselineMissingPromptsInit(t *testing.T) {
	SetToolInfos(fakeCatalog())
	dir := t.TempDir()
	chk := NewSchemaDriftCheck(dir, false, ToolInfos())
	res, err := chk.Run(context.Background(), changeRequest(dir))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != domain.StatusWarn {
		t.Fatalf("status = %s, want WARN (baseline missing)", res.Status)
	}
	if len(res.Findings) != 1 || !strings.Contains(res.Findings[0].Message, "--init-baseline") {
		t.Errorf("want a finding prompting --init-baseline, got %+v", res.Findings)
	}
}

// ---------------------------------------------------------------------------
// G33 — exec:unsafe
// ---------------------------------------------------------------------------

func TestG33_ExecUnsafeDetected(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "exec.go", "package main\nimport \"os/exec\"\nfunc f() { exec.Command(\"sh\", \"-c\", \"ls\") }\n")
	chk := NewExecUnsafeCheck()
	res, err := chk.Run(context.Background(), changeRequest(dir, "exec.go"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != domain.StatusWarn {
		t.Fatalf("status = %s, want WARN", res.Status)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("findings = %d, want 1", len(res.Findings))
	}
	f := res.Findings[0]
	if f.RuleID != "exec:unsafe" {
		t.Errorf("rule = %s, want exec:unsafe", f.RuleID)
	}
	if f.File != "exec.go" {
		t.Errorf("file = %q, want exec.go", f.File)
	}
	if f.Severity != domain.SeverityWarn {
		t.Errorf("severity = %s, want warn (advisory)", f.Severity)
	}
}

func TestG33_TestFileNotFlagged(t *testing.T) {
	dir := t.TempDir()
	// A _test.go file using os/exec is NOT flagged: the gate targets
	// production code paths.
	writeFile(t, dir, "exec_test.go", "package main\nimport \"os/exec\"\nfunc f() { _ = exec.Command(\"ls\") }\n")
	chk := NewExecUnsafeCheck()
	res, err := chk.Run(context.Background(), changeRequest(dir, "exec_test.go"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != domain.StatusPass {
		t.Fatalf("status = %s, want PASS (test files excluded)", res.Status)
	}
}

// ---------------------------------------------------------------------------
// G34 — changelog:missing
// ---------------------------------------------------------------------------

func TestG34_ChangelogMissing(t *testing.T) {
	chk := NewChangelogCheck()
	res, err := chk.Run(context.Background(), changeRequest(t.TempDir(), "internal/app/main.go", "docs/guide.md"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != domain.StatusWarn {
		t.Fatalf("status = %s, want WARN", res.Status)
	}
	if len(res.Findings) != 1 || res.Findings[0].RuleID != "changelog:missing" {
		t.Fatalf("findings = %+v, want one changelog:missing finding", res.Findings)
	}
}

func TestG34_ChangelogPresent(t *testing.T) {
	chk := NewChangelogCheck()
	res, err := chk.Run(context.Background(), changeRequest(t.TempDir(), "internal/app/main.go", "CHANGELOG.md"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != domain.StatusPass {
		t.Fatalf("status = %s, want PASS (CHANGELOG.md in changed set)", res.Status)
	}
}

func TestG34_DocOnlyChangePasses(t *testing.T) {
	chk := NewChangelogCheck()
	// Only docs/config changes: no source change, so no changelog required.
	res, err := chk.Run(context.Background(), changeRequest(t.TempDir(), "docs/guide.md", "config.json", "README.md"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != domain.StatusPass {
		t.Fatalf("status = %s, want PASS (doc-only change)", res.Status)
	}
}

// ---------------------------------------------------------------------------
// G35 — catalog:drift (unit level; the real-repo test lives in
// internal/mcp/catalog_drift_test.go where the live catalog is available)
// ---------------------------------------------------------------------------

func TestG35_CatalogDriftMismatch(t *testing.T) {
	SetToolInfos(fakeCatalog())
	names := fakeCatalogNames()

	// A fake plugin content missing one real tool must produce BLOCK findings.
	first := "kern_alpha"
	rest := make(map[string]bool)
	for n := range names {
		if n != first {
			rest[n] = true
		}
	}
	findings := compareToolSets(names, pluginFromCatalog(rest))
	if len(findings) == 0 {
		t.Fatalf("no findings for a plugin missing %s", first)
	}
	found := false
	for _, f := range findings {
		if f.Severity != domain.SeverityBlock {
			t.Errorf("finding severity = %s, want block", f.Severity)
		}
		if strings.Contains(f.Message, first) {
			found = true
		}
	}
	if !found {
		t.Errorf("no BLOCK finding naming %s (findings: %+v)", first, findings)
	}

	// A plugin exposing an unknown extra tool must also be flagged.
	extra := pluginFromCatalog(names) + "kern_ghost_tool: tool({})\n"
	findings2 := compareToolSets(names, extra)
	foundExtra := false
	for _, f := range findings2 {
		if strings.Contains(f.Message, "kern_ghost_tool") {
			foundExtra = true
		}
	}
	if !foundExtra {
		t.Errorf("no finding for extra plugin tool kern_ghost_tool (findings: %+v)", findings2)
	}
}

func TestG35_CatalogDriftExactMatch(t *testing.T) {
	SetToolInfos(fakeCatalog())
	names := fakeCatalogNames()
	if findings := compareToolSets(names, pluginFromCatalog(names)); len(findings) != 0 {
		t.Fatalf("exact match produced findings: %+v", findings)
	}
}

func TestG35_CatalogDriftPhaseMismatch(t *testing.T) {
	SetToolInfos([]ToolInfo{
		{Name: "kern_alpha", Phase: "explore", RiskLevel: "low"},
		{Name: "kern_beta", Phase: "explore", RiskLevel: "low"},
	})
	catalog := ToolInfos()
	// The plugin declares kern_alpha as "edit" while the catalog says
	// "explore": the plugin would advertise the wrong surface for
	// KERN_MCP_PHASE=explore, so the drift gate must BLOCK.
	content := `const TOOL_PHASES: Record<string, string> = {
  kern_alpha: "edit",
  kern_beta: "explore",
}`
	findings := compareToolPhases(catalog, content)
	if len(findings) != 1 {
		t.Fatalf("findings = %d, want 1 (phase mismatch on kern_alpha); findings: %+v", len(findings), findings)
	}
	f := findings[0]
	if f.Severity != domain.SeverityBlock {
		t.Errorf("severity = %s, want block", f.Severity)
	}
	if !strings.Contains(f.Message, "phase mismatch") ||
		!strings.Contains(f.Message, "catalog=explore") ||
		!strings.Contains(f.Message, "plugin=edit") {
		t.Errorf("message = %q, want phase mismatch with catalog=explore plugin=edit", f.Message)
	}
}

func TestG35_CatalogDriftPhasesAgree(t *testing.T) {
	SetToolInfos([]ToolInfo{
		{Name: "kern_alpha", Phase: "explore", RiskLevel: "low"},
		{Name: "kern_beta", Phase: "edit", RiskLevel: "low"},
	})
	catalog := ToolInfos()
	// Matching phases pass; a plugin-only tool (kern_gamma) is ignored for
	// phase purposes (name drift is compareToolSets' job).
	content := `const TOOL_PHASES: Record<string, string> = {
  kern_alpha: "explore",
  kern_beta: "edit",
  kern_gamma: "cross",
}`
	if findings := compareToolPhases(catalog, content); len(findings) != 0 {
		t.Fatalf("agreeing phases produced findings: %+v", findings)
	}
}
