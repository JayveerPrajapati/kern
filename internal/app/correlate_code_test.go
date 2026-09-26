package app

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/incident"
)

// codeFixture writes a tiny Go module with a service directory (svc/) so the
// incident→twin→code correlation engine has an exact service→code mapping.
func codeFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module codefix\n\ngo 1.21\n")
	writeFile(t, filepath.Join(root, "svc", "main.go"),
		"package main\n\n// CacheService returns a caching service.\nfunc CacheService() string { return \"c\" }\n\nfunc main() { println(CacheService()) }\n")
	writeFile(t, filepath.Join(root, "svc", "cache.go"),
		"package main\n\n// Cache is the cache handle.\nvar Cache = \"mem\"\n")
	return root
}

// TestCorrelateIncidentResolvesServiceToCode pins the engine's exact
// service→code mapping (Feature Batch D): an alert naming the "svc" service
// (a root-relative directory) implicates every source file under svc/ with
// confidence "high", deterministically.
func TestCorrelateIncidentResolvesServiceToCode(t *testing.T) {
	root := codeFixture(t)
	alert := domain.Alert{ID: "A-1", Severity: domain.SeverityCritical, Message: "boom", Service: "svc"}
	corr, err := CorrelateIncident(root, alert)
	if err != nil {
		t.Fatalf("CorrelateIncident: %v", err)
	}
	if corr.AffectedService != "svc" {
		t.Errorf("AffectedService = %q, want svc", corr.AffectedService)
	}
	if corr.Confidence != "high" {
		t.Errorf("Confidence = %q, want high (exact service→code mapping)", corr.Confidence)
	}
	if len(corr.ImplicatedFiles) != 2 {
		t.Fatalf("ImplicatedFiles = %v, want the 2 files under svc/", corr.ImplicatedFiles)
	}
	files := strings.Join(corr.ImplicatedFiles, ",")
	if !strings.Contains(files, "svc/main.go") || !strings.Contains(files, "svc/cache.go") {
		t.Errorf("implicated files missing svc sources: %v", corr.ImplicatedFiles)
	}
	// Symbols defined in the implicated files are surfaced deterministically.
	if len(corr.ImplicatedSymbols) == 0 {
		t.Error("ImplicatedSymbols empty, want symbols from svc files")
	}
	var syms = strings.Join(corr.ImplicatedSymbols, ",")
	if !strings.Contains(syms, "CacheService") {
		t.Errorf("implicated symbols missing CacheService: %v", corr.ImplicatedSymbols)
	}
	// Deterministic: running twice yields identical output.
	corr2, err := CorrelateIncident(root, alert)
	if err != nil {
		t.Fatalf("second CorrelateIncident: %v", err)
	}
	if strings.Join(corr2.ImplicatedFiles, ",") != files || strings.Join(corr2.ImplicatedSymbols, ",") != syms {
		t.Error("correlation is not deterministic across runs")
	}
}

// TestCorrelateIncidentUnknownService: an alert whose service maps to no
// directory and no twin entity yields a graceful empty correlation with
// confidence "low" — never an error.
func TestCorrelateIncidentUnknownService(t *testing.T) {
	root := codeFixture(t)
	alert := domain.Alert{ID: "A-2", Severity: domain.SeverityWarning, Message: "who knows", Service: "no-such-service"}
	corr, err := CorrelateIncident(root, alert)
	if err != nil {
		t.Fatalf("CorrelateIncident: %v", err)
	}
	if corr.Confidence != "low" {
		t.Errorf("Confidence = %q, want low", corr.Confidence)
	}
	if len(corr.ImplicatedFiles) != 0 || len(corr.ImplicatedSymbols) != 0 {
		t.Errorf("unknown service produced code: files=%v syms=%v", corr.ImplicatedFiles, corr.ImplicatedSymbols)
	}
	// Service-less alerts also correlate gracefully.
	_, err = CorrelateIncident(root, domain.Alert{ID: "A-3", Severity: domain.SeverityInfo, Message: "mystery"})
	if err != nil {
		t.Fatalf("service-less CorrelateIncident: %v", err)
	}
}

// TestCorrelateIncidentAutoAttachesPlaybook: a stored playbook matching the
// incident's signature is attached to the correlation report (the same lookup
// the incident engine's IngestAlert performs).
func TestCorrelateIncidentAutoAttachesPlaybook(t *testing.T) {
	root := codeFixture(t)
	alert := domain.Alert{ID: "A-4", Severity: domain.SeverityCritical, Message: "boom", Service: "svc"}
	sig := incident.SignatureForAlert(alert)
	if err := incident.AddPlaybook(root, incident.Playbook{Signature: sig, Steps: []string{"step1", "step2"}, Source: "test"}); err != nil {
		t.Fatalf("AddPlaybook: %v", err)
	}
	corr, err := CorrelateIncident(root, alert)
	if err != nil {
		t.Fatalf("CorrelateIncident: %v", err)
	}
	if corr.PlaybookSignature != sig {
		t.Errorf("PlaybookSignature = %q, want %q", corr.PlaybookSignature, sig)
	}
	if len(corr.PlaybookSteps) != 2 || corr.PlaybookSteps[0] != "step1" {
		t.Errorf("PlaybookSteps = %v, want [step1 step2]", corr.PlaybookSteps)
	}
}

// TestTaskServiceCorrelateCodeRendersReport: the TaskService lane renders the
// correlation report (with the code section + playbook) on an authoritative
// task, so the CLI/MCP surfaces share one render path.
func TestTaskServiceCorrelateCodeRendersReport(t *testing.T) {
	root := codeFixture(t)
	alert := domain.Alert{ID: "A-5", Severity: domain.SeverityCritical, Message: "boom", Service: "svc"}
	sig := incident.SignatureForAlert(alert)
	if err := incident.AddPlaybook(root, incident.Playbook{Signature: sig, Steps: []string{"rollback"}}); err != nil {
		t.Fatalf("AddPlaybook: %v", err)
	}
	p, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := NewTaskService(p, nil).WithAgentID("test")
	task, corr, text, err := ts.CorrelateCode(alert)
	if err != nil {
		t.Fatalf("CorrelateCode: %v", err)
	}
	if string(task.State) != "COMPLETED" {
		t.Errorf("state = %s, want COMPLETED", task.State)
	}
	if corr.Confidence != "high" {
		t.Errorf("Confidence = %q, want high", corr.Confidence)
	}
	for _, want := range []string{"CORRELATION for alert: A-5", "Affected service: svc", "Confidence: high", "svc/main.go", "Playbook: " + sig, "step: rollback"} {
		if !strings.Contains(text, want) {
			t.Errorf("report missing %q:\n%s", want, text)
		}
	}
}
