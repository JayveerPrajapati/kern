package script

import (
	"strings"
	"testing"
)

// egressProbeOptIn forces an unisolated run the same way TestNoIsolateAllowedWithOptIn
// does (KERN_ALLOW_NO_ISOLATE=1): the egress gate is about UNISOLATED runs, so
// making the run deterministically unisolated exercises the gate on every
// platform regardless of whether an unprivileged netns is available.
func egressProbeOptIn(t *testing.T) {
	t.Helper()
	if !runtimeInstalled("bash") {
		t.Skip("bash not installed")
	}
	t.Setenv("KERN_ALLOW_NO_ISOLATE", "1")
	t.Setenv("KERN_EGRESS_POLICY", "")
}

// TestScriptEgressDeclaredExternalDeniedByDefault asserts the fail-closed
// default: a script declaring an external "# egress:" target is refused when
// the operator's policy is the default local-only.
func TestScriptEgressDeclaredExternalDeniedByDefault(t *testing.T) {
	egressProbeOptIn(t)
	res := RunScript(Run{Lang: "bash", NoIsolate: true, Code: "# egress: example.com:443\necho hi\n"})
	if res.Err == nil {
		t.Fatalf("expected fail-closed refusal, got success (stdout=%q)", res.Stdout)
	}
	for _, want := range []string{"example.com:443", "fail-closed", "KERN_EGRESS_POLICY=external-redacted"} {
		if !strings.Contains(res.Err.Error(), want) {
			t.Errorf("egress error %q missing %q", res.Err, want)
		}
	}
	if res.OK {
		t.Error("refused run must not be OK")
	}
}

// TestScriptEgressLocalTargetAllowed asserts a declared local target passes
// the default local-only policy.
func TestScriptEgressLocalTargetAllowed(t *testing.T) {
	egressProbeOptIn(t)
	res := RunScript(Run{Lang: "bash", NoIsolate: true, Code: "# egress: localhost:8080\necho hi\n"})
	if res.Err != nil {
		t.Fatalf("run failed: %v (%s)", res.Err, res.Stderr)
	}
	if strings.TrimSpace(res.Stdout) != "hi" {
		t.Fatalf("stdout = %q, want hi", res.Stdout)
	}
}

// TestScriptEgressNoDeclarationUnchanged asserts that a script without any
// egress declaration AND without network-shaped operations runs exactly as
// before (the deny-by-default gate only fires on network-shaped code).
func TestScriptEgressNoDeclarationUnchanged(t *testing.T) {
	egressProbeOptIn(t)
	res := RunScript(Run{Lang: "bash", NoIsolate: true, Code: "echo hi\n"})
	if res.Err != nil {
		t.Fatalf("run failed: %v (%s)", res.Err, res.Stderr)
	}
	if strings.TrimSpace(res.Stdout) != "hi" {
		t.Fatalf("stdout = %q, want hi", res.Stdout)
	}
}

// TestScriptEgressExternalAllowedWhenRedacted asserts the operator's
// KERN_EGRESS_POLICY=external-redacted override lets a declared external
// target run.
func TestScriptEgressExternalAllowedWhenRedacted(t *testing.T) {
	egressProbeOptIn(t)
	t.Setenv("KERN_EGRESS_POLICY", "external-redacted")
	res := RunScript(Run{Lang: "bash", NoIsolate: true, Code: "# egress: example.com:443\necho hi\n"})
	if res.Err != nil {
		t.Fatalf("run failed: %v (%s)", res.Err, res.Stderr)
	}
	if strings.TrimSpace(res.Stdout) != "hi" {
		t.Fatalf("stdout = %q, want hi", res.Stdout)
	}
}

// TestScriptEgressMalformedTargetRefused asserts a malformed declaration
// ("nocolon", no host:port separator) fails closed.
func TestScriptEgressMalformedTargetRefused(t *testing.T) {
	egressProbeOptIn(t)
	res := RunScript(Run{Lang: "bash", NoIsolate: true, Code: "# egress: nocolon\necho hi\n"})
	if res.Err == nil {
		t.Fatalf("expected refusal for malformed target, got success (stdout=%q)", res.Stdout)
	}
	if !strings.Contains(res.Err.Error(), "invalid egress target") {
		t.Errorf("egress error %q should mention invalid egress target", res.Err)
	}
}

// TestParseEgressTargets pins the declaration scanner: exact "# egress:"
// prefix (case-sensitive), leading whitespace allowed, non-empty values only.
func TestParseEgressTargets(t *testing.T) {
	code := "#!/bin/bash\n" +
		"# egress: example.com:443\n" +
		"    # egress:   api.example.com:8443  \n" +
		"# Egress: case-sensitively-ignored.example.com:443\n" +
		"# egress:\n" +
		"echo hi\n"
	got := parseEgressTargets(code)
	want := []string{"example.com:443", "api.example.com:8443"}
	if len(got) != len(want) {
		t.Fatalf("parseEgressTargets() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("parseEgressTargets()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if len(parseEgressTargets("echo hi\n")) != 0 {
		t.Error("no-declaration code should yield no targets")
	}
}

func TestScriptEgressNetworkCallNoDeclarationDenied(t *testing.T) {
	egressProbeOptIn(t)
	res := RunScript(Run{Lang: "bash", NoIsolate: true, Code: "curl http://example.com\n"})
	if res.Err == nil {
		t.Fatalf("expected deny-by-default refusal, got success (stdout=%q)", res.Stdout)
	}
	for _, want := range []string{"declares no egress targets", "# egress: host:port", "// egress:"} {
		if !strings.Contains(res.Err.Error(), want) {
			t.Errorf("egress error %q missing %q", res.Err, want)
		}
	}
	if res.OK {
		t.Error("refused run must not be OK")
	}
}

func TestScriptEgressNetworkCallWithDeclarationPolicyDenied(t *testing.T) {
	egressProbeOptIn(t)
	res := RunScript(Run{Lang: "bash", NoIsolate: true, Code: "# egress: 8.8.8.8:443\ncurl http://8.8.8.8\n"})
	if res.Err == nil {
		t.Fatalf("expected policy refusal, got success (stdout=%q)", res.Stdout)
	}
	if strings.Contains(res.Err.Error(), "declares no egress targets") {
		t.Errorf("no-declaration gate fired despite a declaration: %v", res.Err)
	}
	for _, want := range []string{"denies target", "8.8.8.8:443"} {
		if !strings.Contains(res.Err.Error(), want) {
			t.Errorf("egress error %q missing %q", res.Err, want)
		}
	}
}

// TestScriptEgressNetworkCallWithLocalDeclarationAllowed asserts that a
// network-calling script with a declared LOCAL target passes the gate (the
// per-target check is unchanged for allowed targets).
func TestScriptEgressNetworkCallWithLocalDeclarationAllowed(t *testing.T) {
	egressProbeOptIn(t)
	res := RunScript(Run{Lang: "bash", NoIsolate: true, Code: "# egress: localhost:80\ncurl http://localhost:80\n"})
	if res.Err != nil && strings.Contains(res.Err.Error(), "egress") {
		t.Fatalf("declared local target should pass the egress gate, got egress error: %v", res.Err)
	}
}

func TestParseEgressTargetsSlashComments(t *testing.T) {
	code := "#!/usr/bin/env node\n" +
		"// egress: api.example.com:443\n" +
		"  // egress:   localhost:8080  \n" +
		"// Egress: case-sensitively-ignored.example.com:443\n" +
		"// egress:\n" +
		"fetch('http://example.com')\n"
	got := parseEgressTargets(code)
	want := []string{"api.example.com:443", "localhost:8080"}
	if len(got) != len(want) {
		t.Fatalf("parseEgressTargets() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("parseEgressTargets()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestParseEgressTargetsMixedComments asserts "# egress:" and "// egress:"
// declarations are both recognized and deduplicated in order of appearance.
func TestParseEgressTargetsMixedComments(t *testing.T) {
	code := "#!/bin/bash\n" +
		"# egress: shell.example.com:443\n" +
		"// egress: js.example.com:8443\n" +
		"echo hi\n"
	got := parseEgressTargets(code)
	want := []string{"shell.example.com:443", "js.example.com:8443"}
	if len(got) != len(want) {
		t.Fatalf("parseEgressTargets() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("parseEgressTargets()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestScriptEgressIsolatedUnaffected(t *testing.T) {
	if err := egressGate("curl http://example.com\n", nil, true); err != nil {
		t.Fatalf("egressGate(isolated) should be a no-op, got %v", err)
	}
	if err := egressGate("fetch('http://example.com')\nimport urllib.request\n", nil, true); err != nil {
		t.Fatalf("egressGate(isolated) should be a no-op, got %v", err)
	}
	if err := egressGate("curl http://example.com\n", []string{"not-a-target"}, true); err != nil {
		t.Fatalf("egressGate(isolated) should be a no-op even with a malformed structured target, got %v", err)
	}
}

func TestContainsNetworkCall(t *testing.T) {
	network := []string{
		"curl http://example.com",
		"wget -q https://example.com/x",
		"nc -z example.com 443",
		"netcat example.com 22",
		"ssh user@host",
		"scp a b@c:/tmp",
		`import "net/http"`,
		`http.Get("https://x")`,
		"http.Post(\"https://x\", ...)",
		"http.NewRequest(\"GET\", url, nil)",
		"net.Dial(\"tcp\", \"example.com:80\")",
		"socket(AF_INET, SOCK_STREAM)",
		"fetch('https://api.example.com')",
		"axios.get('/api')",
		"require('node-fetch')",
		"requests.get('https://x')",
		"import urllib.request",
		"httpx.get(\"https://x\")",
		"aiohttp.ClientSession()",
		"new HttpClient()",
		"Invoke-WebRequest -Uri https://x",
		"Invoke-RestMethod -Uri https://x",
	}
	for _, c := range network {
		if !containsNetworkCall(c) {
			t.Errorf("containsNetworkCall(%q) = false, want true", c)
		}
	}
	benign := []string{
		"echo hi",
		"print(42)",
		"for i in 1 2 3; do echo $i; done",
		"const x = 1;",
		"# egress: example.com:443",
		"// egress: example.com:443",
		"# a comment mentioning the word fetching data locally",
	}
	for _, c := range benign {
		if containsNetworkCall(c) {
			t.Errorf("containsNetworkCall(%q) = true, want false", c)
		}
	}
}

func TestScriptEgressStructuredOnlySatisfiesGate(t *testing.T) {
	egressProbeOptIn(t)

	// Structured declaration only — no comment declarations in the code.
	res := RunScript(Run{Lang: "bash", NoIsolate: true, Code: "echo curl\n", Egress: []string{"localhost:80"}})
	if res.Err != nil && strings.Contains(res.Err.Error(), "egress") {
		t.Fatalf("structured egress declaration should satisfy the gate, got egress error: %v", res.Err)
	}
	if res.Err != nil {
		t.Fatalf("run failed: %v (%s)", res.Err, res.Stderr)
	}

	// Same network-shaped code without any declaration is refused.
	res = RunScript(Run{Lang: "bash", NoIsolate: true, Code: "echo curl\n"})
	if res.Err == nil || !strings.Contains(res.Err.Error(), "declares no egress targets") {
		t.Fatalf("expected deny-by-default refusal, got %v", res.Err)
	}
}

func TestScriptEgressStructuredPolicyChecked(t *testing.T) {
	egressProbeOptIn(t)

	res := RunScript(Run{Lang: "bash", NoIsolate: true, Code: "curl http://8.8.8.8\n", Egress: []string{"8.8.8.8:443"}})
	if res.Err == nil {
		t.Fatalf("expected policy refusal for structured external target, got success (stdout=%q)", res.Stdout)
	}
	if strings.Contains(res.Err.Error(), "declares no egress targets") {
		t.Errorf("no-declaration gate fired despite a structured declaration: %v", res.Err)
	}
	for _, want := range []string{"denies target", "8.8.8.8:443", "fail-closed"} {
		if !strings.Contains(res.Err.Error(), want) {
			t.Errorf("policy error %q missing %q", res.Err, want)
		}
	}
}

func TestScriptEgressStructuredUnionWithComments(t *testing.T) {
	egressProbeOptIn(t)

	// Comment declares one local target, structured declares another — union passes.
	res := RunScript(Run{
		Lang:      "bash",
		NoIsolate: true,
		Code:      "# egress: localhost:80\necho curl\n",
		Egress:    []string{"localhost:8080"},
	})
	if res.Err != nil && strings.Contains(res.Err.Error(), "egress") {
		t.Fatalf("union of comment + structured declarations should pass, got egress error: %v", res.Err)
	}
	if res.Err != nil {
		t.Fatalf("run failed: %v (%s)", res.Err, res.Stderr)
	}

	// A denied structured target in the union refuses the run even though the
	// comment declaration is allowed.
	res = RunScript(Run{
		Lang:      "bash",
		NoIsolate: true,
		Code:      "# egress: localhost:80\ncurl http://8.8.8.8\n",
		Egress:    []string{"8.8.8.8:443"},
	})
	if res.Err == nil || !strings.Contains(res.Err.Error(), "8.8.8.8:443") {
		t.Fatalf("expected policy refusal naming the structured target, got %v", res.Err)
	}

	// A denied comment target in the union refuses the run even though the
	// structured declaration is allowed.
	res = RunScript(Run{
		Lang:      "bash",
		NoIsolate: true,
		Code:      "# egress: 8.8.8.8:443\ncurl http://localhost:80\n",
		Egress:    []string{"localhost:80"},
	})
	if res.Err == nil || !strings.Contains(res.Err.Error(), "8.8.8.8:443") {
		t.Fatalf("expected policy refusal naming the comment-declared target, got %v", res.Err)
	}
}

func TestScriptEgressStructuredInvalidRefused(t *testing.T) {
	egressProbeOptIn(t)

	for _, bad := range []string{
		"nocolon",                  // no host:port separator
		"api.example.com",          // host without port
		"api.example.com:notaport", // non-numeric port
		"api.example.com:0",        // port 0 out of range
		"api.example.com:70000",    // port out of range
	} {
		res := RunScript(Run{Lang: "bash", NoIsolate: true, Code: "curl http://example.com\n", Egress: []string{bad}})
		if res.Err == nil {
			t.Errorf("expected refusal for invalid structured target %q, got success (stdout=%q)", bad, res.Stdout)
			continue
		}
		if !strings.Contains(res.Err.Error(), "invalid egress target") || !strings.Contains(res.Err.Error(), bad) {
			t.Errorf("invalid-target error for %q should name the value, got %v", bad, res.Err)
		}
	}

	// Whitespace-padded and empty structured entries are tolerated: padding is
	// trimmed and empties are skipped, so a padded local target still passes
	// while an all-empty slice behaves like no declaration.
	res := RunScript(Run{Lang: "bash", NoIsolate: true, Code: "echo curl\n", Egress: []string{"", "  localhost:80  "}})
	if res.Err != nil && strings.Contains(res.Err.Error(), "egress") {
		t.Fatalf("padded structured target should pass, got egress error: %v", res.Err)
	}
	res = RunScript(Run{Lang: "bash", NoIsolate: true, Code: "echo curl\n", Egress: []string{"", "  "}})
	if res.Err == nil || !strings.Contains(res.Err.Error(), "declares no egress targets") {
		t.Fatalf("all-empty structured slice should behave like no declaration, got %v", res.Err)
	}
}

// TestEgressGateStructuredUnionNilForNonNetwork pins that structured
// declarations are inert on code that performs no network-shaped operation:
// the deny-by-default gate only fires on network-shaped code.
func TestEgressGateStructuredUnionNilForNonNetwork(t *testing.T) {
	if err := egressGate("echo hi\n", []string{"localhost:80"}, false); err != nil {
		t.Fatalf("non-network code with a structured declaration should pass, got %v", err)
	}
	if err := egressGate("echo hi\n", nil, false); err != nil {
		t.Fatalf("non-network code with no declarations should pass, got %v", err)
	}
}
