package service

import (
	"testing"
)

// compile-time interface assertions: the concrete implementations must
// satisfy the service interfaces.
var (
	_ IndexService      = (*indexService)(nil)
	_ GraphService      = (*graphService)(nil)
	_ MemoryService     = (*memoryService)(nil)
	_ GovernanceService = (*governanceService)(nil)
	_ SecurityService   = (*securityService)(nil)
)

// TestNewWiresAllServices verifies that New returns a fully populated service
// layer: every service is non-nil and implements its interface.
func TestNewWiresAllServices(t *testing.T) {
	svc := New()
	if svc == nil {
		t.Fatal("New() returned nil")
	}
	if svc.Index == nil {
		t.Error("Index service not wired")
	}
	if svc.Graph == nil {
		t.Error("Graph service not wired")
	}
	if svc.Memory == nil {
		t.Error("Memory service not wired")
	}
	if svc.Governance == nil {
		t.Error("Governance service not wired")
	}
	if svc.Security == nil {
		t.Error("Security service not wired")
	}
}
