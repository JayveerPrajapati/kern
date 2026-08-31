package context

import (
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance"
)

// TestAuthorizeItemsScopedAllDimensions verifies the scoped
// authorization covers all five dimensions: agent (firewall), repository,
// task, tenant, and security classification. An item must pass every dimension
// to survive; a mismatched dimension excludes it with a precise DenyReason.
func TestAuthorizeItemsScopedAllDimensions(t *testing.T) {
	// Agent may read "context" but NOT "source", so a FACT item (which maps to
	// the "source" resource) is denied by the agent/firewall dimension.
	agent := governance.NewAgent("a1", "coder", "code",
		[]governance.Permission{{Resource: "context", Action: "read"}})
	fw := governance.NewFirewall().WithAgents(agent)

	auth := domain.ContextAuthorization{
		Agent:                  "a1",
		Repository:             "repo",
		TaskID:                 "task-1",
		Tenant:                 "acme",
		AllowedTenants:         []string{"acme", "globex"},
		AllowedSecurityClasses: []string{"public", "internal"},
	}

	items := []domain.ContextItem{
		{
			ID: "ok", Class: domain.ContextMemory, Content: "m1",
			Repository: "repo", TaskID: "task-1", Tenant: "acme", SecurityClass: "internal",
		},
		{
			ID: "fw-denied", Class: domain.ContextFact, Content: "f1",
			// Fact maps to "source"; agent lacks source:read → denied by firewall.
			Repository: "repo", TaskID: "task-1", Tenant: "acme", SecurityClass: "internal",
		},
		{
			ID: "repo-denied", Class: domain.ContextMemory, Content: "m2",
			Repository: "other", TaskID: "task-1", Tenant: "acme", SecurityClass: "internal",
		},
		{
			ID: "task-denied", Class: domain.ContextMemory, Content: "m3",
			Repository: "repo", TaskID: "task-9", Tenant: "acme", SecurityClass: "internal",
		},
		{
			ID: "tenant-denied", Class: domain.ContextMemory, Content: "m4",
			Repository: "repo", TaskID: "task-1", Tenant: "hooli", SecurityClass: "internal",
		},
		{
			ID: "sec-denied", Class: domain.ContextMemory, Content: "m5",
			Repository: "repo", TaskID: "task-1", Tenant: "acme", SecurityClass: "topsecret",
		},
	}

	out := AuthorizeItemsScoped(items, fw, auth)

	// Only the fully matching item survives.
	if len(out) != 1 {
		t.Fatalf("AuthorizeItemsScoped kept %d items, want 1; got %+v", len(out), out)
	}
	if out[0].ID != "ok" {
		t.Errorf("kept %q, want only the fully-authorized 'ok'", out[0].ID)
	}
	if !out[0].Authorized {
		t.Errorf("'ok' should be Authorized=true")
	}

	// Assert each denied item was excluded (dropped from the returned set) with
	// the correct DenyReason recorded in place on the input slice.
	reasons := map[string]string{
		"fw-denied":     "denied by governance firewall",
		"repo-denied":   "repository scope denied: other",
		"task-denied":   "task scope denied",
		"tenant-denied": "tenant denied",
		"sec-denied":    "security class denied",
	}
	for _, item := range items {
		if item.ID == "ok" {
			continue
		}
		if item.Authorized {
			t.Errorf("%s should be Authorized=false", item.ID)
		}
		if want, ok := reasons[item.ID]; ok && item.DenyReason != want {
			t.Errorf("%s DenyReason = %q, want %q", item.ID, item.DenyReason, want)
		}
	}
}

// TestAuthorizeItemsScopedNilFirewall verifies a nil firewall skips only the
// agent dimension while still enforcing the scoped dimensions.
func TestAuthorizeItemsScopedNilFirewall(t *testing.T) {
	auth := domain.ContextAuthorization{Repository: "repo", Tenant: "acme"}
	items := []domain.ContextItem{
		{ID: "keep", Class: domain.ContextFact, Content: "a", Repository: "repo", Tenant: "acme"},
		{ID: "repo-denied", Class: domain.ContextFact, Content: "b", Repository: "other", Tenant: "acme"},
		{ID: "tenant-denied", Class: domain.ContextFact, Content: "c", Repository: "repo", Tenant: "hooli"},
	}
	out := AuthorizeItemsScoped(items, nil, auth)
	if len(out) != 1 || out[0].ID != "keep" {
		t.Fatalf("nil-fw scoped auth kept %+v, want only 'keep'", out)
	}
}

// TestCanonicalizeItemsEvidenceRefs verifies the canonical-fact dedup model:
// several items sharing a digest collapse to ONE canonical item whose
// EvidenceRefs holds the duplicate IDs, distinct digest-less items are
// preserved, and DedupItems still works unchanged.
func TestCanonicalizeItemsEvidenceRefs(t *testing.T) {
	items := []domain.ContextItem{
		{ID: "c1", Class: domain.ContextFact, Content: "fact one", Digest: "sha-1"},
		{ID: "e1", Class: domain.ContextEvidence, Content: "fact one", Digest: "sha-1"},
		{ID: "e2", Class: domain.ContextEvidence, Content: "fact one", Digest: "sha-1"},
		{ID: "distinct", Class: domain.ContextFact, Content: "no digest"},
	}

	out := CanonicalizeItems(items)

	// Canonical first (c1), then distinct digest-less item.
	if len(out) != 2 {
		t.Fatalf("CanonicalizeItems len = %d, want 2; got %+v", len(out), out)
	}
	if out[0].ID != "c1" || out[1].ID != "distinct" {
		t.Fatalf("CanonicalizeItems order = [%q, %q], want [c1, distinct]", out[0].ID, out[1].ID)
	}
	// Evidence refs carry the duplicate IDs in encounter order.
	if len(out[0].EvidenceRefs) != 2 {
		t.Fatalf("canonical EvidenceRefs = %v, want [e1 e2]", out[0].EvidenceRefs)
	}
	if out[0].EvidenceRefs[0] != "e1" || out[0].EvidenceRefs[1] != "e2" {
		t.Errorf("canonical EvidenceRefs = %v, want [e1 e2]", out[0].EvidenceRefs)
	}
	// The distinct item is preserved unchanged (no evidence refs).
	if len(out[1].EvidenceRefs) != 0 {
		t.Errorf("distinct item should have no EvidenceRefs, got %v", out[1].EvidenceRefs)
	}

	// DedupItems still works (drops duplicates, keeps first occurrences).
	if ded := DedupItems(items); len(ded) != 2 {
		t.Errorf("DedupItems len = %d, want 2", len(ded))
	}
}

// TestSelectContextUsesScopedAuth verifies the SelectContext path authorizes a
// scoped mismatched item away via the scoped authorization wired in select.go.
func TestSelectContextUsesScopedAuth(t *testing.T) {
	agent := governance.NewAgent("a1", "coder", "code",
		[]governance.Permission{{Resource: "context", Action: "read"}})
	fw := governance.NewFirewall().WithAgents(agent)

	items := []domain.ContextItem{
		{ID: "match", Class: domain.ContextMemory, Content: "m", Relevance: 0.9,
			Repository: "repo", Tenant: "acme"},
		{ID: "mismatch", Class: domain.ContextMemory, Content: "m", Relevance: 0.9,
			Repository: "other", Tenant: "acme"},
	}

	req := SelectRequest{
		Items:          items,
		Firewall:       fw,
		Holder:         "a1",
		Repository:     "repo",
		AllowedTenants: []string{"acme"},
		Threshold:      0,
	}
	got := SelectContext(req)
	if len(got) != 1 {
		t.Fatalf("SelectContext kept %d items, want 1 (mismatch filtered); got %+v", len(got), got)
	}
	if got[0].ID != "match" {
		t.Errorf("SelectContext kept %q, want only 'match'", got[0].ID)
	}
}
