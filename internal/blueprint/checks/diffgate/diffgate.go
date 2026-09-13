// Package diffgate implements the deterministic local diff-gate checks
// (KERN-P2-003). Each check is a service.Check that runs against the
// working-tree diff (domain.ChangeRequest.Files) and returns structured
// verdicts. Every check is deterministic and stdlib-only: no network, no
// external services, no LLM. The six checks here are registered as gates
// G30-G35 in internal/blueprint/gates/registry.go.
package diffgate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
	"github.com/JayveerPrajapati/kern/internal/sec"
)

// ---------------------------------------------------------------------------
// MCP catalog provider (dependency inversion)
// ---------------------------------------------------------------------------
//
// The drift checks (schema:drift, catalog:drift) need the live MCP tool
// catalog. internal/blueprint/cli cannot import internal/mcp (mcp
// transitively imports cli via enterprise → web → service, so cli → mcp is
// an import cycle). Instead the checks consume the catalog through a
// provider: internal/mcp registers it in an init() (see
// internal/mcp/catalog_provider.go), so every binary that links mcp — the
// kern binary and its tests — automatically supplies the live catalog. When
// the provider is unset, the drift checks SKIP (they cannot compare).

// ToolInfo is the minimal, neutral contract surface the drift checks read
// from the MCP catalog: name, phase, risk level, the tool's input schema,
// and its model-facing description.
type ToolInfo struct {
	Name        string
	Phase       string
	RiskLevel   string
	InputSchema map[string]any
	Description string
}

// catalogProvider is the registered source of the live MCP tool catalog.
var catalogProvider func() []ToolInfo

// SetCatalogProvider registers the live catalog source. It is called from
// internal/mcp's init; tests may register a fake catalog.
func SetCatalogProvider(p func() []ToolInfo) {
	catalogProvider = p
}

// toolCatalog returns the live catalog and whether a provider is registered.
func toolCatalog() ([]ToolInfo, bool) {
	if catalogProvider == nil {
		return nil, false
	}
	return catalogProvider(), true
}

// ---------------------------------------------------------------------------
// G30 — format:gofmt
// ---------------------------------------------------------------------------

// GofmtCheck (G30) runs `gofmt -l` over the changed .go files and reports
// every unformatted file as an advisory WARN finding. The gofmt tool is
// deterministic: its output is identical across machines, so the verdict is
// reproducible. A gofmt tool failure (syntax error, missing binary) is a
// check ERROR per the Check contract.
type GofmtCheck struct{}

// NewGofmtCheck constructs the gofmt diff-gate check.
func NewGofmtCheck() *GofmtCheck { return &GofmtCheck{} }

// Name is the stable check identifier used in CheckResult.Name and policy
// routing.
func (GofmtCheck) Name() string { return "format:gofmt" }

// Run scans the changed .go files with `gofmt -l` and returns a WARN result
// listing every unformatted file. Deleted files (not on disk) are skipped.
func (c *GofmtCheck) Run(ctx context.Context, req domain.ChangeRequest) (domain.CheckResult, error) {
	if req.RepositoryRoot == "" {
		return domain.CheckResult{Name: c.Name(), Status: domain.StatusError, Error: "repository root required"}, nil
	}
	var goFiles []string
	for _, fc := range req.Files {
		if !strings.HasSuffix(fc.Path, ".go") {
			continue
		}
		abs := filepath.Join(req.RepositoryRoot, filepath.FromSlash(fc.Path))
		if _, err := os.Stat(abs); err != nil {
			continue // deleted or not on disk
		}
		goFiles = append(goFiles, abs)
	}
	if len(goFiles) == 0 {
		return domain.CheckResult{Name: c.Name(), Status: domain.StatusPass}, nil
	}
	sort.Strings(goFiles) // deterministic output order
	cmd := exec.CommandContext(ctx, "gofmt", "-l")
	cmd.Args = append(cmd.Args, goFiles...)
	out, err := cmd.Output()
	if err != nil {
		return domain.CheckResult{Name: c.Name(), Status: domain.StatusError, Error: fmt.Sprintf("gofmt -l failed: %v", err)}, nil
	}
	var findings []domain.Finding
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		rel := line
		if r, rerr := filepath.Rel(req.RepositoryRoot, line); rerr == nil {
			rel = filepath.ToSlash(r)
		}
		findings = append(findings, domain.Finding{
			RuleID:       "format:gofmt",
			Severity:     domain.SeverityWarn,
			Category:     domain.CategoryBuild,
			File:         rel,
			Message:      "file is not gofmt-formatted (run gofmt -w)",
			Explanation:  "Unformatted Go source is a deterministic, machine-checkable quality violation; gofmt output is stable, so this finding is reproducible.",
			SuggestedFix: "gofmt -w " + rel,
			RuleVersion:  "1",
			Confidence:   1.0,
			Scope:        "file",
			Evidence: []domain.Evidence{{
				Kind:        "tool-output",
				Description: "gofmt -l listed the file",
				Location:    rel,
			}},
		})
	}
	if len(findings) == 0 {
		return domain.CheckResult{Name: c.Name(), Status: domain.StatusPass}, nil
	}
	return domain.CheckResult{Name: c.Name(), Status: domain.StatusWarn, Findings: findings}, nil
}

// ---------------------------------------------------------------------------
// G31 — vulnerability:sec
// ---------------------------------------------------------------------------

// VulnCheck (G31) wraps the in-house deterministic vulnerability scanner
// (internal/sec) scoped to the changed files. Sec severities are mapped to
// Blueprint severities on the same ladder kern's secret adapter uses
// (severityFromSec): error → BLOCK, warning → WARN, info → INFO. A BLOCK
// finding (critical vulnerability) therefore fails the gate even in advisory
// mode; warnings never do.
type VulnCheck struct{}

// NewVulnCheck constructs the vulnerability diff-gate check.
func NewVulnCheck() *VulnCheck { return &VulnCheck{} }

// Name is the stable check identifier.
func (VulnCheck) Name() string { return "vulnerability:sec" }

// Run scans each changed file on disk with sec.ScanFile and maps the findings.
// Test files are excluded, mirroring sec's own root-walk semantics (the
// scanner skips _test/_spec files), so a change to a test file — whose
// fixtures routinely contain flagged-looking strings — never trips the gate.
func (c *VulnCheck) Run(ctx context.Context, req domain.ChangeRequest) (domain.CheckResult, error) {
	if req.RepositoryRoot == "" {
		return domain.CheckResult{Name: c.Name(), Status: domain.StatusError, Error: "repository root required"}, nil
	}
	var findings []domain.Finding
	for _, fc := range req.Files {
		if fc.Op == domain.OpDelete {
			continue // deleted files have no content to scan
		}
		rel := filepath.ToSlash(fc.Path)
		if isTestFilePath(rel) {
			continue // sec's root walk skips test files; mirror that
		}
		abs := filepath.Join(req.RepositoryRoot, filepath.FromSlash(fc.Path))
		src, err := os.ReadFile(abs)
		if err != nil {
			continue // not on disk (e.g. rename destination not yet written)
		}
		for _, sf := range sec.ScanFile(rel, src) {
			findings = append(findings, vulnFinding(sf))
		}
	}
	if len(findings) == 0 {
		return domain.CheckResult{Name: c.Name(), Status: domain.StatusPass}, nil
	}
	status := domain.StatusWarn
	for _, f := range findings {
		if f.Severity == domain.SeverityBlock {
			status = domain.StatusBlock
			break
		}
	}
	return domain.CheckResult{Name: c.Name(), Status: status, Findings: findings}, nil
}

// vulnFinding maps one sec finding to a Blueprint finding.
func vulnFinding(sf sec.Finding) domain.Finding {
	return domain.Finding{
		RuleID:       "vulnerability:" + sf.Rule,
		Severity:     vulnSeverity(sf.Severity),
		Category:     domain.CategoryPolicy,
		File:         sf.File,
		Line:         sf.Line,
		Message:      sf.Message,
		Explanation:  "The deterministic in-house vulnerability scanner flagged this pattern in a changed file.",
		SuggestedFix: "Remove or fix the flagged pattern, then re-run `kern diff-gate` to confirm.",
		RuleVersion:  "1",
		Confidence:   0.9,
		Scope:        "file",
		Evidence: []domain.Evidence{{
			Kind:        "pattern-match",
			Description: fmt.Sprintf("sec rule: %s", sf.Rule),
			Location:    fmt.Sprintf("%s:%d", sf.File, sf.Line),
		}},
	}
}

// vulnSeverity maps the sec severity vocabulary ("error"|"warning"|"info") to
// Blueprint severities, mirroring severityFromSec in adapters/kern.
func vulnSeverity(s string) domain.Severity {
	switch s {
	case "error":
		return domain.SeverityBlock
	case "warning":
		return domain.SeverityWarn
	case "info":
		return domain.SeverityInfo
	default:
		return domain.SeverityInfo
	}
}

// ---------------------------------------------------------------------------
// G32 — schema:drift
// ---------------------------------------------------------------------------

// schemaBaselineRelPath is the repo-relative path (slash-separated) of the
// MCP tool-schema baseline file written by `kern diff-gate --init-baseline`.
const schemaBaselineRelPath = ".kern/diff-gate/tool-schemas.json"

// toolSchemaEntry is one baseline entry: the deterministic fingerprint of one
// MCP tool's contract (name, phase, risk, canonical InputSchema JSON).
type toolSchemaEntry struct {
	Name   string `json:"name"`
	Phase  string `json:"phase"`
	Risk   string `json:"risk"`
	SHA256 string `json:"sha256"`
}

// schemaBaseline is the on-disk baseline file format (JSON, deterministic
// ordering — entries sorted by tool name).
type schemaBaseline struct {
	SchemaVersion int               `json:"schema_version"`
	Tools         []toolSchemaEntry `json:"tools"`
}

// schemaBaselineVersion is the format version of the baseline file.
const schemaBaselineVersion = 1

// SchemaDriftCheck (G32) fingerprints the MCP tool catalog and compares it
// against a committed baseline at <root>/.kern/diff-gate/tool-schemas.json.
// Added/removed/changed tools are reported as WARN findings. With
// initBaseline set, the baseline is written (or refreshed) and the check
// reports PASS "baseline initialized".
type SchemaDriftCheck struct {
	root         string
	initBaseline bool
}

// NewSchemaDriftCheck constructs the schema-drift check bound to a repo root.
// initBaseline selects write-mode: the check writes the baseline instead of
// comparing against it.
func NewSchemaDriftCheck(root string, initBaseline bool) *SchemaDriftCheck {
	return &SchemaDriftCheck{root: root, initBaseline: initBaseline}
}

// Name is the stable check identifier.
func (c *SchemaDriftCheck) Name() string { return "schema:drift" }

// Run fingerprints the live MCP catalog and compares it with the baseline.
func (c *SchemaDriftCheck) Run(ctx context.Context, req domain.ChangeRequest) (domain.CheckResult, error) {
	if c.root == "" {
		return domain.CheckResult{Name: c.Name(), Status: domain.StatusError, Error: "repository root required"}, nil
	}
	catalog, ok := toolCatalog()
	if !ok {
		// No catalog provider (mcp not linked): nothing to fingerprint.
		return domain.CheckResult{Name: c.Name(), Status: domain.StatusSkip, Skipped: true}, nil
	}
	entries := fingerprintCatalog(catalog)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	baselinePath := filepath.Join(c.root, filepath.FromSlash(schemaBaselineRelPath))

	if c.initBaseline {
		if err := writeSchemaBaseline(baselinePath, entries); err != nil {
			return domain.CheckResult{Name: c.Name(), Status: domain.StatusError, Error: fmt.Sprintf("write schema baseline: %v", err)}, nil
		}
		return domain.CheckResult{Name: c.Name(), Status: domain.StatusPass, Findings: []domain.Finding{{
			RuleID:      "schema:drift",
			Severity:    domain.SeverityInfo,
			Category:    domain.CategoryPolicy,
			Message:     fmt.Sprintf("schema baseline initialized (%d tools) at %s", len(entries), schemaBaselineRelPath),
			RuleVersion: "1",
			Confidence:  1.0,
			Scope:       "repo",
			Evidence:    []domain.Evidence{{Kind: "baseline", Description: "baseline written from the live MCP catalog", Location: schemaBaselineRelPath}},
		}}}, nil
	}

	baseline, err := loadSchemaBaseline(baselinePath)
	if err != nil {
		if os.IsNotExist(err) {
			return domain.CheckResult{Name: c.Name(), Status: domain.StatusWarn, Findings: []domain.Finding{{
				RuleID:      "schema:drift",
				Severity:    domain.SeverityWarn,
				Category:    domain.CategoryPolicy,
				Message:     "no schema baseline found; run `kern diff-gate --init-baseline` to establish one",
				Explanation: "Without a baseline the MCP tool-schema drift guard cannot compare anything. The baseline is a committed JSON file under .kern/diff-gate/.",
				RuleVersion: "1",
				Confidence:  1.0,
				Scope:       "repo",
			}}}, nil
		}
		return domain.CheckResult{Name: c.Name(), Status: domain.StatusError, Error: fmt.Sprintf("load schema baseline: %v", err)}, nil
	}

	findings := schemaDriftFindings(entries, baseline.Tools)
	if len(findings) == 0 {
		return domain.CheckResult{Name: c.Name(), Status: domain.StatusPass}, nil
	}
	return domain.CheckResult{Name: c.Name(), Status: domain.StatusWarn, Findings: findings}, nil
}

// fingerprintCatalog fingerprints every tool in the catalog.
func fingerprintCatalog(tools []ToolInfo) []toolSchemaEntry {
	entries := make([]toolSchemaEntry, 0, len(tools))
	for _, t := range tools {
		entries = append(entries, toolSchemaEntry{
			Name:   t.Name,
			Phase:  t.Phase,
			Risk:   t.RiskLevel,
			SHA256: toolFingerprint(t),
		})
	}
	return entries
}

// toolFingerprint computes the deterministic sha256 of a tool's contract:
// name, phase, risk, and its InputSchema marshaled canonically (encoding/json
// sorts map keys recursively, so the digest is stable across runs).
func toolFingerprint(t ToolInfo) string {
	schemaJSON, err := json.Marshal(t.InputSchema)
	if err != nil {
		schemaJSON = []byte("{}")
	}
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00%s\x00%s", t.Name, t.Phase, t.RiskLevel, schemaJSON)
	return hex.EncodeToString(h.Sum(nil))
}

// schemaDriftFindings diffs the live catalog against the baseline, returning
// one WARN finding per added/removed/changed tool in deterministic order
// (sorted by message).
func schemaDriftFindings(current, baseline []toolSchemaEntry) []domain.Finding {
	currentByName := make(map[string]toolSchemaEntry, len(current))
	for _, e := range current {
		currentByName[e.Name] = e
	}
	baselineByName := make(map[string]toolSchemaEntry, len(baseline))
	for _, e := range baseline {
		baselineByName[e.Name] = e
	}
	var findings []domain.Finding
	for _, e := range current {
		b, ok := baselineByName[e.Name]
		if !ok {
			findings = append(findings, schemaDriftFinding(e.Name, "added", "", e.SHA256))
			continue
		}
		if b.SHA256 != e.SHA256 {
			findings = append(findings, schemaDriftFinding(e.Name, "changed", b.SHA256, e.SHA256))
		}
	}
	for name := range baselineByName {
		if _, ok := currentByName[name]; !ok {
			findings = append(findings, schemaDriftFinding(name, "removed", "", ""))
		}
	}
	sort.Slice(findings, func(i, j int) bool { return findings[i].Message < findings[j].Message })
	return findings
}

// schemaDriftFinding builds one drift finding (advisory WARN).
func schemaDriftFinding(name, kind, oldHash, newHash string) domain.Finding {
	msg := fmt.Sprintf("MCP tool %s %s since baseline", name, kind)
	if kind == "changed" {
		msg = fmt.Sprintf("MCP tool %s schema changed (sha256 %s -> %s)", name, shortHash(oldHash), shortHash(newHash))
	}
	return domain.Finding{
		RuleID:       "schema:drift",
		Severity:     domain.SeverityWarn,
		Category:     domain.CategoryPolicy,
		File:         schemaBaselineRelPath,
		Message:      msg,
		Explanation:  "The MCP tool catalog drifted from the committed schema baseline. Re-run `kern diff-gate --init-baseline` after intentionally changing tool contracts, and review unintended drift.",
		SuggestedFix: "kern diff-gate --init-baseline (after review)",
		RuleVersion:  "1",
		Confidence:   1.0,
		Scope:        "repo",
		Evidence: []domain.Evidence{{
			Kind:        "fingerprint",
			Description: fmt.Sprintf("tool %s drifted (%s)", name, kind),
			Location:    name,
		}},
	}
}

// shortHash truncates a sha256 for readable messages.
func shortHash(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

// writeSchemaBaseline persists the baseline as pretty JSON, creating the
// .kern/diff-gate directory as needed.
func writeSchemaBaseline(path string, entries []toolSchemaEntry) error {
	bl := schemaBaseline{SchemaVersion: schemaBaselineVersion, Tools: entries}
	data, err := json.MarshalIndent(bl, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// loadSchemaBaseline reads and validates the baseline file.
func loadSchemaBaseline(path string) (schemaBaseline, error) {
	var bl schemaBaseline
	data, err := os.ReadFile(path)
	if err != nil {
		return bl, err
	}
	if err := json.Unmarshal(data, &bl); err != nil {
		return bl, fmt.Errorf("parse %s: %w", path, err)
	}
	if bl.SchemaVersion != schemaBaselineVersion {
		return bl, fmt.Errorf("unsupported schema baseline version %d (want %d)", bl.SchemaVersion, schemaBaselineVersion)
	}
	return bl, nil
}

// ---------------------------------------------------------------------------
// G33 — exec:unsafe
// ---------------------------------------------------------------------------

// ExecUnsafeCheck (G33) flags changed non-test .go files that import
// os/exec, call exec.Command, or contain the "sh -c" shell-out literal. It is
// a deterministic string scan (stdlib only): cheap, reproducible, and scoped
// to the changed set.
type ExecUnsafeCheck struct{}

// NewExecUnsafeCheck constructs the unsafe-execution diff-gate check.
func NewExecUnsafeCheck() *ExecUnsafeCheck { return &ExecUnsafeCheck{} }

// Name is the stable check identifier.
func (ExecUnsafeCheck) Name() string { return "exec:unsafe" }

// Run flags every changed non-test .go file that exercises external
// processes.
func (c *ExecUnsafeCheck) Run(ctx context.Context, req domain.ChangeRequest) (domain.CheckResult, error) {
	if req.RepositoryRoot == "" {
		return domain.CheckResult{Name: c.Name(), Status: domain.StatusError, Error: "repository root required"}, nil
	}
	var findings []domain.Finding
	for _, fc := range req.Files {
		rel := filepath.ToSlash(fc.Path)
		if !strings.HasSuffix(rel, ".go") || isTestFilePath(rel) {
			continue
		}
		abs := filepath.Join(req.RepositoryRoot, filepath.FromSlash(fc.Path))
		src, err := os.ReadFile(abs)
		if err != nil {
			continue // deleted or not on disk
		}
		if bytes.Contains(src, []byte(`"os/exec"`)) || bytes.Contains(src, []byte("exec.Command")) || bytes.Contains(src, []byte("sh -c")) {
			findings = append(findings, domain.Finding{
				RuleID:       "exec:unsafe",
				Severity:     domain.SeverityWarn,
				Category:     domain.CategoryPolicy,
				File:         rel,
				Message:      "changed file executes external processes (os/exec import, exec.Command call, or sh -c literal)",
				Explanation:  "Executing external processes from source is a supply-chain and injection risk; every addition deserves human review. The diff gate is advisory: it flags the file, never blocks by default.",
				SuggestedFix: "Review the exec usage; prefer library calls or validate every input before shelling out.",
				RuleVersion:  "1",
				Confidence:   1.0,
				Scope:        "file",
				Evidence: []domain.Evidence{{
					Kind:        "string-scan",
					Description: "os/exec import, exec.Command call, or sh -c literal",
					Location:    filepath.ToSlash(fc.Path),
				}},
			})
		}
	}
	if len(findings) == 0 {
		return domain.CheckResult{Name: c.Name(), Status: domain.StatusPass}, nil
	}
	return domain.CheckResult{Name: c.Name(), Status: domain.StatusWarn, Findings: findings}, nil
}

// isTestFilePath reports whether a repo-relative path names a test file,
// using the same substring conventions as internal/sec's isTestFile
// (Go/Python/JS/TS `_test.`/`.test.`/`.spec.`, Java *Test.java/*IT.java, and
// Rust `_test.rs`).
func isTestFilePath(path string) bool {
	base := filepath.Base(path)
	if strings.Contains(base, "_test.") || strings.Contains(base, ".test.") ||
		strings.Contains(base, ".spec.") || strings.Contains(base, "_spec.") {
		return true
	}
	for _, suffix := range []string{"Test.java", "Tests.java", "IT.java", "Spec.java", "_test.rs"} {
		if strings.HasSuffix(base, suffix) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// G34 — changelog:missing
// ---------------------------------------------------------------------------

// ChangelogCheck (G34) warns when the changed set contains non-doc source
// changes (anything that is not *.md, docs/, or *.json config) but no
// CHANGELOG.md entry.
type ChangelogCheck struct{}

// NewChangelogCheck constructs the changelog diff-gate check.
func NewChangelogCheck() *ChangelogCheck { return &ChangelogCheck{} }

// Name is the stable check identifier.
func (ChangelogCheck) Name() string { return "changelog:missing" }

// Run returns a WARN finding when source changes lack a CHANGELOG.md entry.
func (c *ChangelogCheck) Run(ctx context.Context, req domain.ChangeRequest) (domain.CheckResult, error) {
	hasSource := false
	hasChangelog := false
	for _, fc := range req.Files {
		p := filepath.ToSlash(fc.Path)
		if isChangelogPath(p) {
			hasChangelog = true
			continue
		}
		if isDocOnlyPath(p) {
			continue
		}
		hasSource = true
	}
	if !hasSource || hasChangelog {
		return domain.CheckResult{Name: c.Name(), Status: domain.StatusPass}, nil
	}
	return domain.CheckResult{Name: c.Name(), Status: domain.StatusWarn, Findings: []domain.Finding{{
		RuleID:       "changelog:missing",
		Severity:     domain.SeverityWarn,
		Category:     domain.CategoryPolicy,
		Message:      "changes without CHANGELOG.md entry",
		Explanation:  "The changed set contains non-doc source changes but no CHANGELOG.md entry. The diff gate is advisory: it flags the omission, never blocks by default.",
		SuggestedFix: "Add a CHANGELOG.md entry describing the user-visible change.",
		RuleVersion:  "1",
		Confidence:   1.0,
		Scope:        "repo",
		Evidence:     []domain.Evidence{{Kind: "change-set", Description: "source changes present, CHANGELOG.md absent from the changed set"}},
	}}}, nil
}

// isChangelogPath reports whether a path is a CHANGELOG.md (any directory).
func isChangelogPath(p string) bool {
	return filepath.Base(p) == "CHANGELOG.md"
}

// isDocOnlyPath reports whether a path is excluded from the "source change"
// test: markdown docs, the docs/ tree, and JSON config files.
func isDocOnlyPath(p string) bool {
	return strings.HasSuffix(p, ".md") || strings.HasPrefix(p, "docs/") || strings.HasSuffix(p, ".json")
}

// ---------------------------------------------------------------------------
// G35 — catalog:drift
// ---------------------------------------------------------------------------

// pluginToolRe extracts opencode plugin tool definitions (`kern_xxx: tool(`).
// It mirrors the parity invariant in internal/setup/setup_test.go so the
// diff-gate check and the parity test agree on the plugin surface.
var pluginToolRe = regexp.MustCompile(`kern_[a-zA-Z0-9_]+:\s*tool\(`)

// pluginPhaseRe extracts the plugin's TOOL_PHASES map entries
// (`kern_xxx: "phase",`). It deliberately matches ONLY the phase metadata, so
// the 121 tool definitions (`kern_xxx: tool(`) and the drift-gate tool-name
// regex above never collide with it.
var pluginPhaseRe = regexp.MustCompile(`kern_([a-zA-Z0-9_]+):\s*"(explore|plan|edit|verify|meta|cross)"`)

// CatalogDriftCheck (G35) compares the MCP tool catalog (mcp.ToolNames())
// with the tool set declared in the opencode plugin
// (internal/setup/assets/plugin/kern.ts). A mismatch is a BLOCK: the plugin
// surface silently lagging the universal catalog is the real drift guard.
type CatalogDriftCheck struct {
	root string
}

// NewCatalogDriftCheck constructs the catalog-drift check bound to a repo
// root (the plugin asset is read relative to it).
func NewCatalogDriftCheck(root string) *CatalogDriftCheck {
	return &CatalogDriftCheck{root: root}
}

// Name is the stable check identifier.
func (c *CatalogDriftCheck) Name() string { return "catalog:drift" }

// Run compares the live MCP catalog with the plugin's declared tool set.
// When the plugin asset is absent (not a kern repo) or no catalog provider is
// registered, the check SKIPs.
func (c *CatalogDriftCheck) Run(ctx context.Context, req domain.ChangeRequest) (domain.CheckResult, error) {
	if c.root == "" {
		return domain.CheckResult{Name: c.Name(), Status: domain.StatusError, Error: "repository root required"}, nil
	}
	catalog, ok := toolCatalog()
	if !ok {
		return domain.CheckResult{Name: c.Name(), Status: domain.StatusSkip, Skipped: true}, nil
	}
	pluginPath := filepath.Join(c.root, filepath.FromSlash("internal/setup/assets/plugin/kern.ts"))
	content, err := os.ReadFile(pluginPath)
	if err != nil {
		return domain.CheckResult{Name: c.Name(), Status: domain.StatusSkip, Skipped: true}, nil
	}
	mcpSet := make(map[string]bool, len(catalog))
	for _, t := range catalog {
		mcpSet[t.Name] = true
	}
	findings := compareToolSets(mcpSet, string(content))
	// Phase parity: a tool whose phase differs between the MCP catalog and
	// the plugin's TOOL_PHASES map means the plugin would advertise the wrong
	// surface for a given KERN_MCP_PHASE — a BLOCK, same as a name drift.
	findings = append(findings, compareToolPhases(catalog, string(content))...)
	if len(findings) == 0 {
		return domain.CheckResult{Name: c.Name(), Status: domain.StatusPass}, nil
	}
	return domain.CheckResult{Name: c.Name(), Status: domain.StatusBlock, Findings: findings}, nil
}

// compareToolSets returns BLOCK-severity findings for every tool present in
// one surface but not the other (the MCP catalog is the source of truth).
func compareToolSets(mcpSet map[string]bool, pluginContent string) []domain.Finding {
	pluginSet := make(map[string]bool)
	for _, m := range pluginToolRe.FindAllString(pluginContent, -1) {
		name := strings.TrimSuffix(m, ": tool(")
		if strings.HasPrefix(name, "kern_") {
			pluginSet[name] = true
		}
	}
	var findings []domain.Finding
	for name := range mcpSet {
		if !pluginSet[name] {
			findings = append(findings, catalogDriftFinding(name, "missing from plugin (internal/setup/assets/plugin/kern.ts)"))
		}
	}
	for name := range pluginSet {
		if !mcpSet[name] {
			findings = append(findings, catalogDriftFinding(name, "present in plugin but not in the MCP catalog"))
		}
	}
	sort.Slice(findings, func(i, j int) bool { return findings[i].Message < findings[j].Message })
	return findings
}

// compareToolPhases returns BLOCK-severity findings for every tool whose phase
// differs between the MCP catalog (source of truth) and the plugin's
// TOOL_PHASES map. Tools present on only one side are ignored: name drift is
// already covered by compareToolSets.
func compareToolPhases(catalog []ToolInfo, pluginContent string) []domain.Finding {
	pluginPhase := make(map[string]string)
	for _, m := range pluginPhaseRe.FindAllStringSubmatch(pluginContent, -1) {
		pluginPhase["kern_"+m[1]] = m[2]
	}
	catalogPhase := make(map[string]string, len(catalog))
	for _, t := range catalog {
		catalogPhase[t.Name] = t.Phase
	}
	var findings []domain.Finding
	for name, cp := range catalogPhase {
		pp, ok := pluginPhase[name]
		if !ok {
			continue // phase unknown in plugin: name drift covers it
		}
		if cp != pp {
			findings = append(findings, catalogDriftFinding(name, fmt.Sprintf("phase mismatch: catalog=%s plugin=%s", cp, pp)))
		}
	}
	sort.Slice(findings, func(i, j int) bool { return findings[i].Message < findings[j].Message })
	return findings
}

// catalogDriftFinding builds one BLOCK-severity catalog drift finding.
func catalogDriftFinding(name, detail string) domain.Finding {
	return domain.Finding{
		RuleID:       "catalog:drift",
		Severity:     domain.SeverityBlock,
		Category:     domain.CategoryPolicy,
		File:         "internal/setup/assets/plugin/kern.ts",
		Message:      fmt.Sprintf("MCP catalog drift: tool %s %s", name, detail),
		Explanation:  "Every agent consumes the MCP server via tools/list; the opencode plugin must expose exactly the same tool set so no surface silently lags the universal catalog. A mismatch blocks the diff gate.",
		SuggestedFix: "Update internal/setup/assets/plugin/kern.ts to match the MCP catalog (and the on-disk .opencode copy via `kern setup`).",
		RuleVersion:  "1",
		Confidence:   1.0,
		Scope:        "repo",
		Evidence:     []domain.Evidence{{Kind: "set-diff", Description: detail, Location: name}},
	}
}
