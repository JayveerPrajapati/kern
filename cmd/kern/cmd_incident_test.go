package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/incident"
)

// incidentCLIFixture writes a tiny Go module with a service directory so the
// incident--correlate/--runbook surfaces (Feature Batch D) have an exact
// service→code mapping.
func incidentCLIFixture(t *testing.T) string {
	t.Helper()
	root := newRoot(t)
	files := map[string]string{
		"go.mod":      "module inclifx\n\ngo 1.20\n",
		"svc/main.go": "package main\n\n// CacheService returns the cache.\nfunc CacheService() string { return \"c\" }\n\nfunc main() { println(CacheService()) }\n",
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

func incidentAlertJSON(service, message string) string {
	b, _ := json.Marshal(domain.Alert{ID: "A-1", Severity: domain.SeverityCritical, Message: message, Service: service})
	return string(b)
}

// TestIncidentRunbookAddsAndLists: `kern incident --runbook <json>` stores a
// playbook and `--list-playbooks` returns it (deterministic, scriptable).
func TestIncidentRunbookAddsAndLists(t *testing.T) {
	root := incidentCLIFixture(t)
	alert := incidentAlertJSON("svc", "boom")
	var al domain.Alert
	if err := json.Unmarshal([]byte(alert), &al); err != nil {
		t.Fatal(err)
	}
	sig := incident.SignatureForAlert(al)
	runbook := `{"signature":"` + sig + `","steps":["step1","step2"],"source":"cli-test"}`

	out := captureStdout(t, func() { runIncident([]string{"--root", root, "--runbook", runbook}) })
	if !strings.Contains(out, "added playbook: "+sig) {
		t.Fatalf("runbook add output = %q, want added playbook %s", out, sig)
	}

	out = captureStdout(t, func() { runIncident([]string{"--root", root, "--list-playbooks"}) })
	if !strings.Contains(out, sig) || !strings.Contains(out, "step: step1") || !strings.Contains(out, "source: cli-test") {
		t.Fatalf("list output = %q, want signature %s, step1, source", out, sig)
	}
}

// TestIncidentCorrelateRendersCodeSection: `kern incident <alert> --correlate`
// renders the incident→twin→code correlation report (implicated files from the
// service mapping, confidence, and the auto-attached playbook).
func TestIncidentCorrelateRendersCodeSection(t *testing.T) {
	root := incidentCLIFixture(t)
	alert := incidentAlertJSON("svc", "boom")
	var al domain.Alert
	if err := json.Unmarshal([]byte(alert), &al); err != nil {
		t.Fatal(err)
	}
	sig := incident.SignatureForAlert(al)
	captureStdout(t, func() {
		runIncident([]string{"--root", root, "--runbook", `{"signature":"` + sig + `","steps":["rollback"]}`})
	})

	out := captureStdout(t, func() { runIncident([]string{"--root", root, alert, "--correlate"}) })
	for _, want := range []string{
		"CORRELATION for alert: A-1",
		"Affected service: svc",
		"Confidence: high",
		"svc/main.go",
		"CacheService",
		"Playbook: " + sig,
		"step: rollback",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("correlate output missing %q:\n%s", want, out)
		}
	}
}

// TestIncidentIngestAutoAttachesPlaybook: ingesting the same alert again after
// storing a playbook surfaces the runbook steps in the incident report.
func TestIncidentIngestAutoAttachesPlaybook(t *testing.T) {
	root := incidentCLIFixture(t)
	alert := incidentAlertJSON("svc", "boom")
	var al domain.Alert
	if err := json.Unmarshal([]byte(alert), &al); err != nil {
		t.Fatal(err)
	}
	sig := incident.SignatureForAlert(al)
	captureStdout(t, func() {
		runIncident([]string{"--root", root, "--runbook", `{"signature":"` + sig + `","steps":["step1","step2"]}`})
	})

	out := captureStdout(t, func() { runIncident([]string{"--root", root, alert}) })
	if !strings.Contains(out, "Playbook: "+sig) || !strings.Contains(out, "step: step1") {
		t.Fatalf("incident report missing auto-attached playbook:\n%s", out)
	}
}

// TestCorrelateCodeFlag: `kern correlate <alert> --code` includes the code
// correlation section; without the flag the runtime chain still renders
// (extended, not broken).
func TestCorrelateCodeFlag(t *testing.T) {
	root := incidentCLIFixture(t)
	alert := incidentAlertJSON("svc", "boom")

	out := captureStdout(t, func() { runCorrelate([]string{"--root", root, alert, "--code"}) })
	if !strings.Contains(out, "Implicated files") || !strings.Contains(out, "svc/main.go") {
		t.Fatalf("--code output missing code section:\n%s", out)
	}

	out = captureStdout(t, func() { runCorrelate([]string{"--root", root, alert}) })
	if !strings.Contains(out, "Evidence chain") {
		t.Fatalf("plain correlate output missing runtime chain:\n%s", out)
	}
}
