package runtime

import (
	"os"
	"path/filepath"
	"time"

	"github.com/JayveerPrajapati/kern/internal/config"
)

// LoadSource resolves the production-intelligence adapter for a project.
// Resolution order (first hit wins):
//  1. KERN_PROMETHEUS_URL (or runtime.prometheus_url) -> live Prometheus poller
//  2. KERN_OTEL_URL (or runtime.otel_url)            -> live OTLP poller
//  3. KERN_K8S_API (or runtime.k8s.api)              -> live Kubernetes poller
//  4. <root>/.kern/runtime.json                      -> local JSON snapshot
//
// Env vars are read via config so .kern/config.json keys work as overrides.
// Returns nil when nothing is configured — the "no telemetry" state that
// every consumer must treat as an absent dimension, not a failure.
func LoadSource(root string) Source {
	interval := PollInterval()
	if url := config.String("", "KERN_PROMETHEUS_URL", "runtime.prometheus_url", ""); url != "" {
		return NewLivePrometheusSource(url, interval)
	}
	if url := config.String("", "KERN_OTEL_URL", "runtime.otel_url", ""); url != "" {
		return NewLiveOtelSource(url, interval)
	}
	if api := config.String("", "KERN_K8S_API", "runtime.k8s.api", ""); api != "" {
		return NewLiveKubernetesSource(
			api,
			os.Getenv("KERN_K8S_TOKEN"),
			config.String("", "KERN_K8S_NAMESPACE", "runtime.k8s.namespace", ""),
			interval,
		)
	}
	st, err := LoadJSON(filepath.Join(root, ".kern", "runtime.json"))
	if err != nil {
		return nil
	}
	return st
}

// PollInterval returns the live-adapter poll interval from KERN_POLL_INTERVAL
// (or runtime.poll_interval in .kern/config.json; a Go duration string,
// default 30s). Invalid values fall back to the default.
func PollInterval() time.Duration {
	const def = 30 * time.Second
	d := config.Duration("", "KERN_POLL_INTERVAL", "runtime.poll_interval", def)
	if d <= 0 {
		return def
	}
	return d
}
