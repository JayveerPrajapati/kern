package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// --- P2-4 (G25): Kern 2.0 Evidence provenance fields ---

// TestG25_CIArtifactCarriesProvenanceFields verifies the CI artifact's
// CIFinding carries the Kern 2.0 Evidence provenance fields for an
// architecture finding produced with the real kern binary: rule_version "1",
// kern_version (probed by the service, best-effort), index_freshness "fresh"
// (the CI worktree builds a fresh index), confidence 1.0, scope "file".
func TestG25_CIArtifactCarriesProvenanceFields(t *testing.T) {
	kernPath := requireKernPath(t)
	binPath := buildBlueprint(t)
	dir := g11Repo(t,
		map[string]string{
			"db/db.go":   "package db\nfunc Query() {}\n",
			"web/web.go": "package web\nfunc Handle() {}\n",
		},
		map[string]string{
			"web/bad.go": "package web\nimport \"example.com/repo/db\"\nfunc Bad() { db.Query() }\n",
		},
	)

	_, _, exitCode, artifact := runCICommand(t, binPath, dir, kernPath)
	if exitCode != 1 {
		t.Fatalf("exit code = %d, want 1 (BLOCK for boundary violation)", exitCode)
	}

	var found *CIFinding
	for i := range artifact.Findings {
		if artifact.Findings[i].RuleID == "architecture:boundary-violation" {
			found = &artifact.Findings[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("artifact missing architecture:boundary-violation finding: %+v", artifact.Findings)
	}
	if found.RuleVersion != "1" {
		t.Errorf("RuleVersion = %q, want \"1\"", found.RuleVersion)
	}
	if found.KernVersion == "" {
		t.Errorf("KernVersion empty, want the real kern version (best-effort probe)")
	}
	// P0.2: the CI worktree starts with no pre-existing index, so
	// ArchitectureCheck.Run builds it on demand — the honest freshness label
	// is "rebuilt" (a build was necessary). "fresh" would mean the index was
	// already current with no rebuild needed, which is not the case here.
	// Both labels indicate a current, usable index.
	if found.IndexFreshness != "fresh" && found.IndexFreshness != "rebuilt" {
		t.Errorf("IndexFreshness = %q, want \"fresh\" or \"rebuilt\" (CI worktree builds the index)", found.IndexFreshness)
	}
	if found.Confidence != 1.0 {
		t.Errorf("Confidence = %v, want 1.0", found.Confidence)
	}
	if found.Scope != "file" {
		t.Errorf("Scope = %q, want \"file\"", found.Scope)
	}
}

// TestG25_CheckJSONIncludesProvenanceFields verifies `blueprint check --json`
// on a repo with a secret finding emits rule_version + kern_version +
// confidence in the finding JSON (end-to-end with the real kern binary).
// --format=json is passed explicitly because g4BlueprintCheck pins
// --format=terminal as a base arg (flag last-wins overrides it).
func TestG25_CheckJSONIncludesProvenanceFields(t *testing.T) {
	_ = requireKernPath(t) // gate needs real kern
	bin := g4BuildBinary(t)
	dir := t.TempDir()
	g4GitRepo(t, dir)
	g4WriteFile(t, dir, "go.mod", "module example.com/test\n\ngo 1.23\n")
	g4WriteFile(t, dir, "clean.go", "package main\nfunc main() {}\n")
	g4RunGit(t, dir, "add", "-A")
	g4RunGit(t, dir, "commit", "-qm", "init")

	// Stage a file with a secret so a finding is produced.
	g4WriteFile(t, dir, "config.go", "package main\nconst AWSKey = \"AKIA1234567890ABCDEF\"\n")
	g4RunGit(t, dir, "add", "config.go")

	out, code := g4BlueprintCheck(t, bin, dir, "--format=json")
	if code != 1 {
		t.Fatalf("exit=%d want 1 (BLOCK for secret); output:\n%s", code, out)
	}
	var result struct {
		Status   string `json:"status"`
		Findings []struct {
			RuleID      string  `json:"rule_id"`
			RuleVersion string  `json:"rule_version"`
			KernVersion string  `json:"kern_version"`
			Confidence  float64 `json:"confidence"`
			Scope       string  `json:"scope"`
		} `json:"findings"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("parse check --json output: %v\n%s", err, out)
	}
	found := false
	for _, f := range result.Findings {
		if !strings.HasPrefix(f.RuleID, "secret:") {
			continue
		}
		found = true
		// T2.1: the secret check is the gitleaks adapter (or the in-house kern
		// scanner as fallback), so rule_version is the detector's version —
		// never empty — and confidence is the detector's (1.0 for gitleaks,
		// 0.95 for kern). P2-4 only requires the fields to be stamped.
		if f.RuleVersion == "" {
			t.Errorf("finding %s rule_version empty, want the detector's version", f.RuleID)
		}
		if f.KernVersion == "" {
			t.Errorf("finding %s kern_version empty, want the real kern version", f.RuleID)
		}
		if f.Confidence == 0 {
			t.Errorf("finding %s confidence = %v, want a non-zero detector confidence", f.RuleID, f.Confidence)
		}
		if f.Scope != "file" {
			t.Errorf("finding %s scope = %q, want \"file\"", f.RuleID, f.Scope)
		}
	}
	if !found {
		t.Fatalf("no secret finding in check --json output:\n%s", out)
	}
}
