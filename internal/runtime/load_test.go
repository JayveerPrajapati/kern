package runtime

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestLoadSourceEnvAdapters pins the adapter resolution order and shape:
// live adapters win over the local snapshot, and the first configured
// endpoint (prometheus > otel > k8s) wins.
func TestLoadSourceEnvAdapters(t *testing.T) {
	t.Chdir(t.TempDir()) // isolate from any cwd-level .kern/config.json

	cases := []struct {
		name string
		env  map[string]string
		want string // source Name()
	}{
		{name: "prometheus", env: map[string]string{"KERN_PROMETHEUS_URL": "http://127.0.0.1:1/q"}, want: "prometheus"},
		{name: "otel", env: map[string]string{"KERN_OTEL_URL": "http://127.0.0.1:1/v1/traces"}, want: "otel"},
		{name: "kubernetes", env: map[string]string{"KERN_K8S_API": "https://127.0.0.1:1", "KERN_K8S_TOKEN": "tok"}, want: "kubernetes"},
		{name: "priority-prometheus-over-otel", env: map[string]string{
			"KERN_PROMETHEUS_URL": "http://127.0.0.1:1/q",
			"KERN_OTEL_URL":       "http://127.0.0.1:1/v1/traces",
		}, want: "prometheus"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for k := range tc.env {
				t.Setenv(k, tc.env[k])
			}
			src := LoadSource(".")
			if src == nil {
				t.Fatalf("LoadSource = nil, want %s", tc.want)
			}
			if src.Name() != tc.want {
				t.Fatalf("source name = %q, want %q", src.Name(), tc.want)
			}
			// Live adapters start a poll loop on construction; stop it so
			// tests do not leak goroutines.
			switch s := src.(type) {
			case *LivePrometheusSource, *LiveOtelSource, *LiveKubernetesSource:
				s.(interface{ Close() }).Close()
			}
		})
	}
}

// TestLoadSourceLocalSnapshot pins the offline fallback: no live-adapter env
// vars + a .kern/runtime.json snapshot yields a local Store.
func TestLoadSourceLocalSnapshot(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	if err := os.MkdirAll(filepath.Join(root, ".kern"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".kern", "runtime.json"),
		[]byte(`{"events":[{"id":"e1","type":"metric","service":"app","severity":"info","message":"rps=1"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	src := LoadSource(root)
	if src == nil {
		t.Fatal("LoadSource = nil, want the local Store")
	}
	if src.Name() != "local" {
		t.Fatalf("source name = %q, want local", src.Name())
	}
	if got := len(src.Events("")); got != 1 {
		t.Fatalf("events = %d, want 1", got)
	}
}

// TestLoadSourceNone pins the absent state: no env vars and no snapshot
// yields nil — the "no telemetry" contract every consumer must treat as an
// absent dimension.
func TestLoadSourceNone(t *testing.T) {
	t.Chdir(t.TempDir())
	if src := LoadSource("."); src != nil {
		t.Fatalf("LoadSource = %v, want nil (nothing configured)", src)
	}
}

// TestPollInterval pins the interval resolution: default 30s, env override,
// and invalid values falling back to the default.
func TestPollInterval(t *testing.T) {
	if got := PollInterval(); got != 30*time.Second {
		t.Fatalf("PollInterval() = %v, want 30s default", got)
	}
	t.Setenv("KERN_POLL_INTERVAL", "5s")
	if got := PollInterval(); got != 5*time.Second {
		t.Fatalf("PollInterval() = %v, want 5s", got)
	}
	t.Setenv("KERN_POLL_INTERVAL", "bogus")
	if got := PollInterval(); got != 30*time.Second {
		t.Fatalf("PollInterval() = %v, want 30s fallback for invalid value", got)
	}
}
