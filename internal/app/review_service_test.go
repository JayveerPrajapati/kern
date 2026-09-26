package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/index"
)

// Tests for the review-family orchestration moved out of cmd/kern (they
// exercise app-layer logic only, no CLI plumbing): stateless plan rendering,
// impact-callee annotation, and change-kind parsing.

func TestRenderStatelessPlanNetNewFeature(t *testing.T) {
	pkt := domain.ContextPacket{
		Symbols: []domain.Symbol{{Name: "RandomTest"}},
		Files:   []domain.File{{Path: "foo_test.go"}},
	}
	rendered := RenderStatelessPlan("Add REST endpoint for consumer lag", pkt, "../..")
	if strings.Contains(rendered, "RandomTest") || strings.Contains(rendered, "foo_test.go") {
		t.Errorf("rendered plan should not contain random test components for net-new feature, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "Scope: net-new feature") {
		t.Errorf("expected net-new feature scope, got:\n%s", rendered)
	}
}

// TestRenderStatelessPlanCLICommand: a net-new plan for a `kern <name>`
// command must name the concrete new file, the registration point, the test
// file, and the verification commands — not the old generic one-liner
// (dogfood finding F-5).
func TestRenderStatelessPlanCLICommand(t *testing.T) {
	rendered := RenderStatelessPlan("Add a `kern dogfood` CLI command that runs a self-check battery and prints a report", domain.ContextPacket{}, "../..")
	for _, want := range []string{
		"cmd/kern/cmd_dogfood.go",
		"cmd/kern/cmd_dogfood_test.go",
		"cmd/kern/dispatch_table.go",
		"go test ./cmd/kern/ -count=1",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("CLI plan missing %q, got:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "Implement the new feature according to specifications") {
		t.Errorf("CLI plan still contains the generic one-liner, got:\n%s", rendered)
	}
}

// TestRenderStatelessPlanCLICommandPlainPhrase: the same detection works
// without backticks ("kern dogfood command" phrasing).
func TestRenderStatelessPlanCLICommandPlainPhrase(t *testing.T) {
	rendered := RenderStatelessPlan("add a kern dogfood command", domain.ContextPacket{}, "../..")
	if !strings.Contains(rendered, "cmd/kern/cmd_dogfood.go") {
		t.Errorf("plain-phrase CLI plan missing new file, got:\n%s", rendered)
	}
}

// TestRenderStatelessPlanValidationNotDuplicated: packet validation items
// must render exactly once (under Tests), not twice (the old version repeated
// them under Implementation steps as well).
func TestRenderStatelessPlanValidationNotDuplicated(t *testing.T) {
	pkt := domain.ContextPacket{
		RequiredValidation: []string{"write and run unit tests for kern", "build verification"},
	}
	rendered := RenderStatelessPlan("Add a status endpoint", pkt, "../..")
	for _, v := range pkt.RequiredValidation {
		if n := strings.Count(rendered, v); n != 1 {
			t.Errorf("validation item %q appears %d times, want exactly 1:\n%s", v, n, rendered)
		}
	}
}

// TestRenderStatelessPlanAdaptiveForeignRepo: a target repo WITHOUT the kern
// layout (no cmd/kern/, no CHANGELOG.md, no root go.mod) must get generic
// implement/test/document steps — kern's own CLI conventions must never leak
// into a foreign codebase (dogfooding F6).
func TestRenderStatelessPlanAdaptiveForeignRepo(t *testing.T) {
	root := t.TempDir()
	rendered := RenderStatelessPlan("Add a `kern dogfood` CLI command", domain.ContextPacket{}, root)
	for _, leak := range []string{
		"cmd/kern/cmd_dogfood.go",
		"cmd/kern/dispatch_table.go",
		"CHANGELOG.md [Unreleased]",
		"go test ./cmd/kern/ -count=1",
		"CLI surface lives in cmd/kern/",
	} {
		if strings.Contains(rendered, leak) {
			t.Errorf("plan for a foreign repo leaks kern convention %q, got:\n%s", leak, rendered)
		}
	}
	for _, want := range []string{
		"Implement the feature",
		"Add unit tests",
		"run the project's own build and test commands",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("foreign-repo plan missing generic step %q, got:\n%s", want, rendered)
		}
	}
}

// TestRenderStatelessPlanAdaptiveKernLayout: a target repo WITH cmd/kern/,
// CHANGELOG.md and a root go.mod gets the concrete kern CLI steps (F6).
func TestRenderStatelessPlanAdaptiveKernLayout(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "cmd", "kern"), 0o755); err != nil {
		t.Fatal(err)
	}
	for rel, content := range map[string]string{
		"go.mod":       "module fixture\n\ngo 1.20\n",
		"CHANGELOG.md": "# Changelog\n",
	} {
		if err := os.WriteFile(filepath.Join(root, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	rendered := RenderStatelessPlan("Add a `kern dogfood` CLI command", domain.ContextPacket{}, root)
	for _, want := range []string{
		"cmd/kern/cmd_dogfood.go",
		"cmd/kern/dispatch_table.go",
		"CHANGELOG.md [Unreleased]",
		"go test ./cmd/kern/ -count=1",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("kern-layout plan missing %q, got:\n%s", want, rendered)
		}
	}
}

// TestRenderStatelessPlanAdaptiveNoChangelog: a repo with cmd/kern/ and a root
// go.mod but NO CHANGELOG.md must not hardcode "CHANGELOG.md [Unreleased]" —
// the changelog step becomes generic (F6).
func TestRenderStatelessPlanAdaptiveNoChangelog(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "cmd", "kern"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module fixture\n\ngo 1.20\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rendered := RenderStatelessPlan("Add a feature", domain.ContextPacket{}, root)
	if strings.Contains(rendered, "CHANGELOG.md [Unreleased]") {
		t.Errorf("plan without a CHANGELOG.md must not hardcode [Unreleased], got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "release notes or changelog") {
		t.Errorf("plan without a CHANGELOG.md should point at release notes, got:\n%s", rendered)
	}
}

func TestAnnotateImpactCallees(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module demo\n\ngo 1.23\n",
		"svc/svc.go": `package svc
import "demo/repo"
func FindUser(id int) string { return repo.Query(id) }
func legacyPrint() string { return "x" }
`,
		"repo/repo.go": `package repo
func Query(id int) string { return fmtInt(id) }
func fmtInt(i int) string { return itoa(i) }
func itoa(i int) string { return "" }
`,
	}
	for rel, content := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	_ = ix.Save()
	text := `IMPACT for: FindUser
Risk: medium
What it calls: 3
  - Query
  - fmtInt
  - itoa
Tests that cover it (direct callers + same-package): 0
`
	got := AnnotateImpactCallees(text, "FindUser", dir)
	for _, want := range []string{"- Query (direct)", "- fmtInt (transitive)", "- itoa (transitive)"} {
		if !strings.Contains(got, want) {
			t.Errorf("annotated text missing %q, got:\n%s", want, got)
		}
	}
	if strings.Contains(got, "IMPACT for: FindUser\nIMPACT for:") {
		t.Errorf("AnnotateImpactCallees must not duplicate the header, got:\n%s", got)
	}
	// Unrelated sections and unknown targets stay untouched.
	if !strings.Contains(got, "Tests that cover it (direct callers + same-package): 0") {
		t.Errorf("unrelated section was altered:\n%s", got)
	}
	if got := AnnotateImpactCallees(text, "NoSuchSymbol", dir); got != text {
		t.Errorf("unknown target must return text unchanged")
	}
}

// TestSimpleSymName covers the qualified/bare name normalization used by the
// impact callee annotation.
func TestSimpleSymName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"repo.Query", "Query"},
		{"FindUser", "FindUser"},
		{"TaskService.Deploy", "Deploy"},
		{"", ""},
	}
	for _, c := range cases {
		if got := simpleSymName(c.in); got != c.want {
			t.Errorf("simpleSymName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestParseChangeKind pins the free-text change-kind derivation used by
// `kern what-if` (verb-first wording maps to the historical kind).
func TestParseChangeKind(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"remove WriteFileAtomic", "remove_symbol"},
		{"delete Client", "remove_symbol"},
		{"drop legacy shim", "remove_symbol"},
		{"change signature of Query", "change_signature"},
		{"modify Handle", "change_signature"},
		{"refactor dispatch", "change_signature"},
		{"add NewServer", "add_symbol"},
		{"create endpoint", "add_symbol"},
		{"rename Foo to Bar", "rename_symbol"},
		{"move util to lib", "move_module"},
		{"split the monolith", "split_service"},
		{"frobnicate the widget", "remove_symbol"},
		{"", "remove_symbol"},
	}
	for _, c := range cases {
		if got := ParseChangeKind(c.in); string(got) != c.want {
			t.Errorf("ParseChangeKind(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
