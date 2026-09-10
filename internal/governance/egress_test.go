package governance

import (
	"strings"
	"testing"
)

func TestCheckEgressLocalOnly(t *testing.T) {
	rule := EgressRule{Policy: EgressLocalOnly}
	allowed := []struct {
		host string
		port int
	}{
		{"localhost", 80}, {"127.0.0.1", 80}, {"::1", 80}, {"192.168.1.5", 443},
		{"10.0.0.1", 443}, {"172.16.0.1", 443}, {"169.254.1.1", 53}, {"localhost.localdomain", 80},
	}
	for _, tc := range allowed {
		dec := CheckEgress(rule, tc.host, tc.port)
		if !dec.Allowed || dec.RequiresApproval {
			t.Errorf("CheckEgress(local-only, %q:%d) = %+v, want allowed", tc.host, tc.port, dec)
		}
	}
	denied := []struct {
		host string
		port int
	}{
		{"8.8.8.8", 443}, {"example.com", 443},
		// Empty, single-label and .local names are NOT local (they may resolve
		// to routable external IPs under DNS search domains) — AUD-02.
		{"", 0}, {"myhost", 80}, {"kern.local", 80}, {"MyHost", 80},
	}
	for _, tc := range denied {
		dec := CheckEgress(rule, tc.host, tc.port)
		if dec.Allowed {
			t.Errorf("CheckEgress(local-only, %q:%d) allowed, want denied", tc.host, tc.port)
		}
		if !strings.Contains(dec.Reason, "local-only") {
			t.Errorf("CheckEgress(local-only, %q:%d) reason %q should mention local-only policy", tc.host, tc.port, dec.Reason)
		}
	}
	if dec := CheckEgress(rule, "127.0.0.1", -1); dec.Allowed {
		t.Error("negative port should be denied")
	}
}

func TestCheckEgressHostPortAllowlist(t *testing.T) {
	rule := EgressRule{Policy: EgressExternalRedacted, Hosts: []string{"api.example.com"}, Ports: []int{443}}
	if dec := CheckEgress(rule, "api.example.com", 443); !dec.Allowed || dec.RequiresApproval {
		t.Errorf("matching host+port should be allowed, got %+v", dec)
	}
	if dec := CheckEgress(rule, "localhost", 443); dec.Allowed {
		t.Errorf("non-allowlisted host should be denied even if local, got %+v", dec)
	}
	if dec := CheckEgress(rule, "api.example.com", 80); dec.Allowed {
		t.Errorf("non-allowlisted port should be denied, got %+v", dec)
	}
}

func TestCheckEgressExternalRedacted(t *testing.T) {
	rule := EgressRule{Policy: EgressExternalRedacted}
	if dec := CheckEgress(rule, "8.8.8.8", 443); !dec.Allowed || dec.RequiresApproval {
		t.Errorf("external target should be allowed with redaction, got %+v", dec)
	}
	if dec := CheckEgress(rule, "127.0.0.1", 80); !dec.Allowed || dec.RequiresApproval {
		t.Errorf("local target should be allowed, got %+v", dec)
	}
}

func TestCheckEgressExternalApproved(t *testing.T) {
	rule := EgressRule{Policy: EgressExternalApproved}
	if dec := CheckEgress(rule, "127.0.0.1", 80); !dec.Allowed || dec.RequiresApproval {
		t.Errorf("local target should be allowed without approval, got %+v", dec)
	}
	dec := CheckEgress(rule, "8.8.8.8", 443)
	if dec.Allowed || !dec.RequiresApproval {
		t.Errorf("external target should require approval, got %+v", dec)
	}
}

func TestIsLocalHost(t *testing.T) {
	table := []struct {
		host string
		want bool
	}{
		{"localhost", true}, {"LOCALHOST", true}, {"localhost.localdomain", true},
		{"127.0.0.1", true}, {"::1", true}, {"10.0.0.1", true},
		{"172.16.0.1", true}, {"192.168.1.1", true}, {"169.254.1.1", true}, {"0.0.0.0", true},
		// Empty, single-label and .local names are NOT local (AUD-02): they can
		// resolve to routable external IPs under DNS search domains.
		{"", false}, {"myhost", false}, {"kern.local", false}, {"MyHost", false},
		{"8.8.8.8", false}, {"example.com", false}, {"1.2.3.4", false}, {"localhost.evil.com", false},
	}
	for _, tc := range table {
		if got := IsLocalHost(tc.host); got != tc.want {
			t.Errorf("IsLocalHost(%q) = %v, want %v", tc.host, got, tc.want)
		}
	}
}

func TestParseEgressTarget(t *testing.T) {
	table := []struct {
		resource string
		host     string
		port     int
	}{
		{"8.8.8.8:443", "8.8.8.8", 443},
		{"127.0.0.1:80", "127.0.0.1", 80},
		{"[::1]:443", "::1", 443},
		{"8.8.8.8", "8.8.8.8", 0},
		{"example.com", "example.com", 0},
		// SplitHostPort wins: "egress:8.8.8.8" parses as host "egress" with a
		// non-numeric port, which falls back to port 0 ("any port").
		{"egress:8.8.8.8", "egress", 0},
		{"example.com:notaport", "example.com", 0},
	}
	for _, tc := range table {
		host, port := parseEgressTarget(tc.resource)
		if host != tc.host || port != tc.port {
			t.Errorf("parseEgressTarget(%q) = (%q, %d), want (%q, %d)", tc.resource, host, port, tc.host, tc.port)
		}
	}
}

func TestFirewallCheckEgress(t *testing.T) {
	egressAgent := func(id string) *AgentIdentity {
		return NewAgent(id, "Egress Agent", "coder", []Permission{{Resource: "egress", Action: "connect"}})
	}

	t.Run("unknown agent denied and audited", func(t *testing.T) {
		f := NewFirewall().WithAgents(egressAgent("egress-agent"))
		allowed, _, _, err := f.CheckEgress("ghost", "127.0.0.1", 80)
		if err == nil || allowed {
			t.Fatalf("unknown agent should be denied, allowed=%v err=%v", allowed, err)
		}
		entries := f.AuditLog().All()
		if len(entries) != 1 || entries[0].Result != "denied" || entries[0].Policy != "egress" {
			t.Errorf("expected one denied egress audit entry, got %+v", entries)
		}
	})

	t.Run("agent without egress permission denied", func(t *testing.T) {
		f := NewFirewall().WithAgents(NewAgent("no-perm", "NoPerm", "coder", nil))
		allowed, _, _, err := f.CheckEgress("no-perm", "127.0.0.1", 80)
		if err == nil || allowed {
			t.Fatalf("agent without egress permission should be denied, allowed=%v err=%v", allowed, err)
		}
		if !strings.Contains(err.Error(), "lacks permission") {
			t.Errorf("error should mention lacks permission, got %q", err.Error())
		}
	})

	t.Run("local-only rule allows local target", func(t *testing.T) {
		f := NewFirewall().WithAgents(egressAgent("egress-agent")).WithEgressRule(EgressRule{Policy: EgressLocalOnly})
		allowed, _, _, err := f.CheckEgress("egress-agent", "localhost", 80)
		if err != nil || !allowed {
			t.Fatalf("local target should be allowed, allowed=%v err=%v", allowed, err)
		}
		entries := f.AuditLog().All()
		if len(entries) != 1 || entries[0].Result != "allowed" || entries[0].Policy != "egress" {
			t.Errorf("expected allowed egress audit entry, got %+v", entries)
		}
	})

	t.Run("local-only rule denies external target", func(t *testing.T) {
		f := NewFirewall().WithAgents(egressAgent("egress-agent")).WithEgressRule(EgressRule{Policy: EgressLocalOnly})
		allowed, _, _, err := f.CheckEgress("egress-agent", "8.8.8.8", 443)
		if err == nil || allowed {
			t.Fatalf("external target should be denied, allowed=%v err=%v", allowed, err)
		}
		if !strings.Contains(err.Error(), "egress to 8.8.8.8:443") {
			t.Errorf("error should mention the target, got %q", err.Error())
		}
	})

	t.Run("external-approved rule requests approval then allows", func(t *testing.T) {
		f := NewFirewall().WithAgents(egressAgent("egress-agent")).WithEgressRule(EgressRule{Policy: EgressExternalApproved})
		allowed, _, appr, err := f.CheckEgress("egress-agent", "8.8.8.8", 443)
		if err != nil {
			t.Fatalf("approval request should not error: %v", err)
		}
		if allowed || appr == nil {
			t.Fatalf("external target should request approval, allowed=%v appr=%+v", allowed, appr)
		}
		entries := f.AuditLog().All()
		if len(entries) != 1 || entries[0].Result != "pending" || entries[0].Policy != "egress" {
			t.Errorf("expected pending egress audit entry, got %+v", entries)
		}
		if err := f.ApproveAction(appr.ID, "human"); err != nil {
			t.Fatalf("ApproveAction: %v", err)
		}
		allowed, dec, _, err := f.CheckEgress("egress-agent", "8.8.8.8", 443)
		if err != nil || !allowed {
			t.Fatalf("approved external target should be allowed, allowed=%v err=%v", allowed, err)
		}
		if !dec.Allowed {
			t.Errorf("decision should report allowed after approval, got %+v", dec)
		}
	})
}

func TestCheckEgressStepInCheck(t *testing.T) {
	agent := NewAgent("egress-step", "Egress Step", "coder", []Permission{
		{Resource: "8.8.8.8:443", Action: "egress"},
		{Resource: "127.0.0.1:443", Action: "egress"},
		{Resource: "tests", Action: "write"},
	})

	t.Run("local-only rule", func(t *testing.T) {
		f := NewFirewall().WithAgents(agent).WithEgressRule(EgressRule{Policy: EgressLocalOnly})
		// External target denied by the egress step.
		if allowed, _, _, err := f.Check(agent.ID, "8.8.8.8:443", "egress"); err == nil || allowed {
			t.Fatalf("external egress target should be denied, allowed=%v err=%v", allowed, err)
		}
		// Local target allowed.
		if allowed, _, _, err := f.Check(agent.ID, "127.0.0.1:443", "egress"); err != nil || !allowed {
			t.Fatalf("local egress target should be allowed, allowed=%v err=%v", allowed, err)
		}
		// Non-egress action unaffected by the rule.
		if allowed, _, _, err := f.Check(agent.ID, "tests", "write"); err != nil || !allowed {
			t.Fatalf("non-egress action should be unaffected, allowed=%v err=%v", allowed, err)
		}
	})

	t.Run("external-approved rule goes through approval", func(t *testing.T) {
		f := NewFirewall().WithAgents(agent).WithEgressRule(EgressRule{Policy: EgressExternalApproved})
		allowed, _, appr, err := f.Check(agent.ID, "8.8.8.8:443", "egress")
		if err != nil {
			t.Fatalf("external egress target should request approval, not error: %v", err)
		}
		if allowed || appr == nil {
			t.Fatalf("external egress target should be pending approval, allowed=%v appr=%+v", allowed, appr)
		}
		if err := f.ApproveAction(appr.ID, "human"); err != nil {
			t.Fatalf("ApproveAction: %v", err)
		}
		if allowed, _, _, err := f.Check(agent.ID, "8.8.8.8:443", "egress"); err != nil || !allowed {
			t.Fatalf("approved external egress target should be allowed, allowed=%v err=%v", allowed, err)
		}
	})
}

func TestEgressPolicyFromEnv(t *testing.T) {
	cases := []struct {
		name string
		env  string
		want EgressPolicy
	}{
		{"unset", "", EgressLocalOnly},
		{"external-redacted", "external-redacted", EgressExternalRedacted},
		{"external-approved", "external-approved", EgressExternalApproved},
		{"garbage", "bananas", EgressLocalOnly},
		{"malformed whitespace", "  external-redacted  ", EgressExternalRedacted},
		{"unknown value", "allow-everything", EgressLocalOnly},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("KERN_EGRESS_POLICY", tc.env)
			if got := EgressPolicyFromEnv(); got != tc.want {
				t.Errorf("EgressPolicyFromEnv() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCheckEgressResource(t *testing.T) {
	localOnly := EgressRule{Policy: EgressLocalOnly}
	redacted := EgressRule{Policy: EgressExternalRedacted}

	// localhost:8080 is local — allowed under the (default) local-only policy.
	if dec := CheckEgressResource(localOnly, "localhost:8080"); !dec.Allowed {
		t.Errorf("localhost:8080 should be allowed under local-only, got %+v", dec)
	}
	// example.com:443 is external — denied under local-only, allowed under
	// external-redacted.
	if dec := CheckEgressResource(localOnly, "example.com:443"); dec.Allowed {
		t.Errorf("example.com:443 should be denied under local-only, got %+v", dec)
	}
	if dec := CheckEgressResource(redacted, "example.com:443"); !dec.Allowed {
		t.Errorf("example.com:443 should be allowed under external-redacted, got %+v", dec)
	}
	// Malformed (no colon) and empty targets fail closed.
	for _, res := range []string{"nocolon", "", "example.com"} {
		dec := CheckEgressResource(redacted, res)
		if dec.Allowed {
			t.Errorf("resource %q should be denied, got %+v", res, dec)
		}
		if !strings.Contains(dec.Reason, "invalid egress target") {
			t.Errorf("resource %q reason %q should mention invalid egress target", res, dec.Reason)
		}
	}
}
