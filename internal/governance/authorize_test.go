package governance

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/index"
)

// writeFile writes a test fixture file under dir, creating parent dirs.
func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(p), err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

// TestAuthorizeContext_DeniedPathExcludesSymbols is the P0.1 DoD gate: a
// registered agent with the context.read permission, a task scope that denies
// secret/, must see PublicA but never SecretB — and SecretB must appear in the
// Denied list with Stage == "path".
func TestAuthorizeContext_DeniedPathExcludesSymbols(t *testing.T) {
	agent := NewAgent("restricted-bot", "Restricted Bot", "planner", []Permission{
		{Resource: "context", Action: "read"},
	})
	if err := RegisterAgent(agent); err != nil {
		t.Fatalf("register agent: %v", err)
	}
	fw := NewFirewall().WithAgents(agent)

	dir := t.TempDir()
	writeFile(t, dir, "public/a.go", `package public

func PublicA() int { return 1 }
`)
	writeFile(t, dir, "secret/b.go", `package secret

func SecretB() string { return "hidden" }
`)
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatalf("build index: %v", err)
	}

	req := Request{
		Task:    "T-1",
		AgentID: "restricted-bot",
		Scope: &domain.TaskScope{
			TaskID:      "T-1",
			DeniedPaths: []string{"secret/"},
		},
		Root: dir,
	}
	resp, err := AuthorizeContext(req, ix, fw)
	if err != nil {
		t.Fatalf("AuthorizeContext: %v", err)
	}
	if !resp.Proof.Decision.Allowed {
		t.Fatalf("expected decision allowed, got %+v", resp.Proof.Decision)
	}

	foundPublic, foundSecret := false, false
	for _, s := range resp.Scope.Symbols {
		switch s.Name {
		case "PublicA":
			foundPublic = true
		case "SecretB":
			foundSecret = true
		}
	}
	if !foundPublic {
		t.Errorf("expected PublicA in allowed scope symbols")
	}
	if foundSecret {
		t.Errorf("SecretB must NOT be in allowed scope symbols")
	}

	var deniedSecret *DeniedSymbol
	for i := range resp.Scope.Denied {
		if resp.Scope.Denied[i].Symbol.Name == "SecretB" {
			deniedSecret = &resp.Scope.Denied[i]
			break
		}
	}
	if deniedSecret == nil {
		t.Fatalf("expected SecretB in scope.Denied, got %d denied entries", len(resp.Scope.Denied))
	}
	if deniedSecret.Stage != "path" {
		t.Errorf("expected denied stage %q, got %q", "path", deniedSecret.Stage)
	}
	if resp.Proof.Fingerprint == "" {
		t.Errorf("expected non-empty fingerprint")
	}
}

// TestAuthorizeContext_UnknownAgentDenied: an agent that is not registered
// must be denied at the authentication stage with a non-nil auditable proof
// and no scope symbols.
func TestAuthorizeContext_UnknownAgentDenied(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.go", `package main

func X() {}
`)
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatalf("build index: %v", err)
	}
	fw := NewFirewall() // no agents registered

	req := Request{Task: "T-2", AgentID: "ghost", Root: dir}
	resp, err := AuthorizeContext(req, ix, fw)
	if err != ErrUnauthorized {
		t.Fatalf("expected ErrUnauthorized, got %v", err)
	}
	if resp.Proof.Decision.Allowed {
		t.Fatalf("expected denied decision")
	}
	if resp.Proof.Decision.Deny == nil || resp.Proof.Decision.Deny.Stage != "authentication" {
		t.Fatalf("expected deny stage %q, got %+v", "authentication", resp.Proof.Decision.Deny)
	}
	if resp.Scope.Symbols != nil {
		t.Errorf("expected nil scope symbols, got %d", len(resp.Scope.Symbols))
	}
	if resp.Proof.Fingerprint == "" {
		t.Errorf("expected non-empty fingerprint")
	}
}

// TestAuthorizeContext_FingerprintStable: identical requests on the same index
// must produce identical fingerprints; mutating the scope symbols (the allowed
// symbol set the decision was computed over) must change the fingerprint.
func TestAuthorizeContext_FingerprintStable(t *testing.T) {
	agent := NewAgent("stable-bot", "Stable Bot", "reviewer", []Permission{
		{Resource: "context", Action: "read"},
	})
	if err := RegisterAgent(agent); err != nil {
		t.Fatalf("register agent: %v", err)
	}
	fw := NewFirewall().WithAgents(agent)

	dir := t.TempDir()
	writeFile(t, dir, "a.go", `package main

func A() {}
func B() {}
`)
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatalf("build index: %v", err)
	}

	req := Request{Task: "T-3", AgentID: "stable-bot", Root: dir}
	r1, err := AuthorizeContext(req, ix, fw)
	if err != nil {
		t.Fatalf("AuthorizeContext (1): %v", err)
	}
	r2, err := AuthorizeContext(req, ix, fw)
	if err != nil {
		t.Fatalf("AuthorizeContext (2): %v", err)
	}
	if r1.Proof.Fingerprint != r2.Proof.Fingerprint {
		t.Errorf("identical requests must produce identical fingerprints: %q vs %q",
			r1.Proof.Fingerprint, r2.Proof.Fingerprint)
	}

	// Mutate the scope symbols: a new allowed symbol changes the set the
	// fingerprint is computed over, so recomputing must differ.
	ix.Symbols = append(ix.Symbols, index.Symbol{
		Kind: "func", Name: "C", File: "a.go", Line: 10, Lang: "go",
	})
	r3, err := AuthorizeContext(req, ix, fw)
	if err != nil {
		t.Fatalf("AuthorizeContext (3): %v", err)
	}
	if r1.Proof.Fingerprint == r3.Proof.Fingerprint {
		t.Errorf("mutating scope symbols must change the fingerprint")
	}
}

// TestEffectiveScope_DefaultIsCwdScoped: a request without an explicit scope
// gets the cwd-scoped default — Paths confined to the request root — instead
// of the old all-paths permissive default.
func TestEffectiveScope_DefaultIsCwdScoped(t *testing.T) {
	req := Request{Task: "T-7", Root: "/tmp/project"}
	sc := effectiveScope(req)
	if sc == nil {
		t.Fatal("effectiveScope must return a non-nil scope")
	}
	if len(sc.Paths) != 1 || sc.Paths[0] != "/tmp/project" {
		t.Fatalf("default scope must confine Paths to the request root, got %v", sc.Paths)
	}
}

// TestEffectiveScope_PermissiveWhenExplicit: an explicitly supplied scope is
// returned as-is — empty Paths still means all paths (explicit user intent,
// not the default).
func TestEffectiveScope_PermissiveWhenExplicit(t *testing.T) {
	explicit := &domain.TaskScope{TaskID: "T-8"}
	req := Request{Task: "T-8", Root: "/tmp/project", Scope: explicit}
	sc := effectiveScope(req)
	if sc != explicit {
		t.Fatal("effectiveScope must return the explicit scope unchanged")
	}
	if len(sc.Paths) != 0 {
		t.Fatalf("explicit scope with empty Paths must stay all-paths, got %v", sc.Paths)
	}
}

// TestEffectiveScopeFailClosedNoRootNoScope: a request with neither an
// explicit scope nor a root must deny every symbol (fail closed, M1) instead
// of falling back to an empty-Paths scope that allows everything.
func TestEffectiveScopeFailClosedNoRootNoScope(t *testing.T) {
	agent := NewAgent("failclosed-bot", "Fail Closed Bot", "planner", []Permission{
		{Resource: "context", Action: "read"},
	})
	if err := RegisterAgent(agent); err != nil {
		t.Fatalf("register agent: %v", err)
	}
	fw := NewFirewall().WithAgents(agent)

	dir := t.TempDir()
	writeFile(t, dir, "a.go", `package main

func A() int { return 1 }
`)
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatalf("build index: %v", err)
	}

	// No Scope, no Root: effectiveScope must be nil...
	sc := effectiveScope(Request{Task: "T-9", AgentID: "failclosed-bot"})
	if sc != nil {
		t.Fatalf("effectiveScope must return nil with no scope and no root, got %+v", sc)
	}

	// ...and AuthorizeContext must deny the request outright.
	req := Request{Task: "T-9", AgentID: "failclosed-bot"}
	resp, err := AuthorizeContext(req, ix, fw)
	if err != ErrUnauthorized {
		t.Fatalf("expected ErrUnauthorized, got %v", err)
	}
	if resp.Proof.Decision.Allowed {
		t.Fatal("expected denied decision for request with no scope and no root")
	}
	if resp.Proof.Decision.Deny == nil || resp.Proof.Decision.Deny.Stage != "path" {
		t.Fatalf("expected deny at path stage, got %+v", resp.Proof.Decision.Deny)
	}
	if resp.Proof.Decision.Deny.Reason != "no task scope and no root: cannot authorize without a scope" {
		t.Fatalf("unexpected deny reason: %q", resp.Proof.Decision.Deny.Reason)
	}
	if resp.Scope.Symbols != nil {
		t.Fatalf("expected nil scope symbols on fail-closed denial, got %d", len(resp.Scope.Symbols))
	}
}
