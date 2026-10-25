package setup

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// fakeGuardPauseKern writes a kern executable whose `guard paused` command
// always reports the supplied status. The hook must consult the CLI only
// after its cheap registry-existence probe, and the bash arm must remain
// governed even when that CLI says a repo pause is active.
func fakeGuardPauseKern(t *testing.T, status string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(bin, "kern")
	script := "#!/bin/sh\nif [ \"$1\" = guard ] && [ \"$2\" = paused ]; then printf '%s\\n' " + status + "; exit 0; fi\nexit 0\n"
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

func activePauseJSON(t *testing.T, root, reason string) string {
	t.Helper()
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	return fmt.Sprintf(
		`{"schema_version":"kern-guard-pause","scope":"repo","root":%q,"reason":%q,"expires":%q,"recorded":%q}`+"\n",
		canonical,
		reason,
		now.Add(time.Hour).Format(time.RFC3339),
		now.Format(time.RFC3339),
	)
}

func quoteJSON(s string) string {
	var b []byte
	b = append(b, '"')
	for _, r := range s {
		switch r {
		case '"':
			b = append(b, '\\', '"')
		case '\\':
			b = append(b, '\\', '\\')
		default:
			b = append(b, []byte(string(r))...)
		}
	}
	b = append(b, '"')
	return string(b)
}

func TestKernGuardPauseScopesReadGrepGlobOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX sh guard")
	}
	script := writeGuardScriptFile(t)
	repo := t.TempDir()
	bin := fakeGuardPauseKern(t, "active")
	home := t.TempDir()
	registry := filepath.Join(home, ".kern", "guard", "pauses.jsonl")
	if err := os.MkdirAll(filepath.Dir(registry), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(registry, []byte(activePauseJSON(t, repo, "focused debugging")), 0o644); err != nil {
		t.Fatal(err)
	}

	allow := map[string]string{
		"Read": `{"tool_name":"Read","tool_input":{"file_path":"/tmp/x/main.go"}}`,
		"grep": `{"tool_name":"grep","tool_input":{"pattern":"RunGuard"}}`,
		"glob": `{"tool_name":"glob","tool_input":{"pattern":"*.go"}}`,
	}
	for name, payload := range allow {
		env := []string{
			"PATH=" + bin + ":/usr/bin:/bin",
			"HOME=" + home,
			"CLAUDE_PROJECT_DIR=" + repo,
		}
		if code, stderr := runGuard(t, script, payload, env); code != 0 {
			t.Errorf("%s: active repo pause must allow steering surface, got exit %d stderr %q", name, code, stderr)
		}
	}

	// The pause is intentionally not a bash bypass: a destructive command
	// still hits the exec-governance gate.
	env := []string{
		"PATH=" + bin + ":/usr/bin:/bin",
		"HOME=" + home,
		"CLAUDE_PROJECT_DIR=" + repo,
	}
	bash := `{"tool_name":"bash","tool_input":{"command":"rm -rf /tmp/x"}}`
	if code, stderr := runGuard(t, script, bash, env); code != 2 {
		t.Errorf("active repo pause must NOT bypass bash governance, got exit %d stderr %q", code, stderr)
	}
}

func TestKernGuardPauseRequiresRegistryAndActiveStatus(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX sh guard")
	}
	script := writeGuardScriptFile(t)
	repo := t.TempDir()
	bin := fakeGuardPauseKern(t, "active")
	read := `{"tool_name":"Read","tool_input":{"file_path":"/tmp/x/main.go"}}`

	// Even a CLI that claims active must not matter when the operator-owned
	// registry does not exist: the existence probe is the cheap first gate.
	missingHome := t.TempDir()
	env := []string{
		"PATH=" + bin + ":/usr/bin:/bin",
		"HOME=" + missingHome,
		"CLAUDE_PROJECT_DIR=" + repo,
	}
	if code, stderr := runGuard(t, script, read, env); code != 2 {
		t.Errorf("missing pause registry must keep reads governed, got exit %d stderr %q", code, stderr)
	}

	// An existing registry with no active record for this repo stays governed.
	home := t.TempDir()
	registry := filepath.Join(home, ".kern", "guard", "pauses.jsonl")
	if err := os.MkdirAll(filepath.Dir(registry), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(registry, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	inactiveBin := fakeGuardPauseKern(t, "inactive")
	env = []string{
		"PATH=" + inactiveBin + ":/usr/bin:/bin",
		"HOME=" + home,
		"CLAUDE_PROJECT_DIR=" + repo,
	}
	if code, stderr := runGuard(t, script, read, env); code != 2 {
		t.Errorf("inactive repo pause must keep reads governed, got exit %d stderr %q", code, stderr)
	}
}
