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
