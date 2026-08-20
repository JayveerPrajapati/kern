package agents

import (
	"errors"
	"fmt"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/agent"
	"github.com/JayveerPrajapati/kern/internal/governance"
)

// ---- roles.go ----

func TestAllRoles(t *testing.T) {
	roles := AllRoles()
	if len(roles) != 7 {
		t.Fatalf("AllRoles() = %d roles, want 7", len(roles))
	}
	seen := map[Role]bool{}
	for _, info := range roles {
		if seen[info.Role] {
			t.Errorf("duplicate role %q in AllRoles", info.Role)
		}
		seen[info.Role] = true
		if info.Name == "" || info.Purpose == "" || info.Produces == "" || info.Consumes == "" || info.Autonomy == "" {
			t.Errorf("RoleInfo %q has an empty field: %+v", info.Role, info)
		}
	}
}

func TestForRole(t *testing.T) {
	for _, want := range []Role{RolePlanner, RoleArchitect, RoleCoder, RoleReviewer, RoleSecurity, RoleTester, RoleSRE} {
		info, ok := ForRole(want)
		if !ok {
			t.Errorf("ForRole(%q) not found", want)
			continue
		}
		if info.Role != want {
			t.Errorf("ForRole(%q).Role = %q", want, info.Role)
		}
	}
	if _, ok := ForRole(Role("no-such-role")); ok {
		t.Error("ForRole(unknown) reported found")
	}
}

// ---- specialist.go ----

func TestNewSpecialist(t *testing.T) {
	s := NewSpecialist(RoleCoder, "coder")
	if s.Role != RoleCoder {
		t.Errorf("Role = %q, want coder", s.Role)
	}
	if s.Agent.ID != "coder" || s.Agent.Name != "coder" || s.Agent.Type != string(RoleCoder) {
		t.Errorf("Agent identity wrong: %+v", s.Agent)
	}
	if s.Agent.CreatedAt.IsZero() {
		t.Error("CreatedAt not set")
	}
}

func TestSpecialistRegistry(t *testing.T) {
	reg := NewSpecialistRegistry()

	// Get on empty registry misses.
	if _, ok := reg.Get("planner"); ok {
		t.Error("Get on empty registry reported found")
	}
	if got := reg.ByRole(RolePlanner); len(got) != 0 {
		t.Errorf("ByRole on empty registry = %d, want 0", len(got))
	}

	// Register + Get.
	s := NewSpecialist(RolePlanner, "planner")
	if err := reg.Register(s); err != nil {
		t.Fatalf("Register: %v", err)
	}
	got, ok := reg.Get("planner")
	if !ok || got != s {
		t.Errorf("Get after register = %v, %v; want specialist, true", got, ok)
	}

	// Duplicate name rejected.
	if err := reg.Register(NewSpecialist(RolePlanner, "planner")); err == nil {
		t.Error("Register duplicate name did not error")
	}

	// Empty name rejected.
	if err := reg.Register(NewSpecialist(RolePlanner, "")); err == nil {
		t.Error("Register empty name did not error")
	}

	// ByRole returns sorted specialists.
	reg.Register(NewSpecialist(RoleTester, "tester-b"))
	reg.Register(NewSpecialist(RoleTester, "tester-a"))
	testers := reg.ByRole(RoleTester)
	if len(testers) != 2 {
		t.Fatalf("ByRole(tester) = %d, want 2", len(testers))
	}
	if testers[0].Agent.ID != "tester-a" || testers[1].Agent.ID != "tester-b" {
		t.Errorf("ByRole not sorted: %q, %q", testers[0].Agent.ID, testers[1].Agent.ID)
	}
}

// ---- team.go ----

func TestStandardTeam(t *testing.T) {
	team, runtime, err := StandardTeam()
	if err != nil {
		t.Fatalf("StandardTeam: %v", err)
	}

	// 7 specialists with the correct role/name mapping.
	want := []struct {
		name string
		role Role
	}{
		{"planner", RolePlanner},
		{"architect", RoleArchitect},
		{"coder", RoleCoder},
		{"reviewer", RoleReviewer},
		{"security", RoleSecurity},
		{"tester", RoleTester},
		{"sre", RoleSRE},
	}
	for _, w := range want {
		s, ok := team.Get(w.name)
		if !ok {
			t.Errorf("team missing specialist %q", w.name)
			continue
		}
		if s.Role != w.role {
			t.Errorf("%q.Role = %q, want %q", w.name, s.Role, w.role)
		}
		if len(s.Capabilities) == 0 {
			t.Errorf("specialist %q has no capabilities", w.name)
		}
	}

	// The runtime registry shares the identities.
	if len(runtime.All()) != 7 {
		t.Errorf("runtime has %d agents, want 7", len(runtime.All()))
	}
	for _, w := range want {
		if _, ok := runtime.Get(w.name); !ok {
			t.Errorf("runtime missing agent %q", w.name)
		}
	}
}

// ---- pipeline.go ----

// fixedHandler returns a deterministic output per stage, never touching a model.
func fixedHandler(action string, specialist *Specialist, task *agent.Task) (string, error) {
	return fmt.Sprintf("%s-by-%s", action, specialist.Agent.ID), nil
}

func TestPipelineHappyPath(t *testing.T) {
	team, runtime, err := StandardTeam()
	if err != nil {
		t.Fatalf("StandardTeam: %v", err)
	}
	p := NewPipeline(team, runtime, governance.NewApprovalWorkflow())

	task := agent.NewTask("code", "implement X")
	finalTask, results, err := p.Run(task, fixedHandler)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if finalTask == nil {
		t.Fatal("Run returned nil task")
	}
	if len(results) != 6 {
		t.Fatalf("results = %d, want 6", len(results))
	}
	wantStages := []string{"plan", "architect", "code", "review", "security", "test"}
	// specialist name for each stage == the role name.
	wantSpecialists := []string{"planner", "architect", "coder", "reviewer", "security", "tester"}
	for i, r := range results {
		if !r.OK {
			t.Errorf("stage %d %q not OK", i, r.Stage)
		}
		if r.Stage != wantStages[i] {
			t.Errorf("stage %d = %q, want %q", i, r.Stage, wantStages[i])
		}
		if r.Specialist != wantSpecialists[i] {
			t.Errorf("stage %d specialist = %q, want %q", i, r.Specialist, wantSpecialists[i])
		}
		if r.Output != fmt.Sprintf("%s-by-%s", r.Stage, r.Specialist) {
			t.Errorf("stage %d output = %q, want %q", i, r.Output, fmt.Sprintf("%s-by-%s", r.Stage, r.Specialist))
		}
	}
}

func TestPipelineFailingStage(t *testing.T) {
	team, runtime, err := StandardTeam()
	if err != nil {
		t.Fatalf("StandardTeam: %v", err)
	}
	p := NewPipeline(team, runtime, nil)

	failAt := map[string]bool{"review": true} // stage 4 (0-indexed 3)
	handler := func(action string, specialist *Specialist, task *agent.Task) (string, error) {
		if failAt[action] {
			return "", fmt.Errorf("review failed: %s", specialist.Agent.ID)
		}
		return action + "-ok", nil
	}

	task := agent.NewTask("code", "implement X")
	_, results, err := p.Run(task, handler)
	if err == nil {
		t.Fatal("Run did not error on failing stage")
	}
	if len(results) != 4 {
		t.Fatalf("partial results = %d, want 4 (plan,architect,code,review)", len(results))
	}
	for i, r := range results {
		if r.Stage == "test" {
			t.Errorf("test stage ran before failing review")
		}
		wantOK := i < 3 // first 3 OK, 4th (review) failed
		if r.OK != wantOK {
			t.Errorf("stage %q OK = %v, want %v", r.Stage, r.OK, wantOK)
		}
	}
	if results[3].Stage != "review" || results[3].OK {
		t.Errorf("stage 3 = %+v, want failing review", results[3])
	}
}

func TestPipelineApproval(t *testing.T) {
	team, runtime, err := StandardTeam()
	if err != nil {
		t.Fatalf("StandardTeam: %v", err)
	}
	p := NewPipeline(team, runtime, governance.NewApprovalWorkflow())

	handler := func(action string, specialist *Specialist, task *agent.Task) (string, error) {
		if action == "code" {
			return "", agent.ErrApprovalRequired
		}
		return "ok", nil
	}

	task := agent.NewTask("code", "implement X")
	_, results, err := p.Run(task, handler)
	if !errors.Is(err, agent.ErrApprovalRequired) {
		t.Fatalf("Run error = %v, want agent.ErrApprovalRequired", err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2 (plan,architect), approval stops before code", len(results))
	}
}

func TestPipelineMissingSpecialist(t *testing.T) {
	// Team missing the tester role => pipeline fails at the test stage.
	team := NewSpecialistRegistry()
	team.Register(NewSpecialist(RolePlanner, "planner"))
	team.Register(NewSpecialist(RoleCoder, "coder"))
	p := NewPipeline(team, agent.NewRegistry(), nil)

	task := agent.NewTask("code", "implement X")
	_, _, err := p.Run(task, fixedHandler)
	if err == nil {
		t.Fatal("Run did not error when a stage had no specialist")
	}
}

func TestPipelineNilArgs(t *testing.T) {
	team, runtime, err := StandardTeam()
	if err != nil {
		t.Fatalf("StandardTeam: %v", err)
	}
	p := NewPipeline(team, runtime, nil)

	if _, _, err := p.Run(nil, fixedHandler); err == nil {
		t.Error("Run(nil task) did not error")
	}
	if _, _, err := p.Run(agent.NewTask("code", "x"), nil); err == nil {
		t.Error("Run(nil handler) did not error")
	}
}

func TestNewPipelineNilDefaults(t *testing.T) {
	p := NewPipeline(nil, nil, nil)
	if p == nil {
		t.Fatal("NewPipeline returned nil")
	}
	if p.team == nil || p.runtime == nil || p.approvals == nil {
		t.Error("NewPipeline did not fill nil defaults")
	}
}
