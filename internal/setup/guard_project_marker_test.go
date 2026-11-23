package setup

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// guardMarkerEnv builds an environment with a usable fake kern and the supplied
// project root, matching the existing fail-open guard fixtures.
func guardMarkerEnv(t *testing.T, home, project string) []string {
	t.Helper()
	bin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "kern"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return []string{"KERN_GUARD_ASSUME_KERN=0", "PATH=/usr/bin:/bin", "HOME=" + home, "CLAUDE_PROJECT_DIR=" + project}
}

func guardedReadPayload(t *testing.T) string {
	t.Helper()
	readFile := filepath.Join(t.TempDir(), "main.go")
	body := "package main\n\n" + strings.Repeat("// governed code fixture line\n", 120)
	if err := os.WriteFile(readFile, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf(`{"tool_name":"Read","tool_input":{"file_path":%q}}`, readFile)
}

func TestKernGuardExplicitProjectMarker(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX sh guard")
	}
	script := writeGuardScriptFile(t)
	home := t.TempDir()
	project := t.TempDir()
	payload := guardedReadPayload(t)
	if err := os.MkdirAll(filepath.Join(project, ".kern"), 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(project, ".kern", "config.json")
	if err := os.WriteFile(marker, []byte(`{"schema_version":1,"managed":true,"guard":{"enabled":true}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _ := runGuard(t, script, payload, guardMarkerEnv(t, home, project)); code != 2 {
		t.Fatalf("explicit managed marker must guard, got exit %d", code)
	}
	if err := os.WriteFile(marker, []byte(`{"schema_version":1,"managed":false,"guard":{"enabled":true}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _ := runGuard(t, script, payload, guardMarkerEnv(t, home, project)); code != 0 {
		t.Fatalf("explicit opt-out must fail open, got exit %d", code)
	}
}

func TestKernGuardAncestorMarkerOptOut(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX sh guard")
	}
	script := writeGuardScriptFile(t)
	home := t.TempDir()
	root := t.TempDir()
	child := filepath.Join(root, "child")
	if err := os.MkdirAll(filepath.Join(root, ".kern"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".kern", "config.json"), []byte(`{"schema_version":1,"managed":false,"guard":{"enabled":false}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	if code, _ := runGuard(t, script, guardedReadPayload(t), guardMarkerEnv(t, home, child)); code != 0 {
		t.Fatalf("ancestor explicit opt-out must fail open, got exit %d", code)
	}
	if err := os.WriteFile(filepath.Join(root, ".kern", "config.json"), []byte(`{"schema_version":1,"managed":true,"guard":{"enabled":true}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _ := runGuard(t, script, guardedReadPayload(t), guardMarkerEnv(t, home, child)); code != 2 {
		t.Fatalf("ancestor explicit opt-in must govern child, got exit %d", code)
	}
}

func TestKernGuardMalformedMarkerKeepsLegacyManaged(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX sh guard")
	}
	script := writeGuardScriptFile(t)
	home := t.TempDir()
	project := t.TempDir()
	payload := guardedReadPayload(t)
	if err := os.MkdirAll(filepath.Join(project, ".kern"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".kern", "index.sqlite"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".kern", "config.json"), []byte(`{`), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _ := runGuard(t, script, payload, guardMarkerEnv(t, home, project)); code != 2 {
		t.Fatalf("malformed marker plus legacy index must remain guarded, got exit %d", code)
	}
}
