package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/incident"
)

// `kern incident list` browses the persisted incident history (Persona 3,
// SRE on-call): newest first, with severity/status, service, root cause
// and PR URL rendered per incident.
func TestIncidentListSubcommand(t *testing.T) {
	root := newRoot(t)
	store := incident.NewStore(root)
	if _, err := store.Save(&domain.Incident{
		Title:           "api latency spike",
		Severity:        domain.SeverityCritical,
		Status:          domain.IncidentInvestigating,
		AffectedService: "api",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	out := captureStdout(t, func() { runIncident([]string{"list", "--root", root}) })
	for _, want := range []string{"incidents (1", "api latency spike", "critical/INVESTIGATING", "service: api"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in output:\n%s", want, out)
		}
	}
}

// --json emits the store's list machine-readably.
func TestIncidentListJSON(t *testing.T) {
	root := newRoot(t)
	store := incident.NewStore(root)
	if _, err := store.Save(&domain.Incident{Title: "db down", Severity: domain.SeverityError}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	out := captureStdout(t, func() { runIncident([]string{"list", "--root", root, "--json"}) })
	var list []domain.Incident
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(list) != 1 || list[0].Title != "db down" {
		t.Fatalf("unexpected list: %+v", list)
	}
}

// An empty store prints a clean "no incidents recorded" instead of nothing.
func TestIncidentListEmpty(t *testing.T) {
	root := newRoot(t)
	out := captureStdout(t, func() { runIncident([]string{"list", "--root", root}) })
	if !strings.Contains(out, "no incidents recorded") {
		t.Fatalf("unexpected empty output: %s", out)
	}
}

// The --force gate override declines anything but an explicit y/Y — one
// fat-finger must not bypass the HIGH-risk repair gate (Persona 3). The
// prompt reader is shared with runHeal (healForceGateConfirmed); its full
// behavior is also covered in cmd_heal_args_test.go.
func TestConfirmForce(t *testing.T) {
	if !healForceGateConfirmed(strings.NewReader("y\n")) {
		t.Fatal("'y' must confirm")
	}
	if !healForceGateConfirmed(strings.NewReader("Y\n")) {
		t.Fatal("'Y' must confirm")
	}
	if healForceGateConfirmed(strings.NewReader("yes\n")) {
		t.Fatal("'yes' must decline (anything but y/Y)")
	}
	if healForceGateConfirmed(strings.NewReader("no\n")) {
		t.Fatal("'no' must decline")
	}
	if healForceGateConfirmed(strings.NewReader("\n")) {
		t.Fatal("empty input must decline")
	}
}
