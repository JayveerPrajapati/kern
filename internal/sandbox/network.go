package sandbox

import (
	"fmt"
	"os"
	"runtime"
	"strings"
)

// NetworkPolicy records the network posture of a sandboxed run — the network
// half of the impact manifest (G-3). A zero-dependency build cannot trace
// syscalls, so the policy captures what was enforced and any network
// failures visible in the command's output, not per-connection attempts.
// (Per-syscall read/write network auditing would need ptrace/eBPF or a
// static tracer — deliberately out of scope.)
type NetworkPolicy struct {
	// Isolated reports whether the run executed inside a private network
	// namespace. It is always false for sandbox.Run: the snapshot sandbox
	// restores files on failure but does NOT block network egress (the
	// script runtime in internal/script isolates and fails closed). The
	// policy records this honestly so callers see the exposure.
	Isolated bool
	// NetnsAvail reports whether the platform supports unprivileged
	// user+network namespaces (Linux yes; macOS/Windows no — the same
	// determination doctor's network-isolation check makes).
	NetnsAvail bool
	// AllowNetEnv reports whether the KERN_ALLOW_NET / KERN_ALLOW_UNISOLATED
	// escape hatch is set (the operator opted unisolated runs in for the
	// surfaces that do isolate).
	AllowNetEnv bool
	// Hits lists the network-error signatures matched in the run's output
	// (deduplicated, capped). Presence hints at network activity — or at
	// least attempts — during the run.
	Hits []string
}

// networkErrorSignatures are lowercase substrings commonly emitted by
// runtimes and CLIs when a network operation fails. A match does not prove
// egress occurred, but the audit should surface the hint.
var networkErrorSignatures = []string{
	"connection refused",
	"connection reset",
	"connection timed out",
	"dial tcp",
	"dial udp",
	"name resolution",
	"network is unreachable",
	"network unreachable",
	"no route to host",
	"no such host",
	"tls handshake",
}

// maxNetworkHits caps the recorded signatures so chatty output cannot bloat
// the result.
const maxNetworkHits = 5

// assessNetwork builds the run's network policy from its combined output
// (G-3).
func assessNetwork(output string) *NetworkPolicy {
	p := &NetworkPolicy{
		Isolated:    false, // sandbox.Run does not isolate egress; see NetworkPolicy.Isolated
		NetnsAvail:  runtime.GOOS == "linux",
		AllowNetEnv: netEscapeHatchSet(),
	}
	lower := strings.ToLower(output)
	for _, sig := range networkErrorSignatures {
		if strings.Contains(lower, sig) {
			p.Hits = append(p.Hits, sig)
			if len(p.Hits) >= maxNetworkHits {
				break
			}
		}
	}
	return p
}

// netEscapeHatchSet reports whether either escape-hatch env var is enabled.
func netEscapeHatchSet() bool {
	on := func(name string) bool {
		switch strings.TrimSpace(os.Getenv(name)) {
		case "1", "true", "TRUE", "True":
			return true
		}
		return false
	}
	return on("KERN_ALLOW_NET") || on("KERN_ALLOW_UNISOLATED")
}

// Summary renders the policy as a one-line verdict for CLI/MCP output. Safe
// on a nil policy.
func (p *NetworkPolicy) Summary() string {
	if p == nil {
		return "unknown (run never started)"
	}
	isolation := "not isolated (egress allowed)"
	if p.Isolated {
		isolation = "isolated (private netns)"
	} else if !p.NetnsAvail {
		isolation += "; netns unavailable on this platform"
	}
	if len(p.Hits) == 0 {
		return isolation + "; no network-error signatures in output"
	}
	return fmt.Sprintf("%s; network-error signatures in output: %s", isolation, strings.Join(p.Hits, ", "))
}
