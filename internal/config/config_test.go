package config

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeConfig writes a .kern/config.json fixture under root.
func writeConfig(t *testing.T, root, content string) {
	t.Helper()
	dir := filepath.Join(root, ".kern")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

// captureStderr runs fn with os.Stderr redirected and returns what it wrote.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	defer func() { os.Stderr = old }()
	fn()
	_ = w.Close()
	b, _ := io.ReadAll(r)
	return string(b)
}

// TestPrecedenceString exercises env > file > default for String.
func TestPrecedenceString(t *testing.T) {
	t.Setenv("KERN_LLM_PROVIDER", "")
	root := t.TempDir()
	writeConfig(t, root, `{"llm": {"provider": "file-provider"}}`)

	if got := String(root, "KERN_LLM_PROVIDER", "llm.provider", "default-provider"); got != "file-provider" {
		t.Fatalf("file tier: got %q, want file-provider", got)
	}

	t.Setenv("KERN_LLM_PROVIDER", "env-provider")
	if got := String(root, "KERN_LLM_PROVIDER", "llm.provider", "default-provider"); got != "env-provider" {
		t.Fatalf("env tier: got %q, want env-provider", got)
	}

	t.Setenv("KERN_LLM_PROVIDER", "")
	clean := t.TempDir() // no config file
	if got := String(clean, "KERN_LLM_PROVIDER", "llm.provider", "default-provider"); got != "default-provider" {
		t.Fatalf("default tier: got %q, want default-provider", got)
	}

	// Empty env var is treated as unset → file tier still wins.
	t.Setenv("KERN_LLM_PROVIDER", "")
	if got := String(root, "KERN_LLM_PROVIDER", "llm.provider", "default-provider"); got != "file-provider" {
		t.Fatalf("empty env must fall through to file: got %q", got)
	}
}

// TestPrecedenceInt exercises env > file > default for Int, including a
// garbage env falling through to the file tier.
func TestPrecedenceInt(t *testing.T) {
	t.Setenv("KERN_TEST_INT", "")
	root := t.TempDir()
	writeConfig(t, root, `{"cache": {"max": 42}}`)

	if got := Int(root, "KERN_TEST_INT", "cache.max", 7); got != 42 {
		t.Fatalf("file tier: got %d, want 42", got)
	}

	t.Setenv("KERN_TEST_INT", "99")
	if got := Int(root, "KERN_TEST_INT", "cache.max", 7); got != 99 {
		t.Fatalf("env tier: got %d, want 99", got)
	}

	t.Setenv("KERN_TEST_INT", "garbage")
	if got := Int(root, "KERN_TEST_INT", "cache.max", 7); got != 42 {
		t.Fatalf("garbage env must fall through to file: got %d, want 42", got)
	}

	t.Setenv("KERN_TEST_INT", "")
	clean := t.TempDir()
	if got := Int(clean, "KERN_TEST_INT", "cache.max", 7); got != 7 {
		t.Fatalf("default tier: got %d, want 7", got)
	}
}

// TestPrecedenceFloat64 exercises env > file > default for Float64.
func TestPrecedenceFloat64(t *testing.T) {
	t.Setenv("KERN_TEST_F", "")
	root := t.TempDir()
	writeConfig(t, root, `{"cost_per_token": 0.5}`)

	if got := Float64(root, "KERN_TEST_F", "cost_per_token", 0.00001); got != 0.5 {
		t.Fatalf("file tier: got %v, want 0.5", got)
	}
	t.Setenv("KERN_TEST_F", "0.75")
	if got := Float64(root, "KERN_TEST_F", "cost_per_token", 0.00001); got != 0.75 {
		t.Fatalf("env tier: got %v, want 0.75", got)
	}
	t.Setenv("KERN_TEST_F", "nope")
	if got := Float64(root, "KERN_TEST_F", "cost_per_token", 0.00001); got != 0.5 {
		t.Fatalf("garbage env must fall through to file: got %v", got)
	}
	t.Setenv("KERN_TEST_F", "")
	clean := t.TempDir()
	if got := Float64(clean, "KERN_TEST_F", "cost_per_token", 0.00001); got != 0.00001 {
		t.Fatalf("default tier: got %v, want 0.00001", got)
	}
}

// TestPrecedenceBool exercises env > file > default for Bool.
func TestPrecedenceBool(t *testing.T) {
	t.Setenv("KERN_TEST_B", "")
	root := t.TempDir()
	writeConfig(t, root, `{"exec": {"watch": true}}`)

	if got := Bool(root, "KERN_TEST_B", "exec.watch", false); !got {
		t.Fatal("file tier: want true")
	}
	t.Setenv("KERN_TEST_B", "0")
	if got := Bool(root, "KERN_TEST_B", "exec.watch", false); got {
		t.Fatal("env tier: want false")
	}
	t.Setenv("KERN_TEST_B", "garbage")
	if got := Bool(root, "KERN_TEST_B", "exec.watch", false); !got {
		t.Fatal("garbage env must fall through to file: want true")
	}
	t.Setenv("KERN_TEST_B", "")
	clean := t.TempDir()
	if got := Bool(clean, "KERN_TEST_B", "exec.watch", false); got {
		t.Fatal("default tier: want false")
	}
}

// TestPrecedenceDuration exercises env > file > default for Duration,
// including "30s" style strings in both tiers.
func TestPrecedenceDuration(t *testing.T) {
	t.Setenv("KERN_TEST_D", "")
	root := t.TempDir()
	writeConfig(t, root, `{"runtime": {"poll_interval": "45s"}}`)

	if got := Duration(root, "KERN_TEST_D", "runtime.poll_interval", 30*time.Second); got != 45*time.Second {
		t.Fatalf("file tier: got %v, want 45s", got)
	}
	t.Setenv("KERN_TEST_D", "90s")
	if got := Duration(root, "KERN_TEST_D", "runtime.poll_interval", 30*time.Second); got != 90*time.Second {
		t.Fatalf("env tier: got %v, want 90s", got)
	}
	t.Setenv("KERN_TEST_D", "not-a-duration")
	if got := Duration(root, "KERN_TEST_D", "runtime.poll_interval", 30*time.Second); got != 45*time.Second {
		t.Fatalf("garbage env must fall through to file: got %v", got)
	}
	t.Setenv("KERN_TEST_D", "")
	clean := t.TempDir()
	if got := Duration(clean, "KERN_TEST_D", "runtime.poll_interval", 30*time.Second); got != 30*time.Second {
		t.Fatalf("default tier: got %v, want 30s", got)
	}
}

// TestDurationBareSeconds locks the historical KERN_DEPLOY_TIMEOUT format
// (bare seconds) through the shared Duration getter.
func TestDurationBareSeconds(t *testing.T) {
	t.Setenv("KERN_TEST_BARE", "30")
	if got := Duration("", "KERN_TEST_BARE", "deploy.timeout", 5*time.Minute); got != 30*time.Second {
		t.Fatalf("bare-seconds env: got %v, want 30s", got)
	}
	t.Setenv("KERN_TEST_BARE", "")
	root := t.TempDir()
	writeConfig(t, root, `{"deploy": {"timeout": "45"}}`)
	if got := Duration(root, "KERN_TEST_BARE", "deploy.timeout", 5*time.Minute); got != 45*time.Second {
		t.Fatalf("bare-seconds file: got %v, want 45s", got)
	}
}

// TestPrecedenceStringMap exercises env > file > default for StringMap.
func TestPrecedenceStringMap(t *testing.T) {
	t.Setenv("KERN_TEST_SM", "")
	root := t.TempDir()
	writeConfig(t, root, `{"webhooks": {"ci": "https://hooks.example.com/ci"}}`)

	got := StringMap(root, "KERN_TEST_SM", "webhooks", nil)
	if got["ci"] != "https://hooks.example.com/ci" {
		t.Fatalf("file tier: got %v", got)
	}

	t.Setenv("KERN_TEST_SM", "a=1, b=2")
	got = StringMap(root, "KERN_TEST_SM", "webhooks", nil)
	if got["a"] != "1" || got["b"] != "2" || len(got) != 2 {
		t.Fatalf("env tier: got %v", got)
	}

	t.Setenv("KERN_TEST_SM", "")
	clean := t.TempDir()
	if got := StringMap(clean, "KERN_TEST_SM", "webhooks", nil); len(got) != 0 {
		t.Fatalf("default tier: got %v, want empty", got)
	}
}

// TestStringMapEnvBare locks the bare-entry convention: an env entry without
// "=" is keyed by itself so call sites can derive a name (bare webhook URLs,
// bare project paths).
func TestStringMapEnvBare(t *testing.T) {
	t.Setenv("KERN_TEST_SM", "https://hooks.example.com/x, name=url")
	got := StringMap("", "KERN_TEST_SM", "webhooks", nil)
	if got["https://hooks.example.com/x"] != "https://hooks.example.com/x" {
		t.Fatalf("bare entry: got %v", got)
	}
	if got["name"] != "url" {
		t.Fatalf("named entry: got %v", got)
	}
}

// TestPrecedenceStrings exercises env > file > default for Strings.
func TestPrecedenceStrings(t *testing.T) {
	t.Setenv("KERN_TEST_SS", "")
	root := t.TempDir()
	writeConfig(t, root, `{"mcp": {"roots": ["/a", " /b "]}}`)

	got := Strings(root, "KERN_TEST_SS", "mcp.roots", nil)
	if len(got) != 2 || got[0] != "/a" || got[1] != "/b" {
		t.Fatalf("file tier: got %v", got)
	}

	t.Setenv("KERN_TEST_SS", " /tmp/x , /tmp/y ,,")
	got = Strings(root, "KERN_TEST_SS", "mcp.roots", nil)
	if len(got) != 2 || got[0] != "/tmp/x" || got[1] != "/tmp/y" {
		t.Fatalf("env tier: got %v", got)
	}

	t.Setenv("KERN_TEST_SS", "")
	clean := t.TempDir()
	if got := Strings(clean, "KERN_TEST_SS", "mcp.roots", nil); len(got) != 0 {
		t.Fatalf("default tier: got %v, want empty", got)
	}
}

// TestStringsSplitColonOrComma locks the historical KERN_ROOTS splitter.
func TestStringsSplitColonOrComma(t *testing.T) {
	t.Setenv("KERN_TEST_SPLIT", "a:b, c")
	got := StringsSplit("", "KERN_TEST_SPLIT", "mcp.roots", nil, splitColonOrComma)
	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Fatalf("got %v", got)
	}
}

// TestDotPathLookup exercises nested and deep dot-path keys.
func TestDotPathLookup(t *testing.T) {
	root := t.TempDir()
	writeConfig(t, root, `{"llm": {"model_roles": {"planner": "p", "coder": "c"}}, "runtime": {"k8s": {"namespace": "prod"}}}`)

	if got := String(root, "", "llm.model_roles.planner", ""); got != "p" {
		t.Fatalf("planner: got %q", got)
	}
	if got := String(root, "", "llm.model_roles.coder", ""); got != "c" {
		t.Fatalf("coder: got %q", got)
	}
	if got := String(root, "", "llm.model_roles.reviewer", "fallback"); got != "fallback" {
		t.Fatalf("missing deep key: got %q", got)
	}
	if got := String(root, "", "runtime.k8s.namespace", ""); got != "prod" {
		t.Fatalf("namespace: got %q", got)
	}
	if got := String(root, "", "runtime.missing", "d"); got != "d" {
		t.Fatalf("missing top key: got %q", got)
	}
}

// TestMalformedFileWarnsAndDefaults locks the one-line-warning contract:
// malformed JSON never errors and falls back to defaults.
func TestMalformedFileWarnsAndDefaults(t *testing.T) {
	root := t.TempDir()
	writeConfig(t, root, `{"llm": {not json`)

	Reset()
	var got string
	warn := captureStderr(t, func() {
		got = String(root, "KERN_LLM_PROVIDER", "llm.provider", "default-provider")
	})
	if got != "default-provider" {
		t.Fatalf("malformed file must fall back to default: got %q", got)
	}
	if !strings.Contains(warn, "kern: ignoring malformed") || !strings.Contains(warn, ".kern/config.json") {
		t.Fatalf("expected one-line malformed warning, got %q", warn)
	}

	// The malformed parse is cached per root: a second read must not re-warn.
	warn2 := captureStderr(t, func() {
		_ = String(root, "KERN_LLM_PROVIDER", "llm.provider", "default-provider")
	})
	if warn2 != "" {
		t.Fatalf("cached malformed root re-warned: %q", warn2)
	}
}

// TestWrongTypeFallsThrough locks the per-key contract: a key of the wrong
// type falls through to the default silently (the file itself is valid).
func TestWrongTypeFallsThrough(t *testing.T) {
	root := t.TempDir()
	writeConfig(t, root, `{"llm": {"provider": 5}, "cost_per_token": "expensive"}`)

	if got := String(root, "", "llm.provider", "ollama"); got != "ollama" {
		t.Fatalf("wrong-typed file string: got %q", got)
	}
	if got := Float64(root, "", "cost_per_token", 0.00001); got != 0.00001 {
		t.Fatalf("wrong-typed file float: got %v", got)
	}
}

// TestMissingFileDefaults locks the silent-default contract: no file, no
// warning, defaults returned.
func TestMissingFileDefaults(t *testing.T) {
	root := t.TempDir()
	Reset()
	warn := captureStderr(t, func() {
		if got := String(root, "KERN_LLM_PROVIDER", "llm.provider", "ollama"); got != "ollama" {
			t.Fatalf("got %q", got)
		}
	})
	if warn != "" {
		t.Fatalf("missing file must be silent, got warning %q", warn)
	}
}

// TestPerRootCaching locks the sync.Map cache: each root parses its own file
// once, and a file change is not picked up until Reset.
func TestPerRootCaching(t *testing.T) {
	Reset()
	rootA := t.TempDir()
	rootB := t.TempDir()
	writeConfig(t, rootA, `{"llm": {"provider": "from-a"}}`)
	writeConfig(t, rootB, `{"llm": {"provider": "from-b"}}`)

	if got := String(rootA, "", "llm.provider", "d"); got != "from-a" {
		t.Fatalf("root A: got %q", got)
	}
	if got := String(rootB, "", "llm.provider", "d"); got != "from-b" {
		t.Fatalf("root B: got %q", got)
	}

	// Overwrite A's file: the cached parse must still win until Reset.
	writeConfig(t, rootA, `{"llm": {"provider": "from-a-v2"}}`)
	if got := String(rootA, "", "llm.provider", "d"); got != "from-a" {
		t.Fatalf("cached parse must win before Reset: got %q", got)
	}

	Reset()
	if got := String(rootA, "", "llm.provider", "d"); got != "from-a-v2" {
		t.Fatalf("after Reset: got %q", got)
	}
}

// TestResetClearsEnvIndependentCache is a focused reset-hook check: Reset
// makes the same root re-read its file.
func TestResetClearsEnvIndependentCache(t *testing.T) {
	Reset()
	root := t.TempDir()
	writeConfig(t, root, `{"tokenizer": "bpe"}`)
	if got := String(root, "KERN_TOKENIZER", "tokenizer", "estimator"); got != "bpe" {
		t.Fatalf("initial: got %q", got)
	}
	Reset()
	writeConfig(t, root, `{"tokenizer": "cl100k"}`)
	if got := String(root, "KERN_TOKENIZER", "tokenizer", "estimator"); got != "cl100k" {
		t.Fatalf("after reset+rewrite: got %q", got)
	}
}

// TestEffectiveReportsSource locks Effective's (value, source) contract for
// the kern config command across all three tiers.
func TestEffectiveReportsSource(t *testing.T) {
	t.Setenv("KERN_EXEC_RISK", "")
	root := t.TempDir()
	writeConfig(t, root, `{"exec": {"risk": "HIGH"}}`)

	for _, k := range Registry {
		if k.Key != "exec.risk" {
			continue
		}
		v, src := Effective(root, k)
		if v.(string) != "HIGH" || src != "file" {
			t.Fatalf("file tier: got (%v, %q)", v, src)
		}
		t.Setenv("KERN_EXEC_RISK", "CRITICAL")
		v, src = Effective(root, k)
		if v.(string) != "CRITICAL" || src != "env" {
			t.Fatalf("env tier: got (%v, %q)", v, src)
		}
		t.Setenv("KERN_EXEC_RISK", "")
		clean := t.TempDir()
		v, src = Effective(clean, k)
		if v.(string) != "MEDIUM" || src != "default" {
			t.Fatalf("default tier: got (%v, %q)", v, src)
		}
	}
}

// TestEffectiveModelRoles locks the per-role resolution used by kern config:
// env per-role vars win per role, file fills the rest.
func TestEffectiveModelRoles(t *testing.T) {
	t.Setenv("KERN_MODEL_PLANNER", "")
	t.Setenv("KERN_MODEL_CODER", "")
	root := t.TempDir()
	writeConfig(t, root, `{"llm": {"model_roles": {"planner": "file-planner", "coder": "file-coder"}}}`)

	var k Key
	for _, r := range Registry {
		if r.Key == "llm.model_roles" {
			k = r
		}
	}
	v, src := Effective(root, k)
	m := v.(map[string]string)
	if m["planner"] != "file-planner" || m["coder"] != "file-coder" || src != "file" {
		t.Fatalf("file tier: got %v (%s)", m, src)
	}

	t.Setenv("KERN_MODEL_PLANNER", "env-planner")
	v, src = Effective(root, k)
	m = v.(map[string]string)
	if m["planner"] != "env-planner" || m["coder"] != "file-coder" || src != "env" {
		t.Fatalf("env-per-role tier: got %v (%s)", m, src)
	}

	t.Setenv("KERN_MODEL_PLANNER", "")
	t.Setenv("KERN_MODEL_CODER", "")
	clean := t.TempDir()
	v, src = Effective(clean, k)
	if m := v.(map[string]string); len(m) != 0 || src != "default" {
		t.Fatalf("default tier: got %v (%s)", m, src)
	}
}
