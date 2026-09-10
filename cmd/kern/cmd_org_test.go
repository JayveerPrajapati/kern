package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/enterprise"
	"github.com/JayveerPrajapati/kern/internal/governance"
)

// TestOrgProjectsSubcommand asserts `kern org --project NAME=ROOT projects`
// prints the registered project as a name<TAB>root line.
func TestOrgProjectsSubcommand(t *testing.T) {
	root := t.TempDir()
	out := captureStdout(t, func() {
		runOrg([]string{"--project", "p1=" + root, "projects"})
	})
	if !strings.Contains(out, "p1\t"+root) {
		t.Fatalf("projects output missing %q line, got:\n%s", "p1\t"+root, out)
	}
}

// TestOrgTeamsRoundTrip exercises create → list → show → remove against a
// server built the same way runOrg builds it (enterprise.New + Register +
// RegisterAgent).
func TestOrgTeamsRoundTrip(t *testing.T) {
	srv := enterprise.New()
	if err := srv.Register("p1", t.TempDir()); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := srv.RegisterAgent(governance.NewAgent("a1", "Agent One", "default", nil)); err != nil {
		t.Fatalf("RegisterAgent: %v", err)
	}

	out := captureStdout(t, func() { runOrgTeamCreate(srv, []string{"t1", "Team One", "--project", "p1", "--member", "a1"}) })
	if !strings.Contains(out, "created team t1") {
		t.Fatalf("create output = %q, want %q", out, "created team t1")
	}

	out = captureStdout(t, func() { runOrgTeams(srv, nil, false) })
	if !strings.Contains(out, "t1\tTeam One\ta1\tp1") {
		t.Fatalf("teams list missing round-trip line, got:\n%s", out)
	}

	out = captureStdout(t, func() { runOrgTeamShow(srv, []string{"t1"}) })
	for _, want := range []string{"ID: t1", "Name: Team One", "Projects: p1", "Members: a1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("team show output missing %q, got:\n%s", want, out)
		}
	}

	out = captureStdout(t, func() { runOrgTeamRemove(srv, []string{"t1"}) })
	if !strings.Contains(out, "removed team t1") {
		t.Fatalf("remove output = %q, want %q", out, "removed team t1")
	}
	if _, ok := srv.Team("t1"); ok {
		t.Fatal("team t1 still exists after remove")
	}
}

// TestOrgAgentsRegisterList asserts `kern org agents register` round-trips
// through `kern org agents --json`: registered id/name/type with the parsed
// permissions appear in the JSON list. The server is built directly (as in
// TestOrgTeamsRoundTrip) because runOrg builds a fresh in-process server per
// invocation, so register and list must share one.
func TestOrgAgentsRegisterList(t *testing.T) {
	srv := enterprise.New()
	out := captureStdout(t, func() {
		runOrgAgentRegister(srv, []string{"a1", "Agent One", "--type", "builder", "--perm", "source:read", "--perm", "tests:write"})
	})
	if !strings.Contains(out, "registered agent a1") {
		t.Fatalf("register output = %q, want %q", out, "registered agent a1")
	}

	got := srv.Agents()
	if len(got) != 1 || got[0].ID != "a1" || got[0].Name != "Agent One" || got[0].Type != "builder" {
		t.Fatalf("unexpected registered agents: %+v", got)
	}
	if len(got[0].Permissions) != 2 ||
		got[0].Permissions[0] != (governance.Permission{Resource: "source", Action: "read"}) ||
		got[0].Permissions[1] != (governance.Permission{Resource: "tests", Action: "write"}) {
		t.Fatalf("unexpected permissions: %+v", got[0].Permissions)
	}

	out = captureStdout(t, func() { runOrgAgents(srv, []string{"--json"}, false) })
	var parsed struct {
		Agents []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"agents"`
		Count int `json:"count"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if parsed.Count != 1 {
		t.Fatalf("expected count 1, got %d: %s", parsed.Count, out)
	}
	a := parsed.Agents[0]
	if a.ID != "a1" || a.Name != "Agent One" || a.Type != "builder" {
		t.Fatalf("unexpected agent: %+v", a)
	}
}

// TestOrgUnknownSubcommand asserts an unknown `kern org` subcommand fails with
// the usage exit code (exitError{code: 2}).
func TestOrgUnknownSubcommand(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic from fatalUsage")
		}
		ee, ok := r.(exitError)
		if !ok || ee.code != 2 {
			t.Fatalf("expected exitError{code: 2}, got %#v", r)
		}
	}()
	_ = captureStderr(t, func() { runOrg([]string{"frobnicate"}) })
}
