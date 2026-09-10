// Package governance egress policy: outbound-connection posture for agent
// actions and sandboxed runs. Pure policy — no I/O, no state; the only env
// access is EgressPolicyFromEnv reading KERN_EGRESS_POLICY at call time.
package governance

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
)

// EgressPolicy enumerates the outbound-connection postures an agent action
// or sandboxed run may take.
type EgressPolicy string

const (
	EgressLocalOnly        EgressPolicy = "local-only"        // localhost/private ranges only; external denied
	EgressExternalRedacted EgressPolicy = "external-redacted" // external allowed, but payloads must be redacted
	EgressExternalApproved EgressPolicy = "external-approved" // external allowed only with a human approval
)

// EgressRule declares the outbound posture. Empty Hosts = any host permitted
// by the policy; empty Ports = any port.
type EgressRule struct {
	Policy EgressPolicy
	Hosts  []string
	Ports  []int
}

// EgressDecision is the outcome of an egress check.
type EgressDecision struct {
	Policy           EgressPolicy
	Allowed          bool
	RequiresApproval bool // external-approved policy, external target
	Reason           string
}

// CheckEgress validates an outbound connection (host:port) against a rule.
// Pure function, no I/O. Deny order: invalid port (<0) → host not in rule.Hosts
// (when non-empty) → port not in rule.Ports (when non-empty) → policy:
// local-only denies non-local hosts; external-redacted allows; external-approved
// allows local targets and returns RequiresApproval=true for external targets.
func CheckEgress(rule EgressRule, host string, port int) EgressDecision {
	if port < 0 {
		return EgressDecision{Policy: rule.Policy, Allowed: false, Reason: fmt.Sprintf("invalid port %d", port)}
	}
	if len(rule.Hosts) > 0 {
		match := false
		for _, h := range rule.Hosts {
			if strings.EqualFold(h, host) {
				match = true
				break
			}
		}
		if !match {
			return EgressDecision{Policy: rule.Policy, Allowed: false, Reason: fmt.Sprintf("host %q not in rule allowlist", host)}
		}
	}
	if len(rule.Ports) > 0 {
		match := false
		for _, p := range rule.Ports {
			if p == port {
				match = true
				break
			}
		}
		if !match {
			return EgressDecision{Policy: rule.Policy, Allowed: false, Reason: fmt.Sprintf("port %d not in rule allowlist", port)}
		}
	}
	switch rule.Policy {
	case EgressExternalRedacted:
		return EgressDecision{Policy: rule.Policy, Allowed: true, Reason: "external target allowed (payloads must be redacted)"}
	case EgressExternalApproved:
		if IsLocalHost(host) {
			return EgressDecision{Policy: rule.Policy, Allowed: true, Reason: "local target allowed"}
		}
		return EgressDecision{Policy: rule.Policy, Allowed: false, RequiresApproval: true, Reason: "external target requires human approval"}
	default: // EgressLocalOnly (and the empty policy, treated as local-only by callers)
		if IsLocalHost(host) {
			return EgressDecision{Policy: rule.Policy, Allowed: true, Reason: "local target allowed"}
		}
		return EgressDecision{Policy: rule.Policy, Allowed: false, Reason: fmt.Sprintf("external target %q denied by local-only policy", host)}
	}
}

// IsLocalHost reports whether host is loopback/private/link-local/unspecified
// (net.ParseIP + IsLoopback/IsPrivate/IsLinkLocalUnicast/IsUnspecified), or
// the literal names "localhost"/"localhost.localdomain" (case-insensitive).
// Name-based classification is deliberately restricted to those two literals:
// single-label hostnames (mDNS), ".local" names, and the empty host are NOT
// local — with DNS search domains (corporate laptops/VPNs) they can resolve to
// routable external IPs, so treating them as local would bypass local-only
// policy and skip approval under external-approved. Empty input fails closed
// (not local). Strip IPv6 brackets before classifying.
func IsLocalHost(host string) bool {
	h := strings.TrimSpace(host)
	h = strings.TrimPrefix(h, "[")
	h = strings.TrimSuffix(h, "]")
	lower := strings.ToLower(h)
	if lower == "" {
		return false
	}
	if lower == "localhost" || lower == "localhost.localdomain" {
		return true
	}
	ip := net.ParseIP(h)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified()
}

// parseEgressTarget extracts a host:port pair from a firewall resource string.
// It tries net.SplitHostPort first; on success the host and parsed port are
// returned (a bad port falls back to 0, meaning "any port"). Otherwise the
// "egress:" prefix is stripped and (resource, 0) is returned, where 0 means
// "any port".
func parseEgressTarget(resource string) (host string, port int) {
	if h, p, err := net.SplitHostPort(resource); err == nil {
		if n, perr := strconv.Atoi(p); perr == nil {
			return h, n
		}
		return h, 0
	}
	return strings.TrimPrefix(resource, "egress:"), 0
}

// EgressPolicyFromEnv reads the local operator's KERN_EGRESS_POLICY env var
// and returns the matching policy. Anything other than "external-redacted" or
// "external-approved" — empty, unknown, or malformed — fails closed to
// EgressLocalOnly.
func EgressPolicyFromEnv() EgressPolicy {
	switch EgressPolicy(strings.TrimSpace(os.Getenv("KERN_EGRESS_POLICY"))) {
	case EgressExternalRedacted:
		return EgressExternalRedacted
	case EgressExternalApproved:
		return EgressExternalApproved
	default:
		return EgressLocalOnly
	}
}

// CheckEgressResource validates a firewall resource string ("host:port", or
// "egress:host:port" after the prefix is stripped) against a rule. Empty or
// malformed resources (no colon) fail closed with a Denied decision carrying
// the reason "invalid egress target".
func CheckEgressResource(rule EgressRule, resource string) EgressDecision {
	if !strings.Contains(resource, ":") {
		return EgressDecision{Policy: rule.Policy, Allowed: false, Reason: "invalid egress target"}
	}
	host, port := parseEgressTarget(resource)
	return CheckEgress(rule, host, port)
}
