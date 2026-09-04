package main

import (
	"strings"
	"testing"
)

// TestG23_ResilienceCheckWiring verifies that `blueprint check --resilience`
// runs the resilience check on a fixture with .blueprint/scenarios/ and that
// the default fast check set (without the flag) does not.
func TestG23_ResilienceCheckWiring(t *testing.T) {
	_ = requireKernPath(t) // skip when kern is not reachable (gate needs it)
	bin := g4BuildBinary(t)
	dir := t.TempDir()
	g4GitRepo(t, dir)

	g4WriteFile(t, dir, "go.mod", "module example.com/test\n\ngo 1.23\n")
	g4WriteFile(t, dir, "main.go", `package main

import "net/http"

func fetchURL(url string) (*http.Response, error) {
	return http.Get(url) // no timeout, no status check
}

func main() {}
`)
	g4WriteFile(t, dir, ".blueprint/scenarios/http.yaml", `scenarios:
  - id: payments-timeout
    kind: http
    params:
      status: 500
      delay_seconds: 0
      path: /api/v1/payments
`)
	g4RunGit(t, dir, "add", "-A")
	g4RunGit(t, dir, "commit", "-qm", "init")

	// Stage a change so the pipeline emits per-check lines.
	g4WriteFile(t, dir, "main.go", `package main

import "net/http"

func fetchURL(url string) (*http.Response, error) {
	return http.Get(url) // no timeout, no status check
}

func main() {}

// staged change
`)
	g4RunGit(t, dir, "add", "main.go")

	// With --resilience the resilience check runs and emits a WARN finding
	// (non-resilient fixture fails the injected 500 fault). WARN-only ⇒ exit 0.
	out, code := g4BlueprintCheck(t, bin, dir, "--resilience")
	if code != 0 {
		t.Fatalf("exit=%d want 0 (WARN-only); output:\n%s", code, out)
	}
	if !strings.Contains(out, "resilience:scenarios") {
		t.Fatalf("output missing resilience check line:\n%s", out)
	}
	if !strings.Contains(out, "WARN") {
		t.Fatalf("output missing WARN status:\n%s", out)
	}

	// Without the flag the fast check set runs and must not include the
	// resilience check.
	out2, code2 := g4BlueprintCheck(t, bin, dir)
	if code2 != 0 {
		t.Fatalf("exit=%d want 0; output:\n%s", code2, out2)
	}
	if strings.Contains(out2, "resilience:scenarios") {
		t.Fatalf("resilience check ran without --resilience:\n%s", out2)
	}
}

// TestG23_ResilienceCheckWiringInvalidYAML verifies that a malformed scenarios
// file degrades to a WARN (never a hard error / block) under --resilience.
func TestG23_ResilienceCheckWiringInvalidYAML(t *testing.T) {
	_ = requireKernPath(t)
	bin := g4BuildBinary(t)
	dir := t.TempDir()
	g4GitRepo(t, dir)

	g4WriteFile(t, dir, "go.mod", "module example.com/test\n\ngo 1.23\n")
	g4WriteFile(t, dir, "main.go", `package main

func main() {}
`)
	g4WriteFile(t, dir, ".blueprint/scenarios/bad.yaml", "scenarios:\n  - id: [unclosed\n")
	g4RunGit(t, dir, "add", "-A")
	g4RunGit(t, dir, "commit", "-qm", "init")
	g4WriteFile(t, dir, "main.go", "package main\n\nfunc main() {}\n\n// staged change\n")
	g4RunGit(t, dir, "add", "main.go")

	out, code := g4BlueprintCheck(t, bin, dir, "--resilience")
	if code != 0 {
		t.Fatalf("exit=%d want 0 (invalid YAML is WARN-only); output:\n%s", code, out)
	}
	if !strings.Contains(out, "resilience:scenarios") || !strings.Contains(out, "WARN") {
		t.Fatalf("output missing resilience WARN line:\n%s", out)
	}
}
