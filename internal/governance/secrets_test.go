package governance

import (
	"strings"
	"testing"
)

func TestStripSecretsDefault(t *testing.T) {
	env := []string{
		"AWS_SECRET_ACCESS_KEY=abc123",
		"GITHUB_TOKEN=ghp_xyz",
		"MY_API_TOKEN=tok",
		"DATABASE_PASSWORD=pw",
		"PATH=/usr/bin",
		"KERN_EMBED_MODEL=some-model",
	}
	out := StripSecrets(env, DefaultSecretFilter())
	joined := strings.Join(out, "\n")
	for _, keep := range []string{"PATH=", "KERN_EMBED_MODEL="} {
		if !strings.Contains(joined, keep) {
			t.Errorf("expected %q to be kept, got %v", keep, out)
		}
	}
	for _, drop := range []string{"AWS_SECRET_ACCESS_KEY", "GITHUB_TOKEN", "MY_API_TOKEN", "DATABASE_PASSWORD"} {
		if strings.Contains(joined, drop) {
			t.Errorf("expected %q to be stripped, got %v", drop, out)
		}
	}
	if len(out) != 2 {
		t.Errorf("expected 2 vars kept, got %v", out)
	}
}

func TestStripSecretsAllowlistWins(t *testing.T) {
	f := SecretFilter{Patterns: []string{"token"}, Exact: []string{"SOME_TOKEN"}, Allowlist: []string{"SOME_TOKEN"}}
	out := StripSecrets([]string{"SOME_TOKEN=abc"}, f)
	if len(out) != 1 || out[0] != "SOME_TOKEN=abc" {
		t.Errorf("allowlisted exact name should win, got %v", out)
	}
}

func TestStripSecretsExact(t *testing.T) {
	// Exact matching is case-sensitive: a differently-cased lookalike is NOT
	// stripped by Exact, but may still be stripped by a case-insensitive
	// Pattern (e.g. "github_token" contains "token").
	f := SecretFilter{Exact: []string{"GITHUB_TOKEN"}, Patterns: []string{"api_key", "token"}}
	env := []string{"GITHUB_TOKEN=1", "github_token=2", "MY_API_KEY=3", "KEEP_ME=4"}
	out := StripSecrets(env, f)
	kept := map[string]bool{}
	for _, kv := range out {
		kept[strings.SplitN(kv, "=", 2)[0]] = true
	}
	if kept["GITHUB_TOKEN"] {
		t.Error("GITHUB_TOKEN should be stripped by Exact")
	}
	if kept["github_token"] {
		t.Error("github_token should be stripped via Pattern 'token' (Exact is case-sensitive)")
	}
	if kept["MY_API_KEY"] {
		t.Error("MY_API_KEY should be stripped by Pattern 'api_key'")
	}
	if !kept["KEEP_ME"] {
		t.Error("KEEP_ME should be kept")
	}
	if len(out) != 1 {
		t.Errorf("expected 1 kept var, got %v", out)
	}
}

func TestStripSecretsRedactWith(t *testing.T) {
	f := SecretFilter{Patterns: []string{"token"}, RedactWith: "[REDACTED]"}
	out := StripSecrets([]string{"API_TOKEN=supersecret", "FOO=bar"}, f)
	if len(out) != 2 {
		t.Fatalf("expected 2 vars, got %v", out)
	}
	if out[0] != "API_TOKEN=[REDACTED]" {
		t.Errorf("secret value should be replaced, got %q", out[0])
	}
	if out[1] != "FOO=bar" {
		t.Errorf("non-secret should be untouched, got %q", out[1])
	}
}

func TestStripSecretsEmptyFilter(t *testing.T) {
	var f SecretFilter
	out := StripSecrets([]string{"SECRET=1", "malformed"}, f)
	if len(out) != 1 || out[0] != "SECRET=1" {
		t.Errorf("zero-value filter should strip nothing (and drop malformed entries), got %v", out)
	}
}

func TestStripSecretsNil(t *testing.T) {
	out := StripSecrets(nil, DefaultSecretFilter())
	if len(out) != 0 {
		t.Errorf("nil env should yield empty output, got %v", out)
	}
}

func TestIsSecret(t *testing.T) {
	f := DefaultSecretFilter()
	table := []struct {
		name string
		want bool
	}{
		{"API_TOKEN", true},
		{"KERN_EMBED_MODEL", false},
		{"FOO", false},
		{"PASSWORD", true},
		{"KERN_ALLOW_NET", false},
	}
	for _, tc := range table {
		if got := f.IsSecret(tc.name); got != tc.want {
			t.Errorf("IsSecret(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}
