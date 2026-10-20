package runtime

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/JayveerPrajapati/kern/internal/config"
)

// ValidateURL reports whether a live-source endpoint is a usable http(s) URL.
// Malformed endpoints are rejected before a poller is constructed, so a typo
// in KERN_PROMETHEUS_URL cannot spawn a silently broken background source
// (an invalid URL fails validation loudly in doctor instead).
func ValidateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("runtime URL %q: %v", raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("runtime URL %q: scheme must be http or https", raw)
	}
	if u.Host == "" {
		return fmt.Errorf("runtime URL %q: missing host", raw)
	}
	return nil
}

// SourceDesc describes the configured runtime source WITHOUT constructing it:
// no poller goroutine, no network. `kern doctor` reports from this so it stays
// offline; live telemetry comes from the consumers that actually poll (the
// MCP kern_runtime tool, the platform wiring for review/incident contexts).
type SourceDesc struct {
	Kind    string        // "" | prometheus | otel | kubernetes | snapshot
	URL     string        // endpoint, or the snapshot file path
	Origin  string        // "env" | "config" | "snapshot"
	AuthEnv string        // credential env var ("" when the source takes none)
	HasAuth bool          // credential env var set (live sources only)
	Poll    time.Duration // poll interval for live sources
	Invalid string        // non-empty when the configured URL failed validation
}

// DescribeSource mirrors LoadSource's resolution order (env/config live
// adapters first, then the .kern/runtime.json snapshot), returning a
// description of what would be wired — and why it cannot be, when invalid.
// Credentials are reported as env-var PRESENCE only; values are never read
// into the description.
func DescribeSource(root string) SourceDesc {
	interval := PollInterval()
	if u := config.String("", "KERN_PROMETHEUS_URL", "runtime.prometheus_url", ""); u != "" {
		return describeLive("prometheus", "KERN_PROMETHEUS_URL", u, "KERN_PROMETHEUS_TOKEN", interval)
	}
	if u := config.String("", "KERN_OTEL_URL", "runtime.otel_url", ""); u != "" {
		return describeLive("otel", "KERN_OTEL_URL", u, "KERN_OTEL_TOKEN", interval)
	}
	if u := config.String("", "KERN_K8S_API", "runtime.k8s.api", ""); u != "" {
		return describeLive("kubernetes", "KERN_K8S_API", u, "KERN_K8S_TOKEN", interval)
	}
	path := filepath.Join(root, ".kern", "runtime.json")
	if _, err := os.Stat(path); err == nil {
		return SourceDesc{Kind: "snapshot", URL: path, Origin: "snapshot"}
	}
	return SourceDesc{}
}

// describeLive builds the live-source description. Origin distinguishes an
// env-var configuration (checked first by config.String's precedence) from a
// .kern/config.json key.
func describeLive(kind, envName, u, authEnv string, interval time.Duration) SourceDesc {
	d := SourceDesc{Kind: kind, URL: u, AuthEnv: authEnv, Poll: interval, Origin: "config"}
	if os.Getenv(envName) == u {
		d.Origin = "env"
	}
	if authEnv != "" && os.Getenv(authEnv) != "" {
		d.HasAuth = true
	}
	if err := ValidateURL(u); err != nil {
		d.Invalid = err.Error()
	}
	return d
}
