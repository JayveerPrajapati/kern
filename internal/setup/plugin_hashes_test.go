package setup

import (
	"testing"
)

// TestShippedPluginHashRegistryCurrent fails whenever the embedded plugin
// changes without appending its new hash to shippedPluginHashes: without the
// registry entry, a future setup run would classify the CURRENT deployment as
// "customized" and refuse to update it (the F-DR1 bug shape).
func TestShippedPluginHashRegistryCurrent(t *testing.T) {
	src, err := pluginFS.ReadFile("assets/plugin/kern.ts")
	if err != nil {
		t.Fatal(err)
	}
	h := pluginHash(src)
	if !shippedPluginHashes[h] {
		t.Fatalf("the current embedded plugin (sha256 %s) is not in shippedPluginHashes — append it to internal/setup/plugin_hashes.go (the registry that lets setup distinguish kern-deployed copies from user edits)", h)
	}
	// Sanity: the registry has enough history to be meaningful.
	if len(shippedPluginHashes) < 10 {
		t.Fatalf("shippedPluginHashes looks truncated: %d entries", len(shippedPluginHashes))
	}
}
