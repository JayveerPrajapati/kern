package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

// G18 (gate "policy evaluator wired into MCP handlers"): the MCP handlers must
// attach the policy engine to the service so agent-facing validation actually
// ENFORCES the loaded config. Before P0-5 the handlers only passed
// service.WithConfig(cfg.Service) and never attached the evaluator, so
// mode:warn, per-source overrides (P0-3), and skip rules had no effect over
// MCP — the raw, unenforced check status leaked through.
//
// These tests mirror the g5/g15 style: they call the handlers directly and
// require the real kern binary (gated by g5RequireKern like g15).

// g18WriteConfig writes a .blueprint/config.yaml into the fixture repo. The
// config file is intentionally left UNSTAGED: policy.Load reads it from disk,
// and the staged/proposed validation must never see it as a changed file.
func g18WriteConfig(t *testing.T, dir, content string) {
	t.Helper()
	g5Write(t, dir, ".blueprint/config.yaml", content)
}

// TestG18_PolicyAppliedViaMCP_WarnMode: a config with mode:warn + secrets:block
// must downgrade the raw BLOCK secret finding to WARN through the staged
// handler. With the evaluator detached (pre-P0-5) this returned the raw BLOCK.
func TestG18_PolicyAppliedViaMCP_WarnMode(t *testing.T) {
	g5RequireKern(t)
	dir := g5Repo(t)
	g18WriteConfig(t, dir, "version: 1\nmode: warn\npolicies:\n  secrets: block\n")

	// Stage a file with a hardcoded secret (same AKIA* token form the g5/g15
	// tests use; detected by gitleaks and the in-house kern scanner alike).
	g5Stage(t, dir, "config.go", "package main\nconst AWSKey = \"AKIA1234567890ABCDEF\"\n")

	args, _ := json.Marshal(map[string]string{"repo": dir, "source": "agent"})
	tr := g5CallValidateStaged(t, args)
	m := g5ParseResult(t, tr)

	status, _ := m["status"].(string)
	if !strings.EqualFold(status, "WARN") {
		t.Fatalf("status=%v, want WARN (mode:warn must downgrade the BLOCK); full: %v", m["status"], m)
	}
	// The finding must survive the downgrade (warn preserves findings).
	findings, _ := m["findings"].([]interface{})
	if len(findings) == 0 {
		t.Fatalf("expected findings preserved under warn mode; full: %v", m)
	}
}

// TestG18_PolicyAppliedViaMCP_SourceOverride: a per-source override
// (sources: {agent: {duplication: skip}}) must flow through the proposed
// handler: a proposed file duplicating an existing function yields a
// duplication finding that would WARN by default, but the agent
// override forces the check's status to SKIP.
func TestG18_PolicyAppliedViaMCP_SourceOverride(t *testing.T) {
	g5RequireKern(t)
	g5RequireFingerprint(t)
	dir := g5Repo(t)
	g18WriteConfig(t, dir, "version: 1\nsources:\n  agent:\n    duplication: skip\n")

	// Give db/db.go a real function body, then propose an identical function in
	// a new file: structural fingerprints match -> finding. (Trivial one-line
	// functions are discounted below the WARN threshold by design, same as
	// TestG15_ProposedDuplicationWarn.)
	const existing = "package db\n\nfunc Query() error {\n\terr := open()\n\tif err != nil {\n\t\treturn err\n\t}\n\treturn nil\n}\n"
	g5Write(t, dir, "db/db.go", existing)
	g5Git(t, dir, "add", "db/db.go")
	g5Git(t, dir, "commit", "-qm", "richer db")

	args, _ := json.Marshal(map[string]interface{}{
		"repo":   dir,
		"source": "agent",
		"files": []map[string]interface{}{
			{"path": "web/dup.go", "content": "package web\n\nfunc Query() error {\n\terr := open()\n\tif err != nil {\n\t\treturn err\n\t}\n\treturn nil\n}\n"},
		},
	})
	tr := g15CallProposed(t, args)
	m := g15ParseResult(t, tr)

	// The duplication check must be SKIPped by the per-source override
	// (P0-3 SourceRules flowing through MCP). T2.1: the check is now named
	// duplication:jscpd (duplication:advisory as in-house fallback).
	checks, _ := m["checks"].([]interface{})
	dupStatus := ""
	for _, c := range checks {
		cm, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		if name, _ := cm["name"].(string); strings.HasPrefix(name, "duplication:") {
			dupStatus, _ = cm["status"].(string)
			break
		}
	}
	if !strings.EqualFold(dupStatus, "SKIP") {
		t.Fatalf("duplication check status=%q, want SKIP (agent override duplication:skip); full: %v", dupStatus, m)
	}
}

// TestG18_PolicyDefaultStillBlocks: no config file means conservative defaults
// (secrets: block) apply — a staged secret must still BLOCK. Guards against
// the policy wiring over-downgrading enforcement.
func TestG18_PolicyDefaultStillBlocks(t *testing.T) {
	g5RequireKern(t)
	dir := g5Repo(t)
	// No .blueprint/config.yaml: DefaultConfig -> secrets block.

	g5Stage(t, dir, "config.go", "package main\nconst AWSKey = \"AKIA1234567890ABCDEF\"\n")

	args, _ := json.Marshal(map[string]string{"repo": dir, "source": "agent"})
	tr := g5CallValidateStaged(t, args)
	m := g5ParseResult(t, tr)

	status, _ := m["status"].(string)
	if !strings.EqualFold(status, "BLOCK") {
		t.Fatalf("status=%v, want BLOCK (default secrets:block must still block); full: %v", m["status"], m)
	}
}
