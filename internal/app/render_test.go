package app

import (
	"fmt"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/whatif"
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

// TestRenderImpactTruncatesLongLists pins P1-4: long sections collapse to
// top-20 + "+N more" so hubs like loadOrBuild (1274 transitive) stay readable.
// Header counts stay exact; full data is preserved in ImpactReport for --json.
func TestRenderImpactTruncatesLongLists(t *testing.T) {
	var callers []string
	for i := 0; i < 30; i++ {
		callers = append(callers, fmt.Sprintf("caller%02d", i))
	}
	out := renderImpactText(domain.ImpactReport{
		Target:      "hub",
		WhoCalls:    callers,
		WhatItCalls: []string{"direct"},
		TestsCover:  []string{"TestHub"},
	})
	if !strings.Contains(out, "What calls this: 30") {
		t.Fatalf("header count must stay exact, got:\n%s", out)
	}
	if !strings.Contains(out, "+10 more (use --json for full list)") {
		t.Fatalf("expected +10 more overflow line, got:\n%s", out)
	}
	if strings.Contains(out, "caller29") {
		t.Fatalf("entries past the top-20 must be truncated, got:\n%s", out)
	}
	if n := strings.Count(out, "\n"); n > 40 {
		t.Fatalf("truncated report = %d lines, want <=40, got:\n%s", n, out)
	}
}

// TestRenderWhatItCallsCollapsesStdlib pins P1-4 stdlib collapse: project
// calls render individually while strings/os/fmt fan-out collapses to one
// summary line with per-package counts.
func TestRenderWhatItCallsCollapsesStdlib(t *testing.T) {
	out := renderImpactText(domain.ImpactReport{
		Target:      "m",
		WhoCalls:    []string{"main"},
		WhatItCalls: []string{"LoadOrBuild", "MyHelper", "strings.Contains", "strings.Split", "os.Open", "fmt.Errorf"},
		TestsCover:  []string{"TestM"},
	})
	for _, want := range []string{"LoadOrBuild", "MyHelper"} {
		if !strings.Contains(out, "- "+want) {
			t.Fatalf("expected project call %q shown, got:\n%s", want, out)
		}
	}
	for _, hidden := range []string{"strings.Contains", "os.Open", "fmt.Errorf"} {
		if strings.Contains(out, "- "+hidden) {
			t.Fatalf("stdlib call %q must collapse, not list, got:\n%s", hidden, out)
		}
	}
	if !strings.Contains(out, "stdlib: 4 calls collapsed") {
		t.Fatalf("expected stdlib summary line, got:\n%s", out)
	}
	if !strings.Contains(out, "strings (2)") {
		t.Fatalf("expected per-package top counts, got:\n%s", out)
	}
	if !strings.Contains(out, "What it calls: 6") {
		t.Fatalf("header count must stay exact (6), got:\n%s", out)
	}
}

// TestStdlibPkgOfKeepsProjectCalls pins the collapse boundary: internal
// packages and receiver-style names never collapse, only real stdlib pkgs.
func TestStdlibPkgOfKeepsProjectCalls(t *testing.T) {
	for _, name := range []string{"index.Load", "app.New", "AuditLog.mu.Lock", "Buffer.Write", "bare"} {
		if pkg, ok := stdlibPkgOf(name); ok {
			t.Fatalf("stdlibPkgOf(%q) = (%q, true), want false — project calls must never collapse", name, pkg)
		}
	}
	for name, wantPkg := range map[string]string{
		"strings.Contains": "strings", "os.Open": "os", "fmt.Errorf": "fmt",
		"bytes.Join": "bytes", "filepath.WalkDir": "filepath", "time.Since": "time",
	} {
		if pkg, ok := stdlibPkgOf(name); !ok || pkg != wantPkg {
			t.Fatalf("stdlibPkgOf(%q) = (%q, %v), want (%q, true)", name, pkg, ok, wantPkg)
		}
	}
}

// TestRenderImpactSmallReportUnchanged pins the inverse: small reports with no
// stdlib render fully with no overflow or collapse lines.
func TestRenderImpactSmallReportUnchanged(t *testing.T) {
	out := renderImpactText(domain.ImpactReport{
		Target:      "dispatchCommand",
		WhoCalls:    []string{"main"},
		WhatItCalls: []string{"usage"},
		TestsCover:  []string{"TestDispatchCommandUnknownExits2"},
	})
	if strings.Contains(out, "more (use --json") {
		t.Fatalf("small report must not truncate, got:\n%s", out)
	}
	if strings.Contains(out, "stdlib:") {
		t.Fatalf("report without stdlib must not collapse, got:\n%s", out)
	}
}

// TestRenderImpactAppendsEvidence pins P2 anchors on impact: a populated
// Evidence field renders as the trailing line; empty stays absent.
func TestRenderImpactAppendsEvidence(t *testing.T) {
	out := renderImpactText(domain.ImpactReport{
		Target:      "dispatchCommand",
		WhoCalls:    []string{"main"},
		WhatItCalls: []string{"usage"},
		TestsCover:  []string{"TestX"},
		Evidence:    "evidence: cmd/kern/dispatch.go:126 evidence-sha256:0123456789abcdef",
	})
	if !strings.Contains(out, "evidence: cmd/kern/dispatch.go:126 evidence-sha256:0123456789abcdef") {
		t.Fatalf("expected anchor line, got:\n%s", out)
	}
	plain := renderImpactText(domain.ImpactReport{Target: "dispatchCommand", WhoCalls: []string{"main"}, WhatItCalls: []string{"usage"}, TestsCover: []string{"TestX"}})
	if strings.Contains(plain, "evidence:") {
		t.Fatalf("empty Evidence must not render, got:\n%s", plain)
	}
}

// TestRenderWhatIfAppendsEvidence pins P2 anchors on what_if output.
func TestRenderWhatIfAppendsEvidence(t *testing.T) {
	imp := whatif.Impact{
		Change:         whatif.Change{Kind: whatif.RemoveSymbol, Target: "dispatchCommand"},
		Risk:           "medium",
		Recommendation: "check callers",
		Evidence:       "evidence: cmd/kern/dispatch.go:126 evidence-sha256:0123456789abcdef",
	}
	out := renderWhatIfText(whatif.RemoveSymbol, "dispatchCommand", "dispatchCommand", imp)
	if !strings.Contains(out, "evidence: cmd/kern/dispatch.go:126 evidence-sha256:0123456789abcdef") {
		t.Fatalf("expected anchor line, got:\n%s", out)
	}
}

func TestNodeNameSymbolQualification(t *testing.T) {
	node1 := domain.Node{
		Symbol: &domain.Symbol{
			Name:     "setUp",
			Receiver: "UserServiceTest",
		},
	}
	if got := nodeName(node1); got != "UserServiceTest.setUp" {
		t.Errorf("nodeName(node1) = %q, want %q", got, "UserServiceTest.setUp")
	}

	node2 := domain.Node{
		Symbol: &domain.Symbol{
			Name: "setUp",
			File: "src/test/java/AuthTest.java",
		},
	}
	if got := nodeName(node2); got != "AuthTest.java:setUp" {
		t.Errorf("nodeName(node2) = %q, want %q", got, "AuthTest.java:setUp")
	}
}

func TestRenderImpactArchitectureRules(t *testing.T) {
	out := renderImpactText(domain.ImpactReport{
		Target:            "UserService",
		WhoCalls:          []string{"Controller"},
		ArchitectureRules: []string{"no-circular-deps", "layer-isolation"},
	})
	if !strings.Contains(out, "Architecture rules: 2") {
		t.Fatalf("expected Architecture rules count 2, got:\n%s", out)
	}
	if !strings.Contains(out, "- no-circular-deps") || !strings.Contains(out, "- layer-isolation") {
		t.Fatalf("missing expected architecture rule names in:\n%s", out)
	}
}
