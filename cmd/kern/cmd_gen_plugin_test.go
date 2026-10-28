package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// preparePluginRepo builds a scratch repo-root layout with the canonical
// plugin copy, a deliberately stale embedded asset, and a registry file
// carrying one historical hash, returning root.
func preparePluginRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	canon := filepath.Join(root, ".opencode", "plugins")
	if err := os.MkdirAll(canon, 0o755); err != nil {
		t.Fatal(err)
	}
	canonical := filepath.Join(canon, "kern.ts")
	if err := os.WriteFile(canonical, []byte("export const plugin = 1;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	asset := filepath.Join(root, "internal", "agentwire", "assets", "plugin")
	if err := os.MkdirAll(asset, 0o755); err != nil {
		t.Fatal(err)
	}
	// Stale asset — differs from canonical.
	if err := os.WriteFile(filepath.Join(asset, "kern.ts"), []byte("export const plugin = 2;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	regDir := filepath.Join(root, filepath.Dir(pluginRegistryRel))
	if err := os.MkdirAll(regDir, 0o755); err != nil {
		t.Fatal(err)
	}
	registry := "package pluginhash\n\nvar shipped = map[string]bool{\n\t\"deadbeef\": true,\n}\n"
	if err := os.WriteFile(filepath.Join(root, pluginRegistryRel), []byte(registry), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestGenPluginCheckFreshAndStale pins the --check gate: a fresh (byte-
// identical) asset with its sha256 registered reports fresh and exits 0; a
// stale asset fails loudly and is NOT rewritten by --check.
func TestGenPluginCheckFreshAndStale(t *testing.T) {
	root := preparePluginRepo(t)
	asset := filepath.Join(root, pluginAssetRel)

	// Stale asset: --check must fail and not rewrite.
	expectExit(t, 1, func() {
		runGenPlugin([]string{"--root", root, "--check"})
	})
	after, err := os.ReadFile(asset)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(after), "plugin = 1") {
		t.Fatal("--check must not rewrite a stale asset")
	}

	// Regenerate: canonical is copied byte-identical into the asset AND its
	// sha256 lands in the registry (the manual step this replaces).
	out := captureStdout(t, func() {
		runGenPlugin([]string{"--root", root})
	})
	if !strings.Contains(out, "regenerated") || !strings.Contains(out, "registered sha256") {
		t.Fatalf("expected regenerated + registered output, got %q", out)
	}
	got, err := os.ReadFile(asset)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "export const plugin = 1;\n" {
		t.Fatalf("asset not byte-identical after regen: %q", got)
	}
	regRaw, err := os.ReadFile(filepath.Join(root, pluginRegistryRel))
	if err != nil {
		t.Fatal(err)
	}
	reg := string(regRaw)
	if !strings.Contains(reg, `"deadbeef": true`) {
		t.Fatalf("historical registry entry lost:\n%s", reg)
	}
	if !strings.Contains(reg, "registered by kern gen-plugin") {
		t.Fatalf("new registry entry not marked:\n%s", reg)
	}

	// Now fresh: --check passes (asset identical + hash registered).
	out = captureStdout(t, func() {
		runGenPlugin([]string{"--root", root, "--check"})
	})
	if !strings.Contains(out, "fresh") {
		t.Fatalf("expected fresh output, got %q", out)
	}

	// Idempotent: a second run adds nothing.
	out = captureStdout(t, func() {
		runGenPlugin([]string{"--root", root})
	})
	if !strings.Contains(out, "already registered") {
		t.Fatalf("second run must be idempotent, got %q", out)
	}
	again, err := os.ReadFile(filepath.Join(root, pluginRegistryRel))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(again), "registered by kern gen-plugin") != 1 {
		t.Fatalf("registry entry duplicated:\n%s", again)
	}
}

// TestGenPluginCheckFailsWhenHashUnregistered pins the second half of the
// freshness gate: a byte-identical asset whose hash is missing from the
// registry still fails --check, because the customized-vs-shipped detection
// depends on the registry.
func TestGenPluginCheckFailsWhenHashUnregistered(t *testing.T) {
	root := preparePluginRepo(t)
	// Sync the asset first (this also registers).
	captureStdout(t, func() { runGenPlugin([]string{"--root", root}) })
	// Strip the entry again, simulating a hand-copied asset: rewrite the
	// registry with only its historical entry.
	regPath := filepath.Join(root, pluginRegistryRel)
	cleaned := "package pluginhash\n\nvar shipped = map[string]bool{\n\t\"deadbeef\": true,\n}\n"
	if err := os.WriteFile(regPath, []byte(cleaned), 0o644); err != nil {
		t.Fatal(err)
	}
	expectExit(t, 1, func() {
		runGenPlugin([]string{"--root", root, "--check"})
	})
}

// TestGenPluginUsageRejectsUnknownFlag pins the usage-error path (exit 2).
func TestGenPluginUsageRejectsUnknownFlag(t *testing.T) {
	expectExit(t, 2, func() {
		runGenPlugin([]string{"--bogus"})
	})
}
