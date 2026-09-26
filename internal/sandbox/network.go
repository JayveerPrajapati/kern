package sandbox

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// NetworkPolicy records the network posture of a sandboxed run — the network
// half of the impact manifest. A zero-dependency build cannot trace
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
	// FSConfined reports whether the run executed under filesystem read
	// confinement (Linux Landlock allowlist; the macOS Seatbelt profile's
	// sensitive-path blocklist is not reflected here). Set by runGuarded
	// after the run, mirroring Isolated.
	FSConfined bool
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
// .
func assessNetwork(output string) *NetworkPolicy {
	p := &NetworkPolicy{
		// Isolated is overridden by runGuarded after the run: it reflects the
		// actual wrap (F-S1), not a static posture.
		NetnsAvail:  networkIsolationAvailable(),
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

// netIsolationProbe caches the isolation availability probe: whether this host
// can run a command in an isolated network environment is a per-host fact that
// does not change during a process lifetime. Mirrors internal/script's probe
// so `kern sandbox` and `kern exec` make the same fail-closed decision.
var (
	netProbeOnce   sync.Once
	netIsolationOK bool
)

// sensitivePathDirs are operator-private locations a sandboxed command must
// never be able to READ (file-borne secrets: SSH keys, cloud credentials,
// GPG keys, session cookies, docker/netrc credentials). Stage 1 (FS
// confinement): on macOS each entry becomes a Seatbelt `(deny file-read-data
// (subpath ...))` rule appended to the network profile; on Linux the
// Landlock allowlist builder (internal/sandbox/landlock) enumerates $HOME
// against the first-level entries and never grants them. This is a
// BLOCKLIST-deny design — `(allow default)` is preserved on macOS, so nothing
// outside these paths is affected and go test / GOCACHE / GOMODCACHE reads
// keep working unchanged. internal/script mirrors this list for kern_exec
// (keep all in sync).
var sensitivePathDirs = []string{
	".ssh",
	".aws",
	".gnupg",
	".config/gcloud",
	".kube",
	".docker/config.json",
	"Library/Cookies",
	".netrc",
}

// fsConfinementEnabled reports whether sandboxed filesystem read confinement
// is active (KERN_SANDBOX_FS_CONFINEMENT, default ON). Disabling follows the
// codebase's "0 disables" convention (KERN_MCP_CACHE, KERN_MCP_WATCH);
// unset or any other value keeps confinement on. The pre-existing
// KERN_ALLOW_UNISOLATED / KERN_ALLOW_NET escape hatch covers this surface
// too: when it is set, runGuarded skips the whole seatbelt wrap (network
// AND filesystem), so FS confinement never applies to an explicitly
// unisolated run.
func fsConfinementEnabled() bool {
	switch strings.TrimSpace(os.Getenv("KERN_SANDBOX_FS_CONFINEMENT")) {
	case "0", "false", "FALSE", "False", "no", "NO", "No", "off", "OFF", "Off":
		return false
	}
	return true
}

// seatbeltProfile generates the Apple Seatbelt (sandbox-exec) profile that
// runs a command with network egress denied except loopback and — when
// filesystem read confinement is enabled — read access to the operator's
// sensitive-path blocklist denied. SBPL is last-match-wins: the trailing
// loopback allow re-permits loopback outbound on top of the blanket deny,
// and the trailing file denies override the leading (allow default) for
// exactly the blocklisted paths.
func seatbeltProfile() string {
	home := ""
	if h, err := os.UserHomeDir(); err == nil {
		// The as-written $HOME is passed through as-is: seatbeltProfileFor
		// denies BOTH the as-written and the canonical spelling of every
		// blocklisted path, so canonicalizing here would throw away the
		// alias the kernel may match on (R2 root cause).
		home = h
	}
	return seatbeltProfileFor(home, fsConfinementEnabled())
}

// seatbeltProfileFor builds the SBPL text for a given home directory and
// confinement toggle. Pure and deterministic so tests can pin the exact
// profile shape: blocklist paths present, (allow default) preserved, the
// real $HOME substituted into every subpath rule.
func seatbeltProfileFor(home string, fsConfine bool) string {
	var b strings.Builder
	b.WriteString("(version 1)\n(allow default)\n(deny network*)\n(allow network-outbound (remote ip \"localhost:*\"))")
	if fsConfine && home != "" {
		for _, p := range sensitivePathDirs {
			joined := filepath.Join(home, p)
			// Deny BOTH spellings (R2 root cause): the darwin kernel
			// canonicalizes the ACCESSED path but matches profile subpaths as
			// written, so a symlinked home (e.g. /var -> /private/var on
			// macOS) can be accessed under either alias depending on
			// name-cache state — a one-sided deny lets the other side through
			// intermittently. The as-written join is always emitted; the
			// canonical form is added when it resolves and differs (fail-safe:
			// on resolution error the single as-written deny remains). Denies
			// are purely additive under the blocklist design.
			fmt.Fprintf(&b, "\n(deny file-read-data (subpath %q))", joined)
			if canon, err := filepath.EvalSymlinks(joined); err == nil && canon != joined {
				fmt.Fprintf(&b, "\n(deny file-read-data (subpath %q))", canon)
			}
		}
	}
	return b.String()
}

// netIsolationPrefix returns the argv prefix that runs a command with
// network egress denied on this host — the exact invocation the availability
// probe validates (macOS Apple Seatbelt `sandbox-exec` profile; Linux
// `unshare` user+net namespace). ok=false when the host cannot isolate.
// The probe's availability and this prefix come from the same mechanism:
// if networkIsolationAvailable() returned true, the prefix executes.
// Loopback stays reachable in both (httptest servers); Linux brings the
// new namespace's lo up best-effort. On darwin the generated profile also
// carries the sensitive-path read blocklist (Stage 1 FS confinement) when
// KERN_SANDBOX_FS_CONFINEMENT is on (the default).
func netIsolationPrefix() (prefix []string, ok bool) {
	if runtime.GOOS == "darwin" {
		bin, err := exec.LookPath("sandbox-exec")
		if err != nil {
			return nil, false
		}
		return []string{bin, "-p", seatbeltProfile()}, true
	}
	bin, err := exec.LookPath("unshare")
	if err != nil {
		return nil, false
	}
	// Inside the fresh netns only loopback exists and it starts DOWN;
	// bring it up best-effort (ip may be absent) so local test servers
	// work, then exec the real command ($@ = cmdName args...).
	script := `ip link set lo up 2>/dev/null || true; exec "$@"`
	return []string{bin, "--user", "--map-root-user", "--net", "sh", "-c", script, "kern-cmd"}, true
}

// networkIsolationAvailable reports whether this host can provide network
// isolation for a sandboxed run (Linux unprivileged user+network namespaces via
// `unshare` or macOS Apple Seatbelt via `sandbox-exec`).
func networkIsolationAvailable() bool {
	netProbeOnce.Do(func() {
		if runtime.GOOS == "darwin" {
			if bin, err := exec.LookPath("sandbox-exec"); err == nil {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				// The probe validates the EXACT profile the wrap uses
				// (seatbeltProfile — the generated network profile plus the
				// sensitive-path read blocklist under the real home when
				// confinement is on): availability and enforcement must be
				// the same mechanism, not a lookalike.
				if err := exec.CommandContext(ctx, bin, "-p", seatbeltProfile(), "true").Run(); err == nil {
					netIsolationOK = true
					return
				}
			}
		}
		bin, err := exec.LookPath("unshare")
		if err != nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := exec.CommandContext(ctx, bin, "--user", "--map-root-user", "--net", "true").Run(); err == nil {
			netIsolationOK = true
		}
	})
	return netIsolationOK
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
	if p.FSConfined {
		isolation += "; fs confined (Landlock allowlist)"
	} else if p.NetnsAvail {
		// Linux with a working netns chain but no Landlock: the sensitive-path
		// blocklist is OFF (kernel too old, probe failed, confinement disabled)
		// — surface the degradation explicitly so operators do not mistake
		// "isolated" for "secret-blocklisted".
		isolation += "; fs confinement unavailable (degraded)"
	}
	if len(p.Hits) == 0 {
		return isolation + "; no network-error signatures in output"
	}
	return fmt.Sprintf("%s; network-error signatures in output: %s", isolation, strings.Join(p.Hits, ", "))
}
