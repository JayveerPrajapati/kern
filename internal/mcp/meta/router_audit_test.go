package meta_test

import (
	"testing"

	"github.com/JayveerPrajapati/kern/internal/mcp/meta"
)

// TestClassifyMetaRequest_RouterAudit pins the 2026-10-01 router keyword
// audit: every keyword added to close a plural/synonym/variant gap of the
// "memories" bug class, plus regression pins for the reordered
// synthesize/heal/repair/validate region (a request naming both a
// repair/heal intent and a build/test word must route by the INTENT, not
// by the generic re-check arm).
func TestClassifyMetaRequest_RouterAudit(t *testing.T) {
	cases := []classifyCase{
		// Memory arm: plurals that HasWord's word boundary skipped before.
		{"lessons", "show me the lessons learned", "kern_memory", map[string]string{"action": "recall"}},
		{"learnings", "what learnings do we have from past sessions", "kern_memory", map[string]string{"action": "recall"}},
		{"memory_regression", "what memories do we have about auth", "kern_memory", map[string]string{"action": "recall"}},

		// Heal: the auto-repair tool had NO keyword arm at all.
		{"heal_word", "heal the repo", "kern_heal", nil},
		{"failing_build", "the failing build needs fixing", "kern_heal", nil},
		{"failing_test", "the failing test is still red", "kern_heal", nil},
		{"fix_the_tests", "fix the tests please", "kern_heal", nil},

		// Repair diagnostics: "compiler error" was matched literally; the
		// common variants fell through to the generic build/test arm.
		{"compile_error", "compile error: undefined: Foo", "kern_repair", map[string]string{"action": "diagnostics"}},
		{"compilation_error", "compilation error in ci", "kern_repair", map[string]string{"action": "diagnostics"}},
		{"build_error", "build error in the sandbox leg", "kern_repair", map[string]string{"action": "diagnostics"}},
		{"repair_word", "get repair diagnostics for this failure", "kern_repair", map[string]string{"action": "diagnostics"}},

		// Synthesize-test: generation phrasings must beat the test-runner arm,
		// and the arm extracts target/file args the handler requires.
		{"synthesize", "synthesize a test for RunDoContext", "kern_synthesize_test", map[string]string{"target": "RunDoContext"}},
		{"write_a_test", "write a test for Add in calc.go", "kern_synthesize_test", map[string]string{"target": "Add", "file": "calc.go"}},
		{"generate_a_test", "generate a test for the edge case", "kern_synthesize_test", nil},

		// Validate regression: plain build/test/lint phrasings unchanged.
		{"validate_regression", "build and test the project", "kern_validate", nil},
		{"run_tests_regression", "run the tests", "kern_validate", nil},
		{"validate_word", "validate the whole project", "kern_validate", nil},

		// Project utilities: singular/plural and synonym variants.
		{"onboarding", "onboarding for this repo", "kern_buddy", nil},
		{"doctor", "run doctor on the project", "kern_health", nil},
		{"token_usage", "show token usage", "kern_stats", nil},
		{"saved", "how many tokens have we saved", "kern_stats", nil},
		{"commit_msg", "draft a commit msg for the staged changes", "kern_commitmsg", nil},
		{"doc_singular", "search the doc for setup instructions", "kern_doc", map[string]string{"action": "search"}},

		// Graph/arch lenses.
		{"most_changed", "show the most changed files", "kern_churn", nil},
		{"untested", "which functions are untested", "kern_test_gaps", nil},
		{"why_do", "why do we use SQLite here", "kern_why", nil},
		{"blast_radius", "blast radius of NewServer", "kern_probe", nil},
		{"cve_id", "check our deps for cve-2024-1234", "kern_security", nil},

		// Exec: "execute" and script-article variants.
		{"execute_word", "execute this script for me", "kern_exec", nil},
	}
	for _, tc := range cases {
		tool, args := meta.ClassifyMetaRequest(tc.request)
		if tool != tc.wantTool {
			t.Errorf("%s: ClassifyMetaRequest(%q) = %q, want %q", tc.name, tc.request, tool, tc.wantTool)
			continue
		}
		for k, want := range tc.wantArgs {
			if got, _ := args[k].(string); got != want {
				t.Errorf("%s: args[%s] = %q, want %q", tc.name, k, got, want)
			}
		}
	}
}
