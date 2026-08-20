package script

import (
	"strings"
	"testing"
)

func TestNetworkIsolationBlocksEgress(t *testing.T) {
	if networkNS() == nil {
		t.Skip("no unprivileged netns on this host")
	}
	res := RunScript(Run{Lang: "python3", Code: `
import urllib.request
try:
    urllib.request.urlopen("http://example.com", timeout=3)
    print("NETWORK_REACHABLE")
except Exception as e:
    print("BLOCKED")
`})
	if res.Err != nil {
		t.Fatalf("run failed: %v (%s)", res.Err, res.Stderr)
	}
	if !res.Isolated {
		t.Fatal("expected Isolated=true")
	}
	if strings.TrimSpace(res.Stdout) != "BLOCKED" {
		t.Fatalf("expected network blocked, got %q", res.Stdout)
	}
}

func TestEnvSanitized(t *testing.T) {
	t.Setenv("HOME", "/real/home")
	t.Setenv("SUPERSECRET", "s3cr3t")
	// Env isolation is what this test exercises, not network; allow the
	// degraded (env-only) path so it runs even where netns is unavailable.
	allowDegradedNetwork(t)
	res := RunScript(Run{Lang: "bash", Code: `echo "HOME=$HOME SECRET=$SUPERSECRET"`})
	if res.Err != nil {
		t.Fatalf("run failed: %v (%s)", res.Err, res.Stderr)
	}
	if strings.Contains(res.Stdout, "SUPERSECRET") || strings.HasPrefix(res.Stdout, "HOME=/real") {
		t.Fatalf("env not sanitized, stdout=%q", res.Stdout)
	}
	if res.Isolated != (networkNS() != nil) {
		t.Fatalf("Isolated mismatch: %v vs networkNS()!=nil", res.Isolated)
	}
}

// TestNoIsolateIgnoredWithoutOptIn asserts that a NoIsolate=true request from
// an arbitrary caller is silently dropped (isolation kept) unless the local
// operator has explicitly set KERN_ALLOW_NO_ISOLATE=1. This is the F2
// fail-closed behaviour.
func TestNoIsolateIgnoredWithoutOptIn(t *testing.T) {
	if !runtimeInstalled("bash") {
		t.Skip("bash not installed")
	}
	t.Setenv("SUPERSECRET", "s3cr3t")
	t.Setenv("KERN_ALLOW_NO_ISOLATE", "") // operator has NOT opted in
	// NoIsolate is dropped -> effective isolation -> F9 fail-closed on hosts
	// without netns; allow the degraded path so we can assert env sanitization.
	allowDegradedNetwork(t)

	res := RunScript(Run{Lang: "bash", Code: `echo "SECRET=$SUPERSECRET"`, NoIsolate: true})
	if res.Err != nil {
		t.Fatalf("run failed: %v (%s)", res.Err, res.Stderr)
	}
	// NoIsolate should have been ignored: the caller env secret must not leak.
	if strings.Contains(res.Stdout, "s3cr3t") {
		t.Fatalf("NoIsolate was honored without KERN_ALLOW_NO_ISOLATE; env secret leaked, stdout=%q", res.Stdout)
	}
}

// TestNoIsolateAllowedWithOptIn asserts that the local operator's explicit
// KERN_ALLOW_NO_ISOLATE=1 does make NoIsolate effective (F2 opt-in path).
func TestNoIsolateAllowedWithOptIn(t *testing.T) {
	if !runtimeInstalled("bash") {
		t.Skip("bash not installed")
	}
	t.Setenv("SUPERSECRET", "s3cr3t")
	t.Setenv("KERN_ALLOW_NO_ISOLATE", "1")

	res := RunScript(Run{Lang: "bash", Code: `echo "SECRET=$SUPERSECRET"`, NoIsolate: true})
	if res.Err != nil {
		t.Fatalf("run failed: %v (%s)", res.Err, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "s3cr3t") {
		t.Fatalf("NoIsolate not honored despite KERN_ALLOW_NO_ISOLATE=1; stdout=%q", res.Stdout)
	}
}
