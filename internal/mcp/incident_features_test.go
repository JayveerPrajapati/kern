package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/incident"
)

// incidentFixture writes a tiny Go module with a service directory so the
// kern_incident correlate/runbook surfaces (Feature Batch D) have an exact
// service→code mapping to resolve.
func incidentFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module incmcp\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "svc"), 0o755); err != nil {
		t.Fatal(err)
	}
	src := "package main\n\n// CacheService returns the cache.\nfunc CacheService() string { return \"c\" }\n\nfunc main() { println(CacheService()) }\n"
	if err := os.WriteFile(filepath.Join(root, "svc", "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestKernIncidentRunbookSchemaValidated: kern_incident accepts the runbook
// argument (schema-validated) and stores the playbook; list_playbooks returns
// it.
func TestKernIncidentRunbookSchemaValidated(t *testing.T) {
	t.Parallel()
	root := incidentFixture(t)
	alert := `{"id":"A-1","severity":"critical","message":"boom","service":"svc"}`
	sig := signatureForAlert(t, alert)

	out := mcpAssertOK(t, "kern_incident", map[string]any{
		"root":    root,
		"runbook": `{"signature":"` + sig + `","steps":["step1","step2"],"source":"mcp-test"}`,
	})
	if !strings.Contains(out, "added playbook: "+sig) {
		t.Fatalf("runbook add output = %q, want added playbook %s", out, sig)
	}

	out = mcpAssertOK(t, "kern_incident", map[string]any{"root": root, "list_playbooks": true})
	if !strings.Contains(out, sig) || !strings.Contains(out, "step1") {
		t.Fatalf("list_playbooks output = %q, want signature %s and step1", out, sig)
	}
}

// TestKernIncidentCorrelateArg: kern_incident with correlate=true renders the
// incident→twin→code correlation report (implicated files from the service
// mapping) and surfaces the auto-attached playbook.
func TestKernIncidentCorrelateArg(t *testing.T) {
	t.Parallel()
	root := incidentFixture(t)
	alert := `{"id":"A-2","severity":"critical","message":"boom","service":"svc"}`
	sig := signatureForAlert(t, alert)
	mcpAssertOK(t, "kern_incident", map[string]any{
		"root":    root,
		"runbook": `{"signature":"` + sig + `","steps":["rollback"]}`,
	})

	out := mcpAssertOK(t, "kern_incident", map[string]any{
		"root":      root,
		"alert":     alert,
		"correlate": true,
	})
	for _, want := range []string{"CORRELATION for alert: A-2", "Affected service: svc", "svc/main.go", "Playbook: " + sig, "step: rollback"} {
		if !strings.Contains(out, want) {
			t.Errorf("correlate report missing %q:\n%s", want, out)
		}
	}
}

// TestKernIncidentIngestAutoAttachesPlaybook: ingesting an alert that matches
// a stored playbook attaches the runbook steps to the incident report.
func TestKernIncidentIngestAutoAttachesPlaybook(t *testing.T) {
	t.Parallel()
	root := incidentFixture(t)
	alert := `{"id":"A-3","severity":"critical","message":"boom","service":"svc"}`
	sig := signatureForAlert(t, alert)
	mcpAssertOK(t, "kern_incident", map[string]any{
		"root":    root,
		"runbook": `{"signature":"` + sig + `","steps":["step1","step2"]}`,
	})

	out := mcpAssertOK(t, "kern_incident", map[string]any{"root": root, "alert": alert})
	if !strings.Contains(out, "Playbook: "+sig) {
		t.Errorf("incident report missing playbook signature %s:\n%s", sig, out)
	}
	if !strings.Contains(out, "step: step1") || !strings.Contains(out, "step: step2") {
		t.Errorf("incident report missing playbook steps:\n%s", out)
	}
}

// TestKernCorrelateCodeArg: kern_correlate with code=true includes the
// incident→twin→code correlation section; without it, the runtime chain still
// renders (extended, not broken).
func TestKernCorrelateCodeArg(t *testing.T) {
	t.Parallel()
	root := incidentFixture(t)
	alert := `{"id":"A-4","severity":"critical","message":"boom","service":"svc"}`

	out := mcpAssertOK(t, "kern_correlate", map[string]any{"root": root, "alert": alert, "code": true})
	if !strings.Contains(out, "Implicated files") || !strings.Contains(out, "svc/main.go") {
		t.Fatalf("code=true report missing code section:\n%s", out)
	}

	out = mcpAssertOK(t, "kern_correlate", map[string]any{"root": root, "alert": alert})
	if !strings.Contains(out, "Evidence chain") {
		t.Fatalf("plain correlate report missing runtime chain:\n%s", out)
	}
}

// signatureForAlert derives the deterministic playbook signature for an alert
// JSON via the incident package's signature derivation (the same derivation
// the engine uses at ingestion, so the runbook signature matches).
func signatureForAlert(t *testing.T, alertJSON string) string {
	t.Helper()
	var al domain.Alert
	if err := json.Unmarshal([]byte(alertJSON), &al); err != nil {
		t.Fatalf("parse alert: %v", err)
	}
	return incident.SignatureForAlert(al)
}
