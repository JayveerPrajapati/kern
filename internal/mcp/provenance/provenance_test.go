package provenance

import (
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/index"
)

// The mcp package's provenance_test.go pins the wire contract through the
// *Server wrappers; these tests pin the leaf's own API directly — the
// contract future handler families depend on when they import this package.

func TestIndexIdentityNilIndex(t *testing.T) {
	p := IndexIdentity(nil, func(string) string { return "abc123" })
	if p.FreshnessVerdict != string(index.FreshnessUnknown) {
		t.Fatalf("nil index verdict = %q, want unknown", p.FreshnessVerdict)
	}
	if p.GitCommit != "" {
		t.Fatalf("nil index commit = %q, want empty", p.GitCommit)
	}
}

func TestIndexIdentityCommitFallback(t *testing.T) {
	ix := &index.Index{Root: "/tmp/kern-fixture"}
	var gotRoot string
	p := IndexIdentity(ix, func(root string) string {
		gotRoot = root
		return "deadbee"
	})
	if gotRoot != "/tmp/kern-fixture" {
		t.Fatalf("commit resolver called with %q", gotRoot)
	}
	if p.GitCommit != "deadbee" {
		t.Fatalf("GitCommit = %q, want deadbee", p.GitCommit)
	}
}

func TestRawProvenance(t *testing.T) {
	p := Raw(nil, func(string) string { return "abc123" }, nil)
	if p.SchemaVersion != SchemaVersion {
		t.Fatalf("SchemaVersion = %d, want %d", p.SchemaVersion, SchemaVersion)
	}
	if p.Mode != ProvenanceModeRaw {
		t.Fatalf("Mode = %q, want raw", p.Mode)
	}
	if p.AuthorizingRule != nil {
		t.Fatal("raw provenance must carry no authorizing rule")
	}
	if p.Symbols == nil || len(p.Symbols) != 0 {
		t.Fatalf("nil symbols must normalize to empty non-nil, got %#v", p.Symbols)
	}
}

func TestGovernedPolicyMapping(t *testing.T) {
	allowed := func() governance.AuthorizationProof {
		return governance.AuthorizationProof{
			Fingerprint: "sha256-fixture",
			DecidedAt:   time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC),
			Decision:    domain.GatewayResult{Allowed: true},
		}
	}
	t.Run("task scope maps to deny-unlisted", func(t *testing.T) {
		p := Governed(nil, func(string) string { return "abc123" }, PolicySourceTaskScope, allowed(), nil)
		if p.Mode != ProvenanceModeGoverned {
			t.Fatalf("Mode = %q", p.Mode)
		}
		if p.AuthorizingRule.Policy != "deny-unlisted" {
			t.Fatalf("policy = %q, want deny-unlisted", p.AuthorizingRule.Policy)
		}
		if p.AuthorizingRule.Fingerprint != "sha256-fixture" || p.AuthorizingRule.DecidedAt != "2026-09-18T12:00:00Z" {
			t.Fatalf("rule = %+v", p.AuthorizingRule)
		}
	})
	t.Run("permissive stays permissive", func(t *testing.T) {
		p := Governed(nil, func(string) string { return "abc123" }, PolicySourcePermissive, allowed(), nil)
		if p.AuthorizingRule.Policy != "permissive-default" {
			t.Fatalf("policy = %q, want permissive-default", p.AuthorizingRule.Policy)
		}
	})
	t.Run("denial carries the denying policy id", func(t *testing.T) {
		proof := allowed()
		proof.Decision.Allowed = false
		proof.Decision.Deny = &domain.DenyReason{Policy: "firewall.permission"}
		p := Governed(nil, func(string) string { return "abc123" }, PolicySourceTaskScope, proof, nil)
		if p.AuthorizingRule.Policy != "firewall.permission" {
			t.Fatalf("policy = %q, want firewall.permission", p.AuthorizingRule.Policy)
		}
	})
}

func TestSummary(t *testing.T) {
	if got := Summary(nil, &Provenance{}); got != "" {
		t.Fatalf("nil index summary = %q, want empty", got)
	}
	ix := &index.Index{
		Symbols:   []index.Symbol{{Name: "A"}, {Name: "B"}},
		Calls:     map[string][]index.CallEdge{"A": {{}, {}}, "B": {{}}},
		Pkgs:      map[string]*index.Pkg{"pkg1": {}, "pkg2": {}},
		UpdatedAt: time.Now(),
	}
	p := &Provenance{Index: IndexProvenance{GitCommit: "abc1234", FreshnessVerdict: "fresh"}}
	got := Summary(ix, p)
	for _, want := range []string{"2 symbols", "3 call edges", "2 packages", "commit abc1234", "fresh"} {
		if !strings.Contains(got, want) {
			t.Fatalf("summary %q missing %q", got, want)
		}
	}
}

func TestSymbolProvenances(t *testing.T) {
	// Zero-value index: nothing resolves, exercising the unresolved-callee
	// passthrough with SimpleName applied, dedup, empty-name skip, sorting.
	got := SymbolProvenances(&index.Index{}, []string{"pkg.Do", "pkg.Do", "", "X"})
	if len(got) != 2 {
		t.Fatalf("got %d records, want 2 (dedup + empty skip): %#v", len(got), got)
	}
	if got[0].Qualified != "X" || got[0].Name != "X" {
		t.Fatalf("sorted first = %+v", got[0])
	}
	if got[1].Qualified != "pkg.Do" || got[1].Name != "Do" {
		t.Fatalf("second = %+v", got[1])
	}
}

func TestSimpleName(t *testing.T) {
	for in, want := range map[string]string{
		"pkg.Func": "Func",
		"Func":     "Func",
		"":         "",
		"a.b.C":    "C",
	} {
		if got := SimpleName(in); got != want {
			t.Fatalf("SimpleName(%q) = %q, want %q", in, got, want)
		}
	}
}
