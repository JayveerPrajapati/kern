package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// preparePluginRepo builds a scratch repo-root layout with the canonical
// plugin copy and a deliberately stale embedded asset, returning root.
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
	return root
}

// TestGenPluginCheckFreshAndStale pins the --check gate: a fresh (byte-
// identical) asset reports fresh and exits 0; a stale asset fails loudly and
// is NOT rewritten by --check.
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

	// Regenerate: canonical is copied byte-identical into the asset.
	out := captureStdout(t, func() {
		runGenPlugin([]string{"--root", root})
	})
	if !strings.Contains(out, "regenerated") {
		t.Fatalf("expected regenerated output, got %q", out)
	}
	got, err := os.ReadFile(asset)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "export const plugin = 1;\n" {
		t.Fatalf("asset not byte-identical after regen: %q", got)
	}

	// Now fresh: --check passes.
	out = captureStdout(t, func() {
		runGenPlugin([]string{"--root", root, "--check"})
	})
	if !strings.Contains(out, "fresh") {
		t.Fatalf("expected fresh output, got %q", out)
	}
}

// TestGenPluginUsageRejectsUnknownFlag pins the usage-error path (exit 2).
func TestGenPluginUsageRejectsUnknownFlag(t *testing.T) {
	expectExit(t, 2, func() {
		runGenPlugin([]string{"--bogus"})
	})
}
