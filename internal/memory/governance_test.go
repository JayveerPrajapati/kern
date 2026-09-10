package memory

import (
	"errors"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

func TestDefaultPolicyPermissive(t *testing.T) {
	ac := NewAccessControl(DefaultPolicy())
	for _, agent := range []string{"", "agent-1", "anyone"} {
		if !ac.CanRead(agent) || !ac.CanWrite(agent) || !ac.CanDelete(agent) {
			t.Fatalf("default policy should allow %q read/write/delete", agent)
		}
	}
}

func TestAccessControlPerAgent(t *testing.T) {
	p := Policy{
		Agents: map[string][]Permission{
			"reader":  {PermissionRead},
			"writer":  {PermissionWrite},
			"deleter": {PermissionDelete},
			"none":    {},
		},
		DefaultPermissions: []Permission{PermissionRead},
	}
	ac := NewAccessControl(p)

	if !ac.CanRead("reader") || ac.CanWrite("reader") || ac.CanDelete("reader") {
		t.Fatalf("reader should have only read")
	}
	if ac.CanRead("writer") || !ac.CanWrite("writer") {
		t.Fatalf("writer should have only write")
	}
	if !ac.CanDelete("deleter") {
		t.Fatalf("deleter should have delete")
	}
	if ac.Can("none", PermissionRead) || ac.Can("none", PermissionWrite) {
		t.Fatalf("explicit empty grant must mean no permissions")
	}
	// Unknown agent falls back to defaults (read only).
	if !ac.CanRead("unknown") || ac.CanWrite("unknown") {
		t.Fatalf("unknown agent should fall back to default permissions")
	}
}

func TestAccessControlAllowAndDenyLists(t *testing.T) {
	p := Policy{
		AllowedAgents:      []string{"agent-a", "agent-b"},
		DeniedAgents:       []string{"agent-b"},
		DefaultPermissions: []Permission{PermissionRead, PermissionWrite, PermissionDelete},
	}
	ac := NewAccessControl(p)
	if !ac.CanRead("agent-a") {
		t.Fatalf("agent-a is allowlisted")
	}
	if ac.CanRead("agent-c") {
		t.Fatalf("agent-c is not allowlisted")
	}
	// DeniedAgents wins even over the allowlist.
	if ac.CanRead("agent-b") {
		t.Fatalf("agent-b is denied")
	}
}

func TestAuthorizeReturnsPermissionError(t *testing.T) {
	ac := NewAccessControl(Policy{DefaultPermissions: []Permission{PermissionRead}})
	err := ac.Authorize("agent-x", PermissionWrite)
	if err == nil {
		t.Fatal("expected denial")
	}
	if !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("errors.Is(err, ErrPermissionDenied) = false, err = %v", err)
	}
	var pe *PermissionError
	if !errors.As(err, &pe) {
		t.Fatalf("expected *PermissionError, got %T", err)
	}
	if pe.Agent != "agent-x" || pe.Permission != PermissionWrite {
		t.Fatalf("unexpected PermissionError: %+v", pe)
	}
	if err := ac.Authorize("agent-x", PermissionRead); err != nil {
		t.Fatalf("read should be allowed: %v", err)
	}
}

func TestGovernedStoreEnforcesWritePermission(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	gov := &Governance{
		Access:    NewAccessControl(Policy{DefaultPermissions: []Permission{PermissionRead}}),
		Audit:     NewAuditTrail(),
		Retention: DefaultRetention(),
	}
	s := NewMemoryStore(t.TempDir()).WithGovernance(gov)

	m := domain.Memory{Content: "secret plan", Type: domain.MemoryDecision, Source: "agent-x"}
	if _, err := s.Add(m); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("Add without write permission: got %v, want ErrPermissionDenied", err)
	}
	if got, _ := s.List(""); len(got) != 0 {
		t.Fatalf("denied Add must not store anything, got %d memories", len(got))
	}

	// An explicit writer grant succeeds and is stored.
	gov.Access = NewAccessControl(Policy{
		Agents:             map[string][]Permission{"writer": {PermissionRead, PermissionWrite}},
		DefaultPermissions: []Permission{PermissionRead},
	})
	m.Source = "writer"
	added, err := s.Add(m)
	if err != nil {
		t.Fatalf("writer Add failed: %v", err)
	}
	if added.ID == "" {
		t.Fatal("expected ID on stored memory")
	}
}

func TestGovernedStoreEnforcesReadPermission(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	gov := &Governance{
		Access:    NewAccessControl(Policy{Agents: map[string][]Permission{"admin": {PermissionRead, PermissionWrite}}}),
		Audit:     NewAuditTrail(),
		Retention: DefaultRetention(),
	}
	s := NewMemoryStore(t.TempDir()).WithGovernance(gov)

	if _, err := s.AuthorizedRecall(Query{Text: "x"}, "spy", 0); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("recall without read permission: got %v, want ErrPermissionDenied", err)
	}
	// Denied recall is still audited.
	denied := gov.Audit.FilterByOperation(OpRecall)
	if len(denied) != 1 || denied[0].Allowed {
		t.Fatalf("expected one denied recall audit event, got %+v", denied)
	}
	// Admin can recall (empty store returns no memories, no error).
	if _, err := s.AuthorizedRecall(Query{Text: "x"}, "admin", 3); err != nil {
		t.Fatalf("admin recall failed: %v", err)
	}
}

func TestGovernedStoreEnforcesDeletePermission(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	gov := &Governance{
		Access:    NewAccessControl(Policy{Agents: map[string][]Permission{"writer": {PermissionRead, PermissionWrite}}}),
		Audit:     NewAuditTrail(),
		Retention: DefaultRetention(),
	}
	s := NewMemoryStore(t.TempDir()).WithGovernance(gov)

	if err := s.Delete("some-id"); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("Delete without delete permission: got %v, want ErrPermissionDenied", err)
	}
	if _, err := s.Update("some-id", "new", nil); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("Update without write permission: got %v, want ErrPermissionDenied", err)
	}
}

func TestUnGovernedStorePreservesLegacyBehavior(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	s := NewMemoryStore(t.TempDir()) // no governance attached

	m, err := s.Add(domain.Memory{Content: "legacy lesson", Type: domain.MemoryLesson})
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	if err := s.Delete(m.ID); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	// AuthorizedRecall without governance is a plain recall.
	if _, err := s.AuthorizedRecall(Query{Text: "legacy"}, "any-agent", 0); err != nil {
		t.Fatalf("AuthorizedRecall failed without governance: %v", err)
	}
	if s.Governance() != nil {
		t.Fatal("Governance() should be nil for legacy store")
	}
}

func TestWithEnvGovernanceGate(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("KERN_MEMORY_GOVERNANCE", "")
	t.Setenv("KERN_MEMORY_POLICY", "")
	t.Setenv("KERN_MEMORY_RETENTION", "")
	t.Setenv("KERN_MEMORY_AUDIT_DIR", t.TempDir())

	s := WithEnvGovernance(NewMemoryStore(t.TempDir()), t.TempDir())
	if s.Governance() != nil {
		t.Fatal("governance must not attach when KERN_MEMORY_GOVERNANCE is unset")
	}

	t.Setenv("KERN_MEMORY_GOVERNANCE", "1")
	s2 := WithEnvGovernance(NewMemoryStore(t.TempDir()), t.TempDir())
	if s2.Governance() == nil {
		t.Fatal("governance must attach when KERN_MEMORY_GOVERNANCE=1")
	}
	// Default policy is permissive: a governed store still accepts writes.
	if _, err := s2.Add(domain.Memory{Content: "ok", Type: domain.MemoryLesson, Source: "agent"}); err != nil {
		t.Fatalf("governed Add with default policy failed: %v", err)
	}
}

func TestNewGovernanceFallsBackOnBadConfig(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("KERN_MEMORY_POLICY", "{not json")
	t.Setenv("KERN_MEMORY_RETENTION", "{also not json")
	t.Setenv("KERN_MEMORY_AUDIT_DIR", t.TempDir())

	gov := NewGovernance(t.TempDir())
	if gov == nil || gov.Access == nil || gov.Audit == nil {
		t.Fatal("NewGovernance must never return a broken layer")
	}
	if !gov.Access.CanRead("anyone") || !gov.Access.CanWrite("anyone") {
		t.Fatal("bad policy config must fall back to permissive default")
	}
}
