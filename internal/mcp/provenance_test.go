package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/governance"
)

// provenanceProject builds a small cross-package tree: public/PublicA calls
// secret/SecretB, so a task scope denying secret/ must filter SecretB out of
// any retrieval response while keeping PublicA and its other context.
func provenanceProject(t *testing.T) string {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module demo\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"app.go": `package main

// Greet says hello.
func Greet() { println(PublicA()) }

func main() { Greet() }
`,
		"public/a.go": `package public

// PublicA returns the length of the secret value.
func PublicA() int { return len(secret.SecretB()) }
`,
		"secret/b.go": `package secret

// SecretB is the denied symbol.
func SecretB() string { return "hidden" }
`,
	}
	for rel, content := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// registerP12Agent registers a context.read agent under a unique ID so
// parallel/package tests never collide on the shared in-memory registry.
func registerP12Agent(t *testing.T) string {
	t.Helper()
	id := "p12-agent-" + t.Name()
	if err := governance.RegisterAgent(governance.NewAgent(id, "P1.2 Test", "tester", []governance.Permission{
		{Resource: "context", Action: "read"},
	})); err != nil {
		t.Fatalf("register agent: %v", err)
	}
	return id
}

// deniedScope is the task-scope argument denying the secret/ directory,
// matching the kern_authorize_context tool's {paths, denied_paths, ...} shape.
func deniedScope() map[string]any {
	return map[string]any{"denied_paths": []any{"secret/"}}
}

// provenanceOf extracts the structured provenance field from a tool response
// result envelope.
func provenanceOf(t *testing.T, resp map[string]any) map[string]any {
	t.Helper()
	res, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result in response: %+v", resp)
	}
	p, ok := res["provenance"].(map[string]any)
	if !ok {
		t.Fatalf("no provenance field in result envelope: %+v", res)
	}
	return p
}

// TestProvenance_RawExplore verifies the P0.1 flip: omitting agent_id (with
// no KERN_MCP_PERMISSIVE) no longer returns raw unfiltered results. The call
// is governed by the default agent with the default cwd-scoped scope — an
// explicit denied scope is honored, the denied symbol is excluded, provenance
// is mode="governed" with policy_source="default-scoped", and an auditable
// authorizing rule is present.
func TestProvenance_RawExplore(t *testing.T) {
	root := provenanceProject(t)
	resp := mcpCall(t, "kern_explore", map[string]any{
		"root":   root,
		"symbol": "PublicA",
		"depth":  "0",
		"scope":  deniedScope(),
	})
	text, isErr := toolResultText(t, resp)
	if isErr {
		t.Fatalf("default-governed explore returned error: %s", text)
	}
	// The denied symbol must not leak through the call-flow sections (names
	// the response serves as symbol evidence).
	flow := text
	if i := strings.Index(flow, "== source =="); i >= 0 {
		flow = flow[:i]
	}
	if strings.Contains(flow, "SecretB") {
		t.Fatalf("default-governed mode leaked denied symbol in call flow: %q", flow)
	}
	if !strings.Contains(text, "PublicA") || !strings.Contains(text, "Greet") {
		t.Fatalf("default-governed mode dropped allowed symbols: %q", text)
	}
	p := provenanceOf(t, resp)
	if p["mode"] != "governed" {
		t.Fatalf("expected mode=governed without agent_id, got %v", p["mode"])
	}
	rule, ok := p["authorizing_rule"].(map[string]any)
	if !ok {
		t.Fatalf("default-governed mode must carry an authorizing rule: %+v", p)
	}
	if rule["policy_source"] != "default-scoped" {
		t.Fatalf("expected policy_source default-scoped, got %v", rule["policy_source"])
	}
	if rule["fingerprint"] == "" || rule["decided_at"] == "" {
		t.Fatalf("authorizing rule incomplete: %+v", rule)
	}
	if sv, ok := p["schema_version"].(float64); !ok || int(sv) != 1 {
		t.Fatalf("expected schema_version 1, got %v", p["schema_version"])
	}
	ix, _ := p["index"].(map[string]any)
	if ix == nil {
		t.Fatalf("expected index identity, got %+v", p)
	}
	if ix["content_root"] == "" || ix["freshness_verdict"] == "" {
		t.Fatalf("index identity incomplete: %+v", ix)
	}
	syms, _ := p["symbols"].([]any)
	if len(syms) == 0 {
		t.Fatalf("governed provenance must list returned symbols, got %+v", p["symbols"])
	}
	var names []string
	for _, s := range syms {
		m := s.(map[string]any)
		names = append(names, m["name"].(string))
		if m["name"] == "SecretB" || strings.Contains(m["qualified"].(string), "SecretB") {
			t.Fatalf("governed provenance leaked denied symbol: %+v", m)
		}
	}
	for _, want := range []string{"PublicA", "Greet", "main"} {
		found := false
		for _, n := range names {
			if n == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("governed provenance symbols %v missing allowed symbol %s", names, want)
		}
	}
}

// TestProvenance_PermissiveRawExplore verifies the KERN_MCP_PERMISSIVE=1
// escape hatch restores the legacy raw mode: omitting agent_id returns FULL
// unfiltered results including the denied symbol, provenance mode="raw", and
// no authorizing rule.
func TestProvenance_PermissiveRawExplore(t *testing.T) {
	t.Setenv("KERN_MCP_PERMISSIVE", "1")
	root := provenanceProject(t)
	resp := mcpCall(t, "kern_explore", map[string]any{
		"root":   root,
		"symbol": "PublicA",
		"depth":  "0",
	})
	text, isErr := toolResultText(t, resp)
	if isErr {
		t.Fatalf("permissive raw explore returned error: %s", text)
	}
	// Full results: SecretB (the denied-in-governed symbol) IS present.
	if !strings.Contains(text, "SecretB") {
		t.Fatalf("permissive raw mode must return full results including SecretB, got: %q", text)
	}
	p := provenanceOf(t, resp)
	if p["mode"] != "raw" {
		t.Fatalf("expected mode=raw, got %v", p["mode"])
	}
	if _, ok := p["authorizing_rule"]; ok {
		t.Fatalf("raw mode must not carry an authorizing rule: %+v", p["authorizing_rule"])
	}
	if sv, ok := p["schema_version"].(float64); !ok || int(sv) != 1 {
		t.Fatalf("expected schema_version 1, got %v", p["schema_version"])
	}
	ix, _ := p["index"].(map[string]any)
	if ix == nil {
		t.Fatalf("expected index identity, got %+v", p)
	}
	if ix["content_root"] == "" || ix["freshness_verdict"] == "" {
		t.Fatalf("index identity incomplete: %+v", ix)
	}
	syms, _ := p["symbols"].([]any)
	if len(syms) == 0 {
		t.Fatalf("raw provenance must list returned symbols, got %+v", p["symbols"])
	}
	var names []string
	for _, s := range syms {
		names = append(names, s.(map[string]any)["name"].(string))
	}
	joined := strings.Join(names, ",")
	for _, want := range []string{"PublicA", "Greet", "main", "SecretB"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("raw provenance symbols %v missing %s", names, want)
		}
	}
}

// TestProvenance_DefaultScopedExplore verifies the new default-governed path:
// no agent_id and no KERN_MCP_PERMISSIVE → the call is governed by the default
// agent with the cwd-scoped default scope. Results are confined to the project
// root (all its symbols retrievable, nothing outside it), provenance is
// mode="governed" with policy_source="default-scoped" and policy
// "deny-unlisted", and an explicit denied scope is honored exactly as in an
// explicit governed call.
func TestProvenance_DefaultScopedExplore(t *testing.T) {
	root := provenanceProject(t)
	// No agent_id, no scope: the default scope admits the whole project root.
	resp := mcpCall(t, "kern_explore", map[string]any{
		"root":   root,
		"symbol": "PublicA",
		"depth":  "0",
	})
	text, isErr := toolResultText(t, resp)
	if isErr {
		t.Fatalf("default-scoped explore returned error: %s", text)
	}
	for _, want := range []string{"PublicA", "Greet", "main", "SecretB"} {
		if !strings.Contains(text, want) {
			t.Fatalf("default scope must admit the whole project root (missing %s): %q", want, text)
		}
	}
	p := provenanceOf(t, resp)
	if p["mode"] != "governed" {
		t.Fatalf("expected mode=governed for the default path, got %v", p["mode"])
	}
	rule, ok := p["authorizing_rule"].(map[string]any)
	if !ok {
		t.Fatalf("default path must carry an authorizing rule: %+v", p)
	}
	if rule["policy_source"] != "default-scoped" {
		t.Fatalf("expected policy_source default-scoped, got %v", rule["policy_source"])
	}
	if rule["policy"] != "deny-unlisted" {
		t.Fatalf("expected deny-unlisted for the cwd-scoped default, got %v", rule["policy"])
	}
	// An explicit denied scope is honored on the default path: the denied
	// symbol is excluded from the call-flow sections exactly as in an explicit
	// governed call. (The verbatim source of the ALLOWED root symbol is
	// authorized context and may mention the call.)
	resp = mcpCall(t, "kern_explore", map[string]any{
		"root":   root,
		"symbol": "PublicA",
		"depth":  "0",
		"scope":  deniedScope(),
	})
	text, isErr = toolResultText(t, resp)
	if isErr {
		t.Fatalf("default-scoped explore (denied scope) returned error: %s", text)
	}
	flow := text
	if i := strings.Index(flow, "== source =="); i >= 0 {
		flow = flow[:i]
	}
	if strings.Contains(flow, "SecretB") {
		t.Fatalf("denied paths must be excluded on the default path: %q", flow)
	}
}

// TestProvenance_GovernedExplore verifies governed mode (agent_id+task):
// results are filtered to the authorized scope and provenance carries the
// populated authorizing rule. SecretB must not appear in the text NOR in the
// provenance symbols (its existence is not leaked).
func TestProvenance_GovernedExplore(t *testing.T) {
	root := provenanceProject(t)
	agent := registerP12Agent(t)
	resp := mcpCall(t, "kern_explore", map[string]any{
		"root":     root,
		"symbol":   "PublicA",
		"depth":    "0",
		"agent_id": agent,
		"task":     "T-p12-governed",
		"scope":    deniedScope(),
	})
	text, isErr := toolResultText(t, resp)
	if isErr {
		t.Fatalf("governed explore returned error: %s", text)
	}
	// The denied symbol must not leak through the call-flow sections (names
	// the response serves as symbol evidence). The verbatim source of the
	// ALLOWED root symbol is authorized context and may mention the call.
	flow := text
	if i := strings.Index(flow, "== source =="); i >= 0 {
		flow = flow[:i]
	}
	if strings.Contains(flow, "SecretB") {
		t.Fatalf("governed mode leaked denied symbol in call flow: %q", flow)
	}
	if !strings.Contains(text, "PublicA") || !strings.Contains(text, "Greet") {
		t.Fatalf("governed mode dropped allowed symbols: %q", text)
	}
	p := provenanceOf(t, resp)
	if p["mode"] != "governed" {
		t.Fatalf("expected mode=governed, got %v", p["mode"])
	}
	rule, ok := p["authorizing_rule"].(map[string]any)
	if !ok {
		t.Fatalf("governed mode must carry an authorizing rule: %+v", p)
	}
	if rule["policy_source"] != "task-scope" {
		t.Fatalf("expected policy_source task-scope, got %v", rule["policy_source"])
	}
	if rule["policy"] != "deny-unlisted" {
		t.Fatalf("expected policy deny-unlisted, got %v", rule["policy"])
	}
	if rule["fingerprint"] == "" || rule["decided_at"] == "" {
		t.Fatalf("authorizing rule incomplete: %+v", rule)
	}
	// The provenance symbols are the filtered subset actually returned: the
	// denied symbol never appears; local symbols carry file:line (foreign
	// targets like the Go builtin "len" are kept name-only, as external deps).
	syms, _ := p["symbols"].([]any)
	if len(syms) == 0 {
		t.Fatalf("governed provenance must list the allowed symbols returned")
	}
	var names []string
	for _, s := range syms {
		m := s.(map[string]any)
		names = append(names, m["name"].(string))
		if m["name"] == "SecretB" || strings.Contains(m["qualified"].(string), "SecretB") {
			t.Fatalf("governed provenance leaked denied symbol: %+v", m)
		}
		if m["name"] != "len" && (m["file"] == "" || int(m["line"].(float64)) == 0) {
			t.Fatalf("provenance symbol missing file:line: %+v", m)
		}
	}
	for _, want := range []string{"PublicA", "Greet", "main"} {
		found := false
		for _, n := range names {
			if n == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("governed provenance symbols %v missing allowed symbol %s", names, want)
		}
	}
}

// TestProvenance_GovernedContextDenial verifies the non-leaking denial path
// for a single-symbol lookup: a denied symbol is indistinguishable from a
// missing one ("no symbol found"), and the provenance records the governed
// decision with an empty symbol set.
func TestProvenance_GovernedContextDenial(t *testing.T) {
	root := provenanceProject(t)
	agent := registerP12Agent(t)
	args := map[string]any{
		"root":     root,
		"symbol":   "SecretB",
		"agent_id": agent,
		"task":     "T-p12-context",
		"scope":    deniedScope(),
	}
	resp := mcpCall(t, "kern_context", args)
	text, isErr := toolResultText(t, resp)
	if isErr {
		t.Fatalf("denied context should be an empty success, not an error: %s", text)
	}
	if !strings.Contains(text, "no symbol found: SecretB") {
		t.Fatalf("denied symbol must look identical to missing, got: %q", text)
	}
	if strings.Contains(text, "source") || strings.Contains(text, "secret") {
		t.Fatalf("denied context leaked content: %q", text)
	}
	p := provenanceOf(t, resp)
	if p["mode"] != "governed" {
		t.Fatalf("expected mode=governed on denial, got %v", p["mode"])
	}
	if rule, ok := p["authorizing_rule"].(map[string]any); !ok || rule["fingerprint"] == "" {
		t.Fatalf("denial must carry an auditable authorizing rule: %+v", p)
	}
	if syms, _ := p["symbols"].([]any); len(syms) != 0 {
		t.Fatalf("denied response must have empty symbols, got %+v", p["symbols"])
	}
	// The same governed call for an allowed symbol returns the source and a
	// populated symbol set.
	args["symbol"] = "Greet"
	resp = mcpCall(t, "kern_context", args)
	text, isErr = toolResultText(t, resp)
	if isErr {
		t.Fatalf("allowed context returned error: %s", text)
	}
	if !strings.Contains(text, "Greet says hello") {
		t.Fatalf("allowed context missing source: %q", text)
	}
	p = provenanceOf(t, resp)
	if syms, _ := p["symbols"].([]any); len(syms) != 1 {
		t.Fatalf("allowed context should list the single returned symbol: %+v", p["symbols"])
	}
}

// TestProvenance_MetaForwardsAgentContext verifies that kern_meta (the NL
// router, default single-tool surface) forwards agent_id/task/scope into the
// routed sub-tool, so governed retrieval is reachable through the default
// surface — and the meta response carries governed provenance.
func TestProvenance_MetaForwardsAgentContext(t *testing.T) {
	root := provenanceProject(t)
	agent := registerP12Agent(t)
	resp := mcpCall(t, "kern_meta", map[string]any{
		"root":     root,
		"request":  "how does PublicA work?",
		"agent_id": agent,
		"task":     "T-p12-meta",
		"scope":    deniedScope(),
	})
	text, isErr := toolResultText(t, resp)
	if isErr {
		t.Fatalf("governed meta explore returned error: %s", text)
	}
	if !strings.Contains(text, "[kern] classified as: kern_explore") {
		t.Fatalf("expected explore classification, got: %q", text)
	}
	// The forwarded scope filtered SecretB out of the routed explore's call
	// flow (its source section is the allowed root's verbatim definition).
	flow := text
	if i := strings.Index(flow, "== source =="); i >= 0 {
		flow = flow[:i]
	}
	if strings.Contains(flow, "SecretB") {
		t.Fatalf("meta-routed governed explore leaked denied symbol: %q", flow)
	}
	p := provenanceOf(t, resp)
	if p["mode"] != "governed" {
		t.Fatalf("meta response must carry governed provenance, got %v", p["mode"])
	}
}

// TestProvenance_GovernedGraph verifies the names-only adjacency path: a
// governed kern_graph call filters denied names out of the rendered text
// (including section counts) and carries governed provenance whose symbols
// equal the returned set.
func TestProvenance_GovernedGraph(t *testing.T) {
	root := provenanceProject(t)
	agent := registerP12Agent(t)
	resp := mcpCall(t, "kern_graph", map[string]any{
		"root":       root,
		"symbol":     "PublicA",
		"max_tokens": "400",
		"agent_id":   agent,
		"task":       "T-p12-graph",
		"scope":      deniedScope(),
	})
	text, isErr := toolResultText(t, resp)
	if isErr {
		t.Fatalf("governed graph returned error: %s", text)
	}
	// The graph text names only allowed symbols (SecretB's definition is in
	// the denied path; the raw-mode callee edge would show it).
	if strings.Contains(text, "SecretB") {
		t.Fatalf("governed graph leaked denied symbol: %q", text)
	}
	if !strings.Contains(text, "graph PublicA") || !strings.Contains(text, "Greet") {
		t.Fatalf("governed graph dropped allowed context: %q", text)
	}
	p := provenanceOf(t, resp)
	if p["mode"] != "governed" {
		t.Fatalf("expected mode=governed, got %v", p["mode"])
	}
	rule, ok := p["authorizing_rule"].(map[string]any)
	if !ok || rule["fingerprint"] == "" {
		t.Fatalf("governed graph must carry the authorizing rule: %+v", p)
	}
	for _, s := range p["symbols"].([]any) {
		if strings.Contains(s.(map[string]any)["name"].(string), "SecretB") {
			t.Fatalf("governed graph provenance leaked denied symbol: %+v", s)
		}
	}
}

// TestProvenance_UnknownAgentDenial verifies the authentication-denial path:
// an unregistered agent_id fails closed with an error, and the error response
// still carries governed provenance with the deny policy in the rule.
func TestProvenance_UnknownAgentDenial(t *testing.T) {
	root := provenanceProject(t)
	resp := mcpCall(t, "kern_explore", map[string]any{
		"root":     root,
		"symbol":   "PublicA",
		"agent_id": "p12-no-such-agent",
		"task":     "T-p12-deny",
		"scope":    deniedScope(),
	})
	text, isErr := toolResultText(t, resp)
	if !isErr {
		t.Fatalf("unknown agent must be denied, got: %s", text)
	}
	if !strings.Contains(text, "authorized") {
		t.Fatalf("expected authorization denial message, got: %q", text)
	}
	p := provenanceOf(t, resp)
	if p["mode"] != "governed" {
		t.Fatalf("denial must carry governed provenance, got %v", p["mode"])
	}
	rule, ok := p["authorizing_rule"].(map[string]any)
	if !ok || rule["fingerprint"] == "" {
		t.Fatalf("denial rule must be auditable: %+v", p)
	}
	if rule["policy"] != "governance.authentication" {
		t.Fatalf("expected governance.authentication deny policy, got %v", rule["policy"])
	}
	if syms, _ := p["symbols"].([]any); len(syms) != 0 {
		t.Fatalf("denied response must not leak symbols: %+v", p["symbols"])
	}
}

// TestProvenance_JSONRoundTrip verifies the provenance struct survives a
// marshal→unmarshal cycle field-for-field, in both governed and raw shapes.
func TestProvenance_JSONRoundTrip(t *testing.T) {
	governed := &Provenance{
		SchemaVersion: provenanceSchemaVersion,
		Mode:          ProvenanceModeGoverned,
		AuthorizingRule: &AuthorizingRule{
			PolicySource: "task-scope",
			Policy:       "deny-unlisted",
			Fingerprint:  "sha256-hex",
			DecidedAt:    "2026-08-30T12:00:00Z",
		},
		Index: IndexProvenance{
			TreeOID:          "abc123",
			ContentRoot:      "sha256-hex",
			GitCommit:        "def456",
			BuiltAt:          "2026-08-30T12:00:00Z",
			FreshnessVerdict: "fresh",
		},
		Symbols: []SymbolProvenance{
			{Name: "FuncName", Qualified: "pkg.FuncName", File: "path/to/file.go", Line: 42},
		},
	}
	b, err := json.Marshal(governed)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out Provenance
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.SchemaVersion != 1 || out.Mode != ProvenanceModeGoverned {
		t.Fatalf("schema/mode not preserved: %+v", out)
	}
	if out.AuthorizingRule == nil {
		t.Fatal("authorizing rule lost in round-trip")
	}
	if out.AuthorizingRule.PolicySource != "task-scope" || out.AuthorizingRule.Policy != "deny-unlisted" ||
		out.AuthorizingRule.Fingerprint != "sha256-hex" || out.AuthorizingRule.DecidedAt != "2026-08-30T12:00:00Z" {
		t.Fatalf("authorizing rule not preserved: %+v", out.AuthorizingRule)
	}
	if out.Index.TreeOID != "abc123" || out.Index.ContentRoot != "sha256-hex" ||
		out.Index.GitCommit != "def456" || out.Index.FreshnessVerdict != "fresh" {
		t.Fatalf("index identity not preserved: %+v", out.Index)
	}
	if len(out.Symbols) != 1 || out.Symbols[0] != governed.Symbols[0] {
		t.Fatalf("symbols not preserved: %+v", out.Symbols)
	}

	// Raw shape: no authorizing rule → absent in JSON, nil after round-trip.
	raw := &Provenance{
		SchemaVersion: provenanceSchemaVersion,
		Mode:          ProvenanceModeRaw,
		Index:         IndexProvenance{ContentRoot: "c", FreshnessVerdict: "stale"},
		Symbols:       []SymbolProvenance{},
	}
	b, err = json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal raw: %v", err)
	}
	if strings.Contains(string(b), "authorizing_rule") {
		t.Fatalf("raw mode must not serialize an authorizing rule: %s", b)
	}
	var out2 Provenance
	if err := json.Unmarshal(b, &out2); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}
	if out2.Mode != ProvenanceModeRaw || out2.AuthorizingRule != nil {
		t.Fatalf("raw round-trip not preserved: %+v", out2)
	}
}
