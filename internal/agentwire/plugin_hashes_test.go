package agentwire

import (
	"testing"

	"github.com/JayveerPrajapati/kern/internal/pluginhash"
)

// TestShippedPluginHashRegistryCurrent fails whenever the embedded plugin
// changes without appending its new hash to the pluginhash registry: without
// the registry entry, a future setup run would classify the CURRENT
// deployment as "customized" and refresh it under a spurious backup instead
// of recognizing it as kern's own older deployment (the F-DR1 bug shape).
func TestShippedPluginHashRegistryCurrent(t *testing.T) {
	src, err := pluginFS.ReadFile("assets/plugin/kern.ts")
	if err != nil {
		t.Fatal(err)
	}
	if !pluginhash.IsShipped(src) {
		t.Fatalf("the current embedded plugin (sha256 %s) is not in the pluginhash registry — append it to internal/pluginhash/pluginhash.go (the registry that lets setup distinguish kern-deployed copies from user edits)", pluginhash.Hash(src))
	}
}
