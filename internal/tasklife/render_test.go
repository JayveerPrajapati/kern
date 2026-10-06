package tasklife

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
	out := RenderWhatIfText(whatif.RemoveSymbol, "dispatchCommand", "dispatchCommand", imp)
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

// TestRenderImpactCoveringTestsHeaderCapped pins V3: when the covering-tests
// list overflows maxCoveringTests, the header must state the DISPLAYED count
// with the real total alongside, so it never contradicts the "+N more (use
// --json for full list)" overflow line below it. The label names the ranked
// tiers only — same-package noise is a separate count line (Fix 1).
func TestRenderImpactCoveringTestsHeaderCapped(t *testing.T) {
	var tests []string
	for i := 0; i < 383; i++ {
		tests = append(tests, fmt.Sprintf("TestSym%03d", i))
	}
	out := renderImpactText(domain.ImpactReport{
		Target:                "shortFingerprint",
		WhoCalls:              []string{"caller1"},
		TestsCover:            tests,
		TestsCoverSamePackage: 431,
	})
	if !strings.Contains(out, "Tests that cover it (file-paired + name-matched + direct callers): 10 shown of 383") {
		t.Fatalf("capped header must show displayed count + real total, got:\n%s", out)
	}
	if !strings.Contains(out, "... +373 more (use --json for full list)") {
		t.Fatalf("expected +373 overflow line, got:\n%s", out)
	}
	// 10 shown + 373 hidden must equal the total the header reports.
	if !strings.Contains(out, "Tests that cover it (file-paired + name-matched + direct callers): 10 shown of 383") {
		t.Fatalf("header math is inconsistent, got:\n%s", out)
	}
	if strings.Contains(out, "Tests that cover it (file-paired + name-matched + direct callers): 383") {
		t.Fatalf("header must not lead with the hidden total, got:\n%s", out)
	}
	// The same-package remainder is a count-only line, never a list (Fix 1).
	if !strings.Contains(out, "same-package tests not shown (low relevance): 431") {
		t.Fatalf("expected the same-package count-only line, got:\n%s", out)
	}
}

// TestRenderImpactCoveringTestsHeaderUncapped pins the inverse: a report with
// few tests keeps the plain exact-count header with no "shown of" wording.
func TestRenderImpactCoveringTestsHeaderUncapped(t *testing.T) {
	out := renderImpactText(domain.ImpactReport{
		Target:     "dispatchCommand",
		WhoCalls:   []string{"main"},
		TestsCover: []string{"TestDispatchCommandUnknownExits2", "TestDispatchCommandHelp", "TestDispatchCommandRun"},
	})
	if !strings.Contains(out, "Tests that cover it (file-paired + name-matched + direct callers): 3") {
		t.Fatalf("uncapped header must show the exact count, got:\n%s", out)
	}
	if strings.Contains(out, "shown of") {
		t.Fatalf("uncapped header must not use 'shown of' wording, got:\n%s", out)
	}
	if strings.Contains(out, "more (use --json") {
		t.Fatalf("uncapped report must not truncate, got:\n%s", out)
	}
	if strings.Contains(out, "same-package tests not shown") {
		t.Fatalf("no same-package line when TestsCoverSamePackage is 0, got:\n%s", out)
	}
}

// TestRenderImpactCoveringTestsTiersShownAsList pins Fix 1's rendering: the
// ranked covering tests (file-paired, name-matched, direct callers) render as
// the list, in tier order, and the same-package remainder appears only as a
// count line — never as list entries implying the whole package covers the
// symbol.
func TestRenderImpactCoveringTestsTiersShownAsList(t *testing.T) {
	out := renderImpactText(domain.ImpactReport{
		Target:   "runTaint",
		WhoCalls: []string{"commandTable"},
		TestsCover: []string{
			"TestParseTaintRange", // file-paired + name-matched (cmd_security_test.go)
			"TestTaintHelper",     // name-matched (taint token)
			"t.TestDirectCaller",  // direct caller
		},
		TestsCoverSamePackage: 418,
	})
	if !strings.Contains(out, "Tests that cover it (file-paired + name-matched + direct callers): 3") {
		t.Fatalf("expected the ranked-tiers header, got:\n%s", out)
	}
	for _, want := range []string{"TestParseTaintRange", "TestTaintHelper", "t.TestDirectCaller"} {
		if !strings.Contains(out, "- "+want) {
			t.Fatalf("ranked covering test %q must be listed, got:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "same-package tests not shown (low relevance): 418") {
		t.Fatalf("expected the same-package count-only line, got:\n%s", out)
	}
	for _, hidden := range []string{"TestAcquireLockWithWaitAcquiresAfterRelease", "TestAgentMessageKnownRecipientQueues"} {
		if strings.Contains(out, hidden) {
			t.Fatalf("same-package noise %q must not be listed as a covering test, got:\n%s", hidden, out)
		}
	}
}

// TestRenderImpactAffectedFiles pins Fix 2: the impact report lists the
// files the blast radius touches (deduped, capped at maxImpactListItems).
func TestRenderImpactAffectedFiles(t *testing.T) {
	out := renderImpactText(domain.ImpactReport{
		Target:   "runTaint",
		WhoCalls: []string{"commandTable"},
		Files:    []string{"cmd/kern/cmd_security.go", "cmd/kern/dispatch_table.go"},
	})
	if !strings.Contains(out, "Affected files: 2") {
		t.Fatalf("expected Affected files header, got:\n%s", out)
	}
	for _, want := range []string{"cmd/kern/cmd_security.go", "cmd/kern/dispatch_table.go"} {
		if !strings.Contains(out, "- "+want) {
			t.Fatalf("affected file %q must be listed, got:\n%s", want, out)
		}
	}
}

// TestRenderWhatIfSamePackageTestsCountOnly pins Fix 1 on the what-if render:
// the same-package remainder renders as a count-only line.
func TestRenderWhatIfSamePackageTestsCountOnly(t *testing.T) {
	imp := whatif.Impact{
		Change:           whatif.Change{Kind: whatif.RemoveSymbol, Target: "runTaint"},
		Risk:             "medium",
		Recommendation:   "check callers",
		Tests:            []string{"TestParseTaintRange"},
		TestsSamePackage: 431,
	}
	out := RenderWhatIfText(whatif.RemoveSymbol, "runTaint", "runTaint", imp)
	if !strings.Contains(out, "tests: 1") {
		t.Fatalf("expected ranked tests count, got:\n%s", out)
	}
	if !strings.Contains(out, "same-package tests not shown (low relevance): 431") {
		t.Fatalf("expected the same-package count-only line, got:\n%s", out)
	}
}

// TestRenderImpactWhatCallsShowsTransitiveContext pins V4: when RiskDetail is
// the transitive-dependents count that drove the risk tier and it exceeds the
// direct caller count, the "What calls this" line must surface both so "Risk:
// medium (3 transitive dependents)" next to "What calls this: 1" reads as a
// direct→transitive relationship, not a contradiction.
func TestRenderImpactWhatCallsShowsTransitiveContext(t *testing.T) {
	out := renderImpactText(domain.ImpactReport{
		Target:     "shortFingerprint",
		WhoCalls:   []string{"caller1"},
		Risk:       "medium",
		RiskDetail: "3 transitive dependents",
	})
	if !strings.Contains(out, "What calls this: 1 (graph nodes; 3 transitive dependents)") {
		t.Fatalf("expected direct+transitive counts on the What calls line, got:\n%s", out)
	}
}

// TestRenderImpactWhatCallsNoTransitiveContext pins the boundaries: the
// transitive context is only appended when RiskDetail is the
// transitive-dependents form. Fallback details ("N direct callers",
// "N services depend on it") and an empty detail keep the plain line.
func TestRenderImpactWhatCallsNoTransitiveContext(t *testing.T) {
	cases := []struct {
		name     string
		detail   string
		direct   []string
		wantLine string
	}{
		{"empty detail", "", []string{"caller1"}, "What calls this: 1 (graph nodes)"},
		{"fallback direct callers", "2 direct callers", []string{"caller1", "caller2"}, "What calls this: 2 (graph nodes)"},
		{"fallback services", "2 services depend on it", []string{"caller1"}, "What calls this: 1 (graph nodes)"},
		{"transitive equal to direct", "1 transitive dependents", []string{"caller1"}, "What calls this: 1 (graph nodes)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := renderImpactText(domain.ImpactReport{
				Target:     "sym",
				WhoCalls:   tc.direct,
				Risk:       "medium",
				RiskDetail: tc.detail,
			})
			if !strings.Contains(out, tc.wantLine) {
				t.Fatalf("expected line %q, got:\n%s", tc.wantLine, out)
			}
			if strings.Contains(out, "graph nodes; ") {
				t.Fatalf("unexpected transitive context for detail %q, got:\n%s", tc.detail, out)
			}
		})
	}
}
