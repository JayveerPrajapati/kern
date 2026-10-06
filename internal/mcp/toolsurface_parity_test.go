package mcp

import (
	"testing"

	"github.com/JayveerPrajapati/kern/internal/mcp/toolsurface"
)

// TestDefaultToolsMatchToolsurface pins toolpolicy.go's defaultTools to the
// shared leaf definition (internal/mcp/toolsurface) — the two must name the
// same surface (deep-dive A1: kern_run recommendations annotate against the
// leaf, advertisement filters against this map; drift would make the
// annotation lie).
func TestDefaultToolsMatchToolsurface(t *testing.T) {
	if len(defaultTools) != 6 {
		t.Fatalf("defaultTools size = %d, want 6", len(defaultTools))
	}
	if len(toolsurface.Default) != len(defaultTools) {
		t.Fatalf("toolsurface.Default size = %d, defaultTools size = %d — surface drift", len(toolsurface.Default), len(defaultTools))
	}
	for _, n := range toolsurface.Default {
		if !defaultTools[n] {
			t.Errorf("toolsurface.Default names %q but toolpolicy.defaultTools does not", n)
		}
	}
}
