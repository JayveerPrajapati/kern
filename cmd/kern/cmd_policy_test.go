package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/governance"
)

// policyFile writes the given policy-set JSON (array or envelope) to a temp
// file and returns its path.
func policyFile(t *testing.T, policiesJSON string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "policies.json")
	if err := os.WriteFile(p, []byte(policiesJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPolicySetGetApplyHappyPath(t *testing.T) {
	orgRoot := t.TempDir()
	file := policyFile(t, `[
		{"ID":"pol-1","Name":"source_write","Description":"d","Rule":"MEDIUM source.write","Scope":"source","Enabled":true},
		{"ID":"pol-2","Name":"production_deploy","Description":"d","Rule":"CRITICAL production.deploy","Scope":"production","Enabled":true}
	]`)

	out := captureStdout(t, func() {
		runPolicy([]string{"set", "--root", orgRoot, "--file", file})
	})
	if !strings.Contains(out, "org policy written") || !strings.Contains(out, "2 policies") {
		t.Fatalf("set output = %q, want written summary", out)
	}

	out = captureStdout(t, func() {
		runPolicy([]string{"get", "--root", orgRoot})
	})
	for _, want := range []string{"org root:", "hash:", "drift:     none", "pol-1", "pol-2"} {
		if !strings.Contains(out, want) {
			t.Errorf("get output missing %q:\n%s", want, out)
		}
	}

	out = captureStdout(t, func() {
		runPolicy([]string{"apply", "--root", orgRoot})
	})
	if !strings.Contains(out, "already applied") {
		t.Fatalf("apply output = %q, want already-applied (no drift)", out)
	}

	// The document is on disk at the canonical path and parses.
	if _, ok, err := governance.LoadOrgPolicy(orgRoot); err != nil || !ok {
		t.Fatalf("LoadOrgPolicy after set: ok=%v err=%v", ok, err)
	}
}

func TestPolicyGetNoPolicy(t *testing.T) {
	orgRoot := t.TempDir()
	out := captureStdout(t, func() {
		runPolicy([]string{"get", "--root", orgRoot})
	})
	if !strings.Contains(out, "no org policy configured") {
		t.Fatalf("get output = %q, want the no-policy notice", out)
	}

	// No org root at all: get answers "no org" (exit 0), set/apply refuse.
	out = captureStdout(t, func() {
		runPolicy([]string{"get"})
	})
	if !strings.Contains(out, "no org root configured") {
		t.Fatalf("get without org root = %q, want the no-org notice", out)
	}
	_, code := captureStderrExit(t, func() {
		runPolicy([]string{"set"})
	})
	if code != 1 {
		t.Fatalf("set without org root exit = %d, want 1", code)
	}
}

func TestPolicySetMergeByID(t *testing.T) {
	orgRoot := t.TempDir()
	first := policyFile(t, `[{"ID":"pol-1","Name":"source_write","Rule":"MEDIUM source.write","Scope":"source","Enabled":true}]`)
	second := policyFile(t, `[
		{"ID":"pol-1","Name":"source_write","Rule":"HIGH source.write","Scope":"source","Enabled":true},
		{"ID":"pol-2","Name":"production_deploy","Rule":"CRITICAL production.deploy","Scope":"production","Enabled":true}
	]`)

	runPolicy([]string{"set", "--root", orgRoot, "--file", first})
	runPolicy([]string{"set", "--root", orgRoot, "--file", second, "--merge"})

	doc, ok, err := governance.LoadOrgPolicy(orgRoot)
	if err != nil || !ok {
		t.Fatalf("LoadOrgPolicy: ok=%v err=%v", ok, err)
	}
	if len(doc.Policies) != 2 {
		t.Fatalf("merged policies = %d, want 2 (pol-1 replaced, pol-2 appended)", len(doc.Policies))
	}
	byID := map[string]string{}
	for _, p := range doc.Policies {
		byID[p.ID] = p.Rule
	}
	if byID["pol-1"] != "HIGH source.write" {
		t.Errorf("pol-1 rule = %q, want the merged HIGH rule", byID["pol-1"])
	}
	if byID["pol-2"] != "CRITICAL production.deploy" {
		t.Errorf("pol-2 missing after merge: %v", byID)
	}
}

func TestPolicyDriftReportAndApply(t *testing.T) {
	orgRoot := t.TempDir()
	file := policyFile(t, `[{"ID":"pol-1","Name":"source_write","Description":"original","Rule":"MEDIUM source.write","Scope":"source","Enabled":true}]`)
	captureStdout(t, func() {
		runPolicy([]string{"set", "--root", orgRoot, "--file", file})
	})

	// Out-of-band edit: rewrite the document with changed content but the
	// ORIGINAL recorded hash — exactly what a hand-edit (or an older writer
	// that does not update the hash) leaves behind.
	doc, ok, err := governance.LoadOrgPolicy(orgRoot)
	if err != nil || !ok {
		t.Fatalf("LoadOrgPolicy: ok=%v err=%v", ok, err)
	}
	doc.Policies[0].Description = "tampered out-of-band"
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(governance.OrgPolicyPath(orgRoot), data, 0o600); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		runPolicy([]string{"get", "--root", orgRoot})
	})
	if !strings.Contains(out, "DETECTED") {
		t.Fatalf("get after out-of-band edit = %q, want DETECTED drift", out)
	}

	out = captureStdout(t, func() {
		runPolicy([]string{"apply", "--root", orgRoot})
	})
	if !strings.Contains(out, "re-applied") || !strings.Contains(out, "hash re-recorded") {
		t.Fatalf("apply output = %q, want re-applied + hash re-recorded", out)
	}

	out = captureStdout(t, func() {
		runPolicy([]string{"get", "--root", orgRoot})
	})
	if !strings.Contains(out, "drift:     none") {
		t.Fatalf("get after apply = %q, want drift resolved", out)
	}

	// The re-recorded hash matches the current content.
	doc2, _, _ := governance.LoadOrgPolicy(orgRoot)
	if doc2.Hash != governance.PolicyHash(doc2.Policies) {
		t.Errorf("recorded hash %q != content hash %q after apply", doc2.Hash, governance.PolicyHash(doc2.Policies))
	}
}

func TestPolicyGetJSON(t *testing.T) {
	orgRoot := t.TempDir()
	file := policyFile(t, `[{"ID":"pol-1","Name":"source_write","Rule":"MEDIUM source.write","Scope":"source","Enabled":true}]`)
	captureStdout(t, func() {
		runPolicy([]string{"set", "--root", orgRoot, "--file", file})
	})
	out := captureStdout(t, func() {
		runPolicy([]string{"get", "--root", orgRoot, "--json"})
	})
	var body struct {
		Root         string           `json:"root"`
		RecordedHash string           `json:"recorded_hash"`
		ContentHash  string           `json:"content_hash"`
		Drifted      bool             `json:"drifted"`
		Count        int              `json:"count"`
		Policies     []map[string]any `json:"policies"`
	}
	if err := json.Unmarshal([]byte(out), &body); err != nil {
		t.Fatalf("get --json output is not JSON: %v\n%s", err, out)
	}
	if body.Root != orgRoot || body.Count != 1 || len(body.Policies) != 1 {
		t.Fatalf("get --json = %+v, want root/count/policies populated", body)
	}
	if body.RecordedHash == "" || body.ContentHash == "" || body.Drifted {
		t.Fatalf("get --json hash/drift = recorded %q content %q drifted %v", body.RecordedHash, body.ContentHash, body.Drifted)
	}
}

func TestPolicyCorruptFailsClosed(t *testing.T) {
	orgRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(orgRoot, ".kern"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(governance.OrgPolicyPath(orgRoot), []byte(`{"version":1,"policies":`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, code := captureStderrExit(t, func() {
		runPolicy([]string{"get", "--root", orgRoot})
	})
	if code != 1 {
		t.Fatalf("get on corrupt store exit = %d, want 1 (fail-closed)", code)
	}
	_, code = captureStderrExit(t, func() {
		runPolicy([]string{"apply", "--root", orgRoot})
	})
	if code != 1 {
		t.Fatalf("apply on corrupt store exit = %d, want 1 (fail-closed)", code)
	}
}

func TestPolicySetEnvelopeForm(t *testing.T) {
	orgRoot := t.TempDir()
	file := policyFile(t, `{"policies":[{"ID":"pol-1","Name":"source_write","Rule":"MEDIUM source.write","Scope":"source","Enabled":true}]}`)
	out := captureStdout(t, func() {
		runPolicy([]string{"set", "--root", orgRoot, "--file", file})
	})
	if !strings.Contains(out, "1 policies") {
		t.Fatalf("set output = %q, want 1 policy written", out)
	}
}

func TestPolicyUnknownSubcommand(t *testing.T) {
	_, code := captureStderrExit(t, func() {
		runPolicy([]string{"frobnicate", "--root", t.TempDir()})
	})
	if code != 2 {
		t.Fatalf("unknown subcommand exit = %d, want 2 (usage)", code)
	}
}
