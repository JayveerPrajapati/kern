package app

import (
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// TestRenderImpactTextWarnsOnUnresolvedTarget pins F-2: an impact report with
// no callers, callees, or tests must carry an explicit WARN so a silently
// empty blast radius cannot be mistaken for a low-risk leaf symbol.
func TestRenderImpactTextWarnsOnUnresolvedTarget(t *testing.T) {
	out := renderImpactText(domain.ImpactReport{Target: "dispatch"})
	if !strings.Contains(out, "WARN: no callers, callees, or tests resolved") {
		t.Fatalf("expected WARN for unresolved target, got:\n%s", out)
	}
}

// TestRenderImpactTextNoWarnWhenResolved pins the inverse: a report with real
// callers/callees stays clean.
func TestRenderImpactTextNoWarnWhenResolved(t *testing.T) {
	out := renderImpactText(domain.ImpactReport{
		Target:            "dispatchCommand",
		WhoCalls:          []string{"main"},
		WhatItCalls:       []string{"usage"},
		TestsCover:        []string{"TestDispatchCommandUnknownExits2"},
		ArchitectureRules: []string{"pol-source-write"},
	})
	if strings.Contains(out, "WARN:") {
		t.Fatalf("unexpected WARN for resolved target, got:\n%s", out)
	}
}
