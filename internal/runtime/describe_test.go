package runtime

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateURL(t *testing.T) {
	cases := []struct {
		raw   string
		valid bool
	}{
		{"http://prometheus:9090/api/v1/query?query=up", true},
		{"https://kubernetes.default.svc", true},
		{"http://localhost:11434", true},
		{"not-a-url", false},     // no scheme
		{"ftp://host:21", false}, // scheme is not http(s)
		{"http://", false},       // missing host
		{"://bad", false},        // unparseable
	}
	for _, c := range cases {
		err := ValidateURL(c.raw)
		if c.valid && err != nil {
			t.Errorf("ValidateURL(%q) = %v, want nil", c.raw, err)
		}
		if !c.valid && err == nil {
			t.Errorf("ValidateURL(%q) = nil, want error", c.raw)
		}
	}
}

func TestDescribeSourceNone(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root) // isolate from any cwd-level env/config
	if d := DescribeSource(root); d.Kind != "" || d.URL != "" {
		t.Fatalf("DescribeSource = %+v, want zero value", d)
	}
}

func TestDescribeSourceLiveEnv(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)

	t.Setenv("KERN_PROMETHEUS_URL", "http://prometheus:9090")
	t.Setenv("KERN_PROMETHEUS_TOKEN", "sekrit")
	d := DescribeSource(root)
	if d.Kind != "prometheus" || d.URL != "http://prometheus:9090" {
		t.Fatalf("desc = %+v, want prometheus with URL", d)
	}
	if d.Origin != "env" {
		t.Errorf("Origin = %q, want env", d.Origin)
	}
	if !d.HasAuth || d.AuthEnv != "KERN_PROMETHEUS_TOKEN" {
		t.Errorf("auth = %v %q, want HasAuth with KERN_PROMETHEUS_TOKEN", d.HasAuth, d.AuthEnv)
	}
	if d.Invalid != "" {
		t.Errorf("Invalid = %q, want empty", d.Invalid)
	}
	if d.Poll <= 0 {
		t.Errorf("Poll = %v, want > 0", d.Poll)
	}

	// Without the token env: same wiring, credentials absent.
	t.Setenv("KERN_PROMETHEUS_TOKEN", "")
	d = DescribeSource(root)
	if d.HasAuth {
		t.Errorf("HasAuth = true with no token env, want false")
	}
}

func TestDescribeSourceKubernetes(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)

	t.Setenv("KERN_K8S_API", "https://kubernetes.default.svc")
	t.Setenv("KERN_K8S_TOKEN", "sa-token")
	d := DescribeSource(root)
	if d.Kind != "kubernetes" || d.AuthEnv != "KERN_K8S_TOKEN" || !d.HasAuth {
		t.Fatalf("desc = %+v, want kubernetes with KERN_K8S_TOKEN set", d)
	}

	// K8s without a token still wires (unauthenticated API servers only).
	t.Setenv("KERN_K8S_TOKEN", "")
	if d = DescribeSource(root); d.HasAuth {
		t.Errorf("HasAuth = true with no KERN_K8S_TOKEN, want false")
	}
}

func TestDescribeSourceInvalidURL(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)

	t.Setenv("KERN_OTEL_URL", "not-a-url")
	d := DescribeSource(root)
	if d.Kind != "otel" {
		t.Fatalf("desc = %+v, want otel kind even when invalid", d)
	}
	if d.Invalid == "" {
		t.Fatalf("Invalid empty for malformed URL, desc = %+v", d)
	}
	// The misconfiguration must not spawn a broken poller.
	if src := LoadSource(root); src != nil {
		t.Errorf("LoadSource returned %v for invalid URL, want nil", src)
	}
}

func TestDescribeSourceConfigFileTier(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)

	if err := os.MkdirAll(filepath.Join(root, ".kern"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := `{"runtime": {"prometheus_url": "http://prometheus:9090"}}`
	if err := os.WriteFile(filepath.Join(root, ".kern", "config.json"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	d := DescribeSource(root)
	if d.Kind != "prometheus" || d.Origin != "config" {
		t.Fatalf("desc = %+v, want prometheus via config tier", d)
	}
	if d.Invalid != "" {
		t.Errorf("Invalid = %q, want empty", d.Invalid)
	}
}

func TestDescribeSourceSnapshot(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)

	if err := os.MkdirAll(filepath.Join(root, ".kern"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".kern", "runtime.json"), []byte(`{"events":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	d := DescribeSource(root)
	if d.Kind != "snapshot" || d.Origin != "snapshot" {
		t.Fatalf("desc = %+v, want snapshot", d)
	}
	if d.URL != filepath.Join(root, ".kern", "runtime.json") {
		t.Errorf("URL = %q, want the snapshot path", d.URL)
	}
	if d.Poll != 0 {
		t.Errorf("Poll = %v, want 0 for a snapshot", d.Poll)
	}
}

// TestDescribeSourcePrecedence pins the resolution order: a live env var wins
// over an existing snapshot file.
func TestDescribeSourcePrecedence(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)

	if err := os.MkdirAll(filepath.Join(root, ".kern"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".kern", "runtime.json"), []byte(`{"events":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KERN_OTEL_URL", "http://otel:4318/v1/metrics")
	d := DescribeSource(root)
	if d.Kind != "otel" {
		t.Fatalf("desc = %+v, want live otel to win over snapshot", d)
	}
}

// TestLoadSourceInvalidURLReturnsNil is the load-side pin of the validation
// gate: every malformed live endpoint yields nil (no broken poller), matching
// the "nothing configured" contract every consumer already handles.
func TestLoadSourceInvalidURLReturnsNil(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)

	t.Setenv("KERN_PROMETHEUS_URL", "ftp://nope")
	if src := LoadSource(root); src != nil {
		t.Errorf("LoadSource = %v, want nil for ftp scheme", src)
	}
	t.Setenv("KERN_PROMETHEUS_URL", "http://")
	if src := LoadSource(root); src != nil {
		t.Errorf("LoadSource = %v, want nil for hostless URL", src)
	}
}
